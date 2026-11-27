package model

import (
	"slices"
	"strings"
	"time"

	"github.com/Joibel/ghsa-manager/internal/versions"
)

// Item is one entry of an advisory's release checklist.
type Item string

// The checklist items, in the order they're shown.
const (
	ItemBranches    Item = "branches"
	ItemDescription Item = "description"
	ItemCVSS        Item = "cvss"
	ItemCVE         Item = "cve"
	ItemVersions    Item = "versions"
	ItemCredits     Item = "credits"
)

// Items is every checklist item, in order.
var Items = []Item{ItemBranches, ItemDescription, ItemCVSS, ItemCVE, ItemVersions, ItemCredits} //nolint:gochecknoglobals // a fixed list

// Tick records that an item was ticked, and a hash of the value ticked.
type Tick struct {
	At   time.Time `json:"at"`
	Hash string    `json:"hash,omitempty"` // "" for description: never stale
}

// Checks is one advisory's local checklist record. It holds hashes, never
// advisory text. Its methods allocate the maps they need, so a zero Checks is
// ready to use.
type Checks struct {
	Ticks        map[Item]Tick        `json:"ticks,omitempty"`
	Waived       map[string]time.Time `json:"waived,omitempty"` // an Item, or "branch:<name>"
	CVERequested time.Time            `json:"cve_requested,omitzero"`
}

// ItemState is how far a checklist item is.
type ItemState int

// The item states. Waived counts towards done; stale doesn't.
const (
	ItemOpen ItemState = iota
	ItemDone
	ItemWaived
	ItemStale
)

// BranchLine is one branch of the branches item.
type BranchLine struct {
	Branch string
	PR     int // 0 = missing
	Waived bool
}

// ItemResult is one evaluated checklist item.
type ItemResult struct {
	Item     Item
	Note     string          // e.g. "can't read affected versions", "not set"
	Branches []BranchLine    // ItemBranches only: expected branches, then unexpected ones with a PR
	Expected map[string]bool // ItemBranches only
	Hints    []Credit        // ItemCredits only: suggested, missing credits
	State    ItemState
}

// Checklist is an advisory's evaluated checklist, one result per item.
type Checklist struct{ Results []ItemResult }

// Count returns how many items are done or waived, out of all of them.
func (c Checklist) Count() (int, int) {
	done := 0
	for _, r := range c.Results {
		if r.State == ItemDone || r.State == ItemWaived {
			done++
		}
	}
	return done, len(c.Results)
}

// Complete reports whether every item is done or waived.
func (c Checklist) Complete() bool {
	done, total := c.Count()
	return done == total
}

// StatusWith is Status, except that an advisory that would be Ready is
// ChecklistOpen until its checklist is complete.
func (a Advisory) StatusWith(cl Checklist) Status {
	s := a.Status()
	if s == Ready && !cl.Complete() {
		return ChecklistOpen
	}
	return s
}

const mainBranch = "main"

func branchWaiver(branch string) string { return "branch:" + branch }

// Evaluate applies the checklist's rules to an advisory. rel is the upstream's
// release branches; supported is how many of the newest GA ones are supported.
func Evaluate(a Advisory, rel []ReleaseBranch, supported int, c Checks) Checklist {
	credits := valueResult(a, ItemCredits, c)
	credits.Hints = creditHints(a)
	return Checklist{Results: []ItemResult{
		branchesResult(a, rel, supported, c),
		valueResult(a, ItemDescription, c),
		valueResult(a, ItemCVSS, c),
		cveResult(a, c),
		valueResult(a, ItemVersions, c),
		credits,
	}}
}

// expectedBranches returns main, the newest supported GA branches whose minor
// a range covers, and every branch without a GA release. It returns a note
// instead when the ranges are missing or can't be read.
func expectedBranches(a Advisory, rel []ReleaseBranch, supported int) ([]string, string) {
	parsed := make([]versions.Range, 0, len(a.Vulns))
	for _, v := range a.Vulns {
		if strings.TrimSpace(v.Range) == "" {
			continue
		}
		r, err := versions.ParseRange(v.Range)
		if err != nil {
			return nil, "can't read affected versions"
		}
		parsed = append(parsed, r)
	}
	if len(parsed) == 0 {
		return nil, "no affected versions set"
	}
	covers := func(b ReleaseBranch) bool {
		return slices.ContainsFunc(parsed, func(r versions.Range) bool { return r.CoversMinor(b.Major, b.Minor) })
	}

	rel = slices.Clone(rel)
	slices.SortStableFunc(rel, func(x, y ReleaseBranch) int {
		if x.Major != y.Major {
			return y.Major - x.Major
		}
		return y.Minor - x.Minor
	})
	expected := []string{mainBranch}
	ga := 0
	for _, b := range rel {
		if !b.GA {
			expected = append(expected, b.Name)
			continue
		}
		if ga >= supported {
			continue
		}
		ga++
		if covers(b) {
			expected = append(expected, b.Name)
		}
	}
	return expected, ""
}

