# P0 Security & Correctness Blockers Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Close the three pre-production P0 blockers from the external review — encrypt LiteLLM virtual-key material at rest (G3), make the CLI refuse plaintext `http://` Hub URLs by default (G19), and make `hydrate --sync` actually prune dropped-resource files instead of silently no-op'ing (G5).

**Architecture:** Three independent task groups, no cross-dependencies — implement and commit in any order or in parallel worktrees.
- **G3** adds a new `internal/keycrypt` AES-256-GCM seal/open package plus a `dekenv` loader that mirrors the existing `credhash`/`pepperenv` pattern; platform-api encrypts the `sk-…` on mint, forwarder decrypts on use, and a migration renames the column + nulls the testing-phase plaintext.
- **G19** adds a shared secure-URL validator in the CLI `config` package (default-refuse non-`https://`) plus an `--insecure`/`ACH_INSECURE` opt-in threaded through the login/hydrate/config commands.
- **G5** extracts the next-state composition out of `step12WriteState` into a pure helper, then feeds that composed state to the step-11 `Sync` call as `newFile` (today it passes the old state twice → zero pruning), preserving the `maybeKill(11)` SIGKILL boundary.

**Tech Stack:** Go (cobra CLI, controller-runtime services), stdlib `crypto/aes`+`crypto/cipher`, Postgres (golang-migrate `db/migrations/*`), stdlib `testing` (no testify in these packages), testcontainers-go for `//go:build integration` DB tests, kind+Helm e2e (`//go:build e2e`).

**Source decisions:** `docs/external-review-resolutions.md` §G3 (DECIDED: Fix-1 encrypt at rest), §G19 (DECIDED: CLI = B default-refuse + opt-in; HTTPSource = D docs-only), §G5 (DECIDED: Path 2 wire STATE-05 now). The P0 roadmap is at lines 1230-1236 of that doc.

---

## Engineering decisions made in this plan (within the DECIDED directions)

These resolve the "open implementation nits" the decision doc left for build time. Confirm before execution — two have operational blast radius (flagged ⚠).

1. **G3 — DEK is mandatory, not a soft warning ⚠.** The decision doc proposed a "loud startup warning when plaintext detected; refuse-to-start under a future production-mode flag." This plan instead makes the data-encryption key (DEK) a **hard requirement** for platform-api and forwarder, mirroring how the `pepper` is already required. Encryption is therefore *always on*; there is no plaintext mode to guard. Consequence: the new `ach-key-encryption-key` Secret must be provisioned everywhere pepper is today (config/secrets, e2e values, any deploy docs) or those services refuse to start. This is simpler and structurally satisfies the acceptance criterion ("ACH stores no LiteLLM virtual-key plaintext at rest").
2. **G3 — rename the column to `litellm_key_material_enc`.** The decision doc allowed either renaming or keeping the name with a changed content contract. Rename is chosen: it is self-documenting and forces the compiler/tests to flag every read/write site, preventing an accidental "read old plaintext as if it were ciphertext" bug. The migration also NULLs the testing-phase plaintext (clean cutover — pre-existing keys already "break by design").
3. **G3 — single key now; rotation deferred (YAGNI).** The stored blob carries a 1-byte version prefix for forward-compatibility, but current/next multi-key rotation is a P1 follow-up, not built here.
4. **G19 — `http://localhost` is also refused by default ⚠.** Per decision B (literal frozen posture). The kind+Helm dev/e2e gateway is `http://localhost:8080`, so e2e CLI invocations and local-dev docs must pass `--insecure` (or `ACH_INSECURE=1`) after this lands. The plan updates those call sites.
5. **G19 — `ACH_INSECURE` is truthy on `1`/`true` (case-insensitive); the `--insecure` flag OR a truthy env var enables the opt-in.**

---

## Pre-flight (once, before any task group)

**Step 0.1: Work in an isolated worktree** (recommended — see superpowers:using-git-worktrees). The host has no Go; every `make`/`go` command auto-routes through the `ach-devtools` container.

**Step 0.2: Confirm a clean baseline**

Run: `make test-unit`
Expected: PASS (warm ~10s). If red on `main`, stop and report — do not build on a broken base.

**Step 0.3: Remember the gates that will block the push** (CLAUDE.md "Publication"):
- Every **new** `*.go` file MUST start with `// SPDX-License-Identifier: Apache-2.0` (pre-push gate 15). `*.sql` migrations do not.
- `go mod tidy` must be clean (no new deps are expected here — all stdlib).
- `make qa-lint` (full) and `make test-unit` run inside the pre-push hook.
- Update the doc(s) listed in each group **in the same commit** (CLAUDE.md "Documentation hygiene").

---

# Task Group G3 — Encrypt `litellm_key_material` at rest (AES-256-GCM)

**Why:** `personal_keys.litellm_key_material` and `environment_keys.litellm_key_material` store the live LiteLLM `sk-…` credential in **plaintext** (migration `000011`, tagged `TESTING-PHASE`). A single Postgres dump yields every user's working provider credential, bypassing ACH entirely. This is the production blocker.

**Acceptance criterion (security):** *ACH stores no LiteLLM virtual-key plaintext at rest.* A DB dump alone is useless without the separate DEK.

---

### Task G3.1: AES-256-GCM seal/open package

**Files:**
- Create: `internal/keycrypt/keycrypt.go`
- Test: `internal/keycrypt/keycrypt_test.go`

**Step 1: Write the failing test**

