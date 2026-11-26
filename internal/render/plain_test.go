package render

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Joibel/ghsa-manager/internal/model"
	"github.com/Joibel/ghsa-manager/internal/source"
)

const (
	h1  = "1111111111111111111111111111111111111111"
	old = "0000000000000000000000000000000000000000"
)

func fetchedAt() time.Time { return time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC) }

func snapshot() source.Snapshot {
	fetched := fetchedAt()
	greenCI := model.CI{State: model.CIGreen, RunHeadSHA: h1, FinishedAt: fetched.Add(-2 * time.Hour)}
	return source.Snapshot{FetchedAt: fetched, Advisories: []model.Advisory{
		{ID: "GHSA-aaaa-bbbb-cccc", Fork: "o/r", PRs: []model.FixPR{
			{Number: 1, Base: "main", HeadSHA: h1, CI: greenCI, Author: "mallory-fixer",
				Reviews: []model.Review{{User: "alice", State: "APPROVED", CommitID: old}}},
			{Number: 2, Base: "release-4.1", HeadSHA: h1,
				CI:      model.CI{State: model.CIRed, RunHeadSHA: h1, FinishedAt: fetched.Add(-time.Hour), SkippedTestJobs: []string{"E2E UI (root)"}},
				Reviews: []model.Review{{User: "bob", State: "CHANGES_REQUESTED", CommitID: h1}}},
		}},
		{ID: "GHSA-dddd-eeee-ffff", Fork: "o/r", Summary: "Token leak via redirect", PRs: []model.FixPR{
			{Number: 1, Base: "main", HeadSHA: h1, CI: greenCI,
				Reviews: []model.Review{{User: "carol", State: "APPROVED", CommitID: h1}}},
		}},
		{ID: "GHSA-gggg-hhhh-iiii"},
	}}
}

func TestPlain(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, Plain(&buf, snapshot(), []string{"GHSA-dddd-eeee-ffff"}))
	want := ` Q  GHSA                 STATE          BRANCH       PR   AUTHOR          CI                             REVIEWS
 ·  GHSA-aaaa-bbbb-cccc  needs CI       fork ✓
                                        main         #1   mallory-fixer   ✓ green 2h ago                 alice approved (stale)
                                        release-4.1  #2                   ✗ red 1h ago (skipped: E2E UI (root)) ✗ bob changes requested
 1  GHSA-dddd-eeee-ffff  ready          fork ✓  Token leak via redirect
                                        main         #1                   ✓ green 2h ago                 ✓ carol approved
 ·  GHSA-gggg-hhhh-iiii  no fork
`
	assert.Equal(t, want, buf.String())
}

func TestCIText(t *testing.T) {
	now := fetchedAt()
	assert.Equal(t, "never", CIText(model.FixPR{HeadSHA: h1}, now))
	assert.Equal(t, "● running", CIText(model.FixPR{HeadSHA: h1, CI: model.CI{State: model.CIRunning, RunHeadSHA: h1}}, now))
	assert.Equal(t, "✓ green 5m ago (stale)", CIText(model.FixPR{HeadSHA: h1, CI: model.CI{State: model.CIGreen, RunHeadSHA: old, FinishedAt: now.Add(-5 * time.Minute)}}, now))
	assert.Equal(t, "? unconfirmed 3d ago (base moved)", CIText(model.FixPR{HeadSHA: h1, CI: model.CI{
		State: model.CIUnconfirmed, RunHeadSHA: h1, FinishedAt: now.Add(-72 * time.Hour), MergedBaseSHA: "aaa1111", CurrentBaseSHA: "bbb2222",
	}}, now))
}
