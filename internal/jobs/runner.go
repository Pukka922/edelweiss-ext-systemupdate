package jobs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
)

// RunnerConfig configures RunJob. Proc is the real host, for pids, the boot id and apk's lock.
type RunnerConfig struct {
	Base, LockPath, APK string
	Env                 []string
	RequireRoot         bool
	Now                 func() time.Time
	Proc                host.FS
	Kinds               map[string]Kind
}

// Kind runs the steps of one kind of job. It sets a status other than Running when the job did not succeed; an
// error it returns makes the job Failed with that error.
type Kind func(r *Run) error

// Run is a running job.
type Run struct {
	Cfg     RunnerConfig
	Dir     string
	Spec    Spec
	id      string
	mu      sync.Mutex
	st      *State
	log     *os.File
	sigterm atomic.Bool
}

// credentials matches the userinfo of an http(s) URL, whatever it holds (user:password, a token alone, an empty user):
// everything between the scheme and the last @ of the authority, which ends at a /, a ?, a # or white space. The
// greedy match takes an @ inside the password along. Text without such a scheme (@tag words, e-mail addresses) is no
// match.
var credentials = regexp.MustCompile(`(?i)(https?://)[^/\s?#]*@`)

// Redact removes the credentials (user:password@, token@) from URLs.
func Redact(s string) string { return credentials.ReplaceAllString(s, "${1}***@") }

// maxHolderName bounds the name of the process that holds apk's lock: its whole command line is not for everyone.
const maxHolderName = 200

// clip shortens s to at most n bytes, cutting on a rune boundary, and appends "…" when it cut something.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// safeHolder is a copy of h fit to be stored in a state or a plan and shown to every user of the page: the command
// line of the process is cleaned of credentials in URLs and cut short.
func safeHolder(h *host.Process) *host.Process {
	if h == nil {
		return nil
	}
	c := *h
	c.Name = clip(Redact(c.Name), maxHolderName)
	return &c
}

// ID is the job's id.
func (r *Run) ID() string { return r.id }

// Set changes the state under the lock and saves it.
func (r *Run) Set(f func(*State)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(r.st)
	_ = WriteJSON(filepath.Join(r.Dir, "state.json"), r.st, 0o644)
}

// Note writes a line of the runner itself to the log. It is only called between two program runs, never while apk
// writes to the log.
func (r *Run) Note(format string, args ...any) {
	fmt.Fprintf(r.log, "== "+format+"\n", args...)
}

// APK runs apk with --progress-fd 3 and args as step: its output goes straight into the log, its progress into the
// progress file. It returns apk's exit status and what it printed.
func (r *Run) APK(step string, args ...string) (int, string, error) {
	progress, err := os.OpenFile(filepath.Join(r.Dir, "progress"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return -1, "", err
	}
	defer progress.Close()
	return r.run(step, r.Cfg.APK, append([]string{"--progress-fd", "3"}, args...), progress)
}

// Exec runs another program (lbu) as step, with its output in the log.
func (r *Run) Exec(step, path string, args ...string) (int, string, error) {
	return r.run(step, path, args, nil)
}

func (r *Run) run(step, path string, args []string, progress *os.File) (int, string, error) {
	r.Note("%s: %s %s", step, filepath.Base(path), Redact(strings.Join(args, " ")))
	start, err := r.log.Seek(0, io.SeekEnd)
	if err != nil {
		return -1, "", err
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = "/"
	cmd.Env = r.Cfg.Env
	cmd.Stdout, cmd.Stderr = r.log, r.log
	if progress != nil {
		cmd.ExtraFiles = []*os.File{progress} // fd 3
	}
	s := Step{Name: step, Started: r.Cfg.Now()}
	r.Set(func(st *State) { st.Phase, st.Progress, st.Current = step, nil, "" })
	if err := cmd.Start(); err != nil {
		s.Exit, s.Ended = -1, r.Cfg.Now()
		r.Set(func(st *State) { st.Steps = append(st.Steps, s) })
		r.Note("%s: %v", step, err)
		return -1, "", err
	}
	pid := cmd.Process.Pid
	ps, _ := r.Cfg.Proc.StartTime(pid)
	r.Set(func(st *State) { st.APKPID, st.APKStart = pid, ps })
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.watch(done)
	}()
	err = cmd.Wait()
	close(done)
	wg.Wait()
	exit := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		exit = ee.ExitCode() // -1 when a signal ended it
	case err != nil:
		exit = -1
	}
	out := r.since(start)
	s.Exit, s.Ended = exit, r.Cfg.Now()
	if o, n := apk.Classify(exit, out); o == apk.Partial {
		s.Errors = n
	}
	r.Set(func(st *State) {
		st.Steps = append(st.Steps, s)
		st.APKPID, st.APKStart = 0, 0
		r.update(st)
		st.Waiting, st.LockHolder = false, nil
	})
	r.noteSigterm()
	return exit, out, nil
}

func (r *Run) noteSigterm() {
	if r.sigterm.Swap(false) {
		r.Note("SIGTERM received and ignored: the job continues")
	}
}