```go
// internal/keycrypt/keycrypt_test.go
package keycrypt

import (
	"bytes"
	"testing"
)

func testKey() []byte { // 32 bytes
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestSealOpen_Roundtrip(t *testing.T) {
	k := testKey()
	pt := []byte("sk-abc123-secret")
	blob, err := Seal(k, pt)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains([]byte(blob), pt) {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := Open(k, blob)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("roundtrip mismatch: %q", got)
	}
}

func TestSeal_NonDeterministic(t *testing.T) {
	k := testKey()
	a, _ := Seal(k, []byte("x"))
	b, _ := Seal(k, []byte("x"))
	if a == b {
		t.Fatal("two seals identical — nonce not random")
	}
}

func TestOpen_TamperFails(t *testing.T) {
	k := testKey()
	blob, _ := Seal(k, []byte("payload"))
	bad := "A" + blob[1:] // flip first base64 char
	if _, err := Open(k, bad); err == nil {
		t.Fatal("tampered ciphertext opened without error")
	}
}

func TestOpen_WrongKeyFails(t *testing.T) {
	blob, _ := Seal(testKey(), []byte("payload"))
	other := make([]byte, KeySize) // all zeros
	if _, err := Open(other, blob); err == nil {
		t.Fatal("wrong key opened ciphertext")
	}
}

func TestSeal_BadKeySize(t *testing.T) {
	if _, err := Seal(make([]byte, 16), []byte("x")); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/keycrypt/...`
Expected: FAIL — `undefined: KeySize`, `undefined: Seal`, `undefined: Open`.

**Step 3: Write minimal implementation**

```go
// internal/keycrypt/keycrypt.go
// SPDX-License-Identifier: Apache-2.0

// Package keycrypt envelope-encrypts secret material at rest with
// AES-256-GCM. The stored form is base64std(version || nonce || ciphertext).
// Used by platform-api (seal on mint) and the forwarder (open on use) to keep
// LiteLLM virtual-key material out of cleartext in Postgres (G3).
package keycrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize is the required data-encryption-key length (AES-256).
const KeySize = 32

// formatV1 tags the on-disk blob so a future rotation/format can be told apart
// without ambiguity. Single-key only today; multi-key rotation is a P1 follow-up.
const formatV1 byte = 1

var (
	// ErrKeySize is returned when the DEK is not exactly KeySize bytes.
	ErrKeySize = fmt.Errorf("keycrypt: key must be %d bytes", KeySize)
	// ErrFormat is returned when a stored blob is malformed or uses an
	// unknown version byte.
	ErrFormat = errors.New("keycrypt: malformed or unknown ciphertext format")
)

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext and returns base64std(version || nonce || ciphertext).
func Seal(key, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := []byte{formatV1}
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal. It returns ErrFormat on a malformed blob and a GCM
// authentication error on a wrong key or tampered ciphertext.
func Open(key []byte, blob string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, ErrFormat
	}
	ns := gcm.NonceSize()
	if len(raw) < 1+ns || raw[0] != formatV1 {
		return nil, ErrFormat
	}
	nonce := raw[1 : 1+ns]
	ct := raw[1+ns:]
	return gcm.Open(nil, nonce, ct, nil)
}
```

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/keycrypt/...`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/keycrypt/keycrypt.go internal/keycrypt/keycrypt_test.go
git commit -m "feat(keycrypt): add AES-256-GCM seal/open for at-rest key material (G3)"
```

---

### Task G3.2: DEK loader (mirror `pepperenv`)

**Files:**
- Create: `internal/keycrypt/dekenv/dekenv.go`
- Test: `internal/keycrypt/dekenv/dekenv_test.go`
- Reference (read first): `internal/credhash/pepperenv/pepperenv.go` — copy its shape exactly.

**Step 1: Write the failing test**

```go
// internal/keycrypt/dekenv/dekenv_test.go
package dekenv

import (
	"encoding/base64"
	"errors"
	"testing"
)

func TestLoad_Valid(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv(EnvVarName, base64.StdEncoding.EncodeToString(key))
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("len = %d, want 32", len(got))
	}
}

func TestLoad_Missing(t *testing.T) {
	t.Setenv(EnvVarName, "")
	if _, err := Load(); !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
}

func TestLoad_Placeholder(t *testing.T) {
	t.Setenv(EnvVarName, PlaceholderPrefix+"whatever")
	if _, err := Load(); !errors.Is(err, ErrPlaceholder) {
		t.Fatalf("want ErrPlaceholder, got %v", err)
	}
}

func TestLoad_WrongLength(t *testing.T) {
	t.Setenv(EnvVarName, base64.StdEncoding.EncodeToString(make([]byte, 16)))
	if _, err := Load(); err == nil {
		t.Fatal("want error for 16-byte key")
	}
}

func TestLoad_NotBase64(t *testing.T) {
	t.Setenv(EnvVarName, "!!!not base64!!!")
	if _, err := Load(); err == nil {
		t.Fatal("want error for non-base64 value")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/keycrypt/dekenv/...`
Expected: FAIL — undefined symbols.

**Step 3: Write minimal implementation**

```go
// internal/keycrypt/dekenv/dekenv.go
// SPDX-License-Identifier: Apache-2.0

// Package dekenv loads the data-encryption key (DEK) used by keycrypt from the
// environment. Mirrors internal/credhash/pepperenv: the value is base64std of
// exactly 32 raw bytes (AES-256). Required by platform-api and the forwarder.
package dekenv

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ackstorm/ach/internal/keycrypt"
)

// EnvVarName is the environment variable holding the base64 DEK.
const EnvVarName = "ACH_KEY_ENCRYPTION_KEY"

// PlaceholderPrefix marks an un-replaced template value.
const PlaceholderPrefix = "REPLACE-ME-WITH-RANDOM-"

var (
	// ErrMissing means the env var is unset/empty.
	ErrMissing = fmt.Errorf("%s is not set", EnvVarName)
	// ErrPlaceholder means the template default was not replaced.
	ErrPlaceholder = fmt.Errorf("%s still holds the placeholder value", EnvVarName)
)

// Load reads, base64-decodes, and validates the DEK (must be 32 bytes).
func Load() ([]byte, error) {
	v := os.Getenv(EnvVarName)
	if v == "" {
		return nil, ErrMissing
	}
	if strings.HasPrefix(v, PlaceholderPrefix) {
		return nil, ErrPlaceholder
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
	if err != nil {
		return nil, fmt.Errorf("%s must be base64: %w", EnvVarName, err)
	}
	if len(raw) != keycrypt.KeySize {
		return nil, fmt.Errorf("%s decoded to %d bytes: %w", EnvVarName, len(raw), errors.New("must be 32 bytes"))
	}
	return raw, nil
}
```

