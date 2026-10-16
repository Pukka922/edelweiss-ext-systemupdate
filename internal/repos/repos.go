// Package repos reads and rewrites apk's repository list (/etc/apk/repositories). A release upgrade only changes
// the branch segment (vX.Y or edge) of lines that have one; everything else – comments, @tags, mirrors, local paths,
// set lines, third-party repositories, line endings – stays byte for byte.
package repos

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

// Kind is the kind of a line.
type Kind int

const (
	Blank Kind = iota
	Comment
	Set
	Repo
)

// Line is one line of a repository file.
type Line struct {
	Raw    string
	Kind   Kind
	Tag    string // "@testing"
	URL    string
	Branch string // v3.24, edge or latest-stable; "" if the URL has no branch segment
	Repo   string // main, community, testing (or the components of a "url component…" line)
	Local  bool   // a path on this host (/media/…/apks)
}

var (
	branchSeg = regexp.MustCompile(`^(v\d+\.\d+|edge|latest-stable)$`)
	targetRe  = regexp.MustCompile(`^(v\d+\.\d+|edge)$`)
	repoNames = map[string]bool{"main": true, "community": true, "testing": true}
)

// ValidTarget reports whether t can be a rewrite target: a stable branch (v3.24) or edge.
func ValidTarget(t string) bool { return targetRe.MatchString(t) }

// ParseLine reads a line: "[v2|v3|ndx] [@tag] url [component…]", a comment, a "set" line or a blank line.
func ParseLine(raw string) Line {
	l := Line{Raw: raw}
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		l.Kind = Blank
		return l
	case strings.HasPrefix(s, "#"):
		l.Kind = Comment
		return l
	case strings.HasPrefix(s, "set "):
		l.Kind = Set
		return l
	}
	l.Kind = Repo
	f := strings.Fields(s)
	i := 0
	if i < len(f) && (f[i] == "v2" || f[i] == "v3" || f[i] == "ndx") {
		i++
	}
	if i < len(f) && strings.HasPrefix(f[i], "@") {
		l.Tag = f[i]
		i++
	}
	if i >= len(f) {
		return l
	}
	l.URL = f[i]
	if strings.HasPrefix(l.URL, "/") {
		l.Local = true
		return l
	}
	parts := strings.Split(strings.TrimSuffix(l.URL, "/"), "/")
	if comps := f[i+1:]; len(comps) > 0 {
		if last := parts[len(parts)-1]; branchSeg.MatchString(last) {
			l.Branch, l.Repo = last, strings.Join(comps, " ")
		}
		return l
	}
	if n := len(parts); n >= 2 && branchSeg.MatchString(parts[n-2]) && repoNames[parts[n-1]] {
		l.Branch, l.Repo = parts[n-2], parts[n-1]
	}
	return l
}

// Branches returns the distinct vX.Y and edge branches the content's untagged repository lines point to. Tagged lines
// are pins for pkg@tag (the Alpine wiki's "@edge" setup) and not the system's branch; latest-stable is no branch name.
func Branches(content string) []string {
	seen := map[string]bool{}
	for _, raw := range strings.Split(content, "\n") {
		l := ParseLine(strings.TrimRight(raw, "\r"))
		if l.Kind == Repo && l.Tag == "" && l.Branch != "" && l.Branch != "latest-stable" {
			seen[l.Branch] = true
		}
	}
	out := make([]string, 0, len(seen))
	for b := range seen {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

// Plan is a rewritten repository file.
type Plan struct {
	Content      string   `json:"-"`
	Changed      int      `json:"changed"`
	Unmapped     []string `json:"unmapped"`     // repository lines that stay as they are
	LatestStable int      `json:"latestStable"` // lines that follow latest-stable (they move by themselves)
}

// Rewrite points the untagged repository lines with a vX.Y, latest-stable or edge segment to target; a line counts
// as changed only when its URL really changes (one that is on target already is neither changed nor unmapped).
// These lines stay as they are and are listed as unmapped:
//   - tagged lines (@edge, @testing, …): they are pins for pkg@tag whatever their branch, and a rewrite would change
//     what the pin means;
//   - lines without a branch segment: local paths, third-party repositories, mirrors with their own layout;
//   - with a stable target the edge lines (only vX.Y lines move to a stable branch) and the testing repository
//     (it exists on edge only).
//
// With a stable target the latest-stable lines stay too, counted in LatestStable: they move by themselves. With edge
// they are rewritten like all others: "latest-stable" would not follow edge.
func Rewrite(content, target string) (Plan, error) {
	if !ValidTarget(target) {
		return Plan{}, errors.New("repos: invalid target")
	}
	edge := target == "edge"
	p := Plan{Unmapped: []string{}}
	lines := strings.Split(content, "\n")
	for i, raw := range lines {
		body, cr := strings.CutSuffix(raw, "\r")
		l := ParseLine(body)
		if l.Kind != Repo {
			continue
		}
		switch {
		case l.Tag == "" && l.Branch == "latest-stable" && !edge:
			p.LatestStable++
			continue
		case l.Tag != "" || l.Branch == "" || (!edge && (l.Branch == "edge" || l.Repo == "testing")):
			p.Unmapped = append(p.Unmapped, strings.TrimSpace(body))
			continue
		}
		url := replaceBranch(l, target)
		if url == l.URL {
			continue
		}
		body = strings.Replace(body, l.URL, url, 1)
		if cr {
			body += "\r"
		}
		lines[i] = body
		p.Changed++
	}
	p.Content = strings.Join(lines, "\n")
	return p, nil
}

func replaceBranch(l Line, target string) string {
	u := strings.TrimSuffix(l.URL, "/")
	trailing := strings.TrimPrefix(l.URL, u)
	parts := strings.Split(u, "/")
	if n := len(parts); n >= 2 && repoNames[parts[n-1]] && parts[n-2] == l.Branch {
		parts[n-2] = target
	} else {
		parts[n-1] = target
	}
	return strings.Join(parts, "/") + trailing
}
