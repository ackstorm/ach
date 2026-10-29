#!/usr/bin/env bash
# govulncheck-gate.sh — run govulncheck and compare its CALLED-set against
# the acknowledged residuals in references/security/govulncheck-acknowledged.md.
#
# Exits 0 iff every reachable advisory has an acknowledged row. A NEW
# (unacknowledged) reachable advisory blocks the push; an acknowledged
# advisory that has cleared (no longer reachable) only emits a NOTE asking to
# prune the stale row — it does NOT block. This is one-directional: new
# advisories are the security signal; a cleared advisory is housekeeping.
#
# Used by `make security` (Phase 13 HRD-04). Invoked inside the devtools
# container — relies on the bare `govulncheck` binary being on PATH.

set -euo pipefail

ACK_FILE="references/security/govulncheck-acknowledged.md"

if [[ ! -f "$ACK_FILE" ]]; then
  echo "FAIL: acknowledged file not found: $ACK_FILE" >&2
  exit 1
fi

# Extract the GO-XXXX-XXXX IDs from the table rows in the ack file.
# Table format: `| <#> | GO-YYYY-NNNN | ...` — we grep the second column.
# Empty ack-list is valid and means "expect zero reachable advisories";
# `|| true` keeps `pipefail` from aborting on the no-match grep exit.
EXPECTED=$( { grep -oE '^\| [0-9]+ \| GO-[0-9]{4}-[0-9]+' "$ACK_FILE" || true; } \
  | awk -F'|' '{print $3}' | tr -d ' ' | sort -u)

# Run govulncheck; capture full output so we can both inspect IDs and surface
# the report to humans on mismatch. govulncheck exits 3 when reachable
# advisories exist — that's expected; we override the exit code via the
# comparison below.
set +e
RAW=$(govulncheck ./... 2>&1)
GOVULN_EXIT=$?
set -e

# Extract reachable (CALLED) advisory IDs. The text-mode output lists each
# reachable advisory under a `Vulnerability #N: GO-XXXX-XXXX` header.
# Zero reachable is the happy path; `|| true` keeps pipefail from aborting.
ACTUAL=$( { echo "$RAW" | grep -oE '^Vulnerability #[0-9]+: GO-[0-9]{4}-[0-9]+' || true; } \
  | awk '{print $NF}' | sort -u)

# UNACKED = reachable advisories with no acknowledged row → hard block.
UNACKED=$(comm -13 <(printf '%s\n' "$EXPECTED") <(printf '%s\n' "$ACTUAL") | sed '/^$/d')
# CLEARED = acknowledged rows that no longer reach → warn only (stale ack).
CLEARED=$(comm -23 <(printf '%s\n' "$EXPECTED") <(printf '%s\n' "$ACTUAL") | sed '/^$/d')

if [[ -n "$CLEARED" ]]; then
  echo "govulncheck-gate: NOTE — acknowledged advisories no longer reachable (remove their rows from $ACK_FILE to keep it honest):" >&2
  echo "$CLEARED" >&2
fi

if [[ -z "$UNACKED" ]]; then
  echo "govulncheck-gate: PASS — no unacknowledged reachable advisories."
  exit 0
fi

echo "govulncheck-gate: FAIL — NEW unacknowledged reachable advisories:" >&2
echo "$UNACKED" >&2
echo "" >&2
echo "Fix the underlying issue OR add a reviewer-approved row to $ACK_FILE, then re-run." >&2
echo "" >&2
echo "Full govulncheck output (exit $GOVULN_EXIT):" >&2
echo "$RAW" >&2
exit 1
