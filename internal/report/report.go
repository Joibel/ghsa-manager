// Package report posts each advisory's CI status as one comment on the
// advisory, edited in place on later runs. The advisory's reporter can read
// these comments and they can't be deleted, so they say only what CI found.
package report

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v90/github"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/state"
)

// Marker starts every comment the tool writes, so it can find its own again.
const Marker = "<!-- ghsa-manager:ci-status -->"

// legacyTitle starts a CI-status comment posted by hand before the tool
// existed; the tool adopts one rather than adding a second.
const legacyTitle = "**CI status for the fix PRs**"

// Body is the comment for an advisory's CI results.
func Body(a model.Advisory, now time.Time) string {
	var b strings.Builder
	b.WriteString(Marker + "\n" + legacyTitle + "\n\n| Fix PR | Branch | CI |\n|---|---|---|\n")
	for _, p := range a.PRs {
		fmt.Fprintf(&b, "| #%d | %s | %s |\n", p.Number, escape(p.Base), cell(p))
	}
	b.WriteString("\nCI runs in a private mirror, because temporary private forks can't run GitHub Actions.\n\n")
	fmt.Fprintf(&b, "_Last updated %s._\n", now.UTC().Format("2006-01-02 15:04 MST"))
	return b.String()
}

// escape keeps a value from breaking the Markdown table.
func escape(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// safeReason turns why CI couldn't start into wording for the advisory's
// reporter, who can read the comment: never the script's own error text.
func safeReason(raw string) string {
	switch {
	case strings.Contains(raw, "conflicts with"):
		return "conflicts with its base branch and needs a rebase"
	case strings.Contains(raw, "merge commit"):
		return "CI couldn't be restarted yet; a maintainer will retry"
	default:
		return "CI couldn't be started; a maintainer will retry"
	}
}

func cell(p model.FixPR) string {
	if p.StartError != "" {
		return "⚠️ not started: " + safeReason(p.StartError)
	}
	var s string
	switch p.CI.State {
	case model.CINever:
		return "not run"
	case model.CIRunning:
		s = "🔄 running"
	case model.CIGreen:
		s = "✅ green"
	case model.CIRed:
		s = "❌ red"
	case model.CICancelled:
		s = "⚪ cancelled"
	case model.CIUnconfirmed:
		s = "❓ unconfirmed: the tested base couldn't be verified"
	}
	if len(p.CI.SkippedTestJobs) > 0 {
		s += ", skipped: " + escape(strings.Join(p.CI.SkippedTestJobs, ", "))
	}
	if p.CI.RunURL != "" {
		s += " ([run](" + p.CI.RunURL + "))"
	}
	return s
}

type comment struct {
	Body string `json:"body"`
	ID   int64  `json:"id"`
}

// Upsert posts the advisory's CI-status comment, or edits the existing one, and
// records its ID in s. go-github has no advisory-comments methods, so it sends
// the requests itself.
func Upsert(ctx context.Context, c *github.Client, upstream gh.Repo, s *state.State, a model.Advisory) error {
	body := Body(a, time.Now())
	base := fmt.Sprintf("repos/%s/security-advisories/%s/comments", upstream, a.ID)
	id := s.CommentIDs[a.ID]
	if id == 0 {
		var err error
		if id, err = findOwn(ctx, c, base); err != nil {
			return err
		}
	}
	if id == 0 {
		var created comment
		if err := do(ctx, c, "POST", base, comment{Body: body}, &created); err != nil {
			return fmt.Errorf("posting %s's CI-status comment: %w", a.ID, err)
		}
		id = created.ID
	} else if err := do(ctx, c, "PATCH", fmt.Sprintf("%s/%d", base, id), comment{Body: body}, nil); err != nil {
		return fmt.Errorf("updating %s's CI-status comment: %w", a.ID, err)
	}
	s.CommentIDs[a.ID] = id
	return nil
}

func findOwn(ctx context.Context, c *github.Client, base string) (int64, error) {
	var comments []comment
	if err := do(ctx, c, "GET", base+"?per_page=100", nil, &comments); err != nil {
		return 0, fmt.Errorf("listing advisory comments: %w", err)
	}
	for _, cm := range comments {
		if strings.HasPrefix(cm.Body, Marker) || strings.HasPrefix(cm.Body, legacyTitle) {
			return cm.ID, nil
		}
	}
	return 0, nil
}

func do(ctx context.Context, c *github.Client, method, url string, body, out any) error {
	req, err := c.NewRequest(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("building %s %s: %w", method, url, err)
	}
	if _, err := c.Do(req, out); err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	return nil
}
