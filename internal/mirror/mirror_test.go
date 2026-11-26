package mirror

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-github/v90/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/model"
)

const (
	runsPath = "/repos/example/mirror/actions/workflows/ci-build.yaml/runs"
	jobsPath = "/repos/example/mirror/actions/runs/10/jobs"
	logPath  = "/repos/example/mirror/actions/jobs/20/logs"
	fixHead  = "35090fbba70cd7a1c84ec8c3d375c4883dbfd71d"
	baseSHA  = "ffcf83133a0000000000000000000000000000ff"
)

// fakeMirror serves the mirror endpoints LatestCI and InFlight use. runs is the
// workflow-runs JSON; logLine is what the "Get changed files" job's log contains.
type mirrorHits struct{ tokenOnLogHost, jobs int }

func fakeMirror(t *testing.T, runs, logLine string) (*Facts, *mirrorHits) {
	return fakeMirrorLog(t, runs, logLine, http.StatusOK)
}

func fakeMirrorLog(t *testing.T, runs, logLine string, logStatus int) (*Facts, *mirrorHits) {
	t.Helper()
	hits := &mirrorHits{}
	mux := http.NewServeMux()
	mux.HandleFunc(runsPath, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("status") {
		case "queued":
			_, _ = fmt.Fprint(w, `{"total_count":1,"workflow_runs":[]}`)
		case "in_progress":
			_, _ = fmt.Fprint(w, `{"total_count":2,"workflow_runs":[]}`)
		default:
			assert.Equal(t, "ghsa-abcd-efgh-ijkl/main-pr1", r.URL.Query().Get("branch"))
			assert.Equal(t, "pull_request", r.URL.Query().Get("event"))
			_, _ = fmt.Fprint(w, runs)
		}
	})
	mux.HandleFunc(jobsPath, func(w http.ResponseWriter, _ *http.Request) {
		hits.jobs++
		_, _ = fmt.Fprint(w, `{"total_count":3,"jobs":[
			{"id":20,"name":"Get changed files","status":"completed","conclusion":"success"},
			{"id":21,"name":"E2E UI (root)","status":"completed","conclusion":"skipped"},
			{"id":22,"name":"Codegen","status":"completed","conclusion":"skipped"}]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc(logPath, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/blob/20", http.StatusFound)
	})
	mux.HandleFunc("/blob/20", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			hits.tokenOnLogHost++
		}
		if logStatus != http.StatusOK {
			w.WriteHeader(logStatus)
			return
		}
		_, _ = fmt.Fprint(w, logLine)
	})
	base := srv.URL + "/"
	c, err := gh.NewClient("tok", github.WithURLs(&base, &base))
	require.NoError(t, err)
	return NewFacts(c, srv.Client(), gh.Repo{Owner: "example", Name: "mirror"}), hits
}

func runJSON(status, conclusion string) string {
	return fmt.Sprintf(`{"total_count":1,"workflow_runs":[{"id":10,"head_sha":%q,"status":%q,"conclusion":%q,
		"html_url":"https://github.com/example/mirror/actions/runs/10",
		"run_started_at":"2026-11-26T11:20:00Z","updated_at":"2026-11-26T12:00:00Z"}]}`,
		fixHead, status, conclusion)
}

func TestLatestCI_GreenWithSkippedJobs(t *testing.T) {
	f, hits := fakeMirror(t, runJSON("completed", "success"), "HEAD is now at abc Merge "+fixHead+" into ffcf83133a\n")
	ci, err := f.LatestCI(t.Context(), "ghsa-abcd-efgh-ijkl/main-pr1", model.RecordedBase{SHA: "ffcf83133a", RunID: 10}, baseSHA)
	require.NoError(t, err)
	assert.Equal(t, model.CIGreen, ci.State)
	assert.Equal(t, fixHead, ci.RunHeadSHA)
	assert.Equal(t, "ffcf83133a", ci.MergedBaseSHA)
	assert.Equal(t, baseSHA, ci.CurrentBaseSHA)
	assert.Equal(t, []string{"E2E UI (root)"}, ci.SkippedTestJobs, "Codegen is not a test job")
	assert.Zero(t, hits.tokenOnLogHost, "the token must not be sent to the log download host")
	assert.Equal(t, int64(10), ci.RunID)
	assert.Equal(t, time.Date(2026, 11, 26, 11, 20, 0, 0, time.UTC), ci.StartedAt.UTC())
}

func TestLatestCI_UnconfirmedWithoutMergeLine(t *testing.T) {
	f, _ := fakeMirror(t, runJSON("completed", "success"), "no merge here\n")
	ci, err := f.LatestCI(t.Context(), "ghsa-abcd-efgh-ijkl/main-pr1", model.RecordedBase{}, baseSHA)
	require.NoError(t, err)
	assert.Equal(t, model.CIUnconfirmed, ci.State)
}

func TestLatestCI_RunningSkipsTheLog(t *testing.T) {
	f, _ := fakeMirror(t, runJSON("in_progress", ""), "")
	ci, err := f.LatestCI(t.Context(), "ghsa-abcd-efgh-ijkl/main-pr1", model.RecordedBase{}, baseSHA)
	require.NoError(t, err)
	assert.Equal(t, model.CIRunning, ci.State)
}

func TestLatestCI_Never(t *testing.T) {
	f, _ := fakeMirror(t, `{"total_count":0,"workflow_runs":[]}`, "")
	ci, err := f.LatestCI(t.Context(), "ghsa-abcd-efgh-ijkl/main-pr1", model.RecordedBase{}, baseSHA)
	require.NoError(t, err)
	assert.Equal(t, model.CINever, ci.State)
	assert.Equal(t, baseSHA, ci.CurrentBaseSHA)
}

func TestInFlight_CountsQueuedAndInProgress(t *testing.T) {
	f, _ := fakeMirror(t, "", "")
	n, err := f.InFlight(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}

// Review #10: an expired (410) or missing log means the merged base is unknown,
// not a failed fetch.
func TestLatestCI_ExpiredLogIsUnconfirmed(t *testing.T) {
	f, _ := fakeMirrorLog(t, runJSON("completed", "success"), "", http.StatusGone)
	ci, err := f.LatestCI(t.Context(), "ghsa-abcd-efgh-ijkl/main-pr1", model.RecordedBase{}, baseSHA)
	require.NoError(t, err)
	assert.Equal(t, model.CIUnconfirmed, ci.State)
}

// Review #5: a base the tool recorded for its own run doesn't apply to a later
// run someone else started.
func TestLatestCI_RecordedBaseOnlyForItsOwnRun(t *testing.T) {
	log := "Merge " + fixHead + " into ffcf83133a\n"
	f, _ := fakeMirror(t, runJSON("completed", "success"), log)
	ci, err := f.LatestCI(t.Context(), "ghsa-abcd-efgh-ijkl/main-pr1", model.RecordedBase{SHA: "aaaa1111", RunID: 99}, baseSHA)
	require.NoError(t, err)
	assert.Equal(t, model.CIGreen, ci.State, "run 10 isn't the recorded run 99")

	ci, err = f.LatestCI(t.Context(), "ghsa-abcd-efgh-ijkl/main-pr1",
		model.RecordedBase{SHA: "aaaa1111", Since: time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC)}, baseSHA)
	require.NoError(t, err)
	assert.Equal(t, model.CIUnconfirmed, ci.State, "a run started after the recording is the tool's run, and its base differs")
}

// Review #9: a completed run's jobs never change, so they're fetched once.
func TestLatestCI_CachesCompletedRunJobs(t *testing.T) {
	f, hits := fakeMirror(t, runJSON("completed", "success"), "Merge "+fixHead+" into ffcf83133a\n")
	for range 2 {
		_, err := f.LatestCI(t.Context(), "ghsa-abcd-efgh-ijkl/main-pr1", model.RecordedBase{}, baseSHA)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, hits.jobs)
}
