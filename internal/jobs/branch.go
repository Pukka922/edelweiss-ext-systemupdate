package jobs

import (
	"regexp"
	"strconv"
)

var stableBranch = regexp.MustCompile(`^v(\d+)\.(\d+)$`)

// branchKey orders branches: anything else < every vX.Y (by number) < edge.
func branchKey(b string) (rank, major, minor int) {
	if b == "edge" {
		return 2, 0, 0
	}
	if m := stableBranch.FindStringSubmatch(b); m != nil {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		return 1, x, y
	}
	return 0, 0, 0
}

// BranchLess reports whether branch a comes before b: v3.9 < v3.23 < v3.100 < edge.
func BranchLess(a, b string) bool {
	ra, xa, ya := branchKey(a)
	rb, xb, yb := branchKey(b)
	if ra != rb {
		return ra < rb
	}
	if xa != xb {
		return xa < xb
	}
	return ya < yb
}

// atLeast reports whether b is a branch and not older than min.
func atLeast(b, min string) bool {
	rank, _, _ := branchKey(b)
	return rank > 0 && !BranchLess(b, min)
}
