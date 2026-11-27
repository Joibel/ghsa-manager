// Package source assembles the draft advisories, their forks' fix PRs and
// reviews, and each PR's mirror CI into a snapshot.
package source

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v90/github"
	"golang.org/x/sync/errgroup"

	"github.com/Joibel/ghsa-manager/internal/gh"
	"github.com/Joibel/ghsa-manager/internal/mirror"
	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/versions"
)

// fetchConcurrency bounds how many advisories are fetched at once.
const fetchConcurrency = 6

// Snapshot is one fetch's result. On error Advisories is empty: callers keep
// showing their previous snapshot.
type Snapshot struct {
	FetchedAt  time.Time
	Err        error
	Advisories []model.Advisory
	// Releases are the upstream's release branches, newest first.
	Releases []model.ReleaseBranch
}

// Source fetches snapshots.
type Source struct {
	c     *github.Client
	facts *mirror.Facts
	repos gh.Repos
}

// New returns a Source reading repos' advisories with c and mirror CI with facts.
func New(c *github.Client, facts *mirror.Facts, repos gh.Repos) *Source {
	return &Source{c: c, facts: facts, repos: repos}
}

// Fetch reads every draft advisory. recordedBases maps a mirror branch to the
// base the tool triggered its latest run against.
func (s *Source) Fetch(ctx context.Context, recordedBases map[string]model.RecordedBase) Snapshot {
	advs, rels, err := s.fetchAll(ctx, recordedBases)
	if err != nil {
		return Snapshot{FetchedAt: time.Now(), Err: err}
	}
	return Snapshot{FetchedAt: time.Now(), Advisories: advs, Releases: rels}
}