> ⚠ Confirm the module path: the import above assumes `github.com/ackstorm/ach`. Verify against `go.mod` and against the import path `pepperenv.go` uses for sibling packages; match it exactly.

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/keycrypt/dekenv/...`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/keycrypt/dekenv/
git commit -m "feat(keycrypt): add ACH_KEY_ENCRYPTION_KEY loader (dekenv) (G3)"
```

---

### Task G3.3: Migration — rename column + null testing-phase plaintext

**Files:**
- Create: `db/migrations/000014_litellm_key_material_enc.up.sql`
- Create: `db/migrations/000014_litellm_key_material_enc.down.sql`
- Reference: `db/migrations/000011_litellm_key_material.up.sql` (current highest is `000013`).

**Step 1: Write the up migration**

```sql
-- 000014_litellm_key_material_enc.up.sql
-- G3: stop storing the LiteLLM virtual key (sk-…) in cleartext. The column now
-- holds keycrypt's base64std(version||nonce||ciphertext) blob (AES-256-GCM),
-- written by platform-api at mint and decrypted by the forwarder at use.
-- Rename for self-documentation, and NULL the testing-phase plaintext rows:
-- they are unrecoverable as ciphertext and pre-migration keys already break by
-- design (clean cutover, per the G3 decision).
ALTER TABLE personal_keys    RENAME COLUMN litellm_key_material TO litellm_key_material_enc;
ALTER TABLE environment_keys RENAME COLUMN litellm_key_material TO litellm_key_material_enc;
UPDATE personal_keys    SET litellm_key_material_enc = NULL WHERE litellm_key_material_enc IS NOT NULL;
UPDATE environment_keys SET litellm_key_material_enc = NULL WHERE litellm_key_material_enc IS NOT NULL;
```

**Step 2: Write the down migration**

```sql
-- 000014_litellm_key_material_enc.down.sql
ALTER TABLE personal_keys    RENAME COLUMN litellm_key_material_enc TO litellm_key_material;
ALTER TABLE environment_keys RENAME COLUMN litellm_key_material_enc TO litellm_key_material;
```

**Step 3: Commit** (DB-layer wiring in the next task references the new name; commit together if you prefer — but the migration alone is a valid checkpoint)

```bash
git add db/migrations/000014_litellm_key_material_enc.up.sql db/migrations/000014_litellm_key_material_enc.down.sql
git commit -m "feat(db): rename litellm_key_material -> _enc, null testing plaintext (G3)"
```

---

### Task G3.4: Point the DB layer at the renamed column

**Files (modify):**
- `internal/db/personal_keys.go:95-112` — `InsertPersonalKey` (column `litellm_key_material` → `litellm_key_material_enc`, `$7`)
- `internal/db/environment_keys.go:37-54` — `InsertEnvironmentKey` (`$8`)
- `internal/db/check_extend.go:85-99` — `PkCheckAndExtend` RETURNING clause
- `internal/db/ek_resolve.go:45-58` — `EkResolve` RETURNING clause
- `internal/db/types_keys.go:43,70,87,101` — update the `// TESTING-PHASE` comments on `LiteLLMKeyMaterial` to "encrypted at rest (keycrypt blob)"; keep the Go field name `LiteLLMKeyMaterial` and `*string` type (minimal churn — the value is now a base64 blob).

**Step 1:** Grep to find every SQL reference so none is missed.

Run: `./scripts/dev.sh grep -rn "litellm_key_material" internal/db`
Expected: the four SQL files above. Change each `litellm_key_material` token to `litellm_key_material_enc`.

**Step 2:** Apply the rename in the four SQL strings and refresh the struct comments.

**Step 3: Verify the DB integration tests still roundtrip the column** (they drive the Go API, which is unchanged; only SQL text moved).

Run: `./scripts/dev.sh go test -tags=integration ./internal/db/...`
Expected: PASS (testcontainers spins a Postgres; needs docker). The key-material roundtrip assertions live at `check_extend_test.go:252` and `ek_resolve_test.go:77` — they store/read an opaque string, so a base64 blob roundtrips fine.

**Step 4: Commit**

```bash
git add internal/db/personal_keys.go internal/db/environment_keys.go internal/db/check_extend.go internal/db/ek_resolve.go internal/db/types_keys.go
git commit -m "feat(db): read/write litellm_key_material_enc column (G3)"
```

---

### Task G3.5: Encrypt on mint (platform-api write path)

**Files (modify):**
- `cmd/ach/cmd/platform_api.go` — add `KeyEncryptionKey []byte` to `platformAPIConfig` (near `Pepper`, ~line 69); in `validatePlatformAPIConfig()` call `dekenv.Load()` (mirror the `pepperenv.Load()` call at ~line 100-104) and fail config validation on error; pass it into `platformapi.Deps{... KeyEncryptionKey: cfg.KeyEncryptionKey}` (~line 264).
- `internal/platformapi/*` `Deps` struct — add `KeyEncryptionKey []byte` field (find the struct that already carries `Pepper`).
- `internal/platformapi/auth/sso.go:438-466` — before building `db.PkInsertRow`, seal: replace `LiteLLMKeyMaterial: &keyResp.Key` (line ~446-449) with the sealed blob.
- `internal/platformapi/envkeys/handler.go:382-464` — `mintAndInsert`; replace `llMaterial := keyResp.Key` (line ~445) with the sealed blob. The handler needs access to the DEK — thread it via the envkeys `Deps`/handler struct from `platformapi.Deps`.

**Step 1: Write the failing test (sso path)**

In `internal/platformapi/auth/sso_test.go` — the existing assertion at line ~888 checks `LiteLLMKeyMaterial`. Inject a test DEK into the test `Deps` and change the assertion to decrypt-and-compare:

