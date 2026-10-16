package host

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestKernel(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{
		"usr/share/kernel/lts/kernel.release":   "6.18.56-0-lts\n",
		"lib/modules/6.18.52-0-lts/modules.dep": "",
		"lib/modules/6.18.56-0-lts/modules.dep": "",
	})}
	k := f.Kernel("6.18.52-0-lts")
	if !k.Applicable || !k.RebootRequired || k.Installed != "6.18.56-0-lts" || k.Flavor != "lts" {
		t.Fatalf("new kernel installed: %+v", k)
	}
	if k := f.Kernel("6.18.56-0-lts"); k.RebootRequired {
		t.Fatalf("running the installed kernel: %+v", k)
	}
	gone := FS{Root: tree(t, map[string]string{"usr/share/kernel/rpi/kernel.release": "6.12.51-0-rpi\n"})}
	if k := gone.Kernel("6.12.51-0-rpi"); !k.RebootRequired {
		t.Fatalf("the modules of the running kernel are gone: %+v", k)
	}
}

func TestKernelNotApplicable(t *testing.T) {
	// a container or WSL: no linux-* package, no /lib/modules of the running kernel – never "reboot required"
	f := FS{Root: tree(t, map[string]string{"etc/alpine-release": "3.24.2\n"})}
	if k := f.Kernel("6.6.114.1-microsoft-standard-WSL2"); k.Applicable || k.RebootRequired {
		t.Fatalf("no kernel package: %+v", k)
	}
}

func TestKernelForeignRunning(t *testing.T) {
	// a linux-lts package is installed, but the guest runs the kernel of its host: nothing to reboot into
	f := FS{Root: tree(t, map[string]string{"usr/share/kernel/lts/kernel.release": "6.18.56-0-lts\n"})}
	for _, running := range []string{"6.6.114.1-microsoft-standard-WSL2", "6.1.21-v8+", "6.1.0-rpi7-rpi-v8", "6.12.1-0-LTS", "6.12.1-lts", ""} {
		if k := f.Kernel(running); k.Applicable || k.RebootRequired || k.Installed != "" || k.Running != running {
			t.Errorf("running %q: %+v", running, k)
		}
	}
}

func TestKernelFlavorSwitch(t *testing.T) {
	// the running kernel is Alpine's virt kernel, only lts is installed: no version to compare, the modules decide
	root := tree(t, map[string]string{"usr/share/kernel/lts/kernel.release": "6.18.56-0-lts\n"})
	f := FS{Root: root}
	if k := f.Kernel("6.18.52-0-virt"); !k.Applicable || !k.RebootRequired || k.Flavor != "virt" || k.Installed != "" {
		t.Fatalf("the modules of the running kernel are gone: %+v", k)
	}
	if err := os.MkdirAll(filepath.Join(root, "lib/modules/6.18.52-0-virt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if k := f.Kernel("6.18.52-0-virt"); !k.Applicable || k.RebootRequired {
		t.Fatalf("the modules of the running kernel are there: %+v", k)
	}
}

func TestBootFilesFresh(t *testing.T) {
	root := tree(t, map[string]string{"boot/initramfs-lts": "x", "boot/vmlinuz-lts": "x"})
	f := FS{Root: root}
	since := time.Now().Add(-time.Minute)
	if !f.BootFilesFresh("lts", since) {
		t.Fatal("fresh files")
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "boot/initramfs-lts"), old, old); err != nil {
		t.Fatal(err)
	}
	if f.BootFilesFresh("lts", since) {
		t.Fatal("an initramfs older than the upgrade is not fresh")
	}
	if (FS{Root: tree(t, map[string]string{"boot/initramfs-lts": "x"})}).BootFilesFresh("lts", since) {
		t.Fatal("a missing vmlinuz is not fresh")
	}
}

// ctimeOf is the inode change time of path. Nothing can set it: unlike the mtime, which apk keeps from the package (the
// build date), it is the moment apk wrote the file.
func ctimeOf(t *testing.T, path string) time.Time {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatal(err)
	}
	return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
}

