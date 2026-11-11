package releases

import (
	"reflect"
	"testing"
	"time"
)

const sample = `{"latest_stable":"v3.24","architectures":["x86_64","aarch64"],"release_branches":[
 {"rel_branch":"edge","git_branch":"master","arches":["x86_64"],"repos":[{"name":"main"},{"name":"community"},{"name":"testing"}]},
 {"rel_branch":"v3.24","eol_date":"2028-06-01","repos":[{"name":"main"},{"name":"community","eol_date":"2026-11-01"}],
  "releases":[{"version":"3.24.2","date":"2026-09-17","notes":"https://alpinelinux.org/posts/Alpine-3.24.2-released.html"},{"version":"3.24.0","date":"2026-05-20","notes":"https://alpinelinux.org/posts/Alpine-3.24.0-released.html"}]},
 {"rel_branch":"v3.23","eol_date":"2027-11-01","repos":[{"name":"main"},{"name":"community","eol_date":"2026-05-01"}],
  "releases":[{"version":"3.23.6","date":"2026-09-17","notes":""}]},
 {"rel_branch":"v3.22","eol_date":"2027-05-01","repos":[{"name":"main"},{"name":"community","eol_date":"2025-11-01"}],"releases":[{"version":"3.22.6","date":"2026-09-17"}]},
 {"rel_branch":"v3.20","eol_date":"2026-04-01","repos":[{"name":"main"}],"releases":[{"version":"3.20.10","date":"2026-01-01"}]}
]}`

var today = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

func TestBranchOf(t *testing.T) {
	for in, want := range map[string]string{"3.24.2": "v3.24", "3.23.0": "v3.23", "3.25_alpha20261001": "edge", "": ""} {
		if got := BranchOf(in); got != want {
			t.Errorf("BranchOf(%q) = %q", in, got)
		}
	}
}

func TestTargets(t *testing.T) {
	ix, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{Branch: "v3.24", EOL: "2028-06-01", Latest: "3.24.2", Notes: "https://alpinelinux.org/posts/Alpine-3.24.0-released.html"},
		{Branch: "edge", Edge: true},
	}
	if got := ix.Targets("v3.23", today); !reflect.DeepEqual(got, want) {
		t.Fatalf("Targets(v3.23) = %+v", got)
	}
	if got := ix.Targets("v3.22", today); len(got) != 2 || got[0].Branch != "v3.23" {
		t.Fatalf("only the next branch: Targets(v3.22) = %+v", got)
	}
	if got := ix.Targets("v3.24", today); len(got) != 1 || !got[0].Edge {
		t.Fatalf("Targets(v3.24) = %+v", got)
	}
	if got := ix.Targets("edge", today); len(got) != 0 {
		t.Fatalf("Targets(edge) = %+v", got)
	}
}

func TestTargetsNeverSkipAnEndedBranch(t *testing.T) {
	ix, _ := Parse([]byte(sample))
	// v3.20 ended on 2026-04-01: from v3.19 it is the next branch, and it is not offered. v3.22 is no target: the
	// spec allows only the next stable branch, never a jump over one (an upgrade over several branches is not
	// supported by Alpine).
	if got := ix.Targets("v3.19", today); len(got) != 1 || !got[0].Edge {
		t.Fatalf("Targets(v3.19) = %+v, want edge only", got)
	}
	// the end date itself counts as ended
	if got := ix.Targets("v3.19", time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)); len(got) != 2 || got[0].Branch != "v3.20" {
		t.Fatalf("Targets(v3.19) the day before the end = %+v", got)
	}
	if got := ix.Targets("v3.19", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)); len(got) != 1 || !got[0].Edge {
		t.Fatalf("Targets(v3.19) on the end date = %+v", got)
	}
	// a branch the list does not name is no stop: the next one it names is
	if got := ix.Targets("v3.21", today); len(got) != 2 || got[0].Branch != "v3.22" {
		t.Fatalf("Targets(v3.21) = %+v", got)
	}
	// no banner offers a release that cannot be upgraded to: the page says why there is none instead
	if got := ix.Banners("v3.19", today); len(got) != 1 || got[0].Kind != "next_release_ended" {
		t.Fatalf("Banners(v3.19) = %+v", got)
	}
}