// since returns what the log got after offset.
func (r *Run) since(offset int64) string {
	f, err := os.Open(r.log.Name())
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, _ := io.ReadAll(f)
	return string(data)
}

// changeLine matches a change line of apk; apk-tools 3 pads the counter to the width of the total: "(  1/400) …".
var changeLine = regexp.MustCompile(`^\(\s*\d+/\d+\) .+$`)

// update puts the last progress line, the last change line of the log and whether apk waits for its database lock
// (and who holds it) into the state.
func (r *Run) update(st *State) {
	if data, err := os.ReadFile(filepath.Join(r.Dir, "progress")); err == nil {
		if p, ok := apk.LastProgress(data); ok {
			st.Progress = &p
		}
	}
	lines := strings.Split(strings.TrimRight(tailOf(r.log.Name(), 4096), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if changeLine.MatchString(lines[i]) {
			st.Current = Redact(lines[i])
			break
		}
	}
	st.Waiting = st.APKPID != 0 && strings.Contains(lines[len(lines)-1], apk.LockWait)
	st.LockHolder = nil
	if st.Waiting {
		if h, err := r.Cfg.Proc.LockHolder(); err == nil && h != nil && h.PID != st.APKPID {
			st.LockHolder = safeHolder(h)
		}
	}
}

func tailOf(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
	}
	data, _ := io.ReadAll(f)
	return string(data)
}

// watch updates the state every second while a step runs (the throttled "current" of the spec).
func (r *Run) watch(done <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			r.Set(r.update)
		}
	}
}

// checkDir accepts only a real directory (no symlink) directly below Base/jobs, owned by root when RequireRoot, and a
// runner running as root when RequireRoot.
func (cfg RunnerConfig) checkDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if filepath.Dir(abs) != filepath.Join(cfg.Base, "jobs") || !ValidID(filepath.Base(abs)) {
		return errors.New("runner: not a job directory")
	}
	fi, err := os.Lstat(abs)
	if err != nil || !fi.IsDir() {
		return errors.New("runner: not a job directory")
	}
	if cfg.RequireRoot {
		if os.Geteuid() != 0 {
			return errors.New("runner: must run as root")
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
			return errors.New("runner: job directory not owned by root")
		}
	}
	return nil
}

// escapeCgroup moves the runner out of the cgroup of the OpenRC service edelweiss, whose cleanup would kill it with
// edelweissd. Best effort: on failure the runner stays where it is.
func escapeCgroup() {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil || !strings.Contains(string(data), "openrc.edelweiss") {
		return
	}
	dir := "/sys/fs/cgroup/edelweiss-systemupdate"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(fmt.Sprint(os.Getpid())), 0o644)
}

// RunJob is the runner (the binary started with --run-job <dir>). It takes the lock, prunes old jobs, runs the job's
// kind and records the result. It returns the process exit status: 0 when the job ran (whatever its result).
func RunJob(cfg RunnerConfig, dir string) int {
	syscall.Umask(0o022)
	signal.Ignore(syscall.SIGHUP) // apk inherits it, like under nohup
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	if err := cfg.checkDir(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	st, err := ReadState(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fail := func(msg string) {
		st.Status, st.Error, st.Ended = Failed, msg, cfg.Now()
		_ = WriteJSON(filepath.Join(dir, "state.json"), st, 0o644)
	}
	lock, err := Lock(cfg.LockPath)
	if err != nil {
		fail("another job runs")
		return 3
	}
	defer lock.Close()
	escapeCgroup()
	spec, err := ReadSpec(dir)
	if err != nil {
		fail(err.Error())
		return 2
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		fail(err.Error())
		return 2
	}
	defer logFile.Close()
	r := &Run{Cfg: cfg, Dir: dir, Spec: *spec, id: st.ID, st: st, log: logFile}
	go func() {
		for range term {
			r.sigterm.Store(true) // noted between two steps: Note never writes while apk does
		}
	}()
	start, _ := cfg.Proc.StartTime(os.Getpid())
	r.Set(func(s *State) {
		s.Status, s.Started = Running, cfg.Now()
		s.RunnerPID, s.RunnerStart, s.BootID = os.Getpid(), start, cfg.Proc.BootID()
	})
	if err := (Store{Base: cfg.Base, Proc: cfg.Proc}).Prune(20, 64<<20); err != nil {
		r.Note("cleaning up old jobs: %v", err)
	}
	if kind := cfg.Kinds[spec.Kind]; kind == nil {
		err = fmt.Errorf("unknown kind %q", spec.Kind)
	} else {
		err = kind(r)
	}
	r.noteSigterm()
	r.mu.Lock()
	final := r.st.Status
	r.mu.Unlock()
	switch {
	case err != nil && final == Running:
		final = Failed
	case final == Running:
		final = Done
	}
	if err != nil {
		r.Note("error: %s", Redact(err.Error()))
	}
	// the last lines go into the log before the state says the job ended: a stream that sees the end has all of it
	r.Note("finished: %s", final)
	r.Set(func(s *State) {
		if err != nil && s.Error == "" {
			s.Error = Redact(err.Error())
		}
		s.Status, s.Ended, s.Phase, s.Waiting, s.LockHolder = final, cfg.Now(), "", false, nil
	})
	return 0
}
