package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/repos"
)

// testNow is the date the release list below is current on (v3.24 is the next branch of v3.23).
var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

const releasesJSON = `{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"edge","repos":[{"name":"main"},{"name":"community"},{"name":"testing"}]},
 {"rel_branch":"v3.24","eol_date":"2028-06-01","repos":[{"name":"main"},{"name":"community","eol_date":"2026-11-01"}],
  "releases":[{"version":"3.24.2","date":"2026-09-17","notes":"https://alpinelinux.org/posts/Alpine-3.24.2-released.html"}]},
 {"rel_branch":"v3.23","eol_date":"2027-11-01","repos":[{"name":"main"},{"name":"community","eol_date":"2026-05-01"}],
  "releases":[{"version":"3.23.6","date":"2026-09-17"}]}]}`

const repos323 = "https://dl-cdn.alpinelinux.org/alpine/v3.23/main\nhttps://dl-cdn.alpinelinux.org/alpine/v3.23/community\n"

// writeTree writes files (path → content) below root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// sys323 is an Alpine 3.23 host installed to disk.
var sys323 = map[string]string{
	"etc/alpine-release":   "3.23.6\n",
	"etc/apk/repositories": repos323,
	"lib/apk/db/installed": "P:musl\nV:1.2.5-r10\n",
	"proc/mounts":          "/dev/sda2 / ext4 rw 0 0\n",
}

// offlineEnv is DefaultEnv with metadata URLs that refuse at once: no test of this package goes online, not even one
// that runs a refresh and forgets to set the URLs.
func offlineEnv() Env {
	e := DefaultEnv()
	e.ReleasesURL = "http://127.0.0.1:1/releases.json"
	e.SecDBURL = func(branch, repo string) string { return "http://127.0.0.1:1/" + branch + "/" + repo + ".json" }
	return e
}

func TestTestEnvsStayOffline(t *testing.T) {
	k, _ := kindEnv(t, nil, nil)
	for name, e := range map[string]Env{"kindEnv": k, "planEnv": planEnv(t, nil, scripted{})} {
		if !strings.HasPrefix(e.ReleasesURL, "http://127.0.0.1:1/") || !strings.HasPrefix(e.SecDBURL("v3.23", "main"), "http://127.0.0.1:1/") {
			t.Errorf("%s would go online: %s, %s", name, e.ReleasesURL, e.SecDBURL("v3.23", "main"))
		}
	}
}

// kindEnv is a host root with Alpine 3.23 in sys mode, a cached release list, and the Env and RunnerConfig of the
// fake apk of Task 6 (apk, lbu and the read-only apk calls all are the fake; they share its step counter).
func kindEnv(t *testing.T, steps []fakeStep, files map[string]string) (Env, RunnerConfig) {
	t.Helper()
	cfg, _ := testEnv(t, steps)
	root := os.Getenv("FAKE_ROOT")
	writeTree(t, root, sys323)
	writeTree(t, root, files)
	e := offlineEnv()
	e.Root, e.Base, e.RunDir, e.APK, e.LBU = root, cfg.Base, t.TempDir(), cfg.APK, cfg.APK
	e.Host = host.FS{Root: root}
	e.Runner = apk.Exec{Path: cfg.APK, Env: cfg.Env}
	e.Now = func() time.Time { return testNow }
	e.Uname = func() string { return "6.12.1-0-lts" }
	writeTree(t, e.Base, map[string]string{"cache/releases.json": releasesJSON})
	cfg.Kinds = Kinds(e)
	return e, cfg
}

func runKind(t *testing.T, cfg RunnerConfig, spec Spec) *State {
	t.Helper()
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	id, dir, err := s.Create(spec, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	RunJob(cfg, dir)
	st, _ := s.Get(id)
	return st
}

// runKindWithBackup runs a job like runKind, with a backup of the repositories (old) in place for it, as a release
// job's R3 would have made. A kind that never restores must leave it alone; one that restores would put it back.
func runKindWithBackup(t *testing.T, e Env, cfg RunnerConfig, spec Spec, old string) *State {
	t.Helper()
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	id, dir, err := s.Create(spec, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repos.BackupPath(e.Root, id), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	RunJob(cfg, dir)
	st, _ := s.Get(id)
	return st
}

func stepNames(st *State) string {
	var names []string
	for _, s := range st.Steps {
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
}

func readRepos(t *testing.T, e Env) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(e.Root, "etc/apk/repositories"))
	return string(b)
}

// releaseSpec stores a fresh plan for target (the one a preview would have made) and returns the job's spec.
func releaseSpec(t *testing.T, e Env, target string, blockers ...string) Spec {
	t.Helper()
	fs, err := repos.Read(e.Root)
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{ID: "p1", Kind: "release", Target: target, Created: testNow, ReposSum: fs.Sum(), Blockers: blockers}
	if err := e.savePlan(p); err != nil {
		t.Fatal(err)
	}
	return Spec{Kind: "release", Target: target, PlanID: "p1", User: "admin"}
}

func TestRefreshWithoutNetwork(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{
		{},
		{Lines: []string{"curl-8.16.0-r1 x86_64 {curl} (curl) [upgradable from: curl-8.16.0-r0]"}}, // list --upgradable
	}, nil)
	_ = os.Remove(filepath.Join(e.Base, "cache/releases.json"))
	e.ReleasesURL = "http://127.0.0.1:1/releases.json" // refused at once
	e.SecDBURL = func(b, r string) string { return "http://127.0.0.1:1/" + b + "/" + r + ".json" }
	cfg.Kinds = Kinds(e)
	st := runKind(t, cfg, Spec{Kind: "refresh", User: "admin"})
	if st.Status != Done || stepNames(st) != "update" {
		t.Fatalf("a refresh without network is still done: %+v", st)
	}
	ups, _, err := e.CachedUpgradable()
	if err != nil || len(ups) != 1 || ups[0].Name != "curl" {
		t.Fatalf("cached list = %+v, %v", ups, err)
	}
	if _, _, err := e.CachedReleases(); err == nil {
		t.Fatal("a releases cache without network")
	}
	if targets, err := e.Targets(); err == nil || len(targets) != 0 {
		t.Fatalf("no release offer without the list: %+v, %v", targets, err)
	}
}

