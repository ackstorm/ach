# Task 5 Report: DELETE /platform/keys/{key_id}

## Final resolved route path

`DELETE /platform/keys/{key_id}` — confirmed by inspection of:
- `internal/platformapi/server.go:178`: `envkeys.MountKeys(r, envkeysDeps)` is called on the Authn-gated group router `r`.
- `internal/platformapi/envkeys/mount.go`: `MountKeys` now uses `r.Route("/platform/keys", func(r chi.Router) { r.Get("/", ...) r.Delete("/{key_id}", ...) })`.
- `GET /platform/keys` (ListAllHandler) resolves via the inner `r.Get("/", ...)`.
- `DELETE /platform/keys/{key_id}` (RevokePersonalHandler) resolves via `r.Delete("/{key_id}", ...)`.
- No collision with `DELETE /platform/env-keys/{key_id}` — that route is registered by `envkeys.Mount` under the separate `/platform/env-keys` subrouter prefix.

## Active-key guard

**Implemented.** `middleware.KeyContext.KeyID` (set by the Authn middleware from the resolved `keystore.KeyInfo.KeyID`) IS the caller's own authenticating key id. It is compared to the target `{key_id}`; a match without `?force=true` returns `409 cannot_revoke_active_key`. With `?force=true`, the revocation proceeds normally.

No auth-middleware refactoring was required — `KeyID` was already available on `keyCtx`.

## keyCtx exposes the caller's own key id

Yes. `middleware.KeyContext.KeyID` (field added in earlier work) is the `pkid_`/`ekid_` key id of the bearer token used to authenticate the request. It is populated by `middleware.WithKeyContext` from `keystore.KeyInfo.KeyID` and read back via `middleware.KeyContextFromCtx`. The guard uses `keyCtx.KeyID == keyID`.

## Audit actions/outcomes emitted

| Condition | Action | Outcome |
|-----------|--------|---------|
| Active-key guard fires (no force) | `ActionPkRevoke` | `cannot_revoke_active_key` |
| DB sentinel (wrong owner / not found) | `ActionPkRevoke` | `OutcomeNotKeyOwner` |
| DB error | `ActionPkRevoke` | `OutcomeInternalError` |
| Success, LiteLLM reachable | `ActionPkRevoke` | `OutcomeRevoked` |
| Success, LiteLLM unreachable | `ActionPkRevoke` | `OutcomeLitellmUnreachable` |

## Files changed

- `internal/db/personal_keys_revoke.go` — copied from `feat/keys-cli-ux` branch (already built there as Task 1/2 prerequisite; worktree was cut from `main` which lacked it).
- `internal/db/personal_keys_revoke_test.go` — copied from `feat/keys-cli-ux` branch.
- `internal/platformapi/envkeys/handler.go` — added `RevokePersonalKeyByOwner` to the `dbOps` interface.
- `internal/platformapi/envkeys/handler_test.go` — added stub `RevokePersonalKeyByOwner` to `fakeEkDB` so it still satisfies the updated interface.
- `internal/platformapi/adapters.go` — added `RevokePersonalKeyByOwner` method to `envkeysDBAdapter`.
- `internal/platformapi/envkeys/mount.go` — changed `MountKeys` from a single `r.Get` to `r.Route("/platform/keys", ...)` to co-locate GET and DELETE on the same subrouter.
- `internal/platformapi/envkeys/revoke_personal.go` — new handler.
- `internal/platformapi/envkeys/revoke_personal_test.go` — new tests (6 cases).

## Test results

```
ok  github.com/ackstorm/ach/internal/platformapi/envkeys   0.007s
ok  github.com/ackstorm/ach/internal/platformapi           0.084s
ok  github.com/ackstorm/ach/internal/platformapi/admin     0.048s
... (all 13 platformapi sub-packages pass)
```

Test cases in `TestRevokePersonalHandler`:
1. `own active key → 200 DB row flipped` — happy path, DB called, 200 returned.
2. `wrong owner → 404 no existence leak` — sentinel maps to 404 `key_not_found`.
3. `active key without force → 409` — guard fires, DB NOT called.
4. `active key with force=true → 200` — guard bypassed, DB called.
5. `ekid_ prefix → 400 redirect message` — prefix check before DB.
6. `litellm unreachable → still 200 (DB-first)` — WARN-04 invariant holds.

## Concerns / follow-ups

- The `personal_keys_revoke.go` (and test) file had to be copied from the `feat/keys-cli-ux` branch since this worktree was cut from `main` which predates that file. When this branch is merged / rebased onto `feat/keys-cli-ux`, the db file will already exist and the copy should be reconciled (no duplicate — same content).
- The `cannot_revoke_active_key` audit outcome string is not in the closed `audit.Outcome*` enum (that enum is for Hub §18.2 outcomes). It is emitted as a raw string. If the enum needs to be extended, add `OutcomeCannotRevokeActiveKey = "cannot_revoke_active_key"` to `internal/audit/events.go`.
