// Package cve requests CVE IDs for draft advisories and records the request
// locally, because GitHub's API doesn't say whether one was requested.
package cve

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-github/v90/github"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/state"
)

// Request asks GitHub to assign a CVE to the upstream's advisory ghsa. Only
// when GitHub accepts the request does it record the request time in the state
// file at statePath.
func Request(ctx context.Context, c *github.Client, upstream gh.Repo, statePath, ghsa string, now time.Time) error {
	if _, err := c.SecurityAdvisories.RequestCVE(ctx, upstream.Owner, upstream.Name, ghsa); err != nil {
		return fmt.Errorf("requesting a CVE for %s: %w", ghsa, err)
	}
	err := state.Update(statePath, func(s *state.State) error {
		checks := s.Checks[ghsa]
		checks.MarkCVERequested(now)
		s.Checks[ghsa] = checks
		return nil
	})
	if err != nil {
		return fmt.Errorf("recording the CVE request for %s: %w", ghsa, err)
	}
	return nil
}
