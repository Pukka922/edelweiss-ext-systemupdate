package host

import (
	"os"
	"path/filepath"
	"testing"
)

// tree writes files (path → content) below a new temp dir and returns it.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestMode(t *testing.T) {
	for name, c := range map[string]struct {
		mounts string
		want   Mode
	}{
		"sys":      {"/dev/mmcblk0p2 / ext4 rw,relatime 0 0\n/dev/mmcblk0p1 /boot vfat rw 0 0\n", Sys},
		"diskless": {"tmpfs / tmpfs rw,mode=755 0 0\n/dev/loop0 /.modloop squashfs ro 0 0\n", Diskless},
		"data":     {"tmpfs / tmpfs rw 0 0\n/dev/sda2 /var ext4 rw 0 0\n", Data},
	} {
		got, err := FS{Root: tree(t, map[string]string{"proc/mounts": c.mounts})}.Mode()
		if err != nil || got != c.want {
			t.Errorf("%s: Mode() = %q, %v", name, got, err)
		}
	}
}

func TestMountsEscapesAndFstab(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{"proc/mounts": "/dev/sda1 /mnt/my\\040disk ext4 ro,noatime 0 0\n"})}
	ms, err := f.Mounts()
	if err != nil || len(ms) != 1 || ms[0].Point != "/mnt/my disk" || !ms[0].ReadOnly() || ms[0].Type != "ext4" {
		t.Fatalf("Mounts() = %+v, %v", ms, err)
	}
	if tab, err := f.Fstab(); err != nil || tab != nil {
		t.Fatalf("a missing fstab is an empty table: %+v, %v", tab, err)
	}
}

func TestReleaseAndInstalled(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{
		"etc/alpine-release":   "3.24.2\n",
		"lib/apk/db/installed": "C:Q1abc\nP:musl\nV:1.2.6-r0\n\nP:grub-efi\nV:2.12-r0\n",
	})}
	if r, err := f.Release(); err != nil || r != "3.24.2" {
		t.Fatalf("Release() = %q, %v", r, err)
	}
	inst, err := f.Installed()
	if err != nil || !inst["musl"] || !inst["grub-efi"] || inst["openssl"] {
		t.Fatalf("Installed() = %v, %v", inst, err)
	}
}

func TestStartTimeAndAlive(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{
		"proc/42/stat":                   "42 (my (odd) name) S 1 42 42 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 98765 1000 10\n",
		"proc/sys/kernel/random/boot_id": "1111-2222\n",
	})}
	st, err := f.StartTime(42)
	if err != nil || st != 98765 {
		t.Fatalf("StartTime = %d, %v", st, err)
	}
	if !f.Alive(42, 98765) || f.Alive(42, 1) || f.Alive(43, 98765) {
		t.Fatal("Alive is wrong")
	}
	if f.BootID() != "1111-2222" {
		t.Fatalf("BootID = %q", f.BootID())
	}
}

func TestAliveNotForZombies(t *testing.T) {
	stat := func(state string) string {
		return "42 (runner) " + state + " 1 42 42 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 98765 1000 10\n"
	}
	for state, alive := range map[string]bool{"R": true, "S": true, "D": true, "Z": false, "X": false} {
		f := FS{Root: tree(t, map[string]string{"proc/42/stat": stat(state)})}
		if got := f.Alive(42, 98765); got != alive {
			t.Errorf("state %s: Alive = %v, want %v", state, got, alive)
		}
		// the start time of a zombie is still readable
		if st, err := f.StartTime(42); err != nil || st != 98765 {
			t.Errorf("state %s: StartTime = %d, %v", state, st, err)
		}
	}
}

// TestRealProc runs against the real /proc of the machine the test runs on, with this very process.
func TestRealProc(t *testing.T) {
	f := New()
	pid := os.Getpid()
	start, err := f.StartTime(pid)
	if err != nil {
		t.Skipf("no usable /proc: %v", err)
	}
	if start == 0 || !f.Alive(pid, start) || f.Alive(pid, start+1) {
		t.Fatalf("StartTime(%d) = %d: Alive is wrong", pid, start)
	}
	if f.BootID() == "" {
		t.Fatal("BootID is empty")
	}
}

func TestDiskSpace(t *testing.T) {
	total, free, err := DiskSpace(t.TempDir())
	if err != nil || total == 0 || free > total {
		t.Fatalf("DiskSpace() = %d, %d, %v", total, free, err)
	}
	if _, _, err := DiskSpace(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing path is an error")
	}
}
