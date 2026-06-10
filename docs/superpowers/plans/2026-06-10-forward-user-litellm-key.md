# Forward Per-User LiteLLM Key Material Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the forwarder authenticate to LiteLLM as the **caller's own** LiteLLM virtual key (1:1 user identity) by persisting the `sk-…` key material at mint time and forwarding it as `x-litellm-api-key`, instead of sending the shared master key.

**Architecture:** At pk_/ek_ mint, persist the LiteLLM-returned virtual key (`keyResp.Key`) into a new nullable plaintext column `litellm_key_material`. The resolve path carries it through `KeyInfo` → `KeyContext` to the forwarder Director, which writes it as `x-litellm-api-key` (bare on `/v1`/`/gemini`/`/a2a`, `Bearer `-prefixed on `/mcp`). The master key and the `x-litellm-key-id` delegation header are removed from the forward path. The JWT/BIP (BFI) path is left untouched.

**Tech Stack:** Go, pgx/pgxpool, golang-migrate SQL migrations, chi, slog, controller-runtime-free `internal/keystore` + `internal/forwarder`.

---

## ⚠️ Security posture (explicit, user-ratified)

This **reverses the FIX01 §A.6 decision** that ACH must never persist the LiteLLM virtual-key plaintext (`internal/litellm/client.go` doc + `internal/platformapi/auth/sso.go:411-418`). The user has explicitly accepted **plaintext** storage for a **testing phase** ("me da igual si es en claro, de momento"). Decisions ratified:

- **No master fallback.** If `litellm_key_material` is NULL (pre-existing keys), `x-litellm-api-key` is forwarded **empty** (`Bearer ` on `/mcp`). Old keys simply break — acceptable.
- **Always active** — no feature flag.
- **Drop `x-litellm-key-id`** — we authenticate as the user's own key, so master-delegation is meaningless.
- The **JWT/BIP (BFI)** path (`Authorization: Bearer <jwt>`) is unchanged.

Every new column/field/code path that stores or moves the plaintext MUST carry a comment tagging it `TESTING-PHASE (reverts FIX01 §A.6)` so the reversal is greppable when it is time to undo it.

**MANDATORY reading before Task 9-10:** `docs/developer-guide/jwt-forwarder.md` (trust-path contract).

---

## File Structure

| File | Responsibility | Change |
|------|----------------|--------|
| `db/migrations/000011_litellm_key_material.{up,down}.sql` | schema | **create** — add nullable `litellm_key_material` to both key tables |
| `internal/db/types_keys.go` | row/insert structs | add `LiteLLMKeyMaterial *string` to `PkKeyInfo`, `EkKeyInfo`, `PkInsertRow`, `EkInsertRow` |
| `internal/db/personal_keys.go` | pk_ insert | INSERT writes `litellm_key_material` |
| `internal/db/check_extend.go` | pk_ resolve | `PkCheckAndExtend` RETURNING + Scan the column |
| `internal/db/environment_keys.go` | ek_ insert | INSERT writes `litellm_key_material` |
| `internal/db/ek_resolve.go` | ek_ resolve | `EkResolve` RETURNING + Scan the column |
| `internal/keystore/keystore.go` | resolved key shape | add `LiteLLMKeyMaterial *string` to `KeyInfo` |
| `internal/keystore/dbresolver.go` | row→KeyInfo | populate `LiteLLMKeyMaterial` in pk/ek lookups |
| `internal/platformapi/middleware/keyctx.go` | request key ctx | add field + populate in `WithKeyContext` |
| `internal/platformapi/auth/sso.go` | pk_ mint | persist `keyResp.Key` |
| `internal/platformapi/envkeys/handler.go` | ek_ mint | persist `keyResp.Key` |
| `internal/forwarder/headers/strip.go` | header write contract | new signature: write `x-litellm-api-key=<material>`, drop `x-litellm-key-id` |
| `internal/forwarder/proxy/proxy.go` | Director | source material from `KeyContext`, `Bearer ` on `/mcp` |
| `docs/developer-guide/jwt-forwarder.md` + `references/troubleshooting.md` | docs | document the new auth model |

---

## Task 1: Migration — add `litellm_key_material` column

**Files:**
- Create: `db/migrations/000011_litellm_key_material.up.sql`
- Create: `db/migrations/000011_litellm_key_material.down.sql`

- [ ] **Step 1: Write the up migration**

