package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/runner"
	"github.com/Joibel/ghsa-manager/internal/source"
	"github.com/Joibel/ghsa-manager/internal/state"
)

const (
	h1  = "1111111111111111111111111111111111111111"
	old = "0000000000000000000000000000000000000000"
	idA = "GHSA-aaaa-bbbb-cccc"
	idD = "GHSA-dddd-eeee-ffff"
)

func now() time.Time { return time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC) }

func snapshot() source.Snapshot {
	green := model.CI{State: model.CIGreen, RunHeadSHA: h1, FinishedAt: now().Add(-2 * time.Hour)}
	red := model.CI{State: model.CIRed, RunHeadSHA: h1, FinishedAt: now().Add(-time.Hour)}
	return source.Snapshot{FetchedAt: now(), Advisories: []model.Advisory{
		{ID: idA, Fork: "o/a", URL: "https://adv/a", Summary: "Workflow archive lookup crosses namespaces", PRs: []model.FixPR{
			{Number: 1, Base: "main", HeadSHA: h1, CI: red, URL: "https://pr/a1", Author: "mallory-fixer"},
			{Number: 2, Base: "release-4.1", HeadSHA: h1, CI: green, URL: "https://pr/a2",
				Reviews: []model.Review{{User: "alice", State: "APPROVED", CommitID: old}}},
			{Number: 3, Base: "release-4.0", HeadSHA: h1, URL: "https://pr/a3"},
		}},
		{ID: idD, Fork: "o/d", URL: "https://adv/d", PRs: []model.FixPR{
			{Number: 1, Base: "main", HeadSHA: h1, CI: red},
		}},
		{ID: "GHSA-gggg-hhhh-iiii", URL: "https://adv/g", Summary: strings.Repeat("very long title ", 20)},
	}}
}

type fakes struct {
	statePath   string
	runErr      error
	snap        source.Snapshot
	runs        [][]int
	runResult   []runner.Result
	opened      []string
	forks       []string
	reported    []string
	reruns      []int64
	inFlight    int
	lockedInRep bool
}

func newModel(t *testing.T, f *fakes) Model {
	t.Helper()
	f.statePath = filepath.Join(t.TempDir(), "state.json")
	return New(Deps{
		Fetch:    func(context.Context, map[string]model.RecordedBase) source.Snapshot { return f.snap },
		InFlight: func(context.Context) (int, error) { return f.inFlight, nil },
		Run: func(_ context.Context, _ string, prs []int) ([]runner.Result, error) {
			f.runs = append(f.runs, prs)
			return f.runResult, f.runErr
		},
		Report: func(_ context.Context, _ *state.State, a model.Advisory) error {
			f.reported = append(f.reported, a.ID)
			// Review #11: the state lock must be free while the network call runs.
			done := make(chan struct{})
			go func() {
				_ = state.Update(f.statePath, func(*state.State) error { return nil })
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				f.lockedInRep = true
			}
			return nil
		},
		Rerun:     func(_ context.Context, runID int64) error { f.reruns = append(f.reruns, runID); return nil },
		MakeFork:  func(_ context.Context, id string) error { f.forks = append(f.forks, id); return nil },
		Open:      func(url string) error { f.opened = append(f.opened, url); return nil },
		StatePath: f.statePath,
		MaxRuns:   3,
		Now:       now,
	})
}

// load delivers a fetch result to the model, as Init's command would.
func load(t *testing.T, m Model, f *fakes) Model {
	t.Helper()
	m, _ = loadCmd(t, m, f)
	return m
}

// loadCmd delivers a fresh fetch result and returns the command it produced.
func loadCmd(t *testing.T, m Model, f *fakes) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(snapMsg{snap: f.snap, inFlight: f.inFlight, countedAt: now()})
	return asModel(t, next), cmd
}

// runCmd executes a start command and returns its runDoneMsg.
func runCmd(t *testing.T, cmd tea.Cmd) runDoneMsg {
	t.Helper()
	require.NotNil(t, cmd)
	for _, msg := range flatten(cmd) {
		if done, ok := msg.(runDoneMsg); ok {
			return done
		}
	}
	t.Fatal("no run started")
	return runDoneMsg{}
}

