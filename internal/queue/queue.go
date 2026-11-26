// Package queue holds the CI queue's rules: marking, ordering, which fix PRs
// start next under the runner cap, and bookkeeping as runs start and finish.
// It's pure: it changes a state.State and talks to nothing.
package queue

import (
	"fmt"
	"slices"
	"time"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/runner"
	"github.com/Joibel/ghsa-manager/internal/state"
)

// Start is a fix PR to start CI for.
type Start struct {
	GHSA string
	PR   int
}

func find(s *state.State, ghsa string) int {
	return slices.IndexFunc(s.Queue, func(e state.QueueEntry) bool { return e.GHSA == ghsa })
}

// Mark queues the advisory's fix PRs that don't have a confirmed green run on
// their current head, main first. It returns false if the advisory is already
// queued or nothing needs running.
func Mark(s *state.State, a model.Advisory) bool {
	if find(s, a.ID) >= 0 {
		return false
	}
	var pending []int
	for _, p := range a.PRs { // the source orders PRs main first
		if !p.GreenOnHead() {
			pending = append(pending, p.Number)
		}
	}
	if len(pending) == 0 {
		return false
	}
	delete(s.Failures, a.ID)
	s.Queue = append(s.Queue, state.QueueEntry{GHSA: a.ID, Pending: pending})
	return true
}

// Unmark removes an advisory from the queue. Runs already started carry on.
func Unmark(s *state.State, ghsa string) {
	if i := find(s, ghsa); i >= 0 {
		s.Queue = slices.Delete(s.Queue, i, i+1)
	}
}

// ToTop moves an advisory to the front of the queue: "do this one now".
func ToTop(s *state.State, ghsa string) {
	if i := find(s, ghsa); i > 0 {
		e := s.Queue[i]
		s.Queue = slices.Insert(slices.Delete(s.Queue, i, i+1), 0, e)
	}
}

// ToBottom moves an advisory to the back of the queue.
func ToBottom(s *state.State, ghsa string) {
	if i := find(s, ghsa); i >= 0 {
		e := s.Queue[i]
		s.Queue = append(slices.Delete(s.Queue, i, i+1), e)
	}
}

// FlightTimeout is how long the tool waits for a flight's run to finish before
// giving up on it, so a run that never started can't hold a slot for ever.
const FlightTimeout = 3 * time.Hour

// registrationLag is how long a just-started run may take to appear in the
// mirror's in-flight count.
const registrationLag = time.Minute

// Next returns the fix PRs to start now, in queue order, without exceeding max
// in-flight runs. mirrorInFlight is the mirror's count of queued and running
// CI runs, taken at countedAt; flights the tool started since then (or just
// before, while the mirror registered them) are added on top.
func Next(s *state.State, mirrorInFlight int, countedAt time.Time, maxRuns int) []Start {
	free := maxRuns - InFlight(s, mirrorInFlight, countedAt)
	var starts []Start
	for _, e := range s.Queue {
		for _, pr := range e.Pending {
			if len(starts) >= free {
				return starts
			}
			starts = append(starts, Start{GHSA: e.GHSA, PR: pr})
		}
	}
	return starts
}

// InFlight is the number of CI runs in flight: the mirror's count taken at
// countedAt, plus flights the tool started since (or just before, while the
// mirror registered them).
func InFlight(s *state.State, mirrorInFlight int, countedAt time.Time) int {
	n := mirrorInFlight
	for _, f := range s.InFlight {
		if f.StartedAt.After(countedAt.Add(-registrationLag)) {
			n++
		}
	}
	return n
}

// Reconcile drops queued and in-flight work that no longer exists in the
// snapshot, retires flights whose run has finished, and gives up on flights
// older than FlightTimeout. It returns notes for the screen.
func Reconcile(s *state.State, snap []model.Advisory, now time.Time) []string {
	byID := make(map[string]model.Advisory, len(snap))
	for _, a := range snap {
		byID[a.ID] = a
	}
	var notes []string
	queue := s.Queue[:0]
	for _, e := range s.Queue {
		a, ok := byID[e.GHSA]
		if !ok || a.Fork == "" {
			notes = append(notes, e.GHSA+" is no longer a draft with a fork; dropped from the queue")
			continue
		}
		pending := e.Pending[:0]
		for _, n := range e.Pending {
			if _, open := prByNumber(a, n); open {
				pending = append(pending, n)
			} else {
				notes = append(notes, fmt.Sprintf("%s #%d is no longer open; dropped", e.GHSA, n))
			}
		}
		e.Pending = pending
		queue = append(queue, e)
	}
	s.Queue = queue

	flights := s.InFlight[:0]
	for _, f := range s.InFlight {
		p, open := prByNumber(byID[f.GHSA], f.PR)
		switch {
		case !open:
		case runOver(f, p.CI):
			if rb, ok := s.Bases[f.Branch]; ok && rb.RunID == 0 && rb.SHA == f.BaseSHA {
				rb.RunID = p.CI.RunID
				s.Bases[f.Branch] = rb
			}
		case now.Sub(f.StartedAt) > FlightTimeout:
			notes = append(notes, fmt.Sprintf("%s #%d: no finished CI run after %s; stopped waiting", f.GHSA, f.PR, FlightTimeout))
		default:
			flights = append(flights, f)
		}
	}
	s.InFlight = flights
	return notes
}