```go
// in the SSO callback test setup, set deps.KeyEncryptionKey = testDEK (32 bytes)
// then, where the inserted row is captured:
if gotRow.LiteLLMKeyMaterial == nil {
	t.Fatal("expected sealed key material, got nil")
}
if *gotRow.LiteLLMKeyMaterial == wantSK {
	t.Fatal("key material stored in PLAINTEXT — must be encrypted")
}
pt, err := keycrypt.Open(testDEK, *gotRow.LiteLLMKeyMaterial)
if err != nil || string(pt) != wantSK {
	t.Fatalf("sealed material did not open to sk-: %v / %q", err, pt)
}
```

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/platformapi/auth/...`
Expected: FAIL — material still equals plaintext.

**Step 3: Implement the seal at both write sites**

```go
// sso.go — replace the &keyResp.Key assignment:
sealed, err := keycrypt.Seal(deps.KeyEncryptionKey, []byte(keyResp.Key))
if err != nil {
	// surface as a 500 on the SSO callback — minting cannot proceed safely
	return // (use the handler's existing error path)
}
// then in PkInsertRow:
LiteLLMKeyMaterial: &sealed,
```

```go
// envkeys/handler.go — replace `llMaterial := keyResp.Key`:
sealed, err := keycrypt.Seal(cr.deps.KeyEncryptionKey, []byte(keyResp.Key))
if err != nil {
	return /* existing mint-error path */
}
// then in EkInsertRow:
LiteLLMKeyMaterial: &sealed,
```

> Match the actual error-return shape of each function — read the surrounding code; both already have error paths for mint failures. Never log `keyResp.Key` or the sealed blob.

**Step 4: Add the analogous envkeys handler test** in `internal/platformapi/envkeys/handler_test.go` (assertion currently at line ~249) — same decrypt-and-compare shape, with `deps.KeyEncryptionKey = testDEK`.

**Step 5: Run tests**

Run: `./scripts/dev.sh go test ./internal/platformapi/...`
Expected: PASS.

**Step 6: Commit**

```bash
git add cmd/ach/cmd/platform_api.go internal/platformapi/
git commit -m "feat(platform-api): encrypt LiteLLM key material on mint (G3)"
```

---

### Task G3.6: Decrypt on use (forwarder read path)

**Files (modify):**
- `cmd/ach/cmd/forwarder.go` — add `KeyEncryptionKey []byte` to `forwarderConfig` (~line 100, near `Pepper`); load via `dekenv.Load()` in `validateForwarderConfig()` (~line 125-129); pass into the forwarder proxy `Deps`.
- `internal/forwarder/proxy/proxy.go:63-99` — the Director closure. Add the DEK to `Deps`; decrypt `*kc.LiteLLMKeyMaterial` once into `material` before `headers.StripAndRewrite` (line ~80) and before the MCP `X-Litellm-Api-Key` set (line ~94).

**Step 1: Write the failing test**

In `internal/forwarder/proxy/proxy_test.go` (existing material-forwarding asserts at lines 78/125/166/185): seal a known `sk-` with a test DEK, put the **sealed** blob in the `KeyContext.LiteLLMKeyMaterial`, set `deps.KeyEncryptionKey = testDEK`, and assert the forwarded header carries the **decrypted** plaintext.

```go
sealed, _ := keycrypt.Seal(testDEK, []byte("sk-real-key"))
kc := middleware.KeyContext{LiteLLMKeyMaterial: &sealed /* ...rest as existing */}
// ... drive the Director, then:
if got := req.Header.Get("X-Litellm-Api-Key"); got != "Bearer sk-real-key" {
	t.Fatalf("forwarded header = %q, want decrypted plaintext", got)
}
```

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/forwarder/proxy/...`
Expected: FAIL — the sealed blob is forwarded verbatim (not decrypted).

**Step 3: Implement decrypt-once in the Director**

```go
// proxy.go, replacing lines ~76-81:
material := ""
if kc, ok := middleware.KeyContextFromCtx(req.Context()); ok && kc.LiteLLMKeyMaterial != nil {
	pt, err := keycrypt.Open(deps.KeyEncryptionKey, *kc.LiteLLMKeyMaterial)
	if err != nil {
		// Decrypt failure (wrong DEK / legacy plaintext row / corruption):
		// forward no key — upstream 401, by design. Do NOT log the material.
		deps.Logger.Warn("key material decrypt failed", "err", err)
	} else {
		material = string(pt)
	}
}
headers.StripAndRewrite(req.Header, material)
```

> Use the forwarder's existing logger handle (match the `Deps` field name). The MCP path at line ~94 reuses the same `material` variable — no second decrypt.

**Step 4: Run tests**

Run: `./scripts/dev.sh go test ./internal/forwarder/...`
Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/ach/cmd/forwarder.go internal/forwarder/proxy/proxy.go internal/forwarder/proxy/handlers_test.go internal/forwarder/proxy/proxy_test.go
git commit -m "feat(forwarder): decrypt LiteLLM key material on use (G3)"
```

---

### Task G3.7: Provision the DEK Secret (deploy/e2e/dev) + docs

**Files:**
- Create: `config/secrets/key_encryption_key_secret.yaml` — mirror `config/secrets/credential_hash_pepper_secret.yaml` (Secret `ach-key-encryption-key`, key `dek`, placeholder `REPLACE-ME-WITH-RANDOM-32-BYTES-BASE64`).
- Modify: `config/deployments/platform-api_deployment.yaml` and `config/deployments/forwarder_deployment.yaml` — add an `ACH_KEY_ENCRYPTION_KEY` env from the new Secret, mirroring the `ACH_CREDENTIAL_HASH_PEPPER` block (~lines 53-57).
- Modify: `test/e2e/cluster/02-ach/ach.values.yaml` — add `ACH_KEY_ENCRYPTION_KEY` to the `extraEnv` list (after the pepper block, ~lines 20-24) and ensure the `ach-key-encryption-key` Secret is created in the e2e bootstrap (wherever the pepper Secret is seeded — grep `ach-credential-hash-pepper` under `test/e2e/` and `scripts/`).
- Modify: `docs/external-review-resolutions.md` §G3 — append a "landed" note (column renamed to `_enc`, DEK mandatory, single-key now).
- Modify: `references/troubleshooting.md` — add a forwarder failure mode: "401 upstream + `key material decrypt failed` log = DEK mismatch or a legacy/plaintext row; re-mint the key."

**Step 1:** Create the Secret manifest and wire the env into both Deployments + e2e values.

**Step 2: Generate a real DEK for local/e2e** (document the command, do not commit a real key):

```bash
openssl rand -base64 32   # -> ACH_KEY_ENCRYPTION_KEY value
```

**Step 3: Bring up a cluster and confirm both services start with the DEK and a login→forward roundtrip works.**

Run: `make cluster-up`
Then: `make wait-platform-api wait-forwarder`
Expected: both Ready; no `dekenv` startup error in `make logs-platform-api` / `make logs-forwarder`.

**Step 4: Commit**

```bash
git add config/secrets/ config/deployments/ test/e2e/cluster/02-ach/ach.values.yaml docs/external-review-resolutions.md references/troubleshooting.md
git commit -m "feat(deploy): provision ACH_KEY_ENCRYPTION_KEY secret; doc G3 (G3)"
```

---

# Task Group G19 — CLI default-refuse non-`https://` Hub URLs

