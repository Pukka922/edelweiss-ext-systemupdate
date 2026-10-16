package apk

import (
	"reflect"
	"testing"
)

const listOut = `openssl-3.5.4-r0 aarch64 {openssl} (Apache-2.0) [upgradable from: openssl-3.5.3-r0]
linux-rpi-6.12.51-r0 aarch64 {linux-rpi} (GPL-2.0-only) [upgradable from: linux-rpi-6.12.48-r0]
py3-foo-bar-1.0-r1 noarch {py-foo-bar} (MIT AND BSD-2-Clause) [upgradable from: py3-foo-bar-1.0-r0]
garbage line
`

func TestParseUpgradable(t *testing.T) {
	ups, skipped := ParseUpgradable(listOut)
	want := []Upgrade{
		{Name: "linux-rpi", Old: "6.12.48-r0", New: "6.12.51-r0", Arch: "aarch64", Origin: "linux-rpi", License: "GPL-2.0-only"},
		{Name: "openssl", Old: "3.5.3-r0", New: "3.5.4-r0", Arch: "aarch64", Origin: "openssl", License: "Apache-2.0"},
		{Name: "py3-foo-bar", Old: "1.0-r0", New: "1.0-r1", Arch: "noarch", Origin: "py-foo-bar", License: "MIT AND BSD-2-Clause"},
	}
	if !reflect.DeepEqual(ups, want) {
		t.Fatalf("ups = %+v\nwant %+v", ups, want)
	}
	if !reflect.DeepEqual(skipped, []string{"garbage line"}) {
		t.Fatalf("skipped = %q", skipped)
	}
}

func TestSplitNameVersion(t *testing.T) {
	for in, want := range map[string][2]string{
		"openssl-3.5.4-r0":   {"openssl", "3.5.4-r0"},
		"py3-foo-bar-1.0-r1": {"py3-foo-bar", "1.0-r1"},
		"gtk+2.0-2.24.33-r9": {"gtk+2.0", "2.24.33-r9"},
	} {
		n, v, ok := SplitNameVersion(in)
		if !ok || n != want[0] || v != want[1] {
			t.Errorf("SplitNameVersion(%q) = %q, %q, %v", in, n, v, ok)
		}
	}
	if _, _, ok := SplitNameVersion("noversion"); ok {
		t.Error("noversion split")
	}
}

const simOut = `(1/5) Upgrading busybox (1.37.0-r20 -> 1.37.0-r24)
(2/5) Installing libfoo (2.0-r0)
(3/5) Purging gtk+2.0 (2.24.33-r9)
(4/5) Downgrading bar@testing (2.0-r1 -> 1.9-r0)
(5/5) Replacing baz (1.0-r0 -> 1.0-r1)
Need to download 38.5 MiB of packages.
After this operation, 3 MiB of additional disk space will be used.
OK: 412 MiB in 120 packages
`

func TestParseSimulation(t *testing.T) {
	s := ParseSimulation(simOut)
	want := []Change{
		{Verb: "Upgrading", Name: "busybox", Old: "1.37.0-r20", New: "1.37.0-r24"},
		{Verb: "Installing", Name: "libfoo", New: "2.0-r0"},
		{Verb: "Purging", Name: "gtk+2.0", Old: "2.24.33-r9"},
		{Verb: "Downgrading", Name: "bar@testing", Old: "2.0-r1", New: "1.9-r0"},
		{Verb: "Replacing", Name: "baz", Old: "1.0-r0", New: "1.0-r1"},
	}
	if !reflect.DeepEqual(s.Changes, want) {
		t.Fatalf("changes = %+v", s.Changes)
	}
	if !s.SizesKnown || s.Download != int64(38.5*(1<<20)) || s.Additional != 3<<20 || !s.OK || s.Errors != 0 {
		t.Fatalf("summary = %+v", s)
	}
	if !reflect.DeepEqual(s.Removed(), []string{"gtk+2.0"}) || !reflect.DeepEqual(s.Downgraded(), []string{"bar@testing"}) {
		t.Fatalf("removed %q downgraded %q", s.Removed(), s.Downgraded())
	}
	freed := ParseSimulation("After this operation, 2 KiB of disk space will be freed.\n2 errors; 10 MiB in 9 packages\n")
	if freed.Additional != -2048 || freed.OK || freed.Errors != 2 {
		t.Fatalf("freed = %+v", freed)
	}
}

func TestHasChanges(t *testing.T) {
	if HasChanges("fetch https://x\n(1/2) Downloading foo\nOK: 1 MiB in 2 packages\n") {
		t.Error("downloads are no change")
	}
	if !HasChanges("(1/1) Upgrading foo (1-r0 -> 2-r0)\n") {
		t.Error("an upgrade is a change")
	}
}

