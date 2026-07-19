# ACH — External Review: Gaps & Resolutions Log

**Status:** living document. Started 2026-06-16.
**Context:** an external reviewer audited the *delivered* ACH against the frozen
specs (`../ach-spec/ach-spec-final-frozen.md`, `ach-cli-spec-final-frozen.md`,
reconciled in `*-spec-final-delivered.md`). This log captures, gap by gap, what
we found, the decision we took, and the follow-up work. **Decisions are recorded
here first; code/spec changes happen later, deliberately.**

Decision legend:
- **DECIDED** — direction agreed; follow-up work listed (not yet done).
- **OPEN** — needs a decision.
- **PENDING** — not yet discussed.

---

## Gap index

| # | Gap | Severity | Status |
|---|-----|----------|--------|
| G1 | Budget per Environment | core-contract | **DECIDED** |
| G2 | UI Objects API / GitOps export = latent (origin='ui' without UI) | scope | **DECIDED** (keep; UI+export = P2) |
| G3 | LiteLLM `sk-` virtual-key plaintext stored (TESTING-PHASE) | security | **DECIDED** (Fix-1 encrypt-at-rest; prod blocker) |
| G4 | No OCI `ach-cli` image / InitContainer | distribution | **DECIDED** (ship separate image, P1) |
| G5 | `hydrate --sync` is a silent stub | correctness/UX | **DECIDED** (Path 2 — wire STATE-05) |
| G6 | `content fetch` / `platforms list` missing | debuggability | **DECIDED** (content fetch P1; platforms list dropped) |
| G7 | Metrics gaps (env_available, hydrate, operator refresh, cache hit/miss) | observability | **DECIDED** (build all except marketplace count) |
| G8 | `skill`/`skill-marketplace` missing from admin refresh; no kind-parity checklist | completeness | **DECIDED** (fix + parity audit; "all CRs same-class") |
| G9 | Local package manager (`repo`/`plugin`/`skill`) framing vs governed path | product clarity | **DECIDED** (Option C — `ach-cli local` namespace) |
| G10 | Too many new kinds before parity; need kind-lifecycle contract | process | **DECIDED** (checklist as-is; both doc + PR-template; `docs/references/`) |
| G12 | Gateway 6th mode framing (pillar vs packaging detail) | docs | **DECIDED** (Option A — optional edge) |
| G13 | `pk_` on runtime routes — unscoped; UX/alerting | UX/governance | **DECIDED** (UX hardening; honor frozen, no toggle) |
| G14 | LiteLLM `default` Team hard dependency (no lazy create) | operability | **DECIDED** (already fixed; A — accept transient race) |
| G15 | BIP invisible conflicts + tiebreaker inverted (alpha-LAST vs frozen alpha-FIRST) | correctness/ops | **DECIDED** (restore alpha-FIRST; std `Synced=False/NameConflict`) |
| G16 | Operator + Content Service single replica (RWO PVC) — no HA | scaling | **DECIDED** (accept v1alpha1; P2 RWX-split deferred) |
| G17 | JWT `nbf` removed; frozen says verify it — **spec-vs-code drift (not a live break)** | conformance | **DECIDED** (drop `nbf`; align spec) |
| G18 | JWT `sub` now bare email (dropped `<ns>/` prefix) — intentional; not a live break | compat | **DECIDED** (ratify bare `sub`; keep `email`) |
| G19 | HTTPS enforcement downgraded to warning-only (CLI) + `http://` allowed on HTTPSource | security posture | **DECIDED** (CLI B: refuse+opt-in; HTTPSource D: keep+doc) |
| G20 | Audit events dropped fields (key.type, environment, route, source.ip/ua) | auditability | **DECIDED** (add environment + source.ip/ua + route) |

(G11 = duplicate of G2; folded in.)

---

## G1 — Budget per Environment

### Plain explanation
A LiteLLM **budget** is a spending cap (USD over a period). The frozen spec
promised that cap could live on an **Environment**: each `Environment.spec.budget`
would become a LiteLLM **tag budget** (the env name is attached as a tag on every
`ek_` request), so spend on that Environment was capped.

### What the code actually does (delivered)
- **`Environment.spec.budget` does not exist** — removed from `EnvironmentSpec`.
  The operator reconciles only the LiteLLM **access group** (capability gating),
  not a budget tag.
- `ek_` keys still carry `Tags=[env.Name]` + `AccessGroups=[env.Name]`
  (`envkeys/handler.go`) — but **for attribution only**, no budget attached.
- **LiteLLM tags are Enterprise-only.** The handler has
  `isEnterpriseTagsRejection`: on a `403 "This feature is only available for
  LiteLLM Enterprise users: tags"` it **drops the tag and retries**. So on OSS
  LiteLLM the env tag isn't even applied.
- **ACH sets no budget anywhere today**: KEY-10 invariant — `max_budget` is
  never set on the LiteLLM user (`UserNewRequest` has no field) nor on the key
  (`KeyGenerateRequest.MaxBudget = nil`).

So a per-Environment budget would have required: re-adding `spec.budget`, an
operator tag-budget reconcile loop, **and** LiteLLM Enterprise. It would cap only
`ek_` traffic (`pk_` carries no tag → uncapped).

### Decision — **DECIDED: no budget per Environment**
Budget is **not** an Environment-level concern. Budget governance hierarchy:

1. **Now → per-USER.** The budget boundary is the LiteLLM user. Environment is a
   capability/context/identity boundary, **not** a cost boundary.
2. **Future → per-KEY.** A key-level budget, **lower than** the user's budget
   (per-key cap ⊆ per-user cap). Designed later.
3. **Drop entirely** the per-Environment budget machinery and promise.

Rationale: per-Environment budget needs LiteLLM Enterprise (tags), leaks via
`pk_`, and adds an operator reconcile surface — for a cap that maps more
naturally onto the principal (user) than onto a capability bundle (Environment).
The user/key hierarchy is simpler and provider-tier-independent.

