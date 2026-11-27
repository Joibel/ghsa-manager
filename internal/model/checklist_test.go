package model

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// releases are Task 2's upstream release branches, newest first.
func releases() []ReleaseBranch {
	return []ReleaseBranch{
		{Name: "release-4.2", Major: 4, Minor: 2, GA: false},
		{Name: "release-4.1", Major: 4, Minor: 1, GA: true},
		{Name: "release-4.0", Major: 4, Minor: 0, GA: true},
		{Name: "release-3.7", Major: 3, Minor: 7, GA: true},
	}
}

var now = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // fixed test clock

func vulnAdvisory(ranges ...string) Advisory {
	a := Advisory{ID: "GHSA-a", Fork: "o/r"}
	for _, r := range ranges {
		a.Vulns = append(a.Vulns, Vulnerability{Package: "github.com/o/r", Range: r, Patched: "4.0.7"})
	}
	return a
}

func withPRs(a Advisory, bases ...string) Advisory {
	for i, b := range bases {
		a.PRs = append(a.PRs, FixPR{Number: i + 1, Base: b, HeadSHA: headSHA})
	}
	return a
}

func result(t *testing.T, cl Checklist, it Item) ItemResult {
	t.Helper()
	for _, r := range cl.Results {
		if r.Item == it {
			return r
		}
	}
	require.Failf(t, "missing item", "%s", it)
	return ItemResult{}
}

func expectedNames(r ItemResult) []string {
	var names []string
	for b, ok := range r.Expected {
		if ok {
			names = append(names, b)
		}
	}
	slices.Sort(names)
	return names
}

func TestEvaluateOrder(t *testing.T) {
	cl := Evaluate(Advisory{}, nil, 2, Checks{})
	got := make([]Item, 0, len(cl.Results))
	for _, r := range cl.Results {
		got = append(got, r.Item)
	}
	assert.Equal(t, Items, got)
	assert.Equal(t, []Item{"branches", "description", "cvss", "cve", "versions", "credits"}, Items)
}

func TestExpectedBranches(t *testing.T) {
	tests := []struct {
		name      string
		ranges    []string
		want      []string
		supported int
	}{
		{"4.0 patch range", []string{">= 4.0.0, < 4.0.7"}, []string{"main", "release-4.0", "release-4.2"}, 2},
		{"3.7 is not among the newest two GA branches", []string{"< 3.7.5"}, []string{"main", "release-4.2"}, 2},
		{"open lower bound, two supported", []string{"< 4.1.3"}, []string{"main", "release-4.0", "release-4.1", "release-4.2"}, 2},
		// The brief lists main, 4.0, 4.1 and 4.2 here, but `< 4.1.3` has no
		// lower bound, so it also covers 3.7, the third-newest GA branch.
		{"open lower bound, three supported", []string{"< 4.1.3"}, []string{"main", "release-3.7", "release-4.0", "release-4.1", "release-4.2"}, 3},
		{"any range counts", []string{">= 4.0.0, < 4.0.7", ">= 4.1.0, < 4.1.2"}, []string{"main", "release-4.0", "release-4.1", "release-4.2"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := result(t, Evaluate(vulnAdvisory(tt.ranges...), releases(), tt.supported, Checks{}), ItemBranches)
			assert.Equal(t, tt.want, expectedNames(r))
			assert.Equal(t, ItemOpen, r.State)
			assert.Empty(t, r.Note)
		})
	}

	t.Run("the releases' order doesn't matter", func(t *testing.T) {
		rel := releases()
		slices.Reverse(rel)
		r := result(t, Evaluate(vulnAdvisory("< 3.7.5"), rel, 2, Checks{}), ItemBranches)
		assert.Equal(t, []string{"main", "release-4.2"}, expectedNames(r))
	})

	t.Run("an unparseable range", func(t *testing.T) {
		a := withPRs(vulnAdvisory(">= 4.0.0, < 4.0.7", "not a range"), "main")
		r := result(t, Evaluate(a, releases(), 2, Checks{}), ItemBranches)
		assert.Equal(t, ItemOpen, r.State)
		assert.Equal(t, "can't read affected versions", r.Note)
		assert.Empty(t, r.Expected)
		assert.Equal(t, []BranchLine{{Branch: "main", PR: 1}}, r.Branches)
	})

	t.Run("no affected versions", func(t *testing.T) {
		a := withPRs(Advisory{ID: "GHSA-a", Fork: "o/r"}, "main")
		r := result(t, Evaluate(a, releases(), 2, Checks{}), ItemBranches)
		assert.Equal(t, ItemOpen, r.State)
		assert.Equal(t, "no affected versions set", r.Note)
		assert.Empty(t, r.Expected)
	})
}

