package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/fetch"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/releases"
	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/secdb"
)

func (e Env) cache(name string) string { return filepath.Join(e.Base, "cache", name) }

func (e Env) writeCache(name string, data []byte) error {
	dir := filepath.Join(e.Base, "cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	return WriteFile(e.cache(name), data, 0o644)
}

// fetchMetadata fetches releases.json and the secdb files of branch into the cache. Each file is parsed before it
// replaces the cached one; a failure keeps the old file and is returned for the log.
func (e Env) fetchMetadata(ctx context.Context, branch string) []error {
	var errs []error
	get := func(url string, limit int64, parse func([]byte) error, name string) {
		data, err := fetch.Get(ctx, e.HTTP, url, limit)
		if err == nil {
			err = parse(data)
		}
		if err == nil {
			err = e.writeCache(name, data)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	get(e.ReleasesURL, releases.Limit, func(b []byte) error { _, err := releases.Parse(b); return err }, "releases.json")
	if branch == "" {
		return errs
	}
	for _, repo := range []string{"main", "community"} {
		get(e.SecDBURL(branch, repo), secdb.Limit, func(b []byte) error { _, err := secdb.Parse(b); return err },
			"secdb-"+branch+"-"+repo+".json")
	}
	return errs
}

// CachedReleases reads the cached releases.json and when it was fetched.
func (e Env) CachedReleases() (*releases.Index, time.Time, error) {
	fi, err := os.Stat(e.cache("releases.json"))
	if err != nil {
		return nil, time.Time{}, err
	}
	data, err := os.ReadFile(e.cache("releases.json"))
	if err != nil {
		return nil, time.Time{}, err
	}
	ix, err := releases.Parse(data)
	if err != nil {
		return nil, time.Time{}, err
	}
	return ix, fi.ModTime(), nil
}

// Targets are the release upgrades the cached release list allows from the installed branch: the next stable branch,
// if it has not reached its end of life (never a later one instead), and edge. There are none without the list.
func (e Env) Targets() ([]releases.Target, error) {
	ix, _, err := e.CachedReleases()
	if err != nil {
		return nil, errors.New("no release list: check for updates first")
	}
	release, _ := e.Host.Release()
	return ix.Targets(releases.BranchOf(release), e.Now()), nil
}

// allowed checks target against Targets.
func (e Env) allowed(target string) error {
	targets, err := e.Targets()
	if err != nil {
		return err
	}
	for _, t := range targets {
		if t.Branch == target {
			return nil
		}
	}
	return errors.New("the target is not allowed")
}

// CachedSecDB merges the cached main and community secdb of branch (empty if not cached).
func (e Env) CachedSecDB(branch string) secdb.Index {
	ix := secdb.Index{}
	for _, repo := range []string{"main", "community"} {
		if data, err := os.ReadFile(e.cache("secdb-" + branch + "-" + repo + ".json")); err == nil {
			if one, err := secdb.Parse(data); err == nil {
				ix.Merge(one)
			}
		}
	}
	return ix
}

// CachedUpgradable reads the list the last refresh or change cached, and when.
func (e Env) CachedUpgradable() ([]apk.Upgrade, time.Time, error) {
	fi, err := os.Stat(e.cache("upgradable.txt"))
	if err != nil {
		return nil, time.Time{}, err
	}
	data, err := os.ReadFile(e.cache("upgradable.txt"))
	if err != nil {
		return nil, time.Time{}, err
	}
	ups, _ := apk.ParseUpgradable(string(data))
	return ups, fi.ModTime(), nil
}

// refreshList caches apk list --upgradable (read-only: no lock, no root needed).
func (e Env) refreshList(ctx context.Context) error {
	out, exit, err := e.Runner.Run(ctx, "list", "--upgradable")
	if err != nil {
		return err
	}
	if exit != 0 {
		return errors.New("apk list failed")
	}
	return e.writeCache("upgradable.txt", []byte(out))
}
