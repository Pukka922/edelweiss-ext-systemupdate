// Package secdb reads Alpine's security database (secdb.alpinelinux.org/<branch>/<repo>.json): for each origin
// package the versions that fixed security issues. An upgrade from installed to candidate is a security fix when one
// of those versions lies in (installed, candidate].
package secdb

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
)

// Limit is the most bytes a secdb file may have.
const Limit = 8 << 20

// URL is the secdb file of a branch (v3.24, edge) and repository (main, community).
func URL(branch, repo string) string {
	return "https://secdb.alpinelinux.org/" + branch + "/" + repo + ".json"
}

type file struct {
	Packages []struct {
		Pkg struct {
			Name     string              `json:"name"`
			Secfixes map[string][]string `json:"secfixes"`
		} `json:"pkg"`
	} `json:"packages"`
}

// Index maps an origin package to its fixed versions and their ids (CVE-…, ALPINE-…, XSA-…).
type Index map[string]map[string][]string

// Parse reads one secdb file.
func Parse(data []byte) (Index, error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Packages == nil {
		return nil, errors.New("secdb: unexpected content")
	}
	ix := Index{}
	for _, p := range f.Packages {
		if p.Pkg.Name == "" {
			continue
		}
		m := ix[p.Pkg.Name]
		if m == nil {
			m = map[string][]string{}
			ix[p.Pkg.Name] = m
		}
		for v, ids := range p.Pkg.Secfixes {
			for _, id := range ids {
				m[v] = append(m[v], strings.Fields(id)...) // an entry may hold several ids
			}
		}
	}
	return ix, nil
}

// Merge adds other's entries (main and community of a branch). ix must not be nil: start from Index{} (or from the
// result of Parse). A nil other adds nothing.
func (ix Index) Merge(other Index) {
	for origin, fixes := range other {
		m := ix[origin]
		if m == nil {
			m = map[string][]string{}
			ix[origin] = m
		}
		for v, ids := range fixes {
			m[v] = append(m[v], ids...)
		}
	}
}

// Fixes returns the sorted, distinct ids fixed by upgrading origin from installed to candidate. Version "0" means
// "never affected" and is skipped.
func (ix Index) Fixes(origin, installed, candidate string) []string {
	seen := map[string]bool{}
	for v, ids := range ix[origin] {
		if v == "0" || apk.Compare(installed, v) >= 0 || apk.Compare(v, candidate) > 0 {
			continue
		}
		for _, id := range ids {
			seen[id] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

var cveRe = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)

// CVELink links a CVE id to Alpine's security tracker ("" for other ids).
func CVELink(id string) string {
	if !cveRe.MatchString(id) {
		return ""
	}
	return "https://security.alpinelinux.org/vuln/" + id
}
