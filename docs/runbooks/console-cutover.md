# Runbook — unified console cutover

Move production from alitellm-auth + the LiteLLM catch-all + the `x-user-id`
impersonation plugin to the ACH console.

Every step below is a commit in the **gitops repo `gitops-genai-blueprint`**
(paths from spec §13, read 2026-09-21 — **re-read the repo before each step**;
it is not checked out beside ACH) plus a verification command run from a host
with cluster access. ACH-side artifacts referenced here live in this repo.

`<domain>`, `<ach-ns>`, `<old alitellm-auth host>` and `<console release>` are
placeholders — substitute per deployment.

**Order is load-bearing. Never reorder 3 → 4 → 5**: the plugin serves
alitellm-auth, and the legacy keys serve alitellm-auth's users.

| # | Step | Reversible? |
|---|------|-------------|
| 1 | `apps/ach-identity` → complete chart | yes — revert the gitops commit |
| 2 | Console release + LiteLLM on its own host (D-18) | yes, **until the D-29 boundary is crossed** |
| 3 | Retire alitellm-auth (D-19) | yes — revert the gitops commit |
| 4 | Remove the LiteLLM impersonation plugin (D-06) | yes — revert the gitops commit |
| 5 | Delete legacy standalone `sk-` keys (D-31) | **NO — irreversible, owner-gated** |
| 6 | Archive + ACH docs | yes |

**Owner-gated (explicit go in-session, dry-run output in front of the owner):**
step 5, and the first `suspended` / `expires_at` row after step 2 (the D-29
boundary).

---

## Preflight

```bash
git log --oneline main | grep -E "drop the identity deployment profile|console session|ek_ suspended state|console stats"
make helm-render-check                     # 5 topologies
helm template x deploy/helm/ach --set profile=identity 2>&1 | grep -m1 "identity"   # must FAIL with the migration guidance (AC-23)
```

- [ ] The Phase 0-3 landmark commits are on `main`.
- [ ] `helm-render-check` OK.
- [ ] `profile: identity` render **fails** with the migration guidance.

---

## Step 1 — `apps/ach-identity` → complete chart

Must land **before** the first ACH image containing `9cfdc2c` (the Phase-0
profile removal) reaches production: that render fails by design on
`profile: identity`.

- [ ] **Snapshot.** `pg_dump` the ACH Postgres that `apps/ach-identity` uses (or
      take the managed-DB snapshot). Record the id here — this is the **last
      restore point below the D-29 boundary**:

      snapshot id: ______________________  taken: ____________  by: ____________

- [ ] **gitops edit** — in `apps/ach-identity/` values:
      - delete `profile: identity`;
      - set the full topology: `operator.enabled`, `contentService`,
        `gateway.enabled: true`, `postgres`/`valkey` connection, `security.*`
        Secret refs;
      - `ACH_BASE_URL = https://api.<domain>` (and `ACH_PUBLIC_BASE_URL` if the
        console host differs);
      - keep the existing Ingress/HTTPRoute host `api.<domain>`;
      - **do NOT** yet remove the `x-user-id` header strip in
        `apps/ach-identity/base/httproute.yaml:29` — alitellm-auth still needs
        it until step 3.

- [ ] **Verify**

```bash
kubectl -n <ach-ns> rollout status deploy/ach-operator deploy/ach-platform-api deploy/ach-forwarder deploy/ach-gateway --timeout=5m
curl -fsS https://api.<domain>/.well-known/oauth-authorization-server | jq .issuer     # AS still answers
ach-cli login --url https://api.<domain> && ach-cli whoami                            # OAuth unchanged (AC-23)
kubectl -n <ach-ns> get environments.ach.ackstorm.ai                                  # operator reconciles the (possibly zero) CRs
```

- [ ] **gitops commit** — `chore(ach-identity): migrate to the complete ACH chart (spec D-21)`

---

## Step 2 — console release + LiteLLM on its own host (D-18, D-26)