### Follow-up work (NOT done yet)
- **Code:** mostly already aligned — `spec.budget` is already absent. Keep the
  `ek_` env tag as **attribution only**; keep the operator `DeleteTag` in the
  Environment deletion drain (tag still used for attribution). Confirm nothing
  tries to set a tag budget (it doesn't).
- **Docs/spec:** purge the per-Environment budget promise wherever it still
  implies enforcement:
  - `../ach-spec/ach-spec-final-delivered.md` §6.3 / §8.6 — reframe env tag as
    attribution-only; remove "tag budget" language; state budget = user-level
    (now), key-level (future).
  - README "Five Pillars / Usage Governance" + CLAUDE.md — stop selling
    "Environment groups … limits." Governance = capabilities/identity/forwarding
    /attribution; budget = LiteLLM user (now) → key (future).
  - `pk_` hydrate warning text — its rationale was partly budget; reword to
    "`ek_` gives Environment attribution + capability scoping" (drop the budget
    justification). [ties to G13]
- **Future design (per-key budget):** spec a key-level `max_budget` (≤ user
  budget) for `ek_` and/or `pk_`. Reverses KEY-10 for keys when introduced.

### Sub-question — **DECIDED: (a)**
Budget stays **out-of-band**: deployer / LiteLLM-Operator sets the user budget in
LiteLLM; ACH keeps KEY-10 (sets nothing). No code change, honest wording. Per-key
budget (future) is where ACH would first start writing budgets.

---

## G2 — UI Objects API / GitOps export = latent (`origin='ui'` without UI)

### Plain explanation
The design (issue #34) anticipated **two write paths** for ACH objects:
- `origin='cr'` — you `kubectl apply` a CRD, the operator projects it to Postgres.
- `origin='ui'` — a future UI/REST writes rows directly to Postgres.
To let both coexist, every projection row got an `origin` ('cr'|'ui') + `locked`
column, and "who wins if both touch the same object" logic
(`ErrOriginConflict` → CR condition `Synced=False/ConflictWithUIRow`).

### What the code actually does (delivered)
- **The UI write path does not exist.** No `/platform/objects/<kind>` route. No
  YAML/GitOps export endpoint. (verified: zero matches.)
- **No code ever writes `origin='ui'`.** Only the operator writes, always
  `origin='cr', locked=true`. Every `'ui'` mention in the codebase is a *comment
  about a future possibility*.
- But the **whole apparatus is wired and dormant**:
  - `origin`+`locked` columns + CHECK constraints on environments, plugins,
    prompts, artifacts, external_refs, marketplace_plugins, skills, … (migrations
    000005/000008/000009).
  - `ErrOriginConflict` in `internal/db/{environments,plugins,prompts,artifacts,
    backend_identity_policies,litellm_connections,tx}.go`.
  - `ConflictWithUIRow` condition flip in ~6 controllers.
  - `ErrUIOriginRefreshUnsupported` in `refresh_signal.go`; content-service
    `pipeline.go`/`paths.go` guard against "future origin='ui' rows pointing
    outside the cache."
- **Consequence:** `ErrOriginConflict` can never be returned (every row is 'cr',
  the origin-gated UPSERT never misses), so `ConflictWithUIRow` is **unreachable**
  in the delivered system. It is dead logic, not a working capability.

### The fork (the real decision)
Everything hinges on one product question: **is a UI / non-GitOps write path on
the roadmap?**

- **Option A — GitOps-first; keep machinery dormant, DE-DOCUMENT it.**
  Reviewer's recommendation. `origin`/`locked` stay as internal-reserved columns
  + dormant code (cheap, forward-compatible). Remove from user-facing
  spec/README/acceptance; stop advertising `ConflictWithUIRow`; move UI Objects
  API + GitOps export to an explicit P2 backlog. **No code removal**, just honest
  framing: "v1alpha1 is GitOps-first; the only supported write path is Kubernetes
  CRDs."
- **Option B — rip it out.** Drop `origin`/`locked` columns (down-migration),
  delete `ErrOriginConflict` + `ConflictWithUIRow` + `ErrUIOriginRefreshUnsupported`
  + the content-service guards. Simplest *runtime* mental model now; throws away
  the #34 work and pays re-add cost if a UI ever lands. Migration churn on 6+
  tables.
- **Option C — finish it.** Build the UI Objects API + GitOps export so the
  machinery becomes live. Large P2; only if a UI is a committed product direction.

### Decision — **DECIDED: keep it (Option A now → Option C later)**
The `origin`/`locked` machinery is **intentional forward-investment**, not
over-engineering to remove. The product intent (confirmed) is a concrete planned
feature — so **Option B (rip out) is rejected**. Reviewer's "latent
over-engineering" label is downgraded to "deliberate, but must not be marketed as
shipped."

**Intended UI → GitOps promotion flow (the roadmap target):**
1. **Today:** all objects (plugins, prompts, environments, …) registered via
   **GitOps** → `origin='cr'`, `locked=true`, read-only everywhere but Git.
2. **Future UI (P2):** a UI registers the same object kinds for max UX →
   `origin='ui'`, `locked=false` (editable in the UI). Workflow: define an
   Environment in the UI, test it, confirm it works.
3. **Export/promotion (P2):** an "Export" action serializes a working `ui` object
   to **YAML** → the user commits it to their GitOps repo.
4. On GitOps apply, the operator **overwrites the row to `origin='cr'`** → it
   becomes **read-only in the UI** (now GitOps-owned). UI = scratchpad with great
   UX; GitOps = source of truth. `origin` is exactly the provenance flag that
   makes this hand-off safe.

**Status split (what's live vs reserved):**
- **Live in v1alpha1:** GitOps-only write path (`origin='cr'`). This is the only
  active object-write surface.
- **Reserved / future (P2):** UI Objects API (writes `origin='ui'`), GitOps
  export (ui→yaml), and the **promotion reconcile** (cr-apply over a matching ui
  row → flip `origin` ui→cr, lock).

### Important gap inside G2 — promotion half is NOT built
The current code implements only the **block** half of conflict resolution:
operator UPSERT is origin-gated → on a `ui` row it returns `ErrOriginConflict` and
flips the CR to `Synced=False/ConflictWithUIRow`, **refusing to overwrite**. The
**promote** half the flow above needs — *"a CRD that matches a `ui` row wins and
flips `origin` ui→cr"* (frozen §15.8 steps 3–5, `PromotionMismatch` on no-match) —
is **absent**. So as wired today, an exported-then-applied object would be
*blocked*, not *promoted*. Building the promotion reconcile is part of the P2 UI
work and is what actually delivers step 4 above.

### Follow-up work (NOT done yet)
- **Docs (now, cheap):** in `../ach-spec/*-delivered.md`, README, CLAUDE.md —
  state plainly "v1alpha1 write path is GitOps/CRD only; UI Objects API + export +
  promotion are **reserved/future (P2)**, not active." Don't present
  `ConflictWithUIRow` / `origin='ui'` as a shipped capability — label them
  reserved.
- **Code (now, trivial):** add a one-line "reserved for future UI write path
  (#34); the promotion half is unbuilt — see G2" doc comment where
  `ConflictWithUIRow` is set, so readers don't mistake dormant code for live.
- **P2 epic (future):** UI Objects API (`POST/PUT/DELETE /platform/objects/<kind>`
  writing `origin='ui'`), GitOps export endpoint (object→YAML), and the promotion
  reconcile (origin flip on spec-match + `PromotionMismatch` on mismatch).
- **Keep:** all `origin`/`locked` columns, constraints, `ErrOriginConflict`, and
  per-controller `ConflictWithUIRow` wiring — they are the foundation, not waste.

---

## G3 — LiteLLM `sk-` virtual-key plaintext stored (security)

*(Security item — written in plain prose, not compressed.)*

### Plain explanation
When ACH mints a `pk_`/`ek_`, LiteLLM returns a virtual key (`sk-…`) — the actual
credential that authorizes calls against LiteLLM. ACH currently stores that `sk-…`
**in plaintext** in Postgres (`personal_keys.litellm_key_material`,
`environment_keys.litellm_key_material`, migration `000011`).

### Why it's there (the architecture pivot — plan `2026-06-10-forward-user-litellm-key.md`)
The forwarder used to authenticate to LiteLLM with the shared **master key** plus
an `x-litellm-key-id` delegation header (frozen FIX01 §A.6 — no per-user secret
stored). It was changed to forward the **caller's own virtual key** as
`x-litellm-api-key` for clean 1:1 identity. The concrete driver: **LiteLLM
v1.87.1's MCP gateway only grants a non-admin virtual key on the exact
`/mcp/{subpath}` route; the master key falls through to an admin-only check and
500s** (`proxy.go:73-99`). So for `/mcp` the forwarder *must* present a non-admin
user key, not the master. To have that key available at request time, ACH
persists it — and, for the testing phase, persists it in cleartext (you accepted
*"me da igual si es en claro, de momento"*). Every such path is tagged
`TESTING-PHASE (reverts FIX01 §A.6)` so it is greppable when undone.

### Why this is the most serious finding
- `pk_`/`ek_` themselves are stored only as **HMAC-SHA256-with-pepper hashes** — a
  database dump does not yield usable ACH keys.
- `litellm_key_material` is the **opposite**: plaintext `sk-…`. A single read of
  the Postgres dump yields **every user's live LiteLLM credential**, usable
  directly against LiteLLM, bypassing ACH entirely (its budgets, audit, access
  groups). The pepper protects the front-door keys and leaves the back-door key in
  the clear.
- For a product whose pitch includes "credential boundary / minimize credential
  sprawl," plaintext provider credentials at rest for the whole user base is
  disqualifying for any production claim.

### Decision — **DECIDED: production blocker + governance now; fix direction OPEN**
1. **Plaintext is acceptable in the testing phase ONLY.** It must not reach
   production in this form.
2. **Make it a tracked, blocking release gate:**
   - A blocking epic/issue to remove plaintext at rest.
   - A security acceptance criterion: *"ACH stores no LiteLLM virtual-key
     plaintext at rest."*
   - A build/runtime guard: when `litellm_key_material` is populated, the build
     is **non-production** — loud startup warning, and a refuse-to-start switch
     once a "production mode" flag exists.
3. **Target fix direction — OPEN (pick one):**
   - **Fix-1 — Encrypt at rest (recommended).** Keep the forward-user-key
     architecture (the MCP-gateway driver is real and hard to undo), but stop
     storing cleartext. Envelope-encrypt `litellm_key_material` (KMS / sealed key
     / a symmetric data-encryption key held like the existing `pepper`); platform-
     api encrypts on write, forwarder decrypts on use. A DB dump alone becomes
     useless without the separate data-encryption key. Moderate effort; both
     services already share secret-handling infra.
   - **Fix-2 — Restore master + `x-litellm-key-id` delegation.** No per-user secret
     stored for `/v1`/`/gemini`/`/a2a`. But the `/mcp` gateway constraint above
     still needs a non-admin user key, so MCP must be solved separately (LiteLLM
     Operator custom-auth plugin, or a per-request short-lived key mint). Cleaner
     end-state, more work, depends on LiteLLM custom-auth being deployable. Needs
     a spike.
   - **Fix-3 — Mint on demand / don't persist.** LiteLLM returns `sk-…` only once
     at `/key/generate`; it cannot be re-fetched. Would require per-session key
     regeneration (churn). Likely not viable — noted for completeness.

### Recommendation
Govern now (step 2 — cheap, immediate), then **Fix-1 (encrypt at rest)** as the
target: it removes plaintext-at-rest without fighting the LiteLLM MCP-gateway
constraint that forced the per-user-key model. Keep Fix-2 as a longer-term
"eliminate storage entirely" goal contingent on a custom-auth spike.

### Fix direction — **DECIDED: Fix-1 (encrypt at rest)**
Keep the forward-user-key architecture; encrypt `litellm_key_material` at rest.
**Fix-2 rejected** — running a LiteLLM custom-auth plugin is operational overhead
we don't want to own. No "delegation later" track.

**Implementation sketch (for when we build it):**
- **Cipher:** AES-256-GCM (authenticated). Store `nonce || ciphertext` (base64 or
  bytea) in the column; rename to `litellm_key_material_enc` to make "encrypted"
  self-documenting, or keep the name with a changed content contract.
- **Key:** a 32-byte data-encryption key in a Kubernetes Secret (mirror the
  existing `pepper` pattern), RBAC-scoped to the **platform-api** (encrypt-on-mint)
  and **forwarder** (decrypt-on-use) ServiceAccounts only. Operator/content-service
  get no read access.
- **Write path:** platform-api encrypts `keyResp.Key` at mint
  (`sso.go`, `envkeys/handler.go`) before INSERT.
- **Read path:** forwarder Director decrypts `kc.LiteLLMKeyMaterial` just before
  `headers.StripAndRewrite` (`proxy.go:78-94`); plaintext lives only in memory for
  the request.
- **Key rotation:** support a `current`/`next` key id (same shape as the JWT
  signing Secret) so material can be re-wrapped without downtime; tag rows with the
  key id used.
- **Existing rows:** testing-phase plaintext rows either get a one-time
  re-encrypt migration or are invalidated (pre-migration keys already "break by
  design" per the original plan) — clean cutover is acceptable.
- **Logging:** never log decrypted material; the forwarder already forbids echoing
  upstream key values.

**Immediate governance (confirmed, do first, independent of the crypto work):**
- Blocking issue to land Fix-1 before any production claim.
- Security acceptance criterion: *"ACH stores no LiteLLM virtual-key plaintext at
  rest."*
- Non-production build guard: loud startup warning when plaintext material is
  detected; refuse-to-start under a future "production mode" flag.

---

## G4 — No OCI `ach-cli` image / InitContainer

### Plain explanation
ACH's headline use case includes deployed agents / CI / Kubernetes Pods that run
`ach-cli env hydrate` to populate an Environment **before** the agent workload
starts — i.e. `ach-cli` as a Kubernetes **InitContainer**. That needs `ach-cli`
packaged as a container image. It isn't.

### What the code actually does (delivered)
- `.goreleaser.yml` builds the `ach-cli` binary (`builds: id: ach-cli`) and ships
  it as **OS archives only** (`archives: cli-binaries`).
- The `dockers:` block publishes **only `ids: [ach]`** (the service binary) →
  `ghcr.io/ackstorm/ach` (amd64/arm64 + manifest). There is **no `ach-cli`
  image**, no `ach-cli` Dockerfile, no InitContainer/Job example.
- Workaround today: users build their own image wrapping `ach-cli`.

### Decision — **DECIDED: ship a separate `ach-cli` image (P1)**
Publish `ghcr.io/ackstorm/ach-cli:<version>` as a **separate** image (NOT bundled
into the service image — keeps the service image lean; `ach-cli` deliberately
drops the k8s/controller-runtime deps). Priority **P1**: important for the agent/
InitContainer story, but a build-your-own workaround exists, so it does not block
correctness/security.

### Follow-up work (NOT done yet)
- **goreleaser:** add `dockers:` entries for `ids: [ach-cli]` (amd64 + arm64) and a
  `docker_manifests` multi-arch manifest, mirroring the `ach` image wiring.
  Reuse the existing **SBOM (CycloneDX) + cosign** signing already applied to the
  `ach` image.
- **Dockerfile:** minimal `Dockerfile.ach-cli` — distroless/static base
  (the static `CGO_ENABLED=0` build, cf. `make build-cli-host`), `ENTRYPOINT
  ["ach-cli"]`, single explicit `COPY` of the binary (follows the repo's
  no-`COPY . .` rule).
- **Examples:** a Kubernetes **InitContainer** snippet — `ach-cli env hydrate`
  writing to a shared `emptyDir` the agent container mounts; credentials
  (`ACH_BASE_URL` + `ACH_API_KEY`/`ek_`) injected from a Secret. Optionally a Helm
  values snippet / a one-shot `Job` example.
- **Docs:** `../ach-spec/ach-cli-spec-final-delivered.md` §2.1/§2.3 (OCI image +
  InitContainer) are currently marked NOT IMPLEMENTED — flip them to delivered
  once the image ships.

### OPEN (minor) — base image
Distroless static (recommended — smallest attack surface; `ach-cli` is a static
Go binary, no libc needed) vs alpine (shell available for debugging). Lean
distroless; decide at build time.

---

## G5 — `hydrate --sync` is a silent stub

### Plain explanation
`ach-cli env hydrate --sync` is supposed to **prune** the files ACH projected for
resources that an Environment **no longer contains** (you removed a plugin/prompt
from the Environment; re-hydrate with `--sync` should delete its old projected
files). The flag exists, looks operational — and does nothing.

### What the code actually does (delivered)
- The commit sequence calls `syncFn(existingState, existingState, …)`
  (`commit.go:561`) — it passes the **old state as BOTH `prev` and `new`**. The
  prune algorithm removes entries present in `prev` but absent from `newFile`;
  with `prev == new` nothing is ever absent → **zero pruning**. Pure no-op.
  In-code `TODO(STATE-05 composition)`: the `newFile` arg should be the *composed
  next-state* built later (step 12), but step 11 (sync) runs first.
- **The prune algorithm is real and proven** (`Sync` at `wiring.go:1355`, with
  `syncOne`/`syncComposite`/`syncDeep` inverse-merge) — `env uninstall` exercises
  it. Only the **hydrate wiring** feeds it the wrong argument.
- Two distinct prunes — don't conflate:
  - **STATE-04** (works): an entry whose target file is *gone from disk* is
    silently pruned on hydrate.
  - **STATE-05 / `--sync`** (stub): an entry whose *resource was dropped from the
    Environment* should be pruned. This is the broken one.
- **Why it matters:** projected files include **credential-bearing adapter
  configs**. A user who trusts `--sync` to clean up a removed resource is left
  with stale (possibly secret-bearing) files on disk, while the command reports
  success. A flag that silently lies is worse than a missing flag.
- Mitigation that exists today: `env uninstall` does full, correct removal — so
  the "remove everything" path works; only the incremental "remove what's no
  longer in the Environment" path is broken.

### Decision — **DECIDED: `--sync` must not silently no-op**
Non-negotiable: stop the lie. Two implementation paths (pick one):
- **Path 1 — disable now, wire later (recommended immediate).** Make `--sync`
  return an explicit `not_implemented` error (or hide the flag) so it can't
  mislead. Cheap, honest, removes the trap immediately. Schedule the real wiring
  as P1.
- **Path 2 — wire STATE-05 now.** Compute the composed next-state and pass it as
  `Sync`'s `newFile` (the code is pre-wired: *"Sync is wired so future composition
  automatically activates STATE-05 inverse-merge"*). Constraint: preserve the
  `maybeKill(11)` SIGKILL-injection boundary that `sc2_commit_sequence_sigkill`
  asserts — compute next-state earlier without moving the step-11 kill point.
  Medium effort; the algorithm already exists and is tested via `env uninstall`.

### Recommendation
**Path 1 immediately** (1-line honesty fix — flag stops lying), then **Path 2 as
P1** to make `--sync` actually prune dropped-resource files. End-state: `--sync`
works; until then it fails loudly instead of silently.

### Fix — **DECIDED: Path 2 (wire STATE-05 now)**
Wire `--sync` for real rather than disabling it. Pass the composed next-state as
`Sync`'s `newFile` so entries for resources dropped from the Environment are
pruned. Hard constraint: **preserve the `maybeKill(11)` SIGKILL-injection boundary**
that `sc2_commit_sequence_sigkill` asserts — compute/compose the next-state earlier
(or via a thunk) without relocating the step-11 kill point. The prune algorithm
(`Sync`/`syncOne`/`syncComposite`/`syncDeep`, `wiring.go:1355`) is already proven
via `env uninstall`; only the hydrate call site (`commit.go:561`) changes from
`(existingState, existingState)` to `(existingState, composedNextState)`.
Implementation note: this is the STATE-05 composition follow-up the TODO references.

---

## G6 — `content fetch` / `platforms list` missing

### Plain explanation
Two debug/utility commands the frozen CLI spec defined are not built:
- `content fetch <kind> <name>` — download one content blob (prompt/plugin/
  artifact/skill) straight from the Content Service, bypassing adapter projection.
- `platforms list` — print the platform adapters compiled into the CLI.

### What the code actually does (delivered)
- Neither command exists (no `content` / `platforms` subcommand in
  `cmd/ach-cli/`).
- The server side exists: `GET /content/{kind}/{name}` with `x-ach-key`
  (+ `x-ach-environment` for `pk_`) is live on the Content Service. So
  `content fetch` is a thin client over an existing endpoint.

### Why `content fetch` matters (the asymmetry)
`content fetch` is the one tool that **isolates a hydrate failure**: it answers
"is the problem auth, the Content Service, the cache, the manifest, or the local
adapter projection?" by fetching the raw blob directly. Without it, a broken
hydrate is hard to bisect. `platforms list` is cosmetic — the adapter set is
already discoverable via `env hydrate --target --help`.

### How content normally flows (so you can place `content fetch`)
`ach-cli env hydrate` runs this chain:
1. **Auth** — CLI sends `x-ach-key` (`pk_`/`ek_`).
2. **Manifest** — `POST /platform/hydrate` returns a list of resources, each with
   a `downloadUrl` ( `{baseURL}/content/{kind}/{name}` ) + sha256.
3. **Content** — for each resource the CLI does `GET /content/{kind}/{name}`; the
   Content Service streams the cached blob (`sendfile`).
4. **Extract** — CLI safely un-tars the blob.
5. **Project** — the platform adapter writes the files into `.claude/` etc.

`content fetch` is a one-shot client for **step 3 only** — same auth, no
extraction, no adapter — so it isolates which layer is broken.

### Concrete debugging value
When a hydrate fails you don't know which layer failed. `content fetch <kind>
<name>` tells you immediately:
- `content fetch` **401/403** → it's **auth** (key expired / wrong environment).
- `content fetch` **404 / not-ready** → it's the **cache / operator** (upstream not
  fetched yet, or `SourceReachable` failing). Nothing to do with your workspace.
- `content fetch` **succeeds** (blob downloads fine) → the problem is **extraction
  or adapter projection** (step 4/5), i.e. local — not the server.

Without it, that bisection is guesswork. `platforms list` carries none of this
weight — it just prints which adapters the CLI knows so you know what `--target`
accepts; the same info is already in `env hydrate --target --help`.

### Why they're missing
Scoped out: the priority surface was `hydrate` / `uninstall` / `status`; the two
debug utilities were deprioritized, not blocked.

### Decision — **DECIDED (your call): build `content fetch` (P1); skip `platforms list`**
- **`content fetch` → P1.** Build it.
- **`platforms list` → skip.** The adapter set is already surfaced via `--help` and
  the `--target` flag. Note: the "platforms" concept was **renamed to "targets"**
  (`--platform` → `--target`), so a `platforms list` command would also be
  misnamed. Drop it from scope (remove from spec, don't mark as deferred-backlog).

### Sketch (for whenever it's built)
- `ach-cli content fetch <kind> <name>`:
  - `kind ∈ {prompt, plugin, artifact, skill}`; resolve credential the same way
    `env hydrate` does (`--api-key`/`--env-key`/`ACH_*`/profile); `--environment`
    required for `pk_`.
  - `GET {baseURL}/content/{kind}/{name}` with `x-ach-key` (+ `x-ach-environment`).
  - Write raw bytes to stdout (gzip for plugin/skill/artifact-directory) or to
    `--output <file>`; surface the upstream error envelope + exit code on failure.
  - No extraction, no adapter logic — deliberately raw, that's the debug value.
  - Update spec `ach-cli-spec-final-delivered.md` §5.9 from NOT IMPLEMENTED →
    delivered.
- `ach-cli platforms list` — **dropped from scope** (adapter set already in
  `--help` / `--target`; "platforms" was renamed to "targets"). Remove §5.8 from
  the CLI spec rather than leaving it as a deferred backlog item.

---

## G7 — Metrics gaps (control plane is blind)

### Plain explanation
Prometheus metrics tell operators what's happening without reading logs. ACH
instrumented the **data plane** well but left the **control plane** (Platform API
+ operator domain logic) with no ACH-specific metrics — so "why is this
Environment not working?" can't be answered from metrics alone.

### What the code actually does (delivered)
**Instrumented (confirmed registered):**
- Forwarder: `forwarder_requests_total{route,key_type,outcome}`,
  `forwarder_request_duration_seconds{route,key_type,status_class}`,
  `forwarder_jwt_signed_total{kind}`, `forwarder_jwt_suppressed_total{kind,reason}`.
- Content Service: `content_service_requests_total{kind,outcome}`,
  `content_service_request_duration_seconds{kind}`,
  `content_service_bytes_served_total{kind}`.
- Shared: `litellm_unreachable_total{caller}`.
- Operator orphan loop: `ach_orphan_cleanup_{candidates,revoked,skipped{reason}}_total`.

**Missing (frozen §18.5 — confirmed zero matches):**
| Metric | What it would tell you | Value |
|--------|------------------------|-------|
| `environment_available{name}` (gauge) | Is this Environment ready right now? The single direct answer to "why doesn't my env work." | **highest** |
| `operator_external_ref_refresh_total{kind,type,result}` | Are upstream fetches (plugin/skill/prompt/artifact) succeeding or failing, by source type? | **high** |
| `platform_api_hydrate_duration_seconds` (histogram) | Hydrate latency/health — the user-facing operation. | **high** |
| `key_resolution_cache_hits_total` / `misses_total {key_type,layer}` | Is the key-resolution cache (Redis/in-proc) effective, or hammering Postgres? Perf diagnosis. | **medium** |
| `platform_api_login_total{outcome}` | Login success/failure rate (SSO/CLI). | **medium** |
| `operator_marketplace_plugin_count{marketplace}` | How many plugins/skills each marketplace discovered. | **low** |

Note: the operator does expose generic controller-runtime reconcile metrics, but
none of the ACH-domain signals above.

### Why it matters
The forwarder/content metrics tell you about **traffic**, but a broken Environment
usually fails **before** any traffic — at refresh (upstream unreachable), at
availability (a condition stuck False), or at hydrate. None of those have a metric
today, so detection relies on `kubectl describe` / logs per-object.

### Decision — **DECIDED (your call): build all EXCEPT the low-value one**
Build:
- `environment_available{name}` (gauge) — **P1** (highest health signal).
- `operator_external_ref_refresh_total{kind,type,result}` — P1.
- `platform_api_hydrate_duration_seconds` (histogram) — P1.
- `key_resolution_cache_hits_total` / `key_resolution_cache_misses_total {key_type,layer}` — P1/P2 (perf).
- `platform_api_login_total{outcome}` — P1/P2.

**Skip:** `operator_marketplace_plugin_count{marketplace}` (cosmetic, low value) —
drop from the §18.5 metric set rather than leaving it as a backlog promise.

### Follow-up work (NOT done yet)
- Add the five metrics at their call sites: operator (env reconcile → set
  `environment_available`; refresh driver → `operator_external_ref_refresh_total`),
  platform-api (hydrate handler → duration histogram; login/SSO+CLI → login total;
  key resolver → cache hit/miss).
- Platform-api `/metrics`: ensure these register on the platform-api collector
  (today it exposes only Go/process collectors).
- Spec `ach-spec-final-delivered.md` §18.5: mark the five as delivered; remove
  `operator_marketplace_plugin_count`.

### Still OPEN (carried forward)
Alert rules derived from these metrics (stale cache, litellm unreachable, failed
refresh, unavailable env, pk_ runtime usage) — not yet decided; revisit under the
"recommended alerts" roadmap item.

---

## G8 — `skill` / `skill-marketplace` missing from admin refresh

### Plain explanation
`admin refresh <kind> <name>` is the manual "go re-fetch this from upstream now"
lever (for debugging / forcing an update without waiting for the periodic refresh
or re-applying the CR). It works for plugin/prompt/artifact/marketplace — but
**not** for the newer `skill` / `skill-marketplace` kinds.

### What the code actually does (delivered)
- Server (`admin/handler.go:552`): valid kinds =
  `"plugin","prompt","artifact","pluginmarketplace"`. `skill`/`skillmarketplace`
  → `400 unknown kind`.
- CLI (`admin.go:149`): `allowedRefreshKinds = {plugin,prompt,artifact,marketplace}`
  (`marketplace`→`pluginmarketplace` server-side). No skill.
- `db.SetForceRefresh` (`refresh_signal.go:79`): switch handles `plugin/prompt/
  artifact` (→ `external_refs`) and `pluginmarketplace` (→ `marketplace_plugins`).
  No `skill`/`skillmarketplace` branch.
- **Consequence:** you cannot force-refresh a skill or skill-marketplace. The only
  way to update one is wait for the operator's periodic refresh or re-apply/poke
  the CR.

### Why this is the symptom, not the disease
`Skill`/`SkillMarketplace` were added fast and mirror `Plugin`/`PluginMarketplace`
almost everywhere (CRD, projection, content route, hydrate, admin inventory) —
but the admin-refresh surface was missed. That's exactly the "each new kind
duplicates a responsibility matrix and leaves small holes" pattern → the general
cure is the **kind-lifecycle contract** tracked separately as **G10**.

### The fix is mechanical (low effort)
Skills already live in `external_refs` (kind discriminator `skill`); skill-
marketplace skills live in `skill_marketplace_skills` (like `marketplace_plugins`):
- `SetForceRefresh`: add `"skill"` to the `external_refs` case; add
  `"skillmarketplace"` to a marketplace-style case targeting
  `skill_marketplace_skills`.
- Server handler valid-kind set: add `skill` + `skillmarketplace`.
- CLI `allowedRefreshKinds`: add `skill` + `skill-marketplace` (with name mapping).
- Confirm the `skill_controller` / `skillmarketplace_controller` honor
  `force_refresh_requested_at` on the `ach_refresh` notification (the plugin path
  already does).

### Decision — **DECIDED: fix it — "all CRs are same-class citizens"**
Stated principle (load-bearing, applies beyond G8): **every CR kind is a
first-class citizen — it must satisfy the full responsibility matrix; no
second-class kinds.** Consequences:
- **G8 immediate:** add `skill` + `skill-marketplace` to admin refresh
  (`SetForceRefresh` external_refs/`skill_marketplace_skills` cases, server
  valid-kind set, CLI `allowedRefreshKinds` with name mapping) and confirm the
  skill controllers honor `force_refresh_requested_at`. **P1.**
- **Parity audit (part of G8):** don't stop at refresh — audit `Skill` and
  `SkillMarketplace` against the **whole** matrix and close any other holes the
  fast-add left (see the matrix under G10). Likely candidates to re-check:
  admin-refresh (this), metrics, CLI render/describe/status, docs/spec symmetry.
- **G10 pre-decided:** this principle *is* the kind-lifecycle contract. Adopt it
  as policy so the hole class stops recurring (formalized at G10).

---

## G9 — Local package manager (`repo`/`plugin`/`skill`) framing

### Plain explanation
ACH has two ways to get a plugin/skill onto disk:
- **Governed:** `ach-cli env hydrate` — pulls from the ACH Hub, scoped to an
  Environment, authorized, audited, reproducible.
- **Local/ungoverned:** `ach-cli plugin install <name@repo>` / `skill install` /
  `repo add` — fetches **directly from git** into your adapter dirs, no Hub.
The risk: nothing tells the user which one they're using, and they share the same
nouns (`plugin`, `skill`).

### What the code actually does (delivered)
- Local PM lives in `internal/cli/localpkg/` (store/source/discover/manager) and
  fetches via `internal/gitfetch` straight from `github:`/`git:` sources.
- **Zero Hub calls** — `repo`/`plugin`/`skill` never touch `/platform`, `/content`,
  or hydrate. Installs are tracked in a local `installed.json`. Confirmed: no
  governed path involved.
- It **shares machinery** with the governed path — the conflict resolver "mirrors
  the governed [hydrate]" (`localpkg/manager/conflict.go`), and both write into the
  same adapter dirs. Good for consistency, but reinforces the confusion.
- **Current help text carries no governance framing** — `plugin.go`: "manages
  locally installed Claude Code plugins"; `repo.go`: "Manage local package
  repositories." Nothing says "ungoverned / not audited / bypasses the Hub."

### The tension
`plugin install foo@repo` looks like an ACH-managed action but is the opposite:
no Environment, no authorization, no audit, no central reproducibility. A user (or
an agent) could lean on it thinking they're inside the control plane. This is a
**positioning** problem, not a correctness bug — the feature is genuinely useful
DX (fast, local, no Hub ceremony).

### Decision — **DECIDED (your call): Option C — structural namespace**
Move `repo`/`plugin`/`skill` under a `local` parent group so the ungoverned
boundary is in the command path itself, and the top-level `plugin`/`skill` nouns
are freed (no collision with the governed concepts):
- `ach-cli local repo add|list|remove|update`
- `ach-cli local plugin install|uninstall|update|outdated|list`
- `ach-cli local skill install|uninstall|update|outdated|list`

Namespace word: **`local`** (recommended — matches the code package `localpkg` and
the existing help text; alternative `dev` is the only real other candidate —
minor, decide at implementation). Keep the explicit "ungoverned" wording (Option A
text) in the `local` group's Long help — C carries the boundary structurally, the
wording still explains *why*.

### Follow-up work (NOT done yet)
- Re-parent the three cobra commands under a new `ach-cli local` command; the
  `internal/cli/localpkg/*` impl is unchanged.
- `local` group Long help: "Local, ungoverned developer path — installs directly
  from git into your adapter dirs. Not governed by the ACH Hub (no Environment, no
  authorization, no audit, no central reproducibility). For governed/reproducible
  distribution use `ach-cli env hydrate`."
- Update spec `ach-cli-spec-final-delivered.md` §5.12 + Appendix A command tree to
  the `local`-namespaced form.
- **Breaking CLI change** (command paths move) — acceptable pre-release
  (v1alpha1). Minor open: ship hidden top-level aliases for one release as a
  transition, or hard-cut. Lean hard-cut (pre-release, cleaner).

---

## G10 — Kind-lifecycle contract ("all CRs same-class citizens")

### Plain explanation
Every new CR kind touches a dozen surfaces (CRD, DB projection, status, content
route, hydrate, admin, metrics, docs, tests). When a kind is added fast, small
surfaces get missed — exactly what happened to `Skill`/`SkillMarketplace`
(G8: admin refresh). The fix is a **checklist**: a kind isn't "done" until every
applicable surface is wired, in the same PR.

### Pre-decided
The **G8 principle "all CRs are same-class citizens"** already commits us to this.
G10 just formalizes it: write the checklist down and enforce it.

### Proposed checklist (ACH-tailored — confirm/adjust)
A new `*.ach.ackstorm.ai` kind MUST land every **applicable** row in the same PR.
Applicability differs by archetype — **object** (Plugin/Skill/Artifact/Prompt),
**discovery** (PluginMarketplace/SkillMarketplace), **governance** (Environment/
BackendIdentityPolicy), **config singleton** (LiteLLMConnection):

| # | Surface | Applies to |
|---|---------|------------|
| 1 | CRD + CEL validation, printcolumns, shortName; generated CRD + `make helm-sync-check` | all |
| 2 | Projection table (migration) + writer controller using `WithTxNotify` + `ach_<domain>_changed` channel | all |
| 3 | Status conditions (closed-set type+reason); `ConflictWithUIRow` + `origin`/`locked` if UI-writable | all |
| 4 | Reconciler `internal/controller/ach/<kind>_controller.go`; finalizer/deletion drain if external side effects | all |
| 5 | Content pipeline: F1 path-narrowing fetch, Stage-2 validation gate, cache path, `/content/<kind>/{name}` | content/object kinds |
| 6 | Admin inventory: `admin list <kind>` + `GET /platform/admin/<kinds>` | all |
| 7 | Admin refresh: `SetForceRefresh` case + server valid-kind set + CLI `allowedRefreshKinds` + controller honors `force_refresh_requested_at` | external-ref / discovery |
| 8 | Hydrate: manifest block + CLI stage/nest + adapter projection rule + `state.File` bucket | kinds that reach the workspace |
| 9 | Metrics: refresh-result counter + availability/health signal | all (as applicable) |
| 10 | Docs/spec: `../ach-spec/*-delivered.md`, README, CLAUDE.md, `examples/`, api-reference (`make gen-crd-ref-docs`) | all |
| 11 | Tests: unit + envtest + e2e fixture in `test/e2e/cluster/` | all |

### Decision — **DECIDED (your call)**
- **(a) Checklist:** adopted as-is (the table above). Archetype-aware applicability
  stays. Amend later if a surface is found missing.
- **(b) Home + enforcement: BOTH.**
  - **Reference doc** at **`docs/references/adding-a-cr-kind.md`** — the full
    checklist + archetype applicability matrix (the "how").
    *Note:* the repo's existing deep-narrative references live at **top-level
    `references/`** (e.g. `references/repo-layout.md`), and `docs/references/`
    does not exist yet. Honoring the stated `docs/references/` path; confirm at
    implementation whether to create `docs/references/` or place it under the
    existing `references/` to match convention.
  - **PR-template checklist** — a per-surface tick-list in
    `.github/pull_request_template.md` so the contract is a visible gate, not just
    a doc.
  - **CLAUDE.md pointer** — one row in the MANDATORY Reading Table → the reference
    doc, so agents read it before adding a kind.
- **Retroactive:** run the checklist against `Skill`/`SkillMarketplace` (folded
  into G8's parity audit) and `LiteLLMConnection`.

---

## G12 — Gateway as 6th mode: framing

### Plain explanation
`ach gateway` is a **dumb edge reverse proxy** — prefix-routes `/platform`,
`/content`, `/v1`, `/gemini`, `/mcp`, `/a2a`, `/.well-known` to the right service.
No auth, no `/metrics`, no `/dex`, no business logic. Its job: present **one
origin** (one Service/Ingress) in front of platform-api + content-service +
forwarder. The reviewer's only point: don't sell a logic-free proxy as an
architectural pillar.

### What the code actually does (delivered)
- `internal/gateway/` (proxy/routes/server); `doc.go` states "no auth … no
  /metrics … no /dex".
- **Same `ach` binary, `args:["gateway"]`** — NOT a separate image (my earlier
  correction to the review). It is a separate **Deployment** + a `gateway.enabled`
  Helm toggle.
- The public Ingress targets `ach-gateway`; in dev the nginx `ach-local-gateway`
  shim fronts it to add `/dex` + `/metrics/<svc>` for a single `localhost:8080`
  origin.
- **Optional:** `gateway.enabled` can be off; you can route per-service Ingress
  instead. Single-origin is the convenience, not a hard requirement.

### Decision — **DECIDED (your call): Option A — reframe as optional edge**
Keep the gateway; reframe in docs. The architecture is the **5 logic modes**
(operator, platform-api, forwarder, content-service, migrate); the gateway is an
**optional single-origin edge proxy** — logic-free, `gateway.enabled`-toggleable,
with per-service Ingress as the alternative. Docs-only; no behavior change.

### Follow-up work (NOT done yet)
- `../ach-spec/ach-spec-final-delivered.md` + README + CLAUDE.md: present gateway
  as optional edge/packaging, not a co-equal architectural mode; state it carries
  no logic and can be disabled.
- Keep the mode table, but annotate gateway as "edge proxy (optional)".

---

## G13 — `pk_` on runtime routes (unscoped) — UX / alerting

### Plain explanation
A `pk_` (Personal Key) is accepted on runtime routes (`/v1`, `/mcp`, …) just like
an `ek_`. But `pk_` is **not bound to any Environment**: LiteLLM authorizes it
against the **union of the user's capabilities**, with no Environment access-group
or attribution tag. `ek_` is the Environment-scoped key. The risk is purely
perception: a user may think a `pk_` runtime call is Environment-scoped when it
isn't.

### What the code actually does (delivered)
- `pk_` works on runtime routes, unscoped — **intentional**. The frozen spec made
  this a **permanent decision** and *removed* the "server-side toggle to forbid
  `pk_` on runtime" from the backlog (mental-model-is-contract): `pk_` = a person,
  `ek_` = a workload bound to an Environment.
- The hydrate `pk_` warning exists (`pkWarning`, `hydrate.go:71`) — though it
  currently renders in the post-run Tips footer, and its text still leans on the
  (now removed, see **G1**) budget rationale.
- Alerting is already feasible: `forwarder_requests_total{key_type}` carries the
  `pk`/`ek` label — a dashboard/alert on `key_type="pk"` on runtime routes needs
  no new metric.

### The decision space
The behavior is intentional and frozen-decided; this gap is about **UX hardening +
one optional policy fork**:
- **Always (regardless of fork):**
  - Reword the `pk_` hydrate warning (tie to **G1** — drop the budget
    justification; say "`pk_` is not Environment-scoped; use `ek_` for
    Environment-bound workloads / CI / agents").
  - Docs: explicit "dev/personal → `pk_`" vs "agent/CI/workload → `ek_`" guidance.
  - Recommended alert on `forwarder_requests_total{key_type="pk"}` for runtime
    routes (ties to the G7 alerts item).
- **Fork — optional server-side `pk_`-forbid switch:**
  - **Honor frozen (no toggle).** `pk_` runtime stays permanently allowed;
    enforcement is the user's deployment choice via LiteLLM, not ACH. Just ship the
    UX + alert.
  - **Add an optional per-deployment policy** to reject `pk_` on runtime routes for
    strict environments. **Reverses the frozen "permanent, no toggle" decision** —
    a real product change, server-side flag on the forwarder.

### Decision — **DECIDED (your call): UX hardening only; honor frozen (no toggle)**
- **(1) UX hardening — yes:**
  - Reword the `pk_` hydrate warning (tie to **G1** — drop budget rationale; say
    "`pk_` is not Environment-scoped; use `ek_` for Environment-bound workloads /
    CI / agents"). Consider rendering it pre-write, not only in the Tips footer.
  - Docs: explicit "dev/personal → `pk_`" vs "agent/CI/workload → `ek_`" guidance.
  - Recommended alert on `forwarder_requests_total{key_type="pk"}` for runtime
    routes (ties to the G7 alerts item).
- **(2) Fork — honor frozen:** **no server-side `pk_`-forbid toggle.** `pk_` on
  runtime stays a permanent first-class capability; strict-environment enforcement
  is the deployer's choice via LiteLLM, not an ACH switch. Mental-model-is-contract
  stands.

---

## G14 — LiteLLM `default` Team dependency — **ALREADY ADDRESSED (reviewer stale)**

### Plain explanation
Every SSO-provisioned user is enrolled into a LiteLLM team aliased `default`. The
reviewer flagged that ACH doesn't create it, so a missing `default` team would
500 the first login. **That bootstrap now exists** — the reviewer's claim was
based on the SSO path alone and missed the operator's proactive bootstrap.

### What the code actually does (delivered)
- `EnsureDefaultTeam` (`internal/litellm/team.go:29`): idempotent — list-first via
  `ListTeamsByAlias("default")`, `POST /team/new` only if absent.
- The **LiteLLMConnection reconciler calls it** (`litellmconnection_controller.go:229`):
  *"Operator-side bootstrap: guarantee LiteLLM has the canonical `default` team
  before any SSO callback fires. Idempotent — will retry on next reconcile."* This
  is exactly the recommended fix (the connection reconciler owns the bootstrap).
- The SSO path still renders `500 default_team_missing` (`auth/doc.go:22`) *if* the
  team is genuinely absent — but the operator proactively creates it, so in steady
  state it is present before logins.

### Residual (minor) — startup-ordering window
The only remaining edge: a login that arrives **before** the operator's first
successful `EnsureDefaultTeam` (operator just started / `LiteLLMConnection` not yet
`Ready` / LiteLLM briefly unreachable) can still 500. It **self-heals** — the next
reconcile creates the team and subsequent logins succeed. The 500 is transient,
not a permanent dead-end.

### Decision — **OPEN (your call) — but near-closed**
The core gap is fixed. Only choose how to treat the transient startup window:
- **A — accept it (recommended).** It self-heals; document the 500 as a transient
  "control plane still converging" and move on. Zero work.
- **B — harden it.** e.g. make the SSO `default_team_missing` path attempt a
  lazy `EnsureDefaultTeam` once before failing, or gate login readiness on
  `LiteLLMConnection Ready`. Small work, closes the window fully.
Tell me A or B. (Also: this corrects the review — G14 was largely a non-issue.)

---

## G15 — BackendIdentityPolicy: invisible conflicts + inverted tiebreaker

### Plain explanation
Two `BackendIdentityPolicy` CRs may name the same target (`kind`+`name`) — k8s
only enforces `metadata.name` uniqueness. When that happens the Forwarder must
pick one. Two problems: (1) the pick **direction was inverted** vs the frozen
spec, and (2) the loser gets **no status**, so `kubectl describe` gives the
operator no clue which BIP actually applies.

### What the code actually does (delivered)
- **Tiebreaker = alphabetically LAST.** `bipcache/cache.go:72` sorts by Name ASC
  and takes `rows[len-1]` ("alpha-LAST winner per the ResolveWinner contract"). The
  frozen spec specified alphabetically **FIRST** (`min(name)`). **Inverted.**
  - Twist worth keeping in mind: if the alpha-LAST row has
    `forwardIdentityJWT=false`, the result is **no JWT** (explicit opt-out wins) —
    any status computation must mirror this exact rule.
- **No conflict status.** `backendidentitypolicy_controller.go:39,72,153` — the
  operator deliberately "stays dumb on BIP duplicates" (OP-16): it emits **no**
  `Synced=DuplicateTarget`, no positive `Synced=True`; steady-state = no condition.
  The only `Synced=False` it ever writes is `ConflictWithUIRow` (the dormant G2
  one). So a shadowed BIP looks identical to an applied one.
- Runtime is unaffected by status (Forwarder resolves directly from the cache) —
  this is good (no status-write latency in the hot path), but it also means status
  is the *only* place visibility could live, and it's empty.

### Two decisions
**(1) Tiebreaker direction.** Either direction is a fine deterministic edge-case
rule; what matters is that **code, spec, and tests agree** (today they don't —
spec says FIRST, code does LAST).
- **Keep alpha-LAST** (no code change; fix the spec/tests to match — recommended,
  least churn), or
- **Restore alpha-FIRST** (match frozen; change `cache.go`).

**(2) Visibility of shadowed BIPs.**
- **A — write a status condition on losers** (e.g. `Effective=False` /
  `Shadowed=True` with `message: "shadowed by BackendIdentityPolicy/<winner>"`).
  Operator computes duplicate groups and **mirrors the Forwarder's ResolveWinner
  rule exactly** (incl. the opt-out twist) so status can't lie. Restores
  `kubectl describe` feedback. This re-introduces the frozen §9.3 `DuplicateTarget`
  computation that OP-16 descoped — but as advisory status only (runtime stays
  forwarder-resolved). Medium effort.
- **B — lighter signal:** a Kubernetes Event on the loser and/or a metric
  (`bip_duplicate_targets`), no per-CR condition. Cheaper, less precise.
- **C — accept + document.** Duplicates are rare; just document the alpha-LAST
  rule so operators can reason manually. Zero code.

### Decision — **DECIDED (your call): restore alpha-FIRST + standardized conflict condition (A)**

**(1) Tiebreaker → restore alpha-FIRST.** Reviewed the codebase: alpha-FIRST /
first-wins is the **house convention**; BIP's alpha-LAST is the lone outlier.

| Conflict point | Direction |
|---|---|
| **BIP duplicate target** (`bipcache/cache.go:72`, `db/backend_identity_policies.go:8`) | **alpha-LAST** ← outlier, FIX |
| PluginMarketplace intra-marketplace dedup (`pluginmarketplace_controller.go:328`) | first-wins ✓ |
| Hydrate `ConflictNamespace` plugin collision (`wiring.go:548`) | keep earliest/first ✓ |
| `ConflictOverwrite` (CLI `conflict.go:20`, localpkg `conflict.go:32`) | last-wins — *different semantic (explicit overwrite policy, not a name-tiebreak); leave as-is* |

Flip BIP to **alpha-FIRST** (`min(name)`), matching frozen + the house convention.
Touch points: `internal/forwarder/bip.ResolveWinner` (the contract fn),
`bipcache/cache.go` (`rows[len-1]` → `rows[0]`), `bipcache/doc.go`,
`db/backend_identity_policies.go`, `proxy/handlers.go`, and the alpha-LAST tests.
**Keep the opt-out twist** — if the now-alpha-FIRST winner has
`forwardIdentityJWT=false` → no JWT (just applied to the first row instead of last).

**(2) Visibility → A, with a STANDARDIZED conflict condition.** Write a condition
on the loser **and** standardize the conflict vocabulary across all kinds (your
call). Proposal — reuse the existing `Synced=False` + reason pattern (already used
by `ConflictWithUIRow`), adding a **standard `reason=NameConflict`**:
- Loser BIP: `Synced=False, reason=NameConflict, message:"shadowed by
  BackendIdentityPolicy/<winner>"`.
- Operator computes duplicate groups and mirrors the Forwarder's ResolveWinner
  rule exactly (now alpha-FIRST + opt-out) so status can't lie. Runtime stays
  forwarder-resolved (status advisory only).
- **Standardize:** `NameConflict` becomes the canonical reason for "another CR of
  the same kind claims the same identity/target," used by *every* kind that can
  collide; `ConflictWithUIRow` stays the canonical reason for cross-origin
  conflicts. Add "emits `Synced=False/NameConflict` on duplicate identity" to the
  **G10 kind-lifecycle checklist** so new kinds inherit it.
  - Open nit: condition *type* — keep it as a reason on the standard `Synced`
    condition (recommended, matches `ConflictWithUIRow`), vs a dedicated
    `NameConflict=True` condition type. Lean reason-on-`Synced` for consistency;
    confirm at implementation.

---

## G16 — Operator + Content Service single replica (RWO PVC)

### Plain explanation
The content cache lives on a `ReadWriteOnce` (RWO) volume — only one Pod can mount
it. The operator **writes** content there (fetch → cache); the content-service
**reads** it (`sendfile`). RWO forces both into the **same Pod** (content-service
is a sidecar of the operator). Result: content serving can't scale horizontally,
and content availability is tied to the operator Pod.

### What the code actually does (delivered)
- Cache PVC `ach-operator-cache` = **ReadWriteOnce** (`operator-deployment.yaml:22`).
- Content-service is a **sidecar in the operator Pod** — explicitly "RWO mandates
  single-Pod mounters, hence co-location" (`operator-deployment.yaml:154`).
- `operator.replicas` default **1**.
- **The fix is already designed-for:** in-code comment — *"If the storage class is
  upgraded to RWX (EFS/NFS/CephFS) this Pod can later be split"*
  (`operator-deployment.yaml:155-156`).

### Important scoping — only the content path is structurally limited
- **forwarder / platform-api / gateway are stateless** — they default to
  `replicas: 1` in `values.yaml` but can scale to N today; no structural limit.
- **operator** is single-active anyway (controller-runtime leader election is the
  norm).
- The real coupling is **content-service ↔ operator Pod** (RWO): content serving
  availability == operator Pod availability; an operator rolling update / crash
  blips content serving, and content-service can't have its own replicas.

### Decision — **OPEN (your call)**
- **For v1alpha1: accept (recommended).** Single-replica content path is fine for
  the current target; just **document the limitation** (content availability tied
  to operator Pod; no content-service HA) and confirm the stateless services
  (forwarder/platform-api/gateway) are scaled as needed.
- **P2 — pick the decoupling path (or defer):**
  - **RWX split (recommended P2).** Upgrade the storage class to RWX
    (EFS/NFS/CephFS), split content-service into its own Deployment with N replicas
    reading the shared volume. **The code already anticipates this** — smallest
    change, keeps `sendfile`.
  - **Object-store backend.** Operator writes to S3/GCS/minio; content-service
    streams from it. Most cloud-native + fully decoupled, but bigger change and
    **loses `sendfile`** (streams instead). 
Tell me: accept for v1alpha1 (yes/no), and the P2 direction (RWX split / object-
store / defer).

### Decision — **DECIDED (your call): accept for v1alpha1; P2 direction deferred**
- **v1alpha1:** accept the single-replica content path. **Document the limitation**
  (content-serving availability tied to the operator Pod; no content-service HA;
  scale the stateless forwarder/platform-api/gateway as needed via `replicas`).
- **P2:** decoupling deferred — not chosen now. The **RWX split** is the
  code-anticipated path when it's picked up (storage class → RWX, split
  content-service into its own Deployment). Revisit at the scaling/HA milestone.

---

## G17 — JWT `nbf` removed (spec says backends MUST verify it)

### Plain explanation
The frozen JWT contract (§9) lists `nbf` ("not before") as a claim ACH emits and
tells backends they MUST verify it. The delivered Forwarder **does not emit `nbf`**.
On its own that's a contract inconsistency; the question is whether it actually
breaks any backend.

### What the code actually does (delivered)
- `signer.go:158-184`: Sign mints `iss`, `sub`, `aud`, `iat`, `exp` (+ `email`).
  **No `nbf`** (and no `jti`, which is correct per §9).
- **Severity is LOWER than first flagged** — the reference backend doesn't require
  `nbf`:
  - mcp-echo `verify.go` enforces `iss` + `aud` + `exp` (`WithExpirationRequired`);
    it does **not** require `nbf`.
  - `jwt-go/v5` validates `nbf` only **if present**; an absent `nbf` is no
    constraint. So a missing `nbf` passes a normal verifier.
  - => Removing `nbf` does **not** break the reference fixture. This is
    **spec-vs-code drift**, not a live interop break.
- Residual risk: a *third-party* backend written strictly to the frozen spec that
  **requires `nbf` presence** (uncommon — most libs treat `nbf` as optional) would
  reject ACH tokens.

### The real trade (why `nbf` might have been dropped)
`nbf = iat` with zero leeway is a **clock-skew footgun**: a backend whose clock is
slightly behind the issuer sees `nbf` in the future and rejects a freshly-minted
token. The 120-second `exp` already bounds validity, so `nbf` adds little for such
a short-lived token while adding skew risk. Dropping it is defensible.

### Decision — **OPEN (your call)**
Pick one (both close the inconsistency):
- **Align spec to code — drop `nbf` (recommended).** Update frozen/delivered §9:
  ACH emits `iss/sub/aud/iat/exp` (+`email`), no `nbf`; backends MUST verify
  `iss/aud/exp` (remove `nbf` from the MUST list). Zero code change; avoids the
  clock-skew footgun. Note in `writing-an-mcp-backend` docs that `nbf` is absent.
- **Restore `nbf` in code.** Add `"nbf"` to Sign to match the frozen spec exactly.
  One line — but reintroduce the skew risk; mitigate with a small negative leeway
  (`nbf = now - 60`) rather than `nbf = now`, and tell backends to verify with
  leeway.
Lean: **drop `nbf`** (align spec to code) — simpler and skew-safe, and the
reference backend already doesn't use it. Tell me which.

### Decision — **DECIDED (your call): drop `nbf` (align spec to code)**
ACH does not emit `nbf`; the contract is updated to match.
- Spec `ach-spec-final-delivered.md` §9 + §9.1: remove `nbf` from the claims block
  and from the "backends MUST verify" list; state the minted set is
  `iss/sub/aud/iat/exp` (+`email`), no `nbf`, no `jti`. `exp = iat + 120` is the
  sole time bound.
- `docs/runbooks/writing-an-mcp-backend.md` + mcp-echo README: note `nbf` is absent;
  verify `iss/aud/exp` only.
- No code change.

---

## G18 — JWT `sub` is now the bare owner email (dropped `<namespace>/` prefix)

### Plain explanation
The frozen contract said `sub = "<namespace>/<owner-email>"` and told backends to
"parse on the first `/`." The delivered Forwarder sets `sub` to the **bare owner
email** (no namespace prefix) and **adds an `email` claim** with the same value.
This was an **intentional change** (commit `4c646d4`, "drop namespace prefix from
JWT sub, use bare owner email").

### What the code actually does (delivered)
- `handlers.go:131-136` / `signer.go`: `sub = kc.OwnerEmail` (bare), plus
  `email = kc.OwnerEmail`. The `Namespace` field was removed from the forwarder
  deps entirely.
- **Not a live break** — the reference mcp-echo backend returns `sub` **verbatim**
  (`verify.go` `Verified.Sub`); it does not split on `/`. Only a third-party
  backend written to the frozen spec that extracts a namespace from the `sub`
  prefix would mis-read.
- The change is **sensible**: `iss` already scopes the deployment/signing
  authority; the frozen spec itself conceded "namespace prefixes inside `sub` …
  do not disambiguate signing trust, which is scoped by `iss` alone." So the
  prefix was redundant for trust — only a cross-deployment principal-labelling
  nicety, an edge case.

### Note — `sub` and `email` are now identical
Both carry the bare owner email → redundant. Harmless (`email` is a conventional
claim some backends prefer), but worth a deliberate keep/drop.

### Decision — **OPEN (your call)**
- **Ratify bare-email `sub` (recommended)** — keep the intentional change; make the
  contract consistent: spec §9/§9.1 already documents bare `sub`; ensure
  `writing-an-mcp-backend` docs say "principal = `sub` (bare email); key on
  `iss`+`sub`," and add a one-line **compat note** that the legacy `<ns>/<email>`
  form is gone (pre-release hard-cut). 
  - Sub-decision: **keep the `email` claim** (harmless, conventional) or **drop it**
    as redundant with `sub`. Lean keep (cheap forward-compat for email-keyed
    backends).
- **Revert to `<namespace>/<email>`** — only if you actually need cross-deployment
  principal disambiguation inside `sub` beyond what `iss` gives. Unlikely.
Tell me: ratify (and keep/drop `email`) or revert.

### Decision — **DECIDED (your call): ratify bare-email `sub`; keep `email` claim**
- `sub` = bare owner email is the contract (the intentional `4c646d4` change stands).
- **Keep** the `email` claim (bare owner email) — harmless, conventional, useful
  for email-keyed backends, even though it duplicates `sub`.
- Docs: `writing-an-mcp-backend` + mcp-echo README — principal = `sub` (bare
  email), key on `iss`+`sub`; add a one-line compat note that the legacy
  `<namespace>/<email>` form is gone (pre-release hard-cut). Spec §9/§9.1 already
  reflects bare `sub` + `email`.
- Clean up the stale "namespace-qualified" comments at `signer.go:47,181`.

---

## G19 — HTTPS enforcement downgraded to warning-only

*(Security item — plain prose.)*

### Plain explanation
The frozen spec required ACH to **refuse** non-`https://` URLs. The delivered code
**accepts `http://`** in two places, downgrading transport security:
1. **CLI → Hub** (the important one): the CLI accepts an `http://` Hub URL and only
   prints a warning. Over `http://`, the CLI sends the `pk_`/`ek_` **bearer
   credential in cleartext** — interceptable on the wire.
2. **Operator HTTPSource fetch** (lower risk): the operator will fetch content from
   an `http://` source — plaintext, but it's content (not credentials) and the
   concession exists for in-cluster e2e fixture servers.

### What the code actually does (delivered)
- CLI `config.go`: `url:` must be `http://` or `https://`; **`http://` is
  accepted**, the command layer only warns about "plaintext transport"
  (`login.go:176`, `hydrate.go:434`, `config.go:402`). `ErrInvalidURLScheme` fires
  only for *non*-http(s) schemes (ftp, empty). **No hard refusal, no `--insecure`
  gate.** (The `doc.go:17` "HTTPS-only refusal" comment is stale.)
- HTTPSource `fetcher.go:12-15,54-56`: `New()` accepts `http://` and `https://`;
  the HTTPS-only invariant (T-02-02-03) was **lifted in Phase 02.1** for in-cluster
  fixtures; "deployments are expected to use https:// by convention" — but nothing
  enforces it.

### Risk asymmetry
- **CLI = high**: `pk_`/`ek_` over plaintext `http://` to a remote Hub = credential
  interception. This is the real exposure.
- **HTTPSource = low**: cluster-internal content fetch; content integrity is a
  separate concern; the e2e fixtures genuinely need plaintext.

### Decision — **OPEN (your call)**
**CLI (pick one):**
- **A — loopback-allow, remote-refuse (recommended).** Auto-allow `http://` only
  for `localhost`/`127.0.0.1` (local dev, frictionless); **refuse** remote
  `http://` Hub URLs. Protects credentials by default without a flag.
- **B — default-refuse + explicit `--insecure`/`ACH_INSECURE` opt-in.** Strict;
  any `http://` (incl. localhost) needs the opt-in. Restores frozen posture
  literally.
- **C — keep warn-only.** Accept the downgrade. Weakest.

**HTTPSource (pick one):**
- **D — keep the `http://` concession, but document it as dev/e2e-only** and (nice-
  to-have) gate it behind a dev/insecure flag or restrict to cluster-internal
  hostnames. Recommended — low risk, e2e needs it.
- **E — restore https-only + an explicit dev escape hatch.** Stricter; more work.

Lean **A** (CLI) + **D** (HTTPSource). Tell me your picks.

### Decision — **DECIDED (your call): CLI = B (default-refuse + opt-in); HTTPSource = D**
- **CLI → B.** Default-**refuse** any non-`https://` Hub URL (incl. localhost). Add
  an explicit opt-in (`--insecure` flag and/or `ACH_INSECURE=1`) required for any
  `http://`. Restores the frozen HTTPS-only posture literally; plaintext only
  happens when the user deliberately opts in. Fix the stale `doc.go:17` comment to
  match. Enforce in `config.go` Load/Save (not just a command-layer warning).
- **HTTPSource → D.** Keep the `http://` concession but **document it as
  dev/e2e-only**; nice-to-have follow-up: gate it behind a dev/insecure flag or
  restrict plaintext to cluster-internal hostnames. Low risk (content, in-cluster).

---

## G20 — Audit events dropped fields

### Plain explanation
ACH emits structured audit events (who did what, outcome). The frozen schema had
more fields than the delivered event carries — and since audit/governance is a
selling point, the missing fields weaken the trail.

### What the code actually does (delivered)
- `audit/emit.go:44` `Event` = `Action, Outcome, Actor, RequestID, KeyID,
  Target{Kind,Name}, Extra map[string]string`.
- **Missing vs frozen schema:**
  | Field | Status | Value |
  |-------|--------|-------|
  | `environment` | only via `Extra` map (ad hoc) | **high** — the core governance dimension ("who did what to *which Environment*") |
  | `source.ip` | **not captured** (middleware reads only `x-ach-key`) | medium — forensics |
  | `source.user_agent` | **not captured** | medium — forensics |
  | `route` | not captured | medium — context for forwarder/content events |
  | `key.type` (pk/ek) | absent, but **derivable** from `KeyID` prefix (`pkid_`/`ekid_`) | low — redundant |
  | `timestamp` | not an explicit field; the slog handler adds it | low — confirm handler emits it |
- **Action renames** (cosmetic): `content.download`→`content.get`,
  `hydrate`→`platform.hydrate`. Spec-vs-code naming drift, not a data gap.

### Decision — **OPEN (your call)**
Pick the subset to promote to first-class audit fields:
- **`environment` → first-class (recommended).** Highest governance value; today
  it's buried in `Extra`. Make it a typed field set on every env-scoped action.
- **`source.ip` + `source.user_agent` → add (recommended for security).** Requires
  plumbing `r.RemoteAddr`/`X-Forwarded-For` + `User-Agent` from the request into
  the audit context (not captured today).
- **`route` → add (optional).** Useful for forwarder/content events.
- **`key.type` → derive or skip.** Cheap to derive from the `KeyID` prefix; or skip
  as redundant.
- **`timestamp` → verify** the slog handler emits it; add only if absent.
- **Action names → keep code, align spec.** Don't churn code for `content.get` vs
  `content.download`; update the spec to the delivered names.
Tell me which fields to add (and confirm "keep code names, fix spec").

### Decision — **DECIDED (your call): add high + medium fields**
- **`environment` → first-class** (high). Typed field, set on every env-scoped
  audit action; stop relying on the `Extra` map.
- **`source.ip` + `source.user_agent` → add** (medium/security). Plumb
  `r.RemoteAddr` / `X-Forwarded-For` + `User-Agent` from the request into the audit
  context (new capture in middleware).
- **`route` → add** (medium). Include the request route on forwarder/content events.
- **`key.type` → skip** (low) — derivable from the `KeyID` prefix (`pkid_`/`ekid_`);
  not worth a dedicated field.
- **`timestamp` → verify** the slog handler emits it; add only if missing.
- **Action names → keep code, fix spec.** Keep `content.get` / `platform.hydrate`;
  update the frozen/delivered spec audit table to the delivered names (no code
  churn).

---

## Consolidated roadmap (derived from the G1–G20 decisions)

### P0 — security / correctness, pre-production blockers
- **G3** — encrypt LiteLLM `sk-` at rest (AES-256-GCM, k8s-Secret DEK); add the
  non-production guard + security acceptance criterion. *(blocker)*
- **G19** — CLI: default-**refuse** non-`https://`; explicit `--insecure`/
  `ACH_INSECURE` opt-in. *(credentials over plaintext)*
- **G5** — wire `--sync` (STATE-05 composition) so it prunes dropped-resource files
  instead of silently no-op'ing. *(credential-file leak / honesty)*

### P1 — close v1alpha1
- **G1** — purge per-Environment budget wording (budget = user-level now, key-level
  future); reword `pk_` warning. *(docs)*
- **G4** — publish `ghcr.io/ackstorm/ach-cli` image + InitContainer/Job example.
- **G6** — build `content fetch`; drop `platforms list`.
- **G7** — add metrics: `environment_available`, `operator_external_ref_refresh_total`,
  `platform_api_hydrate_duration_seconds`, key-resolution cache hit/miss,
  `platform_api_login_total`.
- **G8** — add `skill`/`skill-marketplace` to admin refresh + run the parity audit.
- **G9** — re-parent local PM under `ach-cli local {repo,plugin,skill}`.
- **G10** — write `docs/references/adding-a-cr-kind.md` + PR-template checklist +
  CLAUDE.md pointer ("all CRs same-class").
- **G13** — `pk_` UX: reword warning (pre-write), dev-vs-agent docs, pk-runtime
  alert. *(no server-side toggle — honor frozen)*
- **G15** — BIP: restore alpha-FIRST tiebreaker; add standard `Synced=False/
  NameConflict` condition on losers.
- **G17** — drop `nbf` from the JWT contract (spec/docs only).
- **G18** — ratify bare-email `sub` (keep `email`); fix docs + stale comments.
- **G20** — audit: add `environment` (first-class) + `source.ip`/`source.user_agent`
  + `route`; align spec to delivered action names.
- **G2 (docs half)** — frame v1alpha1 as GitOps/CRD-only write path; mark UI
  Objects/export/promotion as reserved P2; note `ConflictWithUIRow` is dormant.
- **G12** — reframe gateway as an optional edge proxy (docs).
- **G14** — document the transient `default_team_missing` startup window (accepted).

### P2 — evolution
- **G2 (build half)** — UI Objects API (`origin='ui'` writes) + GitOps export + the
  **promotion reconcile** (cr-apply over a matching ui row → flip origin, lock).
- **G16** — content HA: RWX-split (code-anticipated) or object-store cache backend.
- **G7 (alerts)** — recommended alert rules (see carried-forward).

## Carried-forward open items (not yet decided)
- **G7 alerts** — alert rules from the new metrics (stale cache, litellm
  unreachable, failed refresh, unavailable env, `pk_` runtime usage). Revisit.
- Minor implementation nits to settle at build time: G15 condition *type*
  (reason-on-`Synced` vs dedicated `NameConflict=True`); G9 namespace word
  (`local` vs `dev`); G4 base image (distroless vs alpine); G10 doc home
  (`docs/references/` vs existing top-level `references/`); G2 transition aliases
  (hidden vs hard-cut); G9 hidden top-level aliases.

## Corrections to the external review (for the record)
- **G14** (`default` Team) — **already fixed**; the operator bootstraps it via the
  LiteLLMConnection reconciler. Review was stale.
- **G17** (`nbf`) — **not a live break**; the reference backend doesn't require
  `nbf`. Downgraded from "P0 interop break" to spec-vs-code drift.
- **G18** (`sub`) — **intentional and sensible** (`iss` already scopes the
  deployment); not a live break (reference backend doesn't split `sub`).
- **G12** (gateway) — it is the **same `ach` image** (arg-selected), not a separate
  image as the review implied.
- **G1** (budget) — per-Environment budget needs **LiteLLM Enterprise** (tags are
  Enterprise-only; OSS drops them) and leaks via `pk_` — a sharper reason to drop
  it than the review gave.
