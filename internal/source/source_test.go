package source

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	return fakeGitHubRefs(t, pullsStatus, missingReleaseBranch, http.StatusOK)
}

// fakeGitHubRefs is fakeGitHub with a chosen status for the upstream's
// matching-refs endpoints.
func fakeGitHubRefs(t *testing.T, pullsStatus int, missingReleaseBranch bool, refsStatus int) *Source {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/upstream/security-advisories", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "draft", r.URL.Query().Get("state"))
		_, _ = fmt.Fprintf(w, `[
			{"ghsa_id":"GHSA-dddd-eeee-ffff","summary":"second","html_url":"https://github.com/advisory/d","state":"draft","created_at":"2026-09-01T00:00:00Z"},
			{"ghsa_id":"GHSA-aaaa-bbbb-cccc","summary":"first","html_url":"https://github.com/advisory/a","state":"draft","created_at":"2026-10-01T00:00:00Z",
			 "description":"the details","cve_id":"CVE-2026-0001",
			 "cvss":{"vector_string":"CVSS:3.0/AV:N","score":5.0},
			 "cvss_severities":{
			   "cvss_v3":{"vector_string":"CVSS:3.1/AV:N/AC:L","score":7.5},
			   "cvss_v4":{"vector_string":"CVSS:4.0/AV:N/AC:L","score":8.7}},
			 "vulnerabilities":[
			   {"package":{"ecosystem":"go","name":"example.com/up"},"vulnerable_versions":"","vulnerable_version_range":">= 4.0.0, < 4.0.5","patched_versions":"4.0.5"},
			   {"package":{"ecosystem":"go","name":"example.com/up/sub"},"vulnerable_version_range":"< 3.7.9","first_patched_version":{"identifier":"3.7.9"}}],
			 "credits_detailed":[
			   {"user":{"login":"reporter-dev"},"type":"reporter","state":"accepted"},
			   {"user":{"login":"fixer"},"type":"remediation_developer","state":"pending"}],
			 "private_fork":{"full_name":%q}}]`, fork)
	})
	refs := func(w http.ResponseWriter, prefix string, names ...string) {
		if refsStatus != http.StatusOK {
			w.WriteHeader(refsStatus)
			return
		}
		var parts []string
		for _, n := range names {
			parts = append(parts, fmt.Sprintf(`{"ref":%q,"object":{"sha":%q}}`, prefix+n, mainTip))
		}
		_, _ = fmt.Fprintf(w, "[%s]", strings.Join(parts, ","))
	}
	mux.HandleFunc("/repos/example/upstream/git/matching-refs/heads/release-", func(w http.ResponseWriter, _ *http.Request) {
		refs(w, "refs/heads/", "release-3.7", "release-4.0", "release-4.1", "release-4.2", "release-notes")
	})
	mux.HandleFunc("/repos/example/upstream/git/matching-refs/tags/v", func(w http.ResponseWriter, _ *http.Request) {
		refs(w, "refs/tags/", "v4.1.0", "v4.0.0", "v3.7.0", "v4.2.0-rc1")
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

func TestFetchAdvisoryFields(t *testing.T) {
	snap := fakeGitHub(t, http.StatusOK, false).Fetch(t.Context(), nil)
	require.NoError(t, snap.Err)
	a := snap.Advisories[1]
	assert.Equal(t, "the details", a.Description)
	assert.Equal(t, "CVE-2026-0001", a.CVEID)
	assert.Equal(t, "CVSS:4.0/AV:N/AC:L", a.CVSSVector, "v4 wins")
	assert.InDelta(t, 8.7, a.CVSSScore, 0.001)
	assert.Equal(t, []model.Vulnerability{
		{Package: "example.com/up", Range: ">= 4.0.0, < 4.0.5", Patched: "4.0.5"},
		{Package: "example.com/up/sub", Range: "< 3.7.9", Patched: "3.7.9"},
	}, a.Vulns)
	assert.Equal(t, []model.Credit{
		{User: "reporter-dev", Type: "reporter", State: "accepted"},
		{User: "fixer", Type: "remediation_developer", State: "pending"},
	}, a.Credits)
}

func TestFetchReleases(t *testing.T) {
	snap := fakeGitHub(t, http.StatusOK, false).Fetch(t.Context(), nil)
	require.NoError(t, snap.Err)
	assert.Equal(t, []model.ReleaseBranch{
		{Name: "release-4.2", Major: 4, Minor: 2, GA: false},
		{Name: "release-4.1", Major: 4, Minor: 1, GA: true},
		{Name: "release-4.0", Major: 4, Minor: 0, GA: true},
		{Name: "release-3.7", Major: 3, Minor: 7, GA: true},
	}, snap.Releases)
}

func TestFetchReleases_ErrorFailsSnapshot(t *testing.T) {
	snap := fakeGitHubRefs(t, http.StatusOK, false, http.StatusInternalServerError).Fetch(t.Context(), nil)
	require.Error(t, snap.Err)
}