func TestUpgrade(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{
		{},
		{Lines: []string{"(1/1) Upgrading curl (8.16.0-r0 -> 8.16.0-r1)", "OK: 1 MiB in 1 packages"}},
		{}, // list --upgradable afterwards
	}, nil)
	st := runKind(t, cfg, Spec{Kind: "upgrade", User: "admin"})
	if st.Status != Done || stepNames(st) != "self-upgrade,upgrade" || len(st.Post) == 0 {
		t.Fatalf("state = %+v", st)
	}
	if _, _, err := e.CachedUpgradable(); err != nil {
		t.Fatalf("the list is cached again after an upgrade: %v", err)
	}
}

func TestUpgradeDisklessCommitsLBU(t *testing.T) {
	_, cfg := kindEnv(t, []fakeStep{
		{},
		{Lines: []string{"(1/1) Upgrading linux-lts (6.12.1-r0 -> 6.12.2-r0)"}},
		{},        // apk cache clean
		{Exit: 1}, // lbu commit fails
	}, map[string]string{"proc/mounts": "tmpfs / tmpfs rw 0 0\n", "etc/apk/cache/.keep": ""})
	st := runKind(t, cfg, Spec{Kind: "upgrade", User: "admin"})
	if stepNames(st) != "self-upgrade,upgrade,cache-clean,lbu-commit" || st.Status != DoneNotPersisted {
		t.Fatalf("steps %s, status %s", stepNames(st), st.Status)
	}
	// state.json is written indented, which re-indents the raw post: decode it instead of matching its text
	var post Post
	if err := json.Unmarshal(st.Post, &post); err != nil || !post.UpdateKernel {
		t.Fatalf("post = %s, %v", st.Post, err)
	}
}

func TestUpgradeDisklessWithoutChangesSkipsLBU(t *testing.T) {
	_, cfg := kindEnv(t, []fakeStep{{}, {Lines: []string{"OK: 1 MiB in 1 packages"}}},
		map[string]string{"proc/mounts": "tmpfs / tmpfs rw 0 0\n"})
	if st := runKind(t, cfg, Spec{Kind: "upgrade", User: "admin"}); stepNames(st) != "self-upgrade,upgrade" || st.Status != Done {
		t.Fatalf("steps %s, status %s", stepNames(st), st.Status)
	}
}

func TestReleaseHappyPath(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{
		{}, {}, // R2
		{}, // R4
		{}, // R6
		{Lines: []string{"(1/1) Upgrading alpine-release (3.23.6-r0 -> 3.24.2-r0)", "OK: 1 MiB in 1 packages"},
			Write: map[string]string{"lib/apk/db/installed": "P:musl\nV:1.2.6-r0\n"}}, // R7
	}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != Done || !st.ReposSwitched || stepNames(st) != "current-self-upgrade,current-upgrade,update,self-upgrade,upgrade" {
		t.Fatalf("state = %+v (%s)", st, stepNames(st))
	}
	if readRepos(t, e) != strings.ReplaceAll(repos323, "v3.23", "v3.24") {
		t.Fatalf("repositories: %s", readRepos(t, e))
	}
	if b, err := os.ReadFile(repos.BackupPath(e.Root, st.ID)); err != nil || string(b) != repos323 {
		t.Fatalf("the backup stays after a successful upgrade: %q, %v", b, err)
	}
}

func TestReleaseRestoresWhenUpdateFails(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {Exit: 1, Lines: []string{"ERROR: https://dl-cdn.alpinelinux.org/alpine/v3.24/main: temporary error"}}}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != FailedClean || st.ReposSwitched || readRepos(t, e) != repos323 {
		t.Fatalf("state = %+v, repositories: %s", st, readRepos(t, e))
	}
	if stepNames(st) != "current-self-upgrade,current-upgrade,update,restore-update" {
		t.Fatalf("steps %s", stepNames(st))
	}
}

func TestReleaseRestoresWhenUpdateReportsUnavailableRepos(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {Lines: []string{"1 unavailable, 0 stale; 9000 distinct packages available"}}}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != FailedClean || readRepos(t, e) != repos323 {
		t.Fatalf("state = %s, repositories: %s", st.Status, readRepos(t, e))
	}
}

func TestReleaseRestoresWhenOnlyDownloadsHappened(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {}, {}, {Exit: 3, Lines: []string{"fetch https://x", "(1/9) Downloading musl", "ERROR: network"}}}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != FailedClean || readRepos(t, e) != repos323 {
		t.Fatalf("state = %s, repositories: %s", st.Status, readRepos(t, e))
	}
}

