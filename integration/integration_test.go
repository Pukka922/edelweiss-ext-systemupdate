//go:build integration

// Package integration runs the jobs of the extension against the real apk of a throwaway Alpine container, as root:
// scripts/test-alpine.sh starts one container per test. The tests change the system they run on, so they refuse to run
// anywhere else.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/fetch"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/releases"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/repos"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/secdb"
)

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestMain(m *testing.M) {
	// The tests change the system they run on, and so do the runner and the stand-in below: none of them starts
	// anywhere but in a container of Alpine, as root.
	if !exists("/.dockerenv") || !exists("/etc/alpine-release") || os.Geteuid() != 0 {
		fmt.Println("integration: skipped – these tests change the system; scripts/test-alpine.sh runs them in throwaway Alpine containers")
		os.Exit(0)
	}
	switch {
	case len(os.Args) == 3 && os.Args[1] == "--run-job":
		os.Exit(jobs.RunJob(runnerConfig(), os.Args[2]))
	case os.Getenv("INTEGRATION_PARENT") != "":
		// a stand-in for the extension: launch the runner, then wait to be killed with the process group
		if err := (jobs.ExecLauncher{Binary: os.Args[0]}).Launch(os.Getenv("INTEGRATION_PARENT")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	// after the switch: the runner's cleaned environment does not carry the variable
	if os.Getenv("EDELWEISS_ALPINE_TEST") != "1" {
		fmt.Println("integration: skipped – these tests change the system; scripts/test-alpine.sh runs them in throwaway Alpine containers")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// newEnv is the real host, except that metadata is never fetched: the tests bring a release list of their own.
func newEnv() jobs.Env {
	e := jobs.DefaultEnv()
	e.ReleasesURL = "http://127.0.0.1:1/releases.json"
	e.SecDBURL = func(branch, repo string) string { return "http://127.0.0.1:1/" + branch + "/" + repo + ".json" }
	return e
}

func runnerConfig() jobs.RunnerConfig {
	e := newEnv()
	return jobs.RunnerConfig{Base: e.Base, LockPath: jobs.DefaultLock, APK: apk.Path, Env: apk.Env, RequireRoot: true,
		Now: time.Now, Proc: host.New(), Kinds: jobs.Kinds(e)}
}

// releasesFixture supports every branch for long, so the tests depend neither on today's EOL dates nor on the network.
const releasesFixture = `{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"edge","repos":[{"name":"main"},{"name":"community"}]},
 {"rel_branch":"v3.24","eol_date":"2099-01-01","repos":[{"name":"main"},{"name":"community"}],"releases":[{"version":"3.24.0","date":"2026-05-01"}]},
 {"rel_branch":"v3.23","eol_date":"2099-01-01","repos":[{"name":"main"},{"name":"community"}],"releases":[{"version":"3.23.0","date":"2025-12-01"}]},
 {"rel_branch":"v3.22","eol_date":"2099-01-01","repos":[{"name":"main"},{"name":"community"}],"releases":[{"version":"3.22.0","date":"2025-05-30"}]}]}`

func prepare(t *testing.T) jobs.Env {
	t.Helper()
	e := newEnv()
	if err := os.MkdirAll(filepath.Join(e.Base, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.Base, "cache", "releases.json"), []byte(releasesFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return e
}

// run runs a job in this process, as the runner would. The log of a job that did not end well is logged when the test
// fails.
func run(t *testing.T, e jobs.Env, spec jobs.Spec) *jobs.State {
	t.Helper()
	s := jobs.Store{Base: e.Base, Proc: host.New()}
	spec.User = "myUser"
	id, dir, err := s.Create(spec, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if code := jobs.RunJob(runnerConfig(), dir); code != 0 {
		t.Fatalf("RunJob = %d", code)
	}
	st, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != jobs.Done {
		t.Cleanup(func() {
			if t.Failed() {
				log, _ := os.ReadFile(filepath.Join(dir, "log"))
				t.Logf("log of the %s job (%s):\n%s", spec.Kind, st.Status, log)
			}
		})
	}
	return st
}

func apkOut(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(apk.Path, args...)
	cmd.Env = apk.Env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("apk %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func branch(t *testing.T) string {
	t.Helper()
	return releases.BranchOf(strings.TrimSpace(read(t, "/etc/alpine-release")))
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestUpgradeOnAnOldPointRelease(t *testing.T) {
	e := prepare(t)
	if st := run(t, e, jobs.Spec{Kind: "refresh"}); st.Status != jobs.Done {
		t.Fatalf("refresh: %+v", st)
	}
	ups, _, err := e.CachedUpgradable()
	if err != nil || len(ups) == 0 {
		t.Fatalf("an old point release has updates: %v, %v", ups, err)
	}
	if raw := lines(apkOut(t, "list", "--upgradable")); len(raw) != len(ups) {
		t.Fatalf("the parser read %d of %d lines of the real output:\n%s", len(ups), len(raw), strings.Join(raw, "\n"))
	}
	p, err := e.PreviewUpgrade(context.Background())
	if err != nil || len(p.Blockers) != 0 || len(p.Simulation.Changes) == 0 || !p.Simulation.OK || !p.Simulation.SizesKnown {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	st := run(t, e, jobs.Spec{Kind: "upgrade", PlanID: p.ID})
	if st.Status != jobs.Done || len(st.Steps) != 2 || st.Steps[0].Name != "self-upgrade" {
		t.Fatalf("upgrade: %+v", st)
	}
	if left := lines(apkOut(t, "list", "--upgradable")); len(left) != 0 {
		t.Fatalf("still upgradable:\n%s", strings.Join(left, "\n"))
	}
	var post jobs.Post
	if err := json.Unmarshal(st.Post, &post); err != nil || post.Reboot || post.KernelChanged {
		t.Fatalf("a container has no kernel package and never needs a restart: %s, %v", st.Post, err)
	}
}

func releaseUpgrade(t *testing.T, from, to string) {
	e := prepare(t)
	if b := branch(t); b != from {
		t.Fatalf("the container runs Alpine %s, the test needs %s", b, from)
	}
	before := read(t, "/etc/apk/repositories")
	p, err := e.PreviewRelease(context.Background(), to)
	if err != nil || len(p.Blockers) != 0 || !p.Simulation.OK {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	st := run(t, e, jobs.Spec{Kind: "release", Target: to, PlanID: p.ID})
	if st.Status != jobs.Done || !st.ReposSwitched {
		t.Fatalf("release upgrade: %+v", st)
	}
	if b := branch(t); b != to {
		t.Fatalf("alpine-release says %s after the upgrade to %s", b, to)
	}
	if got := read(t, "/etc/apk/repositories"); got != strings.ReplaceAll(before, "/"+from+"/", "/"+to+"/") {
		t.Fatalf("repositories:\n%s", got)
	}
	if backup := read(t, repos.BackupPath("/", st.ID)); backup != before {
		t.Fatalf("backup:\n%s", backup)
	}
	if left := lines(apkOut(t, "list", "--upgradable")); len(left) != 0 {
		t.Fatalf("still upgradable:\n%s", strings.Join(left, "\n"))
	}
}

func TestReleaseUpgrade323To324(t *testing.T) { releaseUpgrade(t, "v3.23", "v3.24") }
func TestReleaseUpgrade322To323(t *testing.T) { releaseUpgrade(t, "v3.22", "v3.23") }

func TestPreviewChangesNothing(t *testing.T) {
	e := prepare(t)
	// A fresh container has no index in its cache, and apk's simulation fetches none ("Not continuing due to
	// stale/unavailable repositories"): the page checks for updates before it previews, so the test does too.
	if st := run(t, e, jobs.Spec{Kind: "refresh"}); st.Status != jobs.Done {
		t.Fatalf("refresh: %+v", st)
	}
	files := []string{"/etc/apk/repositories", "/etc/apk/world", "/lib/apk/db/installed"}
	sums := func() map[string]string {
		out := map[string]string{}
		for _, f := range files {
			out[f] = read(t, f)
		}
		return out
	}
	before := sums()
	jobsBefore, _ := os.ReadDir(filepath.Join(e.Base, "jobs"))
	if p, err := e.PreviewUpgrade(context.Background()); err != nil || len(p.Blockers) != 0 || !p.Simulation.OK {
		t.Fatalf("upgrade preview = %+v, %v", p, err)
	}
	// the simulation has to have run, or the previews would have changed nothing for want of trying
	if p, err := e.PreviewRelease(context.Background(), "v3.24"); err != nil || len(p.Blockers) != 0 || !p.Simulation.OK {
		t.Fatalf("release preview = %+v, %v", p, err)
	}
	if after := sums(); !maps.Equal(before, after) {
		for _, f := range files {
			if before[f] != after[f] {
				t.Errorf("a preview changed %s", f)
			}
		}
	}
	if backups, _ := filepath.Glob("/etc/apk/repositories.edelweiss-*"); len(backups) != 0 {
		t.Fatalf("a preview made backups: %v", backups)
	}
	if left, _ := filepath.Glob(filepath.Join(e.RunDir, "sim*.repos")); len(left) != 0 {
		t.Fatalf("a preview left its simulation file: %v", left)
	}
	if entries, _ := os.ReadDir(filepath.Join(e.Base, "jobs")); len(entries) != len(jobsBefore) {
		t.Fatalf("a preview made jobs: %v, there were %v", entries, jobsBefore)
	}
}

func TestUnreachableMirrorRestores(t *testing.T) {
	e := prepare(t)
	// the current branch up to date first: R2 then needs no download once the mirror is gone
	if st := run(t, e, jobs.Spec{Kind: "upgrade"}); st.Status != jobs.Done {
		t.Fatalf("upgrade: %+v", st)
	}
	p, err := e.PreviewRelease(context.Background(), "v3.24")
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	before := read(t, "/etc/apk/repositories")
	hosts, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(hosts, "127.0.0.1 dl-cdn.alpinelinux.org\n::1 dl-cdn.alpinelinux.org\n"); err != nil {
		hosts.Close()
		t.Fatal(err)
	}
	if err := hosts.Close(); err != nil {
		t.Fatal(err)
	}
	st := run(t, e, jobs.Spec{Kind: "release", Target: "v3.24", PlanID: p.ID})
	if st.Status != jobs.FailedClean || st.ReposSwitched {
		t.Fatalf("release upgrade without a mirror: %+v", st)
	}
	if got := read(t, "/etc/apk/repositories"); got != before {
		t.Fatalf("the repositories are not restored:\n%s", got)
	}
	if b := branch(t); b != "v3.23" {
		t.Fatalf("alpine-release says %s", b)
	}
}

// The runner survives the kill of its parent's whole process group, as procman stops an extension, and the real
// upgrade still finishes.
func TestRunnerSurvivesInAContainer(t *testing.T) {
	e := prepare(t)
	s := jobs.Store{Base: e.Base, Proc: host.New()}
	id, dir, err := s.Create(jobs.Spec{Kind: "upgrade", User: "myUser"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(os.Args[0])
	parent.Env = append(os.Environ(), "INTEGRATION_PARENT="+dir)
	parent.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Minute, "apk to run", func() bool {
		st, _ := s.Get(id)
		return st != nil && st.Status == jobs.Running && st.APKPID != 0
	})
	_ = syscall.Kill(-parent.Process.Pid, syscall.SIGKILL)
	_ = parent.Wait()
	var st *jobs.State
	waitFor(t, 10*time.Minute, "the job to end", func() bool {
		st, _ = s.Get(id)
		return st != nil && st.Finished()
	})
	if st.Status != jobs.Done {
		log, _ := os.ReadFile(filepath.Join(dir, "log"))
		t.Fatalf("the job ended %s:\n%s", st.Status, log)
	}
	if left := lines(apkOut(t, "list", "--upgradable")); len(left) != 0 {
		t.Fatalf("still upgradable:\n%s", strings.Join(left, "\n"))
	}
}

// Compare agrees with apk version -t on the versions Alpine's security database names (main and community of v3.24)
// and on the installed ones. The pairs are random ones and neighbours in sort order, spread over the whole corpus: the
// close calls.
func TestCompareMatchesApk(t *testing.T) {
	seen := map[string]bool{}
	for _, repo := range []string{"main", "community"} {
		data, err := fetch.Get(context.Background(), fetch.NewClient(), secdb.URL("v3.24", repo), secdb.Limit)
		if err != nil {
			t.Fatal(err)
		}
		ix, err := secdb.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		for _, fixes := range ix {
			for v := range fixes {
				if v != "0" {
					seen[v] = true
				}
			}
		}
	}
	for _, l := range lines(read(t, "/lib/apk/db/installed")) {
		if v, ok := strings.CutPrefix(l, "V:"); ok {
			seen[v] = true
		}
	}
	corpus := slices.Sorted(maps.Keys(seen))
	if len(corpus) < 100 {
		t.Fatalf("a corpus of %d versions only", len(corpus))
	}
	const pairs = 2000
	rng := rand.New(rand.NewSource(1))
	symbol := map[int]string{-1: "<", 0: "=", 1: ">"}
	compared, mismatches, unanswered := 0, 0, 0
	for i := 0; i < pairs; i++ {
		a, b := corpus[rng.Intn(len(corpus))], corpus[rng.Intn(len(corpus))]
		if i%4 == 0 { // neighbours in sort order, striding over the whole corpus
			k := (i / 4) * (len(corpus) - 1) / (pairs / 4)
			a, b = corpus[k], corpus[k+1]
		}
		cmd := exec.Command(apk.Path, "version", "-t", a, b)
		cmd.Env = apk.Env
		out, _ := cmd.Output() // the answer is the output; the exit status may encode it too
		want := strings.TrimSpace(string(out))
		if want != "<" && want != "=" && want != ">" {
			if unanswered++; unanswered <= 20 {
				t.Logf("apk cannot compare %q and %q: %q", a, b, want)
			}
			continue
		}
		compared++
		if got := symbol[apk.Compare(a, b)]; got != want {
			if mismatches++; mismatches <= 20 {
				t.Errorf("Compare(%q, %q) = %s, apk says %s", a, b, got, want)
			}
		}
	}
	// the oracle must have compared (nearly) everything it tried, or it proves nothing
	if compared < pairs*95/100 {
		t.Fatalf("apk compared only %d of %d pairs", compared, pairs)
	}
	t.Logf("compared %d of %d pairs of a corpus of %d versions; %d differ", compared, pairs, len(corpus), mismatches)
}
