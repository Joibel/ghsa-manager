package cve

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-github/v90/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/state"
)

func TestRequestRecords(t *testing.T) {
	testRequest(t, http.StatusAccepted, true)
}

func TestRequestFailsRecordsNothing(t *testing.T) {
	testRequest(t, http.StatusUnprocessableEntity, false)
}

func testRequest(t *testing.T, status int, wantRecorded bool) {
	t.Helper()
	mux := http.NewServeMux()
	var hits int
	mux.HandleFunc("POST /repos/o/r/security-advisories/GHSA-x/cve", func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base := srv.URL + "/"
	c, err := gh.NewClient("tok", github.WithURLs(&base, &base))
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	err = Request(t.Context(), c, gh.Repo{Owner: "o", Name: "r"}, path, "GHSA-x", now)
	assert.Equal(t, 1, hits)

	st, loadErr := state.Load(path)
	require.NoError(t, loadErr)
	if wantRecorded {
		require.NoError(t, err)
		assert.True(t, st.Checks["GHSA-x"].CVERequested.Equal(now))
		return
	}
	require.ErrorContains(t, err, "requesting a CVE for GHSA-x")
	assert.NotContains(t, st.Checks, "GHSA-x")
}
