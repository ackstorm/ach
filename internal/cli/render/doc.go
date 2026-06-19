// SPDX-License-Identifier: Apache-2.0

// Package render is the text-output discipline owner for Phase 6 CLI
// subcommands (Pattern P10). Every cobra RunE that emits human-readable
// output to stdout should compose its body via the Format* functions
// here rather than scattering fmt.Printf calls across cmd/ach/cmd/.
//
// The package is a pure formatter — every function takes typed inputs
// and returns a string. Callers write the result to their own io.Writer
// (cmd.OutOrStdout() in cobra context). NO log, NO log/slog, NO direct
// os.Stderr writes — mirrors the internal/cli/config + internal/keys
// no-logger discipline.
//
// Tabular output uses stdlib text/tabwriter for deterministic spacing.
// Lean wire-shape DTOs (EnvView, KeyRowView, HydrateView, RuntimeItem,
// ContextItem) live here so render does NOT pull internal/platformapi
// (which transitively imports k8s.io/* + chi). The CLI binary stays
// thin — render is consumed by `ach config show`, `ach env list`,
// `ach env describe`, `ach env-keys list` (06-05), and
// `ach admin keys list` (06-08).
//
// W3 wire-shape contract: HydrateView.RuntimeItem.Endpoint surfaces
// the per-runtime `endpoint` key; HydrateView.ContextItem.DownloadURL
// surfaces the per-context-entry `downloadUrl` key. The on-the-wire
// JSON tags on these structs match the server's HydrateResponse field
// names so trivial json.Decoder round-trips work without re-mapping.
//
// W7 sharing contract: FormatKeyList is the single source of truth for
// the keys list table — both 06-05 `ach env-keys list` and 06-08
// `ach admin keys list` call this function, NOT inline tabwriter code.
package render
