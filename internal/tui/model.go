// Package tui is ghsa-manager's interactive screen (layout A): every draft
// advisory with its fix PRs, CI and reviews, and a CI queue run a few PRs at a
// time.
package tui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Joibel/ghsa-manager/internal/mirror"
	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/queue"
	"github.com/Joibel/ghsa-manager/internal/runner"
	"github.com/Joibel/ghsa-manager/internal/source"
	"github.com/Joibel/ghsa-manager/internal/state"
)

const (
	maxNotes = 3
	// runTimeout bounds a ghsa-ci.sh run, which can wait minutes for GitHub
	// to rebuild merge commits.
	runTimeout = 25 * time.Minute
	// callTimeout bounds a single re-run or report call.
	callTimeout = 2 * time.Minute
)

// Deps is everything the screen does outside itself. cmd wires the real
// GitHub, runner and reporter; tests pass fakes.
type Deps struct {
	Fetch     func(ctx context.Context, recordedBases map[string]model.RecordedBase) source.Snapshot
	InFlight  func(ctx context.Context) (int, error)
	Run       func(ctx context.Context, ghsa string, prs []int) ([]runner.Result, error)
	Rerun     func(ctx context.Context, runID int64) error
	Report    func(ctx context.Context, s *state.State, a model.Advisory) error
	MakeFork  func(ctx context.Context, ghsa string) error
	Open      func(url string) error
	Now       func() time.Time
	StatePath string
	Refresh   time.Duration
	MaxRuns   int
}

// Model is the screen's state.
type Model struct {
	countedAt time.Time
	snap      source.Snapshot
	// running is the advisory whose start is in progress, if any. Only one runs
	// at a time, so a YubiKey touch is never ambiguous.
	running     string
	confirmFork string
	err         string
	// selID and selPR are the selected row: an advisory, and its PR index or -1.
	// The cursor follows them when rows re-sort.
	selID          string
	notes          []string
	deps           Deps
	selPR          int
	mirrorInFlight int
	// width is the terminal's, for trimming titles; 0 until known.
	width int
}

// flight is a CI run re-run or adopted without ghsa-ci.sh.
type flight struct {
	startedAt          time.Time
	branch, head, base string
	pr                 int
	runID              int64
}

type (
	snapMsg struct {
		countedAt time.Time
		snap      source.Snapshot
		inFlight  int
	}
	runDoneMsg struct {
		startedAt time.Time
		err       error
		ghsa      string
		prs       []int // the PRs given to ghsa-ci.sh
		results   []runner.Result
		flights   []flight
	}
	noteMsg string
	tickMsg time.Time
)

// row is one screen line: an advisory, or one of its fix PRs (pr >= 0).
type row struct {
	adv model.Advisory
	pr  int
}

// New returns the screen.
func New(d Deps) Model {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Refresh == 0 {
		d.Refresh = 5 * time.Minute
	}
	return Model{deps: d, selPR: -1}
}

// Init starts the first fetch and the refresh ticker.
func (m Model) Init() tea.Cmd { return tea.Batch(m.fetch(), m.tick()) }

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.deps.Refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) fetch() tea.Cmd {
	d := m.deps
	return func() tea.Msg {
		ctx := context.Background()
		s, err := state.Load(d.StatePath)
		if err != nil {
			return snapMsg{snap: source.Snapshot{Err: err}}
		}
		snap := d.Fetch(ctx, s.Bases)
		countedAt := d.Now()
		n, err := d.InFlight(ctx)
		if err != nil && snap.Err == nil {
			snap = source.Snapshot{Err: err}
		}
		return snapMsg{snap: snap, inFlight: n, countedAt: countedAt}
	}
}

// Update handles keys, fetch results and finished starts.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.key(msg)
	case snapMsg:
		if msg.snap.Err != nil {
			m.err = msg.snap.Err.Error()
			return m, nil
		}
		m.err = ""
		m.snap, m.mirrorInFlight, m.countedAt = msg.snap, msg.inFlight, msg.countedAt
		if rows := m.rows(); m.selID == "" && len(rows) > 0 {
			m.selID, m.selPR = rows[0].adv.ID, rows[0].pr
		}
		return m.afterRefresh()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case runDoneMsg:
		return m.runDone(msg)
	case noteMsg:
		m.note(string(msg))
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.fetch(), m.tick())
	}
	return m, nil
}

