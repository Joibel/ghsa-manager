package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/render"
	"github.com/Joibel/ghsa-manager/internal/state"
)

const checklistHelp = "space tick/untick · C request CVE · a Claude · esc back"

// detailIndent lines an item's extra detail lines up under its first one:
// " ✓ " plus the label column.
const detailIndent = 16

// listLine is one cursor line of the checklist screen: an item, or one branch
// of the branches item.
type listLine struct {
	item   model.Item
	branch string
}

// listLines is the checklist screen's cursor lines, in model.Items order. The
// branches item is a header line, then one line per branch.
func (Model) listLines(cl model.Checklist) []listLine {
	lines := make([]listLine, 0, len(cl.Results))
	for _, r := range cl.Results {
		lines = append(lines, listLine{item: r.Item})
		if r.Item == model.ItemBranches {
			for _, b := range r.Branches {
				lines = append(lines, listLine{item: r.Item, branch: b.Branch})
			}
		}
	}
	return lines
}

// checklist evaluates an advisory's checklist from the state file.
func (m Model) checklist(a model.Advisory) model.Checklist {
	return model.Evaluate(a, m.snap.Releases, m.deps.Supported, m.loadState().Checks[a.ID])
}

// listKey handles a key on the checklist screen.
func (m Model) listKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	a, ok := m.advisory(m.listID)
	if !ok {
		m.listID = ""
		return m, nil
	}
	lines := m.listLines(m.checklist(a))
	m.listCursor = min(m.listCursor, len(lines)-1)
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.listID = ""
	case "up", "k":
		m.listCursor = max(0, m.listCursor-1)
	case "down", "j":
		m.listCursor = min(len(lines)-1, m.listCursor+1)
	case " ":
		line, now := lines[m.listCursor], m.deps.Now()
		m.updateState(func(s *state.State) error {
			c := s.Checks[a.ID]
			c.Toggle(a, m.snap.Releases, m.deps.Supported, line.item, line.branch, now)
			s.Checks[a.ID] = c
			return nil
		})
	case "C":
		m.confirmCVE = true
	case "r":
		return m, m.fetch()
	}
	return m, nil
}

// confirmCVERequest answers the CVE request's y/n prompt.
func (m Model) confirmCVERequest(msg tea.KeyMsg) (Model, tea.Cmd) {
	m.confirmCVE = false
	if msg.String() != "y" {
		return m, nil
	}
	id, request := m.listID, m.deps.RequestCVE
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		if err := request(ctx, id); err != nil {
			return noteMsg(err.Error())
		}
		return noteMsg("requested a CVE for " + id)
	}
}

// checklistView is the checklist screen (mockup C): a header, then one line
// per cursor line, each with its mark, label and detail.
func (m Model) checklistView(st styles, a model.Advisory, cl model.Checklist, c model.Checks) string {
	results := make(map[model.Item]model.ItemResult, len(cl.Results))
	for _, r := range cl.Results {
		results[r.Item] = r
	}
	head := fmt.Sprintf(" %s  %s  %s", a.ID, render.CountText(cl),
		statusStyle(st, a.StatusWith(cl)).Render(render.StatusText(a, cl)))
	var b strings.Builder
	b.WriteString(head + m.title(a, lipgloss.Width(head)) + "\n\n")
	lines := m.listLines(cl)
	cursor := min(m.listCursor, len(lines)-1)
	for i, l := range lines {
		r := results[l.item]
		var text string
		var more []string
		if l.branch != "" {
			text = branchText(st, r, l.branch)
		} else {
			var detail string
			detail, more = itemDetail(st, a, r, c)
			text = fmt.Sprintf(" %s %-12s %s", stateMark(st, r.State), l.item, detail)
		}
		if i == cursor {
			text = st.selected.Render(text)
		}
		b.WriteString(text + "\n")
		for _, d := range more {
			b.WriteString(strings.Repeat(" ", detailIndent) + d + "\n")
		}
	}
	return b.String()
}

func stateMark(st styles, s model.ItemState) string {
	switch s {
	case model.ItemDone:
		return st.good.Render("✓")
	case model.ItemWaived:
		return st.dim.Render("~")
	case model.ItemStale:
		return st.warn.Render("!")
	case model.ItemOpen:
	}
	return st.bad.Render("✗")
}