func TestReleaseRestoresWhenSelfUpgradeFailsBeforeAChange(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {}, {Exit: 1, Lines: []string{"ERROR: network"}}}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != FailedClean || readRepos(t, e) != repos323 {
		t.Fatalf("state = %s, repositories: %s", st.Status, readRepos(t, e))
	}
}

func TestReleaseNeverRestoresAfterAChange(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {}, {}, {Exit: 2, Lines: []string{"(1/9) Upgrading musl (1.2.5-r10 -> 1.2.6-r0)", "ERROR: busybox: trigger failed"}}}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != Partial || !st.ReposSwitched || !strings.Contains(readRepos(t, e), "/v3.24/main") {
		t.Fatalf("state = %s, repositories: %s", st.Status, readRepos(t, e))
	}
}

func TestReleaseNeverRestoresWhenTheDatabaseChanged(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {}, {}, {Exit: 2, Write: map[string]string{"lib/apk/db/installed": "P:musl\nV:1.2.6-r0\n"}}}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != Partial || !strings.Contains(readRepos(t, e), "/v3.24/main") {
		t.Fatalf("state = %s, repositories: %s", st.Status, readRepos(t, e))
	}
}

func TestReleaseNeverRestoresWhenTheDatabaseIsUnreadable(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {}, {}, {Exit: 3, Lines: []string{"(1/9) Downloading musl", "ERROR: network"}}}, nil)
	db := filepath.Join(e.Root, "lib/apk/db/installed")
	if err := os.Remove(db); err != nil { // a directory cannot be read as a file: whether it changed cannot be told
		t.Fatal(err)
	}
	if err := os.Mkdir(db, 0o755); err != nil {
		t.Fatal(err)
	}
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != Partial || !strings.Contains(readRepos(t, e), "/v3.24/main") {
		t.Fatalf("state = %s, repositories: %s", st.Status, readRepos(t, e))
	}
}

func TestReleaseStopsWhenTheCurrentBranchFails(t *testing.T) {
	// R2 upgrades the branch the host is on: whatever it ends with, nothing was switched. The job is failed, not
	// partial: partial tells the page that a release upgrade stopped halfway and must be resumed, which would block
	// the restart for an upgrade that never began, with nothing to resume.
	failed := fakeStep{Exit: 1, Lines: []string{"ERROR: x"}}
	for name, c := range map[string]struct {
		steps []fakeStep
		ran   string
	}{
		"the self-upgrade fails":             {[]fakeStep{failed}, "current-self-upgrade"},
		"the upgrade fails with one error":   {[]fakeStep{{}, failed}, "current-self-upgrade,current-upgrade"},
		"the upgrade fails with many errors": {[]fakeStep{{}, {Exit: 17, Lines: []string{"ERROR: x"}}}, "current-self-upgrade,current-upgrade"},
		"the upgrade fails for good":         {[]fakeStep{{}, {Exit: 99, Lines: []string{"ERROR: Failed to open apk database"}}}, "current-self-upgrade,current-upgrade"},
		"the upgrade is partly done":         {[]fakeStep{{}, {Exit: 2, Lines: []string{"(1/9) Upgrading musl (1.2.5-r10 -> 1.2.5-r11)", "ERROR: busybox: trigger failed"}}}, "current-self-upgrade,current-upgrade"},
	} {
		t.Run(name, func(t *testing.T) {
			e, cfg := kindEnv(t, c.steps, nil)
			st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
			if st.Status != Failed || st.ReposSwitched || readRepos(t, e) != repos323 || stepNames(st) != c.ran {
				t.Fatalf("state = %+v (%s)", st, stepNames(st))
			}
			if want := "the current branch could not be fully upgraded; nothing was switched"; st.Error != want {
				t.Fatalf("error = %q, want %q", st.Error, want)
			}
			if _, err := os.Stat(repos.BackupPath(e.Root, st.ID)); err == nil {
				t.Fatal("a backup before the switch")
			}
			if r := e.Resumable(Store{Base: cfg.Base, Proc: cfg.Proc}); r != nil {
				t.Fatalf("there is nothing to resume: %+v", r)
			}
		})
	}
}

