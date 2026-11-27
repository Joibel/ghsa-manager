package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/gh"
)

func TestLoad_ReadsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"upstream":"example/upstream","mirror":"example/mirror"}`), 0o600))
	c, err := Load(path, "", "")
	require.NoError(t, err)
	assert.Equal(t, gh.Repo{Owner: "example", Name: "upstream"}, c.Repos.Upstream)
	assert.Equal(t, gh.Repo{Owner: "example", Name: "mirror"}, c.Repos.Mirror)
}

func TestLoad_FlagsOverrideTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"upstream":"example/upstream","mirror":"example/mirror"}`), 0o600))
	c, err := Load(path, "other/up", "")
	require.NoError(t, err)
	assert.Equal(t, "other/up", c.Repos.Upstream.String())
	assert.Equal(t, "example/mirror", c.Repos.Mirror.String())
}

func TestLoad_MissingFileWithFlags(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.json"), "a/b", "c/d")
	require.NoError(t, err)
	assert.Equal(t, "a/b", c.Repos.Upstream.String())
	assert.Equal(t, "c/d", c.Repos.Mirror.String())
}

func TestLoad_MissingReposIsAnError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "none.json"), "a/b", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mirror")
}

func TestLoad_BadRepoName(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "none.json"), "nonsense", "c/d")
	require.Error(t, err)
}

func TestPath_HonoursXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/config")
	assert.Equal(t, "/x/config/ghsa-manager/config.json", Path())
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("HOME", "/h")
	c, err := Load(writeConfig(t, `{"upstream":"o/argo-workflows","mirror":"o/m"}`), "", "")
	require.NoError(t, err)
	assert.Equal(t, "/h/sources/argo-workflows-security", c.Checkout)
	assert.Equal(t, 2, c.SupportedBranches)
}

func TestLoadCheckoutTilde(t *testing.T) {
	t.Setenv("HOME", "/h")
	c, err := Load(writeConfig(t, `{"upstream":"o/u","mirror":"o/m","checkout":"~/x"}`), "", "")
	require.NoError(t, err)
	assert.Equal(t, "/h/x", c.Checkout)
}

func TestLoadCheckoutAsGiven(t *testing.T) {
	t.Setenv("HOME", "/h")
	c, err := Load(writeConfig(t, `{"upstream":"o/u","mirror":"o/m","checkout":"/srv/co"}`), "", "")
	require.NoError(t, err)
	assert.Equal(t, "/srv/co", c.Checkout)
}

func TestLoadSupportedBranches(t *testing.T) {
	c, err := Load(writeConfig(t, `{"upstream":"o/u","mirror":"o/m","supported_branches":3}`), "", "")
	require.NoError(t, err)
	assert.Equal(t, 3, c.SupportedBranches)
}

func TestLoadSupportedBranchesInvalid(t *testing.T) {
	_, err := Load(writeConfig(t, `{"upstream":"o/u","mirror":"o/m","supported_branches":0}`), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "supported_branches")
}