// branchText is one branch's line under the branches item.
func branchText(st styles, r model.ItemResult, branch string) string {
	var bl model.BranchLine
	for _, x := range r.Branches {
		if x.Branch == branch {
			bl = x
		}
	}
	mark, detail := stateMark(st, model.ItemOpen), "missing"
	switch {
	case bl.PR != 0:
		mark, detail = stateMark(st, model.ItemDone), fmt.Sprintf("#%d", bl.PR)
		if !r.Expected[branch] {
			detail += " (not expected)"
		}
	case bl.Waived:
		mark, detail = stateMark(st, model.ItemWaived), "waived"
	}
	return fmt.Sprintf("   %s %-12s %s", mark, branch, detail)
}

// itemDetail is an item's detail: the text after its label, and any lines to
// show under it.
func itemDetail(st styles, a model.Advisory, r model.ItemResult, c model.Checks) (string, []string) {
	switch r.Item {
	case model.ItemBranches:
		return branchesSummary(r), nil
	case model.ItemDescription:
		if r.Note != "" {
			return r.Note, nil
		}
		return tickText(r, c), nil
	case model.ItemCVSS:
		value := a.CVSSVector
		if a.CVSSScore > 0 {
			value = fmt.Sprintf("%.1f %s", a.CVSSScore, value)
		}
		return valueDetail(value, r, c), nil
	case model.ItemCVE:
		switch {
		case a.CVEID != "":
			return a.CVEID, nil
		case !c.CVERequested.IsZero():
			return "requested " + day(c.CVERequested), nil
		}
		return "not requested", nil
	case model.ItemVersions:
		more := make([]string, 0, len(a.Vulns))
		for _, v := range a.Vulns {
			if strings.TrimSpace(v.Range) != "" {
				more = append(more, v.Range+" → "+v.Patched)
			}
		}
		return listDetail(len(more) > 0, r, c), more
	case model.ItemCredits:
		more := make([]string, 0, len(a.Credits)+len(r.Hints))
		for _, cr := range a.Credits {
			more = append(more, fmt.Sprintf("%s (%s, %s)", cr.User, cr.Type, cr.State))
		}
		detail := listDetail(len(more) > 0, r, c)
		for _, h := range r.Hints {
			more = append(more, st.dim.Render("suggest: "+h.User+" as "+h.Type))
		}
		return detail, more
	}
	return "", nil
}

// branchesSummary is the branches item's detail: each expected branch's PR or
// "missing", or why the expected branches aren't known.
func branchesSummary(r model.ItemResult) string {
	if r.Note != "" {
		return r.Note
	}
	var parts []string
	for _, bl := range r.Branches {
		switch {
		case !r.Expected[bl.Branch]:
		case bl.PR != 0:
			parts = append(parts, fmt.Sprintf("%s #%d ✓", bl.Branch, bl.PR))
		case bl.Waived:
			parts = append(parts, bl.Branch+" waived")
		default:
			parts = append(parts, bl.Branch+" missing")
		}
	}
	return strings.Join(parts, " · ")
}

// valueDetail is a one-line value's detail: the value (or "not set"), then
// its tick.
func valueDetail(value string, r model.ItemResult, c model.Checks) string {
	switch {
	case value == "" && r.State == model.ItemOpen:
		return "not set"
	case value == "":
		return "not set · " + tickText(r, c)
	}
	return value + " · " + tickText(r, c)
}

// listDetail is the detail of a value shown as lines under its item: its tick,
// or "not set".
func listDetail(set bool, r model.ItemResult, c model.Checks) string {
	if set {
		return tickText(r, c)
	}
	return valueDetail("", r, c)
}

// tickText says whether an item is ticked, and when.
func tickText(r model.ItemResult, c model.Checks) string {
	switch r.State {
	case model.ItemDone:
		return "ticked " + day(c.Ticks[r.Item].At)
	case model.ItemStale:
		return "ticked (changed since)"
	case model.ItemWaived:
		return "waived " + day(c.Waived[string(r.Item)])
	case model.ItemOpen:
	}
	return "not ticked"
}

func day(t time.Time) string { return t.Format("2 Jan") }
