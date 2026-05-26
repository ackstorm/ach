# FIX01 — findings from the examples/hydrate-demo bring-up

Running `examples/hydrate-demo.sh` end-to-end against the live e2e
cluster surfaced a stack of real bugs and configuration drift between
ACH and LiteLLM v1.83.10. None of these are demo-script-isolated —
each one blocks the real `ach login` + `ach hydrate` CLI commands too
(once Phase 6/7 land), and every one needs a proper fix in `main/`
before the example can run without manual tricks.

Each entry has: severity, the symptom we saw, the root cause, the
suggested fix, and the file path that needs the patch.

---

## A. SSO user-provisioning is broken end-to-end (BLOCKER)

The chain SSO callback → LiteLLM provisionUser → mint pk_ has three
distinct bugs that cascade. All three must be fixed for `examples/
hydrate-demo.sh` to produce the `hydrate.json` artifact without
hacks.

### A.1 — `UserInfo.Teams` decode fails against LiteLLM 1.83 `/user/info` — **DONE**

- **Severity**: HIGH (every first-time SSO attempt fails).
- **Where**: `internal/litellm/users.go` `UserInfoByEmail` +
  `internal/litellm/types.go` `UserInfo`.
- **Symptom**: `provisionUser` returns
  `litellm_unreachable` audit outcome; HTTP 503 from
  `/platform/auth/sso/callback`.
- **Root cause**: LiteLLM v1.83 returns the top-level `teams` field
  as an array of *team objects*
  (`[{"team_id":"…","team_alias":"…", …}]`), not an array of strings.
  ACH's `UserInfo.Teams` is typed `[]string`, so `json.Unmarshal`
  errors with `cannot unmarshal object into Go struct field
  UserInfo.teams of type string`. The error short-circuits before
  the "not found" check and is misclassified as a transport failure.
- **Fix sketch**: Either retype `UserInfo.Teams` to `[]TeamSummary`
  (where `TeamSummary` carries `team_id` + `team_alias`), or keep
  `[]string` and decode into a private envelope that flattens
  `teams[*].team_id` (the value shape downstream `platformapi/teams/
  lookup.go` expects). Update `internal/litellm/users_test.go` +
  `client_test.go` fixtures to use the real LiteLLM 1.83 response
  body.

### A.2 — `/user/info` returns 200 placeholder for unknown emails — **DONE**

- **Severity**: HIGH (interacts with A.1 — once that is fixed, this
  one becomes the next failure mode).
- **Where**: `internal/litellm/users.go` `UserInfoByEmail` + the
  `isLiteLLMNotFound` predicate in
  `internal/platformapi/auth/sso.go`.
- **Symptom**: After A.1 is fixed, `UserInfoByEmail` returns a
  `UserInfo` with `user_id="default_user_id"` and `user_email=""`
  for an email LiteLLM has never seen. `provisionUser` takes the
  "existing user" branch and calls `TeamMemberAdd("default",
  "default_user_id", "user")`, which fails (admin is already on the
  team) → outcome `default_team_missing`.
- **Root cause**: LiteLLM 1.83 does NOT return 404 on
  `GET /user/info?user_email=<unknown>`; it returns 200 with the
  catch-all admin placeholder `default_user_id` and an empty
  `user_email` field. ACH's "not found" branch relies on the
  generic 4xx-as-error wrapping that never fires here.
- **Fix sketch**: In `UserInfoByEmail`, treat
  `user_id == "default_user_id" && user_email == ""` as
  `ErrNotFound`. Document the LiteLLM 1.83 quirk in the helper's
  doc comment. Add a unit test exercising the placeholder.

### A.3 — Duplicate-add 4xx from `/team/member_add` is fail-loud — **DONE**

- **Severity**: MEDIUM (interacts with A.1 + A.2 — symptom shows up
  once the first two are fixed).
- **Where**: `internal/platformapi/auth/sso.go` `provisionUser`,
  both the first-time and existing-user branches.
- **Symptom**: After A.1 + A.2 are fixed, the first-time branch
  succeeds at `UserNew(teams: ["default"])` (LiteLLM auto-enrolls
  the user), then the explicit `TeamMemberAdd("default", new_user_id,
  "user")` returns 400 *"already added"* and the SSO callback
  surfaces `default_team_missing`.
- **Root cause**: LiteLLM `UserNew(teams: [...])` already enrolls
  the user in those teams. The follow-up `TeamMemberAdd` is
  defensive idempotency but fails on duplicate-add. The
  `TeamMemberAdd` helper's doc comment says "caller decides" what
  to do with duplicate-add 4xx; `provisionUser` decides to fail
  loud, which is wrong for the steady-state expected outcome.
