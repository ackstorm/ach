#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# cutover-legacy-keys.sh — delete the legacy standalone LiteLLM keys minted by
# alitellm-auth, at the console cutover (spec D-31).
#
# Ownership rule (identical to the operator's orphan reaper):
#   * a key carrying ANY metadata.ach_issuer is ACH-owned (this release or
#     another) and is NEVER touched;
#   * a key with no alitellm-auth marker is foreign and is NEVER touched.
#
# Markers, pinned from alitellm-auth `generate_litellm_key`
# (src/api/app/litellm_client.py, read 2026-09-22):
#   * metadata.source == "token-factory"  (litellm_client.py:425 — set on every
#     key that function mints; the same marker is stamped on the shared team at
#     litellm_client.py:289)
#   * key_alias prefix "lk-"              (litellm_client.py:412 —
#     f"lk-{secrets.token_hex(8)}", the globally-unique opaque alias; the
#     friendly name lives in metadata.key_alias)
# The draft plan guessed a "team-" team_alias prefix: that is WRONG. The team
# alias is settings.litellm_default_team (config.py:33, default "default"), far
# too generic to use as an ownership marker, so team_alias is not consulted.
# "tf-{timestamp}-{email}" was alitellm-auth's OLDER key_alias format
# (superseded before the "lk-" scheme). It is NOT matched here because the plan
# lists tf-* under "never touched"; if pre-"lk-" keys still exist in production
# the owner must decide explicitly before they are swept.
#
# Dry-run by default: prints only count / alias / user_id — never token values.
# Re-run with --apply to delete.
#
# Env: LITELLM_URL, LITELLM_MASTER_KEY.
set -euo pipefail

: "${LITELLM_URL:?LITELLM_URL is required}"
: "${LITELLM_MASTER_KEY:?LITELLM_MASTER_KEY is required}"

APPLY=${1:-}
if [ -n "$APPLY" ] && [ "$APPLY" != "--apply" ]; then
  echo "usage: $0 [--apply]" >&2
  exit 2
fi

PAGE_SIZE=100
# Bounds the loop against a malformed total_pages (mirrors listAllTeamsPageCap).
PAGE_CAP=200

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
candidates="$work/candidates.tsv"
: >"$candidates"

# /key/list paginates: LiteLLM's default page size is 10, so a single request
# silently drops every key past the 10th and the sweep would report a false
# "0 candidates" while legacy keys stayed alive. Walk every page — same shape as
# internal/litellm/keyinfo.go ListUserKeys.
page=1
while [ "$page" -le "$PAGE_CAP" ]; do
  body=$(curl -fsS -G "$LITELLM_URL/key/list" \
    -H "Authorization: Bearer $LITELLM_MASTER_KEY" \
    --data-urlencode "return_full_object=true" \
    --data-urlencode "include_team_keys=true" \
    --data-urlencode "page=$page" \
    --data-urlencode "size=$PAGE_SIZE")

  printf '%s' "$body" | jq -r '
      .keys[]
      | select((.metadata.ach_issuer // "") == "")              # never an ACH key
      | select(((.metadata.source // "") == "token-factory")
               or ((.key_alias // "") | startswith("lk-")))     # alitellm marker
      | [.token, (.user_id // "-"), (.key_alias // "-"), (.metadata.key_alias // "-")]
      | @tsv' >>"$candidates"

  n=$(printf '%s' "$body" | jq '.keys | length')
  total_pages=$(printf '%s' "$body" | jq '.total_pages // 0')
  if [ "$n" -eq 0 ] || [ "$total_pages" -eq 0 ] || [ "$page" -ge "$total_pages" ]; then
    break
  fi
  page=$((page + 1))
done
if [ "$page" -gt "$PAGE_CAP" ]; then
  echo "WARN: stopped at the page cap ($PAGE_CAP); the sweep may be incomplete" >&2
fi

count=$(wc -l <"$candidates" | tr -d ' ')

if [ "$APPLY" != "--apply" ]; then
  # Redacted: user_id, opaque key_alias, friendly name. No token values.
  printf 'user_id\tkey_alias\tdisplay_name\n'
  cut -f2,3,4 "$candidates"
  echo "candidates: $count" >&2
  echo "dry run; re-run with --apply to delete" >&2
  exit 0
fi

deleted=0
while IFS=$'\t' read -r tok user alias _display; do
  [ -n "$tok" ] || continue
  curl -fsS -X POST "$LITELLM_URL/key/delete" \
    -H "Authorization: Bearer $LITELLM_MASTER_KEY" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg t "$tok" '{keys: [$t]}')" >/dev/null
  deleted=$((deleted + 1))
  echo "deleted $alias ($user)"
done <"$candidates"

echo "deleted: $deleted of $count" >&2
