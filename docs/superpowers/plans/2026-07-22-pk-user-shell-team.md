# Personal Keys via a per-user deny-all shell team — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans. Steps use `- [ ]` checkboxes.

**Goal:** Cap every Personal Key (`pk_`) with a per-user deny-all LiteLLM shell
team `ach-user-<email>`, and have the operator attach each entitled Environment's
access group onto that shell, so one PK reaches exactly the union of the user's
entitlements — closing today's teamless-PK fail-open hole.

**Architecture:** Symmetric to #159's env shell. platform-api provisions the
shell + sets `team_id` + key expiry at mint (Option b). The **operator is the
sole writer** of access-group `assigned_team_ids` (Option A): `reconcileAccessGroup`
adds the shells of entitled members, resolved live from `/team/info` (member
`user_id == email`). No lost-update race; new keys are fail-closed until the next
reconcile.

**Tech Stack:** Go (controller-runtime, cobra), LiteLLM admin API, Postgres
projections. Toolchain routes through `ach-devtools` — never prefix `make` with
`./scripts/dev.sh`; `kubectl` only via `./scripts/dev.sh kubectl`.

**Spec:** `docs/superpowers/specs/2026-07-22-personal-keys-user-shell-team-design.md`
**Measured LiteLLM semantics (do NOT re-derive):** `references/litellm-permission-model.md`

## Global Constraints

- Every new `*.go` starts with `// SPDX-License-Identifier: Apache-2.0` (pre-push gate).
- **DRY the deny-all core** — user + env shells share the sentinels, the
  `object_permission` block, and `ShellTeamDrifted` (alias-agnostic). Factor
  shared helpers; do not copy-paste.
- **Never `assigned_key_ids`** (Hazard 1) — keys live only in their team; grants
  reach them via the group→team mirror.
- **`team_id = team_alias`** for user shells (normalised email) AND, going
  forward, env shells. Treat `CreateTeam` 400/409 "already exists" as success
  (id == alias).
- **Operator is the sole writer of `assigned_team_ids`.** platform-api never
  touches access groups.
- **Fail loud, never under-grant:** a `GetTeamInfo` error in the reconcile →
  `resolveFailed` (requeue). Never PUT a union missing shells (that wrongly
  detaches entitled users).