**Why:** The CLI accepts `http://` Hub URLs with only a warning, sending the `pk_`/`ek_` bearer credential in cleartext on the wire. Decision B: default-**refuse** any non-`https://` URL (incl. localhost); allow only with an explicit `--insecure` flag or `ACH_INSECURE` opt-in.

**Acceptance criterion:** `ach-cli` refuses any `http://` Hub URL (from profile, `--base-url`, or `ACH_BASE_URL`) unless the user explicitly opts in; the opt-in is required even for `localhost`.

---

### Task G19.1: Shared secure-URL validator in the config package

**Files:**
- Modify: `internal/cli/config/config.go` — add `ErrInsecureURL`, a `ValidateSecureURL` function, and thread an `allowInsecure` bool through `Load`/`Save`/`validateProfiles` (lines 55-70 errors; 240-251 `validateProfiles`; 127 Load, 141 Save).
- Modify: `internal/cli/config/config_test.go` — flip the accept-http tests to refuse-by-default + allow-with-insecure.

**Step 1: Write the failing test**

```go
// internal/cli/config/config_test.go (external pkg config_test)
func TestValidateSecureURL(t *testing.T) {
	cases := []struct {
		url      string
		insecure bool
		wantErr  error
	}{
		{"https://hub.example.com", false, nil},
		{"http://hub.example.com", false, ErrInsecureURL},
		{"http://localhost:8080", false, ErrInsecureURL}, // localhost also refused
		{"http://localhost:8080", true, nil},             // opt-in
		{"https://x", true, nil},
		{"ftp://x", false, ErrInvalidURLScheme},
		{"ftp://x", true, ErrInvalidURLScheme}, // insecure does not excuse a bad scheme
	}
	for _, c := range cases {
		err := ValidateSecureURL(c.url, c.insecure)
		if c.wantErr == nil && err != nil {
			t.Errorf("%s insecure=%v: unexpected %v", c.url, c.insecure, err)
		}
		if c.wantErr != nil && !errors.Is(err, c.wantErr) {
			t.Errorf("%s insecure=%v: want %v, got %v", c.url, c.insecure, c.wantErr, err)
		}
	}
}
```

Also update the existing `TestSave_AcceptsHTTPAndHTTPS` (line ~106) and `TestLoad_AcceptsHTTP` (line ~190): rename/rewrite to assert `Save`/`Load` **refuse** `http://` by default and **accept** it when called with `allowInsecure=true`.

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/cli/config/...`
Expected: FAIL — `undefined: ErrInsecureURL`, `undefined: ValidateSecureURL`; old accept-http tests fail.

**Step 3: Implement**

```go
// config.go — new sentinel (near line 61):
// ErrInsecureURL is returned for a non-https:// URL when insecure transport
// was not explicitly opted into (--insecure / ACH_INSECURE).
var ErrInsecureURL = errors.New("config: refusing plaintext http:// — credentials would be sent unencrypted; pass --insecure or set ACH_INSECURE=1 to override")

// ValidateSecureURL enforces the transport posture: https:// always ok; a
// non-http(s) scheme is ErrInvalidURLScheme; http:// is ErrInsecureURL unless
// allowInsecure is set.
func ValidateSecureURL(rawURL string, allowInsecure bool) error {
	switch {
	case strings.HasPrefix(rawURL, "https://"):
		return nil
	case strings.HasPrefix(rawURL, "http://"):
		if allowInsecure {
			return nil
		}
		return fmt.Errorf("%w (url %q)", ErrInsecureURL, rawURL)
	default:
		return fmt.Errorf("%w: %q (must be http:// or https://)", ErrInvalidURLScheme, rawURL)
	}
}
```

Thread `allowInsecure` through `validateProfiles(f *File, allowInsecure bool)` (call `ValidateSecureURL` per profile), and through `Load`/`Save`. Keep the existing public signatures working by default-refuse:

```go
func Load(path string) (*File, error)              { return LoadInsecure(path, false) }
func Save(path string, f *File) error              { return SaveInsecure(path, f, false) }
func LoadInsecure(path string, allowInsecure bool) (*File, error) { /* existing body; validateProfiles(f, allowInsecure) */ }
func SaveInsecure(path string, f *File, allowInsecure bool) error  { /* existing body */ }
```

> Update the package doc (lines 22-24) and the rationale comment (lines 233-238) to state the new posture: "https:// only; http:// refused unless the caller opts into insecure transport."

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/cli/config/...`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/cli/config/config.go internal/cli/config/config_test.go
git commit -m "feat(cli): refuse plaintext http:// by default; add insecure opt-in (G19)"
```

---

### Task G19.2: Add `--insecure` / `ACH_INSECURE` to login, hydrate, config; replace warnings with refusal

**Files (modify):**
- `cmd/ach-cli/cmd/login.go:176-179` — replace the warn block; register `--insecure`; read `ACH_INSECURE`.
- `cmd/ach-cli/cmd/hydrate.go:433-437` — same; flag registered near line 257-269; env read near 364-369.
- `cmd/ach-cli/cmd/config.go:402-405` (`runConfigAdd`) — same; pass `allowInsecure` into the save path (`SaveInsecure`).
- Tests: `cmd/ach-cli/cmd/login_test.go`, `cmd/ach-cli/cmd/hydrate_test.go`, `cmd/ach-cli/cmd/config_add_test.go`.

**Step 1: Write failing tests**

```go
// login_test.go
func TestLogin_RefusesHTTP_ByDefault(t *testing.T) {
	_, stderr, code, _ := executeLogin(t, "http://localhost:8080")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "ACH_INSECURE") {
		t.Fatalf("stderr should cite the opt-in, got %q", stderr)
	}
}
func TestLogin_AllowsHTTP_WithInsecureFlag(t *testing.T) {
	// with --insecure the scheme gate passes (assert it does NOT exit on scheme;
	// follow the existing executeLogin harness for how far the flow proceeds).
}
func TestLogin_AllowsHTTP_WithInsecureEnv(t *testing.T) {
	t.Setenv("ACH_INSECURE", "1")
	// ... assert the scheme gate passes
}
```

Add analogous `hydrate_test.go` tests — mirror the existing `TestHydrate_PK_MissingEnvironment_Exit1_NoHTTP` pattern to assert **no HTTP call is made** when the URL is refused. Add a `config_add_test.go` test that `config add` with `http://` refuses unless `--insecure`.