// The release dialog opens from a new_release banner only, so its "the next release has ended" text could never show:
// the banner says it instead.
func TestBannersNextReleaseEnded(t *testing.T) {
	ix, _ := Parse([]byte(sample))
	ended := Banner{Kind: "next_release_ended", Branch: "v3.20", Date: "2026-04-01"}
	if got := ix.Banners("v3.19", today); !reflect.DeepEqual(got, []Banner{ended}) {
		t.Fatalf("Banners(v3.19) = %+v", got)
	}
	// the day before the end it is still offered, on the end date it has ended
	if got := ix.Banners("v3.19", time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)); len(got) != 1 || got[0].Kind != "new_release" || got[0].Branch != "v3.20" {
		t.Fatalf("the day before the end: %+v", got)
	}
	if got := ix.Banners("v3.19", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)); !reflect.DeepEqual(got, []Banner{ended}) {
		t.Fatalf("on the end date: %+v", got)
	}
	// only the immediate next branch counts: v3.21 is not in the list, the next one it names (v3.22) is alive
	if got := ix.Banners("v3.21", today); len(got) != 1 || got[0].Kind != "new_release" || got[0].Branch != "v3.22" {
		t.Fatalf("Banners(v3.21) = %+v", got)
	}
	// after the notices of the installed branch
	both, _ := Parse([]byte(`{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"v3.19","eol_date":"2025-11-01"},
 {"rel_branch":"v3.20","eol_date":"2026-04-01"}]}`))
	if got := both.Banners("v3.19", today); len(got) != 2 || got[0].Kind != "branch_eol" || got[1].Kind != "next_release_ended" {
		t.Fatalf("both ended: %+v", got)
	}
	// nothing on edge, and nothing for a next branch that announces no end
	if got := ix.Banners("edge", today); len(got) != 0 {
		t.Fatalf("Banners(edge) = %+v", got)
	}
	open, _ := Parse([]byte(`{"latest_stable":"v3.24","release_branches":[{"rel_branch":"v3.20"},{"rel_branch":"v3.19"}]}`))
	if got := open.Banners("v3.19", today); len(got) != 1 || got[0].Kind != "new_release" {
		t.Fatalf("a branch without an end date is alive: %+v", got)
	}
}

func TestBannersEndDates(t *testing.T) {
	ix, _ := Parse([]byte(`{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"v3.24","eol_date":"2028-06-01","repos":[{"name":"main","eol_date":"2028-06-01"},{"name":"community","eol_date":"2026-11-01"}]},
 {"rel_branch":"v3.20","eol_date":"2026-04-01","repos":[{"name":"main","eol_date":"2026-04-01"},{"name":"community","eol_date":"2026-04-01"}]}]}`))
	// a repository that ends with its branch gets no banner of its own
	got := ix.Banners("v3.20", today)
	if len(got) != 2 || got[0].Kind != "branch_eol" || got[1].Kind != "new_release" {
		t.Fatalf("Banners(v3.20) = %+v", got)
	}
	// v3.24 is the newest branch here: its community waits for the next release, on the end date and the day before
	if got := ix.Banners("v3.24", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); len(got) != 1 || got[0].Kind != "repo_eol_next" {
		t.Fatalf("on the end date: %+v", got)
	}
	if got := ix.Banners("v3.24", time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)); len(got) != 1 || got[0].Kind != "repo_eol_next" {
		t.Fatalf("the day before: %+v", got)
	}
}

