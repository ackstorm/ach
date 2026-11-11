# Known gaps and accepted trade-offs

Limitations ACH accepts today. Read before reporting one as a bug or proposing
to close it; `ROADMAP.md` says which ones are scheduled.

## Accepted trade-offs

| Gap | Effect | Notes |
|-----|--------|-------|
| E2E does not gate pull requests | A PR can merge with a red `e2e.yml` | `make release-cut` waits for E2E on the release PR; run `make e2e-full` locally before merging a change to the e2e surfaces. |
| Environment authoring is admin-only | No per-object ACLs, no team self-service | Writing a content CR or a `LiteLLMConnection` is equivalent to reading every Secret in the ACH namespace (`authSecretRef` resolves with the operator's identity), so writers need admin-tier trust. |
| UI write path is Environment-only | The console drafts Environments, no other kind | GitOps wins: the operator takes a draft over on CR apply. |
| Content service HA needs RWX | `contentService.standalone=true` needs a `ReadWriteMany` `operator.cache` | Default is the sidecar on an RWO volume. |
| LISTEN/NOTIFY is at-most-once | A lost notification leaves a reader's cache stale until the 5-minute refresh | Applies to the forwarder's BIP and Environment caches; key revocation is re-checked every 60 s independently. |
| Postgres backup and restore undefined | `pk_`/`ek_` rows and the catalog cannot be re-derived from the CRDs | Back up the ACH database like any Postgres; ACH ships no procedure. |
| Guardrails cover `ek_` traffic only | A `pk_` (human CLI) reaches the same models through the access group, which carries no guardrails | Team-scoped guardrails also need a LiteLLM Enterprise licence. |
| Budgets are enforced after the fact | The request that crosses a ceiling is served; the next is refused | LiteLLM prices a request after it completes. |
| Only git sources can be pinned | `s3`, `gcs` and `http` content always serves the latest object; marketplace entries from other repos follow their own `ref` unless the catalogue lists a `sha` | Those sources cannot fetch an older revision on demand; `spec.<git>.commit` pins git sources and a marketplace's own entries. |
| Leaver detection depends on the Dex connector | `saml`, `oauth`, `keystone`, `authproxy` never ask the IdP on refresh | Offboarding for those is `ach-cli admin keys revoke --owner <email>`. |

## Gotchas

- Never delete an ACH-owned LiteLLM user by hand: LiteLLM deletes that user's
  keys with it, including the oauth `pk_`'s backing key.
- ACH creates LiteLLM users with `auto_create_key: false` and
  `user_id` = the e-mail (`internal/platformapi/auth/sso.go`); without it `/user/new`
  mints an unmanaged default key per user.
- `ach-cli` hydrate keeps no plugin disk cache, by design: every hydrate
  fetches from content-service. Do not add one.
