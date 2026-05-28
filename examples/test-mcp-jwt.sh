#!/usr/bin/env bash
set -euo pipefail

PLATFORM_API_PORT=8080
FORWARDER_PORT=8085
COOKIE_JAR="$(mktemp)"
trap 'rm -f "${COOKIE_JAR}"' EXIT

echo "[test] 1. Port-forwarding platform-api (8080) and dex (5556)..."
kubectl -n ach-system port-forward svc/ach-platform-api "${PLATFORM_API_PORT}:80" >/dev/null 2>&1 &
PAPI_PF=$!
kubectl -n dex-system port-forward svc/dex 5556:5556 >/dev/null 2>&1 &
DEX_PF=$!
trap 'kill ${PAPI_PF} ${DEX_PF} 2>/dev/null || true; rm -f "${COOKIE_JAR}"' EXIT
sleep 2

echo "[test] 2. Driving SSO to get a pk_ key..."
LOGIN_LOC="$(curl -sS -o /dev/null -w '%{redirect_url}' \
  -c "${COOKIE_JAR}" \
  "http://localhost:${PLATFORM_API_PORT}/platform/auth/login")"
if [ -z "${LOGIN_LOC}" ]; then
  echo "Error: login redirect empty" >&2
  exit 1
fi

SSO_RESP="$(curl -sS -L \
  --resolve dex.dex-system.svc.cluster.local:5556:127.0.0.1 \
  -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  "${LOGIN_LOC}")"

PK="$(printf '%s' "${SSO_RESP}" | jq -r '.plaintext // empty' 2>/dev/null || true)"
KEY_ID="$(printf '%s' "${SSO_RESP}" | jq -r '.key_id // empty' 2>/dev/null || true)"
if [ -z "${PK}" ]; then
  echo "Error: SSO login failed to get personal key" >&2
  exit 1
fi
echo "[test]   Mints personal key: ${KEY_ID}"

# Kill previous port-forwards
kill ${PAPI_PF} ${DEX_PF} 2>/dev/null || true

echo "[test] 3. Port-forwarding ach-forwarder (8085:80)..."
kubectl -n ach-system port-forward svc/ach-forwarder "${FORWARDER_PORT}:80" >/dev/null 2>&1 &
FWD_PF=$!
trap 'kill ${FWD_PF} 2>/dev/null || true; rm -f "${COOKIE_JAR}"' EXIT
sleep 2

echo "[test] 4. Making request to the forwarder /mcp/mcp-echo/tools..."
curl -sS -X GET \
  -H "x-ach-key: ${PK}" \
  "http://localhost:${FORWARDER_PORT}/mcp/mcp-echo/tools" > /dev/null || true

echo "[test] Request sent! Let us see what the echo server received:"
echo "--------------------------------------------------------"
kubectl logs -n mcp deploy/mcp-echo --tail=50
echo "--------------------------------------------------------"

kill ${FWD_PF} 2>/dev/null || true
trap - EXIT