`db/migrations/000011_litellm_key_material.up.sql`:
```sql
-- TESTING-PHASE (reverts FIX01 §A.6): persist the per-user LiteLLM virtual-key
-- plaintext so the forwarder can authenticate to LiteLLM as the caller's own
-- key (1:1 identity) instead of the shared master key. Plaintext, nullable;
-- populated at mint (/key/generate response `key`). Pre-existing rows stay NULL.
ALTER TABLE personal_keys    ADD COLUMN IF NOT EXISTS litellm_key_material text;
ALTER TABLE environment_keys ADD COLUMN IF NOT EXISTS litellm_key_material text;
```

- [ ] **Step 2: Write the down migration**

`db/migrations/000011_litellm_key_material.down.sql`:
```sql
ALTER TABLE personal_keys    DROP COLUMN IF EXISTS litellm_key_material;
ALTER TABLE environment_keys DROP COLUMN IF EXISTS litellm_key_material;
```

- [ ] **Step 3: Verify the migration applies cleanly**

Run: `make test-envtest-pkg PKG=./internal/db/... ` (or, if a dedicated migrate check exists, `./scripts/dev.sh go test ./internal/db/...`). 
Expected: migrations apply with no error; if a test asserts the highest migration version, update it to `11`.

- [ ] **Step 4: Commit**

```bash
git add db/migrations/000011_litellm_key_material.up.sql db/migrations/000011_litellm_key_material.down.sql
git commit -m "feat(db): add litellm_key_material column (testing-phase, reverts FIX01 A.6)"
```

---

## Task 2: DB structs — carry the material field

**Files:**
- Modify: `internal/db/types_keys.go:34-94`

- [ ] **Step 1: Add the field to `PkKeyInfo`**

In `internal/db/types_keys.go`, inside `type PkKeyInfo struct`, after the `LiteLLMToken` line:
```go
	LiteLLMToken  *string    // NULL until Phase 3 /key/generate response
	// TESTING-PHASE (reverts FIX01 §A.6): the LiteLLM virtual-key plaintext
	// (sk-…). NULL for rows minted before migration 000011. Populated only by
	// the resolve queries (PkCheckAndExtend / EkResolve); admin reads leave it nil.
	LiteLLMKeyMaterial *string
```

- [ ] **Step 2: Add the field to `EkKeyInfo`**

After its `LiteLLMToken` line:
```go
	LiteLLMToken   *string    // NULL until Phase 3 /key/generate response
	// TESTING-PHASE (reverts FIX01 §A.6): LiteLLM virtual-key plaintext (sk-…).
	LiteLLMKeyMaterial *string
```

- [ ] **Step 3: Add the field to `PkInsertRow` and `EkInsertRow`**

In `PkInsertRow` after `LiteLLMToken *string`:
```go
	LiteLLMToken   *string
	LiteLLMKeyMaterial *string // TESTING-PHASE (reverts FIX01 §A.6)
```
In `EkInsertRow` after `LiteLLMToken *string`:
```go
	LiteLLMToken   *string
	LiteLLMKeyMaterial *string // TESTING-PHASE (reverts FIX01 §A.6)
```

- [ ] **Step 4: Verify it compiles**

Run: `./scripts/dev.sh go build ./internal/db/...`
Expected: builds (no callers broken — new fields are additive and nil by default).

- [ ] **Step 5: Commit**

```bash
git add internal/db/types_keys.go
git commit -m "feat(db): LiteLLMKeyMaterial field on key row/insert structs"
```

---

## Task 3: Persist + resolve the material for pk_ keys

**Files:**
- Modify: `internal/db/personal_keys.go:95-112` (INSERT)
- Modify: `internal/db/check_extend.go:52-98` (resolve)
- Test: `internal/db/check_extend_test.go` (extend existing round-trip)

- [ ] **Step 1: Write the failing round-trip test**

Add to `internal/db/check_extend_test.go` (mirror the existing insert→resolve test in that file; reuse its DB-pool test harness `t` helper — find the existing `TestPkCheckAndExtend*` for the pool setup and `InsertPersonalKey` helper):
```go
func TestPkCheckAndExtend_ReturnsKeyMaterial(t *testing.T) {
	pool := newTestPool(t) // same helper the other PkCheckAndExtend tests use
	hash := "deadbeef" + t.Name()
	material := "sk-test-pk-material"
	err := InsertPersonalKey(t.Context(), pool, PkInsertRow{
		KeyID:              "pkid_" + t.Name(),
		CredentialHash:     hash,
		OwnerEmail:         "u@e",
		ExpiresAt:          time.Now().Add(time.Hour),
		LiteLLMKeyMaterial: &material,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	row, err := PkCheckAndExtend(t.Context(), pool, hash)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if row == nil || row.LiteLLMKeyMaterial == nil || *row.LiteLLMKeyMaterial != material {
		t.Fatalf("LiteLLMKeyMaterial = %v; want %q", row.LiteLLMKeyMaterial, material)
	}
}
```
> If `newTestPool`/`t.Context()` names differ, match the harness the existing tests in this file already use.