**Step 2: Run tests to verify they fail**

Run: `./scripts/dev.sh go test ./cmd/ach-cli/...`
Expected: FAIL — http is still accepted with a warning.

**Step 3: Implement**

In each command, resolve `allowInsecure := flagInsecure || isTruthy(os.Getenv("ACH_INSECURE"))` and replace the `strings.HasPrefix(url, "http://")` warning block with:

```go
if err := config.ValidateSecureURL(url, allowInsecure); err != nil {
	return &exit.CodedError{Code: exit.General, Msg: err.Error(), Wrapped: err}
}
```

Register the flag in each `newXxxCmd()` factory (mirror `--no-warnings`):

```go
cmd.Flags().BoolVar(&flagInsecure, "insecure", false,
	"Allow a plaintext http:// Hub URL (credentials sent unencrypted; localhost still requires this)")
```

Add a small helper (e.g. in `cmd/ach-cli/cmd` or reuse one if present):

```go
func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	}
	return false
}
```

For `runConfigAdd`, also switch the save call to `config.SaveInsecure(path, f, allowInsecure)`.

**Step 4: Run tests**

Run: `./scripts/dev.sh go test ./cmd/ach-cli/...`
Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/ach-cli/cmd/login.go cmd/ach-cli/cmd/hydrate.go cmd/ach-cli/cmd/config.go cmd/ach-cli/cmd/login_test.go cmd/ach-cli/cmd/hydrate_test.go cmd/ach-cli/cmd/config_add_test.go
git commit -m "feat(cli): wire --insecure/ACH_INSECURE through login/hydrate/config (G19)"
```

---

### Task G19.3: Fix stale comments; document HTTPSource concession; update e2e/dev for the localhost refusal

**Files (modify):**
- `internal/cli/doc.go:17` — change "HTTPS-only refusal" to reflect "https:// by default, http:// only with explicit insecure opt-in."
- `internal/sources/http/fetcher.go:10-16` and `internal/sourceserr/errors.go:41` and `api/ach/v1alpha1/external_ref_types.go:308-329` — **docs-only** (HTTPSource decision D): make the comments say plainly "http:// is a dev/e2e-only concession; production must use https:// by convention (not machine-enforced)." No behavior change.
- ⚠ **e2e + local-dev**: grep every place the CLI is invoked against `http://localhost:8080` and add `--insecure` (or export `ACH_INSECURE=1`). Likely: `test/e2e/` CLI invocations, `references/local-testing-gateway.md`, `examples/README.md`, any `Makefile` demo targets. Without this, e2e/login demos will now exit 1.
- `docs/external-review-resolutions.md` §G19 — append a landed note.

**Step 1:** Grep for the affected localhost invocations.

Run: `./scripts/dev.sh grep -rn "localhost:8080" test/ examples/ references/ Makefile scripts/`
Expected: a list of CLI call sites + docs to update with `--insecure`/`ACH_INSECURE=1`.

**Step 2:** Update each comment and each localhost CLI invocation/doc.

**Step 3: Verify e2e CLI path still logs in** (the heaviest check — only if you touched e2e CLI calls).

Run: `make e2e-focus FOCUS="login"` (adjust focus to a login/hydrate subtest), or `make e2e-full`
Expected: PASS — login/hydrate succeed against the local gateway with the insecure opt-in.

**Step 4: Commit**

```bash
git add internal/cli/doc.go internal/sources/http/fetcher.go internal/sourceserr/errors.go api/ach/v1alpha1/external_ref_types.go test/ examples/ references/local-testing-gateway.md docs/external-review-resolutions.md
git commit -m "docs(cli): fix stale https comments; mark HTTPSource http:// dev-only; add --insecure to local flows (G19)"
```

---

# Task Group G5 — Wire `hydrate --sync` (STATE-05 composition)

**Why:** `ach-cli env hydrate --sync` is a silent no-op. The step-11 prune calls `syncFn(existingState, existingState, …)` (`internal/cli/hydrate/commit.go:561`) — passing the old state as **both** `prev` and `newFile`, so the set-difference is always empty and nothing is pruned. Stale, possibly credential-bearing adapter files are left on disk while the command reports success. Fix: pass the **composed next-state** as `newFile`.

