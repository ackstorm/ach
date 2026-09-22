# Runbook — unified console cutover

Move production off alitellm-auth onto the ACH console, and delete the legacy
standalone `sk-` keys.

> **Rewritten 2026-09-22 against the real gitops repo.** The previous revision
> encoded a topology that does not exist (`apps/ach-identity`, ACH on `api.`,
> LiteLLM needing a host split) and would have misled an operator. Every path
> and host below was read from the gitops repo
> (`ssh://git@git-ssh.ackstorm.com:2222/blueprints/gitops/apps/genai/genai-blueprint.git`)
> on 2026-09-22. **Re-read it before each step** — it is live production.

Full reasoning, per-task detail and the self-review live in
`docs/superpowers/plans/2026-09-21-console-phase4-cutover.md` (gitignored, not
committed). This runbook is the tick-list.

## Ground rules

- **Never `git push` the gitops repo from an agent session.** `apps/` is a Flux
  Kustomization with `prune: true`. A push is a production deploy. Commit
  locally; the owner reviews and pushes.
- Commit style in that repo is Angular (`feat(scope):`, `chore(scope):`) with
  **no attribution trailers** — match `git log`.
- `${GENAI_BASE_DOMAIN}` is a Flux `postBuild.substituteFrom` variable out of the
  `terraform-genai-data` Secret. Leave it written that way; do not substitute it
  in the repo.

## The topology, as it actually is

| Host | Serves | Source |
|---|---|---|
| `ach.${GENAI_BASE_DOMAIN}` | **ACH** — `/` → `ach-gateway:80`. The console lands here. | `apps/ach/base/httproute.yaml` |
| `api.${GENAI_BASE_DOMAIN}` | **LiteLLM** — catch-all `/` → `litellm:4000`, `x-user-id` stripped at the edge | `apps/litellm/base/httproute.yaml` |
| `platform.${GENAI_BASE_DOMAIN}` | **alitellm-auth** — the UI people log in to *today* | `apps/alitellm-auth/base/httproute.yaml` |
| `api.${GENAI_BASE_DOMAIN}/.well-known/oauth-protected-resource` | **alitellm-auth** — RFC 9728 doc on LiteLLM's host | `apps/alitellm-auth/base/httproute-api.yaml` |

**D-18's host split already holds.** ACH and LiteLLM have always been on separate
hosts here. There is no host-split step, and any command that curls
`api.<domain>` expecting the console is wrong.

## Already done — do not redo

| Item | State |
|---|---|
| ACH on the complete chart | **Done.** `apps/ach/` runs the full chart. No `profile: identity` exists anywhere in the gitops repo. |
| LiteLLM on its own host (D-18) | **Done** — and always was. `api.` is LiteLLM's. |
| The `x-user-id` impersonation plugin (D-06) | **Done.** `custom_auth` commented out in gitops `9697a0f`; file, `configMapGenerator` entry, `volumeMount` and comment block removed in `5e08a18`. Awaiting the owner's push. |

## Order

| # | Step | Reversible? | Owner present? |
|---|------|-------------|----------------|
| 0 | **GATE** — cut + publish the console release | published charts cannot be unpublished | yes |
| 1 | Decide the `apps/ach/base/helm.yaml` chart pin | yes | **yes — their call** |
| 2 | Deploy + verify the console on `ach.` | yes, **until the D-29 boundary** | yes |
| 3 | Replace what alitellm-auth does besides its UI | yes | **yes — design decision** |
| 4 | Retire alitellm-auth (D-19) | yes | yes |
| 5 | Delete the legacy standalone `sk-` keys (D-31) | **NO — irreversible** | **yes, gated** |
| 6 | Archive + docs | yes | no |

**Steps 3-6 are BLOCKED until 0 and 2 are green.** Production ACH runs chart
`0.9.10`, which has no console — the console entry is still `[Unreleased]`. Until
the release ships, **alitellm-auth is the login people are using**, and retiring
it or deleting their keys takes away a working UI.

