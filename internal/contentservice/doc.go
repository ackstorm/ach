// SPDX-License-Identifier: Apache-2.0

// Package contentservice implements the ACH Content Service HTTP
// surface (Hub §15.2 / §15.6). Post-Plan-05-05 (D-16 rewrite) it
// serves three authenticated routes plus health:
//
//	GET /healthz
//	GET /content/prompt/{name}     -> bytes from prompts.content_type
//	                                  projection column (else
//	                                  application/octet-stream)
//	GET /content/plugin/{name}     -> .tar.gz, Content-Type:
//	                                  application/gzip
//	GET /content/artifact/{name}   -> scope-dispatched: object → bare
//	                                  bytes with application/octet-
//	                                  stream; directory → .tar.gz
//	                                  with application/gzip
//
// Files live under ACH_CACHE_ROOT (default /var/cache/ach) on the RWO
// PVC mounted by the operator Pod that this container shares.
//
// §15.6 pipeline (D-04 7-gate, cheaper-first):
//   1. resolveAuthn      — x-ach-key + keystore.Resolver
//   2/3. resolveEnv      — header policy (pk_ required / ek_ bound) +
//                          envcache.Cache.Get
//   4. enforceTeams      — pk_ only; TeamsResolver intersection
//   5. enforceAllowlist  — name ∈ envRow.Context<Kind> (CHEAPER-FIRST
//                          divergence vs spec §15.6 v10 fix step
//                          order; see pipeline.go doc comment for
//                          rationale + audit-outcome implication)
//   6. resolveContent    — kind-dispatched projection-row lookup;
//                          plugin uses §12.3 CTE
//                          (db.ResolvePluginByName)
//   7. checkStaleness    — now - LSR > MaxStaleness OR LSR == NULL →
//                          503 stale_cache_expired
//   8. file open (D-02)  — os.Open EARLY (inode-pin) → stream.go
//
// HTTP surface invariants (CS-06 / CS-08 / D-01):
//   - Auth required: pk_ (with x-ach-environment header) or ek_
//     (header optional, must match bound env when present)
//   - Cache-Control: no-store (changed from public, max-age=300 in
//     Plan 05-05 drift flag #3 closure)
//   - Identity transfer: Content-Length is exact from f.Stat().Size();
//     no chunked encoding, no compression middleware
//   - Range / If-None-Match / If-Modified-Since / If-Match /
//     If-Unmodified-Since headers are explicitly IGNORED — every
//     successful response is 200 OK with the full body
//   - Body is streamed via io.Copy → *os.File.WriteTo, which on Linux
//     engages sendfile(2) into the response's TCP socket. http.
//     ServeContent is deliberately NOT used (would re-introduce Range
//     handling).
//
// Audit + metrics:
//   - Every request emits exactly one audit event via
//     audit.EmitAudit with Action=content.get and Outcome matching
//     the response body code (D-03 table) or "forwarded" on success.
//   - content_service_requests_total{kind,outcome},
//     _request_duration_seconds{kind}, and _bytes_served_total{kind}
//     are populated on every request. Cardinality budget per §18.5
//     OBS-06: NO request_id, NO owner_email labels.
//   - litellm_unreachable_total{caller=content_service} is
//     incremented on every transport-failure TeamsResolver miss in
//     enforceTeams (shared CounterVec via metrics.
//     MustRegisterLitellmUnreachable).
package contentservice
