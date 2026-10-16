#!/bin/sh
# make test-alpine: runs the integration tests (integration/, go test -tags integration) inside throwaway Alpine
# containers, never on this machine. The test binary is built here (static, CGO_ENABLED=0); each scenario runs in a
# container of its own (docker run --rm --init alpine:<tag>, as root), which is removed afterwards. The tests change the
# container's system (apk upgrade, release upgrades) and need network access to the Alpine mirrors.
# A scenario passes only when docker exits 0 and its output has the "--- PASS: <name>" line, so a renamed test cannot
# pass silently. The script fails when no scenario ran.
# TESTS=<text> runs only the scenarios whose name contains it.
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
OUT=$(mktemp -d)
trap 'rm -rf "$WORK" "$OUT"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
GO=${GO:-go}
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$("$GO" env GOARCH)" "$GO" test -c -tags integration \
  -o "$WORK/integration.test" ./integration)
chmod 0755 "$WORK" "$WORK/integration.test"
failed=0
ran=0
# scenario IMAGE TEST: one test in a fresh container
scenario() {
  case "$2" in *"${TESTS:-}"*) ;; *) return 0 ;; esac
  ran=$((ran + 1))
  printf '== %s on %s\n' "$2" "$1"
  rm -f "$OUT/rc"
  { docker run --rm --init -e EDELWEISS_ALPINE_TEST=1 -v "$WORK:/t:ro" "$1" \
    /t/integration.test -test.run "^$2\$" -test.v -test.count=1 -test.timeout 20m \
    && echo 0 >"$OUT/rc" || echo "$?" >"$OUT/rc"; } 2>&1 | tee "$OUT/log"
  if [ "$(cat "$OUT/rc" 2>/dev/null)" != 0 ] || ! grep -q "^--- PASS: $2 (" "$OUT/log"; then
    printf '== FAILED: %s on %s\n' "$2" "$1" >&2
    failed=1
  fi
}
scenario alpine:3.23.0 TestUpgradeOnAnOldPointRelease
scenario alpine:3.23.0 TestRunnerSurvivesInAContainer
scenario alpine:3.23 TestReleaseUpgrade323To324
scenario alpine:3.22 TestReleaseUpgrade322To323
scenario alpine:3.23 TestPreviewChangesNothing
scenario alpine:3.23 TestUnreachableMirrorRestores
scenario alpine:3.24 TestCompareMatchesApk
if [ "$ran" -eq 0 ]; then
  echo "test-alpine: no scenario matches TESTS=${TESTS:-}" >&2
  exit 1
fi
exit "$failed"
