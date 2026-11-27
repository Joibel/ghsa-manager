package model

// Status is what an advisory needs next.
type Status int

// The status words, in the order an advisory moves through them.
const (
	NoFork Status = iota
	ForkNoPRs
	NeedsCI
	NeedsReview
	Ready
	// ChecklistOpen is an advisory that would be Ready but whose checklist
	// isn't complete.
	ChecklistOpen
)

// String is the status word shown on screen.
func (s Status) String() string {
	switch s {
	case NoFork:
		return "no fork"
	case ForkNoPRs:
		return "fork, no PRs"
	case NeedsCI:
		return "needs CI"
	case NeedsReview:
		return "needs review"
	case Ready:
		return "ready"
	case ChecklistOpen:
		return "checklist"
	}
	return "unknown"
}

// GreenOnHead reports whether the PR's latest run is a confirmed green on its
// current head.
func (p FixPR) GreenOnHead() bool { return p.CI.State == CIGreen && !p.CI.Stale(p.HeadSHA) }

// LatestReviews returns each reviewer's latest counting review. Reviews are in
// the API's order, oldest first.
func (p FixPR) LatestReviews() map[string]Review {
	latest := make(map[string]Review)
	for _, r := range p.Reviews {
		if r.State == ReviewApproved || r.State == ReviewChangesRequested {
			latest[r.User] = r
		}
	}
	return latest
}

// ApprovedOnHead reports whether someone's latest review approves the current head.
func (p FixPR) ApprovedOnHead() bool {
	for _, r := range p.LatestReviews() {
		if r.State == ReviewApproved && !r.Stale(p.HeadSHA) {
			return true
		}
	}
	return false
}

// BlockedOnHead reports whether someone's latest review requests changes on the
// current head.
func (p FixPR) BlockedOnHead() bool {
	for _, r := range p.LatestReviews() {
		if r.State == ReviewChangesRequested && !r.Stale(p.HeadSHA) {
			return true
		}
	}
	return false
}

// Status is what the advisory needs next. CI and review can happen in either
// order; CI is reported first.
func (a Advisory) Status() Status {
	if a.Fork == "" {
		return NoFork
	}
	if len(a.PRs) == 0 {
		return ForkNoPRs
	}
	for _, p := range a.PRs {
		if !p.GreenOnHead() {
			return NeedsCI
		}
	}
	for _, p := range a.PRs {
		if !p.ApprovedOnHead() || p.BlockedOnHead() {
			return NeedsReview
		}
	}
	return Ready
}

// MainNotGreen reports whether the advisory has a main PR without a confirmed
// green run on its head. Cherry-picks don't wait for main, so this is a flag.
func (a Advisory) MainNotGreen() bool {
	for _, p := range a.PRs {
		if p.Base == "main" {
			return !p.GreenOnHead()
		}
	}
	return false
}

// ClassifyRun turns a mirror run's conclusion into a CIState. A successful run
// counts as green only if its merged base is known and, when the tool recorded
// the base it triggered the run against, matches it.
func ClassifyRun(conclusion, recordedBase, mergedBase string) CIState {
	switch conclusion {
	case "":
		return CIRunning
	case "success":
		if mergedBase == "" || (recordedBase != "" && !shaMatch(recordedBase, mergedBase)) {
			return CIUnconfirmed
		}
		return CIGreen
	case "cancelled":
		return CICancelled
	default:
		return CIRed
	}
}
