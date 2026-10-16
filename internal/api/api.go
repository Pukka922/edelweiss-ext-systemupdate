// Package api serves the extension's HTTP API: the host's update status, the cached update list and release
// targets, previews, jobs and their event streams. Every instance reads; only the admin (root) instance changes
// anything, through jobs that a detached runner carries out.
package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/host"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/jobs"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/releases"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/secdb"
	"github.com/Pukka922/edelweiss/sdk"
)

// API holds the dependencies of the routes. Poll is how often an event stream looks at the job's files (500 ms if
// 0); StartWait how long starting a job waits for its runner to report running (5 s if 0).
type API struct {
	Env       jobs.Env
	Store     jobs.Store
	Launcher  jobs.Launcher
	LockPath  string
	Poll      time.Duration
	StartWait time.Duration
	mu        sync.Mutex // one start at a time
}

// Register adds the routes to r.
func Register(r fiber.Router, a *API) {
	admin := sdk.RequireAdmin()
	r.Get("/status", a.status)
	r.Get("/updates", a.updates)
	r.Get("/releases", a.releases)
	r.Post("/refresh", admin, a.refresh)
	r.Post("/preview", admin, a.preview)
	r.Post("/jobs", admin, a.start)
	r.Get("/jobs/current", a.current)
	r.Get("/jobs", a.list)
	r.Get("/jobs/:id/events", a.checkJob, sdk.SSE(a.events))
	r.Get("/jobs/:id/log", admin, a.checkJob, a.log)
}

func body(c fiber.Ctx, v any) error {
	if err := json.Unmarshal(c.Body(), v); err != nil {
		return sdk.NewError(fiber.StatusBadRequest, "invalid_json", "request body must be valid JSON")
	}
	return nil
}

func fieldError(code, field, message string) error {
	return sdk.NewError(fiber.StatusBadRequest, code, message).WithDetails(map[string]any{"field": field})
}

// Status is the head and the banners of the page. BootStale is the live check that a restart is pending and the boot
// files of the installed kernel are missing or older than the kernel: the page blocks the restart while it holds.
type Status struct {
	Release   string            `json:"release"`
	Branch    string            `json:"branch"`
	Arch      string            `json:"arch"`
	Mode      host.Mode         `json:"mode"`
	Reboot    bool              `json:"reboot"`
	BootStale bool              `json:"bootStale"`
	Checked   *time.Time        `json:"checked,omitempty"`
	Banners   []releases.Banner `json:"banners"`
	Resume    *jobs.Resume      `json:"resume,omitempty"`
	Current   *jobs.State       `json:"current,omitempty"`
	Last      *jobs.State       `json:"last,omitempty"`
}

// arch is the host's apk architecture (/etc/apk/arch), else Go's name of it in apk's words.
func (a *API) arch() string {
	if b, err := os.ReadFile(filepath.Join(a.Env.Root, "etc/apk/arch")); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return runtime.GOARCH
}

// checked is when apk last fetched its indexes: the newest APKINDEX file of its cache (nil if there is none).
func (a *API) checked() *time.Time {
	var newest time.Time
	for _, pattern := range []string{"var/cache/apk/APKINDEX.*", "etc/apk/cache/APKINDEX.*"} {
		matches, _ := filepath.Glob(filepath.Join(a.Env.Root, pattern))
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.ModTime().After(newest) {
				newest = fi.ModTime()
			}
		}
	}
	if newest.IsZero() {
		return nil
	}
	return &newest
}

func (a *API) status(c fiber.Ctx) error {
	release, _ := a.Env.Host.Release()
	st := Status{Release: release, Branch: releases.BranchOf(release), Arch: a.arch(), Banners: []releases.Banner{},
		Checked: a.checked()}
	st.Mode, _ = a.Env.Host.Mode()
	k := a.Env.Host.Kernel(a.Env.Uname())
	st.Reboot, st.BootStale = k.RebootRequired, a.Env.Host.BootStale(k)
	if ix, _, err := a.Env.CachedReleases(); err == nil {
		if b := ix.Banners(st.Branch, a.Env.Now()); b != nil {
			st.Banners = b
		}
	}
	st.Current, _ = a.Store.Current()
	if st.Current == nil {
		st.Resume = a.Env.Resumable(a.Store)
	}
	list, _ := a.Store.List(20)
	for i := range list {
		if list[i].Finished() {
			st.Last = &list[i]
			break
		}
	}
	return c.JSON(st)
}

// Advisory is a security issue an update fixes; Link is set for CVE ids.
type Advisory struct {
	ID   string `json:"id"`
	Link string `json:"link,omitempty"`
}

// Update is a package that can be upgraded.
type Update struct {
	Name     string     `json:"name"`
	Old      string     `json:"old"`
	New      string     `json:"new"`
	Origin   string     `json:"origin"`
	Security []Advisory `json:"security"`
	Reboot   bool       `json:"reboot"`
}

// Updates is the cached list of upgradable packages. SecurityData tells whether a security database was cached:
// without it, no update is marked.
type Updates struct {
	Checked      *time.Time `json:"checked,omitempty"`
	SecurityData bool       `json:"securityData"`
	Items        []Update   `json:"items"`
}

func (a *API) updates(c fiber.Ctx) error {
	out := Updates{Items: []Update{}}
	ups, when, err := a.Env.CachedUpgradable()
	if err != nil {
		return c.JSON(out)
	}
	out.Checked = &when
	release, _ := a.Env.Host.Release()
	sec := a.Env.CachedSecDB(releases.BranchOf(release))
	out.SecurityData = len(sec) > 0
	for _, u := range ups {
		origin := u.Origin
		if origin == "" {
			origin = u.Name
		}
		item := Update{Name: u.Name, Old: u.Old, New: u.New, Origin: origin, Security: []Advisory{},
			Reboot: jobs.RebootPackage(u.Name)}
		for _, id := range sec.Fixes(origin, u.Old, u.New) {
			item.Security = append(item.Security, Advisory{ID: id, Link: secdb.CVELink(id)})
		}
		out.Items = append(out.Items, item)
	}
	return c.JSON(out)
}

// Releases are the release upgrades the cached release list offers.
type Releases struct {
	Available bool              `json:"available"`
	Fetched   *time.Time        `json:"fetched,omitempty"`
	Current   string            `json:"current"`
	Latest    string            `json:"latest,omitempty"`
	Targets   []releases.Target `json:"targets"`
}

func (a *API) releases(c fiber.Ctx) error {
	release, _ := a.Env.Host.Release()
	out := Releases{Current: releases.BranchOf(release), Targets: []releases.Target{}}
	ix, fetched, err := a.Env.CachedReleases()
	if err != nil {
		return c.JSON(out)
	}
	out.Available, out.Fetched, out.Latest = true, &fetched, ix.LatestStable
	if targets, err := a.Env.Targets(); err == nil && targets != nil {
		out.Targets = targets
	}
	return c.JSON(out)
}
