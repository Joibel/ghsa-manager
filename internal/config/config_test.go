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
	assert.Equal(t, gh.Repo{Owner: "example", Name: "upstream"}, c.Upstream)
	assert.Equal(t, gh.Repo{Owner: "example", Name: "mirror"}, c.Mirror)
}

func TestLoad_FlagsOverrideTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"upstream":"example/upstream","mirror":"example/mirror"}`), 0o600))
	c, err := Load(path, "other/up", "")
	require.NoError(t, err)
	assert.Equal(t, "other/up", c.Upstream.String())
	assert.Equal(t, "example/mirror", c.Mirror.String())
}

func TestLoad_MissingFileWithFlags(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.json"), "a/b", "c/d")
	require.NoError(t, err)
	assert.Equal(t, "a/b", c.Upstream.String())
	assert.Equal(t, "c/d", c.Mirror.String())
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