- [ ] **gitops edit (a): give LiteLLM its own host** — `apps/litellm`: add an
      Ingress/HTTPRoute for `litellm.<domain>` → `litellm.litellm.svc:4000`
      (LiteLLM's own UI + `/key/*` + `/health` + `/model/*`). Re-point every
      consumer of `api.<domain>/ui`, `/key/*`, `/health` — grep the gitops repo
      for `api.<domain>` and for `/ui`. LibreChat's model endpoint stays on
      `api.<domain>/v1` (proxied family — fine); anything hitting a non-family
      path moves to `litellm.<domain>` or to the cluster-internal
      `litellm.litellm.svc:4000`.

- [ ] **gitops edit (b): bump ACH to the console release** — `apps/ach-identity`
      image tag → the release carrying Phases 1-3 (with `ui/` embedded); set
      `openwork.enabled` + branding if the Den is wanted. `ach migrate` runs
      `000022` as the init container.

- [ ] **Verify**

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://api.<domain>/            # 200 (console index) — NOT LiteLLM
curl -sS https://api.<domain>/ | grep -qi litellm && echo "LEAK: LiteLLM served at /"
for p in /ui /key/list /health /model/info; do curl -sS -o /dev/null -w "$p %{http_code}\n" https://api.<domain>$p; done   # all 404 (AC-24)
curl -sS -o /dev/null -w '%{http_code}\n' https://api.<domain>/v1/models -H "Authorization: Bearer <pk jwt>"   # 200 (family still proxied)
curl -sS https://litellm.<domain>/health                                   # LiteLLM reachable on its own host
# browser: sign in at https://api.<domain>/ → Personal view → create an EK → suspend → the 60 s notice appears
psql "$ACH_DB" -c "select count(*) from environment_keys where status='suspended' or expires_at is not null"
```

> ⚠ **D-29 boundary.** That last query returns 0 until a user acts. **The first
> non-zero crosses the boundary — from then on there is no rollback below
> `<console release>`.** See Rollback below.

- [ ] Release notes: `CHANGELOG.md` in this repo carries the D-29 sentence and
      the D-18 note (already committed — verify it is in the released tag).

---

## Step 3 — retire alitellm-auth (D-19)

**Precondition:** the console has been verified in step 2 for at least one
working day (owner's call), and users have been told that the old login is going
away and that their standalone keys will be **deleted in step 5** (D-31).

- [ ] **gitops edits**
      - delete `apps/alitellm-auth/` (Deployment, its Istio `ext_authz` chart
        blocks, `clients/ackstorm-token` config);
      - remove the `x-user-id` strip from
        `apps/ach-identity/base/httproute.yaml:29` (nothing sets it any more);
      - remove any DNS/Ingress for the old UI host.

- [ ] **Verify**

```bash
kubectl get deploy -A | grep -i alitellm-auth                                  # none
curl -sS -o /dev/null -w '%{http_code}\n' https://<old alitellm-auth host>/    # DNS gone or 404
# console still logs in; ach-cli login still works; an EK still forwards (/v1) — the AS never lived in alitellm-auth (D-19)
```

- [ ] **gitops commit** — `chore: retire alitellm-auth (unified console, spec D-19)`

---

## Step 4 — remove the LiteLLM impersonation plugin (D-06)

`apps/litellm/base/files/x_user_id_auth.py` + `config.yaml:59-66`
(`custom_auth: x_user_id_auth.user_api_key_auth`,
`custom_auth_run_common_checks: true`). The kind cluster has run without it
since `af67faa`; `make e2e-full` (incl. `TestEkStateModel` and the forwarder
precheck tests) is the evidence that ACH's team/access-group model needs no
custom auth.

- [ ] **gitops edit** — remove the file and **both** config lines. With
      `custom_auth` gone, `custom_auth_run_common_checks` has no effect (it only
      governs what runs *after* a custom auth function). Keep every unrelated
      `general_settings` key.

- [ ] **Verify after the LiteLLM rollout**

```bash
kubectl -n litellm rollout status deploy/litellm --timeout=5m
# team ceiling still enforced without the plugin: an ek_ of Environment A must NOT reach a model outside A's access group
curl -sS -o /dev/null -w '%{http_code}\n' https://api.<domain>/v1/chat/completions -H "x-ach-key: <ek of env A>" -d '{"model":"<model NOT in A>","messages":[{"role":"user","content":"hi"}]}'   # 401/403 from LiteLLM
curl -sS -o /dev/null -w '%{http_code}\n' https://api.<domain>/v1/chat/completions -H "x-ach-key: <ek of env A>" -d '{"model":"<model in A>","messages":[{"role":"user","content":"hi"}]}'       # 200
# console stats still answer (they never used x-user-id — AC-19)
```

- [ ] **gitops commit** — `chore(litellm): drop the x-user-id impersonation plugin (spec D-06)`

---

## Step 5 — delete the legacy standalone `sk-` keys (D-31) — ⚠ IRREVERSIBLE, OWNER-GATED

Tool: `scripts/cutover-legacy-keys.sh` (this repo). Needs `LITELLM_URL` and
`LITELLM_MASTER_KEY`.

**Ownership rule — this is the corrected one; the plan file is superseded on this
point (owner ruling, 2026-09-22):**

- a key carrying any `metadata.ach_issuer` is ACH-owned and is **never** touched;
- a key whose `metadata.source == "token-factory"` is alitellm-auth's own and **is** swept;
- a key with no such marker is foreign and is **never** touched.

`metadata.source` is the only selector. The `key_alias` prefix is **not** a
marker — alitellm-auth has used at least three alias forms (`tf-…`, `lk-…`,
`key-YYYY-MM-DD-HHMMSS`), so a `tf-`-aliased token-factory key is swept like any
other. The alias is shown in the dry-run output for your review only.

- [ ] **Dry run against production**

```bash
LITELLM_URL=https://litellm.<domain> LITELLM_MASTER_KEY=... scripts/cutover-legacy-keys.sh
```

- [ ] **Owner gate.** Paste the candidate list to the owner — **count, aliases,
      users only; never the token values** (the dry run prints exactly that).
      **STOP until the owner says go.** Users must already have been told in
      step 3 that they create new EKs from the console.

      owner go given by: ______________________  at: ____________

- [ ] **Apply + verify**

```bash
scripts/cutover-legacy-keys.sh --apply
scripts/cutover-legacy-keys.sh | tail -1                                           # candidates: 0
psql "$ACH_DB" -c "select count(*) from environment_keys where status<>'revoked'"  # unchanged from before the sweep
# a console-created EK still forwards; the orphan reaper's next tick reports 0 candidates (operator logs)
```

---

## Step 6 — archive + ACH docs

- [ ] **Archive alitellm-auth on GitHub** — owner action.
- [ ] `references/upstream-sync.md`: mark the ported pieces (Den, fixtures,
      tests, brand marks) with the archive date.
- [ ] `NOTICE`: the alitellm-auth attribution row **stays** — Apache-2.0
      provenance does not expire.
- [ ] `references/understanding.md`: production picture — console at `/`,
      LiteLLM on its own host, no plugin, no alitellm-auth.
- [ ] `CLAUDE.md` / `AGENTS.md` Quick-context: one sentence — "The LiteLLM
      `x_user_id_auth.py` impersonation plugin and alitellm-auth were retired at
      the console cutover (`<date>`)."
- [ ] ACH commit — `docs: console cutover complete — alitellm-auth retired, LiteLLM on its own host`

---

## Rollback

| # | Step | Rollback |
|---|------|----------|
| 1 | complete chart | Revert the `apps/ach-identity` gitops commit; wait for the rollout. The DB snapshot from step 1 is the restore point. |
| 2 | console release + LiteLLM host | Revert both gitops commits (image tag + `apps/litellm` host) — **only while the D-29 boundary is uncrossed**. Below the boundary, see the box below. |
| 3 | alitellm-auth retired | Revert the gitops commit; alitellm-auth redeploys, and the `x-user-id` strip comes back with it. Legacy keys still exist (they are only deleted in step 5), so users can log in again. |
| 4 | plugin removed | Revert the gitops commit; the LiteLLM rollout restores `x_user_id_auth.py` and the two `config.yaml` lines. |
| 5 | legacy keys deleted | **None.** Deleted LiteLLM keys cannot be recreated with the same secret. Affected users mint a new EK from the console. |
| 6 | archive + docs | Revert the doc commits; un-archive the GitHub repo. |

> ⚠ **Past the D-29 boundary there is no rollback.**
> *"No rollback below `<console release>` once any environment key is suspended
> or carries an expiry: an older orphan worker would reap suspended keys. The
> down migration refuses in that case."*
>
> Recovery past the boundary is **restore from the DB snapshot taken in step 1**,
> accepting the loss of everything written since — not a chart/image revert.

---

## Post-cutover checklist (all must be true)

- [ ] `https://api.<domain>/` = console; `/ui`, `/key/*`, `/health` = 404 (AC-24)
- [ ] `ach-cli login`, `env hydrate`, EK forwarding unchanged (AC-23)
- [ ] console stats/latency for a real user include EK traffic, `data_scope: "user"` (AC-16)
- [ ] no `x-user-id`, no `custom_auth`, no alitellm-auth Deployment (AC-19, D-06, D-19)
- [ ] `scripts/cutover-legacy-keys.sh` dry run reports 0 candidates (AC-25)
- [ ] `CHANGELOG.md` carries the D-29 sentence (AC-25)
- [ ] P-02 measured: time from a LiteLLM team-membership change to an `ek_`
      403/200 flip on the forwarder, recorded in `references/troubleshooting.md`
      as the stated guarantee (spec §17)

---

## Out of scope

**LibreChat's master-key MCP calls** (handover §2.2) are a **separate ticket**.
Do not fold them into this cutover.
