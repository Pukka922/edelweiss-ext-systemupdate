// Package apk runs apk-tools and parses what it prints: the list of upgradable packages, the plan of a simulated
// upgrade, the progress lines of --progress-fd and the exit status. Compare orders Alpine package versions.
package apk

import "strings"

const noSuffix = 4 // the rank of a version without suffix: _alpha.._rc sort before it, _cvs.._p after it

var suffixRanks = map[string]int{"alpha": 0, "beta": 1, "pre": 2, "rc": 3, "cvs": 5, "svn": 6, "git": 7, "hg": 8, "p": 9}

type suffix struct {
	rank int
	num  string
}

type version struct {
	nums     []string
	letter   byte
	suffixes []suffix
	rev      string
	hasRev   bool
}

// Compare orders two Alpine package versions like apk version -t: -1 if a is older, 0 if equal, 1 if newer. A
// version is dot-separated digit groups, an optional letter, suffixes (_alpha, _beta, _pre, _rc before the plain
// version; _cvs, _svn, _git, _hg, _p after it) with optional numbers, an optional ~hash (ignored here) and an optional
// -rN revision; a version without revision is older than the same version with any revision.
func Compare(a, b string) int {
	va, vb := parseVersion(a), parseVersion(b)
	for i := 0; i < len(va.nums) || i < len(vb.nums); i++ {
		if i >= len(va.nums) {
			return -1
		}
		if i >= len(vb.nums) {
			return 1
		}
		if c := compareDigits(va.nums[i], vb.nums[i], i > 0); c != 0 {
			return c
		}
	}
	if c := sign(int(va.letter) - int(vb.letter)); c != 0 {
		return c
	}
	for i := 0; i < len(va.suffixes) || i < len(vb.suffixes); i++ {
		sa, sb := suffix{rank: noSuffix}, suffix{rank: noSuffix}
		if i < len(va.suffixes) {
			sa = va.suffixes[i]
		}
		if i < len(vb.suffixes) {
			sb = vb.suffixes[i]
		}
		if c := sign(sa.rank - sb.rank); c != 0 {
			return c
		}
		if c := compareDigits(sa.num, sb.num, false); c != 0 {
			return c
		}
	}
	if va.hasRev != vb.hasRev {
		// Like apk: a version that ends here is older than one that goes on with a revision, even -r0.
		if vb.hasRev {
			return -1
		}
		return 1
	}
	return compareDigits(va.rev, vb.rev, false)
}

// compareDigits compares two digit strings as numbers. Like apk, a later group with a leading zero ("1.02") compares
// as a decimal fraction, i.e. as a string.
func compareDigits(a, b string, fraction bool) int {
	if fraction && (strings.HasPrefix(a, "0") || strings.HasPrefix(b, "0")) {
		return sign(strings.Compare(a, b))
	}
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return sign(len(a) - len(b))
	}
	return sign(strings.Compare(a, b))
}

func parseVersion(v string) version {
	var out version
	if i := strings.LastIndex(v, "-r"); i >= 0 && i+2 < len(v) && digits(v[i+2:]) {
		out.rev, out.hasRev, v = v[i+2:], true, v[:i]
	}
	if i := strings.IndexByte(v, '~'); i >= 0 {
		v = v[:i]
	}
	j := 0
	for {
		k := j
		for k < len(v) && isDigit(v[k]) {
			k++
		}
		if k == j {
			break
		}
		out.nums = append(out.nums, v[j:k])
		j = k
		if j+1 < len(v) && v[j] == '.' && isDigit(v[j+1]) {
			j++
			continue
		}
		break
	}
	if j < len(v) && v[j] >= 'a' && v[j] <= 'z' && (j+1 == len(v) || v[j+1] == '_') {
		out.letter = v[j]
		j++
	}
	for j < len(v) && v[j] == '_' {
		k := j + 1
		for k < len(v) && v[k] >= 'a' && v[k] <= 'z' {
			k++
		}
		n := k
		for n < len(v) && isDigit(v[n]) {
			n++
		}
		rank, ok := suffixRanks[v[j+1:k]]
		if !ok {
			rank = noSuffix
		}
		out.suffixes = append(out.suffixes, suffix{rank: rank, num: v[k:n]})
		j = n
	}
	return out
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return s != ""
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
