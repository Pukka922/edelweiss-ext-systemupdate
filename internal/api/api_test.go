package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// TestMain keeps the expected error logs of the failure tests out of the test output.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

const releasesJSON = `{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"edge","repos":[{"name":"main"},{"name":"community"}]},
 {"rel_branch":"v3.24","eol_date":"2028-06-01","repos":[{"name":"main"},{"name":"community","eol_date":"2026-11-01"}],
  "releases":[{"version":"3.24.2","date":"2026-09-17","notes":"https://alpinelinux.org/posts/Alpine-3.24.2-released.html"}]},
 {"rel_branch":"v3.23","eol_date":"2027-11-01","repos":[{"name":"main"},{"name":"community","eol_date":"2026-05-01"}],
  "releases":[{"version":"3.23.6","date":"2026-09-17"}]}]}`

const repos323 = "https://dl-cdn.alpinelinux.org/alpine/v3.23/main\nhttps://dl-cdn.alpinelinux.org/alpine/v3.23/community\n"

var alice = sdk.User{Name: "alice", UID: 1000}
var admin = sdk.User{Name: "alice", UID: 1000, Admin: true}

func write(t *testing.T, root string, files map[string]string) {
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

// fakeRunner answers the apk calls of the previews by the suffix of their joined arguments; others get "", 0.
type fakeRunner map[string]string

func (f fakeRunner) Run(_ context.Context, args ...string) (string, int, error) {
	key := strings.Join(args, " ")
	for suffix, out := range f {
		if strings.HasSuffix(key, suffix) {
			return out, 0, nil
		}
	}
	return "", 0, nil
}

// fakeLauncher records the launched job directories. With start, it plays a runner that starts at once: the job
// runs, with the test process as its runner.
type fakeLauncher struct {
	mu    sync.Mutex
	dirs  []string
	start bool
	fail  error
}

func (l *fakeLauncher) Launch(dir string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail != nil {
		return l.fail
	}
	l.dirs = append(l.dirs, dir)
	if l.start {
		setState(dir, func(st *jobs.State) { markRunning(st) })
	}
	return nil
}

func markRunning(st *jobs.State) {
	me, _ := host.New().StartTime(os.Getpid())
	st.Status, st.Started = jobs.Running, time.Now()
	st.RunnerPID, st.RunnerStart, st.BootID = os.Getpid(), me, host.New().BootID()
}

func setState(dir string, f func(*jobs.State)) {
	st, _ := jobs.ReadState(dir)
	f(st)
	_ = jobs.WriteJSON(filepath.Join(dir, "state.json"), st, 0o644)
}

type fixture struct {
	api      *API
	app      *fiber.App
	launcher *fakeLauncher
	root     string
}

// newFixture is an Alpine 3.23 host in sys mode below a temp root, with a cached release list.
func newFixture(t *testing.T, files map[string]string, run fakeRunner) *fixture {
	t.Helper()
	root := t.TempDir()
	write(t, root, map[string]string{
		"etc/alpine-release":   "3.23.6\n",
		"etc/apk/arch":         "x86_64\n",
		"etc/apk/repositories": repos323,
		"lib/apk/db/installed": "P:musl\nV:1.2.5-r10\n",
		"proc/mounts":          "/dev/sda2 / ext4 rw 0 0\n",
	})
	write(t, root, files)
	env := jobs.DefaultEnv()
	env.ReleasesURL = "http://127.0.0.1:1/releases.json" // refused at once: no test goes online
	env.SecDBURL = func(branch, repo string) string { return "http://127.0.0.1:1/" + branch + "/" + repo + ".json" }
	env.Root, env.Base, env.RunDir, env.Host, env.Runner = root, t.TempDir(), t.TempDir(), host.FS{Root: root}, run
	env.Statfs = func(string) (uint64, uint64, error) { return 100 << 30, 50 << 30, nil }
	env.Now = func() time.Time { return testNow }
	env.Uname = func() string { return "6.12.1-0-lts" }
	write(t, env.Base, map[string]string{"cache/releases.json": releasesJSON})
	l := &fakeLauncher{start: true}
	a := &API{Env: env, Store: jobs.Store{Base: env.Base, Proc: host.New()}, Launcher: l,
		LockPath: filepath.Join(t.TempDir(), "lock"), Poll: 10 * time.Millisecond, StartWait: time.Second}
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, a)
	return &fixture{api: a, app: app, launcher: l, root: root}
}

