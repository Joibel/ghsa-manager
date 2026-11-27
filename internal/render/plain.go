package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/source"
)

const (
	advisoryRow = " %-2s %-20s %-14s %-4s %s"
	prRow       = "%45s%-12s #%-3d %-15s %-30s %s"
)

// QueueLabel is an advisory's queue cell: its 1-based position, or "·".
func QueueLabel(id string, queue []string) string {
	for i, q := range queue {
		if q == id {
			return fmt.Sprint(i + 1)
		}
	}
	return "·"
}

// Plain writes the snapshot as layout A, without colour.
func Plain(w io.Writer, snap source.Snapshot, queue []string, lists map[string]model.Checklist) error {
	var b strings.Builder
	fmt.Fprintf(&b, advisoryRow+"%-12s %-4s %-15s %-30s %s\n", "Q", "GHSA", "STATE", "REL", "", "BRANCH", "PR", "AUTHOR", "CI", "REVIEWS")
	for _, a := range snap.Advisories {
		q := QueueLabel(a.ID, queue)
		fork := ForkText(a)
		if q != "·" && len(a.PRs) > 1 && a.MainNotGreen() {
			fork += "  ⚠ main not green"
		}
		line := strings.TrimRight(fmt.Sprintf(advisoryRow, q, a.ID, StatusText(a, lists[a.ID]), CountText(lists[a.ID]), fork), " ")
		if a.Summary != "" {
			line += "  " + Truncate(a.Summary, DefaultTitleWidth)
		}
		b.WriteString(line + "\n")
		for _, p := range a.PRs {
			line := fmt.Sprintf(prRow, "", p.Base, p.Number, Truncate(p.Author, 15), CIText(p, snap.FetchedAt), ReviewsText(p))
			b.WriteString(strings.TrimRight(line, " ") + "\n")
		}
	}
	if snap.Err != nil {
		fmt.Fprintf(&b, "error: %v\n", snap.Err)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("writing status: %w", err)
	}
	return nil
}
