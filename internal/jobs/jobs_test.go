package jobs

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
)

// fakeStep is one call of the fake apk: the lines it prints, the progress lines it writes to fd 3, files it writes
// below FAKE_ROOT (e.g. lib/apk/db/installed), how long it sleeps, the lines it prints after that, its exit status,
// and whether it lists its open fds with their targets.
type fakeStep struct {
	Lines    []string          `json:"lines"`
	Progress []string          `json:"progress"`
	Write    map[string]string `json:"write"`
	Sleep    string            `json:"sleep"`
	After    []string          `json:"after"`
	Exit     int               `json:"exit"`
	ListFDs  bool              `json:"listFds"`
}

func TestMain(m *testing.M) {
	switch {
	case os.Getenv("FAKE_APK") == "1":
		os.Exit(fakeAPK())
	case len(os.Args) == 3 && os.Args[1] == "--run-job":
		os.Exit(RunJob(envConfig(), os.Args[2]))
	case os.Getenv("FAKE_PARENT") == "1":
		os.Exit(fakeParent())
	}
	os.Exit(m.Run())
}

// fakeAPK plays the next step of FAKE_SCRIPT (a JSON list of fakeStep); FAKE_COUNTER counts the calls and keeps
// the arguments of each.
func fakeAPK() int {
	var steps []fakeStep
	data, _ := os.ReadFile(os.Getenv("FAKE_SCRIPT"))
	_ = json.Unmarshal(data, &steps)
	counter := os.Getenv("FAKE_COUNTER")
	n, _ := os.ReadFile(counter)
	i, _ := strconv.Atoi(strings.TrimSpace(string(n)))
	_ = os.WriteFile(counter, []byte(strconv.Itoa(i+1)), 0o644)
	args, _ := json.Marshal(os.Args[1:])
	_ = os.WriteFile(counter+".args."+strconv.Itoa(i), args, 0o644)
	if i >= len(steps) {
		return 0
	}
	s := steps[i]
	if len(s.Progress) > 0 {
		progress := os.NewFile(3, "progress")
		for _, p := range s.Progress {
			fmt.Fprintln(progress, p)
		}
	}
	for _, l := range s.Lines {
		fmt.Println(l)
	}
	for p, c := range s.Write {
		full := filepath.Join(os.Getenv("FAKE_ROOT"), p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(c), 0o644)
	}
	if s.ListFDs {
		entries, _ := os.ReadDir("/proc/self/fd")
		for _, e := range entries {
			target, _ := os.Readlink("/proc/self/fd/" + e.Name())
			fmt.Printf("fd %s -> %s\n", e.Name(), target)
		}
	}
	if d, err := time.ParseDuration(s.Sleep); err == nil {
		time.Sleep(d)
	}
	for _, l := range s.After {
		fmt.Println(l)
	}
	return s.Exit
}

// fakeParent stands in for the extension: like procman's listener, FAKE_LEAK is open without close-on-exec when it
// launches the runner of FAKE_DIR. Then it waits to be killed with its process group.
func fakeParent() int {
	if leak := os.Getenv("FAKE_LEAK"); leak != "" {
		if _, err := syscall.Open(leak, syscall.O_RDONLY, 0); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if err := (ExecLauncher{Binary: os.Args[0], Env: os.Environ()}).Launch(os.Getenv("FAKE_DIR")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	time.Sleep(time.Minute)
	return 0
}

// envConfig is the runner configuration of a test runner process, from the environment testEnv sets. Only the apk
// children are fakes (FAKE_APK=1 is appended; the last value of a variable wins).
func envConfig() RunnerConfig {
	return RunnerConfig{
		Base: os.Getenv("FAKE_BASE"), LockPath: os.Getenv("FAKE_LOCK"), APK: os.Args[0],
		Env: append(os.Environ(), "FAKE_APK=1"), Now: time.Now, Proc: host.New(), Kinds: testKinds(),
	}
}

// testKinds: "script" runs one apk step per entry of the spec's Target ("a|b" → steps a and b) and stops at the
// first failing one.
func testKinds() map[string]Kind {
	return map[string]Kind{"script": func(r *Run) error {
		for _, name := range strings.Split(r.Spec.Target, "|") {
			exit, _, err := r.APK(name, "fake", name)
			if err != nil {
				return err
			}
			if exit != 0 {
				r.Set(func(s *State) { s.Status = Partial })
				return nil
			}
		}
		return nil
	}}
}

// testEnv prepares base, lock, root and a fake apk script, and sets the environment that runner processes read
// (t.Setenv: child processes inherit it). FAKE_APK is set empty, so the test binary itself is no fake.
func testEnv(t *testing.T, steps []fakeStep) (cfg RunnerConfig, counter string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "script.json")
	data, _ := json.Marshal(steps)
	if err := os.WriteFile(script, data, 0o644); err != nil {
		t.Fatal(err)
	}
	counter = filepath.Join(dir, "counter")
	for k, v := range map[string]string{
		"FAKE_APK": "", "FAKE_SCRIPT": script, "FAKE_COUNTER": counter, "FAKE_LOCK": filepath.Join(dir, "lock"),
		"FAKE_BASE": filepath.Join(dir, "base"), "FAKE_ROOT": filepath.Join(dir, "root"),
	} {
		t.Setenv(k, v)
	}
	if err := os.MkdirAll(filepath.Join(dir, "root"), 0o755); err != nil {
		t.Fatal(err)
	}
	return envConfig(), counter
}

// waitFor polls ok every 50 ms until it holds, or fails the test after d.
func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// runDetached runs a script job the way the extension does: a parent process launches the runner and is then killed
// with its whole process group, as procman stops an extension. It returns the job's final state and log, and when the
// parent was killed.
func runDetached(t *testing.T, cfg RunnerConfig, target string, env ...string) (*State, string, time.Time) {
	t.Helper()
	s := Store{Base: cfg.Base, Proc: cfg.Proc}
	id, dir, err := s.Create(Spec{Kind: "script", Target: target, User: "admin"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(os.Args[0])
	parent.Env = append(append(os.Environ(), "FAKE_PARENT=1", "FAKE_DIR="+dir), env...)
	parent.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-parent.Process.Pid, syscall.SIGKILL) })
	waitFor(t, 5*time.Second, "the runner to start", func() bool {
		st, _ := s.Get(id)
		return st != nil && st.RunnerPID != 0 && (st.APKPID != 0 || st.Finished())
	})
	_ = syscall.Kill(-parent.Process.Pid, syscall.SIGKILL)
	_ = parent.Wait()
	killed := time.Now()
	var st *State
	waitFor(t, 10*time.Second, "the job to end", func() bool {
		st, _ = s.Get(id)
		return st != nil && st.Finished()
	})
	log, _ := os.ReadFile(filepath.Join(dir, "log"))
	return st, string(log), killed
}

func dirSize(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
