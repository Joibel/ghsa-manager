package report

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v90/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/state"
)

const (
	id      = "GHSA-abcd-efgh-ijkl"
	head    = "1111111111111111111111111111111111111111"
	comPath = "/repos/example/upstream/security-advisories/GHSA-abcd-efgh-ijkl/comments"
)

func advisory() model.Advisory {
	return model.Advisory{ID: id, Fork: "o/f", PRs: []model.FixPR{
		{Number: 1, Base: "main", HeadSHA: head, CI: model.CI{State: model.CIGreen, RunHeadSHA: head, RunURL: "https://run/1"}},
		{Number: 2, Base: "release-4.1", HeadSHA: head, CI: model.CI{State: model.CIRed, RunHeadSHA: head, RunURL: "https://run/2", SkippedTestJobs: []string{"E2E UI (root)"}}},
		{Number: 3, Base: "release-4.0", HeadSHA: head, CI: model.CI{State: model.CIUnconfirmed, RunHeadSHA: head, RunURL: "https://run/3"}},
		{Number: 4, Base: "release-3.7", HeadSHA: head, StartError: "conflicts with release-3.7; rebase the GHSA PR"},
	}}
}

func TestBody(t *testing.T) {
	now := time.Date(2026, 11, 26, 15, 4, 0, 0, time.UTC)
	want := `<!-- ghsa-manager:ci-status -->
**CI status for the fix PRs**

| Fix PR | Branch | CI |
|---|---|---|
| #1 | main | ✅ green ([run](https://run/1)) |
| #2 | release-4.1 | ❌ red, skipped: E2E UI (root) ([run](https://run/2)) |
| #3 | release-4.0 | ❓ unconfirmed: the tested base couldn't be verified ([run](https://run/3)) |
| #4 | release-3.7 | ⚠️ not started: conflicts with its base branch and needs a rebase |

CI runs in a private mirror, because temporary private forks can't run GitHub Actions.

_Last updated 2026-11-26 15:04 UTC._
`
	assert.Equal(t, want, Body(advisory(), now))
}

// fakeComments serves the advisory comments API, recording writes.
type fakeComments struct {
	patched  map[string]string
	existing string // JSON array returned by GET
	posted   []string
	gets     int
}

func (f *fakeComments) client(t *testing.T) *github.Client {
	t.Helper()
	f.patched = map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc(comPath, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			f.gets++
			_, _ = fmt.Fprint(w, f.existing)
		case http.MethodPost:
			var in struct{ Body string }
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&in))
			f.posted = append(f.posted, in.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"id":777}`)
		}
	})
	mux.HandleFunc(comPath+"/", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		var in struct{ Body string }
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		f.patched[strings.TrimPrefix(r.URL.Path, comPath+"/")] = in.Body
		_, _ = fmt.Fprint(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base := srv.URL + "/"
	c, err := gh.NewClient("tok", github.WithURLs(&base, &base))
	require.NoError(t, err)
	return c
}

func newState(t *testing.T) *state.State {
	t.Helper()
	s, err := state.Load(t.TempDir() + "/state.json")
	require.NoError(t, err)
	return s
}

func TestUpsert_PostsWhenThereIsNoComment(t *testing.T) {
	f := &fakeComments{existing: `[{"id":5,"body":"thanks for the report"}]`}
	s := newState(t)
	require.NoError(t, Upsert(t.Context(), f.client(t), gh.Repo{Owner: "example", Name: "upstream"}, s, advisory()))
	require.Len(t, f.posted, 1)
	assert.True(t, strings.HasPrefix(f.posted[0], Marker))
	assert.Equal(t, int64(777), s.CommentIDs[id])
}

func TestUpsert_PatchesTheMarkerComment(t *testing.T) {
	f := &fakeComments{existing: `[{"id":5,"body":"hi"},{"id":9,"body":"` + Marker + `\nold"}]`}
	s := newState(t)
	require.NoError(t, Upsert(t.Context(), f.client(t), gh.Repo{Owner: "example", Name: "upstream"}, s, advisory()))
	assert.Empty(t, f.posted)
	assert.Contains(t, f.patched, "9")
	assert.Equal(t, int64(9), s.CommentIDs[id])
}

func TestUpsert_AdoptsTheLegacyComment(t *testing.T) {
	f := &fakeComments{existing: `[{"id":12345,"body":"**CI status for the fix PRs** (maintainer test of automated reporting)"}]`}
	s := newState(t)
	require.NoError(t, Upsert(t.Context(), f.client(t), gh.Repo{Owner: "example", Name: "upstream"}, s, advisory()))
	assert.Contains(t, f.patched, "12345")
	assert.Equal(t, int64(12345), s.CommentIDs[id])
}

func TestUpsert_UsesTheRecordedID(t *testing.T) {
	f := &fakeComments{existing: `[]`}
	s := newState(t)
	s.CommentIDs[id] = 42
	require.NoError(t, Upsert(t.Context(), f.client(t), gh.Repo{Owner: "example", Name: "upstream"}, s, advisory()))
	assert.Zero(t, f.gets, "no lookup when the ID is recorded")
	assert.Contains(t, f.patched, "42")
}

// Review #8: the reporter can read the comment, so raw script errors never
// reach it, and a | can't break the table.
func TestBody_ReporterSafeFailureText(t *testing.T) {
	a := model.Advisory{ID: id, PRs: []model.FixPR{
		{Number: 1, Base: "main", StartError: "can't push to the mirror: SSH to github.com failed, and your gh token lacks the workflow scope"},
		{Number: 2, Base: "rel|ease", StartError: "GitHub didn't rebuild the merge commit on the new main in time; mark it again later"},
	}}
	body := Body(a, time.Date(2026, 11, 26, 15, 4, 0, 0, time.UTC))
	assert.NotContains(t, body, "SSH")
	assert.NotContains(t, body, "gh token")
	assert.Contains(t, body, "| #1 | main | ⚠️ not started: CI couldn't be started; a maintainer will retry |")
	assert.Contains(t, body, `| #2 | rel\|ease | ⚠️ not started: CI couldn't be restarted yet; a maintainer will retry |`)
}
