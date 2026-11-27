// Package session opens an advisory's interactive Claude Code session: a tmux
// window running Claude in the security checkout, one per advisory. Opening it
// again switches to the window if it's still there, or resumes the session if
// not. Modelled on todaydeck's internal/workspace.
//
// Claude runs with no MCP servers, both when starting and when resuming. Its
// command line carries only the GHSA ID, the upstream, links, the fork's name,
// the open checklist items' names and fixed rule text: never the advisory's
// title or description.
package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/model"
)

// ClaudeCmd is Claude Code's command.
const ClaudeCmd = "claude"

// windowKey is the tmux window option that tags a window with its advisory.
const windowKey = "@ghsa_key"

// ErrNoTmux means ghsa-manager isn't running inside tmux.
var ErrNoTmux = errors.New("not inside tmux: run ghsa-manager in tmux to open a Claude session")

// namespace makes Claude session IDs from advisories, so the same advisory
// always resumes the same session.
var namespace = uuid.MustParse("0c2b87e8-d6ee-4f20-ba74-a72ca01476a3") //nolint:gochecknoglobals // a constant UUID

// noMCP is the claude flags that load no MCP servers at all: none from the
// user's or project's config, no claude.ai connectors, no plugins'.
var noMCP = []string{"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`} //nolint:gochecknoglobals // fixed flags

// Run runs a command in dir and returns its stdout.
type Run func(ctx context.Context, dir, name string, args ...string) (string, error)

// Opener opens advisories' Claude sessions.
type Opener struct {
	Run Run
	// Upstream and Mirror are the TUI's repositories, passed on to the
	// session's request-cve.
	Upstream gh.Repo
	Mirror   gh.Repo
	// Checkout is the security checkout, where every session runs. It must be
	// an absolute path to an existing directory; it is never created.
	Checkout string
	// ClaudeHome is Claude Code's config directory (~/.claude), for finding
	// session transcripts.
	ClaudeHome string
	// Self is ghsa-manager's own executable, for the session's request-cve.
	Self string
	// Pane is ghsa-manager's own tmux pane ($TMUX_PANE): a switch goes to the
	// client showing it, in that client's own session.
	Pane string
	// InTmux reports whether ghsa-manager is running in tmux.
	InTmux bool
}

// SessionID is the advisory's Claude session ID, the same every time.
func SessionID(upstream gh.Repo, ghsa string) string { //nolint:revive // the interface's name: it is Claude's session ID, not this package's
	return uuid.NewSHA1(namespace, []byte(upstream.String()+"/"+ghsa)).String()
}

// Open opens a's Claude session and returns a line saying what it did. open
// is the checklist's open items, for a new session's prompt.
func (o *Opener) Open(ctx context.Context, a model.Advisory, open []model.Item) (string, error) {
	if !o.InTmux {
		return "", ErrNoTmux
	}
	target, found, err := o.findWindow(ctx, a.ID)
	if err != nil {
		return "", err
	}
	if found {
		if err = o.switchTo(ctx, target); err != nil {
			return "", err
		}
		return "Switched to " + a.ID, nil
	}
	switch {
	case !filepath.IsAbs(o.Checkout):
		return "", fmt.Errorf(`the checkout %q isn't an absolute path; set "checkout" in the config`, o.Checkout)
	case !isDir(o.Checkout):
		return "", fmt.Errorf(`the checkout %s is missing; set "checkout" in the config`, o.Checkout)
	}
	id := SessionID(o.Upstream, a.ID)
	resumed := o.sessionExists(id)
	claude := append([]string{ClaudeCmd}, noMCP...)
	if resumed {
		claude = append(claude, "--resume", id)
	} else {
		// --mcp-config takes several values: "--" ends them, or the prompt
		// would be read as one.
		claude = append(claude, "--session-id", id, "--", Prompt(o, a, open))
	}
	win, err := o.Run(ctx, "", "tmux", "new-window", "-P", "-F", "#{window_id}", "-n", a.ID, "-c", o.Checkout, shellJoin(claude))
	if err != nil {
		return "", err
	}
	if _, err = o.Run(ctx, "", "tmux", "set-option", "-w", "-t", strings.TrimSpace(win), windowKey, a.ID); err != nil {
		return "", err
	}
	if resumed {
		return "Resumed Claude on " + a.ID, nil
	}
	return "Started Claude on " + a.ID, nil
}

// findWindow returns the tmux window already open for the advisory ghsa.
func (o *Opener) findWindow(ctx context.Context, ghsa string) (string, bool, error) {
	out, err := o.Run(ctx, "", "tmux", "list-windows", "-a", "-F", "#{window_id} #{"+windowKey+"}")
	if err != nil {
		return "", false, err
	}
	for line := range strings.Lines(out) {
		target, k, _ := strings.Cut(strings.TrimSpace(line), " ")
		if k == ghsa {
			return target, true, nil
		}
	}
	return "", false, nil
}

func (o *Opener) sessionExists(id string) bool {
	matches, err := filepath.Glob(filepath.Join(o.ClaudeHome, "projects", "*", id+".jsonl"))
	return err == nil && len(matches) > 0
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// Prompt is a new session's first message: where the advisory's work is, what
// the checklist still needs, and the rules for embargoed work. It never
// contains the advisory's title or description.
func Prompt(o *Opener, a model.Advisory, open []model.Item) string {
	api := fmt.Sprintf("repos/%s/security-advisories/%s", o.Upstream, a.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "This session is for the draft security advisory %s on %s: %s\n", a.ID, o.Upstream, a.URL)
	b.WriteString("It is embargoed security work, and this session may be resumed over several days.\n")
	if a.Fork != "" {
		fmt.Fprintf(&b, "Its temporary private fork is %s: https://github.com/%s\n", a.Fork, a.Fork)
	} else {
		b.WriteString("It has no temporary private fork yet.\n")
	}
	if len(a.PRs) > 0 {
		prs := make([]string, len(a.PRs))
		for i, p := range a.PRs {
			prs[i] = fmt.Sprintf("%s (base %s)", p.URL, p.Base)
		}
		fmt.Fprintf(&b, "Its fix PRs are %s.\n", strings.Join(prs, ", "))
	} else {
		b.WriteString("It has no fix PRs yet.\n")
	}
	if len(open) > 0 {
		names := make([]string, len(open))
		for i, it := range open {
			names[i] = string(it)
		}
		fmt.Fprintf(&b, "Its release checklist's open items are %s.\n", strings.Join(names, ", "))
	} else {
		b.WriteString("Every item of its release checklist is done or waived.\n")
	}
	b.WriteString("\nThe rules for this session:\n")
	fmt.Fprintf(&b, "Read the advisory with `gh api %s`.\n", api)
	fmt.Fprintf(&b, "Change it only with `gh api -X PATCH %s`, and only after Alan agrees in this chat.\n", api)
	fmt.Fprintf(&b, "Request a CVE only with `%s request-cve --upstream %s --mirror %s %s`.\n",
		shellJoin([]string{o.Self}), o.Upstream, o.Mirror, a.ID)
	b.WriteString("Never post comments on the advisory (the reporter sees them and they can't be deleted), " +
		"never publish the advisory, and never change its state.\n")
	b.WriteString("Never put advisory details anywhere outside GitHub's private surfaces (the advisory and its " +
		"temporary private fork) and this session.\n")
	fmt.Fprintf(&b, "Work only in this checkout, and never push to the public upstream, %s.\n", o.Upstream)
	b.WriteString("\nStart by getting your bearings: read the advisory, its fix PRs and the state of this checkout. " +
		"Then ask Alan what to work on.")
	return b.String()
}

// shellJoin quotes args for the shell tmux runs the window's command in.
func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
