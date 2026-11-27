package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/source"
	"github.com/Joibel/ghsa-manager/internal/state"
)

// openList loads the snapshot and opens idA's checklist screen.
func openList(t *testing.T, f *fakes) Model {
	t.Helper()
	m := load(t, newModel(t, f), f)
	m = key(m, "l")
	require.Equal(t, idA, m.listID)
	return m
}

// cursorTo moves the checklist cursor down to the line for it and branch.
func cursorTo(t *testing.T, m Model, it model.Item, branch string) Model {
	t.Helper()
	a, ok := m.advisory(m.listID)
	require.True(t, ok)
	lines := m.listLines(m.checklist(a))
	for range lines {
		if l := lines[m.listCursor]; l.item == it && l.branch == branch {
			return m
		}
		m = key(m, "j")
	}
	t.Fatalf("no checklist line for %s %q", it, branch)
	return m
}

// lineWith is the first view line containing s.
func lineWith(t *testing.T, view, s string) string {
	t.Helper()
	for line := range strings.SplitSeq(view, "\n") {
		if strings.Contains(line, s) {
			return line
		}
	}
	t.Fatalf("no line contains %q in:\n%s", s, view)
	return ""
}

func checksOf(t *testing.T, f *fakes) model.Checks {
	t.Helper()
	s, err := state.Load(f.statePath)
	require.NoError(t, err)
	return s.Checks[idA]
}

func TestChecklistOpensAndCloses(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	m = key(m, "down") // a PR row of GHSA-aaaa opens its advisory's checklist
	m = key(m, "l")
	assert.Equal(t, idA, m.listID)
	assert.Equal(t, 0, m.listCursor)
	v := m.View()
	assert.Contains(t, v, checklistHelp)
	assert.NotContains(t, v, "AUTHOR", "the main screen's table isn't shown")

	m = key(m, "esc")
	assert.Empty(t, m.listID)
	assert.Contains(t, m.View(), "AUTHOR")

	m = key(m, "l")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = asModel(t, next)
	assert.Nil(t, cmd, "q goes back, it doesn't quit")
	assert.Empty(t, m.listID)
}

func TestChecklistDetails(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	v := openList(t, f).View()
	assert.Contains(t, lineWith(t, v, "branches"), "✗")
	assert.Contains(t, v, "main #1 ✓ · release-4.2 missing · release-4.1 #2 ✓ · release-4.0 #3 ✓")
	assert.Contains(t, lineWith(t, v, "release-4.2  missing"), "✗")
	assert.Contains(t, lineWith(t, v, "description"), "not ticked")
	assert.Contains(t, lineWith(t, v, "cvss"), "6.5 "+cvssA)
	assert.Contains(t, lineWith(t, v, "cve"), "not requested")
	assert.Contains(t, v, ">= 4.0.0, < 4.0.7 → 4.0.7")
	assert.Contains(t, v, ">= 4.1.0, < 4.1.3 → 4.1.3")
	assert.Contains(t, v, "eve-finder (finder, accepted)")
	assert.Contains(t, v, "suggest: mallory-fixer as remediation_developer")
}

func TestChecklistTickPersists(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := cursorTo(t, openList(t, f), model.ItemDescription, "")
	m = key(m, "space")
	tick, ok := checksOf(t, f).Ticks[model.ItemDescription]
	require.True(t, ok)
	assert.Equal(t, now(), tick.At.UTC())
	line := lineWith(t, m.View(), "description")
	assert.Contains(t, line, "✓")
	assert.Contains(t, line, "ticked 26 Nov")

	m = key(m, "space")
	assert.NotContains(t, checksOf(t, f).Ticks, model.ItemDescription)
	assert.Contains(t, lineWith(t, m.View(), "description"), "✗")
}

func TestChecklistWaiveBranch(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := cursorTo(t, openList(t, f), model.ItemBranches, "release-4.2")
	m = key(m, "space")
	assert.Contains(t, checksOf(t, f).Waived, "branch:release-4.2")
	assert.Contains(t, lineWith(t, m.View(), "release-4.2  waived"), "~")
}

func TestChecklistRequestCVE(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := openList(t, f)
	m = key(m, "C")
	assert.Contains(t, m.View(), "request a CVE for "+idA+"? y/n")
	m = key(m, "n")
	assert.Empty(t, f.cves)
	assert.Contains(t, m.View(), checklistHelp)

	m = key(m, "C")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = asModel(t, next)
	require.NotNil(t, cmd)
	next, _ = m.Update(cmd())
	m = asModel(t, next)
	assert.Equal(t, []string{idA}, f.cves)
	assert.Contains(t, m.View(), "requested a CVE for "+idA)
}

