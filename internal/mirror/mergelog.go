// Package mirror reads CI facts from the private mirror, where ghsa-ci.sh runs
// each fix PR.
package mirror

import (
	"fmt"
	"regexp"
	"strings"
)

// mergeLine is the checkout's record of the merge commit a pull_request run tested.
var mergeLine = regexp.MustCompile(`Merge ([0-9a-f]{40}) into ([0-9a-f]{7,40})`)

// ParseMergedBase finds the merge commit a run checked out in its log, and
// returns the fix head and the base it was merged into. The base may be
// abbreviated.
func ParseMergedBase(log string) (string, string, bool) {
	m := mergeLine.FindStringSubmatch(log)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// BranchName is the mirror branch ghsa-ci.sh pushes a fork PR to.
func BranchName(ghsa, base string, pr int) string {
	return fmt.Sprintf("%s/%s-pr%d", strings.ToLower(ghsa), base, pr)
}