// flatten runs a command, expanding tea.Batch, and collects the messages.
// Fetch commands are skipped: they'd call the fakes' Fetch, which is fine, but
// tick commands would block.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flatten(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func asModel(t *testing.T, tm tea.Model) Model {
	t.Helper()
	m, ok := tm.(Model)
	require.True(t, ok)
	return m
}

func key(m Model, k string) Model {
	var msg tea.KeyMsg
	switch k {
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, _ := m.Update(msg)
	nm, ok := next.(Model)
	if !ok {
		panic("Update returned a non-Model")
	}
	return nm
}

func queueOf(t *testing.T, m Model) []state.QueueEntry {
	t.Helper()
	s, err := state.Load(m.deps.StatePath)
	require.NoError(t, err)
	return s.Queue
}

func TestMark_QueuesNonGreenPRs(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3} // full: nothing starts
	m := load(t, newModel(t, f), f)
	m = key(m, "c") // cursor starts on GHSA-aaaa
	q := queueOf(t, m)
	require.Len(t, q, 1)
	assert.Equal(t, idA, q[0].GHSA)
	assert.Equal(t, []int{1, 3}, q[0].Pending, "the green release-4.1 PR isn't queued")
}

func TestToTop_ReordersTheQueueNotTheRows(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	rowsBefore := m.rows()
	m = key(m, "c")
	m = key(m, "down") // PR rows of GHSA-aaaa
	m = key(m, "down")
	m = key(m, "down")
	m = key(m, "down") // GHSA-dddd
	m = key(m, "c")
	m = key(m, "t") // GHSA-dddd to the top of the queue
	q := queueOf(t, m)
	require.Len(t, q, 2)
	assert.Equal(t, idD, q[0].GHSA)
	m = key(m, "b")
	assert.Equal(t, idA, queueOf(t, m)[0].GHSA)
	assert.Equal(t, rowsBefore, m.rows(), "marking and reordering the queue don't move rows")
}

func TestNothingToRunIsANote(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	for range 6 { // to GHSA-gggg, which has no PRs
		m = key(m, "down")
	}
	m = key(m, "c")
	assert.Empty(t, queueOf(t, m))
	assert.Contains(t, m.View(), "nothing to run")
}

// Review Focus 4: a failed refresh keeps the last good snapshot on screen.
func TestRefreshErrorKeepsTheLastSnapshot(t *testing.T) {
	f := &fakes{snap: snapshot()}
	m := load(t, newModel(t, f), f)
	next, _ := m.Update(snapMsg{snap: source.Snapshot{Err: errors.New("API rate limit exceeded")}})
	m = asModel(t, next)
	v := m.View()
	assert.Contains(t, v, idA)
	assert.Contains(t, v, "API rate limit exceeded")
}

func TestStartingARunShowsTouchYubiKey(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 0}
	m := load(t, newModel(t, f), f)
	m = key(m, "c")
	assert.NotContains(t, m.View(), "touch YubiKey", "runs only start after a fresh count")
	m = load(t, m, f)
	assert.Contains(t, m.View(), "touch YubiKey")
}

// Review #1: after a run, nothing more starts until a fresh in-flight count.
func TestCapWaitsForAFreshCount(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 1}
	m := load(t, newModel(t, f), f)
	m = key(m, "c") // GHSA-aaaa: PRs 1 and 3 aren't green
	m, cmd := loadCmd(t, m, f)
	done := runCmd(t, cmd)
	assert.Equal(t, []int{1, 3}, done.prs)
	next, _ := m.Update(runDoneMsg{ghsa: idA, prs: done.prs, startedAt: done.startedAt, results: []runner.Result{
		{GHSA: idA, ForkPR: 1, HeadSHA: h1, BaseSHA: "b", MirrorBranch: "ghsa-aaaa-bbbb-cccc/main-pr1", Action: "updated"},
		{GHSA: idA, ForkPR: 3, HeadSHA: h1, BaseSHA: "b", MirrorBranch: "ghsa-aaaa-bbbb-cccc/release-4.0-pr3", Action: "updated"},
	}})
	m = asModel(t, next)
	assert.Empty(t, m.running, "no new start from the stale count")
	assert.Contains(t, m.View(), "running 3/3")
}

