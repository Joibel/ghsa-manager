package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/queue"
	"github.com/Joibel/ghsa-manager/internal/render"
	"github.com/Joibel/ghsa-manager/internal/state"
)

type styles struct {
	header, selected, dim, warn, bad, good lipgloss.Style
}

func newStyles() styles {
	return styles{
		header:   lipgloss.NewStyle().Faint(true),
		selected: lipgloss.NewStyle().Reverse(true),
		dim:      lipgloss.NewStyle().Faint(true),
		warn:     lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		bad:      lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		good:     lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
	}
}

func statusStyle(st styles, s model.Status) lipgloss.Style {
	switch s {
	case model.Ready:
		return st.good
	case model.NoFork:
		return st.bad
	case model.NeedsCI, model.NeedsReview, model.ChecklistOpen:
		return st.warn
	case model.ForkNoPRs:
		return st.dim
	}
	return st.dim
}

// View renders layout A and the footer.
func (m Model) View() string {
	st := newStyles()
	s := m.loadState()
	queueIDs := make([]string, 0, len(s.Queue))
	for _, e := range s.Queue {
		queueIDs = append(queueIDs, e.GHSA)
	}
	lists := render.Checklists(m.snap, s.Checks, m.deps.Supported)
	var b strings.Builder
	b.WriteString(st.header.Render(fmt.Sprintf(" %-2s %-20s %-14s %-4s %-12s %-4s %-15s %-30s %s",
		"Q", "GHSA", "STATE", "REL", "BRANCH", "PR", "AUTHOR", "CI", "REVIEWS")) + "\n")
	rows := m.rows()
	cursor := m.cursor(rows)
	for i, r := range rows {
		line := m.rowText(st, r, s, queueIDs, lists[r.adv.ID])
		if i == cursor {
			line = st.selected.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + m.footer(s) + "\n")
	return b.String()
}

func (m Model) rowText(st styles, r row, s *state.State, queueIDs []string, cl model.Checklist) string {
	if r.pr < 0 {
		a := r.adv
		fork := render.ForkText(a)
		if a.MainNotGreen() && cherryPicksBusy(a, s) {
			fork += "  " + st.warn.Render("⚠ main not green")
		}
		relStyle := st.warn
		if cl.Complete() {
			relStyle = st.good
		}
		line := fmt.Sprintf(" %-2s %-20s %s %s %s", render.QueueLabel(a.ID, queueIDs), a.ID,
			statusStyle(st, a.StatusWith(cl)).Render(fmt.Sprintf("%-14s", render.StatusText(a, cl))),
			relStyle.Render(fmt.Sprintf("%-4s", render.CountText(cl))), fork)
		return line + m.title(a, lipgloss.Width(line))
	}
	p := r.adv.PRs[r.pr]
	p.StartError = s.Failures[r.adv.ID][p.Number]
	return fmt.Sprintf("%45s%-12s #%-3d %-15s %-30s %s", "", p.Base, p.Number, render.Truncate(p.Author, 15),
		render.CIText(p, m.deps.Now()), render.ReviewsText(p))
}

// title is the advisory's title, after two spaces, trimmed to the terminal
// width left after the rest of the line (used columns wide).
func (m Model) title(a model.Advisory, used int) string {
	if a.Summary == "" {
		return ""
	}
	room := render.DefaultTitleWidth
	if m.width > 0 {
		room = m.width - used - 2
	}
	if room < 4 {
		return ""
	}
	return "  " + render.Truncate(a.Summary, room)
}

// cherryPicksBusy reports whether any non-main PR of the advisory is queued or
// in flight.
func cherryPicksBusy(a model.Advisory, s *state.State) bool {
	base := map[int]string{}
	for _, p := range a.PRs {
		base[p.Number] = p.Base
	}
	for _, e := range s.Queue {
		if e.GHSA != a.ID {
			continue
		}
		for _, n := range e.Pending {
			if base[n] != "main" {
				return true
			}
		}
	}
	for _, f := range s.InFlight {
		if f.GHSA == a.ID && base[f.PR] != "main" {
			return true
		}
	}
	return false
}

func (m Model) footer(s *state.State) string {
	queued := 0
	for _, e := range s.Queue {
		queued += len(e.Pending)
	}
	inFlight := queue.InFlight(s, m.mirrorInFlight, m.countedAt)
	line := fmt.Sprintf(" running %d/%d · queued %d", inFlight, m.deps.MaxRuns, queued)
	if m.running != "" {
		line += " · pushing " + m.running + " (touch YubiKey)"
	}
	if !m.snap.FetchedAt.IsZero() {
		line += " · fetched " + m.snap.FetchedAt.Format("15:04")
	}
	lines := []string{line}
	if m.err != "" {
		lines = append(lines, " error: "+m.err)
	}
	for _, n := range m.notes {
		lines = append(lines, " "+n)
	}
	if m.confirmFork != "" {
		lines = append(lines, " make a fork for "+m.confirmFork+"? y/n")
	} else {
		lines = append(lines, " "+help)
	}
	return strings.Join(lines, "\n")
}
