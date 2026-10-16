// Package releases reads alpinelinux.org/releases.json: the release branches with their end of life (also per
// repository – community can end earlier than main), the newest release of each, and from that the targets a release
// upgrade may offer and the banners the page shows.
package releases

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// URL is the release list of Alpine; Limit the most bytes it may have.
const (
	URL   = "https://alpinelinux.org/releases.json"
	Limit = 1 << 20
)

type Release struct {
	Version string `json:"version"`
	Date    string `json:"date"`
	Notes   string `json:"notes"`
}

type Repo struct {
	Name    string `json:"name"`
	EOLDate string `json:"eol_date"`
}

type Branch struct {
	RelBranch string    `json:"rel_branch"`
	EOLDate   string    `json:"eol_date"`
	Repos     []Repo    `json:"repos"`
	Releases  []Release `json:"releases"`
}

type Index struct {
	LatestStable string   `json:"latest_stable"`
	Branches     []Branch `json:"release_branches"`
}

var stableRe = regexp.MustCompile(`^v(\d+)\.(\d+)$`)

// dateLayout is the format of every eol_date.
const dateLayout = "2006-01-02"

// Parse decodes releases.json and checks its shape. It fails closed: a rel_branch that is neither "edge" nor vX.Y, or
// a non-empty eol_date that is not YYYY-MM-DD, is an error – an unreadable date would otherwise make an ended branch
// look alive. An empty eol_date means that no end is announced.
func Parse(data []byte) (*Index, error) {
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil, err
	}
	if !stableRe.MatchString(ix.LatestStable) || len(ix.Branches) == 0 {
		return nil, errors.New("releases: unexpected content")
	}
	for _, b := range ix.Branches {
		if b.RelBranch != "edge" && !stableRe.MatchString(b.RelBranch) {
			return nil, fmt.Errorf("releases: unexpected branch %q", b.RelBranch)
		}
		if err := checkDate(b.EOLDate); err != nil {
			return nil, fmt.Errorf("releases: branch %s: %w", b.RelBranch, err)
		}
		for _, r := range b.Repos {
			if err := checkDate(r.EOLDate); err != nil {
				return nil, fmt.Errorf("releases: branch %s, repository %s: %w", b.RelBranch, r.Name, err)
			}
		}
	}
	return &ix, nil
}

func checkDate(date string) error {
	if date == "" {
		return nil
	}
	if _, err := time.Parse(dateLayout, date); err != nil {
		return fmt.Errorf("eol_date %q is not YYYY-MM-DD", date)
	}
	return nil
}

// BranchOf returns the branch of a release: "3.24.2" → "v3.24"; anything that is not x.y.z (edge's
// "3.25_alpha…") → "edge"; "" → "".
func BranchOf(release string) string {
	if release == "" {
		return ""
	}
	parts := strings.SplitN(release, ".", 3)
	if len(parts) == 3 && number(parts[0]) && number(parts[1]) && number(parts[2]) {
		return "v" + parts[0] + "." + parts[1]
	}
	return "edge"
}

