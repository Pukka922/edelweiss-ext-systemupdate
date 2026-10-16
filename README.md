# edelweiss-ext-systemupdate

> **Note:** This project is vibe coded, for lack of time, and made only for my personal use: to manage my Raspberry Pi
> more comfortably. Anyone is welcome to use it, but at their own risk.

Package updates and release upgrades for Alpine Linux, as an extension of
[edelweiss](https://github.com/Pukka922/edelweiss), the web administration interface for Alpine Linux. Its page
**Updates** (menu section System) shows:

- the installed release, the mode of the system (installed to disk, diskless or data mode) and when it was last
  checked;
- banners for a new Alpine release (or for the end of life of the next stable branch, which is then not offered), for
  the end of life of the installed branch or of one of its repositories (also 60 days ahead), for a needed restart and
  for a release upgrade that was left unfinished;
- the list of updates, with badges for security fixes and for packages that need a restart (kernel, firmware, boot
  loader);
- **Install all**, which shows a simulation first: the packages, what would be removed or downgraded, the download size
  and the space needed;
- the **release upgrade** to the next stable branch (for example 3.23 to 3.24) or to `edge` (confirmed by typing
  `edge`), with pre-flight checks and a simulation first;
- live jobs: an update or a release upgrade keeps running when the page is reloaded, admin mode is switched off or
  edelweissd stops, and its log can be followed again afterwards;
- the history of the last jobs.

Everybody who may open the page sees the state; checking for updates, installing and the release upgrade need admin
mode (a user who cannot become admin sees the buttons disabled, with a hint). Texts are in English and German. On a diskless or data mode system, package updates are followed by `lbu commit`;
a release upgrade is only explained there (new boot media), not run.

It is one static binary with a Go backend (`main.go`, `routes.go`, `internal/`) and a SolidJS interface (`ui/`), built
with the edelweiss SDKs.
[Developing edelweiss extensions](https://github.com/Pukka922/edelweiss/blob/main/docs/extension-development.md)
explains its parts. The build scopes the style sheet of the extension to its container
(`edelweiss-ext-systemupdate`); the "Styles" section of `docs/extension-development.md` explains how.

## Requirements

- edelweiss 0.2.0 or newer: the build scopes the style sheet of the extension to its container, which edelweiss 0.1.0
  does not render, so there the extension would show without its styles.
- Alpine Linux 3.22 or newer, with apk-tools 2 or 3 (the integration tests run on 3.22, 3.23 and 3.24).
- A release upgrade only on a system installed to disk (sys mode).

## Installation

On the edelweiss host, as root:

```sh
edelweissd ext install Pukka922/edelweiss-ext-systemupdate
```

or in the web interface under **Settings → Extensions → Install from GitHub** (admin mode). edelweiss shows the
release, the file and its SHA-256 together with a warning, and installs only after you confirm. Updates:
`edelweissd ext update systemupdate`, or the extensions page.

## How it works

**Jobs run in a detached runner.** A check for updates, an upgrade, a release upgrade and its resumption are jobs. The
extension's own process never makes the changes: it starts its own binary as
`edelweiss-ext-systemupdate --run-job <dir>`, in a session of its own, so that stopping the extension (admin mode ends,
the session expires, the extension is updated, edelweissd restarts) does not stop apk in the middle of a transaction.
apk writes straight into the files of the job below `/var/lib/edelweiss-ext-systemupdate`, never into a pipe:

- `jobs/<id>/spec.json`, `state.json`, `log` and `progress`: what the job is to do, where it is, apk's output and
  apk's progress. The page reads these files, so a reload (or another browser) attaches to the same job at the same
  place in the log. Admins read the log; credentials in URLs are redacted. The newest 20 jobs and at most 64 MiB are
  kept, and the log of a running job is never truncated.
- `cache/`: the release list, the security database and the list of updates of the last check.
- `plans/`: the previews of the last 15 minutes. "Install all" and a release upgrade start only from a fresh preview;
  a check and a resume start without one.

A job whose runner died (a crash, a restart of the host) is shown as interrupted.

**One job at a time.** The runner holds `/run/edelweiss-systemupdate.lock`; a second start is refused and names the
running job. If apk runs elsewhere (an `apk add vim` in an ssh session), apk's own database lock is taken: a preview
says who holds it ("waiting for `apk add vim` (pid 1234)") instead of failing with a generic error, and a job waits up
to five minutes for it.

**apk is called directly.** With the absolute path `/sbin/apk`, no shell, `--no-interactive` and a fixed environment
(`PATH`, `LC_ALL=C`; proxy variables of the extension are passed on). A job runs apk in two steps: apk-tools first
(`upgrade --self-upgrade-only`), then the rest (`upgrade --no-self-upgrade`, or `upgrade -a --no-self-upgrade` for a
release upgrade). Previews are simulations (`upgrade -s -vv`) and change nothing on the host except apk's index
cache.

**The release upgrade** goes to the next stable branch, if it has not reached its end of life (if it has, the page
offers no stable target and says so in a banner: never a jump over a branch), or to `edge`, which the server only starts with the typed
confirmation `edge`. In short:

1. The preview (P1 to P4) checks the host (sys mode, a mirror that answers, free space on `/` and `/boot`, `/boot`
   mounted writable where the host needs it, no separate `/usr` from 3.24 on, repositories that all point to the
   installed branch) and simulates the upgrade with a temporary repository file: the rewritten
   `/etc/apk/repositories` plus the `repositories.d` files. Nothing on the host changes except apk's index cache.
2. R1: the repository files must still be those the preview saw.
3. R2: the current branch is brought up to date first, which also brings the newest `alpine-keys`. If that fails, the
   job ends as "failed" and nothing is switched.
4. R3: `/etc/apk/repositories` is saved to `/etc/apk/repositories.edelweiss-<job id>`, then only its branch segment
   is changed. Everything else stays as it was, byte for byte, and so do the lines with a tag (`@edge`, `@testing`):
   they are pins.
5. R4: `apk update`.
6. R5 to R7: the hash of `/lib/apk/db/installed` is noted, then apk-tools is upgraded, then everything (`upgrade -a`).
7. R8: `sync`, then the checks of the result: restart needed, new `*.apk-new` files, processes that still use replaced
   libraries, removed packages and, from 3.24 on with GRUB, the notice to run `grub-install` (the restart button waits
   for the tick that it was run).

**When the repositories are restored, and when the job goes forward.** If R4 fails (a mirror that does not answer, an
index that is not trusted), or R6 or R7 fail while no package has changed (apk printed no change line and the hash of
the installed database is the same), the old repositories are put back, and the job ends as "failed clean". Once a
package has changed, nothing is put back: old repositories with new packages would be a mixed system. The job ends as
"partial" with the notice not to restart, and **Resume** repeats R4 to R8 on the repositories as they are. Resume is
offered only to a branch this page upgrades to: the target of the newest release job, or the next stable branch; not
to a branch the repositories were edited to by hand. A job is never retried by itself.

**The restart waits for the boot files.** After a kernel upgrade the restart button stays disabled, with a notice, while
the boot files of the installed kernel are missing or older than the moment apk installed it (`mkinitfs` failed, or
`/boot` was full or not mounted). That is checked live, whatever job ran since: it clears when `/boot` is fixed or the
host has restarted. "Install all" checks `/boot` (mounted, writable, free space) before it installs a kernel or boot
loader (not firmware, which writes nothing there). It also refuses when `/etc/apk/repositories` names a branch other
than the installed one (lines that follow `latest-stable` are fine): apk would carry the host to that branch without
the checks of a release upgrade.

**What it never does:** run `grub-install`, `update-kernel` or `setup-bootable`; restart services (apk and OpenRC do
not either); restart the host without your confirmation; retry by itself. A host without a kernel package, such as a
container or the dev environment, never shows "restart required".

**Metadata** comes from alpinelinux.org: `releases.json` (branches, end of life, releases) and the security database
`secdb.alpinelinux.org/<branch>/{main,community}.json`. Only the runner, as root, fetches it (https only, redirects only
to the same host, 15 s timeout, size caps of 1 MiB and 8 MiB), when you press "Check now" and when the page is opened in
admin mode and the last check is older than an hour. It is cached. It only steers banners, badges and the offered
targets; what is installed is decided by apk's solver, with its signature checks. Without network access the page still
lists the updates, without security badges, and offers no release upgrade.

## Building

Requirements: Go 1.26, Node.js 22 with npm, make and a C compiler (`gcc`: `make test` runs the Go tests with the race
detector, which needs cgo). `make test-alpine` also needs Docker and network access.

```sh
make build                    # the UI, then static binaries for amd64 and arm64 in build/
make test                     # Go (race detector), vitest and the test of the release guards (scripts/)
make test-alpine              # the integration tests in throwaway Alpine containers (see Tests)
make lint                     # go vet, gofmt, Biome, TypeScript, actionlint
make release VERSION=0.1.0    # release files in dist/: edelweiss-ext-systemupdate_0.1.0_linux_<amd64|arm64>, checksums.txt
```

The Go module requires `github.com/Pukka922/edelweiss/sdk`, and `ui/package.json` names the JS SDK by its download
URL at the edelweiss release. Both come from GitHub (the Go module proxy and `npm ci`), so a fresh clone builds with
`make build test lint`.

## Development

To develop against an edelweiss checkout next to this repository (`../edelweiss`; `EDELWEISS_DIR=<path>` for another
place) instead of the published SDKs, for example for an SDK change that is not released yet:

```sh
make dev-sdk
make build test lint
```

`make dev-sdk`:

- writes a `go.work` (git-ignored) whose `replace` points the Go SDK to `../edelweiss/sdk`;
- builds the JS SDK tarball there (`make sdk-pack`);
- installs it into `ui/node_modules` with `npm install --no-save --no-package-lock`, so `ui/package.json` stays
  unchanged and no `package-lock.json` is written.

To switch back to the published SDKs, run `rm -f go.work go.work.sum && rm -rf ui/node_modules`. The next `make`
runs `npm ci`.

To try the extension, start the edelweiss dev environment (`make dev` in `../edelweiss`, then http://localhost:9090)
and install the release file built here into its container (use `linux_arm64` on an arm64 machine). `make dev` also
builds the system overview, so it needs a checkout of
[edelweiss-ext-system](https://github.com/Pukka922/edelweiss-ext-system) at `../edelweiss-ext-system` (or
`EXT_SYSTEM_DIR=<path>` set) and stops with a message if there is none.

```sh
make release VERSION=0.1.0
mkdir -p ../edelweiss/build
cp dist/edelweiss-ext-systemupdate_0.1.0_linux_amd64 ../edelweiss/build/
cd ../edelweiss
make dev
docker compose -f dev/compose.yaml exec edelweiss /work/build/dev/edelweissd ext install \
  /work/build/edelweiss-ext-systemupdate_0.1.0_linux_amd64 --config /work/dev/edelweiss.dev.toml
```

The running edelweissd picks the extension up within a moment. Install again after a change: that replaces the
extension and stops its processes.

- The dev container is an Alpine container: a job really runs apk in it, as root in admin mode. Its changes are gone
  with the container.
- In dev mode a restart only goes to the log: edelweissd writes "the host would reboot now" and does nothing else.
- `make dev` recreates the dev container and with it its installed extensions: install the file again afterwards.

Files have LF line endings (`.gitattributes`, `.editorconfig`), also in a Windows checkout.

## Tests

`make test` runs the unit tests (Go with the race detector, against fakes and temporary trees) and the UI tests. They
never touch the host's apk, `/etc/apk` or `/lib/apk`, nothing in them runs a real `reboot`, `poweroff`, `lbu` or
`grub-install`, and none of them goes online (no alpinelinux.org, no mirror, no GitHub).

`make test-alpine` (`scripts/test-alpine.sh`) runs the integration tests (`integration/`, build tag `integration`)
against the real apk, in throwaway `alpine` containers: one `docker run --rm --init alpine:<tag>` per scenario, as
root, with a test binary built on your machine. It needs network access to the Alpine mirrors (and, for the version
comparison, to secdb.alpinelinux.org), and the first run pulls the images. `TESTS=<text>` runs only the scenarios whose
name contains the text, for example `TESTS=Unreachable make test-alpine`; a filter that matches nothing is an error. A
scenario counts as passed only when its output has the `--- PASS:` line of its test.

| Scenario | Image | What it shows |
|---|---|---|
| `TestUpgradeOnAnOldPointRelease` | `alpine:3.23.0` | the parser reads the real `apk list --upgradable`; preview and upgrade job run; nothing is left to upgrade |
| `TestRunnerSurvivesInAContainer` | `alpine:3.23.0` | the runner finishes the upgrade after its parent's process group was killed |
| `TestReleaseUpgrade323To324` | `alpine:3.23` | the real release upgrade; repositories and backup as expected |
| `TestReleaseUpgrade322To323` | `alpine:3.22` | the same, one branch earlier |
| `TestPreviewChangesNothing` | `alpine:3.23` | the previews simulate, and change none of `/etc/apk/repositories`, `/etc/apk/world` and `/lib/apk/db/installed`; they make no backup of the repositories and no job |
| `TestUnreachableMirrorRestores` | `alpine:3.23` | with the mirror unreachable the release upgrade ends "failed clean" and the repositories are back |
| `TestCompareMatchesApk` | `alpine:3.24` | the version comparison agrees with `apk version -t` on the versions of the security database (v3.24, main and community) and the installed ones; it fails when apk answers fewer than 95 % of the pairs |

The tests change the system they run on (`apk upgrade`, release upgrades, `/etc/hosts`), so they refuse to run unless
`EDELWEISS_ALPINE_TEST=1` is set, `/.dockerenv` and `/etc/alpine-release` exist and the user is root. Anywhere else
(`go test -tags integration ./integration` on your machine) they print a note and pass without doing anything. They
bring a release list of their own, with end-of-life dates in 2099, instead of fetching `releases.json`. `edge` has no
scenario: its rewrite and its typed confirmation are covered by the unit tests.

For the tests, Docker is used for exactly one thing: the throwaway containers of `make test-alpine`. Run no other
`docker` command for them, and never run them against the apk of the machine you work on.

## Releasing

No file needs changing for a release: the git tag is the only source of the version.

1. Check the version with `make release-check VERSION=<version>`. It has to be a plain `x.y.z` or `x.y.z-pre` by the
   rules of Go (no leading zeros, no empty pre-release identifier, no `+build` metadata).
2. Push `main`, then the tag, which decides the version: `git push origin main`, then
   `git tag v<version> && git push origin v<version>`.

`manifest.json` holds the placeholder version `0.0.0-dev`. The release build puts the version of the tag into the
binaries (`-ldflags -X main.version`) and checks it by running the binary for the build machine with
`--edelweiss-manifest`, because edelweiss refuses a binary whose manifest version differs from the version in the name
of the release file. A local `make build` without `VERSION` reports `0.0.0-dev`.

The release workflow (`.github/workflows/release.yml`) has two jobs, so that the build cannot use the write token.
`build` has a read-only token and keeps no credentials in the checkout. It checks the tag name (`v` and then only
letters, digits, `.`, `+` and `-`), then runs three make targets and hands `dist/` on as a workflow artifact:

- `make release-check`: the tag is a plain `x.y.z` or `x.y.z-pre`;
- `make lint test`: the checks of the CI workflow, so a tag on a commit that fails them publishes nothing;
- `make release`: the release files in `dist/`, with the version of the tag in the binaries.

`publish` runs when `build` has succeeded. It holds the write token (gh needs it), builds nothing, needs neither Go nor
Node and keeps no credentials in its checkout either; only the `gh-release` step gets the token, through `GH_TOKEN`. It
downloads the artifact into `dist/` and runs `make gh-release`: it checks that `dist/` holds exactly `checksums.txt` and
the two binaries of this version, that the files match `checksums.txt` and that it lists exactly them, and creates the
GitHub release `v<version>` with `edelweiss-ext-systemupdate_<version>_linux_amd64`,
`edelweiss-ext-systemupdate_<version>_linux_arm64` and `checksums.txt`. A version with a `-` (`0.1.0-rc.1`) is published
as a GitHub pre-release: the installer takes the latest release, which leaves pre-releases out, so an rc is not offered
as an update.

A compromised build could still hand over altered files under the expected names: `checksums.txt` shows that the files
are consistent, not where they come from.

**Published versions are immutable.** Once a release may have been fetched, never move or delete the tag `v<version>`
and never replace or delete the files of the release:

- edelweiss shows users the SHA-256 of the file and refuses a release that changed after they looked at it
  (`release_changed`);
- sum.golang.org records every version of this Go module that has been fetched, so a moved tag makes `go get` fail with
  "checksum mismatch" for everyone, new fetchers included, not only for those who fetched the old one.

A broken release is fixed with a new patch version.

Rerunning a failed workflow is fine as long as the tag stays on the same commit. "Re-run failed jobs" repeats only the
job that failed, and "Re-run all jobs" builds again (the upload of the artifact replaces the one of the first attempt).
`gh release create` fails if the release already exists, so if the workflow had already created the GitHub release,
delete only the release (never the tag) before the rerun. Do that only for a release that nobody can have downloaded
yet; otherwise publish a new patch version.

The CI workflow (`.github/workflows/ci.yml`) runs `make lint test build` on every push and pull request.

## First publication

edelweiss 0.2.0 is published, and this repository uses its SDKs: `go.sum` and `ui/package-lock.json` pin them,
so `go get`, `go mod tidy` and `npm ci` work directly and the workflows build without an edelweiss checkout. What is
left is the repository on GitHub:

1. Create the public repository `Pukka922/edelweiss-ext-systemupdate` on GitHub. Recommended before the first tag: a tag
   ruleset (Settings → Rules → Rulesets, target: tags) that blocks deleting and updating the tags `v*`, so that a
   published version cannot be moved by mistake (see Releasing above). Then push `main` and the tag:

   ```sh
   git remote add origin https://github.com/Pukka922/edelweiss-ext-systemupdate.git
   git push -u origin main
   git tag v0.1.0
   git push origin v0.1.0
   ```

   When the release workflow has finished, `edelweissd ext install Pukka922/edelweiss-ext-systemupdate` installs the
   extension.

## License

MIT, Copyright (c) 2026 Pukka922, see LICENSE.
