package jobs

import "testing"

func TestBranchLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v3.23", "v3.24", true}, {"v3.24", "v3.23", false}, {"v3.9", "v3.10", true}, {"v3.24", "v3.100", true},
		{"v3.24", "v4.0", true}, {"v3.24", "edge", true}, {"edge", "v3.24", false}, {"edge", "edge", false},
		{"", "v3.1", true}, {"latest-stable", "v3.1", true},
	} {
		if got := BranchLess(c.a, c.b); got != c.want {
			t.Errorf("BranchLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