func TestReleaseAbortsWhenTheRepositoriesChanged(t *testing.T) {
	e, cfg := kindEnv(t, nil, nil)
	spec := releaseSpec(t, e, "v3.24")
	writeTree(t, e.Root, map[string]string{"etc/apk/repositories": "https://other.example.org/alpine/v3.23/main\n"})
	st := runKind(t, cfg, spec)
	if st.Status != Failed || !strings.Contains(st.Error, "changed since the preview") || len(st.Steps) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestReleaseRefusesABlockedOrForeignPlan(t *testing.T) {
	e, cfg := kindEnv(t, nil, nil)
	if st := runKind(t, cfg, releaseSpec(t, e, "v3.24", "usr_separate")); st.Status != Failed || len(st.Steps) != 0 {
		t.Fatalf("a blocked plan: %+v", st)
	}
	spec := releaseSpec(t, e, "v3.24")
	spec.Target = "edge"
	if st := runKind(t, cfg, spec); st.Status != Failed || len(st.Steps) != 0 {
		t.Fatalf("a plan for another target: %+v", st)
	}
	if st := runKind(t, cfg, releaseSpec(t, e, "v3.25")); st.Status != Failed || !strings.Contains(st.Error, "not allowed") {
		t.Fatalf("a target the release list does not offer: %+v", st)
	}
}

func TestReleaseLatestStableOnly(t *testing.T) {
	ls := "https://dl-cdn.alpinelinux.org/alpine/latest-stable/main\nhttps://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {}}, map[string]string{"etc/apk/repositories": ls})
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != Done || stepNames(st) != "update,self-upgrade,upgrade" || readRepos(t, e) != ls {
		t.Fatalf("state = %+v (%s)", st, stepNames(st))
	}
	if _, err := os.Stat(repos.BackupPath(e.Root, st.ID)); err == nil {
		t.Fatal("no backup when no line changes")
	}
}

func TestResumeNeverRestores(t *testing.T) {
	switched := strings.ReplaceAll(repos323, "v3.23", "v3.24")
	e, cfg := kindEnv(t, []fakeStep{{Exit: 1, Lines: []string{"ERROR: temporary error"}}}, map[string]string{"etc/apk/repositories": switched})
	st := runKindWithBackup(t, e, cfg, Spec{Kind: "resume", Target: "v3.24", User: "admin"}, repos323)
	if st.Status != Failed || readRepos(t, e) != switched || stepNames(st) != "update" {
		t.Fatalf("state = %+v, repositories: %s", st, readRepos(t, e))
	}
	if _, err := os.Stat(repos.BackupPath(e.Root, st.ID)); err != nil {
		t.Fatalf("a resume must not use a backup: %v", err)
	}
}

func TestResumeNeverRestoresBeforeAChange(t *testing.T) {
	switched := strings.ReplaceAll(repos323, "v3.23", "v3.24")
	e, cfg := kindEnv(t, []fakeStep{{}, {Exit: 1, Lines: []string{"ERROR: network"}}}, map[string]string{"etc/apk/repositories": switched})
	st := runKindWithBackup(t, e, cfg, Spec{Kind: "resume", Target: "v3.24", User: "admin"}, repos323)
	if st.Status != Failed || readRepos(t, e) != switched || stepNames(st) != "update,self-upgrade" {
		t.Fatalf("state = %+v (%s), repositories: %s", st, stepNames(st), readRepos(t, e))
	}
}

func TestReleaseLatestStableOnlyNeverRestores(t *testing.T) {
	ls := "https://dl-cdn.alpinelinux.org/alpine/latest-stable/main\nhttps://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"
	e, cfg := kindEnv(t, []fakeStep{{Exit: 1, Lines: []string{"ERROR: temporary error"}}}, map[string]string{"etc/apk/repositories": ls})
	st := runKindWithBackup(t, e, cfg, releaseSpec(t, e, "v3.24"), repos323)
	if st.Status != Failed || st.ReposSwitched || readRepos(t, e) != ls || stepNames(st) != "update" {
		t.Fatalf("nothing was switched, so nothing is restored: %+v (%s), repositories: %s", st, stepNames(st), readRepos(t, e))
	}
}

func TestReleaseNeverRestoresAfterTheSelfUpgradeChanged(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {},
		{Lines: []string{"(1/1) Upgrading apk-tools (2.14.9-r3 -> 3.0.1-r0)"}}, // R6 changed a package ...
		{Exit: 3, Lines: []string{"ERROR: network"}},                           // ... and R7 only failed to download
	}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != Partial || !st.ReposSwitched || !strings.Contains(readRepos(t, e), "/v3.24/main") {
		t.Fatalf("state = %s, repositories: %s", st.Status, readRepos(t, e))
	}
}

func TestResumeFinishesTheUpgrade(t *testing.T) {
	switched := strings.ReplaceAll(repos323, "v3.23", "v3.24")
	_, cfg := kindEnv(t, []fakeStep{{}, {}, {Lines: []string{"(1/1) Upgrading busybox (1.37.0-r20 -> 1.37.0-r30)"}}}, map[string]string{"etc/apk/repositories": switched})
	st := runKind(t, cfg, Spec{Kind: "resume", Target: "v3.24", User: "admin"})
	if st.Status != Done || stepNames(st) != "update,self-upgrade,upgrade" {
		t.Fatalf("state = %+v (%s)", st, stepNames(st))
	}
}

