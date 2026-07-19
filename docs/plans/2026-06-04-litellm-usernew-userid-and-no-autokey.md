# LiteLLM UserNew: deterministic user_id=email + no leaked default key — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Stop ACH from (1) leaking an untracked LiteLLM virtual key on every first-time user provision and (2) letting LiteLLM auto-assign a random UUID `user_id` — instead provision new LiteLLM users deterministically with `user_id = email` and zero auto-created keys.

**Architecture:** Two changes to the `litellm.UserNewRequest` call sites (`platformapi/auth` SSO path + `platformapi/envkeys` path), backed by one new optional request field. `POST /user/new` defaults `auto_create_key=true` and (without a supplied `user_id`) assigns a UUID. We send `auto_create_key=false` + `user_id=<email>` so the ONLY key for the user is the tracked `pk_`/`ek_` minted later via `/key/generate`, and the user_id is stable and human-readable. Because a deterministic `user_id=email` makes a repeat `UserNew` collide when the existence probe yields a false-negative (LiteLLM v1.83 broken email lookup, upstream issue #36), both call sites gain duplicate-create tolerance.

**Tech Stack:** Go, `internal/litellm` REST client + `litellm.Client` interface, `net/http`, stdlib `testing`. Tests run in the devtools container via `make test-unit-pkg PKG=...` (host has no Go — see CLAUDE.md "Toolchain").

**Scope boundaries (explicit):**
- IN: `UserNewRequest.AutoCreateKey` field; `user_id=email` + `auto_create_key=false` at both `UserNew` call sites; duplicate-create recovery at both sites; unit tests.
- OUT (deferred, do NOT touch in this plan): removing the `/user/list` fallback hack in `internal/litellm/users.go:87-146`. It is still required for **legacy UUID-keyed users** created before this change; switching the existence probe to `?user_id=<email>` would make every legacy user look "not found" and risk creating a duplicate email-id user. Removing it is a separate migration-aware task.
- OUT: converting the existing prod UUID user (`7bfb43cc-…`) to email — `user_id` is the LiteLLM PK and cannot be renamed; that is a one-time delete+relogin AFTER this ships (see [[dont-hand-delete-ach-litellm-users]]) and is an ops action, not code.

**Behavioral contract after this plan:**
- New (first-time) user → LiteLLM user created with `user_id = email`, NO auto key. Exactly ONE LiteLLM key exists afterward: the tracked `pk_` (SSO) or `ek_` (env-keys), carrying `metadata.ach_key_id`.
- Existing user (found by probe) → unchanged: reuse the returned `user_id` (legacy UUID stays; we never rename).
- Existing user whose probe false-negatives → `UserNew` collides → recover by treating `user_id = email` (the value we requested) and continue. No second LiteLLM user, no 500.

---

### Task 1: Add `AutoCreateKey` to `UserNewRequest` + a bool-pointer helper

**Files:**
- Modify: `internal/litellm/types.go:162-166` (the `UserNewRequest` struct)
- Create: `internal/litellm/ptr.go`
- Test: `internal/litellm/usernew_serialize_test.go` (new)

**Step 1: Write the failing test**

Create `internal/litellm/usernew_serialize_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"strings"
	"testing"
)

// auto_create_key must serialize to an explicit `false` (not be dropped)
// when the caller sets it via BoolPtr(false). With omitempty on a nil
// *bool, an UNSET field is omitted entirely (LiteLLM keeps its default
// auto_create_key=true) — that is the legacy behaviour we are moving away
// from at the call sites.
func TestUserNewRequest_AutoCreateKeyFalseSerializes(t *testing.T) {
	req := &UserNewRequest{
		UserEmail:     "jc@example.com",
		UserID:        "jc@example.com",
		Teams:         []string{"default"},
		AutoCreateKey: BoolPtr(false),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, `"auto_create_key":false`) {
		t.Errorf("expected auto_create_key:false in payload, got %s", got)
	}
	if !strings.Contains(got, `"user_id":"jc@example.com"`) {
		t.Errorf("expected user_id=email in payload, got %s", got)
	}
}

// When AutoCreateKey is nil, omitempty drops it (back-compat: the field is
// absent so LiteLLM applies its server-side default).
func TestUserNewRequest_AutoCreateKeyNilOmitted(t *testing.T) {
	req := &UserNewRequest{UserEmail: "jc@example.com"}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "auto_create_key") {
		t.Errorf("nil AutoCreateKey must be omitted, got %s", string(raw))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/litellm/ -run TestUserNewRequest_AutoCreateKey -v`
Expected: FAIL — `undefined: BoolPtr` and `unknown field AutoCreateKey`.

**Step 3: Write minimal implementation**

Create `internal/litellm/ptr.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

// BoolPtr returns a pointer to b. Used for optional tri-state request
// fields (nil = omit/keep server default, &false / &true = explicit).
func BoolPtr(b bool) *bool { return &b }
```

Modify `internal/litellm/types.go` — extend `UserNewRequest` (keep the existing doc comment; add the field + a one-line note):

```go
type UserNewRequest struct {
	UserEmail string   `json:"user_email"`
	UserID    string   `json:"user_id,omitempty"`
	Teams     []string `json:"teams,omitempty"`
	// AutoCreateKey controls LiteLLM's /user/new default-key minting.
	// nil → field omitted (LiteLLM default auto_create_key=true). ACH
	// callers pass BoolPtr(false) so the user is created WITHOUT an
	// untracked default key — the only key is the pk_/ek_ minted via
	// /key/generate. *bool keeps the tri-state (omit vs explicit false).
	AutoCreateKey *bool `json:"auto_create_key,omitempty"`
}
```

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/litellm/ -run TestUserNewRequest_AutoCreateKey -v`
Expected: PASS (both subtests).

**Step 5: Commit**

```bash
git add internal/litellm/types.go internal/litellm/ptr.go internal/litellm/usernew_serialize_test.go
git commit -m "feat(litellm): add UserNewRequest.AutoCreateKey tri-state field"
```

---

### Task 2: SSO provisionUser — set user_id=email + auto_create_key=false

**Files:**
- Modify: `internal/platformapi/auth/sso.go:576-579` (the `UserNew` call in the 404 branch)
- Test: `internal/platformapi/auth/sso_test.go` (extend the `callRecord` + add assertions)

**Step 1: Write the failing test**

In `internal/platformapi/auth/sso_test.go`, extend `callRecord` (around line 632) to capture the full last UserNew request:

```go
	lastUserNewEmail      string
	lastUserNewReq        *litellm.UserNewRequest // ADD THIS
```

In the `UserNew` fake method (around line 720), record it:

```go
func (f *fakeLiteLLM) UserNew(_ context.Context, req *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	f.rec.userNewCalls++
	f.rec.lastUserNewEmail = req.UserEmail
	f.rec.lastUserNewReq = req // ADD THIS
	return f.userNewBehaviour(req)
}
```

Add a new test (place near `TestCallbackHandler_FirstTimeSSO`, ~line 853):

```go
// First-time SSO must provision the LiteLLM user with a deterministic
// user_id=email and auto_create_key=false so no untracked default key is
// leaked (regression guard for the 2026-06-04 prod finding).
func TestCallbackHandler_FirstTimeSSO_UserIDEmailAndNoAutoKey(t *testing.T) {
	flm := newFakeLiteLLM() // default: UserInfoByEmail→ErrNotFound, UserNew ok
	h := newTestHarness(t, flm)
	h.runHappyCallback(t) // drives a full first-time callback

	req := flm.rec.lastUserNewReq
	if req == nil {
		t.Fatal("UserNew was not called")
	}
	if req.UserID != testEmail {
		t.Errorf("UserNew user_id: got %q, want %q (deterministic email id)", req.UserID, testEmail)
	}
	if req.AutoCreateKey == nil || *req.AutoCreateKey != false {
		t.Errorf("UserNew auto_create_key: got %v, want explicit false", req.AutoCreateKey)
	}
}
```

> NOTE for implementer: reuse this file's EXISTING harness/helpers. Look at how
> `TestCallbackHandler_FirstTimeSSO` (line ~853) builds `Deps`, drives the
> callback, and what constant holds the test email. Replace `newTestHarness` /
> `runHappyCallback` / `testEmail` with the real helper + constant names already
> in `sso_test.go` (do NOT invent new harness scaffolding — match the file).

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/platformapi/auth/ -run TestCallbackHandler_FirstTimeSSO_UserIDEmailAndNoAutoKey -v`
Expected: FAIL — `UserNew user_id: got "" , want "<email>"` and `auto_create_key: got <nil>`.

**Step 3: Write minimal implementation**

In `internal/platformapi/auth/sso.go`, the 404 branch (currently lines 576-579):

```go
			created, createErr := deps.LiteLLM.UserNew(ctx, &litellm.UserNewRequest{
				UserEmail:     email,
				UserID:        email, // deterministic LiteLLM user_id = email (not a random UUID)
				Teams:         []string{"default"},
				AutoCreateKey: litellm.BoolPtr(false), // no leaked default key; pk_ is minted via /key/generate
			})
```

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/platformapi/auth/ -run TestCallbackHandler_FirstTimeSSO -v`
Expected: PASS (new test + existing first-time tests still green).

**Step 5: Commit**

```bash
git add internal/platformapi/auth/sso.go internal/platformapi/auth/sso_test.go
git commit -m "fix(platform-api): provision SSO LiteLLM user with user_id=email, no auto key"
```

---

### Task 3: env-keys provisionUser — same fix

**Files:**
- Modify: `internal/platformapi/envkeys/handler.go:347-350` (the `UserNew` call)
- Test: the envkeys handler test file (find it — likely `internal/platformapi/envkeys/handler_test.go`)

**Step 0: Locate the test + fake**

Run: `ls internal/platformapi/envkeys/*_test.go && grep -n "func.*UserNew" internal/platformapi/envkeys/*_test.go`
This tells you the test file and how its fake LiteLLM records `UserNew`. Mirror the Task-2 recorder pattern (capture `lastUserNewReq`).

**Step 1: Write the failing test**

Add a test that drives the env-key create path for a FIRST-TIME user (fake `UserInfoByEmail` → not-found) and asserts the captured `UserNew` request has `UserID == <ownerEmail>` and `AutoCreateKey != nil && *AutoCreateKey == false`. Match the existing envkeys test harness — reuse its `createReq`/`Deps` builder and owner-email constant; do not scaffold a new one.

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/platformapi/envkeys/ -run <NewTestName> -v`
Expected: FAIL — user_id empty, auto_create_key nil.

**Step 3: Write minimal implementation**

In `internal/platformapi/envkeys/handler.go` (lines 347-350):

```go
			newInfo, err := cr.deps.LiteLLM.UserNew(cr.ctx, &litellm.UserNewRequest{
				UserEmail:     cr.keyCtx.OwnerEmail,
				UserID:        cr.keyCtx.OwnerEmail, // deterministic user_id = email
				Teams:         []string{defaultTeam},
				AutoCreateKey: litellm.BoolPtr(false), // no leaked default key
			})
```

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/platformapi/envkeys/ -v`
Expected: PASS (new test + existing envkeys tests green).

**Step 5: Commit**

```bash
git add internal/platformapi/envkeys/handler.go internal/platformapi/envkeys/<testfile>.go
git commit -m "fix(platform-api): provision env-key LiteLLM user with user_id=email, no auto key"
```

---

### Task 4: Discover LiteLLM's duplicate-user error signature (prod, read-only)

> This task captures the exact error LiteLLM returns when `POST /user/new` is
> called with an already-existing `user_id`, so Task 5's matcher is precise.
> Read-only against prod LiteLLM; creates NO ACH state.

**Step 1: Probe a duplicate create against prod LiteLLM**

Run (from repo root, host kubectl works for the prod EKS context):

```bash
kubectl -n litellm exec deploy/litellm -- python -c '
import os,urllib.request,json,urllib.error
k=os.environ["LITELLM_MASTER_KEY"]
# Re-create the CURRENT real user_id -> guaranteed duplicate, creates nothing new.
body=json.dumps({"user_email":"juancarlos.moreno@ackstorm.com","user_id":"juancarlos.moreno@ackstorm.com","auto_create_key":False}).encode()
r=urllib.request.Request("http://localhost:4000/user/new",data=body,
  headers={"Authorization":"Bearer "+k,"Content-Type":"application/json"})
try:
  print("OK",json.load(urllib.request.urlopen(r)).get("user_id"))
except urllib.error.HTTPError as e:
  print("STATUS",e.code); print(e.read().decode()[:600])
'
```

**Step 2: Record the result**

Note the HTTP status + body substring (e.g. `409`/`400` + `"already exists"`).
Write it into the doc comment of `isDuplicateUserErr` in Task 5 so the matcher
reflects reality, not a guess. (If LiteLLM unexpectedly returns 200/upsert
semantics, Task 5's recovery is still correct but the matcher branch is
effectively dead — note that in the comment.)

No commit (investigation only).

---

### Task 5: Duplicate-create tolerance at both call sites

**Why:** with deterministic `user_id=email`, a false-negative existence probe
(LiteLLM #36) makes the 404 branch call `UserNew` for a user that already
exists → collision. Today that error is fatal (SSO: `provisionKindLitellm` →
500; env-keys: `emitLitellmError` → 500). Recovery: since we requested
`user_id=email`, on a duplicate we KNOW the id is the email — use it and
continue.

**Files:**
- Modify: `internal/platformapi/auth/sso.go` (404 branch, after `UserNew`)
- Modify: `internal/platformapi/envkeys/handler.go` (first-time branch, after `UserNew`)
- Test: `internal/platformapi/auth/sso_test.go`, envkeys test file

**Step 1: Write the failing tests**

SSO test (`sso_test.go`): configure the fake so `userInfoBehaviour` returns
`ErrNotFound` (forces the UserNew branch) and `userNewBehaviour` returns a
duplicate-user error (use the status/substring captured in Task 4, e.g.
`fmt.Errorf("litellm: 409 on POST /user/new (code=...)")`). Assert the callback
SUCCEEDS (HTTP 200, a `pk_` minted) and that `KeyGenerate` was called with
`UserID == testEmail`:

```go
func TestCallbackHandler_FirstTimeSSO_DuplicateUserRecovers(t *testing.T) {
	flm := newFakeLiteLLM()
	flm.userInfoBehaviour = func(string) (*litellm.UserInfo, error) { return nil, litellm.ErrNotFound }
	flm.userNewBehaviour = func(*litellm.UserNewRequest) (*litellm.UserInfo, error) {
		return nil, fmt.Errorf("litellm: 409 on POST /user/new (code=duplicate)") // from Task 4
	}
	h := newTestHarness(t, flm)
	resp := h.runHappyCallback(t) // expect success, NOT 500
	// assert 200 + pk_ minted (match the existing first-time test's assertions)
	if flm.rec.lastKeyGenerateUser != testEmail {
		t.Errorf("KeyGenerate user_id: got %q, want %q", flm.rec.lastKeyGenerateUser, testEmail)
	}
}
```

env-keys test: analogous — first-time owner, `UserNew` returns duplicate error,
assert the create SUCCEEDS and `KeyGenerate.UserID == ownerEmail`.

**Step 2: Run tests to verify they fail**

Run: `./scripts/dev.sh go test ./internal/platformapi/auth/ -run DuplicateUserRecovers -v`
Expected: FAIL — handler returns 500 (error treated as fatal).

**Step 3: Write minimal implementation**

Add a shared matcher in `internal/litellm` (so both packages use one source of
truth). Create `internal/litellm/errors_user.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import "strings"

// IsDuplicateUserErr reports whether err is LiteLLM's "user_id already
// exists" response to POST /user/new. Signature captured against prod
// LiteLLM v1.83 on 2026-06-04: <STATUS> + <SUBSTRING> (fill from Task 4).
// Matching on path + status keeps it robust to body stripping by the 4xx
// wrapper (§9.1 no-body-in-error).
func IsDuplicateUserErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "/user/new") &&
		(strings.Contains(s, "409") || strings.Contains(s, "already exists")) // adjust per Task 4
}
```

SSO `sso.go` 404 branch — wrap the `UserNew` error:

```go
			created, createErr := deps.LiteLLM.UserNew(ctx, &litellm.UserNewRequest{
				UserEmail:     email,
				UserID:        email,
				Teams:         []string{"default"},
				AutoCreateKey: litellm.BoolPtr(false),
			})
			if createErr != nil {
				if !litellm.IsDuplicateUserErr(createErr) {
					return "", &provisionErr{kind: provisionKindLitellm, err: createErr}
				}
				// Probe false-negative (LiteLLM #36): the user already exists
				// with user_id=email (the value we requested). Recover by using
				// email as the id and continuing to TeamMemberAdd.
				deps.Logger.Info("sso.callback: UserNew duplicate — recovering with user_id=email",
					"email", email)
				created = &litellm.UserInfo{UserID: email, UserEmail: email}
			}
			// ...existing TeamMemberAdd(defaultTeamID, created.UserID, "user") block unchanged...
			return created.UserID, nil
```

env-keys `handler.go` first-time branch — analogous:

```go
			newInfo, err := cr.deps.LiteLLM.UserNew(cr.ctx, &litellm.UserNewRequest{
				UserEmail:     cr.keyCtx.OwnerEmail,
				UserID:        cr.keyCtx.OwnerEmail,
				Teams:         []string{defaultTeam},
				AutoCreateKey: litellm.BoolPtr(false),
			})
			if err != nil {
				if !litellm.IsDuplicateUserErr(err) {
					cr.emitLitellmError(err, "envkeys.create: UserNew failed")
					return "", true
				}
				cr.deps.Logger.Info("envkeys.create: UserNew duplicate — recovering with user_id=email",
					"email", cr.keyCtx.OwnerEmail)
				newInfo = &litellm.UserInfo{UserID: cr.keyCtx.OwnerEmail, UserEmail: cr.keyCtx.OwnerEmail}
			}
			userInfo = newInfo
			// ...existing TeamMemberAdd block unchanged...
```

**Step 4: Run tests to verify they pass**

Run: `./scripts/dev.sh go test ./internal/platformapi/auth/ ./internal/platformapi/envkeys/ ./internal/litellm/ -v`
Expected: PASS (new recovery tests + all existing tests green).

**Step 5: Commit**

```bash
git add internal/litellm/errors_user.go internal/platformapi/auth/sso.go \
  internal/platformapi/auth/sso_test.go internal/platformapi/envkeys/handler.go \
  internal/platformapi/envkeys/<testfile>.go
git commit -m "fix(platform-api): tolerate UserNew duplicate-create, recover with user_id=email"
```

---

### Task 6: Full gates + docs

**Step 1: Lint changed packages**

Run: `make qa-lint-changed`
Expected: clean (no new findings).

**Step 2: Unit sweep for touched packages**

Run: `make test-unit-pkg PKG=internal/litellm && make test-unit-pkg PKG=internal/platformapi/auth && make test-unit-pkg PKG=internal/platformapi/envkeys`
Expected: all PASS.

**Step 3: Update the upstream-sync / behavior note if applicable**

These files are original ACH code (not grafted) — no `references/upstream-sync.md`
row needed. Confirm no doc in `references/` or `docs/` currently asserts
"LiteLLM user_id is a UUID" or "first login creates one key". Run:
`grep -rn "auto_create_key\|user_id.*UUID\|random UUID" references/ docs/ CLAUDE.md`
If a stale claim exists, fix it IN THIS BRANCH (CLAUDE.md "Documentation hygiene").

**Step 4: envtest if controller-adjacent (it is not, but confirm)**

This change touches `internal/platformapi/*` + `internal/litellm` only — no
controller/CRD/Helm surface. Per CLAUDE.md, envtest/e2e are NOT required gates
for platform-api-only changes. Skip e2e. (If `make test-envtest` is trivially
green it's fine to run, but not mandated.)

**Step 5: Commit any doc fixes**

```bash
git add -A && git commit -m "docs: align LiteLLM provisioning notes with user_id=email + no auto key"
```

(Skip if Step 3 found nothing to change.)

---

## Post-merge ops (NOT part of this plan — do after the image ships)

1. Deploy the new image to prod (`ach platform-api`).
2. One clean relogin to convert JC's user to email id: delete the prod UUID
   user `7bfb43cc-…` + its keys in LiteLLM, then `ach login`. Fixed code now
   provisions `user_id = juancarlos.moreno@ackstorm.com`. This re-triggers the
   `pk_` cascade once — expected, see [[dont-hand-delete-ach-litellm-users]].
3. Optional housekeeping: revoke the historical orphan default keys in LiteLLM
   (empty `metadata`, no matching ACH `personal_keys` row), e.g. `779193d488f2`.
   They are inert (no backing ACH row) but clutter `/key/list`.
