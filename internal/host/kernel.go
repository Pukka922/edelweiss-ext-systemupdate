package host

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// alpineRelease is the shape of the release of a kernel from Alpine's packages, <version>-<pkgrel>-<flavor>
// (6.12.1-0-lts, 6.6.31-0-rpi). Other kernels, such as the one of WSL, are not managed by apk.
var alpineRelease = regexp.MustCompile(`^[0-9][0-9.]*-[0-9]+-[a-z0-9]+$`)

// Kernel says whether the running kernel is the installed one. Applicable is false when no kernel package is
// installed, or when the running kernel is not one of Alpine's (a container or WSL runs the kernel of its host, even
// if a linux-* package is installed in the guest): then nothing about it needs a reboot.
type Kernel struct {
	Applicable     bool   `json:"applicable"`
	Running        string `json:"running"`
	Installed      string `json:"installed,omitempty"`
	Flavor         string `json:"flavor,omitempty"`
	RebootRequired bool   `json:"rebootRequired"`
}

// Kernel compares the running kernel (uname -r) with /usr/share/kernel/<flavor>/kernel.release of the installed
// kernel package, and checks /lib/modules: an upgrade removes the old modules. A running Alpine kernel whose flavor
// is not installed (a switch from lts to virt) still gets the modules check.
func (f FS) Kernel(running string) Kernel {
	k := Kernel{Running: running}
	releases, _ := filepath.Glob(f.path("usr/share/kernel/*/kernel.release"))
	if len(releases) == 0 || !alpineRelease.MatchString(running) {
		return k
	}
	k.Applicable = true
	k.Flavor = running[strings.LastIndexByte(running, '-')+1:]
	if data, err := os.ReadFile(f.path("usr/share/kernel/" + k.Flavor + "/kernel.release")); err == nil {
		k.Installed = strings.TrimSpace(string(data))
	}
	if _, err := os.Stat(f.path("lib/modules/" + running)); errors.Is(err, fs.ErrNotExist) {
		k.RebootRequired = true
	}
	if k.Installed != "" && k.Installed != running {
		k.RebootRequired = true
	}
	return k
}

// BootFilesFresh reports whether /boot holds an initramfs of flavor written after since, and a vmlinuz: only then is
// a reboot into a new kernel safe.
func (f FS) BootFilesFresh(flavor string, since time.Time) bool {
	fi, err := os.Stat(f.path("boot/initramfs-" + flavor))
	if err != nil || fi.ModTime().Before(since) {
		return false
	}
	_, err = os.Stat(f.path("boot/vmlinuz-" + flavor))
	return err == nil
}

// bootSlack is how much older than the install the initramfs may be and still count as written by it: a FAT /boot
// stores mtimes to two seconds.
const bootSlack = 2 * time.Second

// BootStale is the live check that a restart into the installed kernel is not safe: a restart is pending, and the boot
// files of the installed kernel are missing or older than the moment apk installed its package (mkinitfs failed, or
// /boot was full or not mounted then). That moment is the ctime of the package's kernel.release, not its mtime: apk
// keeps the mtimes of a package's files (the build date), so an old initramfs written after the build date would
// pass. Unlike the post-checks of a job it depends on nothing that happened before: it clears by itself once /boot is
// fixed or the host has restarted. It is false when no kernel.release exists for the flavor, as there is then nothing
// to judge the files by.
func (f FS) BootStale(k Kernel) bool {
	if !k.Applicable || !k.RebootRequired {
		return false
	}
	var st syscall.Stat_t
	if err := syscall.Stat(f.path("usr/share/kernel/"+k.Flavor+"/kernel.release"), &st); err != nil {
		return false
	}
	installed := time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
	return !f.BootFilesFresh(k.Flavor, installed.Add(-bootSlack))
}

// BootState is /boot: listed in fstab, mounted, read-only.
type BootState struct {
	InFstab  bool `json:"inFstab"`
	Mounted  bool `json:"mounted"`
	ReadOnly bool `json:"readOnly"`
}

// Boot reports the state of /boot.
func (f FS) Boot() (BootState, error) {
	var b BootState
	tab, err := f.Fstab()
	if err != nil {
		return b, err
	}
	b.InFstab = find(tab, "/boot") != nil
	ms, err := f.Mounts()
	if err != nil {
		return b, err
	}
	if m := find(ms, "/boot"); m != nil {
		b.Mounted, b.ReadOnly = true, m.ReadOnly()
	}
	return b, nil
}

// SeparateUsr reports whether /usr is a mount of its own (unsupported by Alpine 3.24).
func (f FS) SeparateUsr() (bool, error) {
	ms, err := f.Mounts()
	return find(ms, "/usr") != nil, err
}

// GRUB reports whether a GRUB package is installed and, if so, whether it boots by EFI.
func (f FS) GRUB() (installed, efi bool) {
	pkgs, _ := f.Installed()
	installed = pkgs["grub"] || pkgs["grub-efi"] || pkgs["grub-bios"]
	if !installed {
		return false, false
	}
	ms, _ := f.Mounts()
	return true, pkgs["grub-efi"] || find(ms, "/boot/efi") != nil
}

// RaspberryPi reports whether the host is a Raspberry Pi (device tree model or the Pi bootloader package).
func (f FS) RaspberryPi() bool {
	model, _ := os.ReadFile(f.path("proc/device-tree/model"))
	if strings.Contains(string(model), "Raspberry Pi") {
		return true
	}
	pkgs, _ := f.Installed()
	return pkgs["raspberrypi-bootloader"]
}