func TestResumeRefusesOtherRepositories(t *testing.T) {
	_, cfg := kindEnv(t, nil, nil) // the repositories still point to v3.23
	if st := runKind(t, cfg, Spec{Kind: "resume", Target: "v3.24", User: "admin"}); st.Status != Failed || len(st.Steps) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestResumable(t *testing.T) {
	switched := strings.ReplaceAll(repos323, "v3.23", "v3.24")
	e, cfg := kindEnv(t, nil, nil)
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	if r := e.Resumable(s); r != nil {
		t.Fatalf("nothing to resume: %+v", r)
	}
	writeTree(t, e.Root, map[string]string{"etc/apk/repositories": switched})
	if r := e.Resumable(s); r == nil || r.Target != "v3.24" || r.Reason != "mismatch" {
		t.Fatalf("repositories ahead of the release: %+v", r)
	}
	writeTree(t, e.Root, map[string]string{"etc/alpine-release": "3.24.2\n"})
	if r := e.Resumable(s); r != nil {
		t.Fatalf("a finished upgrade: %+v", r)
	}
	_, dir, _ := s.Create(Spec{Kind: "release", Target: "v3.24"}, time.Now())
	st, _ := ReadState(dir)
	st.Status = Partial
	_ = WriteJSON(dir+"/state.json", st, 0o644)
	if r := e.Resumable(s); r == nil || r.Reason != "partial" {
		t.Fatalf("a partial release job: %+v", r)
	}
	_, dir, _ = s.Create(Spec{Kind: "resume", Target: "v3.24"}, time.Now().Add(time.Second))
	st, _ = ReadState(dir)
	st.Status = Done
	_ = WriteJSON(dir+"/state.json", st, 0o644)
	if r := e.Resumable(s); r != nil {
		t.Fatalf("a resume that finished: %+v", r)
	}
}

// jobWith stores a finished job for Resumable (an interrupted one is a running job of another boot); at orders them.
func jobWith(t *testing.T, s Store, kind, target string, status Status, switched bool, at time.Time) {
	t.Helper()
	_, dir, err := s.Create(Spec{Kind: kind, Target: target}, at)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ReadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Status, st.ReposSwitched = status, switched
	if status == Interrupted {
		st.Status, st.BootID = Running, "another boot"
	}
	if err := WriteJSON(dir+"/state.json", st, 0o644); err != nil {
		t.Fatal(err)
	}
}

const reposLatestStable = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/main\nhttps://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"

func TestResumableLatestStable(t *testing.T) {
	// Branches ignores latest-stable lines, so the repositories name no branch: the newest release or resume job says
	// where the upgrade was going. A job that switched nothing can still be interrupted.
	e, cfg := kindEnv(t, nil, map[string]string{"etc/apk/repositories": reposLatestStable})
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	t0 := time.Now()
	if r := e.Resumable(s); r != nil {
		t.Fatalf("no job: %+v", r)
	}
	for i, c := range []struct {
		kind   string
		status Status
		want   *Resume
	}{
		{"release", Partial, &Resume{Target: "v3.24", Reason: "partial"}},
		{"resume", Done, nil},
		{"resume", Failed, &Resume{Target: "v3.24", Reason: "failed"}},
		{"release", Interrupted, &Resume{Target: "v3.24", Reason: "interrupted"}},
		{"release", Failed, nil},
		{"release", DoneNotPersisted, nil},
	} {
		jobWith(t, s, c.kind, "v3.24", c.status, false, t0.Add(time.Duration(i)*time.Second))
		got := e.Resumable(s)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Fatalf("%d: newest job %s %s: Resumable = %+v, want %+v", i, c.kind, c.status, got, c.want)
		}
	}
}

func TestResumableLatestStableNeedsAJobForAStableTarget(t *testing.T) {
	e, cfg := kindEnv(t, nil, map[string]string{"etc/apk/repositories": reposLatestStable})
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	jobWith(t, s, "release", "edge", Partial, true, time.Now()) // lines that follow latest-stable never go to edge
	if r := e.Resumable(s); r != nil {
		t.Fatalf("a job to edge: %+v", r)
	}
}

func TestResumeOnLatestStable(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {Lines: []string{"(1/1) Upgrading busybox (1.37.0-r20 -> 1.37.0-r30)"}}},
		map[string]string{"etc/apk/repositories": reposLatestStable})
	st := runKind(t, cfg, Spec{Kind: "resume", Target: "v3.24", User: "admin"})
	if st.Status != Done || stepNames(st) != "update,self-upgrade,upgrade" || readRepos(t, e) != reposLatestStable {
		t.Fatalf("state = %+v (%s), repositories: %s", st, stepNames(st), readRepos(t, e))
	}
	if st.ReposSwitched {
		t.Fatal("a resume on latest-stable switched nothing")
	}
}

func TestResumeNeverRestoresOnLatestStable(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{Exit: 1, Lines: []string{"ERROR: temporary error"}}},
		map[string]string{"etc/apk/repositories": reposLatestStable})
	st := runKindWithBackup(t, e, cfg, Spec{Kind: "resume", Target: "v3.24", User: "admin"}, repos323)
	if st.Status != Failed || readRepos(t, e) != reposLatestStable || stepNames(st) != "update" {
		t.Fatalf("state = %+v, repositories: %s", st, readRepos(t, e))
	}
}