// Review #4: a red run on the current head and base is re-run through the API;
// a running one is adopted. Neither goes through ghsa-ci.sh.
func TestRerunsRedAndAdoptsRunning(t *testing.T) {
	snap := snapshot()
	a := &snap.Advisories[0]
	a.PRs[0].CI = model.CI{State: model.CIRed, RunID: 9, RunHeadSHA: h1, MergedBaseSHA: "basebase", CurrentBaseSHA: "basebase", StartedAt: now().Add(-time.Hour)}
	a.PRs[2].CI = model.CI{State: model.CIRunning, RunID: 11, RunHeadSHA: h1, CurrentBaseSHA: "relbase", StartedAt: now().Add(-time.Minute)}
	f := &fakes{snap: snap}
	m := load(t, newModel(t, f), f)
	m = key(m, "c")
	m, cmd := loadCmd(t, m, f)
	done := runCmd(t, cmd)
	assert.Equal(t, []int64{9}, f.reruns)
	assert.Empty(t, f.runs, "ghsa-ci.sh isn't needed")
	_, _ = m.Update(done)
	s, err := state.Load(f.statePath)
	require.NoError(t, err)
	require.Len(t, s.InFlight, 2)
	assert.Equal(t, int64(9), s.Bases["ghsa-aaaa-bbbb-cccc/main-pr1"].RunID)
	assert.Equal(t, int64(11), s.Bases["ghsa-aaaa-bbbb-cccc/release-4.0-pr3"].RunID)
	assert.ElementsMatch(t, []int{1, 3}, s.Queue[0].Started)
}

// Review #3: PRs the script started before failing are in flight; the rest fail.
func TestPartialScriptFailure(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	m = key(m, "c")
	next, _ := m.Update(runDoneMsg{ghsa: idA, prs: []int{1, 3}, startedAt: now(), err: errors.New("PR head moved while running; try again"),
		results: []runner.Result{{GHSA: idA, ForkPR: 1, HeadSHA: h1, BaseSHA: "b", MirrorBranch: "ghsa-aaaa-bbbb-cccc/main-pr1", Action: "updated"}}})
	asModel(t, next)
	s, err := state.Load(f.statePath)
	require.NoError(t, err)
	require.Len(t, s.InFlight, 1)
	assert.Equal(t, 1, s.InFlight[0].PR)
	assert.Equal(t, "PR head moved while running; try again", s.Failures[idA][3])
	assert.NotContains(t, s.Failures[idA], 1)
}

// Review #7: why a PR couldn't start shows on its row.
func TestStartFailureShowsOnTheRow(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	m = key(m, "c")
	next, _ := m.Update(runDoneMsg{ghsa: idA, prs: []int{1}, startedAt: now(), err: errors.New("SSH to github.com failed")})
	m = asModel(t, next)
	assert.Contains(t, m.View(), "start failed: SSH to github.com failed")
}

// Review #8: an advisory whose runs all failed to start gets no comment.
func TestNoReportWhenNothingStarted(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	m = key(m, "c")
	next, _ := m.Update(runDoneMsg{ghsa: idA, prs: []int{1, 3}, startedAt: now(), err: errors.New("SSH to github.com failed")})
	m = asModel(t, next)
	_, cmd := loadCmd(t, m, f)
	flatten(cmd)
	assert.Empty(t, f.reported)
	assert.Empty(t, queueOf(t, m), "unmarked")
}

// Review #11: reporting doesn't hold the state lock across the network call.
func TestReportReleasesTheStateLock(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	m = key(m, "c")
	next, _ := m.Update(runDoneMsg{ghsa: idA, prs: []int{1, 3}, startedAt: now().Add(-time.Hour), results: []runner.Result{
		{GHSA: idA, ForkPR: 1, HeadSHA: h1, BaseSHA: "b", MirrorBranch: "ghsa-aaaa-bbbb-cccc/main-pr1", Action: "updated"},
	}})
	m = asModel(t, next)
	// The main PR's (red) run started after the flight, so it's over.
	f.snap.Advisories[0].PRs[0].CI.StartedAt = now()
	_, cmd := loadCmd(t, m, f)
	flatten(cmd)
	require.Equal(t, []string{idA}, f.reported)
	assert.False(t, f.lockedInRep)
}

