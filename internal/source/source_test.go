package source

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v90/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/mirror"
	"github.com/Joibel/ghsa-manager/internal/model"
)

const (
	fork     = "example/upstream-ghsa-aaaa-bbbb-cccc"
	head1    = "1111111111111111111111111111111111111111"
	head2    = "2222222222222222222222222222222222222222"
	mainTip  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	relTip   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	mirrorRp = "/repos/example/mirror"
)

func fakeGitHub(t *testing.T, pullsStatus int, missingReleaseBranch bool) *Source {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/upstream/security-advisories", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "draft", r.URL.Query().Get("state"))
		_, _ = fmt.Fprintf(w, `[
			{"ghsa_id":"GHSA-dddd-eeee-ffff","summary":"second","html_url":"https://github.com/advisory/d","state":"draft","created_at":"2026-09-01T00:00:00Z"},
			{"ghsa_id":"GHSA-aaaa-bbbb-cccc","summary":"first","html_url":"https://github.com/advisory/a","state":"draft","created_at":"2026-10-01T00:00:00Z",
			 "private_fork":{"full_name":%q}}]`, fork)
	})
	mux.HandleFunc("/repos/"+fork+"/pulls", func(w http.ResponseWriter, _ *http.Request) {
		if pullsStatus != http.StatusOK {
			w.WriteHeader(pullsStatus)
			return
		}
		_, _ = fmt.Fprintf(w, `[
			{"number":1,"title":"fix: one","html_url":"https://github.com/%[1]s/pull/1","head":{"sha":%[2]q},"base":{"ref":"main"},"user":{"login":"reporter-dev"}},
			{"number":2,"title":"fix: one (4.1)","html_url":"https://github.com/%[1]s/pull/2","head":{"sha":%[3]q},"base":{"ref":"release-4.1"}}]`,
			fork, head1, head2)
	})
	mux.HandleFunc("/repos/"+fork+"/pulls/1/reviews", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `[{"user":{"login":"alice"},"state":"APPROVED","commit_id":%q},
			{"user":{"login":"bob"},"state":"COMMENTED","commit_id":%q}]`, head1, head1)
	})
	mux.HandleFunc("/repos/"+fork+"/pulls/2/reviews", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc(mirrorRp+"/branches/main", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"name":"main","commit":{"sha":%q}}`, mainTip)
	})
	mux.HandleFunc(mirrorRp+"/branches/release-4.1", func(w http.ResponseWriter, _ *http.Request) {
		if missingReleaseBranch {
			http.Error(w, `{"message":"Branch not found"}`, http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprintf(w, `{"name":"release-4.1","commit":{"sha":%q}}`, relTip)
	})
	mux.HandleFunc(mirrorRp+"/actions/workflows/ci-build.yaml/runs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"total_count":0,"workflow_runs":[]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base := srv.URL + "/"
	c, err := gh.NewClient("tok", github.WithURLs(&base, &base))
	require.NoError(t, err)
	repos := gh.Repos{Upstream: gh.Repo{Owner: "example", Name: "upstream"}, Mirror: gh.Repo{Owner: "example", Name: "mirror"}}
	return New(c, mirror.NewFacts(c, srv.Client(), repos.Mirror), repos)
}

func TestFetch_AssemblesAdvisories(t *testing.T) {
	snap := fakeGitHub(t, http.StatusOK, false).Fetch(t.Context(), nil)
	require.NoError(t, snap.Err)
	require.Len(t, snap.Advisories, 2)

	assert.Equal(t, "GHSA-dddd-eeee-ffff", snap.Advisories[0].ID, "oldest first")
	a := snap.Advisories[1]
	assert.Equal(t, "GHSA-aaaa-bbbb-cccc", a.ID)
	assert.Equal(t, fork, a.Fork)
	assert.Equal(t, model.NeedsCI, a.Status())
	require.Len(t, a.PRs, 2)
	assert.Equal(t, "main", a.PRs[0].Base)
	assert.Equal(t, head1, a.PRs[0].HeadSHA)
	assert.Equal(t, "reporter-dev", a.PRs[0].Author)
	assert.Equal(t, []model.Review{
		{User: "alice", State: "APPROVED", CommitID: head1},
		{User: "bob", State: "COMMENTED", CommitID: head1},
	}, a.PRs[0].Reviews)
	assert.Equal(t, model.CINever, a.PRs[0].CI.State)
	assert.Equal(t, mainTip, a.PRs[0].CI.CurrentBaseSHA)
	assert.Equal(t, relTip, a.PRs[1].CI.CurrentBaseSHA)

	assert.Equal(t, model.NoFork, snap.Advisories[0].Status())
	assert.False(t, snap.FetchedAt.IsZero())
}

func TestFetch_ErrorReturnsNoPartialData(t *testing.T) {
	snap := fakeGitHub(t, http.StatusInternalServerError, false).Fetch(t.Context(), nil)
	require.Error(t, snap.Err)
	assert.Empty(t, snap.Advisories)
}

// A fix PR can target a branch the mirror doesn't have yet: ghsa-ci.sh creates
// it on the first run. That's an empty tip, not an error.
func TestFetch_MissingMirrorBaseBranch(t *testing.T) {
	snap := fakeGitHub(t, http.StatusOK, true).Fetch(t.Context(), nil)
	require.NoError(t, snap.Err)
	require.Len(t, snap.Advisories[1].PRs, 2)
	assert.Empty(t, snap.Advisories[1].PRs[1].CI.CurrentBaseSHA)
}
