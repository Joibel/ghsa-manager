package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	headSHA = "1111111111111111111111111111111111111111"
	oldSHA  = "0000000000000000000000000000000000000000"
)

func green() CI { return CI{State: CIGreen, RunHeadSHA: headSHA} }

func approved(user, commit string) Review {
	return Review{User: user, State: "APPROVED", CommitID: commit}
}

func changes(user, commit string) Review {
	return Review{User: user, State: "CHANGES_REQUESTED", CommitID: commit}
}

func pr(base string, ci CI, reviews ...Review) FixPR {
	return FixPR{Number: 1, Base: base, HeadSHA: headSHA, CI: ci, Reviews: reviews}
}

func TestAdvisoryStatus(t *testing.T) {
	tests := []struct {
		name string
		adv  Advisory
		want Status
	}{
		{"no fork", Advisory{ID: "GHSA-a"}, NoFork},
		{"fork without PRs", Advisory{ID: "GHSA-a", Fork: "o/r"}, ForkNoPRs},
		{"approved but no CI needs CI", Advisory{Fork: "o/r", PRs: []FixPR{pr("main", CI{}, approved("alice", headSHA))}}, NeedsCI},
		{"red CI needs CI", Advisory{Fork: "o/r", PRs: []FixPR{pr("main", CI{State: CIRed, RunHeadSHA: headSHA}, approved("alice", headSHA))}}, NeedsCI},
		{"green on an old head needs CI", Advisory{Fork: "o/r", PRs: []FixPR{pr("main", CI{State: CIGreen, RunHeadSHA: oldSHA}, approved("alice", headSHA))}}, NeedsCI},
		{"unconfirmed needs CI", Advisory{Fork: "o/r", PRs: []FixPR{pr("main", CI{State: CIUnconfirmed, RunHeadSHA: headSHA}, approved("alice", headSHA))}}, NeedsCI},
		{"green with a stale approval needs review", Advisory{Fork: "o/r", PRs: []FixPR{pr("main", green(), approved("alice", oldSHA))}}, NeedsReview},
		{"green with no reviews needs review", Advisory{Fork: "o/r", PRs: []FixPR{pr("main", green())}}, NeedsReview},
		{"approval then changes requested on head needs review", Advisory{Fork: "o/r", PRs: []FixPR{pr("main", green(), approved("alice", headSHA), changes("alice", headSHA))}}, NeedsReview},
		{"stale changes request does not block another approval", Advisory{Fork: "o/r", PRs: []FixPR{pr("main", green(), changes("bob", oldSHA), approved("alice", headSHA))}}, Ready},
		{"changes request on head blocks another approval", Advisory{Fork: "o/r", PRs: []FixPR{pr("main", green(), changes("bob", headSHA), approved("alice", headSHA))}}, NeedsReview},
		{"every PR green and approved is ready", Advisory{Fork: "o/r", PRs: []FixPR{
			pr("main", green(), approved("alice", headSHA)),
			pr("release-4.1", green(), approved("bob", headSHA)),
		}}, Ready},
		{"one PR unapproved holds the advisory at needs review", Advisory{Fork: "o/r", PRs: []FixPR{
			pr("main", green(), approved("alice", headSHA)),
			pr("release-4.1", green()),
		}}, NeedsReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.adv.Status())
		})
	}
}

func TestClassifyRun(t *testing.T) {
	tests := []struct {
		name, conclusion, recorded, merged string
		want                               CIState
	}{
		{"recorded short prefix of merged", "success", "abc1234", "abc1234def5678", CIGreen},
		{"merged short prefix of recorded", "success", "abc1234def5678", "abc1234", CIGreen},
		{"no merged base found", "success", "abc", "", CIUnconfirmed},
		{"manual run with no recorded base", "success", "", "fff", CIGreen},
		{"merged base differs from recorded", "success", "aaa", "bbb", CIUnconfirmed},
		{"failure", "failure", "", "", CIRed},
		{"timed out", "timed_out", "", "", CIRed},
		{"cancelled", "cancelled", "", "", CICancelled},
		{"still running", "", "", "", CIRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClassifyRun(tt.conclusion, tt.recorded, tt.merged))
		})
	}
}

func TestMainNotGreen(t *testing.T) {
	red := Advisory{Fork: "o/r", PRs: []FixPR{pr("main", CI{State: CIRed, RunHeadSHA: headSHA}), pr("release-4.1", green())}}
	assert.True(t, red.MainNotGreen())
	noMain := Advisory{Fork: "o/r", PRs: []FixPR{pr("release-4.1", CI{State: CIRed, RunHeadSHA: headSHA})}}
	assert.False(t, noMain.MainNotGreen())
	greenMain := Advisory{Fork: "o/r", PRs: []FixPR{pr("main", green())}}
	assert.False(t, greenMain.MainNotGreen())
}

func TestCIStaleAndBaseMoved(t *testing.T) {
	assert.False(t, CI{}.Stale(headSHA), "no run is never stale")
	assert.True(t, CI{RunHeadSHA: oldSHA}.Stale(headSHA))
	assert.False(t, CI{RunHeadSHA: headSHA}.Stale(headSHA))
	assert.True(t, CI{MergedBaseSHA: "aaa1111", CurrentBaseSHA: "bbb2222"}.BaseMoved())
	assert.False(t, CI{MergedBaseSHA: "aaa1111", CurrentBaseSHA: "aaa1111ffff"}.BaseMoved())
	assert.False(t, CI{CurrentBaseSHA: "bbb"}.BaseMoved(), "unknown merged base is not a move")
}

func TestReviewStale(t *testing.T) {
	p := pr("main", green(), approved("alice", oldSHA), approved("bob", headSHA))
	latest := p.LatestReviews()
	assert.True(t, latest["alice"].Stale(p.HeadSHA))
	assert.False(t, latest["bob"].Stale(p.HeadSHA))
}

func TestNormalizeID(t *testing.T) {
	assert.Equal(t, "GHSA-abcd-efgh-ijkl", NormalizeID("GHSA-ABCD-EFGH-IJKL"))
	assert.Equal(t, "GHSA-abcd-efgh-ijkl", NormalizeID("ghsa-abcd-efgh-ijkl"))
	assert.Equal(t, "GHSA-abcd-efgh-ijkl", NormalizeID("GHSA-abcd-efgh-ijkl"))
}