// Review #12: the cursor stays on its advisory when marking re-sorts the rows.
func TestCursorFollowsItsAdvisory(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	for range 4 {
		m = key(m, "down")
	}
	sel, ok := m.selected()
	require.True(t, ok)
	require.Equal(t, idD, sel.adv.ID)
	m = key(m, "c") // GHSA-dddd moves to the top of the list
	sel, ok = m.selected()
	require.True(t, ok)
	assert.Equal(t, idD, sel.adv.ID)
}

// Review #6: x forgets a stuck advisory's flights.
func TestClearFlightsKey(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	require.NoError(t, state.Update(f.statePath, func(s *state.State) error {
		s.InFlight = []state.Flight{{GHSA: idA, PR: 1, StartedAt: now()}}
		return nil
	}))
	key(m, "x")
	s, err := state.Load(f.statePath)
	require.NoError(t, err)
	assert.Empty(t, s.InFlight)
}

func TestRunDoneRecordsFlightsAndFailures(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	m = key(m, "c")
	next, _ := m.Update(runDoneMsg{ghsa: idA, prs: []int{1, 3}, startedAt: now(), results: []runner.Result{
		{GHSA: idA, ForkPR: 1, HeadSHA: h1, BaseSHA: "b", MirrorBranch: "ghsa-aaaa-bbbb-cccc/main-pr1", Action: "updated"},
	}})
	m = asModel(t, next)
	s, err := state.Load(m.deps.StatePath)
	require.NoError(t, err)
	require.Len(t, s.InFlight, 1)
	assert.Equal(t, 1, s.InFlight[0].PR)
	assert.NotContains(t, m.View(), "touch YubiKey")

	next, _ = m.Update(runDoneMsg{ghsa: idA, prs: []int{3}, err: errors.New("SSH to github.com failed")})
	m = asModel(t, next)
	s, err = state.Load(m.deps.StatePath)
	require.NoError(t, err)
	assert.Equal(t, "SSH to github.com failed", s.Failures[idA][3])
}

func TestMakeForkAsksFirst(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	for range 6 {
		m = key(m, "down")
	}
	m = key(m, "f")
	assert.Contains(t, m.View(), "make a fork for GHSA-gggg-hhhh-iiii? y/n")
	m = key(m, "n")
	assert.Empty(t, f.forks)
	m = key(m, "f")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	require.NotNil(t, cmd)
	cmd()
	assert.Equal(t, []string{"GHSA-gggg-hhhh-iiii"}, f.forks)
}

func TestOpenAdvisoryAndPR(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	m = key(m, "enter")
	m = key(m, "down") // main PR row
	key(m, "p")
	assert.Equal(t, []string{"https://adv/a", "https://pr/a1"}, f.opened)
}

// R posts the selected advisory's current CI results straight away, without
// re-running anything or touching the queue.
func TestReportNowPostsCurrentResults(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")}) // cursor on GHSA-aaaa, which has runs
	flatten(cmd)
	assert.Equal(t, []string{idA}, f.reported)
	assert.Empty(t, f.runs)
	assert.Empty(t, f.reruns)
	assert.Empty(t, queueOf(t, m))
}

func TestReportNowNeedsARun(t *testing.T) {
	snap := snapshot()
	snap.Advisories[1].PRs[0].CI = model.CI{} // GHSA-dddd: never run
	f := &fakes{snap: snap, inFlight: 3}
	m := load(t, newModel(t, f), f)
	for range 4 {
		m = key(m, "down")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	m = asModel(t, next)
	flatten(cmd)
	assert.Empty(t, f.reported)
	assert.Contains(t, m.View(), "no CI has run yet")
}

// R on a queued advisory reports but leaves it queued.
func TestReportNowKeepsTheQueue(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	m = key(m, "c")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	flatten(cmd)
	assert.Equal(t, []string{idA}, f.reported)
	assert.NotEmpty(t, queueOf(t, m))
}
