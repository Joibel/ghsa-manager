// Package render turns snapshots into text: the plain `status` output, and
// the cell texts the TUI shares.
package render

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/source"
)

// Checklists evaluates every advisory's release checklist, keyed by GHSA ID.
func Checklists(snap source.Snapshot, checks map[string]model.Checks, supported int) map[string]model.Checklist {
	out := make(map[string]model.Checklist, len(snap.Advisories))
	for _, a := range snap.Advisories {
		out[a.ID] = model.Evaluate(a, snap.Releases, supported, checks[a.ID])
	}
	return out
}

// StatusText is an advisory's status word; a Ready advisory with an open
// checklist reads "checklist n/6".
func StatusText(a model.Advisory, cl model.Checklist) string {
	s := a.StatusWith(cl)
	if s == model.ChecklistOpen {
		return s.String() + " " + CountText(cl)
	}
	return s.String()
}

// CountText is the checklist's REL cell: "3/6".
func CountText(cl model.Checklist) string {
	done, total := cl.Count()
	return fmt.Sprintf("%d/%d", done, total)
}

// CIText is a PR's CI cell: result, age and notes.
func CIText(p model.FixPR, now time.Time) string {
	var s string
	switch p.CI.State {
	case model.CINever:
		return "never"
	case model.CIRunning:
		s = "● running"
	case model.CIGreen:
		s = "✓ green"
	case model.CIRed:
		s = "✗ red"
	case model.CICancelled:
		s = "○ cancelled"
	case model.CIUnconfirmed:
		s = "? unconfirmed"
	}
	if !p.CI.FinishedAt.IsZero() {
		s += " " + Age(now.Sub(p.CI.FinishedAt))
	}
	var notes []string
	if p.CI.Stale(p.HeadSHA) {
		notes = append(notes, "stale")
	}
	if p.CI.BaseMoved() {
		notes = append(notes, "base moved")
	}
	if len(p.CI.SkippedTestJobs) > 0 {
		notes = append(notes, "skipped: "+strings.Join(p.CI.SkippedTestJobs, ", "))
	}
	if p.StartError != "" {
		notes = append(notes, "start failed: "+p.StartError)
	}
	if len(notes) > 0 {
		s += " (" + strings.Join(notes, "; ") + ")"
	}
	return s
}

// ReviewsText is a PR's reviews cell: each reviewer's latest counting review.
func ReviewsText(p model.FixPR) string {
	latest := p.LatestReviews()
	users := make([]string, 0, len(latest))
	for u := range latest {
		users = append(users, u)
	}
	sort.Strings(users)
	parts := make([]string, 0, len(users))
	for _, u := range users {
		r := latest[u]
		verb := "approved"
		icon := "✓"
		if r.State == model.ReviewChangesRequested {
			verb, icon = "changes requested", "✗"
		}
		if r.Stale(p.HeadSHA) {
			parts = append(parts, fmt.Sprintf("%s %s (stale)", u, verb))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s %s", icon, u, verb))
	}
	return strings.Join(parts, ", ")
}

// Age is a short "how long ago".
func Age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// ForkText is an advisory's fork cell.
func ForkText(a model.Advisory) string {
	if a.Fork == "" {
		return ""
	}
	return "fork ✓"
}

// DefaultTitleWidth is how much of an advisory's title to show when the
// terminal width isn't known.
const DefaultTitleWidth = 60

// Truncate shortens s to at most n runes, ending in "…" when it cuts.
func Truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if n <= 0 {
		return ""
	}
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
