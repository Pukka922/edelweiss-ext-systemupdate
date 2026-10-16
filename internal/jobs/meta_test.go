package jobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCachedMetadata(t *testing.T) {
	e := DefaultEnv()
	e.Base = t.TempDir()
	if _, _, err := e.CachedReleases(); err == nil {
		t.Fatal("no cache yet")
	}
	if ix := e.CachedSecDB("v3.23"); len(ix) != 0 {
		t.Fatalf("no secdb yet: %+v", ix)
	}
	writeTree(t, e.Base, map[string]string{
		"cache/releases.json":              releasesJSON,
		"cache/secdb-v3.23-main.json":      `{"packages":[{"pkg":{"name":"openssl","secfixes":{"3.5.4-r0":["CVE-2026-1234"]}}}]}`,
		"cache/secdb-v3.23-community.json": `{"packages":[{"pkg":{"name":"curl","secfixes":{"8.16.0-r1":["CVE-2026-2222 ALPINE-15000"]}}}]}`,
		"cache/upgradable.txt":             "curl-8.16.0-r1 x86_64 {curl} (curl) [upgradable from: curl-8.16.0-r0]\n",
	})
	if ix, fetched, err := e.CachedReleases(); err != nil || ix.LatestStable != "v3.24" || fetched.IsZero() {
		t.Fatalf("CachedReleases = %+v, %v, %v", ix, fetched, err)
	}
	sec := e.CachedSecDB("v3.23")
	if got := sec.Fixes("curl", "8.16.0-r0", "8.16.0-r1"); len(got) != 2 {
		t.Fatalf("community fixes = %v", got)
	}
	if got := sec.Fixes("openssl", "3.5.3-r0", "3.5.4-r0"); len(got) != 1 {
		t.Fatalf("main fixes = %v", got)
	}
	if ups, _, err := e.CachedUpgradable(); err != nil || len(ups) != 1 || ups[0].New != "8.16.0-r1" {
		t.Fatalf("CachedUpgradable = %+v, %v", ups, err)
	}
}

func TestFetchMetadataKeepsTheOldCacheOnJunk(t *testing.T) {
	junk := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case junk:
			_, _ = w.Write([]byte("<html>captive portal</html>"))
		case r.URL.Path == "/releases.json":
			_, _ = w.Write([]byte(releasesJSON))
		default:
			_, _ = w.Write([]byte(`{"packages":[]}`))
		}
	}))
	defer srv.Close()
	e := DefaultEnv()
	e.Base, e.HTTP = t.TempDir(), srv.Client()
	e.ReleasesURL = srv.URL + "/releases.json"
	e.SecDBURL = func(b, r string) string { return srv.URL + "/" + b + "/" + r + ".json" }
	if errs := e.fetchMetadata(context.Background(), "v3.23"); len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, name := range []string{"releases.json", "secdb-v3.23-main.json", "secdb-v3.23-community.json"} {
		if fi, err := os.Stat(filepath.Join(e.Base, "cache", name)); err != nil || fi.Mode().Perm() != 0o644 {
			t.Fatalf("%s: %v, %v", name, fi, err)
		}
	}
	junk = true
	if errs := e.fetchMetadata(context.Background(), "v3.23"); len(errs) != 3 {
		t.Fatalf("errors = %v", errs)
	}
	if ix, _, err := e.CachedReleases(); err != nil || ix.LatestStable != "v3.24" {
		t.Fatalf("the old cache must stay: %+v, %v", ix, err)
	}
}
