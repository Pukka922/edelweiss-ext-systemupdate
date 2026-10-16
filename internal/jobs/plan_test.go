package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
)

type answer struct {
	out  string
	exit int
}

// scripted answers apk calls by the suffix of their joined arguments; others get "", 0.
type scripted map[string]answer

func (s scripted) Run(_ context.Context, args ...string) (string, int, error) {
	key := strings.Join(args, " ")
	for suffix, a := range s {
		if strings.HasSuffix(key, suffix) {
			return a.out, a.exit, nil
		}
	}
	return "", 0, nil
}

// simFile is what a call of apk with --repositories-file saw: the file, its content and its mode at that moment.
type simFile struct {
	Path, Content string
	Mode          os.FileMode
}

// recorded wraps a runner and keeps what the --repositories-file of each call held while the call ran (the preview
// removes the file afterwards).
type recorded struct {
	apk.Runner
	files []simFile
}

func (r *recorded) Run(ctx context.Context, args ...string) (string, int, error) {
	for i, a := range args {
		if a == "--repositories-file" && i+1 < len(args) {
			f := simFile{Path: args[i+1]}
			if data, err := os.ReadFile(f.Path); err == nil {
				f.Content = string(data)
			}
			if fi, err := os.Stat(f.Path); err == nil {
				f.Mode = fi.Mode().Perm()
			}
			r.files = append(r.files, f)
		}
	}
	return r.Runner.Run(ctx, args...)
}

func planEnv(t *testing.T, files map[string]string, run apk.Runner) Env {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, sys323)
	writeTree(t, root, files)
	e := offlineEnv()
	e.Root, e.Base, e.RunDir, e.Host, e.Runner = root, t.TempDir(), t.TempDir(), host.FS{Root: root}, run
	e.Statfs = func(string) (uint64, uint64, error) { return 100 << 30, 50 << 30, nil }
	e.Now = func() time.Time { return testNow }
	writeTree(t, e.Base, map[string]string{"cache/releases.json": releasesJSON})
	return e
}

const simRelease = "(1/2) Upgrading alpine-release (3.23.6-r0 -> 3.24.2-r0)\n(2/2) Purging gtk+2.0 (2.24.33-r9)\n" +
	"Need to download 386 MiB of packages.\nAfter this operation, 41 MiB of additional disk space will be used.\nOK: 1 GiB in 400 packages\n"

