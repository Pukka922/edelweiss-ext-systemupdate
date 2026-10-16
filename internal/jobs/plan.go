package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/releases"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/repos"
)

// Plan is the result of a preview; a job must cite a fresh one.
type Plan struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Target     string         `json:"target,omitempty"`
	Created    time.Time      `json:"created"`
	ReposSum   string         `json:"reposSum,omitempty"`
	Simulation apk.Simulation `json:"simulation"`
	Repos      *repos.Plan    `json:"repos,omitempty"`
	Held       []string       `json:"held"`     // listed as upgradable, but the simulation does not change them
	Messages   []string       `json:"messages"` // apk's lines about repositories it could not use
	Warnings   []string       `json:"warnings"`
	Blockers   []string       `json:"blockers"`
	LockHolder *host.Process  `json:"lockHolder,omitempty"`
}

const (
	planTTL  = 15 * time.Minute
	planSkew = time.Minute // how far a plan may be dated ahead of the clock
)

var planIDRe = regexp.MustCompile(`^[a-z0-9]{1,32}$`)

func (e Env) savePlan(p *Plan) error {
	dir := filepath.Join(e.Base, "plans")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	e.prunePlans(dir, p.ID)
	return WriteJSON(filepath.Join(dir, p.ID+".json"), p, 0o600)
}

// prunePlans removes the plans past their time to live, except keep: no job can use them any more, and the previews
// would pile them up. It is best effort.
func (e Env) prunePlans(dir, keep string) {
	entries, _ := os.ReadDir(dir)
	for _, ent := range entries {
		id, ok := strings.CutSuffix(ent.Name(), ".json")
		if !ok || id == keep || !planIDRe.MatchString(id) {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		var p Plan
		if readJSON(path, &p) == nil && e.Now().Sub(p.Created) > planTTL {
			_ = os.Remove(path)
		}
	}
}

// LoadPlan reads plan id; it fails for an invalid or unknown id, for a plan older than 15 minutes, and for one dated
// in the future (more than a minute of clock skew).
func (e Env) LoadPlan(id string) (*Plan, error) {
	if !planIDRe.MatchString(id) {
		return nil, errors.New("plan: invalid id")
	}
	var p Plan
	if err := readJSON(filepath.Join(e.Base, "plans", id+".json"), &p); err != nil {
		return nil, err
	}
	if age := e.Now().Sub(p.Created); age > planTTL || age < -planSkew {
		return nil, errors.New("plan: expired")
	}
	return &p, nil
}

func newPlan(e Env, kind, target string) *Plan {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return &Plan{ID: hex.EncodeToString(b[:]), Kind: kind, Target: target, Created: e.Now(),
		Held: []string{}, Messages: []string{}, Warnings: []string{}, Blockers: []string{}}
}

// block and warn add a code once.
func (p *Plan) block(code string) {
	if !slices.Contains(p.Blockers, code) {
		p.Blockers = append(p.Blockers, code)
	}
}

func (p *Plan) warn(code string) {
	if !slices.Contains(p.Warnings, code) {
		p.Warnings = append(p.Warnings, code)
	}
}

// busy blocks the plan because another apk holds the database lock, and names it (without credentials, bounded: the
// plan is for every user of the page).
func (e Env) busy(p *Plan) {
	p.block("apk_busy")
	h, _ := e.Host.LockHolder()
	p.LockHolder = safeHolder(h)
}

// simulate runs apk's simulation with args and puts the result into the plan.
func (e Env) simulate(ctx context.Context, p *Plan, args ...string) {
	out, exit, err := e.Runner.Run(ctx, args...)
	p.Simulation = apk.ParseSimulation(out)
	switch o, _ := apk.Classify(exit, out); {
	case err != nil:
		p.block("simulation_failed")
	case o == apk.LockTimeout:
		e.busy(p)
	case o != apk.OK:
		p.block("simulation_failed")
	}
	if len(p.Simulation.Removed()) > 0 {
		p.warn("removals")
	}
	if len(p.Simulation.Downgraded()) > 0 {
		p.warn("downgrades")
	}
}

// space blocks the plan when / cannot hold the download + the additional space + 10 % + 100 MiB.
func (e Env) space(p *Plan) {
	need := p.Simulation.Download
	if p.Simulation.Additional > 0 {
		need += p.Simulation.Additional
	}
	need += need/10 + 100<<20
	if _, free, err := e.Statfs(e.Root); err == nil && int64(free) < need {
		p.block("no_space_root")
	}
}

// bootSpace blocks the plan when a mounted /boot cannot hold twice its current kernels and initramfs files.
func (e Env) bootSpace(p *Plan) {
	if b, err := e.Host.Boot(); err != nil || !b.Mounted {
		return
	}
	var need int64
	for _, pattern := range []string{"boot/vmlinuz-*", "boot/initramfs-*"} {
		matches, _ := filepath.Glob(filepath.Join(e.Root, pattern))
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil {
				need += 2 * fi.Size()
			}
		}
	}
	if _, free, err := e.Statfs(filepath.Join(e.Root, "boot")); err == nil && int64(free) < need {
		p.block("no_space_boot")
	}
}