func (f *fixture) do(t *testing.T, method, path, body string, u sdk.User) *http.Response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return sdktest.Do(t, f.app, sdktest.Request(method, path, r, u))
}

// job creates a job of kind with the store and changes its state with f.
func (f *fixture) job(t *testing.T, kind string, change func(*jobs.State)) (*jobs.State, string) {
	t.Helper()
	id, dir, err := f.api.Store.Create(jobs.Spec{Kind: kind, User: "alice"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	setState(dir, change)
	st, _ := f.api.Store.Get(id)
	return st, dir
}

type errorBody struct {
	Error struct {
		Code  string      `json:"code"`
		Field string      `json:"field"`
		Job   *jobs.State `json:"job"`
	} `json:"error"`
}

func expectError(t *testing.T, resp *http.Response, status int, code string) errorBody {
	t.Helper()
	if resp.StatusCode != status {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, want %d: %s", resp.StatusCode, status, b)
	}
	e := sdktest.DecodeJSON[errorBody](t, resp)
	if e.Error.Code != code {
		t.Fatalf("code %q, want %q", e.Error.Code, code)
	}
	return e
}

func TestStatus(t *testing.T) {
	f := newFixture(t, map[string]string{"var/cache/apk/APKINDEX.0a1b2c3d.tar.gz": "x"}, nil)
	checked := testNow.Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(f.root, "var/cache/apk/APKINDEX.0a1b2c3d.tar.gz"), checked, checked)
	resp := f.do(t, "GET", "/status", "", alice)
	st := sdktest.DecodeJSON[Status](t, resp)
	if st.Release != "3.23.6" || st.Branch != "v3.23" || st.Arch != "x86_64" || st.Mode != host.Sys || st.Reboot {
		t.Fatalf("status = %+v", st)
	}
	if st.Checked == nil || !st.Checked.Equal(checked) {
		t.Fatalf("checked = %v", st.Checked)
	}
	kinds := []string{}
	for _, b := range st.Banners {
		kinds = append(kinds, b.Kind)
	}
	if strings.Join(kinds, ",") != "repo_eol,new_release" {
		t.Fatalf("banners = %+v", st.Banners)
	}
	if st.Resume != nil || st.Current != nil || st.Last != nil {
		t.Fatalf("status = %+v", st)
	}
}

// The next stable branch has ended: /status says so (the page offers no release upgrade then), in the shape the UI reads.
func TestStatusNextReleaseEnded(t *testing.T) {
	f := newFixture(t, nil, nil)
	write(t, f.api.Env.Base, map[string]string{"cache/releases.json": `{"latest_stable":"v3.24","release_branches":[
 {"rel_branch":"edge","repos":[{"name":"main"}]},
 {"rel_branch":"v3.24","eol_date":"2026-09-01","repos":[{"name":"main"}],"releases":[{"version":"3.24.0","date":"2026-05-20"}]},
 {"rel_branch":"v3.23","eol_date":"2027-11-01","repos":[{"name":"main"}],"releases":[{"version":"3.23.6","date":"2026-09-17"}]}]}`})
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(f.do(t, "GET", "/status", "", alice).Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if want := `[{"kind":"next_release_ended","branch":"v3.24","date":"2026-09-01"}]`; string(raw["banners"]) != want {
		t.Fatalf("banners = %s, want %s", raw["banners"], want)
	}
}

// The page gates the restart on the live bootStale of /status, not on the post-checks of an old job: a restart is
// pending (6.12.2 installed, 6.12.1 runs) and the boot files decide.
func TestStatusBootStale(t *testing.T) {
	f := newFixture(t, map[string]string{
		"usr/share/kernel/lts/kernel.release":  "6.12.2-0-lts\n",
		"lib/modules/6.12.1-0-lts/modules.dep": "",
		"lib/modules/6.12.2-0-lts/modules.dep": "",
	}, nil)
	touch := func(rel string, at time.Time) {
		if err := os.Chtimes(filepath.Join(f.root, rel), at, at); err != nil {
			t.Fatal(err)
		}
	}
	// the boot files are judged against the ctime of kernel.release, the moment apk wrote it: nothing can set it
	var st0 syscall.Stat_t
	if err := syscall.Stat(filepath.Join(f.root, "usr/share/kernel/lts/kernel.release"), &st0); err != nil {
		t.Fatal(err)
	}
	released := time.Unix(int64(st0.Ctim.Sec), int64(st0.Ctim.Nsec))
	status := func() Status { return sdktest.DecodeJSON[Status](t, f.do(t, "GET", "/status", "", alice)) }
	if st := status(); !st.Reboot || !st.BootStale {
		t.Fatalf("a restart is pending and /boot has no kernel files: %+v", st)
	}
	write(t, f.root, map[string]string{"boot/initramfs-lts": "x"})
	touch("boot/initramfs-lts", released.Add(-time.Minute))
	write(t, f.root, map[string]string{"boot/vmlinuz-lts": "x"})
	if st := status(); !st.Reboot || !st.BootStale {
		t.Fatalf("an initramfs older than the kernel: %+v", st)
	}
	touch("boot/initramfs-lts", released.Add(time.Minute)) // the user ran mkinitfs
	if st := status(); !st.Reboot || st.BootStale {
		t.Fatalf("fresh boot files clear it: %+v", st)
	}
	touch("boot/initramfs-lts", released.Add(-time.Minute))
	f.api.Env.Uname = func() string { return "6.12.2-0-lts" } // after the restart
	if st := status(); st.Reboot || st.BootStale {
		t.Fatalf("nothing is pending after the restart: %+v", st)
	}
	var raw map[string]json.RawMessage
	_ = json.NewDecoder(f.do(t, "GET", "/status", "", alice).Body).Decode(&raw)
	if string(raw["bootStale"]) != "false" {
		t.Fatalf("bootStale is always in the answer: %s", raw["bootStale"])
	}
}

func TestStatusWithoutMetadata(t *testing.T) {
	f := newFixture(t, nil, nil)
	_ = os.Remove(filepath.Join(f.api.Env.Base, "cache/releases.json"))
	resp := f.do(t, "GET", "/status", "", alice)
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["banners"]) != "[]" || raw["checked"] != nil {
		t.Fatalf("status = %s", raw)
	}
}

func TestStatusShowsJobsAndResume(t *testing.T) {
	f := newFixture(t, map[string]string{"etc/apk/repositories": strings.ReplaceAll(repos323, "v3.23", "v3.24")}, nil)
	done, _ := f.job(t, "release", func(st *jobs.State) { st.Status, st.Target = jobs.Partial, "v3.24" })
	st := sdktest.DecodeJSON[Status](t, f.do(t, "GET", "/status", "", alice))
	if st.Resume == nil || st.Resume.Target != "v3.24" || st.Last == nil || st.Last.ID != done.ID || st.Current != nil {
		t.Fatalf("status = %+v", st)
	}
	running, _ := f.job(t, "upgrade", markRunning)
	st = sdktest.DecodeJSON[Status](t, f.do(t, "GET", "/status", "", alice))
	if st.Current == nil || st.Current.ID != running.ID || st.Resume != nil {
		t.Fatalf("status while a job runs = %+v", st)
	}
}

func TestUpdates(t *testing.T) {
	f := newFixture(t, nil, nil)
	write(t, f.api.Env.Base, map[string]string{
		"cache/upgradable.txt": "curl-8.16.0-r1 x86_64 {curl} (curl) [upgradable from: curl-8.16.0-r0]\n" +
			"linux-lts-6.12.2-r0 x86_64 {linux-lts} (GPL-2.0-only) [upgradable from: linux-lts-6.12.1-r0]\n",
		"cache/secdb-v3.23-community.json": `{"packages":[{"pkg":{"name":"curl","secfixes":{"8.16.0-r1":["CVE-2026-2222 ALPINE-15000"]}}}]}`,
		"cache/secdb-v3.23-main.json":      `{"packages":[]}`,
	})
	u := sdktest.DecodeJSON[Updates](t, f.do(t, "GET", "/updates", "", alice))
	if u.Checked == nil || !u.SecurityData || len(u.Items) != 2 {
		t.Fatalf("updates = %+v", u)
	}
	curl, kernel := u.Items[0], u.Items[1]
	if curl.Name != "curl" || curl.Old != "8.16.0-r0" || curl.New != "8.16.0-r1" || curl.Reboot {
		t.Fatalf("curl = %+v", curl)
	}
	want := []Advisory{{ID: "ALPINE-15000"}, {ID: "CVE-2026-2222", Link: "https://security.alpinelinux.org/vuln/CVE-2026-2222"}}
	if len(curl.Security) != 2 || curl.Security[0] != want[0] || curl.Security[1] != want[1] {
		t.Fatalf("security = %+v", curl.Security)
	}
	if !kernel.Reboot || len(kernel.Security) != 0 {
		t.Fatalf("linux-lts = %+v", kernel)
	}
}

func TestUpdatesWithoutCache(t *testing.T) {
	f := newFixture(t, nil, nil)
	resp := f.do(t, "GET", "/updates", "", alice)
	var raw map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	if string(raw["items"]) != "[]" || string(raw["securityData"]) != "false" || raw["checked"] != nil {
		t.Fatalf("updates = %s", raw)
	}
}

func TestReleases(t *testing.T) {
	f := newFixture(t, nil, nil)
	r := sdktest.DecodeJSON[Releases](t, f.do(t, "GET", "/releases", "", alice))
	if !r.Available || r.Current != "v3.23" || r.Latest != "v3.24" || len(r.Targets) != 2 || r.Targets[0].Branch != "v3.24" || !r.Targets[1].Edge {
		t.Fatalf("releases = %+v", r)
	}
}

func TestReleasesNextBranchEnded(t *testing.T) {
	// v3.24 is the next branch of v3.23 and has ended: no stable target (a jump to a later branch is not offered),
	// but the page still learns the newest stable branch, to tell why
	f := newFixture(t, nil, nil)
	ended := strings.Replace(releasesJSON, `"eol_date":"2028-06-01"`, `"eol_date":"2026-01-01"`, 1)
	write(t, f.api.Env.Base, map[string]string{"cache/releases.json": ended})
	r := sdktest.DecodeJSON[Releases](t, f.do(t, "GET", "/releases", "", alice))
	if !r.Available || r.Current != "v3.23" || r.Latest != "v3.24" || len(r.Targets) != 1 || !r.Targets[0].Edge {
		t.Fatalf("releases = %+v", r)
	}
}

func TestReleasesWithoutCache(t *testing.T) {
	f := newFixture(t, nil, nil)
	_ = os.Remove(filepath.Join(f.api.Env.Base, "cache/releases.json"))
	resp := f.do(t, "GET", "/releases", "", alice)
	var raw map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	if string(raw["available"]) != "false" || string(raw["targets"]) != "[]" {
		t.Fatalf("releases = %s", raw)
	}
}

func TestAdminOnly(t *testing.T) {
	f := newFixture(t, nil, nil)
	st, _ := f.job(t, "refresh", func(st *jobs.State) { st.Status = jobs.Done })
	for _, r := range []struct{ method, path, body string }{
		{"POST", "/refresh", ""},
		{"POST", "/preview", `{"kind":"upgrade"}`},
		{"POST", "/jobs", `{"kind":"resume"}`},
		{"GET", "/jobs/" + st.ID + "/log", ""},
	} {
		expectError(t, f.do(t, r.method, r.path, r.body, alice), 403, "admin_required")
	}
	if len(f.launcher.dirs) != 0 {
		t.Fatal("a user started a job")
	}
}
