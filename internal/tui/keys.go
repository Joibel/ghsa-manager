package tui

import (
	"context"
	"fmt"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/queue"
	"github.com/Joibel/ghsa-manager/internal/state"
)

const help = "c mark for CI · u unmark · t/b send to top/bottom of queue · x clear stuck runs · R report CI now · l checklist · a Claude · enter open advisory · p open PR · f make fork · r refresh · q quit"

func (m Model) key(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case m.confirmFork != "":
		return m.confirm(msg)
	case m.confirmCVE:
		return m.confirmCVERequest(msg)
	case m.listID != "":
		return m.listKey(msg)
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "r":
		return m, m.fetch()
	default:
		if sel, ok := m.selected(); ok {
			return m.action(msg.String(), sel)
		}
	}
	return m, nil
}

// action applies a key to the selected row's advisory or PR.
func (m Model) action(k string, sel row) (Model, tea.Cmd) {
	switch k {
	case "c":
		return m.mark(sel)
	case "u":
		m.updateState(func(s *state.State) error { queue.Unmark(s, sel.adv.ID); return nil })
	case "t":
		m.updateState(func(s *state.State) error { queue.ToTop(s, sel.adv.ID); return nil })
	case "b":
		m.updateState(func(s *state.State) error { queue.ToBottom(s, sel.adv.ID); return nil })
	case "R":
		return m.reportNow(sel)
	case "x":
		m.updateState(func(s *state.State) error { queue.ClearFlights(s, sel.adv.ID); return nil })
		m.note(sel.adv.ID + ": forgot its in-flight runs")
	case "enter", "o":
		m.open(sel.adv.URL)
	case "p":
		if len(sel.adv.PRs) > 0 {
			m.open(sel.adv.PRs[max(0, sel.pr)].URL)
		}
	case "f":
		if sel.adv.Fork == "" {
			m.confirmFork = sel.adv.ID
		}
	case "l":
		m.listID, m.listCursor = sel.adv.ID, 0
	case "a":
		return m, m.openSession(sel.adv)
	}
	return m, nil
}

// openSession opens a's Claude session, off the Bubble Tea loop: the tmux
// calls can block briefly. The session's prompt names the checklist's open and
// stale items.
func (m Model) openSession(a model.Advisory) tea.Cmd {
	var open []model.Item
	for _, r := range m.checklist(a).Results {
		if r.State == model.ItemOpen || r.State == model.ItemStale {
			open = append(open, r.Item)
		}
	}
	openSession := m.deps.OpenSession
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		note, err := openSession(ctx, a, open)
		if err != nil {
			return noteMsg(fmt.Sprintf("couldn't open a Claude session for %s: %v", a.ID, err))
		}
		return noteMsg(note)
	}
}

func (m Model) mark(sel row) (Model, tea.Cmd) {
	var marked bool
	if !m.updateState(func(s *state.State) error { marked = queue.Mark(s, sel.adv); return nil }) {
		return m, nil
	}
	if !marked {
		m.note(sel.adv.ID + ": nothing to run (already queued, or every PR is green on its head)")
		return m, nil
	}
	// Starts wait for a fresh in-flight count.
	return m, m.fetch()
}

// reportNow posts the advisory's current CI results without running anything,
// for CI started outside the tool. It needs at least one PR with a run.
func (m Model) reportNow(sel row) (Model, tea.Cmd) {
	if !slices.ContainsFunc(sel.adv.PRs, func(p model.FixPR) bool { return p.CI.State != model.CINever }) {
		m.note(sel.adv.ID + ": no CI has run yet, so nothing to report")
		return m, nil
	}
	m.note(sel.adv.ID + ": reporting CI on the advisory")
	return m, m.report(sel.adv.ID, false)
}

func (m *Model) open(url string) {
	if url == "" {
		return
	}
	if err := m.deps.Open(url); err != nil {
		m.note("couldn't open " + url + ": " + err.Error())
	}
}

func (m Model) confirm(msg tea.KeyMsg) (Model, tea.Cmd) {
	id := m.confirmFork
	m.confirmFork = ""
	if msg.String() != "y" {
		return m, nil
	}
	makeFork := m.deps.MakeFork
	return m, func() tea.Msg {
		if err := makeFork(context.Background(), id); err != nil {
			return noteMsg(fmt.Sprintf("couldn't make a fork for %s: %v", id, err))
		}
		return noteMsg("made a fork for " + id + "; refresh with r")
	}
}
