#!/usr/bin/env bash
# rotate VERSION DATE: moves [Unreleased] into a [VERSION] - DATE section.
# extract VERSION: prints that section's body, the GitHub release notes.
set -euo pipefail

file="${CHANGELOG:-CHANGELOG.md}"
die() { echo "changelog: $1" >&2; exit 1; }

section() { # heading prefix
  awk -v h="$1" '
    index($0, h) == 1 { found = 1; next }
    /^## \[/ { found = 0 }
    found' "$file" | sed -e '/./,$!d' -e ':a' -e '/^\n*$/{$d;N;ba' -e '}'
}

case "${1:-}" in
  rotate)
    version="$2"
    date="$3"
    grep -qF "## [$version]" "$file" && die "$version already has a section"
    [[ -n "$(section '## [Unreleased]')" ]] || die "[Unreleased] is empty"
    awk -v v="$version" -v d="$date" '
      $0 == "## [Unreleased]" { print; print ""; print "## [" v "] - " d; next }
      1' "$file" >"$file.tmp"
    mv "$file.tmp" "$file"
    ;;
  extract)
    notes="$(section "## [$2]")"
    [[ -n "$notes" ]] || die "no section for $2"
    printf '%s\n' "$notes"
    ;;
  *)
    die "usage: changelog.sh rotate VERSION DATE | extract VERSION"
    ;;
esac
