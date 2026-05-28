// SPDX-License-Identifier: Apache-2.0

// Package cli holds the shared internals every `ach` cobra subcommand
// imports — yaml multi-deployment config registry (CLI spec §3.2), the
// outbound HTTP client that carries the `x-ach-key` header into the
// Platform API, the §15.5 error-envelope decoder, the redaction helper
// for `--verbose` header dumps, and the typed exit-code matrix from
// §9.3. The package itself is intentionally empty (file-level doc
// only) — concrete types live in the leaf subpackages `config`,
// `httpclient`, and `exit`. Each leaf is import-cycle-free: `config`
// and `httpclient` are root leaves; `exit` imports `httpclient` so it
// can map *ServerError to an exit Code in one place.
//
// The on-disk config file at `~/.config/ach/config.yaml` is the local
// trust artifact Hub §15.4 authorizes to hold pk_/ek_ plaintext at
// mode 0600. The discipline that keeps that posture honest — file
// mode, atomic rename, HTTPS-only refusal — lives in `config`.
package cli