- **Fix sketch**: Add an `isDuplicateAddErr(err)` predicate in
  `internal/litellm/team.go` (string match on "already" + "400").
  In `provisionUser`, swallow that error on BOTH branches (log at
  info level for traceability). The defensive intent stays — genuine
  team-not-found errors still surface as
  `default_team_missing`.

### A.6 — `/key/generate` rejects ACH's `pk_` prefix (LiteLLM enforces `sk-`)

- **Severity**: HIGH (last blocker on the SSO → pk_ → hydrate path).
- **Where**: `internal/platformapi/auth/sso.go` step 6b — the
  `deps.LiteLLM.KeyGenerate(...)` call that supplies `Key:
  plaintext`. Also `internal/litellm/client.go` doc comment on
  `KeyGenerate` claims "LiteLLM stores ACH's prefix verbatim" —
  that claim is false for LiteLLM v1.83.10.
- **Symptom**: Manual probe response:
  `Invalid key format. LiteLLM Virtual Key must start with 'sk-'.
  Received: pk_t****1234`. SSO callback surfaces
  `litellm_unreachable` with message `litellm key/generate
  unreachable`.
- **Root cause**: LiteLLM v1.83 enforces a `sk-` prefix on
  virtual-key plaintexts and 400-rejects anything else. ACH's
  Phase 3 D-13 design (bearer plaintext stored verbatim across both
  ACH's Postgres and LiteLLM's KV) is incompatible with that
  upstream invariant.
- **Fix sketch** (design decision needed — STOP):
  Option A: drop `req.Key` from KeyGenerate; let LiteLLM
  auto-generate an opaque `sk-…` token; store ACH's `pk_…` plaintext
  in Postgres only; the forwarder translates `pk_…` →
  `sk-…` (via the persisted token mapping) before calling
  LiteLLM. Cleanest separation but requires the forwarder to do the
  swap on every request.
  Option B: prefix-swap the bearer at KeyGenerate time —
  `req.Key = "sk-" + strings.TrimPrefix(plaintext, "pk_")` — and
  reverse-map on inbound. Keeps Postgres + LiteLLM in lockstep on a
  single plaintext value but exposes ACH's internal `pk_` naming
  through the LiteLLM admin UI.
  This is the last A-series blocker — once decided + implemented,
  `examples/hydrate-demo.sh` should produce
  `examples/hydrate.json` end-to-end.

### A.5 — `/user/info` returns placeholder even for EXISTING users — **DONE**

- **Severity**: HIGH (interacts with A.2 — second-run SSO fails 409
  on UserNew because the user actually does exist).
- **Where**: `internal/litellm/users.go` `UserInfoByEmail`.
- **Symptom**: After A.2 routes the placeholder to ErrNotFound, the
  first-time branch calls UserNew which returns 409 because the user
  already exists from a prior SSO attempt. provisionUser surfaces
  `litellm_unreachable`.
- **Root cause**: LiteLLM v1.83 `/user/info?user_email=…` returns
  the `default_user_id` admin placeholder both for unknown emails
  AND for known-but-existing users — the email-keyed lookup is
  broken upstream. `/user/list?user_email=…` does work and returns
  the actual user row.
- **Fix**: In `UserInfoByEmail`'s placeholder branch, fall back to
  `/user/list?user_email=…` and return the first matching entry
  before declaring `ErrNotFound`.

### A.4 — `TeamMemberAdd` is called with `team_id="default"` literal — **DONE**

- **Severity**: MEDIUM (works only because we manually pre-seed
  LiteLLM with a team whose `team_id="default"` literal — see
  examples/hydrate-demo.sh step 1 trick).
- **Where**: `internal/platformapi/auth/sso.go` `provisionUser`.
- **Symptom**: Without the pre-seed trick, `TeamMemberAdd` fails
  because LiteLLM's actual `team_id` is a UUID, not the alias
  `"default"`.
- **Root cause**: ACH conflates `team_alias` (`"default"`) with
  `team_id` (UUID). Production deployments would need either a
  team-alias-to-team-id lookup in `provisionUser` or LiteLLM has to
  accept both interchangeably (it does for `team_id="default"` as a
  string but only if the deployer pre-creates with that literal id —
  not the default UUID auto-assign).
