package repos

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseLine(t *testing.T) {
	for raw, want := range map[string]Line{
		"https://dl-cdn.alpinelinux.org/alpine/v3.23/main":              {Kind: Repo, URL: "https://dl-cdn.alpinelinux.org/alpine/v3.23/main", Branch: "v3.23", Repo: "main"},
		"@testing https://mirror.example.org/alpine/edge/testing":       {Kind: Repo, Tag: "@testing", URL: "https://mirror.example.org/alpine/edge/testing", Branch: "edge", Repo: "testing"},
		"v3 https://dl-cdn.alpinelinux.org/alpine/v3.24 main community": {Kind: Repo, URL: "https://dl-cdn.alpinelinux.org/alpine/v3.24", Branch: "v3.24", Repo: "main community"},
		"https://dl-cdn.alpinelinux.org/alpine/latest-stable/community": {Kind: Repo, URL: "https://dl-cdn.alpinelinux.org/alpine/latest-stable/community", Branch: "latest-stable", Repo: "community"},
		"/media/mmcblk0p1/apks":                                {Kind: Repo, URL: "/media/mmcblk0p1/apks", Local: true},
		"https://repo.example.org/alpine/apks":                 {Kind: Repo, URL: "https://repo.example.org/alpine/apks"},
		"#https://dl-cdn.alpinelinux.org/alpine/v3.23/testing": {Kind: Comment},
		"set mirror=https://dl-cdn.alpinelinux.org":            {Kind: Set},
		"   ": {Kind: Blank},
	} {
		got := ParseLine(raw)
		want.Raw = raw
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ParseLine(%q) = %+v\nwant %+v", raw, got, want)
		}
	}
}

const sample = "# my mirrors\r\n" +
	"https://mirror.example.org/alpine/v3.23/main\r\n" +
	"http://mirror.example.org/alpine/v3.23/community\r\n" +
	"@testing https://dl-cdn.alpinelinux.org/alpine/edge/testing\r\n" +
	"v3 https://dl-cdn.alpinelinux.org/alpine/v3.23 main community\r\n" +
	"https://dl-cdn.alpinelinux.org/alpine/latest-stable/main\r\n" +
	"/media/sda1/apks\r\n" +
	"set x=1\r\n" +
	"https://repo.example.org/apks" // no final newline

func TestRewriteKeepsEverythingElse(t *testing.T) {
	p, err := Rewrite(sample, "v3.24")
	if err != nil {
		t.Fatal(err)
	}
	want := "# my mirrors\r\n" +
		"https://mirror.example.org/alpine/v3.24/main\r\n" +
		"http://mirror.example.org/alpine/v3.24/community\r\n" +
		"@testing https://dl-cdn.alpinelinux.org/alpine/edge/testing\r\n" +
		"v3 https://dl-cdn.alpinelinux.org/alpine/v3.24 main community\r\n" +
		"https://dl-cdn.alpinelinux.org/alpine/latest-stable/main\r\n" +
		"/media/sda1/apks\r\n" +
		"set x=1\r\n" +
		"https://repo.example.org/apks"
	if p.Content != want {
		t.Fatalf("content:\n%q\nwant\n%q", p.Content, want)
	}
	if p.Changed != 3 || p.LatestStable != 1 {
		t.Fatalf("plan = %+v", p)
	}
	wantUnmapped := []string{"@testing https://dl-cdn.alpinelinux.org/alpine/edge/testing", "/media/sda1/apks", "https://repo.example.org/apks"}
	if !reflect.DeepEqual(p.Unmapped, wantUnmapped) {
		t.Fatalf("unmapped = %q", p.Unmapped)
	}
}