// bootMount blocks the plan when /boot is listed in /etc/fstab, or the host is a Raspberry Pi installed to disk (whose
// /boot holds its boot files), but is not mounted or is mounted read-only: a kernel or boot loader package would write
// to the wrong place or fail halfway. A diskless or data-mode Pi boots from a medium of its own, which an upgrade does
// not write to: the Pi alone is no reason there.
func (e Env) bootMount(p *Plan) {
	mode, merr := e.Host.Mode()
	pi := merr == nil && mode == host.Sys && e.Host.RaspberryPi()
	if b, err := e.Host.Boot(); err == nil && (b.InFstab || pi) {
		switch {
		case !b.Mounted:
			p.block("boot_not_mounted")
		case b.ReadOnly:
			p.block("boot_read_only")
		}
	}
}

// PreviewUpgrade simulates "Install all". The repositories have to name the installed branch (or follow latest-stable):
// apk upgrades to whatever branch they name, and "Install all" must not carry a host across branches without the checks
// of a release upgrade; that is blocked, and nothing is simulated. When the simulation changes a kernel or a boot loader,
// /boot gets the checks of a release upgrade: mounted and writable where the host needs it, and room for the new files.
func (e Env) PreviewUpgrade(ctx context.Context) (*Plan, error) {
	p := newPlan(e, "upgrade", "")
	fs, err := repos.Read(e.Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	release, _ := e.Host.Release()
	if br := repos.Branches(fs.Main.Content); len(br) > 0 && (len(br) != 1 || br[0] != releases.BranchOf(release)) {
		p.block("repos_inconsistent")
		return p, e.savePlan(p)
	}
	e.simulate(ctx, p, "--no-interactive", "--wait", "10", "upgrade", "-s", "-vv")
	e.space(p)
	if changesPackage(p.Simulation.Changes, bootChainPackage) {
		e.bootMount(p)
		e.bootSpace(p)
	}
	if ups, _, err := e.CachedUpgradable(); err == nil && p.Simulation.OK {
		changed := map[string]bool{}
		for _, c := range p.Simulation.Changes {
			changed[c.Name] = true
		}
		for _, u := range ups {
			if !changed[u.Name] {
				p.Held = append(p.Held, u.Name)
			}
		}
	}
	return p, e.savePlan(p)
}

var unavailable = regexp.MustCompile(`\b[1-9]\d* unavailable\b`)

// updateFailures returns the lines of apk update's output that name a repository apk could not use (UNTRUSTED,
// unreachable), and the summary line when it counts unavailable ones: apk may exit 0 nevertheless.
func updateFailures(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "ERROR:"),
			strings.HasPrefix(l, "WARNING:") && (strings.Contains(l, "UNTRUSTED") || strings.Contains(l, "error") || strings.Contains(l, "unavailable")),
			unavailable.MatchString(l):
			lines = append(lines, Redact(l))
		}
	}
	return lines
}

// hasLatestStable reports whether content has an untagged repository line that follows latest-stable.
func hasLatestStable(content string) bool {
	for _, raw := range strings.Split(content, "\n") {
		if l := repos.ParseLine(strings.TrimRight(raw, "\r")); l.Kind == repos.Repo && l.Tag == "" && l.Branch == "latest-stable" {
			return true
		}
	}
	return false
}