**Never reorder 3 → 4 → 5**: the `api.` host's authorizer lives inside
alitellm-auth, and the legacy keys serve alitellm-auth's users.

---

## Step 0 — GATE: cut and publish the console release

- [ ] Phases 1-3 are on `main`:

```bash
git log --oneline main | grep -E "console session|ek_ suspended|console stats"
make helm-render-check
make e2e-full            # local-only gate; CI does not run e2e
```

- [ ] `CHANGELOG.md` carries the D-29 sentence verbatim under the entry being
      released. Do not reword it.
- [ ] **Read Step 1 before choosing the version** — it interacts with the pin.

```bash
git fetch --tags && gh release list | head -5    # never cut from a stale tag list
make release-cut VERSION=<X.Y.Z>
```

- [ ] The chart is published to `oci://ghcr.io/ackstorm/charts` at that version.

---

## Step 1 — ⚠️ OWNER: the `version: 0.9.x` range is an ungated deploy path

`apps/ach/base/helm.yaml:13` pins **`version: 0.9.x`**, a semver range. Flux polls
the registry every 5 m and the HelmRelease re-resolves every 24 h. **Any `0.9.x`
chart that reaches `oci://ghcr.io/ackstorm/charts` deploys itself to production
within a day — no gitops commit, no diff, no human.**

Two opposite failure modes, same range:

- Console cut as **`0.9.11`** → it **auto-deploys on `helm push`**. The **D-29
  irreversible boundary gets crossed by a registry push instead of a reviewed
  change**, with nothing in the repo recording that production moved.
- Console cut as **`0.10.0`** → the range **never picks it up**. The cutover looks
  like a no-op; the first symptom is a user reporting no console.

- [ ] **Recommendation presented to the owner** (not applied): replace the range
      with the exact console-release version, so the deploy is a reviewed commit
      and rollback is `git revert`. The gitops repo's own `CLAUDE.md` already
      says *"PRODUCCIÓN: fijar versiones exactas"*, so the range is out of policy
      — **but this is the owner's decision, not ours.**

      decision: ______________________  by: ____________  date: ____________

- [ ] If declined: note in Step 2 that there is **no gitops artifact** for the
      deploy and that rollback means publishing an older chart.

---

## Step 2 — deploy + verify the console (owner present)

- [ ] **Snapshot** the ACH Postgres (`ach-postgresql`, ns `ach`). **Last restore
      point below the D-29 boundary.**

      snapshot id: ______________________  taken: ____________  by: ____________

- [ ] **gitops edit** — apply the Step 1 decision in `apps/ach/base/helm.yaml`.
      That is the only ACH-side gitops change this cutover needs; the values are
      already complete. Commit locally, hand to the owner to push.

- [ ] **Verify — run these yourself, from a host with cluster access.**

```bash
kubectl -n ach get helmrelease ach -o jsonpath='{.status.history[0].chartVersion}{"\n"}'
kubectl -n ach rollout status deploy/ach-operator deploy/ach-platform-api \
                              deploy/ach-forwarder deploy/ach-gateway --timeout=5m

# console is at ach., NOT api.
curl -sS -o /dev/null -w '%{http_code}\n' https://ach.${GENAI_BASE_DOMAIN}/
curl -sS https://ach.${GENAI_BASE_DOMAIN}/ | grep -qi 'console not built' \
  && echo 'FAIL: chart built without make ui-build'

# ACH has no catch-all (D-18): 404 at ACH, still live on LiteLLM's own host
for p in /ui /key/list /health /model/info; do
  curl -sS -o /dev/null -w "ach$p %{http_code}\n" https://ach.${GENAI_BASE_DOMAIN}$p
done
curl -sS -o /dev/null -w 'api/health %{http_code}\n' https://api.${GENAI_BASE_DOMAIN}/health

curl -fsS https://ach.${GENAI_BASE_DOMAIN}/.well-known/oauth-authorization-server | jq .issuer
curl -fsS https://ach.${GENAI_BASE_DOMAIN}/.well-known/oauth-protected-resource   | jq .resource
ach-cli login --url https://ach.${GENAI_BASE_DOMAIN} && ach-cli whoami
ach-cli env hydrate <an existing Environment>
kubectl -n ach get environments.ach.ackstorm.ai

# D-29 watch — 0 until a user acts; the first non-zero crosses the boundary
psql "$ACH_DB" -c "select count(*) from environment_keys where status='suspended' or expires_at is not null"
```