func (m *Model) note(s string) {
	m.notes = append(m.notes, s)
	if len(m.notes) > maxNotes {
		m.notes = m.notes[len(m.notes)-maxNotes:]
	}
}

func (m *Model) updateState(fn func(*state.State) error) bool {
	if err := state.Update(m.deps.StatePath, fn); err != nil {
		m.err = err.Error()
		return false
	}
	return true
}

func (m Model) loadState() *state.State {
	s, err := state.Load(m.deps.StatePath)
	if err != nil {
		return state.Empty() // the error shows on the next state update
	}
	return s
}

// afterRefresh runs on every fresh snapshot: it drops vanished work, reports
// advisories whose runs are all done, and starts whatever the cap allows. It's
// the only place starts happen, so they always use a fresh in-flight count.
func (m Model) afterRefresh() (Model, tea.Cmd) {
	var notes []string
	var toReport []string
	now := m.deps.Now()
	m.updateState(func(s *state.State) error {
		notes = queue.Reconcile(s, m.snap.Advisories, now)
		for _, e := range queue.Finished(s) {
			switch {
			case e.GHSA == m.running:
			case len(e.Started) == 0:
				queue.Unmark(s, e.GHSA)
				notes = append(notes, e.GHSA+": nothing started, so nothing to report")
			default:
				toReport = append(toReport, e.GHSA)
			}
		}
		return nil
	})
	for _, n := range notes {
		m.note(n)
	}
	cmds := make([]tea.Cmd, 0, len(toReport)+1)
	for _, id := range toReport {
		cmds = append(cmds, m.report(id, true))
	}
	cmds = append(cmds, m.schedule())
	return m, tea.Batch(cmds...)
}

// report posts an advisory's CI-status comment, then unmarks it. The network
// call runs on a copy of the state, outside the state lock.
// report posts id's CI results on the advisory. unmark drops it from the queue
// afterwards, for a queued advisory whose runs have all finished.
func (m Model) report(id string, unmark bool) tea.Cmd {
	a, ok := m.advisory(id)
	if !ok {
		return nil
	}
	a.PRs = slices.Clone(a.PRs)
	failures := m.loadState().Failures[id]
	for i := range a.PRs {
		a.PRs[i].StartError = failures[a.PRs[i].Number]
	}
	d := m.deps
	return func() tea.Msg {
		local, err := state.Load(d.StatePath)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			err = d.Report(ctx, local, a)
			cancel()
		}
		if err == nil {
			err = state.Update(d.StatePath, func(s *state.State) error {
				if cid, ok := local.CommentIDs[id]; ok {
					s.CommentIDs[id] = cid
				}
				if unmark {
					queue.Unmark(s, id)
				}
				return nil
			})
		}
		if err != nil {
			return noteMsg(fmt.Sprintf("couldn't report %s: %v", id, err))
		}
		return noteMsg("reported CI for " + id + " on the advisory")
	}
}

// schedule starts the next advisory's PRs if a slot is free and no start is in
// progress. A PR already running on its current head is adopted; one that's
// red or cancelled on its current head and base is re-run through the API;
// the rest go through ghsa-ci.sh.
func (m *Model) schedule() tea.Cmd {
	if m.running != "" {
		return nil
	}
	starts := queue.Next(m.loadState(), m.mirrorInFlight, m.countedAt, m.deps.MaxRuns)
	if len(starts) == 0 {
		return nil
	}
	ghsa := starts[0].GHSA
	a, _ := m.advisory(ghsa)
	var adopt, rerun []flight
	var script []int
	for _, st := range starts {
		if st.GHSA != ghsa {
			continue
		}
		p, ok := prByNumber(a, st.PR)
		if !ok {
			script = append(script, st.PR)
			continue
		}
		f := flight{pr: p.Number, branch: mirror.BranchName(ghsa, p.Base, p.Number), head: p.HeadSHA, runID: p.CI.RunID}
		switch howToStart(p) {
		case adoptRun:
			f.startedAt = p.CI.StartedAt
			adopt = append(adopt, f)
		case rerunRun:
			f.base = p.CI.MergedBaseSHA
			rerun = append(rerun, f)
		case scriptRun:
			script = append(script, p.Number)
		}
	}
	m.running = ghsa
	d := m.deps
	return func() tea.Msg {
		done := runDoneMsg{ghsa: ghsa, startedAt: d.Now(), flights: adopt}
		for _, f := range rerun {
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			err := d.Rerun(ctx, f.runID)
			cancel()
			if err != nil { // e.g. too old to re-run: start it afresh instead
				script = append(script, f.pr)
				continue
			}
			f.startedAt = done.startedAt
			done.flights = append(done.flights, f)
		}
		if len(script) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
			done.prs = script
			done.results, done.err = d.Run(ctx, ghsa, script)
			cancel()
		}
		return done
	}
}

