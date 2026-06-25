# SC3 JWT alpha-last opt-out flake — investigation handoff

**Status:** OPEN — needs root-cause + fix by a follow-up agent.
**Discovered:** 2026-06-25, during the `feat/keys-cli-ux` pre-release e2e gate.
**Owner of this report:** keys-cli-ux release work (the failure is orthogonal to
that change — see "Why this is not the keys change" below).

---

## One-line summary

`TestPhase4Invariants/SC3_JwtMintAndBipAlphaLast/alpha_last_optout_suppresses_mint`
fails **deterministically** on a freshly-built kind cluster: after applying an
alpha-last BIP with `forwardIdentityJWT: false`, the forwarder keeps minting a
JWT for ≥30s, so the test times out. The sibling subtest
`later_name_flips_winner_to_mint` (the inverse — make a JWT *appear*) **passes
in <0.5s** on the same cluster.

## Exact failure

```
phase4_invariants_test.go:330: sc3: jwt_present never became false within 30s
  on /mcp/demo-mcp-jwt; last capture={Method:DELETE Path:/ AuthorizationSeen:Bearer eyJhbGc...
  JWTPresent:true JWTClaims:{Iss:http://localhost:8080 Sub:kilgore@kilgore.trout
  Aud:mcp:demo-mcp-jwt Kid:ach-d6ec7e4833310895 ...}}

--- FAIL: TestPhase4Invariants/SC3_JwtMintAndBipAlphaLast (43.09s)
    --- FAIL: .../alpha_last_optout_suppresses_mint (37.11s)
    --- PASS: .../later_name_flips_winner_to_mint (0.14s)
```

Reproduced twice: once in a full `make e2e-full` clean-room run, once via
`make e2e-focus RUN='TestPhase4Invariants/SC3_JwtMintAndBipAlphaLast'` on the
already-warm cluster (so it is **not** a cold-start settling artifact — the
cluster had been up >12 min and the operator >8 min when it failed on retry).

## The smoking-gun asymmetry

The two subtests are mirror images, yet only one fails:

| Subtest | Action | Expected | Result |
|---------|--------|----------|--------|
| `alpha_last_optout_suppresses_mint` | apply alpha-last BIP `forwardIdentityJWT: **false**` | JWT **disappears** (no mint) | ❌ stays minting ≥30s |
| `later_name_flips_winner_to_mint`   | apply later-sorting BIP `forwardIdentityJWT: **true**`  | JWT **appears** (mint)       | ✅ <0.5s |

**"Make a JWT appear" propagates fast; "make a JWT disappear" does not.** That
asymmetry is the core clue. LISTEN/NOTIFY plumbing clearly works (the flip lands
sub-second) — so the problem is specific to the opt-out (`false`) transition.

## Mechanism (how the path works today)

- **Test** (`test/e2e/phase4_sc3_helpers_test.go`):
  - `sc3ApplyBIP(t, name, route, forwardJWT)` applies a throwaway BIP CR.
  - `sc3AssertJWTPresentEventually(...)` polls **30 attempts × 1s sleep** (30s
    budget). Each attempt drives a real echo through the forwarder
    (`/mcp/demo-mcp-jwt`) and reads the mcp-echo `__capture/last` endpoint to
    see whether the forwarder attached `Authorization: Bearer …`.
- **Opt-out subtest setup** (`phase4_invariants_test.go:325-335`): applies
  **two** BIPs back-to-back — `zz-sc3-aaa-jwt-on` (true) then
  `zz-sc3-zzz-jwt-off` (false). Alpha order over the route `demo-mcp-jwt`:
  `bip-demo-mcp-jwt` (synced, true) < `zz-sc3-aaa-jwt-on` (true) <
  `zz-sc3-zzz-jwt-off` (false). The alpha-LAST name wins the tiebreak →
  `zzz-off` should win → **no mint**.
- **Forwarder BIP cache** (`internal/forwarder/bipcache/cache.go`):
  `refreshInterval = 5 * time.Minute`; fast path is `db.RunRefreshLoop` over the
  Postgres `Channel` (LISTEN/NOTIFY `ach_bip_changed`). NOTIFY is at-most-once
  on session loss — a missed NOTIFY means waiting up to **5 min** for the
  periodic refresh, far past the test's 30s window.
- **Operator** projects BIP CRs → `backend_identity_policies` table, emitting
  `NOTIFY ach_bip_changed` from the same tx via `with_tx_notify`.

## Leading hypotheses (for the investigator to confirm/refute)