- **Fix sketch**: In `provisionUser`, look up the team's actual
  `team_id` by alias via `ListTeamsByAlias("default")` before calling
  `TeamMemberAdd`. Cache the resolved id (it doesn't change).

---

## B. Helm chart values had stale Dex coordinates

These are configuration-only and have been fixed in
`test/e2e/values/ach.values.yaml` so the e2e cluster + the
example script work today. Mentioning them here so a future code
reviewer knows WHY the values diverged from ach-old.

### B.1 — `ACH_DEX_CLIENT_SECRET` did not match the Dex staticClient

- **Where**: `test/e2e/values/ach.values.yaml`,
  `scripts/dex-config.yaml`.
- **Was**: `ACH_DEX_CLIENT_SECRET=dev-dex-client-secret` (Helm)
  vs. `secret: dev-secret-not-for-prod` (Dex).
- **Symptom**: SSO callback returned 502
  `sso code exchange failed` because Dex `/token` rejected the
  client_secret. Fixed by aligning both to `dev-secret-not-for-prod`.

### B.2 — `ACH_DEX_REDIRECT_URL` did not match the registered redirectURI

- **Where**: same files as B.1.
- **Was**: `https://ach.local.test/login/callback` (Helm) vs. a
  staticClient redirectURI of
  `http://localhost:8080/platform/auth/sso/callback` (Dex).
- **Symptom**: Dex rejected the `/authorize` request with
  `Unregistered redirect_uri`. Fixed by aligning the Helm value to
  the registered URL (real deployments will use a public ingress
  URL behind TLS — the value would then change in the production
  values overlay).

---

## C. Operator-side conditions don't expose `Ready`

### C.1 — No composite `Ready` condition on the `Environment` CR

- **Severity**: LOW (cosmetic — engineers can `kubectl wait
  --for=condition=ExecutionResourcesResolved` and `--for=condition=
  AccessGroupSynced` separately, but every other Kubernetes
  controller emits a `Ready` rollup).
- **Where**: `internal/controller/ach/environment_controller.go`,
  `api/ach/v1alpha1/environment_types.go`.
- **Symptom**: `kubectl wait --for=condition=Ready environment/demo`
  times out because no controller writes that condition.
  `examples/hydrate-demo.sh` had to wait on
  `ExecutionResourcesResolved` instead.
- **Root cause**: Documented closed-set conditions
  (`Available`, `ContentReady`, `ExecutionResourcesResolved`,
  `AccessGroupSynced`) are only partially emitted today. There's no
  Ready rollup that combines them.
- **Fix sketch**: After each `SetStatusCondition` call, evaluate the
  three sub-conditions and emit `Ready` = True iff all three are
  True (or whatever the §6.6 spec says — verify the Hub-spec
  acceptance rule).

---

## D. CLI commands are missing (planned but not yet implemented)

Everything `examples/hydrate-demo.sh` does end-to-end will eventually
be a single `ach hydrate --environment demo` invocation. The CLI's
config + login + hydrate engine lives in ROADMAP Phase 6+7 and is
not yet implemented in this repo.

- `cmd/ach/cmd/login.go` — not present (`ach login` does not exist).
- `cmd/ach/cmd/hydrate.go` — not present (`ach hydrate` does not
  exist).
- `cmd/ach/cmd/env.go` — env-keys CRUD subcommands not present.
- `cmd/ach/cmd/whoami.go` — not present.

`examples/hydrate-demo.sh` is the explicit stand-in for the missing
CLI commands. Once Phase 6+7 lands, the script can be deleted and
replaced with the CLI invocations it currently scripts.

---

## E. Repo-layout drift from CLAUDE.md

`CLAUDE.md` (line 116) claims `examples/  ← runnable CR samples`
exists. It did not until this commit; `config/samples/` carries the
kubebuilder default stubs with empty `spec.runtime/context: []`
arrays. Now `examples/` exists. CLAUDE.md should be updated to point
new contributors at it.

---

## Suggested order to attack

1. A.1 (decode envelope) — unblocks any subsequent SSO debugging.
2. A.2 (placeholder detection) — gates the first-time vs.
   existing-user branch.
3. A.3 + A.4 (duplicate-add + team-id lookup) — make
   `provisionUser` actually idempotent.
4. C.1 (Ready rollup) — let `examples/hydrate-demo.sh` and every
   downstream `kubectl wait` stop hard-coding the
   `ExecutionResourcesResolved` condition name.
5. D — Phase 6+7 CLI work (largest scope; do last).

Once A.1 + A.2 + A.3 are in, `examples/hydrate-demo.sh` should
produce `examples/hydrate.json` end-to-end with NO manual tricks
(no `team_id="default"` literal trick, no port-forward DNS aliasing
beyond Dex). At that point promote the script into a real
`test/e2e/hydrate_demo_test.go` that asserts the JSON-schema shape
against a checked-in golden file.
