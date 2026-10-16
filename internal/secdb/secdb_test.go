package secdb

import (
	"reflect"
	"testing"
)

const mainJSON = `{"reponame":"main","distroversion":"v3.24","packages":[
 {"pkg":{"name":"openssl","secfixes":{"3.5.4-r0":["CVE-2026-1234","CVE-2026-1301"],"3.5.2-r0":["CVE-2026-0001"],"0":["CVE-2020-9999"]}}},
 {"pkg":{"name":"curl","secfixes":{"8.16.0-r1":["CVE-2026-2222 ALPINE-15000"]}}}]}`

func TestFixes(t *testing.T) {
	ix, err := Parse([]byte(mainJSON))
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Fixes("openssl", "3.5.3-r0", "3.5.4-r0"); !reflect.DeepEqual(got, []string{"CVE-2026-1234", "CVE-2026-1301"}) {
		t.Fatalf("openssl: %q", got)
	}
	if got := ix.Fixes("openssl", "3.5.1-r0", "3.5.4-r0"); len(got) != 3 {
		t.Fatalf("two fixed versions in between: %q", got)
	}
	if got := ix.Fixes("curl", "8.16.0-r0", "8.16.0-r1"); !reflect.DeepEqual(got, []string{"ALPINE-15000", "CVE-2026-2222"}) {
		t.Fatalf("curl: %q", got)
	}
	if got := ix.Fixes("openssl", "3.5.4-r0", "3.5.4-r0"); got != nil {
		t.Fatalf("no upgrade, no fix: %q", got)
	}
	if got := ix.Fixes("busybox", "1-r0", "2-r0"); got != nil {
		t.Fatalf("unknown origin: %q", got)
	}
	other, _ := Parse([]byte(`{"packages":[{"pkg":{"name":"busybox","secfixes":{"2-r0":["CVE-2026-3333"]}}}]}`))
	ix.Merge(other)
	if got := ix.Fixes("busybox", "1-r0", "2-r0"); !reflect.DeepEqual(got, []string{"CVE-2026-3333"}) {
		t.Fatalf("merged: %q", got)
	}
}

func TestFixesUpperBound(t *testing.T) {
	ix, err := Parse([]byte(`{"packages":[{"pkg":{"name":"openssl","secfixes":{"3.5.4-r0":["CVE-2026-1234"],"3.5.9-r0":["CVE-2026-9999"]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	// a fix in a version above the candidate is not part of this upgrade
	if got := ix.Fixes("openssl", "3.5.3-r0", "3.5.4-r0"); !reflect.DeepEqual(got, []string{"CVE-2026-1234"}) {
		t.Fatalf("candidate 3.5.4-r0: %q", got)
	}
	if got := ix.Fixes("openssl", "3.5.3-r0", "3.5.9-r0"); !reflect.DeepEqual(got, []string{"CVE-2026-1234", "CVE-2026-9999"}) {
		t.Fatalf("candidate 3.5.9-r0: %q", got)
	}
	if got := ix.Fixes("openssl", "3.5.3-r0", "3.5.3-r1"); got != nil {
		t.Fatalf("candidate below both fixes: %q", got)
	}
}

func TestNilIndex(t *testing.T) {
	var ix Index
	if got := ix.Fixes("openssl", "1-r0", "2-r0"); got != nil {
		t.Fatalf("Fixes on a nil Index: %q", got)
	}
	m := Index{}
	m.Merge(nil) // nothing to add
	m.Merge(ix)
	if len(m) != 0 {
		t.Fatalf("merging nothing: %v", m)
	}
}

func TestParseRejectsJunk(t *testing.T) {
	for _, junk := range []string{"", "[]", `{"apkurl":"x"}`} {
		if _, err := Parse([]byte(junk)); err == nil {
			t.Errorf("Parse(%q) accepted", junk)
		}
	}
}

func TestCVELink(t *testing.T) {
	if CVELink("CVE-2026-1234") != "https://security.alpinelinux.org/vuln/CVE-2026-1234" {
		t.Fatal("cve link")
	}
	for _, id := range []string{"ALPINE-15000", "CVE-2026-291i67", "XSA-1", "CVE-26-1"} {
		if CVELink(id) != "" {
			t.Errorf("link for %q", id)
		}
	}
}
