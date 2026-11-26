package queue

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/runner"
	"github.com/Joibel/ghsa-manager/internal/state"
)

const (
	head = "1111111111111111111111111111111111111111"
	a    = "GHSA-aaaa-bbbb-cccc"
	b    = "GHSA-dddd-eeee-ffff"
)

func newState() *state.State {
	s, _ := state.Load("/nonexistent/state.json")
	return s
}

func greenPR(n int, base string) model.FixPR {
	return model.FixPR{Number: n, Base: base, HeadSHA: head, CI: model.CI{State: model.CIGreen, RunHeadSHA: head}}
}

func redPR(n int, base string) model.FixPR {
	return model.FixPR{Number: n, Base: base, HeadSHA: head, CI: model.CI{State: model.CIRed, RunHeadSHA: head}}
}

func adv(id string, prs ...model.FixPR) model.Advisory {
	return model.Advisory{ID: id, Fork: "o/" + id, PRs: prs}
}

func TestMark_OnlyNonGreenPRsMainFirst(t *testing.T) {
	s := newState()
	ok := Mark(s, adv(a, redPR(1, "main"), greenPR(2, "release-4.1"), redPR(3, "release-4.0")))
	require.True(t, ok)
	require.Len(t, s.Queue, 1)
	assert.Equal(t, []int{1, 3}, s.Queue[0].Pending)
}

func TestMark_NothingToRun(t *testing.T) {
	s := newState()
	assert.False(t, Mark(s, adv(a, greenPR(1, "main"))))
	assert.Empty(t, s.Queue)
}

func TestMark_AlreadyQueuedIsANoOp(t *testing.T) {
	s := newState()
	require.True(t, Mark(s, adv(a, redPR(1, "main"))))
	assert.False(t, Mark(s, adv(a, redPR(1, "main"))))
	assert.Len(t, s.Queue, 1)
}

func TestUnmarkAndToTopToBottom(t *testing.T) {
	const c = "GHSA-gggg-hhhh-iiii"
	s := newState()
	Mark(s, adv(a, redPR(1, "main")))
	Mark(s, adv(b, redPR(1, "main")))
	Mark(s, adv(c, redPR(1, "main")))
	ToTop(s, c)
	assert.Equal(t, []string{c, a, b}, ids(s))
	ToBottom(s, c)
	assert.Equal(t, []string{a, b, c}, ids(s))
	ToTop(s, "GHSA-not-queued") // no-op
	assert.Equal(t, []string{a, b, c}, ids(s))
	Unmark(s, b)
	assert.Equal(t, []string{a, c}, ids(s))
}

func ids(s *state.State) []string {
	out := make([]string, 0, len(s.Queue))
	for _, e := range s.Queue {
		out = append(out, e.GHSA)
	}
	return out
}

var t0 = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // fixed test clock

func TestNext_FillsFreeSlotsInQueueOrder(t *testing.T) {
	s := newState()
	snap := []model.Advisory{adv(a, redPR(1, "main")), adv(b, redPR(1, "main"), redPR(2, "release-4.1"))}
	Mark(s, snap[1])
	Mark(s, snap[0])
	starts := Next(s, 1, t0, 3)
	assert.Equal(t, []Start{{GHSA: b, PR: 1}, {GHSA: b, PR: 2}}, starts)
}

func TestNext_NoFreeSlots(t *testing.T) {
	s := newState()
	Mark(s, adv(a, redPR(1, "main")))
	assert.Empty(t, Next(s, 3, t0, 3))
}

// Review Focus 3: after a restart, flights started after the mirror was
// counted still count; older ones are in the mirror's count.
func TestNext_CountsFlightsTheMirrorCountMayMiss(t *testing.T) {
	s := newState()
	s.InFlight = []state.Flight{{GHSA: b, PR: 1, StartedAt: t0.Add(-10 * time.Minute)}, {GHSA: b, PR: 2, StartedAt: t0.Add(time.Second)}}
	Mark(s, adv(a, redPR(1, "main"), redPR(2, "release-4.1")))
	assert.Len(t, Next(s, 1, t0, 3), 1, "1 counted by the mirror + 1 started after the count")
}

// Review #1: with an outside run in the mirror and a stale count, starting more
// PRs must not exceed the cap.
func TestNext_StaleCountDoesNotOvershoot(t *testing.T) {
	s := newState()
	Mark(s, adv(a, redPR(1, "main"), redPR(2, "release-4.1"), redPR(3, "release-4.0")))
	starts := Next(s, 1, t0, 3) // one run someone else started
	require.Len(t, starts, 2)
	RecordStart(s, a, []runner.Result{
		{GHSA: a, ForkPR: 1, HeadSHA: head, BaseSHA: "x", MirrorBranch: "b1", Action: "updated"},
		{GHSA: a, ForkPR: 2, HeadSHA: head, BaseSHA: "x", MirrorBranch: "b2", Action: "updated"},
	}, t0.Add(time.Second))
	assert.Empty(t, Next(s, 1, t0, 3), "still counting 1 outside + 2 just started")
}

// Review Focus 1: a queued advisory that left draft, lost its fork or closed a
// PR drops out with a note.
func TestReconcile_DropsVanishedWork(t *testing.T) {
	s := newState()
	Mark(s, adv(a, redPR(1, "main"), redPR(2, "release-4.1")))
	Mark(s, adv(b, redPR(1, "main")))
	s.InFlight = []state.Flight{{GHSA: b, PR: 1}}
	snap := []model.Advisory{adv(a, redPR(1, "main"))} // b left draft; a's PR 2 closed

	notes := Reconcile(s, snap, t0)
	require.Len(t, s.Queue, 1)
	assert.Equal(t, []int{1}, s.Queue[0].Pending)
	assert.Empty(t, s.InFlight)
	assert.ElementsMatch(t, []string{
		"GHSA-dddd-eeee-ffff is no longer a draft with a fork; dropped from the queue",
		"GHSA-aaaa-bbbb-cccc #2 is no longer open; dropped",
	}, notes)
}

