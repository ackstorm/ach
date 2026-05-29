// SPDX-License-Identifier: Apache-2.0

// Package manifest decodes the POST /platform/hydrate response into a
// typed *Manifest, asserts schemaVersion == "v1alpha1", and asserts
// both runtime and context blocks are present. STATE-09 + CLI spec
// §6.2 + Hub spec §15.2.
//
// Boundary discipline per 07-CONTEXT.md D-09 + Claude's Discretion:
// this package is strict POST + decode + version-assert only. NO
// scope-filter logic — `--include-runtime` / `--only-runtime` belong
// to the hydrate orchestrator (07-W1-06) which calls Fetch once per
// `hydrate.Run` invocation (commit-sequence step 5).
//
// ContentRef is a single struct serving BOTH runtime and context
// entries from the Hub response (see examples/hydrate.json):
//
//   - runtime.{models,mcpServers,a2aAgents}[*] populate Endpoint
//   - context.{prompts,plugins,artifacts}[*] populate DownloadURL
//
// Both shares ID + Name. The `omitempty` tags on Endpoint and
// DownloadURL keep encoded output minimal and round-trip clean against
// examples/hydrate.json — context entries decode with Endpoint == ""
// and runtime entries decode with DownloadURL == "". Adapters (ADAPT-03,
// 07-W3-01..04) consume m.Runtime.MCPServers[i].Endpoint to construct
// platform-specific runtime-config URLs without re-fetching.
//
// Discipline (mirrors internal/cli/state — 07-W1-02):
//   - stdlib + encoding/json only — NO yaml, NO log, NO log/slog.
//   - json.Decoder.DisallowUnknownFields(true) — unknown top-level or
//     nested fields are a decode error wrapped via fmt.Errorf so
//     callers see the JSON path. Distinct from ErrSchemaMismatch
//     (which fires only on schemaVersion drift or nil runtime/context).
//   - ErrSchemaMismatch is the sentinel for caller mapping to
//     exit.SchemaMismatch (code 5 — see internal/cli/exit, 07-W1-01).
package manifest
