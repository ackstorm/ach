// SPDX-License-Identifier: Apache-2.0

// Package bip is the Forwarder-side request-time lookup helper for
// BackendIdentityPolicy CRs (Hub §9.3 / FWD-05 / OP-16 / TODO.md §6).
//
// Two exported entry points + one constant:
//
//   - TargetIndexKey — the controller-runtime field-indexer key
//     ("spec.target") used to look up BIPs by their target {kind, name}
//     tuple. The indexed string format is "<kind>/<name>".
//   - RegisterIndex — installs the field indexer on a controller-runtime
//     Manager. MUST be called BEFORE the first GetInformer call on
//     BackendIdentityPolicy (controller-runtime requirement).
//   - ResolveWinner — request-time lookup. Returns the alphabetically-LAST
//     matching CR (by metadata.name ASC sort, then Items[len-1]); nil when
//     zero BIPs match OR when the winner has spec.forwardIdentityJWT=false
//     (explicit opt-out, equivalent to no policy).
//
// Per TODO.md §6 (feedback_bip_no_shadow_logic.md) the Operator stays
// dumb on BIP duplicates: there is NO Synced=DuplicateTarget reason, NO
// shadow-flip reconciler, NO Operator-side precedence logic. Multiple
// BIPs targeting the same (kind, name) coexist; the Forwarder resolves
// the duplicate at READ time per the alpha-LAST contract. Operators flip
// precedence by renaming CRs (a "zz-" prefix on metadata.name makes the
// rename the alpha-LAST winner — see B9 in index_test.go).
//
// Per OP-16, ResolveWinner reads spec.target + spec.forwardIdentityJWT
// ONLY; it MUST NOT read .Status (Operator is the sole status writer
// and runtime authority is decoupled from status-write latency).
package bip
