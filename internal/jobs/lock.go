package jobs

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// ErrBusy means another job holds the lock.
var ErrBusy = errors.New("jobs: another job runs")

// Lock takes the job lock (flock, exclusive, non-blocking). Go opens files with O_CLOEXEC, so apk never inherits it.
func Lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return f, nil
}

// Locked reports whether a job holds the lock. A missing lock file is free; the check does not create it.
func Locked(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.Is(err, unix.EWOULDBLOCK)
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return false
}
