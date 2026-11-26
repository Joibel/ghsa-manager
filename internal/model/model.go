// Package model holds ghsa-manager's view of the draft advisories, and the
// pure rules that turn it into status words. Nothing here talks to GitHub.
package model

import (
	"strings"
	"time"
)

// Review is a PR review. Only APPROVED and CHANGES_REQUESTED states count;
// others (COMMENTED, DISMISSED, PENDING) are ignored.
type Review struct {
	User     string
	State    string
	CommitID string
}

// Review states that count.
const (
	ReviewApproved         = "APPROVED"
	ReviewChangesRequested = "CHANGES_REQUESTED"
)

// Stale reports whether the review was given on a head other than head.
func (r Review) Stale(head string) bool { return r.CommitID != head }

// CIState is the result of a PR's latest mirror CI run.
type CIState int

// The CI states, in no particular order.
const (
	CINever CIState = iota
	CIRunning
	CIGreen
	CIRed
	CICancelled
	CIUnconfirmed
)

// CI is a PR's latest mirror CI run.
type CI struct {
	// StartedAt is when the run's latest attempt started (a re-run starts a new
	// attempt of the same run).
	StartedAt  time.Time
	FinishedAt time.Time
	RunURL     string
	// RunHeadSHA is the fix head the run tested.
	RunHeadSHA string
	// MergedBaseSHA is the base the run's merge commit was built on, read from
	// the run's log; "" when unknown. It may be abbreviated.
	MergedBaseSHA string
	// CurrentBaseSHA is the mirror base branch's tip now.
	CurrentBaseSHA string
	// SkippedTestJobs are unit or e2e test jobs the run skipped.
	SkippedTestJobs []string
	RunID           int64
	State           CIState
}

// RecordedBase is the base the tool triggered a mirror branch's run against. It
// applies to run RunID once known, and before that to any run that started at
// or after Since. Runs other people start are judged on their own merged base.
type RecordedBase struct {
	Since time.Time `json:"since"`
	SHA   string    `json:"sha"`
	RunID int64     `json:"run_id,omitempty"`
}

// For returns the recorded SHA if it applies to the given run, else "".
func (r RecordedBase) For(runID int64, runStarted time.Time) string {
	switch {
	case r.SHA == "":
		return ""
	case r.RunID != 0:
		if r.RunID == runID {
			return r.SHA
		}
		return ""
	case !runStarted.Before(r.Since):
		return r.SHA
	}
	return ""
}

// Stale reports whether the run tested a head other than head.
func (c CI) Stale(head string) bool { return c.RunHeadSHA != "" && c.RunHeadSHA != head }

// BaseMoved reports whether the base has moved since the run merged it. It is a
// note, not staleness: main moves several times a day.
func (c CI) BaseMoved() bool {
	return c.MergedBaseSHA != "" && c.CurrentBaseSHA != "" && !shaMatch(c.MergedBaseSHA, c.CurrentBaseSHA)
}

// FixPR is an open fix PR in an advisory's temporary private fork.
type FixPR struct {
	Reviews []Review
	// Author is the login of whoever opened the PR.
	Author  string
	Base    string
	HeadSHA string
	URL     string
	Title   string
	// StartError is why the tool couldn't start CI for this PR, if it couldn't.
	StartError string
	CI         CI
	Number     int
}

// Advisory is a draft security advisory and its fork's fix PRs.
type Advisory struct {
	// CreatedAt orders the screen: oldest advisory first.
	CreatedAt time.Time
	ID        string
	Summary   string
	URL       string
	// Fork is the temporary private fork's full name; "" when there is none.
	Fork string
	PRs  []FixPR
}

// shaMatch reports whether two SHAs name the same commit, allowing either to be
// abbreviated.
func shaMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// NormalizeID returns an advisory ID in GitHub's form, "GHSA-" then lowercase
// ("GHSA-abcd-efgh-ijkl"), whatever case it arrived in. ghsa-ci.sh prints IDs
// fully upper-cased.
func NormalizeID(id string) string {
	if len(id) < 5 || !strings.EqualFold(id[:5], "GHSA-") {
		return id
	}
	return "GHSA-" + strings.ToLower(id[5:])
}
