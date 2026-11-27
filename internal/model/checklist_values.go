package model

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// The credit types the hints suggest.
const (
	remediationDeveloper = "remediation_developer"
	remediationReviewer  = "remediation_reviewer"
)

// ValueHash is the sha256 hex of a canonical form of the item's value, so a
// tick can go stale without the state file holding advisory text. It's ""
// for description, whose ticks never go stale, and for branches and cve,
// which aren't ticked.
func ValueHash(a Advisory, it Item) string {
	var lines []string
	switch it {
	case ItemCVSS:
		lines = []string{a.CVSSVector}
	case ItemVersions:
		lines = make([]string, 0, len(a.Vulns))
		for _, v := range a.Vulns {
			lines = append(lines, v.Package+"|"+v.Range+"|"+v.Patched)
		}
	case ItemCredits:
		// The state is left out: a credit being accepted doesn't unset a tick.
		lines = make([]string, 0, len(a.Credits))
		for _, c := range a.Credits {
			lines = append(lines, c.User+"|"+c.Type)
		}
	case ItemDescription, ItemBranches, ItemCVE:
		return ""
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// creditHints suggests credits that look missing: the fix PRs' authors as
// remediation developers, and their approvers as remediation reviewers.
func creditHints(a Advisory) []Credit {
	var hints []Credit
	suggest := func(user, typ string) {
		if user == "" {
			return
		}
		match := func(c Credit) bool { return strings.EqualFold(c.User, user) && c.Type == typ }
		if slices.ContainsFunc(a.Credits, match) || slices.ContainsFunc(hints, match) {
			return
		}
		hints = append(hints, Credit{User: user, Type: typ})
	}
	for _, p := range a.PRs {
		suggest(p.Author, remediationDeveloper)
	}
	for _, p := range a.PRs {
		latest := p.LatestReviews()
		var approvers []string
		for user, r := range latest {
			if r.State == ReviewApproved {
				approvers = append(approvers, user)
			}
		}
		slices.Sort(approvers)
		for _, user := range approvers {
			suggest(user, remediationReviewer)
		}
	}
	return hints
}
