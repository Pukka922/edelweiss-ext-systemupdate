package apk

import "testing"

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.0-r0", "1.0-r0", 0},
		{"1.0-r0", "1.0-r1", -1},
		{"1.2-r0", "1.10-r0", -1},
		{"1.2.1-r0", "1.2-r0", 1},
		{"3.5.3-r0", "3.5.4-r0", -1},
		{"1.2_rc1-r0", "1.2-r0", -1},
		{"1.2_alpha-r0", "1.2_beta-r0", -1},
		{"1.2_p1-r0", "1.2-r0", 1},
		{"1.2a-r0", "1.2-r0", 1},
		{"1.2a-r0", "1.2b-r0", -1},
		{"2.4.33-r9", "2.4.33-r10", -1},
		{"0_git20210421-r0", "0_git20210422-r0", -1},
		{"6.18.52-r0", "6.18.56-r0", -1},
		{"1.0", "1.0-r0", -1}, // apk: the version without revision ends earlier and sorts before any revision, even r0
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}