// apk-tools 3 pads the counter to the width of the total ("(%*i/%i)"): a transaction of 400 changes prints
// "(  1/400)" … "( 99/400)". Those are changes too: a job that died within them must not count as unchanged.
const paddedOut = `(  1/400) Upgrading busybox (1.37.0-r20 -> 1.37.0-r24)
(  2/400) Installing libfoo (2.0-r0)
( 99/400) Purging gtk+2.0 (2.24.33-r9)
(100/400) Downgrading bar@testing (2.0-r1 -> 1.9-r0)
(400/400) Replacing baz (1.0-r0 -> 1.0-r1)
OK: 412 MiB in 120 packages
`

func TestParseSimulationPaddedCounters(t *testing.T) {
	s := ParseSimulation(paddedOut)
	want := []Change{
		{Verb: "Upgrading", Name: "busybox", Old: "1.37.0-r20", New: "1.37.0-r24"},
		{Verb: "Installing", Name: "libfoo", New: "2.0-r0"},
		{Verb: "Purging", Name: "gtk+2.0", Old: "2.24.33-r9"},
		{Verb: "Downgrading", Name: "bar@testing", Old: "2.0-r1", New: "1.9-r0"},
		{Verb: "Replacing", Name: "baz", Old: "1.0-r0", New: "1.0-r1"},
	}
	if !reflect.DeepEqual(s.Changes, want) {
		t.Fatalf("changes = %+v", s.Changes)
	}
	if !reflect.DeepEqual(s.Removed(), []string{"gtk+2.0"}) || !reflect.DeepEqual(s.Downgraded(), []string{"bar@testing"}) {
		t.Fatalf("removed %q downgraded %q", s.Removed(), s.Downgraded())
	}
}

func TestHasChangesPaddedCounters(t *testing.T) {
	for _, line := range []string{
		"( 1/12) Upgrading musl (1.2.5-r10 -> 1.2.6-r0)",
		"(  1/400) Installing libfoo (2.0-r0)",
		"( 99/400) Purging gtk+2.0 (2.24.33-r9)",
		"( 5/12) Downgrading bar@testing (2.0-r1 -> 1.9-r0)",
		"\r( 1/12) Upgrading musl (1.2.5-r10 -> 1.2.6-r0)\r",
	} {
		if !HasChanges("fetch https://x\n" + line + "\n") {
			t.Errorf("%q is a change", line)
		}
	}
	if HasChanges("fetch https://x\n( 1/12) Downloading foo\nOK: 1 MiB in 2 packages\n") {
		t.Error("a padded download is no change")
	}
}

func TestHasChangesCountsEveryVerbButDownloading(t *testing.T) {
	// the spec: every verb except Downloading counts as a change, also one this parser does not know (a failed open
	// must not mean "nothing changed", as the repositories would be restored over a half-done upgrade)
	for _, line := range []string{
		"(1/2) Frobbing x",
		"( 1/12) Frobbing x (1-r0 -> 2-r0)",
		"(  1/400) Executing busybox-1.37.0-r30.trigger",
		"\r(1/2) Frobbing x\r",
	} {
		if !HasChanges("fetch https://x\n" + line + "\n") {
			t.Errorf("%q is a change", line)
		}
	}
	for _, line := range []string{
		"(1/2) Downloading x",
		"( 1/12) Downloading x (1-r0)",
		"(  1/400) Downloading musl-1.2.6-r0",
		"OK: 1 MiB in 2 packages",
		"(1/2)Frobbing x",        // not a change line: no space after the counter
		"(a/b) Frobbing x",       // no counter
		"fetch (1/2) Frobbing x", // not at the start of the line
	} {
		if HasChanges("fetch https://x\n" + line + "\n") {
			t.Errorf("%q is no change", line)
		}
	}
}

func TestLastProgress(t *testing.T) {
	p, ok := LastProgress([]byte("0/10 download\n4/10 download\n7/10 install\n"))
	if !ok || p != (Progress{Cur: 7, Max: 10, Stage: "install"}) {
		t.Fatalf("p = %+v %v", p, ok)
	}
	if p, ok := LastProgress([]byte("3/9\n")); !ok || p.Cur != 3 || p.Stage != "" {
		t.Fatalf("no stage: %+v", p)
	}
	if _, ok := LastProgress(nil); ok {
		t.Error("empty")
	}
}

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		exit   int
		out    string
		want   Outcome
		errors int
	}{
		{0, "", OK, 0},
		{3, "", Partial, 3},
		{99, "ERROR: Unable to lock database: Resource temporarily unavailable", LockTimeout, 0},
		{99, "ERROR: Failed to open apk database", Failed, 0},
		{-1, "", Failed, 0},
	} {
		if o, n := Classify(c.exit, c.out); o != c.want || n != c.errors {
			t.Errorf("Classify(%d) = %v, %d", c.exit, o, n)
		}
	}
}
