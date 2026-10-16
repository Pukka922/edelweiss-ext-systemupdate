package jobs

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
)

// Launcher starts the runner of a job directory.
type Launcher interface {
	Launch(dir string) error
}

// ExecLauncher starts Binary --run-job <dir> in a new session (Setsid; no Pdeathsig), so that stopping the
// extension's process group – admin mode off, session expiry, extension update, edelweissd stop – does not stop the
// job. Env is the runner's whole environment (apk.Env if nil). The extension uses /proc/self/exe as Binary: the
// inode it runs from, even after an update renamed a new binary into its place.
type ExecLauncher struct {
	Binary string
	Env    []string
}

func (l ExecLauncher) Launch(dir string) error {
	cloexecFrom3()
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	env := l.Env
	if env == nil {
		env = apk.Env
	}
	cmd := exec.Command(l.Binary, "--run-job", dir)
	cmd.Dir = "/"
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap the runner, should it end while the extension still runs
	return nil
}

// cloexecFrom3 marks every fd from 3 on close-on-exec. procman hands the extension its listener as fd 3 without
// CLOEXEC, and neither the runner nor apk nor a service a package script restarts may inherit it.
func cloexecFrom3() {
	if err := unix.CloseRange(3, ^uint(0), unix.CLOSE_RANGE_CLOEXEC); err == nil {
		return
	}
	entries, _ := os.ReadDir("/proc/self/fd")
	for _, e := range entries {
		if fd, err := strconv.Atoi(e.Name()); err == nil && fd >= 3 {
			_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
		}
	}
}
