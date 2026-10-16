package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
)

func runSync(t *testing.T, cfg RunnerConfig, target string) (*State, string) {
	t.Helper()
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	id, dir, err := s.Create(Spec{Kind: "script", Target: target, User: "admin"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if code := RunJob(cfg, dir); code != 0 {
		t.Fatalf("RunJob = %d", code)
	}
	st, _ := s.Get(id)
	log, _ := os.ReadFile(filepath.Join(dir, "log"))
	return st, string(log)
}

func TestRunJobSteps(t *testing.T) {
	cfg, counter := testEnv(t, []fakeStep{
		{Lines: []string{"(1/2) Upgrading a (1-r0 -> 2-r0)"}},
		{Lines: []string{"OK: 1 MiB in 2 packages"}, Progress: []string{"1/2 install", "2/2 install"}},
	})
	st, log := runSync(t, cfg, "first|second")
	if st.Status != Done || len(st.Steps) != 2 || st.Steps[0].Name != "first" || st.Steps[1].Exit != 0 {
		t.Fatalf("state = %+v", st)
	}
	if st.RunnerPID != os.Getpid() || st.BootID == "" || st.Started.IsZero() || st.Ended.IsZero() || st.APKPID != 0 {
		t.Fatalf("state = %+v", st)
	}
	if st.Progress == nil || st.Progress.Cur != 2 || st.Progress.Max != 2 {
		t.Fatalf("progress = %+v", st.Progress)
	}
	if st.Current != "(1/2) Upgrading a (1-r0 -> 2-r0)" {
		t.Fatalf("current = %q", st.Current)
	}
	for _, want := range []string{"== first: ", "(1/2) Upgrading a", "== second: ", "OK: 1 MiB", "== finished: done"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log lacks %q:\n%s", want, log)
		}
	}
	args, _ := os.ReadFile(counter + ".args.0")
	if string(args) != `["--progress-fd","3","fake","first"]` {
		t.Fatalf("apk args = %s", args)
	}
	if fi, _ := os.Stat(filepath.Join(cfg.Base, "jobs", st.ID, "log")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("log mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(cfg.Base, "jobs", st.ID, "progress")); fi.Mode().Perm() != 0o644 {
		t.Fatalf("progress mode %v", fi.Mode())
	}
}

func TestRunJobCurrentWithPaddedCounter(t *testing.T) {
	// apk-tools 3 pads the counter to the width of the total
	cfg, _ := testEnv(t, []fakeStep{{Lines: []string{"(  1/400) Upgrading a (1-r0 -> 2-r0)", "( 99/400) Upgrading b (1-r0 -> 2-r0)"}}})
	st, _ := runSync(t, cfg, "only")
	if st.Current != "( 99/400) Upgrading b (1-r0 -> 2-r0)" {
		t.Fatalf("current = %q", st.Current)
	}
}

func TestRunJobFailingStep(t *testing.T) {
	cfg, _ := testEnv(t, []fakeStep{{Exit: 2, Lines: []string{"ERROR: x"}}})
	st, _ := runSync(t, cfg, "only|never")
	if st.Status != Partial || len(st.Steps) != 1 || st.Steps[0].Exit != 2 || st.Steps[0].Errors != 2 {
		t.Fatalf("state = %+v", st)
	}
}

func TestRunJobUnknownKind(t *testing.T) {
	cfg, _ := testEnv(t, nil)
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	id, dir, _ := s.Create(Spec{Kind: "nope"}, time.Now())
	RunJob(cfg, dir)
	if st, _ := s.Get(id); st.Status != Failed || !strings.Contains(st.Error, "unknown kind") {
		t.Fatalf("state = %+v", st)
	}
}

func TestRunJobRefusesASecondJob(t *testing.T) {
	cfg, _ := testEnv(t, nil)
	held, err := Lock(cfg.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	id, dir, _ := s.Create(Spec{Kind: "script", Target: "a"}, time.Now())
	if code := RunJob(cfg, dir); code == 0 {
		t.Fatal("RunJob ran while the lock was held")
	}
	if st, _ := s.Get(id); st.Status != Failed || !strings.Contains(st.Error, "another job") || len(st.Steps) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestRunJobChecksTheDirectory(t *testing.T) {
	cfg, _ := testEnv(t, nil)
	if code := RunJob(cfg, t.TempDir()); code == 0 {
		t.Fatal("a directory outside the base was accepted")
	}
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	_, dir, _ := s.Create(Spec{Kind: "script", Target: "a"}, time.Now())
	link := filepath.Join(cfg.Base, "jobs", "20261013T120000.000-00000000")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if code := RunJob(cfg, link); code == 0 {
		t.Fatal("a symlink was accepted")
	}
	if os.Geteuid() != 0 {
		cfg.RequireRoot = true
		if code := RunJob(cfg, dir); code == 0 {
			t.Fatal("RequireRoot accepted a runner that is not root")
		}
	}
}

func TestPruneRunsAtTheStart(t *testing.T) {
	cfg, _ := testEnv(t, []fakeStep{{}})
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	finishedJobs(t, s, 21, 10)
	st, _ := runSync(t, cfg, "a")
	list, _ := s.List(30)
	if len(list) != 20 || list[0].ID != st.ID {
		t.Fatalf("the newest 20 jobs stay, the new one among them: %d jobs, newest %s", len(list), list[0].ID)
	}
}

func TestWaitingShowsInTheState(t *testing.T) {
	cfg, _ := testEnv(t, []fakeStep{{Lines: []string{"Waiting for repository lock"}, Sleep: "2500ms", After: []string{"OK: 1 MiB"}}})
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	id, dir, _ := s.Create(Spec{Kind: "script", Target: "wait"}, time.Now())
	done := make(chan int)
	go func() { done <- RunJob(cfg, dir) }()
	waitFor(t, 5*time.Second, "waiting in the state", func() bool {
		st, _ := s.Get(id)
		return st != nil && st.Waiting
	})
	<-done
	if st, _ := s.Get(id); st.Waiting || st.LockHolder != nil || st.Status != Done {
		t.Fatalf("after the step: %+v", st)
	}
}

// The runner survives the kill of the extension's whole process group, as procman does it.
func TestRunnerSurvivesTheParentsProcessGroup(t *testing.T) {
	cfg, _ := testEnv(t, []fakeStep{{Sleep: "1500ms", After: []string{"slow step done"}}})
	st, log, killed := runDetached(t, cfg, "slow")
	if st.Status != Done || !strings.Contains(log, "slow step done") || !st.Ended.After(killed) {
		t.Fatalf("state = %+v, killed %v, log:\n%s", st, killed, log)
	}
}

// Neither the runner nor apk inherits a descriptor of the extension (procman passes the listener without CLOEXEC);
// apk sees its progress file as fd 3.
func TestNoDescriptorLeaks(t *testing.T) {
	cfg, _ := testEnv(t, []fakeStep{{ListFDs: true}})
	leak := filepath.Join(t.TempDir(), "listener")
	if err := os.WriteFile(leak, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st, log, _ := runDetached(t, cfg, "fds", "FAKE_LEAK="+leak)
	if st.Status != Done {
		t.Fatalf("state = %+v", st)
	}
	if strings.Contains(log, leak) {
		t.Fatalf("apk inherited the extension's descriptor:\n%s", log)
	}
	if !strings.Contains(log, "fd 3 -> "+filepath.Join(cfg.Base, "jobs", st.ID, "progress")) {
		t.Fatalf("the progress file is not fd 3:\n%s", log)
	}
	for _, std := range []string{"fd 0 -> /dev/null", "fd 1 -> " + filepath.Join(cfg.Base, "jobs", st.ID, "log")} {
		if !strings.Contains(log, std) {
			t.Fatalf("log lacks %q:\n%s", std, log)
		}
	}
}

func TestRedact(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"user and password":         {"fetch https://myUser:s3cret@repo.example.org/alpine/v3.24/main", "fetch https://***@repo.example.org/alpine/v3.24/main"},
		"http and https":            {"http://a:b@x/ and https://c:d@y/", "http://***@x/ and https://***@y/"},
		"no credentials":            {"https://dl-cdn.alpinelinux.org/alpine/v3.24/main", "https://dl-cdn.alpinelinux.org/alpine/v3.24/main"},
		"token only":                {"https://ghp_TOKEN123@repo.example.org/alpine/main", "https://***@repo.example.org/alpine/main"},
		"token only without path":   {"fetch https://TOKEN@host", "fetch https://***@host"},
		"empty user":                {"https://:s3cret@host/alpine", "https://***@host/alpine"},
		"upper-case scheme":         {"HTTPS://u:p@h/x and Http://tok@h2/y", "HTTPS://***@h/x and Http://***@h2/y"},
		"at sign in the password":   {"https://u:p@ss@host/x", "https://***@host/x"},
		"percent-encoded password":  {"https://u:p%40ss@host/x", "https://***@host/x"},
		"in a command line":         {"apk add https://u:p@host/pkg.apk --force", "apk add https://***@host/pkg.apk --force"},
		"a tag":                     {"@testing https://dl-cdn.alpinelinux.org/alpine/edge/testing", "@testing https://dl-cdn.alpinelinux.org/alpine/edge/testing"},
		"tag after the url":         {"https://dl-cdn.alpinelinux.org/alpine/edge/main @edge", "https://dl-cdn.alpinelinux.org/alpine/edge/main @edge"},
		"a tag word":                {"apk add curl@edge vim@testing", "apk add curl@edge vim@testing"},
		"an e-mail address":         {"mail me@example.org or https://example.org/me@example.org", "mail me@example.org or https://example.org/me@example.org"},
		"an @ in the query":         {"https://example.org?mail=me@example.org and https://example.org#me@x", "https://example.org?mail=me@example.org and https://example.org#me@x"},
		"another scheme":            {"ssh://git@example.org/x", "ssh://git@example.org/x"},
		"nothing":                   {"", ""},
		"a scheme alone":            {"https://", "https://"},
		"an at sign after the host": {"https://host @tag", "https://host @tag"},
	} {
		if got := Redact(c.in); got != c.want {
			t.Errorf("%s: Redact(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestClip(t *testing.T) {
	if got := clip("short", 200); got != "short" {
		t.Fatalf("short: %q", got)
	}
	if got := clip(strings.Repeat("a", 200), 200); got != strings.Repeat("a", 200) {
		t.Fatalf("exactly the limit is not cut: %d bytes", len(got))
	}
	if got := clip(strings.Repeat("a", 201), 200); got != strings.Repeat("a", 200)+"…" {
		t.Fatalf("one over the limit: %d bytes", len(got))
	}
	// "é" is two bytes: a cut in the middle of one backs off to its start
	if got, want := clip(strings.Repeat("é", 150), 200), strings.Repeat("é", 100)+"…"; got != want {
		t.Fatalf("rune boundary: %q", got)
	}
	got := clip("a"+strings.Repeat("é", 150), 200) // byte 200 is the first half of a rune
	if want := "a" + strings.Repeat("é", 99) + "…"; got != want || !utf8.ValidString(got) {
		t.Fatalf("rune boundary: %q", got)
	}
}

func TestSafeHolder(t *testing.T) {
	if safeHolder(nil) != nil {
		t.Fatal("no holder stays none")
	}
	in := &host.Process{PID: 1234, Name: "apk add https://u:s3cret@repo.example.org/x " + strings.Repeat("y", 400)}
	got := safeHolder(in)
	if got.PID != 1234 || strings.Contains(got.Name, "s3cret") || !strings.HasPrefix(got.Name, "apk add https://***@repo.example.org/x y") ||
		!strings.HasSuffix(got.Name, "…") || len(got.Name) > 200+len("…") {
		t.Fatalf("holder = %q (%d bytes)", got.Name, len(got.Name))
	}
	if !strings.Contains(in.Name, "s3cret") {
		t.Fatal("the original is not changed")
	}
}

// The name of the process that holds apk's lock goes into state.json (world-readable), /status and the event stream:
// it is cleaned of credentials and cut short first.
func TestWaitingStoresARedactedBoundedLockHolder(t *testing.T) {
	cfg, _ := testEnv(t, []fakeStep{{Lines: []string{"Waiting for repository lock"}, Sleep: "2500ms", After: []string{"OK: 1 MiB"}}})
	proc := t.TempDir()
	writeTree(t, proc, map[string]string{
		"lib/apk/db/lock":   "",
		"proc/4242/cmdline": "apk\x00add\x00https://u:s3cret@repo.example.org/x\x00" + strings.Repeat("v", 500) + "\x00",
	})
	var st syscall.Stat_t
	if err := syscall.Stat(filepath.Join(proc, "lib/apk/db/lock"), &st); err != nil {
		t.Fatal(err)
	}
	writeTree(t, proc, map[string]string{"proc/locks": fmt.Sprintf("1: FLOCK  ADVISORY  WRITE 4242 08:01:%d 0 EOF\n", st.Ino)})
	cfg.Proc = host.FS{Root: proc}
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	_, dir, _ := s.Create(Spec{Kind: "script", Target: "wait"}, time.Now())
	done := make(chan int)
	go func() { done <- RunJob(cfg, dir) }()
	var holder *host.Process
	waitFor(t, 5*time.Second, "the lock holder in the state", func() bool {
		cur, err := ReadState(dir) // not Store.Get: the fake /proc knows no runner
		if err == nil && cur.Waiting && cur.LockHolder != nil {
			holder = cur.LockHolder
			return true
		}
		return false
	})
	<-done
	if holder.PID != 4242 || strings.Contains(holder.Name, "s3cret") || !strings.HasPrefix(holder.Name, "apk add https://***@repo.example.org/x v") ||
		!strings.HasSuffix(holder.Name, "…") || len(holder.Name) > 200+len("…") {
		t.Fatalf("holder = %q (%d bytes)", holder.Name, len(holder.Name))
	}
}