1. **Two-BIP NOTIFY coalescing.** The opt-out setup applies `aaa-on` and
   `zzz-off` in quick succession. If the forwarder's refresh loop debounces /
   coalesces the two NOTIFYs and reads an intermediate DB state (only `aaa-on`
   visible, or `zzz-off` not yet committed), it locks in `mint=true` and won't
   re-evaluate until the 5-min periodic refresh. The flip subtest applies only
   **one** BIP, so there is nothing to coalesce — consistent with it passing.
   → Check `db.RunRefreshLoop` debounce/coalesce behavior and whether a single
     refresh after two rapid NOTIFYs reads the final committed state.

2. **Winner-selection re-evaluation bug on opt-out.** The forwarder's alpha-last
   tiebreak may compute the winner correctly when the winner says "mint" but
   fail to *demote* to "no mint" when the alpha-last winner is `false`
   (e.g., a `mint=true` decision cached per-route and only overwritten by
   another `true`, never cleared by a `false`).
   → Audit the BIP winner-selection + per-route mint decision in the forwarder
     JWT mint path (`internal/forwarder/…` mint/route resolution).

3. **Operator projection latency for the `false` row.** Less likely (operator
   logs show no errors/panics; the flip lands fast), but confirm the `zzz-off`
   row actually lands in `backend_identity_policies` with
   `forward_identity_jwt = false` promptly after apply.

4. **Test window too tight / inherently racy.** If root-cause is purely
   propagation latency with no logic bug, the 30s budget may simply be too
   small for the opt-out path. A fix could widen the budget OR make the test
   force a forwarder cache refresh. **Prefer fixing the real asymmetry first** —
   widening the window would mask hypothesis 1/2 if one of those is real.

## Reproduction

```bash
make e2e-full                 # clean-room; SC3 fails in the Phase4 block, cluster kept
# or, on a warm cluster:
make e2e-focus RUN='TestPhase4Invariants/SC3_JwtMintAndBipAlphaLast'
```

Live diagnosis while the cluster is up (everything is in the `ach-system`
namespace, not `ach`):

```bash
./scripts/dev.sh kubectl get backendidentitypolicies -n ach-system
./scripts/dev.sh kubectl logs -n ach-system -l app.kubernetes.io/component=forwarder --tail=200
# BIP projection rows (need the PG password from the chart/secret, not bare psql):
./scripts/dev.sh kubectl exec -n ach-system ach-postgres-0 -- \
  env PGPASSWORD=<pw> psql -U ach -d ach \
  -c "select name, forward_identity_jwt, mcp_route from backend_identity_policies order by name;"
```

Note: SC3's `t.Cleanup` removes its throwaway BIPs, so after the run the table
is back to the synced baseline — to inspect mid-test state, add a breakpoint /
sleep or query during the 30s poll window.

## Relevant code

- Test: `test/e2e/phase4_invariants_test.go:301-355`
  (`testPhase4SC3JwtMintAndBipAlphaLast`)
- Test helpers: `test/e2e/phase4_sc3_helpers_test.go`
  (`sc3ApplyBIP` :27, `sc3AssertJWTPresentEventually` :81 — 30×1s loop)
- Forwarder BIP cache: `internal/forwarder/bipcache/cache.go`
  (`refreshInterval = 5 * time.Minute` :24, `RunRefreshLoop` :107)
- Forwarder JWT mint / route resolution: `internal/forwarder/` (winner-selection
  + per-route mint decision — the place hypothesis 2 lives)
- Operator BIP projection + `with_tx_notify` (emits `ach_bip_changed`)

## Why this is NOT the keys-cli-ux change

The `feat/keys-cli-ux` diff touches only:
`internal/db/personal_keys_revoke.go`,
`internal/platformapi/envkeys/{revoke_personal,mount}.go`,
`cmd/ach-cli/cmd/keys.go`, `internal/cli/render/ek.go`, and docs. It does **not**
touch the forwarder, BIP cache, JWT mint, operator reconcile, or envstore. The
SC3 failure is independent of it. It is also pre-documented as a known
flake (auto-memory `e2e-clusterup-flake-and-sc3`: "SC3 alpha-last opt-out flakes
on degraded clusters") — this report upgrades that note from "flaky" to
"deterministic on a fresh cluster, with the appear/disappear asymmetry isolated."

All other e2e coverage passed in the same run: Phase4 (header rewrite, mcp/a2a
precheck, ek tag injection, JWT validate round-trips, the flip sibling, JWKS/RBAC,
promotion/finalizer/GitOps-takeover), Phase5 (content sendfile, error matrix,
plugin precedence, staleness, metrics, invalid fixtures, plugin filter), and
Phase6 (projection idempotence/lifecycle across all adapters).