func TestBranchesDone(t *testing.T) {
	a := withPRs(vulnAdvisory(">= 4.0.0, < 4.0.7"), "main", "release-4.0")
	waive := func(branches ...string) Checks {
		c := Checks{Waived: map[string]time.Time{}}
		for _, b := range branches {
			c.Waived["branch:"+b] = now
		}
		return c
	}

	t.Run("PRs plus a waiver", func(t *testing.T) {
		r := result(t, Evaluate(a, releases(), 2, waive("release-4.2")), ItemBranches)
		assert.Equal(t, ItemDone, r.State)
		assert.Equal(t, []BranchLine{
			{Branch: "main", PR: 1},
			{Branch: "release-4.2", Waived: true},
			{Branch: "release-4.0", PR: 2},
		}, r.Branches)
	})

	t.Run("without the waiver", func(t *testing.T) {
		r := result(t, Evaluate(a, releases(), 2, Checks{}), ItemBranches)
		assert.Equal(t, ItemOpen, r.State)
		assert.Contains(t, r.Branches, BranchLine{Branch: "release-4.2"})
	})

	t.Run("a waiver on a branch that isn't expected", func(t *testing.T) {
		r := result(t, Evaluate(a, releases(), 2, waive("release-4.1")), ItemBranches)
		assert.Equal(t, ItemOpen, r.State)
		for _, l := range r.Branches {
			assert.NotEqual(t, "release-4.1", l.Branch)
		}
	})

	t.Run("a PR to a branch that isn't expected", func(t *testing.T) {
		b := withPRs(vulnAdvisory(">= 4.0.0, < 4.0.7"), "main", "release-4.0", "release-4.1")
		r := result(t, Evaluate(b, releases(), 2, waive("release-4.2")), ItemBranches)
		assert.Equal(t, ItemDone, r.State)
		assert.Equal(t, BranchLine{Branch: "release-4.1", PR: 3}, r.Branches[len(r.Branches)-1])
		assert.False(t, r.Expected["release-4.1"])
	})
}

func tickAt(c *Checks, a Advisory, it Item) {
	c.Toggle(a, releases(), 2, it, "", now)
}

func TestTickStale(t *testing.T) {
	t.Run("cvss vector changes", func(t *testing.T) {
		a := Advisory{CVSSVector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}
		var c Checks
		tickAt(&c, a, ItemCVSS)
		assert.Equal(t, ItemDone, result(t, Evaluate(a, nil, 2, c), ItemCVSS).State)
		a.CVSSVector = "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:L/A:N"
		assert.Equal(t, ItemStale, result(t, Evaluate(a, nil, 2, c), ItemCVSS).State)
	})

	t.Run("versions reordered", func(t *testing.T) {
		a := Advisory{Vulns: []Vulnerability{
			{Package: "p", Range: ">= 4.0.0, < 4.0.7", Patched: "4.0.7"},
			{Package: "p", Range: ">= 4.1.0, < 4.1.2", Patched: "4.1.2"},
		}}
		var c Checks
		tickAt(&c, a, ItemVersions)
		slices.Reverse(a.Vulns)
		assert.Equal(t, ItemDone, result(t, Evaluate(a, nil, 2, c), ItemVersions).State)
		a.Vulns[0].Patched = "4.1.3"
		assert.Equal(t, ItemStale, result(t, Evaluate(a, nil, 2, c), ItemVersions).State)
	})

	t.Run("credits", func(t *testing.T) {
		a := Advisory{Credits: []Credit{{User: "finder", Type: "reporter", State: "pending"}}}
		var c Checks
		tickAt(&c, a, ItemCredits)
		a.Credits[0].State = "accepted"
		assert.Equal(t, ItemDone, result(t, Evaluate(a, nil, 2, c), ItemCredits).State)
		a.Credits = append(a.Credits, Credit{User: "fixer", Type: "remediation_developer", State: "pending"})
		assert.Equal(t, ItemStale, result(t, Evaluate(a, nil, 2, c), ItemCredits).State)
	})
}

