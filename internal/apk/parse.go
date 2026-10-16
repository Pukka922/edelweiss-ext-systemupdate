package apk

import (
	"bytes"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// LockWait is what apk --wait prints while another apk holds the database lock.
const LockWait = "Waiting for repository lock"

var (
	upgradableLine = regexp.MustCompile(`^(\S+) (\S+) \{([^}]*)\} \((.*)\) \[upgradable from: (\S+)\]$`)
	nameVersion    = regexp.MustCompile(`^(.+)-([^-]+-r\d+)$`)
	// apk-tools 3 pads the counter to the width of the total ("(%*i/%i)"): "(  1/400) Upgrading …"
	changeLine   = regexp.MustCompile(`^\(\s*(\d+)/(\d+)\) (Installing|Upgrading|Downgrading|Purging|Replacing|Reinstalling|Updating pinning) (\S+) \((.+?)(?: -> (.+))?\)$`)
	downloadLine = regexp.MustCompile(`^Need to download ([0-9]+(?:\.[0-9]+)?) (B|KiB|MiB|GiB|TiB) of packages`)
	afterLine    = regexp.MustCompile(`^After this operation, ([0-9]+(?:\.[0-9]+)?) (B|KiB|MiB|GiB|TiB) of (?:additional )?disk space will be (used|freed)`)
	errorsLine   = regexp.MustCompile(`^(\d+) errors?;`)
	progressLine = regexp.MustCompile(`^(\d+)/(\d+)(?: (\S+))?$`)
)

// Upgrade is one line of apk list --upgradable.
type Upgrade struct {
	Name    string `json:"name"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Arch    string `json:"arch"`
	Origin  string `json:"origin"`
	License string `json:"license"`
}

// SplitNameVersion splits "name-1.2.3-r0" into its name and version.
func SplitNameVersion(nv string) (name, version string, ok bool) {
	m := nameVersion.FindStringSubmatch(nv)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// ParseUpgradable parses apk list --upgradable, sorted by name. Lines it cannot read are returned in skipped.
func ParseUpgradable(out string) (ups []Upgrade, skipped []string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := upgradableLine.FindStringSubmatch(line)
		if m == nil {
			skipped = append(skipped, line)
			continue
		}
		name, newV, ok1 := SplitNameVersion(m[1])
		oldName, oldV, ok2 := SplitNameVersion(m[5])
		if !ok1 || !ok2 || name != oldName {
			skipped = append(skipped, line)
			continue
		}
		ups = append(ups, Upgrade{Name: name, Old: oldV, New: newV, Arch: m[2], Origin: m[3], License: m[4]})
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].Name < ups[j].Name })
	return ups, skipped
}

// Change is one planned (simulate) or done step of an apk transaction.
type Change struct {
	Verb string `json:"verb"`
	Name string `json:"name"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
}

// Simulation is the summary of apk upgrade -s -vv (or of a real run's output).
type Simulation struct {
	Changes    []Change `json:"changes"`
	Download   int64    `json:"download"`   // bytes to download
	Additional int64    `json:"additional"` // bytes of disk space the transaction adds; negative if it frees space
	SizesKnown bool     `json:"sizesKnown"`
	OK         bool     `json:"ok"`
	Errors     int      `json:"errors"`
}

// ParseSimulation reads the change lines, the size lines and the final OK or error line of apk's output.
func ParseSimulation(out string) Simulation {
	s := Simulation{Changes: []Change{}}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if m := changeLine.FindStringSubmatch(line); m != nil {
			c := Change{Verb: m[3], Name: m[4]}
			switch c.Verb {
			case "Installing", "Reinstalling":
				c.New = m[5]
			case "Purging", "Updating pinning":
				c.Old = m[5]
			default:
				c.Old, c.New = m[5], m[6]
			}
			s.Changes = append(s.Changes, c)
			continue
		}
		if m := downloadLine.FindStringSubmatch(line); m != nil {
			s.Download, s.SizesKnown = size(m[1], m[2]), true
			continue
		}
		if m := afterLine.FindStringSubmatch(line); m != nil {
			s.Additional, s.SizesKnown = size(m[1], m[2]), true
			if m[3] == "freed" {
				s.Additional = -s.Additional
			}
			continue
		}
		if strings.HasPrefix(line, "OK: ") {
			s.OK = true
			continue
		}
		if m := errorsLine.FindStringSubmatch(line); m != nil {
			s.Errors, _ = strconv.Atoi(m[1])
		}
	}
	return s
}

// Removed lists the packages the transaction purges.
func (s Simulation) Removed() []string { return s.names("Purging") }

// Downgraded lists the packages the transaction downgrades.
func (s Simulation) Downgraded() []string { return s.names("Downgrading") }

func (s Simulation) names(verb string) []string {
	var out []string
	for _, c := range s.Changes {
		if c.Verb == verb {
			out = append(out, c.Name)
		}
	}
	return out
}

// stepLine matches the start of any step line of apk: the counter (padded to the width of the total: "(  1/400)") and
// the verb that follows it.
var stepLine = regexp.MustCompile(`^\(\s*\d+/\d+\) (\S+) `)

// HasChanges reports whether apk's output shows a step that changed the system: every step line whose verb is not
// Downloading, whatever the verb is (a verb this parser does not know must not read as "nothing changed").
func HasChanges(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if m := stepLine.FindStringSubmatch(strings.TrimSpace(strings.TrimRight(line, "\r"))); m != nil && m[1] != "Downloading" {
			return true
		}
	}
	return false
}

var units = map[string]float64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}

func size(num, unit string) int64 {
	f, _ := strconv.ParseFloat(num, 64)
	return int64(f * units[unit])
}

// Progress is one line of apk --progress-fd: cur of max, in a stage (download, install; empty on apk 2).
type Progress struct {
	Cur   int64  `json:"cur"`
	Max   int64  `json:"max"`
	Stage string `json:"stage,omitempty"`
}

// LastProgress returns the last progress line of data.
func LastProgress(data []byte) (Progress, bool) {
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if m := progressLine.FindSubmatch(bytes.TrimSpace(lines[i])); m != nil {
			cur, _ := strconv.ParseInt(string(m[1]), 10, 64)
			max, _ := strconv.ParseInt(string(m[2]), 10, 64)
			return Progress{Cur: cur, Max: max, Stage: string(m[3])}, true
		}
	}
	return Progress{}, false
}

// Outcome classifies an apk exit status.
type Outcome int

const (
	OK Outcome = iota
	Partial
	LockTimeout
	Failed
)

// Classify maps apk's exit status: 0 is OK, 1–98 the number of errors (Partial), 99 an internal error – a lock
// timeout if the output says so – and anything else (killed, not started) Failed.
func Classify(exit int, output string) (Outcome, int) {
	switch {
	case exit == 0:
		return OK, 0
	case exit >= 1 && exit <= 98:
		return Partial, exit
	case exit == 99 && strings.Contains(output, "Unable to lock database"):
		return LockTimeout, 0
	}
	return Failed, 0
}
