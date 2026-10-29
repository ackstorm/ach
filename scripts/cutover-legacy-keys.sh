#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# cutover-legacy-keys.sh — delete the legacy standalone LiteLLM keys minted by
# alitellm-auth, at the console cutover (spec D-31).
#
# Ownership rule — the ONLY selector, do not add to it:
#   * a key carrying ANY metadata.ach_issuer is ACH-owned (this release or
#     another) and is NEVER touched;
#   * a key whose metadata.source == "token-factory" is alitellm-auth's and IS
#     swept;
#   * a key with no such marker is foreign and is NEVER touched.
#
# The marker is pinned from alitellm-auth (src/api/app/litellm_client.py, read
# 2026-09-22): `generate_litellm_key` stamps metadata.source "token-factory" on
# every key it mints (:425) and on the shared team (:289), and alitellm-auth
# itself uses that same field as its "is this key mine" test (:489-493).
#
# DO NOT select on the key_alias prefix. alitellm-auth has used at least three
# alias forms — "tf-{timestamp}-{email}" (oldest), the opaque "lk-{random}"
# (:412), and the friendly "key-YYYY-MM-DD-HHMMSS" kept in metadata.key_alias
# under D-10 — so the alias is not a stable ownership marker. It is printed in
# the dry-run output for the human reviewing the candidate list, nothing more.
# team_alias is not consulted either: it is settings.litellm_default_team
# (config.py:33, default the literal "default"), far too generic to own a key.
#
# CORRECTION — the cutover plan's Global Constraints are WRONG on this point and
# are superseded here by the owner's ruling of 2026-09-22. The plan says
# "foreign non-alitellm keys (dashboard tf-*, token-factory) are never touched",
# which mislabels alitellm-auth's OWN ownership marker as foreign. Followed
# literally, the sweep would skip the very keys D-31 exists to delete and then
# report "candidates: 0". Do not "fix" this back to the plan's text.
# (docs/superpowers/plans/2026-09-21-console-phase4-cutover.md, Global Constraints)
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
      | select(.metadata.ach_issuer == null or .metadata.ach_issuer == "")   # never an ACH key
      | select(.metadata.source == "token-factory")                          # the alitellm-auth marker
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
