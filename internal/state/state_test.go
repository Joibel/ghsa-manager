package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/model"
)

func TestLoad_MissingFileIsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "state.json"))
	require.NoError(t, err)
	assert.Empty(t, s.Queue)
	assert.Empty(t, s.InFlight)
}

func TestUpdate_WritesPrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ghsa-manager")
	path := filepath.Join(dir, "state.json")
	require.NoError(t, Update(path, func(s *State) error {
		s.Queue = append(s.Queue, QueueEntry{GHSA: "GHSA-aaaa-bbbb-cccc", Pending: []int{1}})
		s.Bases["ghsa-aaaa-bbbb-cccc/main-pr1"] = model.RecordedBase{SHA: "abc"}
		return nil
	}))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), di.Mode().Perm())

	s, err := Load(path)
	require.NoError(t, err)
	require.Len(t, s.Queue, 1)
	assert.Equal(t, []int{1}, s.Queue[0].Pending)
	assert.Equal(t, "abc", s.Bases["ghsa-aaaa-bbbb-cccc/main-pr1"].SHA)
}

func TestUpdate_SerialisesWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range 50 {
				assert.NoError(t, Update(path, func(s *State) error {
					s.Queue = append(s.Queue, QueueEntry{GHSA: "x"})
					return nil
				}))
			}
		})
	}
	wg.Wait()
	s, err := Load(path)
	require.NoError(t, err)
	assert.Len(t, s.Queue, 100)
}

func TestUpdate_CorruptFileIsAnErrorAndUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	err := Update(path, func(*State) error { return nil })
	require.Error(t, err)
	b, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(b))
}

func TestPath_HonoursXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/x/state")
	assert.Equal(t, "/x/state/ghsa-manager/example_upstream.json", Path("example/upstream"))
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/u")
	assert.Equal(t, "/home/u/.local/state/ghsa-manager/example_upstream.json", Path("example/upstream"))
}

func TestLoadOldStateHasChecks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"comment_ids":{},"queue":[]}`), 0o600))
	s, err := Load(path)
	require.NoError(t, err)
	assert.NotNil(t, s.Checks)
	assert.NotNil(t, Empty().Checks)

	at := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	require.NoError(t, Update(path, func(s *State) error {
		c := s.Checks["GHSA-aaaa-bbbb-cccc"]
		c.Toggle(model.Advisory{CVSSVector: "CVSS:3.1/AV:N"}, nil, 2, model.ItemCVSS, "", at)
		s.Checks["GHSA-aaaa-bbbb-cccc"] = c
		return nil
	}))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	b, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.NotContains(t, string(b), "CVSS", "the state file holds hashes, never values")

	s, err = Load(path)
	require.NoError(t, err)
	tick := s.Checks["GHSA-aaaa-bbbb-cccc"].Ticks[model.ItemCVSS]
	assert.True(t, at.Equal(tick.At))
	assert.Equal(t, model.ValueHash(model.Advisory{CVSSVector: "CVSS:3.1/AV:N"}, model.ItemCVSS), tick.Hash)
}