func TestValueHash(t *testing.T) {
	a := Advisory{
		Description: "secret details",
		CVSSVector:  "CVSS:3.1/AV:N",
		Vulns:       []Vulnerability{{Package: "p", Range: "< 1.0.0", Patched: "1.0.0"}},
		Credits:     []Credit{{User: "u", Type: "finder", State: "accepted"}},
	}
	assert.Empty(t, ValueHash(a, ItemDescription))
	for _, it := range []Item{ItemCVSS, ItemVersions, ItemCredits} {
		h := ValueHash(a, it)
		assert.Len(t, h, 64, it)
		assert.NotContains(t, h, "CVSS", it)
	}
	assert.NotEqual(t, ValueHash(a, ItemCVSS), ValueHash(a, ItemVersions))
}

func TestDescriptionNeverStale(t *testing.T) {
	a := Advisory{Description: "first wording"}
	var c Checks
	tickAt(&c, a, ItemDescription)
	assert.Empty(t, c.Ticks[ItemDescription].Hash)
	a.Description = "second wording"
	assert.Equal(t, ItemDone, result(t, Evaluate(a, nil, 2, c), ItemDescription).State)
}

func TestCVE(t *testing.T) {
	assert.Equal(t, ItemDone, result(t, Evaluate(Advisory{CVEID: "CVE-2026-1"}, nil, 2, Checks{}), ItemCVE).State)
	assert.Equal(t, ItemDone, result(t, Evaluate(Advisory{}, nil, 2, Checks{CVERequested: now}), ItemCVE).State)
	assert.Equal(t, ItemOpen, result(t, Evaluate(Advisory{}, nil, 2, Checks{}), ItemCVE).State)
}

