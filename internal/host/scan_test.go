package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestAPKNew(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{
		"etc/ssh/sshd_config":         "",
		"etc/ssh/sshd_config.apk-new": "",
		"etc/conf.d/nginx.apk-new":    "",
	})}
	got, err := f.APKNew()
	want := []string{"/etc/conf.d/nginx.apk-new", "/etc/ssh/sshd_config.apk-new"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("APKNew() = %q, %v", got, err)
	}
}

func TestStaleProcesses(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{
		"proc/100/maps":   "7f00-7f10 r-xp 0 08:01 12 /usr/lib/libssl.so.3 (deleted)\n",
		"proc/100/comm":   "sshd\n",
		"proc/100/stat":   statLine(100, "sshd", 4242),
		"proc/100/cgroup": "0::/openrc.sshd\n",
		"proc/200/maps":   "7f00-7f10 rw-s 0 00:01 9 /memfd:jit (deleted)\n", // not a library
		"proc/200/comm":   "node\n",
		"proc/300/maps":   "7f00-7f10 r-xp 0 08:01 12 /lib/ld-musl-aarch64.so.1\n",
		"proc/300/stat":   statLine(300, "init", 1),
	})}
	got, err := f.StaleProcesses()
	want := []Process{{PID: 100, Name: "sshd", Service: "sshd", Start: 4242}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("StaleProcesses() = %+v, %v", got, err)
	}
}

func TestStaleProcessesPaths(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{
		"proc/400/maps":   "7f00-7f10 r-xp 0 08:01 12 /lib/libcrypto.so.3 (deleted)\n",
		"proc/400/comm":   "ntpd\n",
		"proc/400/stat":   statLine(400, "ntpd", 77),
		"proc/500/maps":   "7f00-7f10 rw-p 0 08:01 12 /tmp/scratch.dat (deleted)\n", // deleted, but not a library
		"proc/500/comm":   "worker\n",
		"proc/600/comm":   "kworker\n", // no maps: a kernel thread, or gone
		"proc/self/maps":  "7f00-7f10 r-xp 0 08:01 12 /usr/lib/libz.so.1 (deleted)\n",
		"proc/stat":       "cpu 1 2 3\n",
		"proc/abc/maps":   "7f00-7f10 r-xp 0 08:01 12 /usr/lib/libz.so.1 (deleted)\n",
		"proc/400/cgroup": "0::/\n", // not in an OpenRC service
	})}
	got, err := f.StaleProcesses()
	want := []Process{{PID: 400, Name: "ntpd", Start: 77}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("StaleProcesses() = %+v, %v", got, err)
	}
}

// statLine is /proc/<pid>/stat of a sleeping process with the given comm and start time (field 22).
func statLine(pid int, comm string, start uint64) string {
	return fmt.Sprintf("%d (%s) S 1 %d %d 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 %d 1000000 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0\n",
		pid, comm, pid, pid, start)
}

func TestStaleProcessesRecordsTheStartOfAnOddName(t *testing.T) {
	// comm may hold spaces and parentheses: the fields count from the last ")"
	f := FS{Root: tree(t, map[string]string{
		"proc/700/maps": "7f00-7f10 r-xp 0 08:01 12 /usr/lib/libz.so.1 (deleted)\n",
		"proc/700/comm": "a) (b\n",
		"proc/700/stat": statLine(700, "a) (b", 9001),
	})}
	got, err := f.StaleProcesses()
	want := []Process{{PID: 700, Name: "a) (b", Start: 9001}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("StaleProcesses() = %+v, %v", got, err)
	}
}

func TestStaleProcessesSkipsOneWhoseStatCannotBeRead(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{
		"proc/800/maps": "7f00-7f10 r-xp 0 08:01 12 /usr/lib/libz.so.1 (deleted)\n",
		"proc/800/comm": "gone\n",
	})}
	got, err := f.StaleProcesses()
	if err != nil || len(got) != 0 {
		t.Fatalf("StaleProcesses() = %+v, %v", got, err)
	}
}