func TestResumeRefusesEdgeOnLatestStable(t *testing.T) {
	_, cfg := kindEnv(t, nil, map[string]string{"etc/apk/repositories": reposLatestStable})
	if st := runKind(t, cfg, Spec{Kind: "resume", Target: "edge", User: "admin"}); st.Status != Failed || len(st.Steps) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestResumeNeedsSysMode(t *testing.T) {
	switched := strings.ReplaceAll(repos323, "v3.23", "v3.24")
	diskless := map[string]string{"etc/apk/repositories": switched, "proc/mounts": "tmpfs / tmpfs rw 0 0\n"}
	e, cfg := kindEnv(t, nil, diskless)
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	if r := e.Resumable(s); r != nil {
		t.Fatalf("a diskless host has nothing to resume: %+v", r)
	}
	st := runKind(t, cfg, Spec{Kind: "resume", Target: "v3.24", User: "admin"})
	if st.Status != Failed || len(st.Steps) != 0 || !strings.Contains(st.Error, "installed to disk") {
		t.Fatalf("state = %+v", st)
	}
	writeTree(t, e.Root, map[string]string{"proc/mounts": "/dev/sda2 / ext4 rw 0 0\n"})
	if r := e.Resumable(s); r == nil || r.Reason != "mismatch" {
		t.Fatalf("the same repositories on a system installed to disk: %+v", r)
	}
}

func TestResumableNeedsAKnownRelease(t *testing.T) {
	switched := strings.ReplaceAll(repos323, "v3.23", "v3.24")
	e, cfg := kindEnv(t, nil, map[string]string{"etc/apk/repositories": switched})
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	if r := e.Resumable(s); r == nil || r.Reason != "mismatch" {
		t.Fatalf("control: %+v", r)
	}
	writeTree(t, e.Root, map[string]string{"etc/alpine-release": "\n"})
	if r := e.Resumable(s); r != nil {
		t.Fatalf("an empty release file: %+v", r)
	}
	if err := os.Remove(filepath.Join(e.Root, "etc/alpine-release")); err != nil {
		t.Fatal(err)
	}
	if r := e.Resumable(s); r != nil {
		t.Fatalf("no release file: %+v", r)
	}
}

func TestReleaseNeverRestoresAfterAPaddedChangeLine(t *testing.T) {
	// apk-tools 3 pads the counter ("( 1/12)"), and writes the installed database only after the change loop: a
	// runner that dies within the first 99 changes leaves the hash as it was. The change line alone says that
	// packages changed, and the repositories must stay.
	for _, exit := range []int{2, 255} {
		t.Run(fmt.Sprint("exit ", exit), func(t *testing.T) {
			e, cfg := kindEnv(t, []fakeStep{{}, {}, {}, {},
				{Exit: exit, Lines: []string{"( 1/12) Upgrading musl (1.2.5-r10 -> 1.2.6-r0)"}}}, nil)
			st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
			if st.Status != Partial || !st.ReposSwitched || !strings.Contains(readRepos(t, e), "/v3.24/main") {
				t.Fatalf("state = %s, repositories: %s", st.Status, readRepos(t, e))
			}
		})
	}
}

func TestUpgradeDisklessCommitsAfterPaddedChangeLines(t *testing.T) {
	_, cfg := kindEnv(t, []fakeStep{
		{},
		{Lines: []string{"(  1/400) Upgrading linux-lts (6.12.1-r0 -> 6.12.2-r0)"}},
		{}, // lbu commit
	}, map[string]string{"proc/mounts": "tmpfs / tmpfs rw 0 0\n"})
	st := runKind(t, cfg, Spec{Kind: "upgrade", User: "admin"})
	if stepNames(st) != "self-upgrade,upgrade,lbu-commit" || st.Status != Done {
		t.Fatalf("steps %s, status %s", stepNames(st), st.Status)
	}
	var post Post
	if err := json.Unmarshal(st.Post, &post); err != nil || !post.UpdateKernel {
		t.Fatalf("post = %s, %v", st.Post, err)
	}
}

func TestUpgradeDisklessCommitsWhenTheDatabaseChanged(t *testing.T) {
	// no change line in what apk printed, but the installed database is not the one the job started with
	_, cfg := kindEnv(t, []fakeStep{
		{},
		{Lines: []string{"OK: 1 MiB in 1 packages"}, Write: map[string]string{"lib/apk/db/installed": "P:musl\nV:1.2.6-r0\n"}},
		{}, // lbu commit
	}, map[string]string{"proc/mounts": "tmpfs / tmpfs rw 0 0\n"})
	if st := runKind(t, cfg, Spec{Kind: "upgrade", User: "admin"}); stepNames(st) != "self-upgrade,upgrade,lbu-commit" || st.Status != Done {
		t.Fatalf("steps %s, status %s", stepNames(st), st.Status)
	}
}

func TestReleaseRestoresWhenTheSwitchFails(t *testing.T) {
	// WriteAtomic can fail after the rename (the fsync of the directory): the file may be the new one. Either way the
	// backup goes back, as nothing else has happened yet.
	renameFailed := func(string, []byte, os.FileMode) error { return errors.New("rename failed") }
	fsyncFailed := func(path string, data []byte, perm os.FileMode) error {
		if err := repos.WriteAtomic(path, data, perm); err != nil {
			return err
		}
		return errors.New("fsync failed")
	}
	for name, write := range map[string]func(string, []byte, os.FileMode) error{"rename": renameFailed, "fsync": fsyncFailed} {
		t.Run(name, func(t *testing.T) {
			old := writeRepos
			writeRepos = write
			t.Cleanup(func() { writeRepos = old })
			e, cfg := kindEnv(t, []fakeStep{{}, {}}, nil)
			st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
			if st.Status != Failed || st.ReposSwitched || readRepos(t, e) != repos323 || stepNames(st) != "current-self-upgrade,current-upgrade" {
				t.Fatalf("state = %+v (%s), repositories: %s", st, stepNames(st), readRepos(t, e))
			}
			if _, err := os.Stat(repos.BackupPath(e.Root, st.ID)); err == nil {
				t.Fatal("the backup was put back, so it is gone")
			}
		})
	}
}

func TestReleaseSwitchFailsAndTheBackupIsGone(t *testing.T) {
	// when the backup cannot be put back either, nobody knows which file is in place: the job stays marked as switched
	old := writeRepos
	t.Cleanup(func() { writeRepos = old })
	writeRepos = func(path string, _ []byte, _ os.FileMode) error {
		backups, _ := filepath.Glob(path + ".edelweiss-*")
		for _, b := range backups {
			_ = os.Remove(b)
		}
		return errors.New("rename failed")
	}
	e, cfg := kindEnv(t, []fakeStep{{}, {}}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != Failed || !st.ReposSwitched || !strings.Contains(st.Error, "could not be put back") {
		t.Fatalf("state = %+v", st)
	}
}

func TestLockTimeoutIsSaidOnEveryStep(t *testing.T) {
	busy := fakeStep{Exit: 99, Lines: []string{"ERROR: Unable to lock database: Resource temporarily unavailable"}}
	switched := map[string]string{"etc/apk/repositories": strings.ReplaceAll(repos323, "v3.23", "v3.24")}
	release := func(t *testing.T, e Env) Spec { return releaseSpec(t, e, "v3.24") }
	resume := func(*testing.T, Env) Spec { return Spec{Kind: "resume", Target: "v3.24", User: "admin"} }
	cases := map[string]struct {
		steps  []fakeStep
		files  map[string]string
		spec   func(*testing.T, Env) Spec
		status Status
	}{
		"R2 self-upgrade": {[]fakeStep{busy}, nil, release, Failed},
		"R2 upgrade":      {[]fakeStep{{}, busy}, nil, release, Failed},
		"R4":              {[]fakeStep{{}, {}, busy}, nil, release, FailedClean},
		"R6":              {[]fakeStep{{}, {}, {}, busy}, nil, release, FailedClean},
		"R7":              {[]fakeStep{{}, {}, {}, {}, busy}, nil, release, FailedClean},
		"resume R4":       {[]fakeStep{busy}, switched, resume, Failed},
		"resume R7":       {[]fakeStep{{}, {}, busy}, switched, resume, Failed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e, cfg := kindEnv(t, c.steps, c.files)
			st := runKind(t, cfg, c.spec(t, e))
			if st.Status != c.status || !strings.Contains(st.Error, "apk is busy") {
				t.Fatalf("state = %s, error %q", st.Status, st.Error)
			}
		})
	}
}

func TestReleasePartialRefreshesTheList(t *testing.T) {
	e, cfg := kindEnv(t, []fakeStep{{}, {}, {}, {},
		{Exit: 2, Lines: []string{"( 1/12) Upgrading musl (1.2.5-r10 -> 1.2.6-r0)"}},
		{Lines: []string{"curl-8.16.0-r1 x86_64 {curl} (curl) [upgradable from: curl-8.16.0-r0]"}}, // list --upgradable
	}, nil)
	st := runKind(t, cfg, releaseSpec(t, e, "v3.24"))
	if st.Status != Partial || len(st.Post) == 0 {
		t.Fatalf("state = %+v", st)
	}
	if ups, _, err := e.CachedUpgradable(); err != nil || len(ups) != 1 || ups[0].Name != "curl" {
		t.Fatalf("the list is cached after a partial upgrade too: %+v, %v", ups, err)
	}
}

// breakMode makes the mode of the host unreadable: /proc/mounts is a directory.
func breakMode(t *testing.T, e Env) {
	t.Helper()
	mounts := filepath.Join(e.Root, "proc/mounts")
	if err := os.Remove(mounts); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mounts, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Host.Mode(); err == nil {
		t.Fatal("the mode is still readable")
	}
}

func TestUpgradeWithAnUnreadableModeDoesNotRunLBU(t *testing.T) {
	// lbu commit is for a host known to run from RAM: a mode that cannot be read is no reason to run it
	e, cfg := kindEnv(t, []fakeStep{
		{},
		{Lines: []string{"(1/1) Upgrading linux-lts (6.12.1-r0 -> 6.12.2-r0)"}},
		{}, {}, // apk cache clean and lbu commit, which must not run
	}, map[string]string{"etc/apk/cache/.keep": ""})
	breakMode(t, e)
	st := runKind(t, cfg, Spec{Kind: "upgrade", User: "admin"})
	if stepNames(st) != "self-upgrade,upgrade" || st.Status != Done {
		t.Fatalf("steps %s, status %s", stepNames(st), st.Status)
	}
	var post Post
	if err := json.Unmarshal(st.Post, &post); err != nil || post.UpdateKernel {
		t.Fatalf("post = %s, %v: the hint for diskless hosts is for hosts known to be diskless", st.Post, err)
	}
}

func TestReleaseNeedsSysMode(t *testing.T) {
	// the preview checks the mode, and so does the job: the host may differ from the one the preview saw
	for name, change := range map[string]func(*testing.T, Env){
		"diskless": func(t *testing.T, e Env) {
			writeTree(t, e.Root, map[string]string{"proc/mounts": "tmpfs / tmpfs rw 0 0\n"})
		},
		"data mode": func(t *testing.T, e Env) {
			writeTree(t, e.Root, map[string]string{"proc/mounts": "tmpfs / tmpfs rw 0 0\n/dev/sda3 /var ext4 rw 0 0\n"})
		},
		"unreadable": breakMode,
	} {
		t.Run(name, func(t *testing.T) {
			e, cfg := kindEnv(t, []fakeStep{{}, {}, {}, {}, {}}, nil)
			spec := releaseSpec(t, e, "v3.24")
			change(t, e)
			st := runKind(t, cfg, spec)
			if st.Status != Failed || len(st.Steps) != 0 || !strings.Contains(st.Error, "installed to disk") || readRepos(t, e) != repos323 {
				t.Fatalf("state = %+v, repositories: %s", st, readRepos(t, e))
			}
		})
	}
}

func reposTo(branch string) string { return strings.ReplaceAll(repos323, "v3.23", branch) }

// An unoffered target is one the repositories were edited to by hand, or an old job's: a resume runs apk upgrade -a
// on whatever they point to, so it is offered only to a branch this page upgrades to – the target of the newest
// release job, or the next stable branch – never by the repositories alone.
func TestResumableOnlyToABranchThisPageUpgradesTo(t *testing.T) {
	endedList := strings.Replace(releasesJSON, `"eol_date":"2028-06-01"`, `"eol_date":"2026-01-01"`, 1)
	t0 := time.Now().Add(-time.Hour)
	for name, c := range map[string]struct {
		branch string
		list   string
		jobs   func(*testing.T, Store)
		want   *Resume
	}{
		"the next branch":                  {"v3.24", "", nil, &Resume{Target: "v3.24", Reason: "mismatch"}},
		"a branch two ahead":               {"v3.25", "", nil, nil},
		"a branch far ahead":               {"v3.26", "", nil, nil},
		"edge, never upgraded to":          {"edge", "", nil, nil},
		"the next branch, when it ended":   {"v3.24", endedList, nil, nil},
		"edge after a release job to edge": {"edge", "", func(t *testing.T, s Store) { jobWith(t, s, "release", "edge", Failed, false, t0) }, &Resume{Target: "edge", Reason: "mismatch"}},
		"edge after a partial job to edge": {"edge", "", func(t *testing.T, s Store) { jobWith(t, s, "release", "edge", Partial, true, t0) }, &Resume{Target: "edge", Reason: "mismatch"}},
		"edge, though the newest release job went elsewhere": {"edge", "", func(t *testing.T, s Store) {
			jobWith(t, s, "release", "edge", Done, true, t0)
			jobWith(t, s, "release", "v3.24", FailedClean, false, t0.Add(time.Minute))
		}, nil},
		"a branch two ahead after a release job to it":     {"v3.25", "", func(t *testing.T, s Store) { jobWith(t, s, "release", "v3.25", Done, true, t0) }, &Resume{Target: "v3.25", Reason: "mismatch"}},
		"a branch two ahead after a resume job to it only": {"v3.25", "", func(t *testing.T, s Store) { jobWith(t, s, "resume", "v3.25", Failed, true, t0) }, nil},
	} {
		t.Run(name, func(t *testing.T) {
			e, cfg := kindEnv(t, nil, map[string]string{"etc/apk/repositories": reposTo(c.branch)})
			s := Store{Base: cfg.Base, Proc: cfg.Proc}
			if c.list != "" {
				writeTree(t, e.Base, map[string]string{"cache/releases.json": c.list})
			}
			if c.jobs != nil {
				c.jobs(t, s)
			}
			got := e.Resumable(s)
			if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
				t.Fatalf("Resumable = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestResumableOfAJobIsBoundToTheNewestReleaseJob(t *testing.T) {
	// the history decides for repositories on the branch the host already runs, too: a failed resume to v3.25 is no
	// reason to resume to it unless the newest release job went there (the page never offered the resume)
	e, cfg := kindEnv(t, nil, map[string]string{"etc/apk/repositories": reposTo("v3.25"), "etc/alpine-release": "3.25.0\n"})
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	t0 := time.Now().Add(-time.Hour)
	jobWith(t, s, "release", "v3.24", Done, true, t0)
	jobWith(t, s, "resume", "v3.25", Failed, true, t0.Add(time.Minute))
	if r := e.Resumable(s); r != nil {
		t.Fatalf("the newest release job did not go to v3.25: %+v", r)
	}
	jobWith(t, s, "release", "v3.25", Partial, true, t0.Add(2*time.Minute))
	jobWith(t, s, "resume", "v3.25", Failed, true, t0.Add(3*time.Minute))
	if r := e.Resumable(s); r == nil || r.Reason != "failed" || r.Target != "v3.25" {
		t.Fatalf("the newest release job went to v3.25: %+v", r)
	}
}

func TestResumeRefusesATargetThePageDidNotUpgradeTo(t *testing.T) {
	for name, branch := range map[string]string{"a branch ahead": "v3.26", "the branch after the next": "v3.25", "edge": "edge"} {
		t.Run(name, func(t *testing.T) {
			e, cfg := kindEnv(t, []fakeStep{{}, {}, {}}, map[string]string{"etc/apk/repositories": reposTo(branch)})
			// the repositories do point to the target: it is the target, not the repositories, that is refused
			st := runKind(t, cfg, Spec{Kind: "resume", Target: branch, User: "admin"})
			if st.Status != Failed || len(st.Steps) != 0 || !strings.Contains(st.Error, "did not upgrade to") {
				t.Fatalf("state = %+v", st)
			}
			if readRepos(t, e) != reposTo(branch) {
				t.Fatal("the repositories are not touched")
			}
		})
	}
}

func TestResumeToEdgeAfterAReleaseJobToEdge(t *testing.T) {
	_, cfg := kindEnv(t, []fakeStep{{}, {}, {Lines: []string{"(1/1) Upgrading busybox (1.37.0-r20 -> 1.37.0-r30)"}}},
		map[string]string{"etc/apk/repositories": reposTo("edge")})
	jobWith(t, Store{Base: cfg.Base, Proc: cfg.Proc}, "release", "edge", Partial, true, time.Now().Add(-time.Minute))
	st := runKind(t, cfg, Spec{Kind: "resume", Target: "edge", User: "admin"})
	if st.Status != Done || stepNames(st) != "update,self-upgrade,upgrade" {
		t.Fatalf("state = %+v (%s)", st, stepNames(st))
	}
}

func TestRebootPackage(t *testing.T) {
	for name, want := range map[string]bool{
		"linux-lts": true, "linux-virt": true, "linux-rpi": true, "linux-firmware-intel": true, "linux-firmware": true,
		"grub-efi": true, "raspberrypi-bootloader": true, "syslinux": true,
		"linux-pam": false, "linux-headers": false, "curl": false,
	} {
		if RebootPackage(name) != want {
			t.Errorf("RebootPackage(%q) = %v", name, !want)
		}
	}
}