func (s *Source) fetchAll(ctx context.Context, recordedBases map[string]model.RecordedBase) ([]model.Advisory, []model.ReleaseBranch, error) {
	drafts, err := s.drafts(ctx)
	if err != nil {
		return nil, nil, err
	}
	bases := &baseCache{s: s, tips: make(map[string]string)}
	advs := make([]model.Advisory, len(drafts))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fetchConcurrency)
	var rels []model.ReleaseBranch
	g.Go(func() error {
		var err error
		rels, err = s.releases(gctx)
		return err
	})
	for i, d := range drafts {
		g.Go(func() error {
			a, err := s.advisory(gctx, d, recordedBases, bases)
			advs[i] = a
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err //nolint:wrapcheck // errors from advisory() and releases() are already wrapped
	}
	// Oldest first, so rows never move as advisories are marked or worked on.
	sort.Slice(advs, func(i, j int) bool {
		if !advs[i].CreatedAt.Equal(advs[j].CreatedAt) {
			return advs[i].CreatedAt.Before(advs[j].CreatedAt)
		}
		return advs[i].ID < advs[j].ID
	})
	return advs, rels, nil
}

func (s *Source) drafts(ctx context.Context) ([]*github.SecurityAdvisory, error) {
	opts := &github.ListRepositorySecurityAdvisoriesOptions{State: "draft"}
	opts.PerPage = 100
	var all []*github.SecurityAdvisory
	for {
		page, resp, err := s.c.SecurityAdvisories.ListRepositorySecurityAdvisories(ctx, s.repos.Upstream.Owner, s.repos.Upstream.Name, opts)
		if err != nil {
			return nil, fmt.Errorf("listing draft advisories: %w", err)
		}
		all = append(all, page...)
		if resp.After == "" {
			return all, nil
		}
		opts.After = resp.After
	}
}

func (s *Source) advisory(ctx context.Context, d *github.SecurityAdvisory, recordedBases map[string]model.RecordedBase, bases *baseCache) (model.Advisory, error) {
	a := model.Advisory{
		ID: model.NormalizeID(d.GetGHSAID()), Summary: d.GetSummary(), URL: d.GetHTMLURL(),
		CreatedAt: d.GetCreatedAt().Time, Description: d.GetDescription(), CVEID: d.GetCVEID(),
	}
	a.CVSSVector, a.CVSSScore = cvss(d)
	for _, v := range d.Vulnerabilities {
		patched := v.GetPatchedVersions()
		if patched == "" {
			patched = v.GetFirstPatchedVersion().GetIdentifier()
		}
		a.Vulns = append(a.Vulns, model.Vulnerability{
			Package: v.GetPackage().GetName(), Range: v.GetVulnerableVersionRange(), Patched: patched,
		})
	}
	for _, c := range d.CreditsDetailed {
		a.Credits = append(a.Credits, model.Credit{User: c.GetUser().GetLogin(), Type: c.GetType(), State: c.GetState()})
	}
	if d.PrivateFork == nil {
		return a, nil
	}
	a.Fork = d.PrivateFork.GetFullName()
	owner, repo, ok := strings.Cut(a.Fork, "/")
	if !ok {
		return a, fmt.Errorf("%s: unexpected fork name %q", a.ID, a.Fork)
	}
	prs, _, err := s.c.PullRequests.List(ctx, owner, repo, &github.PullRequestListOptions{
		State: "open", ListOptions: github.ListOptions{PerPage: 100},
	})
	if err != nil {
		return a, fmt.Errorf("%s: listing fix PRs: %w", a.ID, err)
	}
	for _, p := range prs {
		pr, err := s.fixPR(ctx, a.ID, owner, repo, p, recordedBases, bases)
		if err != nil {
			return a, err
		}
		a.PRs = append(a.PRs, pr)
	}
	sort.Slice(a.PRs, func(i, j int) bool { return branchOrder(a.PRs[i].Base) < branchOrder(a.PRs[j].Base) })
	return a, nil
}

// cvss returns the advisory's vector and score: CVSS v4, then v3, then the
// legacy field, whichever first has a vector.
func cvss(d *github.SecurityAdvisory) (string, float64) {
	cands := []*github.AdvisoryCVSS{d.CVSS}
	if sev := d.CVSSSeverities; sev != nil {
		cands = []*github.AdvisoryCVSS{sev.CVSSV4, sev.CVSSV3, d.CVSS}
	}
	for _, c := range cands {
		if c.GetVectorString() != "" {
			return c.GetVectorString(), c.GetScore()
		}
	}
	return "", 0
}

// releases lists the upstream's release-X.Y branches, newest first, marking
// those with a vX.Y.0 tag as GA. Names that aren't release-X.Y are skipped.
func (s *Source) releases(ctx context.Context) ([]model.ReleaseBranch, error) {
	up := s.repos.Upstream
	branches, _, err := s.c.Git.ListMatchingRefs(ctx, up.Owner, up.Name, "heads/release-")
	if err != nil {
		return nil, fmt.Errorf("listing release branches: %w", err)
	}
	tags, _, err := s.c.Git.ListMatchingRefs(ctx, up.Owner, up.Name, "tags/v")
	if err != nil {
		return nil, fmt.Errorf("listing release tags: %w", err)
	}
	have := make(map[string]bool, len(tags))
	for _, t := range tags {
		have[strings.TrimPrefix(t.GetRef(), "refs/tags/")] = true
	}
	var out []model.ReleaseBranch
	for _, b := range branches {
		name := strings.TrimPrefix(b.GetRef(), "refs/heads/")
		xy := strings.TrimPrefix(name, "release-")
		v, err := versions.Parse(xy + ".0")
		if err != nil || strings.Count(xy, ".") != 1 {
			continue
		}
		out = append(out, model.ReleaseBranch{
			Name: name, Major: v.Major, Minor: v.Minor, GA: have[fmt.Sprintf("v%d.%d.0", v.Major, v.Minor)],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Major != out[j].Major {
			return out[i].Major > out[j].Major
		}
		return out[i].Minor > out[j].Minor
	})
	return out, nil
}

func (s *Source) fixPR(ctx context.Context, id, owner, repo string, p *github.PullRequest, recordedBases map[string]model.RecordedBase, bases *baseCache) (model.FixPR, error) {
	pr := model.FixPR{
		Number: p.GetNumber(), Base: p.GetBase().GetRef(), HeadSHA: p.GetHead().GetSHA(),
		URL: p.GetHTMLURL(), Title: p.GetTitle(), Author: p.GetUser().GetLogin(),
	}
	reviews, _, err := s.c.PullRequests.ListReviews(ctx, owner, repo, pr.Number, &github.ListOptions{PerPage: 100})
	if err != nil {
		return pr, fmt.Errorf("%s: listing reviews of #%d: %w", id, pr.Number, err)
	}
	for _, r := range reviews {
		pr.Reviews = append(pr.Reviews, model.Review{User: r.GetUser().GetLogin(), State: r.GetState(), CommitID: r.GetCommitID()})
	}
	tip, err := bases.tip(ctx, pr.Base)
	if err != nil {
		return pr, err
	}
	branch := mirror.BranchName(id, pr.Base, pr.Number)
	if pr.CI, err = s.facts.LatestCI(ctx, branch, recordedBases[branch], tip); err != nil {
		return pr, fmt.Errorf("%s: %w", id, err)
	}
	return pr, nil
}

// branchOrder puts main first, then release branches newest first.
func branchOrder(base string) string {
	if base == "main" {
		return "0"
	}
	return "1" + invert(base)
}

// invert makes a descending string sort ascending, for release-4.1 before release-4.0.
func invert(s string) string {
	b := []byte(s)
	for i := range b {
		b[i] = 255 - b[i]
	}
	return string(b)
}

// baseCache reads each mirror base branch's tip once per fetch.
type baseCache struct {
	s    *Source
	tips map[string]string
	mu   sync.Mutex
}

func (b *baseCache) tip(ctx context.Context, base string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.tips[base]; ok {
		return t, nil
	}
	br, resp, err := b.s.c.Repositories.GetBranch(ctx, b.s.repos.Mirror.Owner, b.s.repos.Mirror.Name, base, 1)
	switch {
	case resp != nil && resp.StatusCode == http.StatusNotFound:
		b.tips[base] = "" // ghsa-ci.sh creates it on the first run against this base
	case err != nil:
		return "", fmt.Errorf("reading mirror branch %s: %w", base, err)
	default:
		b.tips[base] = br.GetCommit().GetSHA()
	}
	return b.tips[base], nil
}
