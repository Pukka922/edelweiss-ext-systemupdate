// Package host reads the facts the updates extension needs about the system: mounts and the Alpine mode
// (sys, diskless, data), the release, installed packages, the kernel, boot files, configuration files apk left as
// *.apk-new, processes that still map replaced libraries, and who holds apk's lock. Every path is below Root, so tests
// run against a temp tree.
package host

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FS reads the host below Root ("/" in production).
type FS struct{ Root string }

// New returns the FS of the real host.
func New() FS { return FS{Root: "/"} }

func (f FS) path(p string) string { return filepath.Join(f.Root, p) }

// Mode is how Alpine runs: installed to disk (sys), from RAM (diskless) or from RAM with /var on disk (data).
type Mode string

const (
	Sys      Mode = "sys"
	Diskless Mode = "diskless"
	Data     Mode = "data"
)

// Mount is a line of /proc/mounts or /etc/fstab.
type Mount struct {
	Device  string
	Point   string
	Type    string
	Options []string
}

// ReadOnly reports whether the mount has the ro option.
func (m Mount) ReadOnly() bool {
	for _, o := range m.Options {
		if o == "ro" {
			return true
		}
	}
	return false
}

func (f FS) table(p string) ([]Mount, error) {
	data, err := os.ReadFile(f.path(p))
	if err != nil {
		return nil, err
	}
	var out []Mount
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		m := Mount{Device: unescape(fields[0]), Point: unescape(fields[1]), Type: fields[2]}
		if len(fields) > 3 {
			m.Options = strings.Split(fields[3], ",")
		}
		out = append(out, m)
	}
	return out, nil
}

// unescape decodes the octal escapes of /proc/mounts (\040 for a space).
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Mounts reads /proc/mounts.
func (f FS) Mounts() ([]Mount, error) { return f.table("proc/mounts") }

// Fstab reads /etc/fstab; a missing file is an empty table.
func (f FS) Fstab() ([]Mount, error) {
	ms, err := f.table("etc/fstab")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return ms, err
}

func find(ms []Mount, point string) *Mount {
	var found *Mount
	for i := range ms {
		if ms[i].Point == point {
			found = &ms[i] // the last one wins, as in the kernel's view
		}
	}
	return found
}

// Mode tells sys, diskless and data apart: a tmpfs / means RAM, then /var on a block device means data.
func (f FS) Mode() (Mode, error) {
	ms, err := f.Mounts()
	if err != nil {
		return "", err
	}
	if root := find(ms, "/"); root == nil || root.Type != "tmpfs" {
		return Sys, nil
	}
	if v := find(ms, "/var"); v != nil && strings.HasPrefix(v.Device, "/dev/") {
		return Data, nil
	}
	return Diskless, nil
}

// Release reads /etc/alpine-release, e.g. "3.24.2".
func (f FS) Release() (string, error) {
	data, err := os.ReadFile(f.path("etc/alpine-release"))
	return strings.TrimSpace(string(data)), err
}

// Installed returns the names of the installed packages (the P: lines of apk's database).
func (f FS) Installed() (map[string]bool, error) {
	file, err := os.Open(f.path("lib/apk/db/installed"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	defer file.Close()
	out := map[string]bool{}
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "P:"); ok {
			out[name] = true
		}
	}
	return out, sc.Err()
}

// BootID is the kernel's id of the current boot ("" if unknown).
func (f FS) BootID() string {
	data, _ := os.ReadFile(f.path("proc/sys/kernel/random/boot_id"))
	return strings.TrimSpace(string(data))
}

// stat reads fields 3 (the state letter) and 22 (the start time) of /proc/<pid>/stat.
func (f FS) stat(pid int) (state byte, start uint64, err error) {
	data, err := os.ReadFile(f.path("proc/" + strconv.Itoa(pid) + "/stat"))
	if err != nil {
		return 0, 0, err
	}
	i := bytes.LastIndexByte(data, ')') // the name may hold spaces and parentheses
	if i < 0 {
		return 0, 0, errors.New("host: malformed stat")
	}
	fields := strings.Fields(string(data[i+1:])) // fields[0] is field 3 (state)
	if len(fields) < 20 {
		return 0, 0, errors.New("host: short stat")
	}
	start, err = strconv.ParseUint(fields[19], 10, 64)
	return fields[0][0], start, err
}

// StartTime returns field 22 of /proc/<pid>/stat: when the process started, in clock ticks after boot.
func (f FS) StartTime(pid int) (uint64, error) {
	_, start, err := f.stat(pid)
	return start, err
}

// Alive reports whether pid runs and is still the process that started at start. A zombie (Z) or dead (X) process is
// gone for all purposes here, though its stat file is still readable until the parent reaps it.
func (f FS) Alive(pid int, start uint64) bool {
	state, st, err := f.stat(pid)
	return err == nil && state != 'Z' && state != 'X' && st == start
}