func TestRewriteToEdge(t *testing.T) {
	p, err := Rewrite("https://dl-cdn.alpinelinux.org/alpine/v3.24/main\nhttps://dl-cdn.alpinelinux.org/alpine/v3.24/community\n", "edge")
	if err != nil {
		t.Fatal(err)
	}
	if p.Content != "https://dl-cdn.alpinelinux.org/alpine/edge/main\nhttps://dl-cdn.alpinelinux.org/alpine/edge/community\n" || p.Changed != 2 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestRewriteRefusesBadTargets(t *testing.T) {
	for _, target := range []string{"", "3.24", "v3.24/main", "latest-stable", "v3.24 ", "../x"} {
		if _, err := Rewrite("https://x/alpine/v3.23/main\n", target); err == nil {
			t.Errorf("target %q accepted", target)
		}
	}
}

// The brief's row expected {"edge", "v3.23"}: the edge came from the "@testing" line, a tagged pin, which Branches
// ignores now.
func TestBranches(t *testing.T) {
	if got := Branches(sample); !reflect.DeepEqual(got, []string{"v3.23"}) {
		t.Fatalf("Branches = %q", got)
	}
}

func TestBranchesIgnoresPins(t *testing.T) {
	pins := "https://dl-cdn.alpinelinux.org/alpine/v3.23/main\n" +
		"https://dl-cdn.alpinelinux.org/alpine/v3.23/community\n" +
		"@edge http://dl-cdn.alpinelinux.org/alpine/edge/main\n" +
		"@edgecommunity http://dl-cdn.alpinelinux.org/alpine/edge/community\n" +
		"@old https://x/alpine/v3.22/main\n"
	if got := Branches(pins); !reflect.DeepEqual(got, []string{"v3.23"}) {
		t.Fatalf("Branches = %q", got)
	}
	// An untagged edge line is the system's branch.
	if got := Branches("https://x/alpine/edge/main\n@t https://x/alpine/v3.22/main\n"); !reflect.DeepEqual(got, []string{"edge"}) {
		t.Fatalf("Branches = %q", got)
	}
}

// Tagged lines (the Alpine wiki's "@edge" setup, "@old", …) are pins for pkg@tag: they never change, whatever target
// and branch, and are listed as unmapped. With a stable target untagged edge lines stay too.
func TestRewriteKeepsPins(t *testing.T) {
	const in = "https://dl-cdn.alpinelinux.org/alpine/v3.23/main\n" +
		"@edge http://dl-cdn.alpinelinux.org/alpine/edge/main\r\n" +
		"@edgecommunity http://dl-cdn.alpinelinux.org/alpine/edge/community\n" +
		"@old https://x/alpine/v3.22/main\n" +
		"@lts https://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n" +
		"https://x/alpine/edge/community\n"
	pins := []string{
		"@edge http://dl-cdn.alpinelinux.org/alpine/edge/main",
		"@edgecommunity http://dl-cdn.alpinelinux.org/alpine/edge/community",
		"@old https://x/alpine/v3.22/main",
		"@lts https://dl-cdn.alpinelinux.org/alpine/latest-stable/community",
	}
	rest := in[strings.Index(in, "@edge"):] // everything after the first line

	p, err := Rewrite(in, "v3.24")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://dl-cdn.alpinelinux.org/alpine/v3.24/main\n" + rest
	if p.Content != want || p.Changed != 1 || p.LatestStable != 0 {
		t.Fatalf("stable target: %+v\nwant content %q", p, want)
	}
	if want := append(append([]string{}, pins...), "https://x/alpine/edge/community"); !reflect.DeepEqual(p.Unmapped, want) {
		t.Fatalf("stable target: unmapped = %q", p.Unmapped)
	}

	// With the edge target the untagged edge line is on the target already: neither changed nor unmapped.
	p, err = Rewrite(in, "edge")
	if err != nil {
		t.Fatal(err)
	}
	want = "https://dl-cdn.alpinelinux.org/alpine/edge/main\n" + rest
	if p.Content != want || p.Changed != 1 || p.LatestStable != 0 {
		t.Fatalf("edge target: %+v\nwant content %q", p, want)
	}
	if !reflect.DeepEqual(p.Unmapped, pins) {
		t.Fatalf("edge target: unmapped = %q", p.Unmapped)
	}
}

// With edge every repository line points to edge, latest-stable ones too (they would not follow edge by themselves).
func TestRewriteEdgeTakesLatestStable(t *testing.T) {
	p, err := Rewrite("https://dl-cdn.alpinelinux.org/alpine/latest-stable/main\r\nv3 https://dl-cdn.alpinelinux.org/alpine/latest-stable main community\n", "edge")
	if err != nil {
		t.Fatal(err)
	}
	const want = "https://dl-cdn.alpinelinux.org/alpine/edge/main\r\nv3 https://dl-cdn.alpinelinux.org/alpine/edge main community\n"
	if p.Content != want || p.Changed != 2 || p.LatestStable != 0 || len(p.Unmapped) != 0 {
		t.Fatalf("plan = %+v\nwant content %q", p, want)
	}
}

// Only lines whose URL really changes count; a line that is on the target branch already is neither changed nor unmapped.
func TestRewriteCountsRealChanges(t *testing.T) {
	const in = "https://dl-cdn.alpinelinux.org/alpine/v3.24/main\nhttps://dl-cdn.alpinelinux.org/alpine/v3.24/community/\n"
	p, err := Rewrite(in, "v3.24")
	if err != nil {
		t.Fatal(err)
	}
	if p.Content != in || p.Changed != 0 || p.LatestStable != 0 || len(p.Unmapped) != 0 {
		t.Fatalf("plan = %+v", p)
	}
	p, err = Rewrite("https://x/alpine/edge/main\nhttps://x/alpine/v3.23/main\n", "edge")
	if err != nil {
		t.Fatal(err)
	}
	if p.Changed != 1 || len(p.Unmapped) != 0 {
		t.Fatalf("plan = %+v", p)
	}
}
