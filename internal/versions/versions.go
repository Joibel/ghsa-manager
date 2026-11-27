// Package versions parses the affected-version ranges of security advisories
// and decides which release minors they cover.
package versions

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is a semantic version, X.Y.Z with an optional pre-release.
type Version struct {
	Pre                 string
	Major, Minor, Patch int
}

// Parse reads an optional "v" prefix, then X.Y.Z with an optional -pre suffix.
func Parse(s string) (Version, error) {
	rest := strings.TrimPrefix(s, "v")
	core, pre, hasPre := strings.Cut(rest, "-")
	if hasPre && pre == "" {
		return Version{}, fmt.Errorf("version %q: empty pre-release", s)
	}

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version %q: want X.Y.Z", s)
	}

	var nums [3]int
	for i, p := range parts {
		n, err := parseNumber(p)
		if err != nil {
			return Version{}, fmt.Errorf("version %q: %w", s, err)
		}
		nums[i] = n
	}

	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: pre}, nil
}

func parseNumber(p string) (int, error) {
	if p == "" || strings.Trim(p, "0123456789") != "" {
		return 0, fmt.Errorf("%q is not a number", p)
	}

	n, err := strconv.Atoi(p)
	if err != nil {
		return 0, fmt.Errorf("%q: %w", p, err)
	}

	return n, nil
}

// Less reports whether v sorts before o. A pre-release sorts before its
// release, and pre-release strings compare lexically.
func (v Version) Less(o Version) bool {
	switch {
	case v.Major != o.Major:
		return v.Major < o.Major
	case v.Minor != o.Minor:
		return v.Minor < o.Minor
	case v.Patch != o.Patch:
		return v.Patch < o.Patch
	case v.Pre == o.Pre:
		return false
	case v.Pre == "":
		return false
	case o.Pre == "":
		return true
	default:
		return v.Pre < o.Pre
	}
}

// bound is one end of an interval. A nil version means unbounded.
type bound struct {
	v      *Version
	closed bool
}

// Range is the intersection of a comma-separated list of constraints.
type Range struct {
	lo, hi bound
}

// ParseRange parses constraints such as ">= 4.0.0, < 4.0.7". Each is one of
// >=, >, <=, < or = followed by a version; no operator means =.
func ParseRange(s string) (Range, error) {
	if strings.TrimSpace(s) == "" {
		return Range{}, errors.New("empty version range")
	}

	var r Range
	for part := range strings.SplitSeq(s, ",") {
		part = strings.Join(strings.Fields(part), "")

		op, ver := splitOperator(part)
		v, err := Parse(ver)
		if err != nil {
			return Range{}, fmt.Errorf("range %q: %w", s, err)
		}

		r.apply(op, v)
	}

	return r, nil
}

func splitOperator(s string) (string, string) {
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if rest, ok := strings.CutPrefix(s, op); ok {
			return op, rest
		}
	}

	return "=", s
}

// apply narrows the range by one constraint.
func (r *Range) apply(op string, v Version) {
	switch op {
	case ">=":
		r.raiseLo(bound{&v, true})
	case ">":
		r.raiseLo(bound{&v, false})
	case "<=":
		r.lowerHi(bound{&v, true})
	case "<":
		// "< X.Y.Z" excludes X.Y.Z's own pre-releases too, so the bound is
		// the lowest pre-release of X.Y.Z.
		if v.Pre == "" {
			v.Pre = lowestPre
		}
		r.lowerHi(bound{&v, false})
	default:
		r.raiseLo(bound{&v, true})
		r.lowerHi(bound{&v, true})
	}
}

// lowestPre is the lowest pre-release string: it sorts before every other
// one that starts with a letter or a digit above 0.
const lowestPre = "0"

func (r *Range) raiseLo(b bound) {
	if r.lo.v == nil || tighterLo(b, r.lo) {
		r.lo = b
	}
}

func (r *Range) lowerHi(b bound) {
	if r.hi.v == nil || tighterHi(b, r.hi) {
		r.hi = b
	}
}

func tighterLo(a, b bound) bool {
	if a.v.Less(*b.v) || b.v.Less(*a.v) {
		return b.v.Less(*a.v)
	}

	return !a.closed && b.closed
}

func tighterHi(a, b bound) bool {
	if a.v.Less(*b.v) || b.v.Less(*a.v) {
		return a.v.Less(*b.v)
	}

	return !a.closed && b.closed
}

// CoversMinor reports whether some version from major.minor.0-<any pre> up
// to, but not including, major.(minor+1).0-<lowest pre> satisfies the range.
func (r Range) CoversMinor(major, minor int) bool {
	start := Version{Major: major, Minor: minor, Pre: lowestPre}
	end := Version{Major: major, Minor: minor + 1, Pre: lowestPre}

	lo := bound{&start, true}
	if r.lo.v != nil && tighterLo(r.lo, lo) {
		lo = r.lo
	}

	hi := bound{&end, false}
	if r.hi.v != nil && tighterHi(r.hi, hi) {
		hi = r.hi
	}

	if lo.v.Less(*hi.v) {
		return true
	}

	return !hi.v.Less(*lo.v) && lo.closed && hi.closed
}

// AnyCoversMinor reports whether the union of the ranges covers the minor.
// It returns the first parse error it hits.
func AnyCoversMinor(ranges []string, major, minor int) (bool, error) {
	covered := false

	for _, s := range ranges {
		r, err := ParseRange(s)
		if err != nil {
			return false, err
		}

		covered = covered || r.CoversMinor(major, minor)
	}

	return covered, nil
}