func TestToggle(t *testing.T) {
	state := func(a Advisory, c Checks, it Item) ItemState {
		return result(t, Evaluate(a, releases(), 2, c), it).State
	}

	t.Run("set value: tick, then untick", func(t *testing.T) {
		a := Advisory{CVSSVector: "CVSS:3.1/AV:N"}
		var c Checks
		tickAt(&c, a, ItemCVSS)
		assert.Equal(t, ItemDone, state(a, c, ItemCVSS))
		assert.Equal(t, now, c.Ticks[ItemCVSS].At)
		assert.Equal(t, ValueHash(a, ItemCVSS), c.Ticks[ItemCVSS].Hash)
		tickAt(&c, a, ItemCVSS)
		assert.Equal(t, ItemOpen, state(a, c, ItemCVSS))
		assert.NotContains(t, c.Ticks, ItemCVSS)
	})

	t.Run("stale: retick with the current hash", func(t *testing.T) {
		a := Advisory{CVSSVector: "CVSS:3.1/AV:N"}
		var c Checks
		tickAt(&c, a, ItemCVSS)
		a.CVSSVector = "CVSS:3.1/AV:L"
		require.Equal(t, ItemStale, state(a, c, ItemCVSS))
		tickAt(&c, a, ItemCVSS)
		assert.Equal(t, ItemDone, state(a, c, ItemCVSS))
	})

	t.Run("stale and now empty: untick, never tick an empty value", func(t *testing.T) {
		a := Advisory{CVSSVector: "CVSS:3.1/AV:N"}
		var c Checks
		tickAt(&c, a, ItemCVSS)
		a.CVSSVector = ""
		require.Equal(t, ItemStale, state(a, c, ItemCVSS))
		tickAt(&c, a, ItemCVSS)
		assert.Equal(t, ItemOpen, state(a, c, ItemCVSS))
	})

	t.Run("empty value: waive, then unwaive", func(t *testing.T) {
		for _, it := range []Item{ItemDescription, ItemCVSS, ItemVersions, ItemCredits} {
			var c Checks
			r := result(t, Evaluate(Advisory{}, nil, 2, c), it)
			assert.Equal(t, "not set", r.Note, it)
			tickAt(&c, Advisory{}, it)
			assert.Equal(t, ItemWaived, state(Advisory{}, c, it), it)
			assert.Equal(t, now, c.Waived[string(it)], it)
			assert.NotContains(t, c.Ticks, it, it)
			tickAt(&c, Advisory{}, it)
			assert.Equal(t, ItemOpen, state(Advisory{}, c, it), it)
			assert.NotContains(t, c.Waived, string(it), it)
		}
	})

	t.Run("a set value is never waived", func(t *testing.T) {
		a := Advisory{Credits: []Credit{{User: "u", Type: "finder"}}}
		var c Checks
		tickAt(&c, a, ItemCredits)
		assert.Empty(t, c.Waived)
		assert.Equal(t, ItemDone, state(a, c, ItemCredits))
	})

	t.Run("description ticks with no hash", func(t *testing.T) {
		a := Advisory{Description: "d"}
		var c Checks
		tickAt(&c, a, ItemDescription)
		assert.Equal(t, Tick{At: now}, c.Ticks[ItemDescription])
	})

	t.Run("cve toggles requested", func(t *testing.T) {
		var c Checks
		tickAt(&c, Advisory{}, ItemCVE)
		assert.Equal(t, now, c.CVERequested)
		tickAt(&c, Advisory{}, ItemCVE)
		assert.True(t, c.CVERequested.IsZero())
	})

	t.Run("cve with an ID does nothing", func(t *testing.T) {
		var c Checks
		tickAt(&c, Advisory{CVEID: "CVE-2026-1"}, ItemCVE)
		assert.True(t, c.CVERequested.IsZero())
	})

	t.Run("MarkCVERequested", func(t *testing.T) {
		var c Checks
		c.MarkCVERequested(now)
		assert.Equal(t, now, c.CVERequested)
	})

	t.Run("branch waiver toggles on an expected branch without a PR", func(t *testing.T) {
		a := withPRs(vulnAdvisory(">= 4.0.0, < 4.0.7"), "main", "release-4.0")
		var c Checks
		c.Toggle(a, releases(), 2, ItemBranches, "release-4.2", now)
		assert.Equal(t, now, c.Waived["branch:release-4.2"])
		assert.Equal(t, ItemDone, state(a, c, ItemBranches))
		c.Toggle(a, releases(), 2, ItemBranches, "release-4.2", now)
		assert.NotContains(t, c.Waived, "branch:release-4.2")
	})

	t.Run("branch waiver ignores branches with a PR, unexpected ones and none", func(t *testing.T) {
		a := withPRs(vulnAdvisory(">= 4.0.0, < 4.0.7"), "main", "release-4.0")
		var c Checks
		for _, b := range []string{"main", "release-4.1", "release-9.9", ""} {
			c.Toggle(a, releases(), 2, ItemBranches, b, now)
		}
		assert.Empty(t, c.Waived)
	})
}