type startKind int

const (
	scriptRun startKind = iota
	adoptRun
	rerunRun
)

// howToStart decides how to get CI for a PR: adopt a run already going on its
// head, re-run a red or cancelled one that tested its head on the current base,
// or start afresh through ghsa-ci.sh.
func howToStart(p model.FixPR) startKind {
	if p.CI.RunID == 0 || p.CI.RunHeadSHA != p.HeadSHA {
		return scriptRun
	}
	switch p.CI.State {
	case model.CIRunning:
		return adoptRun
	case model.CIRed, model.CICancelled:
		if p.CI.MergedBaseSHA != "" && !p.CI.BaseMoved() {
			return rerunRun
		}
	case model.CINever, model.CIGreen, model.CIUnconfirmed:
	}
	return scriptRun
}

func prByNumber(a model.Advisory, n int) (model.FixPR, bool) {
	for _, p := range a.PRs {
		if p.Number == n {
			return p, true
		}
	}
	return model.FixPR{}, false
}

func (m Model) runDone(msg runDoneMsg) (Model, tea.Cmd) {
	m.running = ""
	m.updateState(func(s *state.State) error {
		for _, f := range msg.flights {
			queue.RecordFlight(s, msg.ghsa, f.pr, f.branch, f.head, f.base, f.startedAt, f.runID)
		}
		queue.RecordStart(s, msg.ghsa, msg.results, msg.startedAt)
		var rest []int
		for _, n := range msg.prs {
			if !slices.ContainsFunc(msg.results, func(r runner.Result) bool { return r.ForkPR == n }) {
				rest = append(rest, n)
			}
		}
		switch {
		case len(rest) == 0:
		case msg.err != nil:
			queue.RecordFailure(s, msg.ghsa, rest, msg.err.Error())
		default:
			queue.RecordFailure(s, msg.ghsa, rest, "ghsa-ci.sh reported no result")
		}
		return nil
	})
	if msg.err != nil {
		m.note(fmt.Sprintf("%s: %v", msg.ghsa, msg.err))
	}
	// The next start waits for a fresh in-flight count.
	return m, m.fetch()
}

func (m Model) advisory(id string) (model.Advisory, bool) {
	for _, a := range m.snap.Advisories {
		if a.ID == id {
			return a, true
		}
	}
	return model.Advisory{}, false
}

func (m Model) rows() []row {
	var rows []row
	for _, a := range m.snap.Advisories { // oldest first; never re-sorted
		rows = append(rows, row{adv: a, pr: -1})
		for i := range a.PRs {
			rows = append(rows, row{adv: a, pr: i})
		}
	}
	return rows
}

// cursor is the selected row's index, found by advisory and PR so it survives
// re-sorting.
func (m Model) cursor(rows []row) int {
	for i, r := range rows {
		if r.adv.ID == m.selID && r.pr == m.selPR {
			return i
		}
	}
	for i, r := range rows { // the PR row went away: fall back to its advisory
		if r.adv.ID == m.selID && r.pr == -1 {
			return i
		}
	}
	return 0
}

func (m Model) selected() (row, bool) {
	rows := m.rows()
	if len(rows) == 0 {
		return row{}, false
	}
	return rows[m.cursor(rows)], true
}

// moveCursor selects the row delta rows away.
func (m *Model) moveCursor(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		return
	}
	i := max(0, min(len(rows)-1, m.cursor(rows)+delta))
	m.selID, m.selPR = rows[i].adv.ID, rows[i].pr
}
