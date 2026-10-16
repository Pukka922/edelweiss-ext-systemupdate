package jobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
)

const (
	kernelChange   = "(1/3) Upgrading linux-lts (6.12.1-r0 -> 6.12.2-r0)\n(2/3) Upgrading busybox (1.37.0-r20 -> 1.37.0-r30)\nOK: 1 MiB in 2 packages\n"
	noKernelChange = "(1/1) Upgrading busybox (1.37.0-r20 -> 1.37.0-r30)\nOK: 1 MiB in 2 packages\n"
)

// kernelTree is a host that runs 6.12.1-0-lts with 6.12.2-0-lts installed (a restart is pending), and the boot files
// of flavor lts with the given modification time (none if boot is zero).
func kernelTree(t *testing.T, boot time.Time, vmlinuz bool) Env {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"usr/share/kernel/lts/kernel.release":  "6.12.2-0-lts\n",
		"lib/modules/6.12.1-0-lts/modules.dep": "",
		"lib/modules/6.12.2-0-lts/modules.dep": "",
		"lib/apk/db/installed":                 "P:musl\nV:1.2.5-r10\n",
	}
	if !boot.IsZero() {
		files["boot/initramfs-lts"] = "x"
		if vmlinuz {
			files["boot/vmlinuz-lts"] = "x"
		}
	}
	writeTree(t, root, files)
	if !boot.IsZero() {
		if err := os.Chtimes(filepath.Join(root, "boot/initramfs-lts"), boot, boot); err != nil {
			t.Fatal(err)
		}
	}
	e := offlineEnv()
	e.Root, e.Host = root, host.FS{Root: root}
	e.Uname = func() string { return "6.12.1-0-lts" }
	return e
}

func TestPost(t *testing.T) {
	since := time.Now().Add(-10 * time.Minute)
	fresh, stale := since.Add(time.Minute), since.Add(-time.Hour)
	for name, c := range map[string]struct {
		boot                          time.Time
		vmlinuz                       bool
		out                           string
		reboot, kernelChanged, bootOK bool
	}{
		"kernel changed, fresh boot files":    {fresh, true, kernelChange, true, true, true},
		"kernel changed, stale initramfs":     {stale, true, kernelChange, true, true, false},
		"kernel changed, no vmlinuz":          {fresh, false, kernelChange, true, true, false},
		"kernel changed, no boot files":       {time.Time{}, false, kernelChange, true, true, false},
		"padded counters":                     {stale, true, "(  1/400) Upgrading linux-lts (6.12.1-r0 -> 6.12.2-r0)\n", true, true, false},
		"kernel installed":                    {stale, true, "( 1/9) Installing linux-virt (6.12.2-r0)\n", true, true, false},
		"no kernel change, a restart pending": {stale, true, noKernelChange, true, false, true},
		"no output":                           {stale, true, "", true, false, true},
		"only firmware changed":               {stale, true, "(1/1) Upgrading linux-firmware-intel (20260101-r0 -> 20260201-r0)\n", true, false, true},
		"a package that only looks like one":  {stale, true, "(1/1) Upgrading linux-pam (1.7.0-r0 -> 1.7.1-r0)\n(2/2) Upgrading linux-headers (6.12-r0 -> 6.13-r0)\n", true, false, true},
		"a download is no change":             {stale, true, "(1/3) Downloading linux-lts (6.12.2-r0)\n", true, false, true},
	} {
		p := kernelTree(t, c.boot, c.vmlinuz).post(since, c.out, false)
		if p.Reboot != c.reboot || p.KernelChanged != c.kernelChanged || p.BootOK != c.bootOK {
			t.Errorf("%s: reboot %v, kernelChanged %v, bootOk %v; want %v, %v, %v", name, p.Reboot, p.KernelChanged, p.BootOK,
				c.reboot, c.kernelChanged, c.bootOK)
		}
	}
}

func TestPostKernelReinstalled(t *testing.T) {
	// the running kernel is the installed one, and the job reinstalled its package: no restart is pending, but the
	// boot files are still the job's to answer for
	since := time.Now().Add(-10 * time.Minute)
	for name, c := range map[string]struct {
		boot   time.Time
		bootOK bool
	}{"fresh": {since.Add(time.Minute), true}, "stale": {since.Add(-time.Hour), false}} {
		e := kernelTree(t, c.boot, true)
		e.Uname = func() string { return "6.12.2-0-lts" }
		p := e.post(since, "(1/1) Reinstalling linux-lts (6.12.2-r0)\n", false)
		if p.Reboot || !p.KernelChanged || p.BootOK != c.bootOK {
			t.Errorf("%s: post = %+v", name, p)
		}
	}
}

func TestPostNotApplicable(t *testing.T) {
	// a container: a kernel package is installed and upgraded, but the guest runs the kernel of its host
	e := kernelTree(t, time.Time{}, false)
	e.Uname = func() string { return "6.6.114.1-microsoft-standard-WSL2" }
	p := e.post(time.Now().Add(-time.Minute), kernelChange, false)
	if p.Reboot || p.KernelChanged || !p.BootOK {
		t.Fatalf("post = %+v", p)
	}
}