- [ ] **Browser (owner):** sign in at `https://ach.${GENAI_BASE_DOMAIN}/` →
      Personal → create an EK → suspend → the ≤60 s notice appears.
      ⚠ **This crosses the D-29 boundary.** Do it deliberately, after the above
      is green.

- [ ] **Soak ≥ one working day** with both UIs up. This is why Steps 3+ are gated.

---

## Step 3 — ⚠️ replace what alitellm-auth does besides its UI

**Retiring alitellm-auth is not `rm -rf apps/alitellm-auth/`.** Read
`apps/alitellm-auth/base/helm.yaml` in full first. It provides four things
nothing else provides:

1. **The Envoy `ext_authz` authorizer for the whole `api.${GENAI_BASE_DOMAIN}`
   host.** `authz.enabled` renders `alitellm-auth-authz…:9001`;
   `infra/istio-system/base/helmrelease-istiod.yaml:100-111` registers it as the
   `alitellm-authz` extensionProvider with **`failOpen: false`**; `istio.enabled`
   renders the CUSTOM `AuthorizationPolicy` on `external-gateway`
   (`exemptPaths: ["/health", "/.well-known/*"]`). Delete alitellm-auth and that
   host either goes dark or goes wide open. **Decide which before touching it.**
2. **LibreChat's only identity path.** `apps/librechat/base/librechat.yaml` sends
   a raw **Dex access token** to `api.<domain>/v1` and `/mcp/vmcp-think`;
   ext_authz (`idpIssuer`/`idpAudience: chat`/`idpSubjectClaim: email`) swaps it
   for that user's LiteLLM key. **ACH's forwarder cannot do this** — it resolves
   `pk_`/`ek_` in a declared header slot, or an `Authorization: Bearer` that is
   *ACH's own* OAuth JWT; a foreign IdP token is forwarded with no ACH identity.
3. **`publicArtifacts`** — the unauthenticated OpenCode catalog at
   `platform.<domain>/public/opencode/api.json`.
4. **`factoryConfig`** — the token factory that minted the keys Step 5 deletes.

- [ ] For each of the four, record: *keep alitellm-auth headless for it* /
      *move it to ACH (name the feature, say whether it exists)* / *drop it and
      accept the consequence*.

      1: ____________  2: ____________  3: ____________  4: ____________

- [ ] The `api.` host keeps an authorizer. If ext_authz goes, LiteLLM's own key
      auth is the only edge gate — **including for `/ui` and `/key/*`.** Check it
      against `ui_access_mode: "admin_only"` and decide explicitly.