- Every `litellm.Client` implementation must keep compiling — the concrete
  `RESTClient`, `NoopClient`, and EVERY test fake, including any behind
  `//go:build integration` (the Task-2 lesson from #159).
- Existing teamless PKs are NOT migrated (spec decision iii — document only).

---

### Task 1: litellm plumbing — key expiry, team-member decode, duplicate-team error

**Files:**
- Modify: `internal/litellm/types.go` (`KeyGenerateRequest`, `TeamListEntry`)
- Create: `internal/litellm/errors_team.go`
- Test: `internal/litellm/errors_team_test.go`, extend `internal/litellm/keygen_test.go`, `internal/litellm/team_test.go`

**Interfaces:**
- Produces: `KeyGenerateRequest.Duration string`; `TeamListEntry.MembersWithRoles`
  + `(TeamListEntry).MemberEmails() []string`; `IsDuplicateTeamErr(err error) bool`.
- Consumes: `APIError` (existing).

- [ ] **Step 1: Write failing tests**

`internal/litellm/errors_team_test.go`:
```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"errors"
	"net/http"
	"testing"
)

func TestIsDuplicateTeamErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed 400 team/new body", &APIError{StatusCode: http.StatusBadRequest, Path: "/team/new",
			Body: []byte(`{"error":{"message":"Team id = ach-user-a@b.com already exists"}}`)}, true},
		{"typed 409", &APIError{StatusCode: http.StatusConflict, Path: "/team/new",
			Body: []byte("already exists")}, true},
		{"wrapped string", errors.New(`litellm: POST /team/new: 400 Team id = x already exists`), true},
		{"unrelated", &APIError{StatusCode: http.StatusInternalServerError, Path: "/team/new",
			Body: []byte("boom")}, false},
	}
	for _, c := range cases {
		if got := IsDuplicateTeamErr(c.err); got != c.want {
			t.Errorf("%s: IsDuplicateTeamErr = %v, want %v", c.name, got, c.want)
		}
	}
}
```

Add to `internal/litellm/keygen_test.go` (a serialize assertion that `Duration`
lands on the wire):
```go
func TestKeyGenerateRequestSerializesDuration(t *testing.T) {
	b, err := json.Marshal(&KeyGenerateRequest{UserID: "u", TeamID: "ach-user-a@b.com", Duration: "168h"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"duration":"168h"`) {
		t.Fatalf("duration missing from body: %s", b)
	}
	if !strings.Contains(string(b), `"team_id":"ach-user-a@b.com"`) {
		t.Fatalf("team_id missing from body: %s", b)
	}
}
```

Add to `internal/litellm/team_test.go` (member decode + helper):
```go
func TestTeamInfoDecodesMembersAsEmails(t *testing.T) {
	// GetTeamInfo returns members_with_roles; user_id == email (provisionUser).
	raw := `{"team_info":{"team_id":"t1","team_alias":"default",
		"members_with_roles":[{"user_id":"a@b.com","role":"user"},{"user_id":"c@d.com","role":"admin"}]}}`
	srv := newTestServer(t, http.StatusOK, raw) // mirror TestGetTeamInfoDecodesEnvelopeAndFlat's helper
	c := srv.client()
	info, err := c.GetTeamInfo(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	got := info.MemberEmails()
	if len(got) != 2 || got[0] != "a@b.com" || got[1] != "c@d.com" {
		t.Fatalf("MemberEmails = %v, want [a@b.com c@d.com]", got)
	}
}
```
(Use the exact server-construction helper the existing `TestGetTeamInfoDecodesEnvelopeAndFlat`
at `team_test.go:292` uses; match its shape rather than inventing `newTestServer`.)

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-unit-pkg PKG=./internal/litellm/`
  Expected: FAIL — `IsDuplicateTeamErr` undefined, `Duration`/`MembersWithRoles`/`MemberEmails` undefined.

- [ ] **Step 3: Implement**

In `internal/litellm/types.go`, add to `KeyGenerateRequest` (after `TeamID`):
```go
	// Duration sets the LiteLLM key's own expiry (e.g. "168h"). Without it
	// the virtual key is minted with expires:None and outlives the ACH
	// personal_keys row (which carries pkExpiryWindow). Empty = omitted.
	Duration  string            `json:"duration,omitempty"`
```

In `TeamListEntry` (after `ObjectPermission`):
```go
	// MembersWithRoles is the team's membership as returned by GET /team/info
	// (the list endpoints omit it). LiteLLM's user_id equals the email
	// (provisionUser sets user_id=email), so a member's user_id IS their
	// email — used by the operator to derive that member's ach-user-<email>
	// shell without a /user/info round-trip.
	MembersWithRoles []TeamMemberRole `json:"members_with_roles,omitempty"`
}

// TeamMemberRole is one entry of members_with_roles.
type TeamMemberRole struct {
	UserID string `json:"user_id"`
	Role   string `json:"role,omitempty"`
}

// MemberEmails returns the member user_ids (== emails). Empty entries dropped.
func (e TeamListEntry) MemberEmails() []string {
	out := make([]string, 0, len(e.MembersWithRoles))
	for _, m := range e.MembersWithRoles {
		if m.UserID != "" {
			out = append(out, m.UserID)
		}
	}
	return out
}
```
(Note: `TeamListEntry` currently closes at `}` after `ObjectPermission` — move
the `MembersWithRoles` field inside the struct, then declare `TeamMemberRole`
and `MemberEmails` after it.)

Create `internal/litellm/errors_team.go` mirroring `errors_user.go` exactly, with
`/team/new` in place of `/user/new` and no 409-only shortcut (LiteLLM returns
**400** for a duplicate team_id, per the spec):
```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"errors"
	"strings"
)

// IsDuplicateTeamErr reports whether err is LiteLLM's "Team id = <id> already
// exists" response to POST /team/new. Measured: HTTP 400 (NOT 409 like
// /user/new) with the phrase "already exists" in the body. Because ACH sets
// team_id == team_alias, a duplicate means the shell already exists with the
// id the caller already knows, so callers treat this as success.
func IsDuplicateTeamErr(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if strings.Contains(apiErr.Path, "/team/new") &&
			strings.Contains(string(apiErr.Body), "already exists") {
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "/team/new") && strings.Contains(s, "already exists")
}
```

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-unit-pkg PKG=./internal/litellm/`
  Expected: PASS (all litellm tests, including the existing ones, stay green).

- [ ] **Step 5: Commit**
```bash
git add internal/litellm/types.go internal/litellm/errors_team.go \
  internal/litellm/errors_team_test.go internal/litellm/keygen_test.go internal/litellm/team_test.go
git commit -m "feat(litellm): key duration, team-member email decode, IsDuplicateTeamErr"
```

---

### Task 2: litellm user-shell primitives + env-shell team_id + DRY deny-all core

**Files:**
- Create: `internal/litellm/usershell.go`
- Modify: `internal/litellm/shellteam.go` (extract shared builder; set `TeamID` on `NewShellTeamRequest`)
- Test: `internal/litellm/usershell_test.go`, extend `internal/litellm/shellteam_test.go`

**Interfaces:**
- Produces: `UserShellPrefix`, `UserShellAlias(email) string`,
  `NewUserShellRequest(email) *NewTeamRequest`, `IsUserShellManaged(TeamListEntry, email) bool`,
  `IsUserShellShaped(TeamListEntry, email) bool`.
- Consumes: `ShellTeamPermissions`, `ShellTeamDrifted`, `ShellTeamDenyAllModel`,
  `NewTeamRequest.TeamID` (all existing).

- [ ] **Step 1: Write failing tests**

`internal/litellm/usershell_test.go`:
```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUserShellAliasNormalises(t *testing.T) {
	if got := UserShellAlias("  JC@Example.com "); got != "ach-user-jc@example.com" {
		t.Fatalf("UserShellAlias = %q", got)
	}
}

func TestNewUserShellRequestSetsIDAndSentinels(t *testing.T) {
	req := NewUserShellRequest("JC@Example.com")
	if req.TeamID != "ach-user-jc@example.com" || req.TeamAlias != "ach-user-jc@example.com" {
		t.Fatalf("id/alias = %q/%q", req.TeamID, req.TeamAlias)
	}
	b, _ := json.Marshal(req)
	// deny-all: the one impossible model, agents nil-UUID, MCP explicit empty.
	for _, want := range []string{`"__deny_all__"`, `"00000000-0000-0000-0000-000000000000"`, `"mcp_servers":[]`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
}

func TestIsUserShellManagedAndShaped(t *testing.T) {
	managed := TeamListEntry{
		TeamAlias: "ach-user-jc@example.com",
		Models:    []string{ShellTeamDenyAllModel},
		Metadata:  json.RawMessage(`{"ach_managed":"user-shell","ach_user":"jc@example.com"}`),
	}
	if !IsUserShellManaged(managed, "jc@example.com") {
		t.Fatal("managed shell not recognised")
	}
	if IsUserShellManaged(managed, "other@example.com") {
		t.Fatal("cross-user false positive")
	}
	shapedOnly := TeamListEntry{TeamAlias: "ach-user-jc@example.com", Models: []string{ShellTeamDenyAllModel}}
	if IsUserShellManaged(shapedOnly, "jc@example.com") {
		t.Fatal("no metadata must not be 'managed'")
	}
	if !IsUserShellShaped(shapedOnly, "jc@example.com") {
		t.Fatal("shape not recognised")
	}
}
```

Add to `internal/litellm/shellteam_test.go`:
```go
func TestNewShellTeamRequestSetsTeamIDToAlias(t *testing.T) {
	req := NewShellTeamRequest("demo")
	if req.TeamID != "ach-env-demo" || req.TeamAlias != "ach-env-demo" {
		t.Fatalf("env shell id/alias = %q/%q, want ach-env-demo", req.TeamID, req.TeamAlias)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-unit-pkg PKG=./internal/litellm/`
  Expected: FAIL — user-shell symbols undefined; env shell `TeamID` empty.

- [ ] **Step 3: Implement**

In `internal/litellm/shellteam.go`, extract the shared builder and set the env
shell's `TeamID`:
```go
// denyAllTeamRequest builds a POST /team/new body for a deny-all shell, shared
// by the env shell (ach-env-<name>) and the user shell (ach-user-<email>).
// team_id is set == alias so the id is deterministic and creation is idempotent.
func denyAllTeamRequest(alias string, metadata map[string]any) *NewTeamRequest {
	return &NewTeamRequest{
		TeamID:           alias,
		TeamAlias:        alias,
		Models:           []string{ShellTeamDenyAllModel},
		ObjectPermission: ShellTeamPermissions(),
		Metadata:         metadata,
	}
}

// NewShellTeamRequest is the POST /team/new body for an Environment's shell.
func NewShellTeamRequest(env string) *NewTeamRequest {
	return denyAllTeamRequest(ShellTeamAlias(env), ShellTeamMetadata(env))
}
```
(Delete the old inline `NewShellTeamRequest` body; `ShellTeamPermissions`,
`ShellTeamMetadata`, `ShellTeamAlias` unchanged.)

Create `internal/litellm/usershell.go`:
```go
// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"encoding/json"
	"slices"
	"strings"
)

// The per-user deny-all SHELL TEAM caps a Personal Key exactly as the
// per-Environment shell caps an Environment Key (references/litellm-permission-model.md).
// The shell holds NO grants; the operator attaches every entitled Environment's
// access group onto it, and the pk_ inherits their union through the group→team
// mirror. A pk_ has one team_id, so this per-user shell is what lets a single
// key cover the union of a user's entitlements.
const (
	UserShellPrefix = "ach-user-"

	// UserShellManagedMetadataValue marks a team as an ACH-owned user shell,
	// distinct from the env-shell value so the two ownership checks never
	// cross-adopt. ShellTeamManagedMetadataKey / ShellTeamManagedEnvKey are
	// reused; the companion key here carries the email.
	UserShellManagedMetadataValue = "user-shell"
	UserShellManagedUserKey       = "ach_user"
)

// NormalizeEmail lower-cases and trims so ach-user-<email> is stable across
// casing/whitespace variants of the same identity.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// UserShellAlias is the LiteLLM team alias (== team_id) for a user's shell.
func UserShellAlias(email string) string { return UserShellPrefix + NormalizeEmail(email) }

// UserShellMetadata is the ownership bag stamped at create time.
func UserShellMetadata(email string) map[string]any {
	return map[string]any{
		ShellTeamManagedMetadataKey: UserShellManagedMetadataValue,
		UserShellManagedUserKey:     NormalizeEmail(email),
	}
}

// NewUserShellRequest is the POST /team/new body for a user's shell.
func NewUserShellRequest(email string) *NewTeamRequest {
	return denyAllTeamRequest(UserShellAlias(email), UserShellMetadata(email))
}

// IsUserShellManaged reports whether e carries the ACH user-shell ownership
// marker for email. Absent/unparseable metadata is NOT managed (fail safe).
func IsUserShellManaged(e TeamListEntry, email string) bool {
	if len(e.Metadata) == 0 {
		return false
	}
	var meta map[string]string
	if err := json.Unmarshal(e.Metadata, &meta); err != nil {
		return false
	}
	return meta[ShellTeamManagedMetadataKey] == UserShellManagedMetadataValue &&
		meta[UserShellManagedUserKey] == NormalizeEmail(email)
}

// IsUserShellShaped reports whether e already carries a user shell's alias and
// deny-all Models sentinel for email, independent of ownership metadata (the
// migration path for shells created before the metadata existed — mirrors
// IsShellTeamShaped).
func IsUserShellShaped(e TeamListEntry, email string) bool {
	return e.TeamAlias == UserShellAlias(email) && slices.Equal(e.Models, []string{ShellTeamDenyAllModel})
}
```
(Drift detection reuses the existing `ShellTeamDrifted` — it reads only Models +
ObjectPermission and is alias-agnostic. Do not duplicate it.)

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-unit-pkg PKG=./internal/litellm/`
  Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/litellm/usershell.go internal/litellm/shellteam.go \
  internal/litellm/usershell_test.go internal/litellm/shellteam_test.go
git commit -m "feat(litellm): per-user deny-all shell primitives; env shell team_id=alias; DRY builder"
```

---

### Task 3: platform-api — mint PK into the user shell (ensure shell + team_id + expiry)

**Files:**
- Modify: `internal/platformapi/auth/sso.go` (`mintAndPersistPK`, ~lines 435-449)
- Test: `internal/platformapi/auth/sso_test.go` (extend `fakeLiteLLM` + add cases)

**Interfaces:**
- Consumes: `litellm.UserShellAlias`, `litellm.NewUserShellRequest`,
  `litellm.IsDuplicateTeamErr` (T1/T2), `KeyGenerateRequest.Duration` (T1),
  `pkExpiryWindow` (existing, `sso.go:269`).
- Produces: PK minted with `TeamID = ach-user-<email>`, `Duration = pkExpiryWindow`.

- [ ] **Step 1: Extend the fake + write failing tests**

In `sso_test.go`, make `fakeLiteLLM.CreateTeam` recordable + injectable (mirror the
existing `keyGenerateBehaviour` seam). Add to the recorder struct and the fake:
```go
	// in the recorder struct (near lastKeyGenerateReq):
	lastCreateTeamReq *litellm.NewTeamRequest
	// in fakeLiteLLM:
	createTeamBehaviour func(req *litellm.NewTeamRequest) (*litellm.TeamListEntry, error)
```
```go
func (f *fakeLiteLLM) CreateTeam(_ context.Context, req *litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
	f.rec.lastCreateTeamReq = req
	if f.createTeamBehaviour != nil {
		return f.createTeamBehaviour(req)
	}
	return &litellm.TeamListEntry{TeamID: req.TeamID, TeamAlias: req.TeamAlias}, nil
}
```
(Default `newFakeLiteLLM()` leaves `createTeamBehaviour` nil = success.)

New tests:
```go
func TestMintPK_PutsKeyInUserShellWithExpiry(t *testing.T) {
	// ... build callbackDeps with a fresh fakeLiteLLM, drive a successful callback ...
	// Assert the user shell was ensured and the key landed in it with an expiry.
	if fake.rec.lastCreateTeamReq == nil ||
		fake.rec.lastCreateTeamReq.TeamID != "ach-user-"+strings.ToLower(email) {
		t.Fatalf("user shell not ensured: %+v", fake.rec.lastCreateTeamReq)
	}
	kg := fake.rec.lastKeyGenerateReq
	if kg.TeamID != "ach-user-"+strings.ToLower(email) {
		t.Fatalf("key team_id = %q, want user shell", kg.TeamID)
	}
	if kg.Duration == "" {
		t.Fatal("key minted without a duration (expires:None regression)")
	}
}

func TestMintPK_DuplicateShellIsSuccess(t *testing.T) {
	fake.createTeamBehaviour = func(*litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
		return nil, &litellm.APIError{StatusCode: 400, Path: "/team/new",
			Body: []byte("Team id = x already exists")}
	}
	// Drive the callback; expect success (key still minted with the derived team_id).
}

func TestMintPK_ShellEnsureFailure_503_NoMint(t *testing.T) {
	fake.createTeamBehaviour = func(*litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
		return nil, errors.New("litellm down")
	}
	// Drive the callback; expect 503 and that KeyGenerate was NEVER called
	// (fake.rec.lastKeyGenerateReq stays nil) — a key with no live shell is fail-open.
}
```
(Reuse the existing successful-callback scaffolding — see the test around
`sso_test.go:476` / `wantExpires` at `:909` for how a callback is driven and
assertions read `fake.rec`.)

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-unit-pkg PKG=./internal/platformapi/auth/`
  Expected: FAIL — key still minted teamless, no CreateTeam, no Duration.

- [ ] **Step 3: Implement** — in `mintAndPersistPK`, before the `KeyGenerate` call:
```go
	// Cap the pk_ with the caller's per-user deny-all shell team. A key with
	// no live team is fail-open on models AND agents (measured; the exact hole
	// this change closes). The shell MUST exist before KeyGenerate — LiteLLM
	// silently accepts a nonexistent team_id and mints a fail-open key
	// (Hazard 4). team_id == alias, so a 400 "already exists" means the shell
	// is already there with the id we know: success.
	shellID := litellm.UserShellAlias(email)
	if _, tErr := deps.LiteLLM.CreateTeam(ctx, litellm.NewUserShellRequest(email)); tErr != nil {
		if !litellm.IsDuplicateTeamErr(tErr) {
			deps.fail(ctx, w, actor, audit.OutcomeLitellmUnreachable, http.StatusServiceUnavailable,
				"litellm user shell provision failed", reqID, "")
			return
		}
	}
```
Then extend the `KeyGenerateRequest` literal:
```go
	keyResp, err := deps.LiteLLM.KeyGenerate(ctx, &litellm.KeyGenerateRequest{
		UserID:    userID,
		KeyAlias:  keyID,
		MaxBudget: nil,
		TeamID:    shellID,                       // per-user deny-all shell (grants attach via the operator)
		Duration:  durationString(pkExpiryWindow), // LiteLLM key expires with the ACH row
		Metadata: map[string]string{
			"ach_key_id":      keyID,
			"ach_key_type":    "pk",
			"ach_owner_email": email,
		},
	})
```
Add a small helper (same file) — send hours to avoid day-unit ambiguity:
```go
// durationString renders a Go duration as a LiteLLM key duration ("168h").
// LiteLLM accepts an <int><unit> string; hours dodge day-unit differences.
func durationString(d time.Duration) string { return fmt.Sprintf("%dh", int(d.Hours())) }
```
(`ponytail:` verify the deployed LiteLLM honours `"168h"` on `/key/generate` in
the e2e task; adjust the unit only if it rejects it.)

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-unit-pkg PKG=./internal/platformapi/auth/`
  Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/platformapi/auth/sso.go internal/platformapi/auth/sso_test.go
git commit -m "feat(platform-api): mint pk_ into per-user shell team with matching key expiry"
```

---

### Task 4: operator — attach entitled members' user shells in reconcileAccessGroup

**Files:**
- Create: `internal/controller/ach/environment_usershells.go`
- Modify: `internal/controller/ach/environment_controller.go` (`reconcileAccessGroup`, after the env-shell union at ~line 792)
- Test: `internal/controller/ach/environment_usershells_test.go` (extend the `accessGroupFakeImpl`)

**Interfaces:**
- Consumes: `litellm.UserShellAlias`, `TeamListEntry.MemberEmails()` (T1/T2),
  `Client.GetTeamInfo` (existing), the `byAlias` map + resolved authorized-team
  ids already built in `reconcileAccessGroup`.
- Produces: `assigned_team_ids` union that includes the shells of entitled members.

- [ ] **Step 1: Write failing test** — `environment_usershells_test.go`:

The `accessGroupFakeImpl` (`access_group_fake_test.go`) already backs
`GetTeamInfo` from `teamsByID` and flattens `teamsByAlias` into `ListAllTeams`.
Seed: an authorized human team whose `teamsByID` entry carries
`MembersWithRoles`, plus one existing `ach-user-<email>` shell and one entitled
member whose shell does NOT exist.
```go
// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"testing"

	"github.com/ackstorm/ach/internal/litellm"
)

func TestReconcileAccessGroup_AttachesEntitledUserShells(t *testing.T) {
	fake := newAccessGroupFake()
	// Authorized human team "run" (resolved to id t-run) with two members.
	fake.teamsByAlias["run"] = []litellm.TeamListEntry{{TeamID: "t-run", TeamAlias: "run"}}
	fake.teamsByID["t-run"] = litellm.TeamListEntry{
		TeamID: "t-run", TeamAlias: "run",
		MembersWithRoles: []litellm.TeamMemberRole{{UserID: "a@b.com"}, {UserID: "c@d.com"}},
	}
	// a@b.com HAS a shell; c@d.com does NOT (never minted a pk_).
	fake.teamsByAlias["ach-user-a@b.com"] = []litellm.TeamListEntry{{TeamID: "ach-user-a@b.com", TeamAlias: "ach-user-a@b.com"}}

	env := newEnvironment("demo", withAuthorizedTeams("run")) // use the pkg's env test builder
	cond := reconcileEnvAccessGroup(t, fake, env)             // helper that runs reconcileAccessGroup

	ag := fake.lastAccessGroupUpsert() // the created/updated access group
	assertContains(t, ag.AssignedTeamIDs, "ach-user-a@b.com") // entitled + shell exists → attached
	assertContains(t, ag.AssignedTeamIDs, "ach-env-demo")     // env shell still there
	assertNotContains(t, ag.AssignedTeamIDs, "ach-user-c@d.com") // entitled but no shell → skipped
	assertTrue(t, cond) // AccessGroupSynced True
}

func TestReconcileAccessGroup_MemberRemovalDetaches(t *testing.T) {
	// Same seed, then drop a@b.com from t-run's MembersWithRoles and re-reconcile;
	// assert ach-user-a@b.com is no longer in AssignedTeamIDs (detach == revoke).
}

func TestReconcileAccessGroup_TeamInfoError_ResolveFailed(t *testing.T) {
	// Make GetTeamInfo("t-run") return a transport error; assert the condition
	// is AccessGroupSynced=False (resolveFailed) and NO access-group PUT dropped
	// the user shells (never under-grant).
}
```
(Adapt the helper names — `newAccessGroupFake`, `newEnvironment`,
`reconcileEnvAccessGroup`, `lastAccessGroupUpsert`, `assert*` — to the exact ones
in `environment_accessgroup_test.go`; those tests at `:56`/`:260` show the real
scaffolding. Do not invent helpers that already exist under another name.)

- [ ] **Step 2: Run tests, verify they fail**
  Run: `make test-unit-pkg PKG=./internal/controller/ach/`
  Expected: FAIL — user shells never added to the union.

- [ ] **Step 3: Implement** — `environment_usershells.go`:
```go
// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"fmt"

	"github.com/ackstorm/ach/internal/litellm"
)

// entitledUserShellIDs returns the team ids of the ach-user-<email> shells for
// every member of the given authorized teams whose shell already exists.
//
// It is how a pk_ reaches its grants: the operator (sole writer of
// assigned_team_ids) attaches each entitled Environment's access group onto the
// member's per-user shell. Membership is read LIVE from GET /team/info (member
// user_id == email — provisionUser), so removing a user from an authorized team
// drops their shell from the next union and detaches the grant.
//
// Only shells present in byAlias (built from the same ListAllTeams pass) are
// returned: absent means the user never minted a pk_ (lazy creation) — adding a
// phantom team_id is pointless and LiteLLM accepts it silently (Hazard 4).
//
// A GetTeamInfo error is returned to the caller, which fails the reconcile:
// never PUT a union missing shells, which would wrongly detach entitled users.
//
// ponytail: one GetTeamInfo per authorized team per reconcile. authorizedTeams
// is 2-3 today; batch or cache if it grows large.
func (r *EnvironmentReconciler) entitledUserShellIDs(
	ctx context.Context,
	authorizedTeamIDs []string,
	byAlias map[string]string,
) ([]string, error) {
	seen := make(map[string]struct{})
	var out []string
	for _, teamID := range authorizedTeamIDs {
		info, err := r.LiteLLM.GetTeamInfo(ctx, teamID)
		if err != nil {
			return nil, fmt.Errorf("GetTeamInfo(%s): %w", teamID, err)
		}
		if info == nil {
			continue
		}
		for _, email := range info.MemberEmails() {
			shellID, ok := byAlias[litellm.UserShellAlias(email)]
			if !ok || shellID == "" {
				continue // no shell yet (never minted a pk_)
			}
			if _, dup := seen[shellID]; dup {
				continue
			}
			seen[shellID] = struct{}{}
			out = append(out, shellID)
		}
	}
	return out, nil
}
```

In `environment_controller.go` `reconcileAccessGroup`, right after the env-shell
append (`if !slices.Contains(teamIDs, shellID) { ... }`, ~line 792-794) and BEFORE
`GetAccessGroupByName`:
```go
	// Attach the shells of every entitled member so their pk_ inherits this
	// env's grants (the operator is the sole writer of assigned_team_ids). The
	// authorized-team ids resolved above are the input; user shells are added
	// only when they exist (lazy creation). A resolution error fails loud
	// rather than PUTting a union that would detach entitled users.
	userShellIDs, uErr := r.entitledUserShellIDs(ctx, resolvedAuthorizedTeamIDs, byAlias)
	if uErr != nil {
		return resolveFailed(env, "GetTeamInfo", uErr)
	}
	for _, id := range userShellIDs {
		if !slices.Contains(teamIDs, id) {
			teamIDs = append(teamIDs, id)
		}
	}
```
`resolvedAuthorizedTeamIDs` = the `teamIDs` slice as it stood BEFORE the env
shell was appended (i.e. only `env.Spec.AuthorizedTeams` resolved). Capture it
into a separate variable at line ~758 (`resolvedAuthorizedTeamIDs := slices.Clone(teamIDs)`)
so the member scan runs over the human teams only, not the env shell (which has
no members).

- [ ] **Step 4: Run tests, verify they pass**
  Run: `make test-unit-pkg PKG=./internal/controller/ach/`
  Expected: PASS.

- [ ] **Step 5: Run the envtest controller suite (race) to catch reconcile regressions**
  Run: `make test-envtest-pkg PKG=./internal/controller/ach/`
  Expected: PASS (or at most the pre-existing `TestEnvSkillContentPresent_NotSynced`
  flake noted in the #159 ledger — re-run once on a clean tree to confirm).

- [ ] **Step 6: Commit**
```bash
git add internal/controller/ach/environment_usershells.go \
  internal/controller/ach/environment_controller.go \
  internal/controller/ach/environment_usershells_test.go
git commit -m "feat(operator): attach entitled members' user shells to the access group union"
```

---

### Task 5: Documentation (same-commit contract)

**Files:**
- Modify: `references/litellm-permission-model.md` (§10 pk_ residual → now shelled;
  add the user-shell section + the iii residual)
- Modify: `CLAUDE.md` (shell-team bullet + platform-api row)
- Modify: `references/understanding.md` (pk_ contract)
- Modify: `references/troubleshooting.md` (deny-all first-login window; fail-open pk_ symptom)

- [ ] **Step 1: Update `references/litellm-permission-model.md`** — replace the
  §10 "pk_ stays fail-open on models (residual)" claim with: pk_ is now capped by
  `ach-user-<email>`, symmetric to `ach-env-<name>`; the operator attaches
  entitled Environments' access groups onto the shell; one pk_ covers the union.
  Record the LOCKED residual: PKs minted before this change keep `team_id=NULL` +
  `expires:None` and stay fail-open until revoked by hand (decision iii).

- [ ] **Step 2: Update `CLAUDE.md`** — in the deny-all shell bullet, add the
  per-user shell alongside the per-env one; note the operator is the sole writer
  of `assigned_team_ids` and adds entitled members' shells; note platform-api
  provisions the user shell at mint (Option b). Keep it to the existing bullet's
  density — this is a navigation hub, not a textbook.

- [ ] **Step 3: Update `references/understanding.md`** — the pk_ lifecycle now
  ends in `ach-user-<email>`, not teamless; grants via operator attachment.

- [ ] **Step 4: Update `references/troubleshooting.md`** — add: "fresh pk_ reaches
  nothing for ≤5 min after first login" = expected (fail-closed until the next
  operator reconcile attaches the shell; one-time); "old pk_ reaches everything" =
  the pre-change residual (iii), revoke it by hand.

- [ ] **Step 5: Commit**
```bash
git add references/litellm-permission-model.md CLAUDE.md \
  references/understanding.md references/troubleshooting.md
git commit -m "docs: pk_ capped by per-user shell team; operator attaches entitled shells"
```

---

### Task 6: E2E — assert the user shell + enforcement against real LiteLLM

**Files:**
- Modify: `test/e2e/phase4_accessgroup_test.go` (or a sibling e2e test in that phase)

**Interfaces:** consumes the real LiteLLM in the kind cluster (the same one #159's
sentinel/mirror assertions use).

- [ ] **Step 1: Add an e2e assertion** mirroring the spec's verification, for a PK
  minted for a user entitled to an env:
  - the user shell `ach-user-<email>` exists with the sentinels (models
    `["__deny_all__"]`, `object_permission.agents` nil-UUID, empty MCP);
  - it appears in the entitled env's access-group `assigned_team_ids`;
  - a model in the env's `runtime.models` → 200; a model no entitlement grants →
    `team_model_access_denied`;
  - `GET /mcp-rest/tools/list` = the union of the entitled env(s)' `mcpServers`
    catalogs (compare against per-server catalogs, NOT substrings — servers share
    generic tool names);
  - `GET /v1/agents` = the entitled `a2aAgents`; an unentitled agent's
    `message/send` → denied.
  - This is also where `durationString("168h")` is validated end-to-end: assert
    the minted key carries a non-null `expires`.

- [ ] **Step 2: Run the focused e2e** (cluster must be up — `make e2e-full` once,
  then focus):
  Run: `make e2e-focus RUN="TestPhase4Promotion/UserShell"` (name to match the added subtest)
  Expected: PASS.

- [ ] **Step 3: Commit**
```bash
git add test/e2e/phase4_accessgroup_test.go
git commit -m "test(e2e): assert per-user shell membership + pk_ enforcement in real LiteLLM"
```

---

### Task 7: Full gates + live validation

- [ ] **Step 1:** `make test-unit` → PASS
- [ ] **Step 2:** `make qa-lint` → PASS (watch for unused `resolvedAuthorizedTeamIDs`
      if the wiring drifted, and gocyclo on `reconcileAccessGroup` — if it trips,
      the user-shell loop is already extracted to `environment_usershells.go`, so
      lift the capture too).
- [ ] **Step 3:** `make test-envtest` (race) → PASS
- [ ] **Step 4:** `make e2e-full` → E2E RESULT: PASS (cluster kept). Then live-verify:
```bash
./scripts/dev.sh kubectl -n ach exec deploy/ach-... -- true  # sanity
# Mint a pk_ via the host CLI against the kept cluster (see memory: local-cli-test-against-kind),
# then inspect LiteLLM: the user shell exists with sentinels + is in the env group's
# assigned_team_ids; the key reaches entitled models/mcp/agents and nothing else.
```
- [ ] **Step 5:** `make qa-security` → PASS (if a fresh stdlib advisory trips the
      gate, fix at the root — bump — not by adding an ack row, per the #159
      precedent).
- [ ] **Step 6:** `make pre-push` → 18 gates, 0 failures.
- [ ] **Step 7:** Push the branch, open the PR (do NOT merge/release without JC's go).

---

## Self-Review

**Spec coverage:** per-user shell (T2) · operator sole-writer attachment (T4) ·
platform-api provisions at mint, Option b (T3) · team_id=alias both shells +
idempotent create (T1 dup-err, T2 builder, T3/T4 use) · expiry fix (T1 field, T3
set, T6 assert) · member resolution via user_id==email (T1 decode, T4 use) ·
Hazards 1-4 (constraints + T3/T4) · human teams untouched (T4 only adds) ·
env-shell migration by-hand + recreate under alias (T2) · residual iii
(T5 docs) · verification (T6). All covered.

**Placeholder scan:** test bodies for T3/T4's secondary cases (removal, error
paths) are sketched with intent + assertions and point at the exact existing
scaffolding to copy; the primary path in each task has full code. No TBD/TODO in
implementation steps.

**Type consistency:** `Duration`, `MembersWithRoles`/`TeamMemberRole`/
`MemberEmails`, `IsDuplicateTeamErr` (T1) → consumed with those exact names in
T3/T4. `UserShellAlias`/`NewUserShellRequest`/`IsUserShellManaged`/`IsUserShellShaped`
(T2) → consumed in T3/T4. `denyAllTeamRequest` shared by both `New*ShellRequest`.
`entitledUserShellIDs(ctx, authorizedTeamIDs, byAlias)` (T4) matches its call site.