func TestRunning(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{"proc/100/stat": statLine(100, "sshd", 4242)})}
	for name, c := range map[string]struct {
		p    Process
		want bool
	}{
		"same pid and start":    {Process{PID: 100, Start: 4242}, true},
		"same pid, other start": {Process{PID: 100, Start: 4243}, false},
		"pid gone":              {Process{PID: 101, Start: 4242}, false},
		"old record, no start":  {Process{PID: 100}, false},
	} {
		if got := f.Running(c.p); got != c.want {
			t.Errorf("%s: Running(%+v) = %v, want %v", name, c.p, got, c.want)
		}
	}
}

func TestBootTime(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{"proc/stat": "cpu  1 2 3\nbtime 1700000000\nprocesses 5\n"})}
	got, err := f.BootTime()
	if err != nil || !got.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("BootTime() = %v, %v", got, err)
	}
	for name, content := range map[string]string{"no btime": "cpu 1 2 3\n", "garbled": "btime x\n"} {
		if _, err := (FS{Root: tree(t, map[string]string{"proc/stat": content})}).BootTime(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := (FS{Root: t.TempDir()}).BootTime(); err == nil {
		t.Error("no /proc/stat: no error")
	}
}

func TestStaleProcessesNoProc(t *testing.T) {
	// a missing /proc is an error, not "no stale processes"
	got, err := FS{Root: t.TempDir()}.StaleProcesses()
	if err == nil || !errors.Is(err, os.ErrNotExist) || got != nil {
		t.Fatalf("StaleProcesses() = %+v, %v", got, err)
	}
}

func TestLockHolder(t *testing.T) {
	root := tree(t, map[string]string{"lib/apk/db/lock": "", "proc/1234/cmdline": "apk\x00add\x00vim\x00"})
	var st syscall.Stat_t
	if err := syscall.Stat(filepath.Join(root, "lib/apk/db/lock"), &st); err != nil {
		t.Fatal(err)
	}
	locks := fmt.Sprintf("1: POSIX  ADVISORY  WRITE 99 08:01:5 0 EOF\n2: FLOCK  ADVISORY  WRITE 1234 08:01:%d 0 EOF\n", st.Ino)
	if err := os.WriteFile(filepath.Join(root, "proc/locks"), []byte(locks), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := FS{Root: root}.LockHolder()
	if err != nil || p == nil || p.PID != 1234 || p.Name != "apk add vim" {
		t.Fatalf("LockHolder() = %+v, %v", p, err)
	}
	if p, _ := (FS{Root: tree(t, map[string]string{"lib/apk/db/lock": "", "proc/locks": ""})}).LockHolder(); p != nil {
		t.Fatalf("free lock: %+v", p)
	}
}

func TestLockHolderUnknownPID(t *testing.T) {
	// /proc/locks prints pid 0 for a holder outside the reader's pid namespace and -1 for an OFD lock: nobody to name,
	// and the process queued behind it is no holder
	for name, pid := range map[string]string{"other pid namespace": "0", "OFD lock": "-1"} {
		root := tree(t, map[string]string{"lib/apk/db/lock": "", "proc/1234/cmdline": "apk\x00add\x00vim\x00"})
		var st syscall.Stat_t
		if err := syscall.Stat(filepath.Join(root, "lib/apk/db/lock"), &st); err != nil {
			t.Fatal(err)
		}
		locks := fmt.Sprintf("1: FLOCK  ADVISORY  WRITE %s 08:01:%d 0 EOF\n1: -> FLOCK  ADVISORY  WRITE 1234 08:01:%d 0 EOF\n", pid, st.Ino, st.Ino)
		if err := os.WriteFile(filepath.Join(root, "proc/locks"), []byte(locks), 0o644); err != nil {
			t.Fatal(err)
		}
		if p, err := (FS{Root: root}).LockHolder(); err != nil || p != nil {
			t.Errorf("%s: LockHolder() = %+v, %v", name, p, err)
		}
	}
}
