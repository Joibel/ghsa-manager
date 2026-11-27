// Package cmd holds ghsa-manager's subcommands.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Joibel/ghsa-manager/internal/config"
	"github.com/Joibel/ghsa-manager/internal/mirror"
	"github.com/Joibel/ghsa-manager/internal/render"
	"github.com/Joibel/ghsa-manager/internal/source"
	"github.com/Joibel/ghsa-manager/internal/state"
)

// NewStatusCmd prints every draft advisory once, without the TUI. upstream and
// mirror point at the root command's flags.
func NewStatusCmd(upstream, mirrorRepo *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print every draft advisory's fork, fix PRs, CI and reviews",
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := config.Load(config.Path(), *upstream, *mirrorRepo)
			if err != nil {
				return err
			}
			repos := cfg.Repos
			client, err := newClient(c.Context())
			if err != nil {
				return err
			}
			st, err := state.Load(state.Path(repos.Upstream.String()))
			if err != nil {
				return fmt.Errorf("reading local state: %w", err)
			}
			queue := make([]string, 0, len(st.Queue))
			for _, e := range st.Queue {
				queue = append(queue, e.GHSA)
			}
			snap := source.New(client, mirror.NewFacts(client, nil, repos.Mirror), repos).Fetch(c.Context(), st.Bases)
			if err := render.Plain(c.OutOrStdout(), snap, queue, render.Checklists(snap, st.Checks, cfg.SupportedBranches)); err != nil {
				return err
			}
			if snap.Err != nil {
				return fmt.Errorf("fetching advisories: %w", snap.Err)
			}
			return nil
		},
	}
}