func branchesResult(a Advisory, rel []ReleaseBranch, supported int, c Checks) ItemResult {
	res := ItemResult{Item: ItemBranches, State: ItemDone}
	names, note := expectedBranches(a, rel, supported)
	if note != "" {
		res.State, res.Note = ItemOpen, note
	} else {
		res.Expected = make(map[string]bool, len(names))
	}
	for _, b := range names {
		res.Expected[b] = true
		line := BranchLine{Branch: b, PR: prFor(a, b)}
		_, line.Waived = c.Waived[branchWaiver(b)]
		if line.PR == 0 && !line.Waived {
			res.State = ItemOpen
		}
		res.Branches = append(res.Branches, line)
	}
	seen := map[string]bool{}
	for _, p := range a.PRs {
		if !res.Expected[p.Base] && !seen[p.Base] {
			seen[p.Base] = true
			res.Branches = append(res.Branches, BranchLine{Branch: p.Base, PR: p.Number})
		}
	}
	return res
}

// prFor returns the number of the first fix PR to branch, or 0.
func prFor(a Advisory, branch string) int {
	for _, p := range a.PRs {
		if p.Base == branch {
			return p.Number
		}
	}
	return 0
}

// valueEmpty reports whether a ticked item's value is unset.
func valueEmpty(a Advisory, it Item) bool {
	switch it {
	case ItemDescription:
		return strings.TrimSpace(a.Description) == ""
	case ItemCVSS:
		return a.CVSSVector == ""
	case ItemVersions:
		return len(a.Vulns) == 0
	case ItemCredits:
		return len(a.Credits) == 0
	case ItemBranches, ItemCVE:
	}
	return false
}

// valueResult evaluates description, cvss, versions or credits.
func valueResult(a Advisory, it Item, c Checks) ItemResult {
	res := ItemResult{Item: it}
	if t, ok := c.Ticks[it]; ok {
		res.State = ItemStale
		if t.Hash == ValueHash(a, it) {
			res.State = ItemDone
		}
		return res
	}
	if _, ok := c.Waived[string(it)]; ok {
		res.State = ItemWaived
		return res
	}
	if valueEmpty(a, it) {
		res.Note = "not set"
	}
	return res
}

func cveResult(a Advisory, c Checks) ItemResult {
	res := ItemResult{Item: ItemCVE}
	if a.CVEID != "" || !c.CVERequested.IsZero() {
		res.State = ItemDone
	}
	return res
}

// Toggle is what space does to an item. For branches it toggles the waiver on
// branch, if branch is expected and has no PR.
func (c *Checks) Toggle(a Advisory, rel []ReleaseBranch, supported int, it Item, branch string, now time.Time) {
	switch it {
	case ItemBranches:
		c.toggleBranch(a, rel, supported, branch, now)
	case ItemCVE:
		if a.CVEID != "" {
			return
		}
		if c.CVERequested.IsZero() {
			c.CVERequested = now
		} else {
			c.CVERequested = time.Time{}
		}
	case ItemDescription, ItemCVSS, ItemVersions, ItemCredits:
		c.toggleValue(a, it, now)
	}
}

func (c *Checks) toggleBranch(a Advisory, rel []ReleaseBranch, supported int, branch string, now time.Time) {
	if branch == "" {
		return
	}
	names, _ := expectedBranches(a, rel, supported)
	if !slices.Contains(names, branch) || prFor(a, branch) != 0 {
		return
	}
	key := branchWaiver(branch)
	if _, ok := c.Waived[key]; ok {
		delete(c.Waived, key)
		return
	}
	c.waive(key, now)
}

func (c *Checks) toggleValue(a Advisory, it Item, now time.Time) {
	hash := ValueHash(a, it)
	empty := valueEmpty(a, it)
	if t, ok := c.Ticks[it]; ok {
		// A stale tick is reticked, but never onto an empty value.
		if t.Hash == hash || empty {
			delete(c.Ticks, it)
		} else {
			c.Ticks[it] = Tick{At: now, Hash: hash}
		}
		return
	}
	if _, ok := c.Waived[string(it)]; ok {
		delete(c.Waived, string(it))
		return
	}
	if empty {
		c.waive(string(it), now)
		return
	}
	if c.Ticks == nil {
		c.Ticks = map[Item]Tick{}
	}
	c.Ticks[it] = Tick{At: now, Hash: hash}
}

func (c *Checks) waive(key string, now time.Time) {
	if c.Waived == nil {
		c.Waived = map[string]time.Time{}
	}
	c.Waived[key] = now
}

// MarkCVERequested records that a CVE was requested.
func (c *Checks) MarkCVERequested(now time.Time) { c.CVERequested = now }