// bootTree is a host that runs 6.12.1-0-lts with 6.12.2-0-lts installed, whose kernel.release was just written
// (apk writes it); vmlinuz says whether /boot has a vmlinuz-lts. It returns the host and the ctime of kernel.release.
func bootTree(t *testing.T, vmlinuz bool) (FS, time.Time) {
	t.Helper()
	files := map[string]string{
		"usr/share/kernel/lts/kernel.release":  "6.12.2-0-lts\n",
		"lib/modules/6.12.1-0-lts/modules.dep": "",
		"lib/modules/6.12.2-0-lts/modules.dep": "",
	}
	if vmlinuz {
		files["boot/vmlinuz-lts"] = "x"
	}
	root := tree(t, files)
	return FS{Root: root}, ctimeOf(t, filepath.Join(root, "usr/share/kernel/lts/kernel.release"))
}

// touch sets the mtime of the file below the host's root (a missing file is created first).
func touch(t *testing.T, f FS, rel string, at time.Time) {
	t.Helper()
	p := filepath.Join(f.Root, rel)
	if _, err := os.Stat(p); err != nil {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestBootStale(t *testing.T) {
	const running = "6.12.1-0-lts"
	type initramfs struct {
		exists bool
		offset time.Duration // its mtime relative to the ctime of kernel.release
	}
	for name, c := range map[string]struct {
		initramfs initramfs
		vmlinuz   bool
		want      bool
	}{
		"fresh":                    {initramfs{true, time.Minute}, true, false},
		"written with the package": {initramfs{true, 0}, true, false},
		"within the slack":         {initramfs{true, -time.Second}, true, false},
		"beyond the slack":         {initramfs{true, -3 * time.Second}, true, true},
		"older than the kernel":    {initramfs{true, -time.Minute}, true, true},
		"no vmlinuz":               {initramfs{true, time.Minute}, false, true},
		"no initramfs":             {initramfs{}, true, true},
		"no boot files":            {initramfs{}, false, true},
	} {
		f, written := bootTree(t, c.vmlinuz)
		if c.initramfs.exists {
			touch(t, f, "boot/initramfs-lts", written.Add(c.initramfs.offset))
		}
		k := f.Kernel(running)
		if !k.Applicable || !k.RebootRequired {
			t.Fatalf("%s: the tree is meant to need a restart: %+v", name, k)
		}
		if got := f.BootStale(k); got != c.want {
			t.Errorf("%s: BootStale = %v, want %v", name, got, c.want)
		}
	}
}

// apk keeps the mtimes of a package's files (the build date), so the mtime of kernel.release says nothing about when
// the kernel was installed: an initramfs of the previous kernel, written after the new kernel was built but before it
// was installed, must stay stale.
func TestBootStaleJudgesByTheCtimeOfKernelRelease(t *testing.T) {
	f, written := bootTree(t, true)
	built := written.Add(-30 * 24 * time.Hour)
	touch(t, f, "usr/share/kernel/lts/kernel.release", built) // the mtime moves, the ctime stays "now"
	if ctime := ctimeOf(t, filepath.Join(f.Root, "usr/share/kernel/lts/kernel.release")); ctime.Before(written) {
		t.Fatalf("control: Chtimes must not lower the ctime (%v < %v)", ctime, written)
	}
	k := f.Kernel("6.12.1-0-lts")
	if !k.RebootRequired {
		t.Fatalf("a restart is pending: %+v", k)
	}
	touch(t, f, "boot/initramfs-lts", written.Add(-24*time.Hour)) // after the build, long before the install
	if !f.BootStale(k) {
		t.Fatal("an initramfs older than the install is stale, whatever the mtime of kernel.release says")
	}
	touch(t, f, "boot/initramfs-lts", time.Now().Add(time.Minute)) // mkinitfs ran after the install
	if f.BootStale(k) {
		t.Fatal("an initramfs newer than the install is fresh")
	}
}

func TestBootStaleClearsWhenTheBootFilesAreFixed(t *testing.T) {
	f, written := bootTree(t, true)
	touch(t, f, "boot/initramfs-lts", written.Add(-time.Minute))
	k := f.Kernel("6.12.1-0-lts")
	if !f.BootStale(k) {
		t.Fatal("an initramfs older than the kernel is stale")
	}
	// the user runs mkinitfs
	touch(t, f, "boot/initramfs-lts", time.Now().Add(time.Minute))
	if f.BootStale(k) {
		t.Fatal("a fresh initramfs clears it")
	}
	// or reboots into the kernel: then there is nothing to wait for, whatever the files are
	touch(t, f, "boot/initramfs-lts", written.Add(-time.Hour))
	if !f.BootStale(k) {
		t.Fatal("control: stale again")
	}
	if rebooted := f.Kernel("6.12.2-0-lts"); f.BootStale(rebooted) {
		t.Fatalf("running the installed kernel: %+v", rebooted)
	}
}

func TestBootStaleNeedsAPendingRestartOfAnAlpineKernel(t *testing.T) {
	f, _ := bootTree(t, false) // no boot files at all
	if !f.BootStale(f.Kernel("6.12.1-0-lts")) {
		t.Fatal("control: stale")
	}
	// no kernel package on the host (a container): nothing to boot into
	none := FS{Root: tree(t, map[string]string{"etc/alpine-release": "3.24.2\n"})}
	if none.BootStale(none.Kernel("6.6.114.1-microsoft-standard-WSL2")) {
		t.Fatal("not applicable")
	}
	// the guest runs the kernel of its host although a linux-lts package is installed
	if f.BootStale(f.Kernel("6.6.114.1-microsoft-standard-WSL2")) {
		t.Fatal("a foreign running kernel")
	}
	// the running flavor has no kernel.release: its boot files cannot be judged
	if f.BootStale(f.Kernel("6.12.1-0-virt")) {
		t.Fatal("a flavor without kernel.release")
	}
	// an Applicable kernel that needs no restart
	if f.BootStale(Kernel{Applicable: true, Flavor: "lts", RebootRequired: false}) {
		t.Fatal("no restart pending")
	}
}

func TestBootAndUsr(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{
		"etc/fstab":   "# comment\nUUID=abc /boot vfat defaults 0 2\n/dev/sda2 / ext4 defaults 0 1\n",
		"proc/mounts": "/dev/sda2 / ext4 rw 0 0\n/dev/sda1 /boot vfat ro 0 0\n/dev/sda3 /usr ext4 rw 0 0\n",
	})}
	b, err := f.Boot()
	if err != nil || !b.InFstab || !b.Mounted || !b.ReadOnly {
		t.Fatalf("Boot() = %+v, %v", b, err)
	}
	if sep, _ := f.SeparateUsr(); !sep {
		t.Fatal("/usr is a mount")
	}
	unmounted := FS{Root: tree(t, map[string]string{"etc/fstab": "LABEL=boot /boot vfat defaults 0 2\n", "proc/mounts": "/dev/sda2 / ext4 rw 0 0\n"})}
	if b, _ := unmounted.Boot(); !b.InFstab || b.Mounted {
		t.Fatalf("not mounted: %+v", b)
	}
}

