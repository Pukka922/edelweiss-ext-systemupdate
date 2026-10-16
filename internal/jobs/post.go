package jobs

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
)

// Post is what the page shows after a job.
type Post struct {
	Reboot        bool           `json:"reboot"`
	BootOK        bool           `json:"bootOk"`
	KernelChanged bool           `json:"kernelChanged"`
	APKNew        []string       `json:"apkNew"`
	Stale         []host.Process `json:"stale"`
	Removed       []string       `json:"removed"`
	GRUB          bool           `json:"grub"`
	GRUBEFI       bool           `json:"grubEfi"`
	UpdateKernel  bool           `json:"updateKernel"` // diskless/data: a kernel or boot loader changed
}

var (
	kernelPackage   = regexp.MustCompile(`^linux-(lts|virt|edge|stable|rpi\d*)$`)
	firmwarePackage = regexp.MustCompile(`^linux-firmware(-.+)?$`)
	bootloaders     = map[string]bool{"grub": true, "grub-bios": true, "grub-efi": true, "syslinux": true,
		"raspberrypi-bootloader": true, "raspberrypi-bootloader-common": true, "u-boot": true}
)

// RebootPackage reports whether a package affects booting: a kernel, firmware or a boot loader.
func RebootPackage(name string) bool {
	return bootChainPackage(name) || firmwarePackage.MatchString(name)
}

// bootChainPackage reports whether installing a package writes to /boot: a kernel or a boot loader. Firmware is loaded
// at boot (a restart is due), but it is installed under /lib/firmware.
func bootChainPackage(name string) bool {
	return kernelPackage.MatchString(name) || bootloaders[name]
}

// changesPackage reports whether one of the changes changes a package for which match holds.
func changesPackage(changes []apk.Change, match func(name string) bool) bool {
	for _, c := range changes {
		if c.Verb != "Downloading" && match(c.Name) {
			return true
		}
	}
	return false
}

// bootPackageChanged reports whether the change lines of out (apk's output, padded counters included) change a kernel,
// firmware or a boot loader.
func bootPackageChanged(out string) bool {
	return changesPackage(apk.ParseSimulation(out).Changes, RebootPackage)
}

// kernelPackageChanged reports whether the change lines of out change a kernel package (linux-lts, linux-virt, ...):
// not firmware, not a boot loader, which come with no kernel.release or initramfs of their own.
func kernelPackageChanged(out string) bool {
	return changesPackage(apk.ParseSimulation(out).Changes, kernelPackage.MatchString)
}

// post computes the checks after change steps that started at since and printed out. KernelChanged says that this
// job's own output changed a kernel package, not that a restart is pending (a kernel an earlier job installed, or the
// administrator did, says nothing about this job): only then does BootOK ask whether /boot holds an initramfs written
// since the job started and a vmlinuz. The restart gate of the page does not rest on it: it follows the live check of
// host.BootStale.
func (e Env) post(since time.Time, out string, release bool) Post {
	p := Post{APKNew: []string{}, Stale: []host.Process{}, Removed: []string{}}
	k := e.Host.Kernel(e.Uname())
	p.Reboot = k.RebootRequired
	p.KernelChanged = k.Applicable && kernelPackageChanged(out)
	p.BootOK = !p.KernelChanged || e.Host.BootFilesFresh(k.Flavor, since)
	if v, err := e.Host.APKNew(); err == nil && v != nil {
		p.APKNew = v
	}
	if v, err := e.Host.StaleProcesses(); err == nil && v != nil {
		p.Stale = v
	}
	if v := apk.ParseSimulation(out).Removed(); v != nil {
		p.Removed = v
	}
	if release {
		p.GRUB, p.GRUBEFI = e.Host.GRUB()
	}
	return p
}

func (r *Run) setPost(p Post) {
	data, _ := json.Marshal(p)
	r.Set(func(s *State) { s.Post = data })
}