func TestChecklistRequestCVEFails(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3, cveErr: errors.New("requesting a CVE for " + idA + ": 422")}
	m := key(openList(t, f), "C")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	require.NotNil(t, cmd)
	next, _ := m.Update(cmd())
	assert.Contains(t, asModel(t, next).View(), "requesting a CVE for "+idA+": 422")
}

// An advisory that has a CVE needs no request: C says so and doesn't ask.
func TestChecklistRequestCVEHasOne(t *testing.T) {
	snap := snapshot()
	snap.Advisories[0].CVEID = "CVE-2026-1234"
	f := &fakes{snap: snap, inFlight: 3}
	m := key(openList(t, f), "C")
	v := m.View()
	assert.Contains(t, v, idA+" already has CVE-2026-1234")
	assert.NotContains(t, v, "y/n")
	assert.Contains(t, v, checklistHelp)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	asModel(t, next)
	assert.Nil(t, cmd)
	assert.Empty(t, f.cves)
}

// A request already recorded is mentioned, with its date, before asking again.
func TestChecklistRequestCVEAgain(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := openList(t, f)
	require.NoError(t, state.Update(f.statePath, func(s *state.State) error {
		c := s.Checks[idA]
		c.MarkCVERequested(now())
		s.Checks[idA] = c
		return nil
	}))
	m = key(m, "C")
	assert.Contains(t, m.View(), "a CVE was already requested for "+idA+" on 26 Nov; request again? y/n")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = asModel(t, next)
	require.NotNil(t, cmd)
	next, _ = m.Update(cmd())
	asModel(t, next)
	assert.Equal(t, []string{idA}, f.cves)
}

// The CVE line reflects a request recorded in the state file.
func TestChecklistCVERequested(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := openList(t, f)
	require.NoError(t, state.Update(f.statePath, func(s *state.State) error {
		c := s.Checks[idA]
		c.MarkCVERequested(now())
		s.Checks[idA] = c
		return nil
	}))
	line := lineWith(t, m.View(), "cve")
	assert.Contains(t, line, "requested 26 Nov")
	assert.Contains(t, line, "✓")
}

func TestChecklistVanished(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := openList(t, f)
	f.snap = source.Snapshot{FetchedAt: now(), Advisories: snapshot().Advisories[1:]}
	m = load(t, m, f)
	assert.Empty(t, m.listID)
	assert.Contains(t, m.View(), idA+" is no longer a draft")
}

func TestChecklistStale(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := openList(t, f)
	require.NoError(t, state.Update(f.statePath, func(s *state.State) error {
		s.Checks[idA] = model.Checks{Ticks: map[model.Item]model.Tick{
			model.ItemCVSS: {At: now().Add(-24 * time.Hour), Hash: "old"},
		}}
		return nil
	}))
	line := lineWith(t, m.View(), "cvss")
	assert.Contains(t, line, "ticked (changed since)")
	assert.Contains(t, line, "!")
}

// pressA presses a and delivers the session note its command returns.
func pressA(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	require.NotNil(t, cmd)
	next, _ = asModel(t, next).Update(cmd())
	return asModel(t, next)
}

func TestSessionKey(t *testing.T) {
	// idA's open items: branches (release-4.2 has no PR) and the unticked
	// values; its CVE isn't requested.
	wantOpen := []model.Item{model.ItemBranches, model.ItemDescription, model.ItemCVSS, model.ItemCVE, model.ItemVersions, model.ItemCredits}

	f := &fakes{snap: snapshot(), inFlight: 3}
	m := pressA(t, load(t, newModel(t, f), f))
	assert.Equal(t, []session{{id: idA, open: wantOpen}}, f.sessions)
	assert.Contains(t, m.View(), "Started Claude on "+idA)

	// On the checklist screen, a ticked item isn't open, and a stale one is.
	f = &fakes{snap: snapshot(), inFlight: 3}
	m = openList(t, f)
	m = key(cursorTo(t, m, model.ItemDescription, ""), "space")
	m = key(cursorTo(t, m, model.ItemCVSS, ""), "space")
	snap := snapshot()
	snap.Advisories[0].CVSSVector = "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:N/A:N"
	f.snap = snap
	m = load(t, m, f)
	m = pressA(t, m)
	assert.Equal(t, []session{{id: idA, open: []model.Item{
		model.ItemBranches, model.ItemCVSS, model.ItemCVE, model.ItemVersions, model.ItemCredits,
	}}}, f.sessions)
	assert.Equal(t, idA, m.listID, "a keeps the checklist screen open")

	f.sessionErr = errors.New("not inside tmux")
	m = pressA(t, m)
	assert.Contains(t, m.View(), "not inside tmux")
}