func TestGRUBAndPi(t *testing.T) {
	f := FS{Root: tree(t, map[string]string{
		"lib/apk/db/installed":   "P:grub-efi\nV:2.12-r0\n\nP:raspberrypi-bootloader\nV:1-r0\n",
		"proc/mounts":            "/dev/sda1 /boot/efi vfat rw 0 0\n",
		"proc/device-tree/model": "Raspberry Pi 4 Model B Rev 1.4\x00",
	})}
	if inst, efi := f.GRUB(); !inst || !efi {
		t.Fatalf("GRUB() = %v %v", inst, efi)
	}
	if !f.RaspberryPi() {
		t.Fatal("RaspberryPi")
	}
	if inst, efi := (FS{Root: tree(t, map[string]string{"proc/mounts": "/dev/sda1 /boot/efi vfat rw 0 0\n"})}).GRUB(); inst || efi {
		t.Fatal("no grub package: neither installed nor efi")
	}
}

func TestGRUBEFIEvidence(t *testing.T) {
	for name, c := range map[string]struct {
		installed, mounts string
		efi               bool
	}{
		"bios":          {"P:grub-bios\nV:2.12-r0\n", "/dev/sda1 /boot ext4 rw 0 0\n", false},
		"grub, efi dir": {"P:grub\nV:2.12-r0\n", "/dev/sda1 /boot/efi vfat rw 0 0\n", true},
		"grub-efi":      {"P:grub-efi\nV:2.12-r0\n", "/dev/sda1 /boot ext4 rw 0 0\n", true},
	} {
		f := FS{Root: tree(t, map[string]string{"lib/apk/db/installed": c.installed, "proc/mounts": c.mounts})}
		if inst, efi := f.GRUB(); !inst || efi != c.efi {
			t.Errorf("%s: GRUB() = %v %v, want true %v", name, inst, efi, c.efi)
		}
	}
}
