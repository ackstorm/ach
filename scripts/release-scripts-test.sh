#!/usr/bin/env bash
# Self-check for scripts/ci-changes.sh and scripts/changelog.sh.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"
git init -q -b main
git config user.email t@t
git config user.name t

fail() { echo "FAIL: $1" >&2; exit 1; }

mkdir -p deploy/helm/ach internal docs
printf 'version: 1.0.0\nappVersion: v1.0.0\n' >deploy/helm/ach/Chart.yaml
printf 'image:\n  tag: v1.0.0\nreplicas: 1\n' >deploy/helm/ach/values.yaml
echo x >internal/a.go
echo x >docs/a.md
git add -A
git commit -qm base
base=$(git rev-parse HEAD)

expect_code() { # name expected
  git add -A
  git commit -qm "$1"
  got=$("$root/scripts/ci-changes.sh" "$base" HEAD)
  [[ "$got" == "code=$2" ]] || fail "$1: got $got, want code=$2"
  git reset -q --hard "$base"
}

echo y >docs/a.md
expect_code "docs only" false
sed -i 's/1\.0\.0/1.0.1/' deploy/helm/ach/Chart.yaml deploy/helm/ach/values.yaml
expect_code "version lines only" false
sed -i 's/replicas: 1/replicas: 2/' deploy/helm/ach/values.yaml
expect_code "other values change" true
sed -i 's/1\.0\.0/1.0.1/' deploy/helm/ach/Chart.yaml
echo y >internal/a.go
expect_code "version plus code" true

echo "OK release scripts"
