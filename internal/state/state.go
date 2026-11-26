// Package state holds ghsa-manager's only local data: the CI queue, in-flight
// runs, recorded bases and report comment IDs. It's written atomically, at
// 0600 in a 0700 directory, with writers serialised by an flock on a sidecar.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Joibel/ghsa-manager/internal/model"
)

// Flight is a fix PR whose mirror CI the tool started and hasn't seen finish.
type Flight struct {
	StartedAt time.Time `json:"started_at"`
	GHSA      string    `json:"ghsa"`
	Branch    string    `json:"branch"`
	HeadSHA   string    `json:"head_sha"`
	BaseSHA   string    `json:"base_sha"`
	PR        int       `json:"pr"`
}

// QueueEntry is a marked advisory. Pending lists the fix PRs still to start;
// Started lists those whose CI started, re-ran or was adopted.
type QueueEntry struct {
	GHSA    string `json:"ghsa"`
	Pending []int  `json:"pending"`
	Started []int  `json:"started,omitempty"`
}

// State is the whole state file.
type State struct {
	// CommentIDs maps an advisory to its CI-status comment.
	CommentIDs map[string]int64 `json:"comment_ids"`
	// Bases maps a mirror branch to the base the tool triggered its run against.
	Bases map[string]model.RecordedBase `json:"bases"`
	// Failures maps an advisory to the fix PRs whose CI couldn't start, with the
	// reason. They're kept, for the screen, until the advisory is marked again.
	Failures map[string]map[int]string `json:"failures"`
	Queue    []QueueEntry              `json:"queue"`
	InFlight []Flight                  `json:"in_flight"`
}

// Empty returns a state with nothing queued.
func Empty() *State {
	return &State{CommentIDs: map[string]int64{}, Bases: map[string]model.RecordedBase{}, Failures: map[string]map[int]string{}}
}

// Path is the state file for an upstream:
// $XDG_STATE_HOME/ghsa-manager/<owner>_<repo>.json, defaulting to
// ~/.local/state/ghsa-manager/.
func Path(upstream string) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(base, "ghsa-manager", strings.ReplaceAll(upstream, "/", "_")+".json")
}

// Load reads the state file. A missing file is an empty state.
func Load(path string) (*State, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is the tool's own state file
	if errors.Is(err, fs.ErrNotExist) {
		return Empty(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	s := Empty()
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if s.CommentIDs == nil {
		s.CommentIDs = map[string]int64{}
	}
	if s.Bases == nil {
		s.Bases = map[string]model.RecordedBase{}
	}
	if s.Failures == nil {
		s.Failures = map[string]map[int]string{}
	}
	return s, nil
}

// Update loads the state under an exclusive lock, applies fn, and saves the
// result atomically. If fn returns an error nothing is saved.
func Update(path string, fn func(*State) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a private directory needs its execute bit
		return fmt.Errorf("securing %s: %w", dir, err)
	}
	unlock, err := lock(path + ".lock")
	if err != nil {
		return err
	}
	return errors.Join(apply(path, fn), unlock())
}

func apply(path string, fn func(*State) error) error {
	s, err := Load(path)
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	return save(path, s)
}

func save(path string, s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return fmt.Errorf("creating temp state file: %w", err)
	}
	werr := writeTemp(tmp, b)
	if werr == nil {
		if err := os.Rename(tmp.Name(), path); err != nil {
			werr = fmt.Errorf("saving %s: %w", path, err)
		}
	}
	if werr != nil {
		if err := os.Remove(tmp.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			werr = errors.Join(werr, fmt.Errorf("removing temp state file: %w", err))
		}
	}
	return werr
}

// writeTemp makes tmp private, writes b and closes it.
func writeTemp(tmp *os.File, b []byte) error {
	err := tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(b)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing temp state file: %w", err)
	}
	return nil
}

// lock takes an exclusive flock on a sidecar file. The state file itself can't
// carry the lock: each atomic save replaces its inode.
func lock(lockPath string) (func() error, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // the tool's own lock file
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", lockPath, err)
	}
	fd := int(f.Fd())
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return nil, errors.Join(fmt.Errorf("locking %s: %w", lockPath, err), f.Close())
	}
	return func() error {
		// Closing also releases the lock; unlock first so it isn't left to GC.
		return errors.Join(unix.Flock(fd, unix.LOCK_UN), f.Close())
	}, nil
}
