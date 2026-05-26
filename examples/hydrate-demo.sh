#!/usr/bin/env bash
# examples/hydrate-demo.sh — end-to-end demo of the ACH Hub data plane.
#
# Stand-in for the not-yet-implemented `ach login` + `ach hydrate` CLI
# commands (ROADMAP Phase 6+7). Drives the same wire path the CLI
# eventually will:
#
#   1. Seed the LiteLLM "default" Team.
#   2. kubectl apply -f examples/ — Environment + Plugin + Prompt +
#      LiteLLMConnection CRs.
#   3. Wait for the operator's ExecutionResourcesResolved condition
#      (the §6.6 closed-set marker — no `Ready` rollup yet, see FIX01
#      §C.1).
#   4. Port-forward platform-api (8080) + Dex (5556).
#   5. Drive a Dex SSO round-trip → mint a pk_ via /platform/auth/login
#      → /sso/callback.
#   6. POST /platform/hydrate with environment=demo → save the JSON
#      response.
#
# Output: examples/hydrate.json — the snapshot the CLI will eventually
# return.
#
# Known blockers (see FIX01.md for the full inventory):
#   - FIX01 §A.1 / A.2 / A.3 — provisionUser is currently broken end-
#     to-end against LiteLLM v1.83 (json type mismatch on /user/info,
#     placeholder-user-id catch-all, fail-loud on duplicate /team/
#     member_add). Step 5 will fail with `litellm_unreachable` or
#     `default_team_missing` until those land. Steps 1-4 work today.
#
# Prereqs: cluster brought up via `make cluster-up` (operator +
# platform-api + LiteLLM + Dex + ach-postgres all Running).

set -euo pipefail

NS="${NS:-ach-system}"
ENV_NAME="${ENV_NAME:-demo}"
PLATFORM_API_PORT="${PLATFORM_API_PORT:-8080}"
LITELLM_MASTER_KEY="${LITELLM_MASTER_KEY:-sk-test-master-key}"
OUT="${OUT:-examples/hydrate.json}"
COOKIE_JAR="$(mktemp)"
trap 'rm -f "${COOKIE_JAR}"' EXIT

echo "[hydrate-demo] 1. seeding LiteLLM Team 'default' (idempotent)..."
kubectl -n litellm-system port-forward svc/litellm 4001:4000 >/dev/null 2>&1 &
LITELLM_PF=$!
trap 'kill ${LITELLM_PF} 2>/dev/null || true; rm -f "${COOKIE_JAR}"' EXIT
sleep 2
# LiteLLM auto-assigns team_id as a UUID. ACH's SSO path currently
# hard-codes team_id="default" (FIX01 §A.4) — once that's fixed the
# UUID assignment here is what production deployments will see.
curl -sS -X POST "http://localhost:4001/team/new" \
  -H "Authorization: Bearer ${LITELLM_MASTER_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"team_alias":"default","members_with_roles":[]}' \
  | head -c 500
echo
kill ${LITELLM_PF} 2>/dev/null || true

echo "[hydrate-demo] 2. kubectl apply -f examples/*.yaml..."
kubectl apply -f examples/01-litellmconnection.yaml \
              -f examples/06-plugin-caveman.yaml \
              -f examples/07-prompt-claudecode-leak.yaml \
              -f examples/08-artifact-openclaw-templates.yaml \
              -f examples/04-environment-demo.yaml

echo "[hydrate-demo] 3. waiting for Environment/${ENV_NAME} ExecutionResourcesResolved=True..."
# No `Ready` rollup condition yet on the Environment CR (FIX01 §C.1).
kubectl -n "${NS}" wait --for=condition=ExecutionResourcesResolved \
  "environment/${ENV_NAME}" --timeout=120s || {
    echo "[hydrate-demo] Environment did not converge — dumping status:" >&2
    kubectl -n "${NS}" describe "environment/${ENV_NAME}" >&2
    exit 1
  }

echo "[hydrate-demo] 4. port-forward svc/ach-platform-api ${PLATFORM_API_PORT}:80 + svc/dex 5556:5556..."
kubectl -n "${NS}" port-forward svc/ach-platform-api "${PLATFORM_API_PORT}:80" >/dev/null 2>&1 &
PAPI_PF=$!
kubectl -n dex-system port-forward svc/dex 5556:5556 >/dev/null 2>&1 &
DEX_PF=$!
trap 'kill ${PAPI_PF} ${DEX_PF} 2>/dev/null || true; rm -f "${COOKIE_JAR}"' EXIT
sleep 2

echo "[hydrate-demo] 5. driving Dex SSO (mockCallback → kilgore@kilgore.trout)..."
# 5a. GET /platform/auth/login → 302 to Dex authorize URL.
LOGIN_LOC="$(curl -sS -o /dev/null -w '%{redirect_url}' \
  -c "${COOKIE_JAR}" \
  "http://localhost:${PLATFORM_API_PORT}/platform/auth/login")"
if [ -z "${LOGIN_LOC}" ]; then
  echo "[hydrate-demo] login redirect Location empty — platform-api not reachable?" >&2
  exit 1
fi
# 5b. Platform-api redirects to Dex at the issuer URL
# (http://dex.dex-system.svc.cluster.local:5556/...). Host curl can't
# resolve that DNS name, but the port-forward above bound Dex on
# localhost:5556. Use curl --resolve to alias the in-cluster DNS name
# to 127.0.0.1; Dex generates absolute self-URLs from the issuer so a
# plain host-swap breaks the embedded form action.
SSO_RESP="$(curl -sS -L \
  --resolve dex.dex-system.svc.cluster.local:5556:127.0.0.1 \
  -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  "${LOGIN_LOC}")"
PK="$(printf '%s' "${SSO_RESP}" | jq -r '.plaintext // empty' 2>/dev/null || true)"
KEY_ID="$(printf '%s' "${SSO_RESP}" | jq -r '.key_id // empty' 2>/dev/null || true)"
OWNER="$(printf '%s' "${SSO_RESP}" | jq -r '.owner_email // empty' 2>/dev/null || true)"
if [ -z "${PK}" ]; then
  cat >&2 <<EOF
[hydrate-demo] SSO did not return pk_ plaintext.

Raw response (first 1KB):
$(printf '%s' "${SSO_RESP}" | head -c 1024)

This is the expected failure mode today — see FIX01.md §A for the
inventory of LiteLLM-client + provisionUser bugs that block the SSO
path end-to-end. Steps 1-4 of this script DID succeed: the CRs are
applied, the operator reconciled to ExecutionResourcesResolved=True,
and the platform-api + dex Services are reachable on the port-
forwards. Inspect the cluster state with:

  kubectl -n ${NS} describe environment/${ENV_NAME}
  kubectl -n ${NS} get plugin,prompt,environment
EOF
  exit 1
fi
echo "[hydrate-demo]   pk minted: ${KEY_ID} (owner ${OWNER})"

echo "[hydrate-demo] 6. POST /platform/hydrate environment=${ENV_NAME}..."
mkdir -p "$(dirname "${OUT}")"
curl -sS -X POST \
  -H "Content-Type: application/json" \
  -H "x-ach-key: ${PK}" \
  -d "{\"environment\":\"${ENV_NAME}\"}" \
  "http://localhost:${PLATFORM_API_PORT}/platform/hydrate" \
  | jq . > "${OUT}"

echo "[hydrate-demo] DONE — hydrate JSON written to ${OUT}"
echo "[hydrate-demo] preview:"
head -40 "${OUT}"