- [ ] **Step 2: Run it to verify it fails**

Run: `make test-envtest-pkg PKG=./internal/db/ FOCUS=TestPkCheckAndExtend_ReturnsKeyMaterial`
Expected: FAIL — column not in INSERT/SELECT yet (scan leaves it nil).

- [ ] **Step 3: Add the column to `InsertPersonalKey`**

In `internal/db/personal_keys.go`, replace the INSERT in `InsertPersonalKey`:
```go
	const sql = `
		INSERT INTO personal_keys
		    (key_id, credential_hash, owner_email, expires_at,
		     status, litellm_user_id, litellm_token, litellm_key_material)
		VALUES ($1, $2, $3, $4, 'active', $5, $6, $7)
	`
	if _, err := pool.Exec(ctx, sql,
		row.KeyID, row.CredentialHash, row.OwnerEmail, row.ExpiresAt,
		row.LiteLLMUserID, row.LiteLLMToken, row.LiteLLMKeyMaterial,
	); err != nil {
```

- [ ] **Step 4: Add the column to `PkCheckAndExtend` RETURNING + Scan**

In `internal/db/check_extend.go`, extend the RETURNING list and the Scan:
```go
		RETURNING personal_keys.key_id,
		          personal_keys.owner_email,
		          personal_keys.expires_at,
		          personal_keys.litellm_user_id,
		          personal_keys.litellm_token,
		          personal_keys.litellm_key_material
	`
	r := &PkKeyInfo{}
	err := pool.QueryRow(ctx, sql, credentialHashHex).Scan(
		&r.KeyID, &r.OwnerEmail, &r.ExpiresAt, &r.LiteLLMUserID, &r.LiteLLMToken,
		&r.LiteLLMKeyMaterial,
	)
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `make test-envtest-pkg PKG=./internal/db/ FOCUS=TestPkCheckAndExtend_ReturnsKeyMaterial`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/personal_keys.go internal/db/check_extend.go internal/db/check_extend_test.go
git commit -m "feat(db): persist + resolve litellm_key_material for pk_ keys"
```

---

## Task 4: Persist + resolve the material for ek_ keys

**Files:**
- Modify: `internal/db/environment_keys.go:37-54` (INSERT)
- Modify: `internal/db/ek_resolve.go:41-71` (resolve)
- Test: `internal/db/ek_resolve_test.go`

- [ ] **Step 1: Write the failing round-trip test**

Add to `internal/db/ek_resolve_test.go` (mirror the existing `TestEkResolve*` harness):
```go
func TestEkResolve_ReturnsKeyMaterial(t *testing.T) {
	pool := newTestPool(t)
	hash := "cafebabe" + t.Name()
	material := "sk-test-ek-material"
	if err := InsertEnvironmentKey(t.Context(), pool, EkInsertRow{
		KeyID:              "ekid_" + t.Name(),
		CredentialHash:     hash,
		Environment:        "demo",
		OwnerEmail:         "u@e",
		Name:               "n",
		LiteLLMKeyMaterial: &material,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	row, err := EkResolve(t.Context(), pool, hash)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if row == nil || row.LiteLLMKeyMaterial == nil || *row.LiteLLMKeyMaterial != material {
		t.Fatalf("LiteLLMKeyMaterial = %v; want %q", row.LiteLLMKeyMaterial, material)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test-envtest-pkg PKG=./internal/db/ FOCUS=TestEkResolve_ReturnsKeyMaterial`
Expected: FAIL.

- [ ] **Step 3: Add the column to `InsertEnvironmentKey`**

In `internal/db/environment_keys.go`:
```go
	const sql = `
		INSERT INTO environment_keys
		    (key_id, credential_hash, environment, owner_email, name,
		     status, litellm_user_id, litellm_token, litellm_key_material)
		VALUES ($1, $2, $3, $4, $5, 'active', $6, $7, $8)
	`
	if _, err := pool.Exec(ctx, sql,
		row.KeyID, row.CredentialHash, row.Environment, row.OwnerEmail, row.Name,
		row.LiteLLMUserID, row.LiteLLMToken, row.LiteLLMKeyMaterial,
	); err != nil {
```

- [ ] **Step 4: Add the column to `EkResolve` RETURNING + Scan**

In `internal/db/ek_resolve.go`:
```go
		RETURNING key_id, environment, owner_email, name,
		          litellm_user_id, litellm_token, litellm_key_material
	`
	r := &EkKeyInfo{}
	err := pool.QueryRow(ctx, sql, credentialHashHex).Scan(
		&r.KeyID, &r.Environment, &r.OwnerEmail, &r.Name,
		&r.LiteLLMUserID, &r.LiteLLMToken, &r.LiteLLMKeyMaterial,
	)
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `make test-envtest-pkg PKG=./internal/db/ FOCUS=TestEkResolve_ReturnsKeyMaterial`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/environment_keys.go internal/db/ek_resolve.go internal/db/ek_resolve_test.go
git commit -m "feat(db): persist + resolve litellm_key_material for ek_ keys"
```

---

## Task 5: keystore — carry material into `KeyInfo`

**Files:**
- Modify: `internal/keystore/keystore.go:50-58`
- Modify: `internal/keystore/dbresolver.go:125-153`
- Test: `internal/keystore/dbresolver_test.go`

- [ ] **Step 1: Add the field to `KeyInfo`**

In `internal/keystore/keystore.go`, after the `LiteLLMToken` field:
```go
	LiteLLMToken  *string           `json:"litellm_token,omitempty"`
	// TESTING-PHASE (reverts FIX01 §A.6): LiteLLM virtual-key plaintext (sk-…),
	// forwarded as x-litellm-api-key so LiteLLM sees the caller's own key.
	LiteLLMKeyMaterial *string       `json:"litellm_key_material,omitempty"`
```

- [ ] **Step 2: Populate it in both lookups**

In `internal/keystore/dbresolver.go`, `pkLookupFor` return value, add after `LiteLLMToken`:
```go
			LiteLLMToken:       row.LiteLLMToken,
			LiteLLMKeyMaterial: row.LiteLLMKeyMaterial,
```
Same in `ekLookupFor`:
```go
			LiteLLMToken:       row.LiteLLMToken,
			LiteLLMKeyMaterial: row.LiteLLMKeyMaterial,
```

- [ ] **Step 3: Extend the resolver test**

In `internal/keystore/dbresolver_test.go`, find the existing pk_ resolve test that asserts `LiteLLMToken` flows through the fake `db`-row, and add a parallel assertion that `LiteLLMKeyMaterial` is carried. If the test uses a fake lookup returning a `PkKeyInfo`, set its `LiteLLMKeyMaterial` and assert the resolved `KeyInfo.LiteLLMKeyMaterial` matches. (Mirror the file's existing pattern; do not invent a new harness.)

- [ ] **Step 4: Run keystore tests**

Run: `make test-unit-pkg PKG=./internal/keystore/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/keystore/keystore.go internal/keystore/dbresolver.go internal/keystore/dbresolver_test.go
git commit -m "feat(keystore): carry LiteLLMKeyMaterial through KeyInfo"
```

---

## Task 6: middleware — carry material into `KeyContext`

**Files:**
- Modify: `internal/platformapi/middleware/keyctx.go:20-60`
- Test: `internal/platformapi/middleware/keyctx_test.go` (if present)

- [ ] **Step 1: Add the field to `KeyContext`**

After `LiteLLMToken *string`:
```go
	LiteLLMToken  *string
	LiteLLMKeyMaterial *string // TESTING-PHASE (reverts FIX01 §A.6)
	LiteLLMUserID *string
```

- [ ] **Step 2: Populate it in `WithKeyContext`**

In the `KeyContext{...}` literal built inside `WithKeyContext`, after `LiteLLMToken: info.LiteLLMToken,`:
```go
		LiteLLMToken:  info.LiteLLMToken,
		LiteLLMKeyMaterial: info.LiteLLMKeyMaterial,
		LiteLLMUserID: info.LiteLLMUserID,
```

- [ ] **Step 3: Verify it compiles + middleware tests pass**

Run: `make test-unit-pkg PKG=./internal/platformapi/middleware/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/platformapi/middleware/keyctx.go internal/platformapi/middleware/keyctx_test.go
git commit -m "feat(middleware): carry LiteLLMKeyMaterial in KeyContext"
```

---

## Task 7: pk_ mint persists the virtual key

**Files:**
- Modify: `internal/platformapi/auth/sso.go:438-446`
- Test: `internal/platformapi/auth/sso_test.go`

- [ ] **Step 1: Write/extend the failing mint test**

In `internal/platformapi/auth/sso_test.go`, find the test that asserts `mintAndPersistPK`/callback receives a `PkInsertRow` with `LiteLLMToken == &keyResp.Token` (the fake `LiteLLM.KeyGenerate` returns a `KeyGenerateResponse`). Make that fake return a `Key` and add an assertion:
```go
	// fake KeyGenerate returns Key:"sk-pk-xyz", Token:"tok-pk"
	if gotRow.LiteLLMKeyMaterial == nil || *gotRow.LiteLLMKeyMaterial != "sk-pk-xyz" {
		t.Fatalf("LiteLLMKeyMaterial = %v; want sk-pk-xyz", gotRow.LiteLLMKeyMaterial)
	}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test-unit-pkg PKG=./internal/platformapi/auth/`
Expected: FAIL — material not persisted.

- [ ] **Step 3: Persist `keyResp.Key`**

In `internal/platformapi/auth/sso.go`, in the `db.PkInsertRow{...}` literal, add (and update the FIX01 comment above the `KeyGenerate` call to note the testing-phase reversal):
```go
	row := db.PkInsertRow{
		KeyID:          keyID,
		CredentialHash: credHash,
		OwnerEmail:     email,
		ExpiresAt:      expiresAt,
		LiteLLMUserID:  &userID,
		LiteLLMToken:   &keyResp.Token,
		// TESTING-PHASE (reverts FIX01 §A.6): persist the sk-… plaintext so the
		// forwarder can authenticate to LiteLLM as this user's own key.
		LiteLLMKeyMaterial: &keyResp.Key,
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `make test-unit-pkg PKG=./internal/platformapi/auth/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platformapi/auth/sso.go internal/platformapi/auth/sso_test.go
git commit -m "feat(platformapi): pk_ mint persists litellm key material (testing-phase)"
```

---

## Task 8: ek_ mint persists the virtual key

**Files:**
- Modify: `internal/platformapi/envkeys/handler.go:443-461`
- Test: `internal/platformapi/envkeys/handler_test.go`

- [ ] **Step 1: Extend the failing mint test**

In the envkeys handler test, find where the fake DB's `InsertEnvironmentKey` captures the `EkInsertRow`. Make the fake `LiteLLM.KeyGenerate` return `Key:"sk-ek-xyz"` and assert:
```go
	if gotRow.LiteLLMKeyMaterial == nil || *gotRow.LiteLLMKeyMaterial != "sk-ek-xyz" {
		t.Fatalf("LiteLLMKeyMaterial = %v; want sk-ek-xyz", gotRow.LiteLLMKeyMaterial)
	}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test-unit-pkg PKG=./internal/platformapi/envkeys/`
Expected: FAIL.

- [ ] **Step 3: Persist `keyResp.Key`**

In `internal/platformapi/envkeys/handler.go`, after `llUserID := userID`:
```go
	llToken := keyResp.Token
	llUserID := userID
	llMaterial := keyResp.Key // TESTING-PHASE (reverts FIX01 §A.6)
```
And in the `db.EkInsertRow{...}` literal:
```go
		LiteLLMUserID:  &llUserID,
		LiteLLMToken:   &llToken,
		LiteLLMKeyMaterial: &llMaterial,
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `make test-unit-pkg PKG=./internal/platformapi/envkeys/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platformapi/envkeys/handler.go internal/platformapi/envkeys/handler_test.go
git commit -m "feat(platformapi): ek_ mint persists litellm key material (testing-phase)"
```

---

## Task 9: Forwarder header contract — `x-litellm-api-key=<material>`, drop key-id

**Files:**
- Modify: `internal/forwarder/headers/strip.go:69-117`
- Test: `internal/forwarder/headers/strip_test.go`

**READ FIRST:** `docs/developer-guide/jwt-forwarder.md` §1-3.

- [ ] **Step 1: Rewrite the failing strip test for the new contract**

Replace the assertions in `internal/forwarder/headers/strip_test.go` that check `x-litellm-api-key == masterKey` and `x-litellm-key-id == token`. New test:
```go
func TestStripAndRewrite_WritesUserKeyNoKeyID(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer client-jwt")        // must be stripped
	h.Set("X-Litellm-Api-Key", "client-supplied")       // must be stripped then overwritten
	h.Set("X-Litellm-Key-Id", "client-keyid")           // must be stripped, NOT re-added
	h.Set("X-Ach-Key", "pk_secret")                     // must be stripped

	StripAndRewrite(h, "sk-user-material")

	if got := h.Get("X-Litellm-Api-Key"); got != "sk-user-material" {
		t.Errorf("x-litellm-api-key = %q; want sk-user-material", got)
	}
	if _, ok := h["X-Litellm-Key-Id"]; ok {
		t.Errorf("x-litellm-key-id must NOT be set (delegation removed)")
	}
	if h.Get("Authorization") != "" {
		t.Errorf("Authorization must be stripped")
	}
}

func TestStripAndRewrite_EmptyMaterial(t *testing.T) {
	h := http.Header{}
	StripAndRewrite(h, "")
	if got := h.Get("X-Litellm-Api-Key"); got != "" {
		t.Errorf("empty material → empty header; got %q", got)
	}
}
```
> Remove/replace any existing strip_test assertions referencing the old two-arg `(masterKey, litellmToken)` contract.

- [ ] **Step 2: Run it to verify it fails**

Run: `make test-unit-pkg PKG=./internal/forwarder/headers/`
Expected: FAIL — signature still `(masterKey, litellmToken)`.

- [ ] **Step 3: Change the signature + write pass**

In `internal/forwarder/headers/strip.go`, change the function signature and the doc + write pass (the **strip pass is unchanged** — keep stripping `Authorization`, `x-litellm-*`, `x-ach-*`, hop-by-hop, Connection-named):
```go
// StripAndRewrite strips client trust headers (D-06) then writes the LiteLLM
// auth header (D-07). TESTING-PHASE (reverts FIX01 §A.6 / D-13): the value
// written is the CALLER's own LiteLLM virtual key (litellmAPIKey) — NOT the
// shared master key — so LiteLLM attributes the request 1:1 to the user. The
// x-litellm-key-id delegation header is no longer written (we authenticate as
// the user's own key). An empty litellmAPIKey writes an empty header (callers
// with no stored material — pre-migration keys — fail upstream, by design).
func StripAndRewrite(h http.Header, litellmAPIKey string) {
	// ... unchanged Connection-token collection + strip pass ...

	// Write pass.
	h.Set("x-litellm-api-key", litellmAPIKey)
}
```
Delete the old `h.Set("x-litellm-key-id", litellmToken)` line.

- [ ] **Step 4: Run the test to verify it passes**

Run: `make test-unit-pkg PKG=./internal/forwarder/headers/`
Expected: PASS (the package will not fully build until Task 10 updates the caller — that is expected; run `go vet` of the headers package alone if needed, or proceed to Task 10 then run both).

- [ ] **Step 5: Commit** (after Task 10 compiles the caller — or stage now and commit together with Task 10)

```bash
git add internal/forwarder/headers/strip.go internal/forwarder/headers/strip_test.go
```

---

## Task 10: Forwarder Director — source material from `KeyContext`

**Files:**
- Modify: `internal/forwarder/proxy/proxy.go:62-108`
- Test: `internal/forwarder/proxy/proxy_test.go`

- [ ] **Step 1: Rewrite the failing Director test**

In `internal/forwarder/proxy/proxy_test.go`, replace assertions checking the master key / key-id with the new contract. Use the existing `upstreamSpy()` + a `KeyContext` carrying `LiteLLMKeyMaterial`:
```go
func TestDirector_ForwardsUserMaterial(t *testing.T) {
	upstream, rec := upstreamSpy() // record x-litellm-api-key
	defer upstream.Close()
	material := "sk-user-1"
	rp := New(Deps{LiteLLMUpstream: mustParseURL(t, upstream.URL), Logger: slog.Default()})

	// /v1 → bare material
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	r = withKeyMaterial(t, r, material) // helper: KeyContext{LiteLLMKeyMaterial:&material}
	rp.ServeHTTP(httptest.NewRecorder(), r)
	if got := rec.LastHeader("X-Litellm-Api-Key"); got != material {
		t.Errorf("/v1 x-litellm-api-key = %q; want %q", got, material)
	}
	if rec.LastHeader("X-Litellm-Key-Id") != "" {
		t.Errorf("x-litellm-key-id must be absent")
	}

	// /mcp → Bearer-prefixed
	r2 := httptest.NewRequest(http.MethodPost, "/mcp/server-x", strings.NewReader("{}"))
	r2 = withKeyMaterial(t, r2, material)
	rp.ServeHTTP(httptest.NewRecorder(), r2)
	if got := rec.LastHeader("X-Litellm-Api-Key"); got != "Bearer "+material {
		t.Errorf("/mcp x-litellm-api-key = %q; want Bearer %q", got, material)
	}
}
```
> Add to `upstreamRec` a `LastHeader(name string) string` accessor if not present, and a `withKeyMaterial` helper that attaches `middleware.WithKeyContext(ctx, &keystore.KeyInfo{LiteLLMKeyMaterial:&m}, false)`. Reuse `mustParseURL` already in the package.

- [ ] **Step 2: Run it to verify it fails**

Run: `make test-unit-pkg PKG=./internal/forwarder/proxy/`
Expected: FAIL — Director still sends master + key-id.

- [ ] **Step 3: Rewrite the Director header block**

In `internal/forwarder/proxy/proxy.go`, replace the block from `litellmToken := ""` through the `/mcp` master-key `Set` (currently lines 79-102) with:
```go
			// TESTING-PHASE (reverts FIX01 §A.6 / D-13): forward the CALLER's
			// own LiteLLM virtual key as x-litellm-api-key (1:1 identity). The
			// master key is no longer sent; x-litellm-key-id delegation is gone.
			material := ""
			if kc, ok := middleware.KeyContextFromCtx(req.Context()); ok && kc.LiteLLMKeyMaterial != nil {
				material = *kc.LiteLLMKeyMaterial
			}
			headers.StripAndRewrite(req.Header, material)
			// MCP route only: LiteLLM's MCP key parser (user_api_key_auth_mcp.py)
			// requires a "Bearer " prefix; /v1, /gemini, /a2a take the bare value.
			if routeFor(req.URL.Path) == "/mcp" {
				req.Header.Set("X-Litellm-Api-Key", "Bearer "+material)
			}
```
Leave the JWT-attach block (`if token, present := jwtFromCtx(...)`) untouched AFTER this.

- [ ] **Step 4: Run the test to verify it passes**

Run: `make test-unit-pkg PKG=./internal/forwarder/proxy/`
Expected: PASS.

- [ ] **Step 5: Remove the now-dead `LiteLLMMasterKey` from the proxy/server forward path**

The master key fed ONLY the proxy header write, which is gone. The forwarder STILL needs the master elsewhere — `cmd/ach/cmd/forwarder.go:273` builds the `ll` LiteLLM client used by `keystore.NewLiteLLMTeamsResolver` (precheck `unauthorized_team` via `/user/info`). That path is UNCHANGED. Only the dead data-path field is removed:
- `internal/forwarder/proxy/proxy.go`: delete `LiteLLMMasterKey string` from `Deps` and the now-dead `if deps.LiteLLMMasterKey == ""` guard.
- `internal/forwarder/server.go`: delete `LiteLLMMasterKey string` from `Deps` (line 35) and its assignment into `proxy.HandlerDeps.Deps` (line 58).
- `cmd/ach/cmd/forwarder.go`: stop passing `LiteLLMMasterKey: llmRes.MasterKey` into the forwarder `Deps` — but KEEP `ll := litellm.NewRESTClient(..., llmRes.MasterKey, ...)` for the TeamsResolver.
- `internal/forwarder/proxy/handlers_test.go`: drop `LiteLLMMasterKey: "shared"` from `mkDeps`.

Run: `./scripts/dev.sh go build ./... && grep -rn "LiteLLMMasterKey" internal/forwarder/ cmd/ach/cmd/forwarder.go` — expect zero hits in proxy/server, one remaining `ll`-client master usage in forwarder.go.

- [ ] **Step 6: Commit (Tasks 9 + 10 together)**

```bash
git add internal/forwarder/headers/strip.go internal/forwarder/headers/strip_test.go \
        internal/forwarder/proxy/proxy.go internal/forwarder/proxy/proxy_test.go
git commit -m "feat(forwarder): forward per-user litellm key as x-litellm-api-key (testing-phase)"
```

---

## Task 11: Fix forwarder handler + e2e tests for the new contract

**Files:**
- Modify: `internal/forwarder/proxy/handlers_test.go` (mkDeps still sets `LiteLLMMasterKey: "shared"` — harmless, but any assertion on forwarded master/key-id must change)
- Modify: `test/e2e/phase4_invariants_test.go` (SC1_HeaderRewrite)

- [ ] **Step 1: Audit handler tests for stale master/key-id assertions**

Run: `grep -n "x-litellm-key-id\|X-Litellm-Key-Id\|LiteLLMMasterKey\|master" internal/forwarder/proxy/handlers_test.go internal/forwarder/proxy/proxy_test.go`
For each assertion expecting the master key or a `x-litellm-key-id` header on the upstream request, update it to expect the per-user material (or its absence for key-id). The `upstreamSpy` in handlers_test only records `Authorization`; if a test asserts the upstream saw the master, switch it to set a `LiteLLMKeyMaterial` on the `KeyContext` (via `requestWithKC`, extend `KeyContext` construction in `requestWithKC` to pass `LiteLLMKeyMaterial`) and assert that.

- [ ] **Step 2: Extend `requestWithKC` to thread material**

In `internal/forwarder/proxy/handlers_test.go`, the `requestWithKC` helper builds a `keystore.KeyInfo`; add `LiteLLMKeyMaterial: kc.LiteLLMKeyMaterial,` to that literal so tests can supply material via `middleware.KeyContext{LiteLLMKeyMaterial: ...}`.

- [ ] **Step 3: Run forwarder unit tests**

Run: `make test-unit-pkg PKG=./internal/forwarder/proxy/ && make test-unit-pkg PKG=./internal/forwarder/`
Expected: PASS.

- [ ] **Step 4: Update e2e SC1 header-rewrite assertion**

In `test/e2e/phase4_invariants_test.go` `testPhase4SC1*`, the test asserts the forwarder→LiteLLM hop does not 401. With per-user material now minted (Tasks 7-8), the e2e-acquired pk_/ek_ carries real material, so LiteLLM should accept it. If the test hard-codes any master-key expectation, relax it to "status != 401". Add a comment noting the material-forward model.

- [ ] **Step 5: Commit**

```bash
git add internal/forwarder/proxy/handlers_test.go test/e2e/phase4_invariants_test.go
git commit -m "test(forwarder): update header-rewrite expectations to per-user material"
```

---

## Task 12: Docs + memory + lint sweep

**Files:**
- Modify: `docs/developer-guide/jwt-forwarder.md` §3 (LiteLLM auth)
- Modify: `references/troubleshooting.md`
- Create: memory file `~/.claude/projects/.../memory/litellm-key-material-forward.md`

- [ ] **Step 1: Document the new LiteLLM auth model**

In `docs/developer-guide/jwt-forwarder.md`, add a subsection under §3 explaining: the forwarder now sends the caller's own LiteLLM virtual key as `x-litellm-api-key` (bare on `/v1`/`/gemini`/`/a2a`, `Bearer ` on `/mcp`); master key + `x-litellm-key-id` are no longer forwarded; persisted plaintext in `litellm_key_material` (TESTING-PHASE, reverts FIX01 §A.6); NULL material → empty header → 401 (re-mint the key). Note the JWT/BIP path is orthogonal and unchanged.

- [ ] **Step 2: Add a troubleshooting entry**

In `references/troubleshooting.md`, add: "❌ LiteLLM 401 on /v1 or /mcp after upgrade → the key predates migration 000011 (no `litellm_key_material`); re-mint the pk_/ek_."

- [ ] **Step 3: Write the memory file** (one fact, project type) recording the FIX01 §A.6 reversal as a deliberate testing-phase decision with the `litellm_key_material` column + the revert path, and add the `MEMORY.md` index line.

- [ ] **Step 4: Full lint sweep**

Run: `make qa-lint-changed`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add docs/developer-guide/jwt-forwarder.md references/troubleshooting.md
git commit -m "docs(forwarder): document per-user litellm key forwarding (testing-phase)"
```

---

## Task 13: Full gates (envtest + e2e)

- [ ] **Step 1: Unit + envtest**

Run: `make test-unit && make test-envtest`
Expected: green (envtest applies migration 000011 and exercises the resolve path).

- [ ] **Step 2: E2E (MANDATORY — touches forwarder + platformapi + db + e2e)**

Run: `make e2e-full`
Expected: green. Watch `TestPhase4Invariants/SC1_HeaderRewrite` and `SC2_McpA2aPrecheck` specifically. If a freshly-minted e2e key 401s at LiteLLM, confirm migration 000011 ran in the cluster and the mint persisted `Key`.

- [ ] **Step 3: Manual smoke (live cluster kept by e2e-full)**

```bash
# Mint a fresh pk_ via SSO, fire a /v1 call, confirm 200 (LiteLLM saw the user key):
# (use the helper in references/local-testing-gateway.md to mint)
curl -s -H "x-ach-key: $PK" http://localhost:8080/v1/models -o /dev/null -w '%{http_code}\n'
# Confirm at the MCP/LiteLLM side it is the USER key, not the master.
```

- [ ] **Step 4: Final commit if any fixes were needed during gating, then stop for review.**

---

## Self-Review notes

- **Spec coverage:** persist material (T1-2,7-8) · resolve through KeyInfo/KeyContext (T3-6) · forward as x-litellm-api-key bare/Bearer (T9-10) · drop master + key-id (T9-10) · no flag, always-on (T10) · NULL→empty, no fallback (T9 EmptyMaterial test) · JWT path untouched (T10 step 3 leaves jwt block) · docs (T12) · gates (T13). ✓
- **Type consistency:** field is `LiteLLMKeyMaterial *string` everywhere (PkKeyInfo, EkKeyInfo, PkInsertRow, EkInsertRow, KeyInfo, KeyContext); SQL column `litellm_key_material`; `StripAndRewrite(h, litellmAPIKey string)` one-arg. ✓
- **Risk:** existing forwarder/db/keystore tests asserting the old master/key-id contract will fail to compile/pass — Task 9-11 explicitly update them; if the executor finds additional stale assertions, fix in place (same contract).
- **Revert path:** every touch tagged `TESTING-PHASE (reverts FIX01 §A.6)`; `grep -rn "TESTING-PHASE (reverts FIX01" ` enumerates the full surface to undo.
