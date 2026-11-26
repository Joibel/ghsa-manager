package gh

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGh writes an executable script standing in for the gh CLI.
func fakeGh(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700)) //nolint:gosec // test fixture must be executable
	return path
}

func TestToken_TrimsOutput(t *testing.T) {
	tok, err := tokenFrom(t.Context(), fakeGh(t, `echo abc`))
	require.NoError(t, err)
	assert.Equal(t, "abc", tok)
}

func TestToken_ErrorIncludesStderr(t *testing.T) {
	_, err := tokenFrom(t.Context(), fakeGh(t, `echo "no oauth token found" >&2; exit 1`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no oauth token found")
}

func TestToken_IgnoresEnv(t *testing.T) {
	t.Setenv("GH_TOKEN", "x")
	t.Setenv("GITHUB_TOKEN", "y")
	tok, err := tokenFrom(t.Context(), fakeGh(t, `echo from-gh`))
	require.NoError(t, err)
	assert.Equal(t, "from-gh", tok)
}

func TestToken_EmptyOutputIsAnError(t *testing.T) {
	_, err := tokenFrom(t.Context(), fakeGh(t, `echo ""`))
	require.Error(t, err)
}
