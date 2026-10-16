package host

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// APKNew lists the configuration files apk left as *.apk-new below /etc, as absolute paths of the host.
func (f FS) APKNew() ([]string, error) {
	var out []string
	err := filepath.WalkDir(f.path("etc"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable directory does not stop the scan
		}
		if !d.IsDir() && strings.HasSuffix(p, ".apk-new") {
			rel, _ := filepath.Rel(f.Root, p)
			out = append(out, "/"+filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// Process is a process of the host; Service is its OpenRC service, when it runs in one.
type Process struct {
	PID     int    `json:"pid"`
	Name    string `json:"name"`
	Service string `json:"service,omitempty"`
}

func (f FS) pids() ([]int, error) {
	entries, err := os.ReadDir(f.path("proc"))
	if err != nil {
		return nil, err
	}
	var out []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out, nil
}

// StaleProcesses lists processes that still map a deleted library or binary below /usr or /lib: an upgrade replaced
// it, and the process keeps the old one until it restarts. It fails when /proc cannot be listed; a process whose maps
// cannot be read is skipped, as processes come and go.
func (f FS) StaleProcesses() ([]Process, error) {
	ids, err := f.pids()
	if err != nil {
		return nil, err
	}
	var out []Process
	for _, pid := range ids {
		dir := "proc/" + strconv.Itoa(pid) + "/"
		file, err := os.Open(f.path(dir + "maps"))
		if err != nil {
			continue
		}
		stale := false
		sc := bufio.NewScanner(file)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasSuffix(line, " (deleted)") && (strings.Contains(line, " /usr/") || strings.Contains(line, " /lib")) {
				stale = true
				break
			}
		}
		file.Close()
		if !stale {
			continue
		}
		name, _ := os.ReadFile(f.path(dir + "comm"))
		out = append(out, Process{PID: pid, Name: strings.TrimSpace(string(name)), Service: f.service(dir)})
	}
	return out, nil
}

// service reads the OpenRC service from the process's cgroup (cgroup2: "0::/openrc.<service>").
func (f FS) service(dir string) string {
	data, _ := os.ReadFile(f.path(dir + "cgroup"))
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "openrc."); i >= 0 {
			s := line[i+len("openrc."):]
			if j := strings.IndexByte(s, '/'); j >= 0 {
				s = s[:j]
			}
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// LockHolder returns the process that holds apk's database lock (/lib/apk/db/lock), or nil when the lock is free or
// its holder is unknown. /proc/locks lines look like "2: FLOCK  ADVISORY  WRITE 1234 08:01:5678 0 EOF": the pid stands
// before dev:inode. The kernel prints pid 0 for a holder outside the reader's pid namespace and -1 for an OFD lock.
func (f FS) LockHolder() (*Process, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(f.path("lib/apk/db/lock"), &st); err != nil {
		return nil, nil
	}
	data, err := os.ReadFile(f.path("proc/locks"))
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		for i := 1; i < len(fields); i++ {
			parts := strings.Split(fields[i], ":")
			if len(parts) != 3 {
				continue
			}
			ino, err := strconv.ParseUint(parts[2], 10, 64)
			if err != nil || ino != st.Ino {
				continue
			}
			pid, err := strconv.Atoi(fields[i-1])
			if err != nil {
				continue
			}
			if pid <= 0 {
				return nil, nil // the first line of the inode is the holder: no process to name; later ones only wait
			}
			cmd, _ := os.ReadFile(f.path("proc/" + strconv.Itoa(pid) + "/cmdline"))
			name := strings.TrimSpace(strings.ReplaceAll(string(cmd), "\x00", " "))
			return &Process{PID: pid, Name: name}, nil
		}
	}
	return nil, nil
}
