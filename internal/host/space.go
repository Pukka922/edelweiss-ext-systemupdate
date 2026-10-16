package host

import "golang.org/x/sys/unix"

// Statfs returns the total and the free (available to unprivileged users) bytes of the filesystem holding path.
type Statfs func(path string) (total, free uint64, err error)

// DiskSpace is the real Statfs.
func DiskSpace(path string) (total, free uint64, err error) {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return 0, 0, err
	}
	return s.Blocks * uint64(s.Bsize), s.Bavail * uint64(s.Bsize), nil
}