func number(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// Target is a branch a release upgrade may go to.
type Target struct {
	Branch string `json:"branch"`
	EOL    string `json:"eol,omitempty"`
	Latest string `json:"latest,omitempty"`
	Notes  string `json:"notes,omitempty"` // the announcement of the branch's first release, only an alpinelinux.org https URL
	Edge   bool   `json:"edge,omitempty"`
}

func minor(branch string) (int, int, bool) {
	m := stableRe.FindStringSubmatch(branch)
	if m == nil {
		return 0, 0, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a, b, true
}

func less(a1, b1, a2, b2 int) bool { return a1 < a2 || (a1 == a2 && b1 < b2) }

func (ix *Index) find(branch string) *Branch {
	for i := range ix.Branches {
		if ix.Branches[i].RelBranch == branch {
			return &ix.Branches[i]
		}
	}
	return nil
}

func expired(date string, at time.Time) bool {
	d, err := time.Parse(dateLayout, date)
	return err == nil && !at.Before(d)
}

// notesPrefix is where the announcement of a release lives; Notes of any other origin is dropped.
const notesPrefix = "https://alpinelinux.org/"

// notesPath is the shape of the notes in the real releases.json: a relative path to the Markdown source of a post.
var notesPath = regexp.MustCompile(`^posts/([A-Za-z0-9._-]+)\.md$`)

// notesURL is the link to the announcement: an https://alpinelinux.org/ URL as it is, a relative posts/<name>.md as the
// HTML post <name>.html on that host. Anything else is dropped.
func notesURL(s string) string {
	if strings.HasPrefix(s, notesPrefix) {
		return s
	}
	if m := notesPath.FindStringSubmatch(s); m != nil {
		return notesPrefix + "posts/" + m[1] + ".html"
	}
	return ""
}

// next is the stable branch right after current: the smallest listed vX.Y greater than it. It is nil when there is none,
// and for a current that is no vX.Y (edge).
func (ix *Index) next(current string) *Branch {
	ca, cb, ok := minor(current)
	if !ok {
		return nil
	}
	var next *Branch
	na, nb := 1<<30, 1<<30
	for i := range ix.Branches {
		b := &ix.Branches[i]
		ba, bb, ok := minor(b.RelBranch)
		if !ok || !less(ca, cb, ba, bb) {
			continue
		}
		if less(ba, bb, na, nb) {
			next, na, nb = b, ba, bb
		}
	}
	return next
}

// Targets are the stable branch right after current – the smallest listed vX.Y greater than it – when that has not
// reached its end of life, then edge. When the next branch has ended, there is no stable target: a jump over it is not
// offered. Nothing on edge itself, and nothing for a nil Index.
func (ix *Index) Targets(current string, today time.Time) []Target {
	if ix == nil {
		return nil
	}
	if _, _, ok := minor(current); !ok {
		return nil
	}
	next := ix.next(current)
	var out []Target
	if next != nil && !expired(next.EOLDate, today) {
		t := Target{Branch: next.RelBranch, EOL: next.EOLDate}
		if len(next.Releases) > 0 {
			t.Latest = next.Releases[0].Version // newest first
			t.Notes = notesURL(next.Releases[len(next.Releases)-1].Notes)
		}
		out = append(out, t)
	}
	if ix.find("edge") != nil {
		out = append(out, Target{Branch: "edge", Edge: true})
	}
	return out
}

// Banner is a notice for the page.
type Banner struct {
	Kind   string `json:"kind"` // new_release, next_release_ended, branch_eol, branch_eol_soon, repo_eol, repo_eol_soon
	Branch string `json:"branch"`
	Repo   string `json:"repo,omitempty"`
	Date   string `json:"date,omitempty"`
}

const soon = 60 * 24 * time.Hour

// Banners: the current branch, or one of its repositories, past or within 60 days of its end of life, then a newer
// stable branch to upgrade to – or, when the next stable branch has reached its end of life, that fact (the page offers
// no release upgrade then, and says why). Nothing for a nil Index.
func (ix *Index) Banners(current string, today time.Time) []Banner {
	if ix == nil {
		return nil
	}
	var out []Banner
	if b := ix.find(current); b != nil {
		switch {
		case expired(b.EOLDate, today):
			out = append(out, Banner{Kind: "branch_eol", Branch: current, Date: b.EOLDate})
		case expired(b.EOLDate, today.Add(soon)):
			out = append(out, Banner{Kind: "branch_eol_soon", Branch: current, Date: b.EOLDate})
		}
		for _, r := range b.Repos {
			switch {
			case r.EOLDate == "" || r.EOLDate == b.EOLDate:
			case expired(r.EOLDate, today):
				out = append(out, Banner{Kind: "repo_eol", Branch: current, Repo: r.Name, Date: r.EOLDate})
			case expired(r.EOLDate, today.Add(soon)):
				out = append(out, Banner{Kind: "repo_eol_soon", Branch: current, Repo: r.Name, Date: r.EOLDate})
			}
		}
	}
	for _, t := range ix.Targets(current, today) {
		if !t.Edge {
			out = append(out, Banner{Kind: "new_release", Branch: t.Branch, Date: t.EOL})
		}
	}
	if next := ix.next(current); next != nil && expired(next.EOLDate, today) {
		out = append(out, Banner{Kind: "next_release_ended", Branch: next.RelBranch, Date: next.EOLDate})
	}
	return out
}