- [ ] Document the design in `docs/site/architecture/identity-sso.md` **in the
      same commit** (that repo's convention).

---

## Step 4 — retire alitellm-auth (D-19)

**Precondition:** Step 3's four decisions are implemented; the console has soaked;
users have been told the old login is going away and their standalone keys will be
**deleted in Step 5**.

- [ ] **gitops edits**
      - remove `alitellm-auth` from `apps/kustomization.yaml`;
      - delete `apps/alitellm-auth/`;
      - remove the `alitellm-authz` extensionProvider from
        `infra/istio-system/base/helmrelease-istiod.yaml:96-111` — **only after
        Step 3's replacement is live.** A provider referenced by a surviving
        `AuthorizationPolicy` with `failOpen: false` is an outage;
      - remove/repoint the `platform.${GENAI_BASE_DOMAIN}` DNS record.

- [ ] **`/.well-known/oauth-protected-resource` on `api.` — DECIDED: delete the
      route (`apps/alitellm-auth/base/httproute-api.yaml`). Do NOT re-home it on
      ACH.**

      - ACH's forwarder composes both discovery documents itself —
        `proxy.WellKnownHandler(deps.BaseURL)` (`internal/forwarder/server.go:86`)
        emits `{"resource": base + rest, "authorization_servers": [base]}` where
        `base` is `ACH_BASE_URL` = `https://ach.${GENAI_BASE_DOMAIN}`. **It never
        reads `Host` or `X-Forwarded-Host`.** Served at `api.<domain>` it would
        advertise `resource: https://ach.<domain>`, and RFC 9728 §3.2
        (`docs/developer-guide/jwt-forwarder.md` §1.4) requires the client to
        **reject** metadata whose `resource` is not the resource it addressed.
        Re-homing publishes a document every conforming client must throw away.
      - Post-cutover the OAuth-protected resources are ACH's route families, and
        their documents already exist at the right sibling paths on ACH's host:
        `https://ach.${GENAI_BASE_DOMAIN}/.well-known/oauth-protected-resource[/v1|/gemini|/mcp/<n>|/a2a/<n>]`.
        `api.` is LiteLLM authenticating bare virtual keys — not an OAuth
        resource server — so no PRM belongs there.
      - A `404` is the correct, discoverable signal. A client that hard-codes the
        old URL is fixed by pointing it at ACH's, not by feeding it a bad doc.

      ⚠ **Owner: before the route disappears**, confirm nothing fetches it.
      Gateway access logs for that exact path across the soak window are the
      evidence — `exemptPaths` makes it reachable unauthenticated today, so
      anything could be using it.

      confirmed no consumers by: ______________________  at: ____________

- [ ] **Verify**

```bash
kubectl get deploy -A | grep -i alitellm-auth                                      # none
curl -sS -o /dev/null -w '%{http_code}\n' https://platform.${GENAI_BASE_DOMAIN}/   # gone or 404
curl -sS -o /dev/null -w '%{http_code}\n' \
  https://api.${GENAI_BASE_DOMAIN}/.well-known/oauth-protected-resource            # 404 — intended
curl -fsS https://ach.${GENAI_BASE_DOMAIN}/.well-known/oauth-protected-resource | jq .resource
# api. still authorizes (Step 3); LibreChat still answers; console + ach-cli login still work
```

- [ ] **gitops commit** — `chore: retire alitellm-auth (unified console, ACH spec D-19)`

---

## Step 5 — delete the legacy standalone `sk-` keys (D-31) — ⚠ IRREVERSIBLE, OWNER-GATED

Tool: `scripts/cutover-legacy-keys.sh` (this repo). It exists; do not rewrite it.

Ownership rule (owner ruling 2026-09-22):

- any `metadata.ach_issuer` → ACH-owned → **never touched**;
- `metadata.source == "token-factory"` → alitellm-auth's → **swept**;
- anything else → foreign → **never touched**.

`metadata.source` is the only selector. `key_alias` is **not** a marker
(alitellm-auth has used `tf-…`, `lk-…` and `key-YYYY-MM-DD-HHMMSS`); it is
printed for review only.

- [ ] **Dry run.** `LITELLM_URL` is LiteLLM's endpoint — in-cluster
      `http://litellm.litellm.svc:4000`, or `https://api.${GENAI_BASE_DOMAIN}`.

```bash
LITELLM_URL=... LITELLM_MASTER_KEY=... scripts/cutover-legacy-keys.sh
```

- [ ] **Owner gate.** Paste the candidate list — **count, aliases, users only,
      never token values.** **STOP until the owner says go.**

      owner go given by: ______________________  at: ____________

- [ ] **Apply + verify**

```bash
scripts/cutover-legacy-keys.sh --apply
scripts/cutover-legacy-keys.sh | tail -1                                           # candidates: 0
psql "$ACH_DB" -c "select count(*) from environment_keys where status<>'revoked'"  # unchanged
# a console-created ek_ still forwards; the orphan reaper's next tick reports 0
```

---

## Step 6 — archive + docs

- [ ] **gitops docs**, one commit: `docs/site/ai-gateway/alitellm-auth.md`
      (delete or mark retired, **and drop it from `mkdocs.yml`** —
      `mkdocs --strict` runs in that repo's CI and a dangling nav entry fails the
      pipeline), `docs/site/architecture/identity-sso.md`,
      `docs/site/applications/librechat.md`, `docs/site/security/known-gaps.md`,
      `README.md` (the Mermaid diagram and the service catalog both still list
      `alitellm-auth (platform.)`).
- [ ] **Consider dropping the now-inert `x-user-id` strip**
      (`apps/litellm/base/httproute.yaml:24-31` and the ext_authz header list).
      With `custom_auth` gone LiteLLM ignores the header, so the filter is a
      no-op. **Recommend keeping it** — it costs nothing and is the one guard
      between a re-enabled handler and an internet-supplied identity. Either way,
      fix the comment, which still calls it "LiteLLM's impersonation contract".
- [ ] **Archive alitellm-auth** on GitHub (owner). `references/upstream-sync.md`:
      mark the ported pieces (Den, fixtures, tests, brand marks) with the archive
      date. `NOTICE`: the attribution row **stays** — Apache-2.0 provenance does
      not expire.
- [ ] **ACH docs** — `references/understanding.md` (console at
      `https://ach.${GENAI_BASE_DOMAIN}/`, LiteLLM on `api.`, no plugin, no
      alitellm-auth); one sentence in `CLAUDE.md` / `AGENTS.md` Quick-context.
- [ ] **ACH commit** — `docs: console cutover complete — alitellm-auth retired`

---

## Rollback

| # | Step | Rollback |
|---|------|----------|
| 0 | release cut | Nothing in-cluster until step 2. A published chart cannot be unpublished — see step 1. |
| 1 | pin | Revert the gitops commit. |
| 2 | console deploy | Revert the `apps/ach/base/helm.yaml` commit — **only while the D-29 boundary is uncrossed.** If the pin was declined there is no commit to revert; rollback means publishing an older chart. |
| 3 | authorizer replacement | Revert the gitops commit; alitellm-auth's ext_authz is still running (not deleted until step 4). |
| 4 | alitellm-auth retired | Revert the gitops commits; alitellm-auth redeploys. Legacy keys still exist, so users can log in again. |
| 5 | legacy keys deleted | **None.** Deleted LiteLLM keys cannot be recreated with the same secret. Affected users mint a new `ek_` from the console. |
| 6 | archive + docs | Revert the doc commits; un-archive the repo. |

> ⚠ **Past the D-29 boundary there is no rollback.**
> *"No rollback below `<console release>` once any environment key is suspended
> or carries an expiry: an older orphan worker would reap suspended keys. The
> down migration refuses in that case."*
>
> Recovery past the boundary is **restore from the step 2 snapshot**, accepting
> the loss of everything written since — not a chart/image revert.

---

## Post-cutover checklist (all must be true)

- [ ] `https://ach.${GENAI_BASE_DOMAIN}/` = console; `/ui`, `/key/*`, `/health` =
      404 **at ACH** and still live on `api.` (AC-24)
- [ ] `ach-cli login`, `env hydrate`, `ek_` forwarding unchanged (AC-23)
- [ ] console stats/latency for a real user include `ek_` traffic,
      `data_scope: "user"` (AC-16)
- [ ] no `x_user_id_auth.py`, no `custom_auth`, no alitellm-auth Deployment
      (D-06, D-19)
- [ ] `api.${GENAI_BASE_DOMAIN}` still has an authorizer, and LibreChat works
- [ ] `scripts/cutover-legacy-keys.sh` dry run reports 0 candidates (AC-25)
- [ ] `CHANGELOG.md` carries the D-29 sentence (AC-25)
- [ ] P-02 measured: time from a LiteLLM team-membership change to an `ek_`
      403/200 flip on the forwarder, recorded in `references/troubleshooting.md`
      (spec §17)

---

## Out of scope

**LibreChat.** Its master-key MCP calls (handover §2.2) are a separate ticket.
Step 3 must not *break* LibreChat's current Dex-token auth path, but moving it
onto ACH is not this cutover's work.
