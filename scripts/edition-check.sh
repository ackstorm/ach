#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Only cmd/ach links the Enterprise packages: no other core package imports
# ee/, ach-cli links none, and core UI code imports ui/src/ee only through @ee.
set -euo pipefail
fail() { echo "$1" >&2; exit 1; }
imports=$(go list -f '{{.ImportPath}}{{range .Imports}} {{.}}{{end}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}' ./...)
bad=$(awk '$1 !~ /^github\.com\/ackstorm\/ach\/(ee(\/|$)|cmd\/ach$)/ {
  for (i = 2; i <= NF; i++) if ($i ~ /^github\.com\/ackstorm\/ach\/ee(\/|$)/) print $1 " imports " $i }' <<<"$imports")
[ -z "$bad" ] || { echo "$bad" >&2; fail "core packages import ee/"; }
go list -deps ./cmd/ach | grep -q '/ee/register$' || fail "cmd/ach does not link ee/register"
if go list -deps ./cmd/ach-cli | grep -E '^github\.com/ackstorm/ach/ee(/|$)' >&2; then
  fail "ach-cli links ee/ packages"
fi
[ -d ui/src ] || fail "ui/src missing"
rc=0
hits=$(grep -rnE "['\"](@/|(\.\./)+|\./)([^'\"]*/)?ee/" ui/src --include='*.ts' --include='*.tsx') || rc=$?
[ "$rc" -le 1 ] || fail "ui import scan failed (grep exit $rc)"
hits=$(grep -v '^ui/src/ee/' <<<"$hits" || true)
if [ -n "$hits" ]; then
  echo "$hits" >&2
  fail "core UI imports ui/src/ee directly; use @ee"
fi
cmp -s ee/LICENSE ui/src/ee/LICENSE || fail "licence copies differ"
echo "edition-check OK"
