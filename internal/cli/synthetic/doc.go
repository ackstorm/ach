// SPDX-License-Identifier: Apache-2.0

// Package synthetic centralizes the CLI-07 + spec §3.3 enforcement of
// "synthetic mode" (ACH_BASE_URL + a resolved credential) across every
// `ach` subcommand.
//
// Synthetic mode lets CI / container environments run ach without a
// disk-resident ~/.config/ach/config.yaml by sourcing the Hub URL from
// ACH_BASE_URL and the bearer from ACH_API_KEY (or the equivalent
// --api-key flag). Once active, four invariants hold:
//
//   - `ach login`, `ach logout`, `ach config *` are unavailable
//     (config-mutating; no disk registry to mutate). Exit 1.
//   - `ach env-keys create` requires --no-save (D-08). Exit 1 without.
//   - --deployment / ACH_DEPLOYMENT are rejected on every subcommand
//     (the deployment-resolution chain bypasses disk entirely; the
//     conceptual deployment is named "(env)"). Exit 1.
//   - --env-key / ACH_ENV_KEY are rejected on every read-side command
//     (hydrate, whoami, env list/describe, env-keys list/revoke) —
//     ek_ labels can only be dereferenced against the on-disk EK map,
//     which synthetic mode has no access to (CLI-09). Exit 1.
//
// Half-set mode (ACH_BASE_URL set but NO credential resolves) is a
// distinct error state — exit 1 with the half-set message — so a user
// who set only one of the two env vars never falls back to bare-mode
// disk-config silently. See CLI-07 / T-06-07-01.
//
// The package exports:
//
//   - SyntheticDeploymentLabel = "(env)" — the constant the Phase 7
//     state.json writer records as the deployment name when synthetic
//     mode is active. Surfaced here so callers across phases agree on
//     the literal string.
//   - Gate (typed int) + GateLogin/GateLogout/GateConfig/
//     GateEnvKeysCreate/GateHydrate/GateWhoami/GateEnvList/
//     GateEnvDescribe/GateEnvKeysList/GateEnvKeysRevoke/GateAdmin —
//     closed-enum tags that subcommands pass to GuardCommand to declare
//     their disposition under synthetic mode.
//   - Params — the resolved flag-value bag a caller passes alongside
//     its Gate. Env vars are read via Getenv (default os.Getenv;
//     overridable in tests).
//   - IsActive(Params) bool — pure predicate.
//   - IsHalfSet(Params) bool — pure predicate.
//   - GuardCommand(Params) error — composite check. Returns
//     *exit.CodedError when the invocation must be rejected; nil when
//     it is OK to proceed.
//
// Spec / requirement anchors:
//   - spec/ach_cli_spec_v20260515_FINALv4.md §3.3 (synthetic mode
//     definitive contract).
//   - .planning/REQUIREMENTS.md CLI-07 / CLI-08 / CLI-09.
//   - 06-CONTEXT.md D-07 (env-keys-create always-persist), D-08
//     (synthetic --no-save mandate), D-11 (mutex credentials).
package synthetic
