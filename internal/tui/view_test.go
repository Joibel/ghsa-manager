package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stretchr/testify/assert"
)

func TestView_LayoutA(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 1}
	m := load(t, newModel(t, f), f)
	m = key(m, "c") // queue GHSA-aaaa; one slot of three is taken in the mirror
	v := m.View()
	for _, want := range []string{
		"GHSA-aaaa-bbbb-cccc",
		"needs CI",
		"⚠ main not green",
		"release-4.1  #2",
		"✓ green 2h ago",
		"alice approved (stale)",
		"GHSA-gggg-hhhh-iiii",
		"no fork",
		"running 1/3",
		"#1   mallory-fixer",
		"AUTHOR",
	} {
		assert.Contains(t, v, want)
	}
}

func TestView_AdvisoryTitles(t *testing.T) {
	f := &fakes{snap: snapshot(), inFlight: 3}
	m := load(t, newModel(t, f), f)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = asModel(t, next)
	v := m.View()
	assert.Contains(t, v, "Workflow archive lookup crosses namespaces")
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "GHSA-gggg-hhhh-iiii") {
			assert.LessOrEqual(t, lipgloss.Width(line), 100, "a long title is trimmed to the terminal width")
			assert.Contains(t, line, "…")
		}
	}
}