func TestReconcile_RetiresFinishedFlights(t *testing.T) {
	started := t0
	s := newState()
	s.Queue = []state.QueueEntry{{GHSA: a}}
	s.Bases["b1"] = model.RecordedBase{SHA: "x", Since: started}
	s.InFlight = []state.Flight{{GHSA: a, PR: 1, Branch: "b1", HeadSHA: head, BaseSHA: "x", StartedAt: started}, {GHSA: a, PR: 2, HeadSHA: head, StartedAt: started}}
	done := redPR(1, "main")
	done.CI.RunID, done.CI.StartedAt = 55, started.Add(time.Minute)
	oldRun := redPR(2, "release-4.1")
	oldRun.CI.StartedAt = started.Add(-time.Hour)
	Reconcile(s, []model.Advisory{adv(a, done, oldRun)}, started.Add(time.Hour))
	require.Len(t, s.InFlight, 1)
	assert.Equal(t, 2, s.InFlight[0].PR, "a run that started before the flight isn't its run")
	assert.Equal(t, int64(55), s.Bases["b1"].RunID, "the recorded base is pinned to the run once known")
}

// Review #6: a flight whose run never shows up is dropped after a timeout.
func TestReconcile_DropsFlightsThatTimeOut(t *testing.T) {
	s := newState()
	s.InFlight = []state.Flight{{GHSA: a, PR: 1, HeadSHA: head, StartedAt: t0}}
	notes := Reconcile(s, []model.Advisory{adv(a, redPR(1, "main"))}, t0.Add(FlightTimeout+time.Minute))
	assert.Empty(t, s.InFlight)
	assert.Contains(t, notes, "GHSA-aaaa-bbbb-cccc #1: no finished CI run after 3h0m0s; stopped waiting")
}

func TestClearFlights(t *testing.T) {
	s := newState()
	s.InFlight = []state.Flight{{GHSA: a, PR: 1}, {GHSA: b, PR: 1}}
	ClearFlights(s, a)
	require.Len(t, s.InFlight, 1)
	assert.Equal(t, b, s.InFlight[0].GHSA)
}

func TestRecordStart(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	s := newState()
	Mark(s, adv(a, redPR(1, "main"), redPR(2, "release-4.1"), redPR(3, "release-4.0")))
	Mark(s, adv(b, redPR(1, "main")))
	RecordStart(s, a, []runner.Result{
		{GHSA: "GHSA-AAAA-BBBB-CCCC", ForkPR: 1, HeadSHA: head, BaseSHA: "base1", MirrorBranch: "br1", Action: "reopened"},
		{GHSA: a, ForkPR: 2, HeadSHA: head, BaseSHA: "base2", MirrorBranch: "br2", Action: "updated", Conflicting: true},
		{GHSA: a, ForkPR: 3, HeadSHA: head, BaseSHA: "base3", MirrorBranch: "br3", Action: "timeout"},
	}, now)
	require.Len(t, s.InFlight, 1)
	assert.Equal(t, state.Flight{GHSA: a, PR: 1, Branch: "br1", HeadSHA: head, BaseSHA: "base1", StartedAt: now}, s.InFlight[0])
	assert.Equal(t, model.RecordedBase{SHA: "base1", Since: now}, s.Bases["br1"])
	assert.Empty(t, s.Queue[0].Pending)
	assert.Equal(t, []int{1}, s.Queue[0].Started)
	assert.Contains(t, s.Failures[a][2], "conflicts")
	assert.Contains(t, s.Failures[a][3], "merge commit")
}

func TestRecordFlight_AdoptsARunningRun(t *testing.T) {
	s := newState()
	Mark(s, adv(a, redPR(1, "main")))
	RecordFlight(s, a, 1, "br1", head, "base1", t0, 77)
	require.Len(t, s.InFlight, 1)
	assert.Empty(t, s.Queue[0].Pending)
	assert.Equal(t, []int{1}, s.Queue[0].Started)
	assert.Equal(t, model.RecordedBase{SHA: "base1", Since: t0, RunID: 77}, s.Bases["br1"])
}

func TestRecordFailureAndFinished(t *testing.T) {
	s := newState()
	Mark(s, adv(a, redPR(1, "main")))
	Mark(s, adv(b, redPR(1, "main")))
	RecordFailure(s, a, []int{1}, "SSH failed")
	assert.Equal(t, "SSH failed", s.Failures[a][1])
	done := Finished(s)
	require.Len(t, done, 1)
	assert.Equal(t, a, done[0].GHSA)
	assert.Empty(t, done[0].Started, "nothing started, so nothing to report")
}

// Review #7: failures stay visible until the advisory is marked again.
func TestMark_ClearsOldFailures(t *testing.T) {
	s := newState()
	Mark(s, adv(a, redPR(1, "main")))
	RecordFailure(s, a, []int{1}, "SSH failed")
	Unmark(s, a)
	assert.Equal(t, "SSH failed", s.Failures[a][1], "kept after the advisory leaves the queue")
	Mark(s, adv(a, redPR(1, "main")))
	assert.Empty(t, s.Failures[a])
}
