package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Joibel/ghsa-manager/internal/config"
	"github.com/Joibel/ghsa-manager/internal/cve"
	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/state"
)

// NewRequestCVECmd requests a CVE for one advisory and records the request.
// upstream and mirror point at the root command's flags.
func NewRequestCVECmd(upstream, mirrorRepo *string) *cobra.Command {
	return &cobra.Command{
		Use:   "request-cve <GHSA>",
		Short: "Request a CVE for a draft advisory and record that it was requested",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := config.Load(config.Path(), *upstream, *mirrorRepo)
			if err != nil {
				return err
			}
			client, err := newClient(c.Context())
			if err != nil {
				return err
			}
			ghsa := model.NormalizeID(args[0])
			if err = cve.Request(c.Context(), client, cfg.Repos.Upstream, state.Path(cfg.Repos.Upstream.String()), ghsa, time.Now()); err != nil {
				return err
			}
			if _, err = fmt.Fprintf(c.OutOrStdout(), "requested a CVE for %s\n", ghsa); err != nil {
				return fmt.Errorf("printing the result: %w", err)
			}
			return nil
		},
	}
}
