package main

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/Pukka922/edelweiss-ext-systemupdate/internal/apk"
	"github.com/Pukka922/edelweiss/sdk"
)

// sdk.New panics on an invalid manifest, so building the extension proves the embedded manifest.json is valid. The
// committed manifest holds the placeholder version: a release gets its version from the git tag (manifestWithVersion).
func TestManifest(t *testing.T) {
	ext := sdk.New(sdk.Options{Manifest: manifest})
	m := ext.Manifest
	if m.ID != "systemupdate" {
		t.Fatalf("manifest id %q, want systemupdate", m.ID)
	}
	if m.Version != "0.0.0-dev" {
		t.Fatalf("manifest version %q, want the placeholder 0.0.0-dev", m.Version)
	}
	if m.Menu.Section != "system" {
		t.Fatalf("menu section %q, want system", m.Menu.Section)
	}
	for _, lang := range []string{"de", "en"} {
		if got := m.Name[lang]; got != "Updates" {
			t.Fatalf("name[%s] = %q, want Updates", lang, got)
		}
	}
}

// Without a release version the manifest stays as it is.
func TestManifestWithoutVersion(t *testing.T) {
	got, err := manifestWithVersion(manifest, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, manifest) {
		t.Fatalf("manifestWithVersion(manifest, \"\") = %q, want the manifest unchanged", got)
	}
}

// A release version replaces the version field and nothing else: the parsed manifest equals the one of manifest.json
// with its version set, and so do the JSON objects (which also holds for fields the SDK does not know).
func TestManifestWithVersion(t *testing.T) {
	const v = "0.2.0-rc.1"
	raw, err := manifestWithVersion(manifest, v)
	if err != nil {
		t.Fatal(err)
	}
	got := sdk.New(sdk.Options{Manifest: raw}).Manifest
	if got.Version != v {
		t.Fatalf("manifest version %q, want %s", got.Version, v)
	}
	want := sdk.New(sdk.Options{Manifest: manifest}).Manifest
	want.Version = v
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest = %+v, want the manifest.json one with version %s: %+v", got, v, want)
	}
	var gotFields, wantFields map[string]any
	if err := json.Unmarshal(raw, &gotFields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifest, &wantFields); err != nil {
		t.Fatal(err)
	}
	wantFields["version"] = v
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("manifest fields = %v, want %v", gotFields, wantFields)
	}
}

func TestManifestWithVersionInvalidJSON(t *testing.T) {
	for _, raw := range []string{"{", "[]", "null"} {
		if got, err := manifestWithVersion([]byte(raw), "0.2.0"); err == nil {
			t.Fatalf("manifestWithVersion(%q) returned %q and no error", raw, got)
		}
	}
}

var proxyNames = []string{"http_proxy", "https_proxy", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}

// noProxyVars unsets the proxy variables for the test; t.Setenv puts the original values back at its end.
func noProxyVars(t *testing.T) {
	t.Helper()
	for _, k := range proxyNames {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
}

// The runner and apk get apk's fixed environment plus the proxy variables of this process, and nothing else.
func TestRunnerEnv(t *testing.T) {
	noProxyVars(t)
	t.Setenv("https_proxy", "http://proxy.example.org:3128")
	t.Setenv("NO_PROXY", "localhost,.example.org")
	t.Setenv("EDELWEISS_UNRELATED", "x")
	fixed := slices.Clone(apk.Env)

	want := append(slices.Clone(apk.Env), "https_proxy=http://proxy.example.org:3128", "NO_PROXY=localhost,.example.org")
	if got := runnerEnv(); !slices.Equal(got, want) {
		t.Fatalf("runnerEnv() = %q, want %q", got, want)
	}
	if !slices.Equal(apk.Env, fixed) {
		t.Fatalf("apk.Env changed to %q", apk.Env)
	}
	if got := runnerConfig().Env; !slices.Equal(got, want) {
		t.Fatalf("the runner's environment = %q, want %q", got, want)
	}
}

func TestRunnerEnvCarriesEveryProxyVariable(t *testing.T) {
	noProxyVars(t)
	want := slices.Clone(apk.Env)
	for _, k := range proxyNames {
		t.Setenv(k, "v-"+k)
		want = append(want, k+"=v-"+k)
	}
	if got := runnerEnv(); !slices.Equal(got, want) {
		t.Fatalf("runnerEnv() = %q, want %q", got, want)
	}
}

func TestRunnerEnvWithoutProxy(t *testing.T) {
	noProxyVars(t)
	t.Setenv("EDELWEISS_UNRELATED", "x")
	if got := runnerEnv(); !slices.Equal(got, apk.Env) {
		t.Fatalf("runnerEnv() = %q, want %q", got, apk.Env)
	}
}