func TestCreditHints(t *testing.T) {
	fixer := FixPR{Number: 1, Base: "main", Author: "fixer", HeadSHA: headSHA}

	t.Run("PR author", func(t *testing.T) {
		a := Advisory{PRs: []FixPR{fixer}}
		r := result(t, Evaluate(a, nil, 2, Checks{}), ItemCredits)
		assert.Equal(t, []Credit{{User: "fixer", Type: "remediation_developer"}}, r.Hints)
	})

	t.Run("approver, once across PRs", func(t *testing.T) {
		p1 := fixer
		p1.Reviews = []Review{approved("rev", oldSHA), changes("other", headSHA)}
		p2 := FixPR{Number: 2, Base: "release-4.0", Author: "fixer", HeadSHA: headSHA, Reviews: []Review{approved("rev", headSHA)}}
		a := Advisory{PRs: []FixPR{p1, p2}}
		r := result(t, Evaluate(a, nil, 2, Checks{}), ItemCredits)
		assert.Equal(t, []Credit{
			{User: "fixer", Type: "remediation_developer"},
			{User: "rev", Type: "remediation_reviewer"},
		}, r.Hints)
	})

	t.Run("a later changes request is not an approval", func(t *testing.T) {
		p := fixer
		p.Reviews = []Review{approved("rev", headSHA), changes("rev", headSHA)}
		a := Advisory{PRs: []FixPR{p}, Credits: []Credit{{User: "fixer", Type: "remediation_developer"}}}
		assert.Empty(t, result(t, Evaluate(a, nil, 2, Checks{}), ItemCredits).Hints)
	})

	t.Run("an existing matching credit", func(t *testing.T) {
		p := fixer
		p.Reviews = []Review{approved("rev", headSHA)}
		a := Advisory{PRs: []FixPR{p}, Credits: []Credit{
			{User: "fixer", Type: "remediation_developer", State: "declined"},
			{User: "rev", Type: "remediation_reviewer", State: "accepted"},
		}}
		assert.Empty(t, result(t, Evaluate(a, nil, 2, Checks{}), ItemCredits).Hints)
	})

	t.Run("a credit of another type", func(t *testing.T) {
		a := Advisory{PRs: []FixPR{fixer}, Credits: []Credit{{User: "fixer", Type: "finder"}}}
		assert.Equal(t, []Credit{{User: "fixer", Type: "remediation_developer"}},
			result(t, Evaluate(a, nil, 2, Checks{}), ItemCredits).Hints)
	})
}

// completeChecks returns checks under which every item but skip is done for a.
func completeChecks(a Advisory, skip Item) Checks {
	var c Checks
	for _, it := range []Item{ItemDescription, ItemCVSS, ItemVersions, ItemCredits} {
		if it != skip {
			c.Toggle(a, releases(), 2, it, "", now)
		}
	}
	if skip != ItemCVE {
		c.MarkCVERequested(now)
	}
	return c
}

func TestStatusWith(t *testing.T) {
	a := Advisory{
		Fork:        "o/r",
		PRs:         []FixPR{pr("main", green(), approved("alice", headSHA))},
		Description: "d",
		CVSSVector:  "CVSS:3.1/AV:N",
		Vulns:       []Vulnerability{{Package: "p", Range: "< 3.0.0", Patched: "3.0.0"}},
		Credits:     []Credit{{User: "u", Type: "finder"}},
	}
	rel := []ReleaseBranch{{Name: "release-4.0", Major: 4, Minor: 0, GA: true}}
	require.Equal(t, Ready, a.Status())

	five := Evaluate(a, rel, 2, completeChecks(a, ItemCVSS))
	done, total := five.Count()
	assert.Equal(t, 5, done)
	assert.Equal(t, 6, total)
	assert.False(t, five.Complete())
	assert.Equal(t, ChecklistOpen, a.StatusWith(five))
	assert.Equal(t, "checklist", ChecklistOpen.String())

	six := Evaluate(a, rel, 2, completeChecks(a, ""))
	assert.True(t, six.Complete())
	assert.Equal(t, Ready, a.StatusWith(six))

	waived := Advisory{Fork: a.Fork, PRs: a.PRs, Vulns: a.Vulns}
	c := completeChecks(waived, "")
	cl := Evaluate(waived, rel, 2, c)
	assert.Equal(t, ItemWaived, result(t, cl, ItemCVSS).State)
	assert.True(t, cl.Complete(), "waived items count towards done")

	needsCI := a
	needsCI.PRs = []FixPR{pr("main", CI{}, approved("alice", headSHA))}
	assert.Equal(t, NeedsCI, needsCI.StatusWith(five))
}
