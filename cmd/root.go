package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/go-github/v90/github"
	"github.com/spf13/cobra"

	"github.com/Joibel/ghsa-manager/internal/config"
	"github.com/Joibel/ghsa-manager/internal/cve"
	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/mirror"
	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/report"
	"github.com/Joibel/ghsa-manager/internal/runner"
	"github.com/Joibel/ghsa-manager/internal/session"
	"github.com/Joibel/ghsa-manager/internal/source"
	"github.com/Joibel/ghsa-manager/internal/state"
	"github.com/Joibel/ghsa-manager/internal/tui"
)

// NewRootCmd is ghsa-manager: the TUI by default, plus subcommands.
func NewRootCmd() *cobra.Command {
	var maxRuns int
	var refresh time.Duration
	var upstream, mirrorRepo string
	root := &cobra.Command{
		Use:   "ghsa-manager",
		Short: "Track and run CI for draft argo-workflows security advisories",
		Long: `ghsa-manager shows every draft security advisory of a repository with its
temporary fork, fix PRs, last CI run in a private mirror, and reviews. Mark
advisories for CI and it runs their fix PRs through the mirror's shared
ghsa-ci.sh, a few at a time, then reports the results as a comment on the
advisory.

The upstream and mirror repositories come from the config file
($XDG_CONFIG_HOME/ghsa-manager/config.json) or --upstream and --mirror.`,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := config.Load(config.Path(), upstream, mirrorRepo)
			if err != nil {
				return err
			}
			client, err := newClient(c.Context())
			if err != nil {
				return err
			}
			p := tea.NewProgram(tui.New(deps(client, cfg, maxRuns, refresh)), tea.WithAltScreen(), tea.WithContext(c.Context()))
			if _, err := p.Run(); err != nil {
				return fmt.Errorf("running the TUI: %w", err)
			}
			return nil
		},
	}
	root.Flags().IntVar(&maxRuns, "max-runs", 3, "most mirror CI runs in flight at once")
	root.Flags().DurationVar(&refresh, "refresh", 5*time.Minute, "how often to refresh from GitHub")
	root.PersistentFlags().StringVar(&upstream, "upstream", "", "repository whose draft advisories to track (owner/name)")
	root.PersistentFlags().StringVar(&mirrorRepo, "mirror", "", "private mirror repository where fix PRs' CI runs (owner/name)")
	root.AddCommand(NewStatusCmd(&upstream, &mirrorRepo))
	root.AddCommand(NewRequestCVECmd(&upstream, &mirrorRepo))
	return root
}

func newClient(ctx context.Context) (*github.Client, error) {
	tok, err := gh.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the gh login: %w", err)
	}
	client, err := gh.NewClient(tok, github.WithTimeout(time.Minute))
	if err != nil {
		return nil, fmt.Errorf("creating the GitHub client: %w", err)
	}
	return client, nil
}

func deps(c *github.Client, cfg config.Config, maxRuns int, refresh time.Duration) tui.Deps {
	repos := cfg.Repos
	facts := mirror.NewFacts(c, nil, repos.Mirror)
	src := source.New(c, facts, repos)
	run := runner.Runner{Script: runner.FromMirror(c, repos.Mirror)}
	statePath := state.Path(repos.Upstream.String())
	return tui.Deps{
		Fetch:    src.Fetch,
		InFlight: facts.InFlight,
		Run:      run.Run,
		Rerun: func(ctx context.Context, runID int64) error {
			if _, err := c.Actions.RerunFailedJobsByID(ctx, repos.Mirror.Owner, repos.Mirror.Name, runID); err != nil {
				return fmt.Errorf("re-running run %d: %w", runID, err)
			}
			return nil
		},
		Report: func(ctx context.Context, s *state.State, a model.Advisory) error {
			return report.Upsert(ctx, c, repos.Upstream, s, a)
		},
		MakeFork: func(ctx context.Context, ghsa string) error {
			if _, _, err := c.SecurityAdvisories.CreateTemporaryPrivateFork(ctx, repos.Upstream.Owner, repos.Upstream.Name, ghsa); err != nil {
				return fmt.Errorf("creating the temporary private fork: %w", err)
			}
			return nil
		},
		RequestCVE: func(ctx context.Context, ghsa string) error {
			return cve.Request(ctx, c, repos.Upstream, statePath, ghsa, time.Now())
		},
		OpenSession: openSession(repos, cfg.Checkout),
		Open: func(url string) error {
			if err := exec.Command("xdg-open", url).Start(); err != nil { //nolint:gosec,noctx // opens a GitHub URL in the browser; outlives the request
				return fmt.Errorf("xdg-open: %w", err)
			}
			return nil
		},
		StatePath: statePath,
		MaxRuns:   maxRuns,
		Supported: cfg.SupportedBranches,
		Refresh:   refresh,
	}
}

// openSession opens an advisory's Claude session in tmux, in the security
// checkout.
func openSession(repos gh.Repos, checkout string) func(context.Context, model.Advisory, []model.Item) (string, error) {
	self, err := os.Executable()
	if err != nil {
		self = "ghsa-manager"
	}
	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		claudeHome = filepath.Join(os.Getenv("HOME"), ".claude")
	}
	o := &session.Opener{
		Run: execRun, Upstream: repos.Upstream, Mirror: repos.Mirror, Checkout: checkout,
		ClaudeHome: claudeHome, Self: self,
		Pane: os.Getenv("TMUX_PANE"), InTmux: os.Getenv("TMUX") != "",
	}
	return func(ctx context.Context, a model.Advisory, open []model.Item) (string, error) {
		if _, err := exec.LookPath(session.ClaudeCmd); err != nil {
			return "", errors.New("claude isn't on PATH: install Claude Code to open a session")
		}
		return o.Open(ctx, a, open)
	}
}

// execRun runs a command in dir and returns its stdout. Its error carries the
// command's stderr.
func execRun(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: running tmux is the point
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return string(out), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return string(out), fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}