func TestPreviewRelease(t *testing.T) {
	run := &recorded{Runner: scripted{"upgrade -a -s -vv --no-self-upgrade": {out: simRelease}}}
	e := planEnv(t, nil, run)
	p, err := e.PreviewRelease(context.Background(), "v3.24")
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if p.Simulation.Download != 386<<20 || len(p.Simulation.Removed()) != 1 || !contains(p.Warnings, "removals") {
		t.Fatalf("plan = %+v", p)
	}
	if !contains(p.Warnings, "notes_3_24") || p.Repos == nil || p.Repos.Changed != 2 {
		t.Fatalf("plan = %+v", p)
	}
	if got, err := e.LoadPlan(p.ID); err != nil || got.Target != "v3.24" || got.ReposSum != p.ReposSum {
		t.Fatalf("LoadPlan = %+v, %v", got, err)
	}
	// the simulation file is the preview's own (named by its plan), 0600, and gone when the preview is over
	if len(run.files) != 2 { // the update, then the simulation
		t.Fatalf("calls with a repositories file: %+v", run.files)
	}
	for _, sim := range run.files {
		if want := filepath.Join(e.RunDir, "sim-"+p.ID+".repos"); sim.Path != want {
			t.Fatalf("sim file %s, want %s", sim.Path, want)
		}
		if !strings.Contains(sim.Content, "/v3.24/main") || sim.Mode != 0o600 {
			t.Fatalf("sim file: %+v", sim)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(e.RunDir, "sim*.repos")); len(left) != 0 {
		t.Fatalf("the preview left %v", left)
	}
	if fi, _ := os.Stat(filepath.Join(e.Base, "plans", p.ID+".json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("plan file mode %v", fi.Mode())
	}
	if b, _ := os.ReadFile(filepath.Join(e.Root, "etc/apk/repositories")); string(b) != repos323 {
		t.Fatal("a preview must not change the repositories")
	}
}

func TestPreviewReleaseToEdge(t *testing.T) {
	e := planEnv(t, map[string]string{"lib/apk/db/installed": "P:grub-efi\nV:2.12-r0\n"}, scripted{})
	p, _ := e.PreviewRelease(context.Background(), "edge")
	if len(p.Blockers) != 0 || !contains(p.Warnings, "edge") || !contains(p.Warnings, "grub_install") || !contains(p.Warnings, "notes_3_24") {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPreviewReleaseBlockers(t *testing.T) {
	cases := map[string]struct {
		files  map[string]string
		target string
		want   string
	}{
		"diskless":    {map[string]string{"proc/mounts": "tmpfs / tmpfs rw 0 0\n"}, "v3.24", "mode_not_sys"},
		"not offered": {nil, "v3.25", "target_not_allowed"},
		"bad target":  {nil, "v3.24/../x", "target_not_allowed"},
		"usr":         {map[string]string{"proc/mounts": "/dev/sda2 / ext4 rw 0 0\n/dev/sda3 /usr ext4 rw 0 0\n"}, "v3.24", "usr_separate"},
		"boot ro":     {map[string]string{"etc/fstab": "LABEL=b /boot vfat defaults 0 2\n", "proc/mounts": "/dev/sda2 / ext4 rw 0 0\n/dev/sda1 /boot vfat ro 0 0\n"}, "v3.24", "boot_read_only"},
		"boot gone":   {map[string]string{"etc/fstab": "LABEL=b /boot vfat defaults 0 2\n"}, "v3.24", "boot_not_mounted"},
		"mixed":       {map[string]string{"etc/apk/repositories": "https://x/alpine/v3.22/main\nhttps://x/alpine/v3.23/community\n"}, "v3.24", "repos_inconsistent"},
		"ahead":       {map[string]string{"etc/apk/repositories": "https://x/alpine/v3.24/main\n"}, "v3.24", "repos_inconsistent"},
		"nothing":     {map[string]string{"etc/apk/repositories": "/media/usb/apks\n"}, "v3.24", "repos_inconsistent"},
		"extra .d":    {map[string]string{"etc/apk/repositories.d/x.list": "https://dl-cdn.alpinelinux.org/alpine/v3.23/testing\n"}, "v3.24", "extra_repos_mappable"},
		// ExtraMappable ignores latest-stable lines, but after a switch to edge such a line would leave the host mixed.
		"extra .d latest-stable to edge": {map[string]string{"etc/apk/repositories.d/x.list": "https://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"}, "edge", "extra_repos_mappable"},
	}
	for name, c := range cases {
		e := planEnv(t, c.files, scripted{})
		p, err := e.PreviewRelease(context.Background(), c.target)
		if err != nil || !contains(p.Blockers, c.want) {
			t.Errorf("%s: plan = %+v, %v", name, p, err)
		}
	}
}

func TestPreviewReleaseExtraLatestStable(t *testing.T) {
	files := map[string]string{"etc/apk/repositories.d/x.list": "https://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"}
	// to a stable branch the line moves by itself
	e := planEnv(t, files, scripted{})
	if p, err := e.PreviewRelease(context.Background(), "v3.24"); err != nil || contains(p.Blockers, "extra_repos_mappable") {
		t.Fatalf("a latest-stable .d line before a stable target: %+v, %v", p, err)
	}
	// a tagged latest-stable line is a pin, not part of the system's branch
	files = map[string]string{"lib/apk/repositories.d/x.list": "@pin https://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"}
	e = planEnv(t, files, scripted{})
	if p, err := e.PreviewRelease(context.Background(), "edge"); err != nil || contains(p.Blockers, "extra_repos_mappable") {
		t.Fatalf("a tagged latest-stable .d line before edge: %+v, %v", p, err)
	}
}

func TestPreviewReleaseUnavailableRepository(t *testing.T) {
	e := planEnv(t, nil, scripted{"update": {out: "WARNING: updating and opening https://dl-cdn.alpinelinux.org/alpine/v3.24/main: UNTRUSTED signature\n1 unavailable, 0 stale; 9000 distinct packages available\n"}})
	p, _ := e.PreviewRelease(context.Background(), "v3.24")
	if !contains(p.Blockers, "repos_unavailable") || len(p.Messages) != 2 || !strings.Contains(p.Messages[0], "UNTRUSTED") {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPreviewUpgrade(t *testing.T) {
	e := planEnv(t, nil, scripted{"upgrade -s -vv": {out: "(1/1) Upgrading curl (8.16.0-r0 -> 8.16.0-r1)\nNeed to download 1 MiB of packages.\nAfter this operation, 0 B of additional disk space will be used.\nOK: 1 GiB in 1 packages\n"}})
	writeTree(t, e.Base, map[string]string{"cache/upgradable.txt": "curl-8.16.0-r1 x86_64 {curl} (curl) [upgradable from: curl-8.16.0-r0]\n" +
		"foo-2-r0 x86_64 {foo} (MIT) [upgradable from: foo-1-r0]\n"})
	p, err := e.PreviewUpgrade(context.Background())
	if err != nil || len(p.Blockers) != 0 || len(p.Simulation.Changes) != 1 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if len(p.Held) != 1 || p.Held[0] != "foo" {
		t.Fatalf("held = %+v", p.Held)
	}
}

func TestPreviewUpgradeSpace(t *testing.T) {
	e := planEnv(t, nil, scripted{"upgrade -s -vv": {out: "(1/1) Upgrading x (1-r0 -> 2-r0)\nNeed to download 900 MiB of packages.\nAfter this operation, 200 MiB of additional disk space will be used.\nOK: 1 GiB in 1 packages\n"}})
	e.Statfs = func(string) (uint64, uint64, error) { return 10 << 30, 1 << 30, nil } // 1 GiB < 900 + 200 + 10 % + 100 MiB
	if p, _ := e.PreviewUpgrade(context.Background()); !contains(p.Blockers, "no_space_root") {
		t.Fatalf("plan = %+v", p)
	}
	e.Statfs = func(string) (uint64, uint64, error) { return 10 << 30, 2 << 30, nil }
	if p, _ := e.PreviewUpgrade(context.Background()); contains(p.Blockers, "no_space_root") {
		t.Fatalf("2 GiB are enough: %+v", p)
	}
}

// counting keeps the apk calls it passes on.
type counting struct {
	apk.Runner
	calls []string
}

func (c *counting) Run(ctx context.Context, args ...string) (string, int, error) {
	c.calls = append(c.calls, strings.Join(args, " "))
	return c.Runner.Run(ctx, args...)
}

// "Install all" must not carry a host across branches: apk would upgrade to whatever branch the repositories name, and
// none of the release checks (EOL, the next branch only, the backup) would have been made.
func TestPreviewUpgradeNeedsRepositoriesOfTheInstalledBranch(t *testing.T) {
	const ls = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"
	for name, c := range map[string]struct {
		repos   string
		blocked bool
	}{
		"matching":                   {repos323, false},
		"ahead":                      {"https://dl-cdn.alpinelinux.org/alpine/v3.26/main\nhttps://dl-cdn.alpinelinux.org/alpine/v3.26/community\n", true},
		"behind":                     {"https://dl-cdn.alpinelinux.org/alpine/v3.22/main\n", true},
		"edge on a stable host":      {"https://dl-cdn.alpinelinux.org/alpine/edge/main\n", true},
		"mixed":                      {"https://x/alpine/v3.22/main\nhttps://x/alpine/v3.23/community\n", true},
		"matching and one ahead":     {repos323 + "https://x/alpine/v3.24/community\n", true},
		"latest-stable only":         {reposLatestStable, false},
		"latest-stable and matching": {"https://dl-cdn.alpinelinux.org/alpine/v3.23/main\n" + ls, false},
		"tagged pins are no branch":  {repos323 + "@edge https://dl-cdn.alpinelinux.org/alpine/edge/testing\n", false},
		"local path only":            {"/media/usb/apks\n", false},
		"matching, CRLF":             {strings.ReplaceAll(repos323, "\n", "\r\n"), false},
		"matching, no final newline": {strings.TrimSuffix(repos323, "\n"), false},
	} {
		run := &counting{Runner: scripted{"upgrade -s -vv": {out: simCurlUpgrade}}}
		e := planEnv(t, map[string]string{"etc/apk/repositories": c.repos}, run)
		p, err := e.PreviewUpgrade(context.Background())
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if contains(p.Blockers, "repos_inconsistent") != c.blocked {
			t.Errorf("%s: blockers %v, want repos_inconsistent = %v", name, p.Blockers, c.blocked)
		}
		if c.blocked && len(run.calls) != 0 {
			t.Errorf("%s: a blocked preview simulated: %v", name, run.calls)
		}
		if !c.blocked && len(run.calls) == 0 {
			t.Errorf("%s: the simulation did not run", name)
		}
		// the blocked plan is saved like any other, and a job can never cite it
		if got, err := e.LoadPlan(p.ID); err != nil || contains(got.Blockers, "repos_inconsistent") != c.blocked {
			t.Errorf("%s: LoadPlan = %+v, %v", name, got, err)
		}
	}
}

// an edge host names edge in its repositories and runs "3.25_alpha…": its own branch
func TestPreviewUpgradeOnEdge(t *testing.T) {
	files := map[string]string{"etc/alpine-release": "3.25_alpha20261001\n", "etc/apk/repositories": "https://dl-cdn.alpinelinux.org/alpine/edge/main\nhttps://dl-cdn.alpinelinux.org/alpine/edge/community\n"}
	e := planEnv(t, files, scripted{"upgrade -s -vv": {out: simCurlUpgrade}})
	if p, err := e.PreviewUpgrade(context.Background()); err != nil || len(p.Blockers) != 0 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
}

// a host without a main repositories file names no branch: apk's simulation says what it makes of that
func TestPreviewUpgradeWithoutRepositoriesFile(t *testing.T) {
	e := planEnv(t, nil, scripted{"upgrade -s -vv": {out: simCurlUpgrade}})
	if err := os.Remove(filepath.Join(e.Root, "etc/apk/repositories")); err != nil {
		t.Fatal(err)
	}
	if p, err := e.PreviewUpgrade(context.Background()); err != nil || contains(p.Blockers, "repos_inconsistent") {
		t.Fatalf("plan = %+v, %v", p, err)
	}
}

func TestPreviewApkBusy(t *testing.T) {
	const busy = "ERROR: Unable to lock database: Resource temporarily unavailable\n"
	e := planEnv(t, map[string]string{"lib/apk/db/lock": "", "proc/1234/cmdline": "apk\x00add\x00vim\x00"},
		scripted{"upgrade -s -vv": {out: busy, exit: 99}, "update": {out: busy, exit: 99}})
	var st syscall.Stat_t
	if err := syscall.Stat(filepath.Join(e.Root, "lib/apk/db/lock"), &st); err != nil {
		t.Fatal(err)
	}
	writeTree(t, e.Root, map[string]string{"proc/locks": fmt.Sprintf(
		"1: POSIX  ADVISORY  WRITE 99 08:01:5 0 EOF\n2: FLOCK  ADVISORY  WRITE 1234 08:01:%d 0 EOF\n", st.Ino)})
	p, _ := e.PreviewUpgrade(context.Background())
	if !contains(p.Blockers, "apk_busy") || p.LockHolder == nil || p.LockHolder.PID != 1234 || p.LockHolder.Name != "apk add vim" {
		t.Fatalf("an upgrade preview names who holds the lock: %+v (%+v)", p, p.LockHolder)
	}
	// the release preview does too, when its apk update runs into the lock
	p, _ = e.PreviewRelease(context.Background(), "v3.24")
	if !contains(p.Blockers, "apk_busy") || p.LockHolder == nil || p.LockHolder.PID != 1234 || p.LockHolder.Name != "apk add vim" {
		t.Fatalf("a release preview names who holds the lock: %+v (%+v)", p, p.LockHolder)
	}
}

// The plan goes to every user of the page: the name of the lock holder has no credentials and is not unbounded.
func TestPreviewApkBusyRedactsAndBoundsTheHolder(t *testing.T) {
	const busy = "ERROR: Unable to lock database: Resource temporarily unavailable\n"
	e := planEnv(t, map[string]string{
		"lib/apk/db/lock":   "",
		"proc/1234/cmdline": "apk\x00add\x00https://u:s3cret@repo.example.org/x\x00" + strings.Repeat("v", 500) + "\x00",
	}, scripted{"upgrade -s -vv": {out: busy, exit: 99}})
	var st syscall.Stat_t
	if err := syscall.Stat(filepath.Join(e.Root, "lib/apk/db/lock"), &st); err != nil {
		t.Fatal(err)
	}
	writeTree(t, e.Root, map[string]string{"proc/locks": fmt.Sprintf("1: FLOCK  ADVISORY  WRITE 1234 08:01:%d 0 EOF\n", st.Ino)})
	p, _ := e.PreviewUpgrade(context.Background())
	if p.LockHolder == nil {
		t.Fatalf("no lock holder: %+v", p)
	}
	if n := p.LockHolder.Name; strings.Contains(n, "s3cret") || !strings.HasPrefix(n, "apk add https://***@repo.example.org/x v") ||
		!strings.HasSuffix(n, "…") || len(n) > 200+len("…") {
		t.Fatalf("holder = %q (%d bytes)", n, len(n))
	}
	stored, err := e.LoadPlan(p.ID) // and so is what the plan file keeps
	if err != nil || stored.LockHolder == nil || strings.Contains(stored.LockHolder.Name, "s3cret") {
		t.Fatalf("stored plan = %+v, %v", stored, err)
	}
}

// releasesJSON325 is a list on which v3.25 is the newest stable branch: from v3.23 the next branch is still v3.24.
const releasesJSON325 = `{"latest_stable":"v3.25","release_branches":[
 {"rel_branch":"edge","repos":[{"name":"main"},{"name":"community"},{"name":"testing"}]},
 {"rel_branch":"v3.25","eol_date":"2029-06-01","repos":[{"name":"main"},{"name":"community"}],
  "releases":[{"version":"3.25.0","date":"2026-12-01"}]},
 {"rel_branch":"v3.24","eol_date":"2028-06-01","repos":[{"name":"main"},{"name":"community"}],
  "releases":[{"version":"3.24.2","date":"2026-09-17"}]},
 {"rel_branch":"v3.23","eol_date":"2027-11-01","repos":[{"name":"main"},{"name":"community"}],
  "releases":[{"version":"3.23.6","date":"2026-09-17"}]}]}`

func TestPreviewReleaseLatestStablePastTheTarget(t *testing.T) {
	// latest-stable lines move to the newest stable branch: past the target when the host is two branches behind
	const ls = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"
	cases := map[string]map[string]string{
		"main file only":    {"etc/apk/repositories": reposLatestStable},
		"main file mixed":   {"etc/apk/repositories": "https://dl-cdn.alpinelinux.org/alpine/v3.23/main\n" + ls},
		".d file":           {"etc/apk/repositories.d/x.list": ls},
		"lib .d file":       {"lib/apk/repositories.d/x.list": ls},
		"main file and .d":  {"etc/apk/repositories": reposLatestStable, "etc/apk/repositories.d/x.list": ls},
		"main file CRLF":    {"etc/apk/repositories": strings.ReplaceAll(reposLatestStable, "\n", "\r\n")},
		"main file, no \\n": {"etc/apk/repositories": strings.TrimSuffix(reposLatestStable, "\n")},
	}
	for name, files := range cases {
		for _, list := range []struct {
			newest, json string
			blocked      bool
		}{{"v3.24", releasesJSON, false}, {"v3.25", releasesJSON325, true}} {
			e := planEnv(t, files, scripted{})
			writeTree(t, e.Base, map[string]string{"cache/releases.json": list.json})
			p, err := e.PreviewRelease(context.Background(), "v3.24")
			// its own blocker: the repositories are consistent, but the lines would carry the upgrade past the target
			if err != nil || contains(p.Blockers, "latest_stable_ahead") != list.blocked || contains(p.Blockers, "repos_inconsistent") {
				t.Errorf("%s, newest stable branch %s: want latest_stable_ahead = %v and no repos_inconsistent: plan = %+v, %v", name, list.newest, list.blocked, p, err)
			}
		}
	}
}

func TestPreviewReleaseLatestStableToEdge(t *testing.T) {
	// to edge the main file's latest-stable lines are rewritten: it does not matter which branch is the newest
	e := planEnv(t, map[string]string{"etc/apk/repositories": reposLatestStable}, scripted{})
	writeTree(t, e.Base, map[string]string{"cache/releases.json": releasesJSON325})
	if p, err := e.PreviewRelease(context.Background(), "edge"); err != nil || len(p.Blockers) != 0 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
}

func TestPreviewSimulationFails(t *testing.T) {
	e := planEnv(t, nil, scripted{"upgrade -s -vv": {out: "ERROR: unable to select packages:\n  foo (no such package)\n", exit: 1}})
	if p, _ := e.PreviewUpgrade(context.Background()); !contains(p.Blockers, "simulation_failed") {
		t.Fatalf("plan = %+v", p)
	}
}

func TestLoadPlanExpires(t *testing.T) {
	e := planEnv(t, nil, scripted{})
	_ = e.savePlan(&Plan{ID: "old", Kind: "upgrade", Created: testNow.Add(-16 * time.Minute)})
	if _, err := e.LoadPlan("old"); err == nil {
		t.Fatal("an expired plan loaded")
	}
	for _, bad := range []string{"../x", "", "A"} {
		if _, err := e.LoadPlan(bad); err == nil {
			t.Fatalf("the bad id %q loaded", bad)
		}
	}
}

func TestLoadPlanRejectsAPlanFromTheFuture(t *testing.T) {
	e := planEnv(t, nil, scripted{})
	_ = e.savePlan(&Plan{ID: "skewed", Kind: "upgrade", Created: testNow.Add(30 * time.Second)})
	_ = e.savePlan(&Plan{ID: "future", Kind: "upgrade", Created: testNow.Add(2 * time.Minute)})
	if _, err := e.LoadPlan("skewed"); err != nil {
		t.Fatalf("a little clock skew is fine: %v", err)
	}
	if _, err := e.LoadPlan("future"); err == nil {
		t.Fatal("a plan dated in the future loaded")
	}
}

func TestSavePlanRemovesExpiredPlans(t *testing.T) {
	e := planEnv(t, nil, scripted{})
	dir := filepath.Join(e.Base, "plans")
	_ = e.savePlan(&Plan{ID: "recent", Kind: "upgrade", Created: testNow.Add(-14 * time.Minute)})
	_ = e.savePlan(&Plan{ID: "stale", Kind: "upgrade", Created: testNow.Add(-16 * time.Minute)})
	writeTree(t, dir, map[string]string{"notes.txt": "not a plan"})
	if err := e.savePlan(&Plan{ID: "fresh", Kind: "upgrade", Created: testNow}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"stale": false, "recent": true, "fresh": true} {
		if _, err := os.Stat(filepath.Join(dir, id+".json")); (err == nil) != want {
			t.Errorf("plan %s present = %v, want %v", id, err == nil, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("only plans are removed: %v", err)
	}
}

func TestPreviewsDoNotPileUpPlans(t *testing.T) {
	e := planEnv(t, nil, scripted{})
	now := testNow
	e.Now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if _, err := e.PreviewUpgrade(context.Background()); err != nil {
			t.Fatal(err)
		}
		now = now.Add(10 * time.Minute)
	}
	entries, _ := os.ReadDir(filepath.Join(e.Base, "plans"))
	if len(entries) > 2 { // the previews of the last 15 minutes
		t.Fatalf("%d plan files", len(entries))
	}
}

func TestPreviewReleaseSimFileIsPerPreview(t *testing.T) {
	// two previews (in the page, two admins at once) must not write into one file: each has its own, named by its
	// plan, which the preview removes whatever way it ends
	run := &recorded{Runner: scripted{"update": {out: "ERROR: https://dl-cdn.alpinelinux.org/alpine/edge/main: temporary error\n", exit: 1}}}
	e := planEnv(t, nil, run)
	first, err := e.PreviewRelease(context.Background(), "v3.24") // ends at the update: repos_unavailable
	if err != nil || !contains(first.Blockers, "repos_unavailable") {
		t.Fatalf("plan = %+v, %v", first, err)
	}
	run.Runner = scripted{}
	second, err := e.PreviewRelease(context.Background(), "edge") // runs the simulation too
	if err != nil || len(second.Blockers) != 0 {
		t.Fatalf("plan = %+v, %v", second, err)
	}
	if first.ID == second.ID || len(run.files) != 3 {
		t.Fatalf("plans %s, %s; calls %+v", first.ID, second.ID, run.files)
	}
	for i, c := range []struct{ id, branch string }{{first.ID, "/v3.24/"}, {second.ID, "/edge/"}, {second.ID, "/edge/"}} {
		sim := run.files[i]
		if sim.Path != filepath.Join(e.RunDir, "sim-"+c.id+".repos") || !strings.Contains(sim.Content, c.branch+"main") || sim.Mode != 0o600 {
			t.Errorf("call %d: %+v, want the file of plan %s with %s", i, sim, c.id, c.branch)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(e.RunDir, "sim*.repos")); len(left) != 0 {
		t.Fatalf("previews left %v", left)
	}
}

const (
	simKernelUpgrade = "(1/2) Upgrading linux-lts (6.12.1-r0 -> 6.12.2-r0)\n(2/2) Upgrading curl (8.16.0-r0 -> 8.16.0-r1)\n" +
		"Need to download 1 MiB of packages.\nAfter this operation, 0 B of additional disk space will be used.\nOK: 1 GiB in 400 packages\n"
	simCurlUpgrade = "(1/1) Upgrading curl (8.16.0-r0 -> 8.16.0-r1)\n" +
		"Need to download 1 MiB of packages.\nAfter this operation, 0 B of additional disk space will be used.\nOK: 1 GiB in 400 packages\n"
	simFirmwareUpgrade = "(1/1) Upgrading linux-firmware-intel (1-r0 -> 2-r0)\nOK: 1 GiB in 400 packages\n"
)

func TestPreviewUpgradeChecksBootWhenTheBootChainChanges(t *testing.T) {
	bootFstab := map[string]string{"etc/fstab": "LABEL=b /boot vfat defaults 0 2\n"}
	merge := func(ms ...map[string]string) map[string]string {
		out := map[string]string{}
		for _, m := range ms {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}
	notMounted := bootFstab
	readOnly := merge(bootFstab, map[string]string{"proc/mounts": "/dev/sda2 / ext4 rw 0 0\n/dev/sda1 /boot vfat ro 0 0\n"})
	mountedRW := merge(bootFstab, map[string]string{"proc/mounts": "/dev/sda2 / ext4 rw 0 0\n/dev/sda1 /boot vfat rw 0 0\n",
		"boot/vmlinuz-lts": "0123456789", "boot/initramfs-lts": "0123456789"})
	for name, c := range map[string]struct {
		files map[string]string
		sim   string
		want  string // "" for no boot blocker
	}{
		"kernel, /boot not mounted": {notMounted, simKernelUpgrade, "boot_not_mounted"},
		"kernel, /boot read-only":   {readOnly, simKernelUpgrade, "boot_read_only"},
		"kernel, a Raspberry Pi":    {map[string]string{"proc/device-tree/model": "Raspberry Pi 4 Model B Rev 1.4\x00"}, simKernelUpgrade, "boot_not_mounted"},
		"kernel, /boot full":        {mountedRW, simKernelUpgrade, "no_space_boot"},
		// firmware is loaded at boot, but installing it writes nothing to /boot
		"firmware":                       {readOnly, simFirmwareUpgrade, ""},
		"firmware, /boot not mounted":    {notMounted, simFirmwareUpgrade, ""},
		"firmware, /boot full":           {mountedRW, simFirmwareUpgrade, ""},
		"firmware, a Raspberry Pi":       {map[string]string{"proc/device-tree/model": "Raspberry Pi 4 Model B Rev 1.4\x00"}, simFirmwareUpgrade, ""},
		"firmware and a kernel":          {readOnly, "(1/2) Upgrading linux-firmware-intel (1-r0 -> 2-r0)\n(2/2) Upgrading linux-lts (6.12.1-r0 -> 6.12.2-r0)\nOK: 1 GiB in 400 packages\n", "boot_read_only"},
		"boot loader":                    {readOnly, "(1/1) Upgrading grub-efi (2.12-r0 -> 2.12-r1)\nOK: 1 GiB in 400 packages\n", "boot_read_only"},
		"boot loader, /boot not mounted": {notMounted, "(1/1) Upgrading syslinux (6.04-r0 -> 6.04-r1)\nOK: 1 GiB in 400 packages\n", "boot_not_mounted"},
		// the Pi's /boot holds its boot files only where the host boots from it: a diskless or data-mode Pi boots from a
		// medium it does not write on an upgrade
		"kernel, a Raspberry Pi in diskless mode": {map[string]string{"proc/device-tree/model": "Raspberry Pi 4 Model B Rev 1.4\x00",
			"proc/mounts": "tmpfs / tmpfs rw 0 0\n/dev/loop0 /.modloop squashfs ro 0 0\n"}, simKernelUpgrade, ""},
		"kernel, a Raspberry Pi in data mode": {map[string]string{"proc/device-tree/model": "Raspberry Pi 4 Model B Rev 1.4\x00",
			"proc/mounts": "tmpfs / tmpfs rw 0 0\n/dev/sda2 /var ext4 rw 0 0\n"}, simKernelUpgrade, ""},
		"kernel, the Pi boot loader package in diskless mode": {map[string]string{"lib/apk/db/installed": "P:raspberrypi-bootloader\nV:1-r0\n",
			"proc/mounts": "tmpfs / tmpfs rw 0 0\n/dev/loop0 /.modloop squashfs ro 0 0\n"}, simKernelUpgrade, ""},
		"padded counters":                  {readOnly, "( 1/12) Upgrading linux-virt (6.12.1-r0 -> 6.12.2-r0)\nOK: 1 GiB in 400 packages\n", "boot_read_only"},
		"no kernel in the simulation":      {readOnly, simCurlUpgrade, ""},
		"no kernel, /boot full":            {mountedRW, simCurlUpgrade, ""},
		"kernel, /boot not in the fstab":   {nil, simKernelUpgrade, ""},
		"kernel, /boot there and writable": {merge(bootFstab, map[string]string{"proc/mounts": "/dev/sda2 / ext4 rw 0 0\n/dev/sda1 /boot vfat rw 0 0\n"}), simKernelUpgrade, ""},
	} {
		e := planEnv(t, c.files, scripted{"upgrade -s -vv": {out: c.sim}})
		e.Statfs = func(p string) (uint64, uint64, error) {
			if strings.HasSuffix(p, "/boot") {
				return 100 << 20, 10, nil // 10 bytes free: 2 x the 20 bytes of the kernel files do not fit
			}
			return 100 << 30, 50 << 30, nil
		}
		p, err := e.PreviewUpgrade(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		boot := []string{}
		for _, b := range p.Blockers {
			if strings.HasPrefix(b, "boot_") || b == "no_space_boot" {
				boot = append(boot, b)
			}
		}
		if (c.want == "") != (len(boot) == 0) || (c.want != "" && !contains(boot, c.want)) {
			t.Errorf("%s: boot blockers %v, want %q (all: %v)", name, boot, c.want, p.Blockers)
		}
	}
}

func TestPreviewReleaseBootSpace(t *testing.T) {
	// the room for twice the kernel files on /boot is checked by the release preview too
	files := map[string]string{"etc/fstab": "LABEL=b /boot vfat defaults 0 2\n",
		"proc/mounts":      "/dev/sda2 / ext4 rw 0 0\n/dev/sda1 /boot vfat rw 0 0\n",
		"boot/vmlinuz-lts": "0123456789", "boot/initramfs-lts": "0123456789"}
	e := planEnv(t, files, scripted{})
	free := uint64(10)
	e.Statfs = func(p string) (uint64, uint64, error) {
		if strings.HasSuffix(p, "/boot") {
			return 100 << 20, free, nil
		}
		return 100 << 30, 50 << 30, nil
	}
	if p, err := e.PreviewRelease(context.Background(), "v3.24"); err != nil || !contains(p.Blockers, "no_space_boot") {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	free = 40 // twice 20 bytes
	if p, err := e.PreviewRelease(context.Background(), "v3.24"); err != nil || contains(p.Blockers, "no_space_boot") {
		t.Fatalf("plan = %+v, %v", p, err)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