// ClearFlights forgets an advisory's in-flight runs, for a run stuck in a way
// the tool can't see.
func ClearFlights(s *state.State, ghsa string) {
	s.InFlight = slices.DeleteFunc(s.InFlight, func(f state.Flight) bool { return f.GHSA == ghsa })
}

func prByNumber(a model.Advisory, n int) (model.FixPR, bool) {
	for _, p := range a.PRs {
		if p.Number == n {
			return p, true
		}
	}
	return model.FixPR{}, false
}

// runOver reports whether the flight's run is over: the PR's latest run tested
// the flight's head, has a result, and its latest attempt started at or after
// the flight did.
func runOver(f state.Flight, ci model.CI) bool {
	return ci.RunHeadSHA == f.HeadSHA && ci.State != model.CIRunning && ci.State != model.CINever &&
		!ci.StartedAt.Before(f.StartedAt)
}

// RecordStart moves PRs ghsa-ci.sh started from pending to in-flight, recording
// the base each run was triggered against. startedAt is when the tool began
// the run, before the script pushed anything. A PR whose CI didn't restart, or
// that conflicts with its base, is recorded as failed instead.
func RecordStart(s *state.State, ghsa string, results []runner.Result, startedAt time.Time) {
	for _, r := range results {
		switch {
		case r.Conflicting:
			RecordFailure(s, ghsa, []int{r.ForkPR}, "conflicts with "+r.Base+"; rebase the GHSA PR")
		case r.Action == "timeout":
			RecordFailure(s, ghsa, []int{r.ForkPR}, "GitHub didn't rebuild the merge commit on the new "+r.Base+" in time; mark it again later")
		case r.Action == "unchanged":
			RecordFailure(s, ghsa, []int{r.ForkPR}, "CI not restarted: this head was already tested on the current "+r.Base)
		default:
			RecordFlight(s, ghsa, r.ForkPR, r.MirrorBranch, r.HeadSHA, r.BaseSHA, startedAt, 0)
		}
	}
}

// RecordFlight records a fix PR's CI as in flight: started by ghsa-ci.sh, re-run
// through the API, or adopted while already running (then runID is known).
func RecordFlight(s *state.State, ghsa string, pr int, branch, head, base string, startedAt time.Time, runID int64) {
	ghsa = model.NormalizeID(ghsa)
	if i := find(s, ghsa); i >= 0 {
		s.Queue[i].Pending = slices.DeleteFunc(s.Queue[i].Pending, func(n int) bool { return n == pr })
		s.Queue[i].Started = append(s.Queue[i].Started, pr)
	}
	s.Bases[branch] = model.RecordedBase{SHA: base, Since: startedAt, RunID: runID}
	s.InFlight = append(s.InFlight, state.Flight{GHSA: ghsa, PR: pr, Branch: branch, HeadSHA: head, BaseSHA: base, StartedAt: startedAt})
}

// RecordFailure takes PRs out of pending and records why they couldn't start.
// They aren't retried automatically; the reasons stay until the next Mark.
func RecordFailure(s *state.State, ghsa string, prs []int, reason string) {
	if i := find(s, ghsa); i >= 0 {
		s.Queue[i].Pending = slices.DeleteFunc(s.Queue[i].Pending, func(n int) bool { return slices.Contains(prs, n) })
	}
	if s.Failures[ghsa] == nil {
		s.Failures[ghsa] = map[int]string{}
	}
	for _, n := range prs {
		s.Failures[ghsa][n] = reason
	}
}

// Finished returns the queued advisories with nothing left pending or in
// flight. The caller reports those that started anything, then unmarks them.
func Finished(s *state.State) []state.QueueEntry {
	busy := map[string]bool{}
	for _, f := range s.InFlight {
		busy[f.GHSA] = true
	}
	var done []state.QueueEntry
	for _, e := range s.Queue {
		if len(e.Pending) == 0 && !busy[e.GHSA] {
			done = append(done, e)
		}
	}
	return done
}