**Hard constraint:** Preserve the `maybeKill(11)` SIGKILL-injection boundary. The composition must be a **pure, no-I/O** computation done after `maybeKill(11)` (commit.go:553) and before `syncFn` (561); the state write must remain inside step 12 (`step12WriteState`, called at 581). The e2e test `sc2_commit_sequence_sigkill` (`test/e2e/cli_hydrate_engine_test.go:558`) asserts `state.json` is byte-identical after a SIGKILL at step 11 — i.e. step 12 never ran.

**Acceptance criterion:** After hydrating an Environment that contains resource B, re-hydrating with `--sync` against a manifest that no longer contains B prunes B's projected files; the `sc2` SIGKILL boundary is unchanged.

---

### Task G5.1: Extract a pure `composeNextState` helper from `step12WriteState`

**Files (modify):**
- `internal/cli/hydrate/commit.go:1039-1098` — split `step12WriteState`. Read this function first: it builds `next *state.File` (lines 1045-1089) and then saves it.

**Step 1: Write the failing test**

```go
// internal/cli/hydrate/commit_test.go
func TestComposeNextState_IsPure_AndReflectsRender(t *testing.T) {
	// existing state has plugins A and B
	existing := &state.File{
		SchemaVersion: "3",
		Plugins: []state.FileEntry{
			{Target: "pluginA"}, {Target: "pluginB"},
		},
	}
	// build a commit whose render/manifest contains ONLY pluginA (dropped B)
	c := newTestCommitWithRender(t /* render carrying only pluginA */)

	next := c.composeNextState(existing /* + same args step12WriteState takes */)

	// 1. purity: composing must not write state.json (no stateStore.Save call)
	if c.stateStore.(*fakeStore).saveCount != 0 {
		t.Fatal("composeNextState performed I/O — must be pure")
	}
	// 2. correctness: the composed next-state must NOT contain the dropped pluginB
	if containsTarget(next.Plugins, "pluginB") {
		t.Fatal("dropped resource still present in composed next-state")
	}
	if !containsTarget(next.Plugins, "pluginA") {
		t.Fatal("surviving resource missing from composed next-state")
	}
}
```

> Adapt the harness to the existing `commit_test.go` helpers (`newCommit`/fakes). The point is two assertions: **no save** during compose, and **dropped entries absent** from the composed result. Match `composeNextState`'s parameter list to `step12WriteState`'s exactly (`existing, m, render, adapterRan, platformID, runtimeFiles`).

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/cli/hydrate/ -run TestComposeNextState`
Expected: FAIL — `composeNextState` undefined.

**Step 3: Implement the extraction**

Move the `next` construction (current lines 1045-1089) into a new pure method; leave the `Save` in `step12WriteState`:

```go
// composeNextState builds the post-hydrate state.File in memory. PURE — no I/O.
// Used both by step 11 (--sync prune target) and step 12 (the persisted state),
// guaranteeing the sync newFile and the saved state are identical.
func (c *commit) composeNextState(existing *state.File, m <manifestType>, render <renderResultType>, adapterRan bool, platformID string, runtimeFiles []state.FileEntry) *state.File {
	next := &state.File{SchemaVersion: "3", Environment: c.opts.Environment}
	if existing != nil {
		next.Profile = existing.Profile
		next.Prompts = existing.Prompts
		next.Plugins = existing.Plugins
		next.Artifacts = existing.Artifacts
		next.Skills = existing.Skills
		next.RuntimeFiles = existing.RuntimeFiles
		next.Adapter = existing.Adapter
	}
	if adapterRan {
		next.Adapter = adapterSectionFromRender(platformID, render)
	}
	if adapterRan && !c.opts.OnlyRuntime {
		next.Plugins = pluginsSectionFromRender(render)
		next.Skills = skillsSectionFromRender(render)
	}
	next.RuntimeFiles = runtimeFiles
	return next
}

func (c *commit) step12WriteState(existing *state.File, m <manifestType>, render <renderResultType>, adapterRan bool, platformID string, runtimeFiles []state.FileEntry) error {
	next := c.composeNextState(existing, m, render, adapterRan, platformID, runtimeFiles)
	return c.stateStore.Save(next) // existing save body
}
```

> Copy the real parameter types from the current `step12WriteState` signature; the `<manifestType>`/`<renderResultType>` above are placeholders.

**⚠ Pruning-coverage note (the key correctness risk):** `composeNextState` carries `existing.Prompts`/`existing.Artifacts` forward unchanged when the render does not recompose them. If the failing acceptance test in **G5.3** shows a dropped *prompt/artifact* is not pruned, extend `composeNextState` to rebuild those buckets from the fresh manifest `m`/render (so dropped entries are absent → pruned). Plugins/Skills/Adapter are already recomposed from render. Resolve this against how prompt/artifact state entries are derived during this hydrate.

**Step 4: Run test to verify it passes**

Run: `./scripts/dev.sh go test ./internal/cli/hydrate/ -run TestComposeNextState`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/cli/hydrate/commit.go internal/cli/hydrate/commit_test.go
git commit -m "refactor(hydrate): extract pure composeNextState from step12 (G5)"
```

---

### Task G5.2: Feed the composed next-state to the step-11 `Sync`

**Files (modify):**
- `internal/cli/hydrate/commit.go:549-578` — the step-11 block.

**Step 1: Write the failing test**

Extend `commit_test.go` to capture the `syncFn` args (use the existing `syncFn` package seam at commit.go:628). Assert `newFile != prev` and that `newFile` omits the dropped entry:

```go
func TestRun_Step11Sync_PassesComposedNextState(t *testing.T) {
	var gotPrev, gotNew *state.File
	orig := syncFn
	syncFn = func(prev, newFile *state.File, achDir, toolRoot string, opts SyncOptions) (SyncStats, error) {
		gotPrev, gotNew = prev, newFile
		return SyncStats{}, nil
	}
	t.Cleanup(func() { syncFn = orig })

	// existing has A and B; render/manifest has only A; opts.Sync = true
	// ... drive c.run()

	if gotPrev == gotNew {
		t.Fatal("prev and newFile are the same pointer — the no-op bug")
	}
	if containsTarget(gotNew.Plugins, "pluginB") {
		t.Fatal("composed newFile still contains the dropped resource")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `./scripts/dev.sh go test ./internal/cli/hydrate/ -run TestRun_Step11Sync_PassesComposedNextState`
Expected: FAIL — both args are still `existingState`.

**Step 3: Implement the call-site fix**

Replace the buggy block (keeping `maybeKill(11)` at line 553 exactly where it is, BEFORE the compose/sync):

```go
c.maybeKill(11) // SIGKILL boundary — stays here, before any compose/sync (sc2 invariant)
if c.opts.Sync && !c.opts.DryRun {
	// STATE-05: prune state entries (and their files) for resources dropped from
	// the Environment. composeNextState is pure (no I/O), so state.json is still
	// untouched at the maybeKill(11) point above.
	composed := c.composeNextState(existingState, m, renderResult, adapterRan, result.PlatformID, runtimeFiles)
	stats, err := syncFn(existingState, composed, c.achDir, c.toolRoot, SyncOptions{
		Force:  c.opts.Force,
		Stderr: c.opts.Stderr,
	})
	if err != nil {
		// preserve the existing error handling here
	}
	result.FilesPruned += stats.Pruned
	result.FilesPreserved += stats.Preserved
}
```

Delete the `TODO(STATE-05 composition)` comment (lines ~555-560) — it is now resolved.

**Step 4: Run the full hydrate package + the existing sync tests**

Run: `./scripts/dev.sh go test ./internal/cli/hydrate/...`
Expected: PASS — including `TestRun_Step11Sync_InvokedWhenSyncOptSet`, `..._NotInvokedWhenSyncOptUnset`, `..._NotInvokedUnderDryRun`, and the `TestSync_*` algorithm tests in `wiring_test.go` (unchanged — `Sync` itself was not touched).

**Step 5: Commit**

```bash
git add internal/cli/hydrate/commit.go internal/cli/hydrate/commit_test.go
git commit -m "fix(hydrate): pass composed next-state to --sync prune (G5)"
```

---

### Task G5.3: End-to-end prune verification + SIGKILL-boundary regression + docs

**Files:**
- Modify: `cmd/ach-cli/cmd/hydrate.go` — confirm the `--sync` flag help (line ~269) no longer implies a stub.
- Modify: spec/docs — `docs/external-review-resolutions.md` §G5 landed note; update the CLI spec `--sync` entry from stub → delivered (the decision doc cites `ach-cli-spec-final-delivered.md`; locate the `--sync` section and flip it).

**Step 1: Add an end-to-end prune test** (proves the acceptance criterion against the **real** `Sync`, on disk). Prefer a hydrate-engine test in `cmd/ach-cli/cmd/hydrate_test.go` using the existing `executeHydrateEngine` + `newHydrateMock` harness (TLS mock — combine with `--insecure` only if the harness uses http):
- Hydrate an env whose manifest yields projected files for plugins A and B → assert both files exist.
- Re-hydrate the same env with `--sync` against a manifest that yields only A → assert B's projected file is **gone** and A's remains.

If a hydrate-engine harness cannot drive two manifests easily, fall back to driving `commit.run()` directly with a real temp `achDir`/`toolRoot` and the real `syncFn` (do NOT swap the seam), seeding on-disk files for A and B.

**Step 2: Run the prune test**

Run: `./scripts/dev.sh go test ./cmd/ach-cli/cmd/... -run Sync`
Expected: PASS — B pruned, A preserved. If a dropped *prompt/artifact* is not pruned, return to **G5.1** and recompose that bucket from the manifest.

**Step 3: Verify the SIGKILL boundary is intact** (the hard constraint).

Static check — the kill must still sit before the sync:

Run: `./scripts/dev.sh grep -n "maybeKill(11)" internal/cli/hydrate/commit.go`
Expected: a single hit on the line BEFORE the `composeNextState`/`syncFn` block (the line the `sc2` test's error message references).

Then run the e2e regression (needs a cluster; local-only gate):

Run: `make e2e-focus FOCUS="sc2_commit_sequence_sigkill"`
Expected: PASS — `state.json` byte-identical after the step-11 SIGKILL (step 12 never ran). This proves `composeNextState` did not leak any I/O into step 11.

**Step 4: Commit**

```bash
git add cmd/ach-cli/cmd/hydrate.go cmd/ach-cli/cmd/hydrate_test.go docs/external-review-resolutions.md
git commit -m "test(hydrate): e2e prune for --sync; doc STATE-05 delivered (G5)"
```

---

# Final verification (all groups)

**Step F.1: Lint the touched packages**

Run: `make qa-lint-changed`
Expected: clean.

**Step F.2: Full unit sweep**

Run: `make test-unit`
Expected: PASS.

**Step F.3: DB integration (G3) — if docker available**

Run: `./scripts/dev.sh go test -tags=integration ./internal/db/...`
Expected: PASS.

**Step F.4: E2E final gate** (mandatory — G3 touches forwarder/platform-api, G5 touches the hydrate engine, G19 touches the CLI used by e2e; all are in the "must run e2e" surface per CLAUDE.md).

Run: `make e2e-full`
Expected: PASS. Cluster is kept up on pass or fail; `make cluster-down` to reclaim.

**Step F.5: Pre-push gate** (host-only; the installed hook fires it — do not run by hand unless pushing after a `--no-verify`).

The 18 gates include SPDX headers on the new `internal/keycrypt/*.go` files, `go mod tidy` drift, full lint, unit, and chart CRD drift. Fix root causes; never `--no-verify`.

---

## Notes for the executor
- **TDD throughout** (superpowers:test-driven-development): every behavioral change has a test written first that fails for the right reason.
- **Surgical changes** (CLAUDE.md): touch only what each task names; do not refactor adjacent code.
- **No new dependencies** — G3 uses only `crypto/aes`+`crypto/cipher`+`crypto/rand`+`encoding/base64` (stdlib).
- **Never log secrets**: not `keyResp.Key`, not the sealed blob, not decrypted material, not the DEK.
- **Group independence**: G3, G19, G5 share no files and can be committed/reviewed independently.