// extraLatestStable reports whether a .d file has an untagged latest-stable line. ExtraMappable ignores such lines (they
// move by themselves), but after a switch to edge one would stay on the stable branch and leave the host mixed.
func extraLatestStable(fs repos.Files) bool {
	for _, f := range fs.Extra {
		if hasLatestStable(f.Content) {
			return true
		}
	}
	return false
}

// PreviewRelease runs the pre-flight checks of a release upgrade to target (P1) and simulates it with a temporary
// repository file (P2–P4); nothing on the host changes except apk's index cache. The plan (P5) is stored either way:
// the page shows its blockers.
func (e Env) PreviewRelease(ctx context.Context, target string) (*Plan, error) {
	p := newPlan(e, "release", target)
	if !repos.ValidTarget(target) || e.allowed(target) != nil {
		p.block("target_not_allowed")
	}
	if mode, err := e.Host.Mode(); err != nil || mode != host.Sys {
		p.block("mode_not_sys")
	}
	release, _ := e.Host.Release()
	current := releases.BranchOf(release)
	if target == "edge" {
		p.warn("edge")
	}
	if sep, _ := e.Host.SeparateUsr(); sep && atLeast(target, "v3.24") {
		p.block("usr_separate")
	}
	e.bootMount(p)
	if installed, _ := e.Host.GRUB(); installed && atLeast(target, "v3.24") {
		p.warn("grub_install")
	}
	if atLeast(target, "v3.24") && !atLeast(current, "v3.24") {
		p.warn("notes_3_24")
	}
	fs, err := repos.Read(e.Root)
	if err != nil {
		return nil, err
	}
	p.ReposSum = fs.Sum()
	rp, err := repos.Rewrite(fs.Main.Content, target)
	if err != nil {
		p.block("target_not_allowed")
		return p, e.savePlan(p)
	}
	p.Repos = &rp
	switch br := repos.Branches(fs.Main.Content); {
	case rp.Changed == 0 && rp.LatestStable == 0:
		p.block("repos_inconsistent") // no line that could follow the upgrade
	case len(br) == 0: // latest-stable lines only: they move by themselves
	case len(br) != 1 || br[0] != current:
		p.block("repos_inconsistent")
	}
	if len(fs.ExtraMappable()) > 0 || (target == "edge" && extraLatestStable(fs)) {
		p.block("extra_repos_mappable")
	}
	if target != "edge" && (hasLatestStable(fs.Main.Content) || extraLatestStable(fs)) {
		// latest-stable lines move to the newest stable branch, which lies beyond the target when the host is two
		// branches behind: the job would end on a release other than the one confirmed
		if ix, _, err := e.CachedReleases(); err == nil && ix.LatestStable != target {
			p.block("latest_stable_ahead")
		}
	}
	if len(rp.Unmapped) > 0 {
		p.warn("unmapped_repos")
	}
	if rp.LatestStable > 0 {
		p.warn("latest_stable")
	}
	if len(p.Blockers) > 0 {
		return p, e.savePlan(p)
	}
	if err := os.MkdirAll(e.RunDir, 0o700); err != nil {
		return nil, err
	}
	// a file of this preview's own (the plan's id is hex): previews may overlap, and none may read another's
	sim := filepath.Join(e.RunDir, "sim-"+p.ID+".repos")
	if err := WriteFile(sim, []byte(fs.Sim(rp.Content)), 0o600); err != nil {
		return nil, err
	}
	defer os.Remove(sim)
	out, exit, err := e.Runner.Run(ctx, "--repositories-file", sim, "--no-interactive", "--wait", "10", "update")
	if o, _ := apk.Classify(exit, out); err == nil && o == apk.LockTimeout {
		e.busy(p)
		return p, e.savePlan(p)
	}
	if fails := updateFailures(out); err != nil || exit != 0 || len(fails) > 0 {
		p.block("repos_unavailable")
		p.Messages = append(p.Messages, fails...)
		return p, e.savePlan(p)
	}
	e.simulate(ctx, p, "--repositories-file", sim, "--no-interactive", "--wait", "10", "upgrade", "-a", "-s", "-vv",
		"--no-self-upgrade")
	e.space(p)
	e.bootSpace(p)
	return p, e.savePlan(p)
}