// repoDoc is a list whose v3.24 has a community repository with the given end date; newer adds a v3.25 after it.
func repoDoc(t *testing.T, repoEOL string, newer bool) *Index {
	t.Helper()
	doc := `{"latest_stable":"v3.24","release_branches":[`
	if newer {
		doc = `{"latest_stable":"v3.25","release_branches":[{"rel_branch":"v3.25","eol_date":"2028-11-01"},`
	}
	doc += `{"rel_branch":"v3.24","eol_date":"2028-06-01","repos":[{"name":"main"},{"name":"community","eol_date":"` + repoEOL + `"}]}]}`
	ix, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestBannersRepoNewestBranch(t *testing.T) {
	at := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	want := []Banner{{Kind: "repo_eol_next", Branch: "v3.24", Repo: "community", Date: "2026-11-01"}}
	if got := repoDoc(t, "2026-11-01", false).Banners("v3.24", at); !reflect.DeepEqual(got, want) {
		t.Fatalf("passed: %+v", got)
	}
	want[0].Date = "2026-12-20"
	if got := repoDoc(t, "2026-12-20", false).Banners("v3.24", at); !reflect.DeepEqual(got, want) {
		t.Fatalf("within 60 days: %+v", got)
	}
	if got := repoDoc(t, "2027-01-20", false).Banners("v3.24", at); len(got) != 0 {
		t.Fatalf("more than 60 days ahead: %+v", got)
	}
}

func TestBannersRepoNewerBranchListed(t *testing.T) {
	at := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	want := []Banner{
		{Kind: "repo_eol", Branch: "v3.24", Repo: "community", Date: "2026-11-01"},
		{Kind: "new_release", Branch: "v3.25", Date: "2028-11-01"},
	}
	if got := repoDoc(t, "2026-11-01", true).Banners("v3.24", at); !reflect.DeepEqual(got, want) {
		t.Fatalf("passed: %+v", got)
	}
	want[0] = Banner{Kind: "repo_eol_soon", Branch: "v3.24", Repo: "community", Date: "2026-12-20"}
	if got := repoDoc(t, "2026-12-20", true).Banners("v3.24", at); !reflect.DeepEqual(got, want) {
		t.Fatalf("within 60 days: %+v", got)
	}
}

func TestBannersRepoDateEqualOrEmpty(t *testing.T) {
	at := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	for _, newer := range []bool{false, true} {
		for _, d := range []string{"", "2028-06-01"} {
			for _, b := range repoDoc(t, d, newer).Banners("v3.24", at) {
				if b.Repo != "" {
					t.Fatalf("repo %q, newer %v: %+v", d, newer, b)
				}
			}
		}
	}
}

// TestBannersRealList is the shape of the real list on 2026-11-05: v3.24 is the newest branch and its community
// repository is past its planned end – there is nothing to upgrade to, so only the soft notice shows.
func TestBannersRealList(t *testing.T) {
	ix, err := Parse([]byte(`{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"edge","repos":[{"name":"main"},{"name":"community"},{"name":"testing"}]},
 {"rel_branch":"v3.24","eol_date":"2028-06-01","repos":[{"name":"main"},{"name":"community","eol_date":"2026-11-01"}]},
 {"rel_branch":"v3.23","eol_date":"2027-11-01","repos":[{"name":"main"},{"name":"community","eol_date":"2026-05-01"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Banner{{Kind: "repo_eol_next", Branch: "v3.24", Repo: "community", Date: "2026-11-01"}}
	if got := ix.Banners("v3.24", time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)); !reflect.DeepEqual(got, want) {
		t.Fatalf("Banners(v3.24) = %+v", got)
	}
}

func TestBanners(t *testing.T) {
	ix, _ := Parse([]byte(sample))
	want := []Banner{
		{Kind: "repo_eol", Branch: "v3.23", Repo: "community", Date: "2026-05-01"},
		{Kind: "new_release", Branch: "v3.24", Date: "2028-06-01"},
	}
	if got := ix.Banners("v3.23", today); !reflect.DeepEqual(got, want) {
		t.Fatalf("Banners(v3.23) = %+v", got)
	}
	if got := ix.Banners("v3.24", today); len(got) != 1 || got[0].Kind != "repo_eol_next" {
		t.Fatalf("Banners(v3.24) = %+v", got) // community of 3.24 ends 2026-11-01, within 60 days, no newer branch
	}
	if got := ix.Banners("v3.20", today); len(got) == 0 || got[0].Kind != "branch_eol" {
		t.Fatalf("Banners(v3.20) = %+v", got)
	}
}

func TestBannersBranchEOLSoon(t *testing.T) {
	branches := func(eol string) *Index {
		ix, err := Parse([]byte(`{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"v3.24","eol_date":"2028-06-01"},
 {"rel_branch":"v3.22","eol_date":"` + eol + `","repos":[{"name":"main","eol_date":"` + eol + `"}]}]}`))
		if err != nil {
			t.Fatal(err)
		}
		return ix
	}
	want := []Banner{
		{Kind: "branch_eol_soon", Branch: "v3.22", Date: "2026-11-15"},
		{Kind: "new_release", Branch: "v3.24", Date: "2028-06-01"},
	}
	if got := branches("2026-11-15").Banners("v3.22", today); !reflect.DeepEqual(got, want) {
		t.Fatalf("Banners(v3.22) = %+v", got)
	}
	// 60 days from 2026-10-13 is 2026-12-12: the last day that still counts as soon
	if got := branches("2026-12-12").Banners("v3.22", today); len(got) != 2 || got[0].Kind != "branch_eol_soon" {
		t.Fatalf("60 days ahead: %+v", got)
	}
	if got := branches("2026-12-13").Banners("v3.22", today); len(got) != 1 || got[0].Kind != "new_release" {
		t.Fatalf("61 days ahead: %+v", got)
	}
}

func TestNotesOnlyFromAlpine(t *testing.T) {
	for notes, want := range map[string]string{
		"https://alpinelinux.org/posts/Alpine-3.24.0-released.html": "https://alpinelinux.org/posts/Alpine-3.24.0-released.html",
		"http://alpinelinux.org/posts/x.html":                       "",
		"https://alpinelinux.org.evil.example/x":                    "",
		"https://evil.example/https://alpinelinux.org/":             "",
		"javascript:alert(1)":                                       "",
		"":                                                          "",
	} {
		ix, err := Parse([]byte(`{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"v3.24","releases":[{"version":"3.24.0","notes":"` + notes + `"}]}]}`))
		if err != nil {
			t.Fatal(err)
		}
		got := ix.Targets("v3.23", today)
		if len(got) != 1 || got[0].Branch != "v3.24" || got[0].Notes != want {
			t.Errorf("notes %q: Targets = %+v, want notes %q", notes, got, want)
		}
	}
}

// The real releases.json has relative notes ("posts/Alpine-3.24.0-released.md"): the page links to the HTML post.
func TestNotesRelativeLikeTheRealList(t *testing.T) {
	const real = `{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"v3.24","eol_date":"2028-06-01","releases":[
  {"version":"3.24.2","date":"2026-09-17","notes":"posts/Alpine-3.24.2-released.md"},
  {"version":"3.24.0","date":"2026-05-20","notes":"posts/Alpine-3.24.0-released.md"}]}]}`
	ix, err := Parse([]byte(real))
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Targets("v3.23", today)
	if len(got) != 1 || got[0].Notes != "https://alpinelinux.org/posts/Alpine-3.24.0-released.html" {
		t.Fatalf("Targets(v3.23) = %+v", got)
	}
	for notes, want := range map[string]string{
		"posts/Alpine-3.24.0-released.md":      "https://alpinelinux.org/posts/Alpine-3.24.0-released.html",
		"posts/Alpine-3.25.0_rc1-released.md":  "https://alpinelinux.org/posts/Alpine-3.25.0_rc1-released.html",
		"https://alpinelinux.org/posts/x.html": "https://alpinelinux.org/posts/x.html",
		"posts/x.html":                         "",
		"posts/x.md?y=1":                       "",
		"posts/x.md#a":                         "",
		"posts/x.md\n":                         "",
		"posts/a b.md":                         "",
		"posts/sub/x.md":                       "",
		"posts/../x.md":                        "",
		"posts/.md":                            "",
		"/posts/x.md":                          "",
		"//evil.example/posts/x.md":            "",
		"other/x.md":                           "",
		"x.md":                                 "",
		"mailto:x@example.org":                 "",
	} {
		if got := notesURL(notes); got != want {
			t.Errorf("notesURL(%q) = %q, want %q", notes, got, want)
		}
	}
}

func TestNilIndex(t *testing.T) {
	var ix *Index
	if got := ix.Targets("v3.23", today); got != nil {
		t.Fatalf("Targets on a nil Index = %+v", got)
	}
	if got := ix.Banners("v3.23", today); got != nil {
		t.Fatalf("Banners on a nil Index = %+v", got)
	}
}

func TestParseFailsClosed(t *testing.T) {
	doc := func(branch, branchEOL, repoEOL string) string {
		return `{"latest_stable":"v3.24","release_branches":[{"rel_branch":"` + branch + `","eol_date":"` + branchEOL +
			`","repos":[{"name":"main","eol_date":"` + repoEOL + `"}]}]}`
	}
	// the sample of this package parses, and so do empty dates and edge
	for _, ok := range []string{sample, doc("v3.24", "", ""), doc("edge", "", ""), doc("v3.9", "2027-05-01", "2026-11-01")} {
		if _, err := Parse([]byte(ok)); err != nil {
			t.Errorf("Parse(%q): %v", ok, err)
		}
	}
	for name, bad := range map[string]string{
		"branch date without zeros": doc("v3.22", "2027-5-1", ""),
		"branch date with a time":   doc("v3.22", "2027-05-01T00:00:00Z", ""),
		"branch date in words":      doc("v3.22", "soon", ""),
		"branch date out of range":  doc("v3.22", "2027-13-01", ""),
		"repo date without zeros":   doc("v3.22", "2027-05-01", "2026-5-1"),
		"repo date with a time":     doc("v3.22", "2027-05-01", "2026-05-01 00:00"),
		"branch without v":          doc("3.22", "2027-05-01", ""),
		"branch with a patch level": doc("v3.22.1", "2027-05-01", ""),
		"branch named stable":       doc("stable", "2027-05-01", ""),
		"branch with a space":       doc("v3.22 ", "2027-05-01", ""),
		"empty branch":              doc("", "2027-05-01", ""),
		"branch latest-stable":      doc("latest-stable", "", ""),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseRejectsJunk(t *testing.T) {
	for _, junk := range []string{"", "[]", `{"latest_stable":1}`, `{"latest_stable":"v3.24"}`} {
		if _, err := Parse([]byte(junk)); err == nil {
			t.Errorf("Parse(%q) accepted", junk)
		}
	}
}
