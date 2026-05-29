// SPDX-License-Identifier: Apache-2.0

// Package state owns <ach-dir>/state.json — the CLI's local state
// ledger per CLI spec §8.2. Schema v2 (verbatim per D-13 — clean break,
// no v1 reader):
//
//	{
//	  "schemaVersion": "2",
//	  "environment":   "engineering-prod",
//	  "deployment":    "ackstorm-prod",
//	  "prompts":       [{target, hash, sourceHash, merge?, keys?}],
//	  "plugins":       [{target, hash, sourceHash, merge?, keys?}],
//	  "artifacts":     [{target, hash, sourceHash, merge?, keys?}],
//	  "runtimeFiles":  [{target, hash, sourceHash, merge?, keys?}],
//	  "adapter": {
//	    "id":    "claude-code",
//	    "files": [{target, hash, sourceHash, merge?, keys?}]
//	  }
//	}
//
// Discipline (mirrors internal/cli/config, internal/cachefs,
// internal/credhash):
//
//   - stdlib + encoding/json only — NO yaml, NO log, NO log/slog.
//   - Atomic publication via tmp + fsync(fd) + rename(2) +
//     fsync(parent_dir) in the same dir per §8.7 STATE-07 contract
//     (TOCTOU-safe; survives kernel/SIGKILL crashes between Write
//     and Rename).
//   - schemaVersion != "2" → ErrSchemaMismatch (CLI spec §8.2 +
//     §9.3 exit code 5). No v1 reader code ships per D-13.
//   - Same-<ach-dir> different-Environment → ErrEnvironmentGuard
//     (CLI spec §8.3 + §9.3 exit code 4) — see guard.go.
//   - tmp/ sweep is unconditional at hydrate start per D-02 + spec
//     §6.7 step 2 — see sweep.go.
//   - File mode 0644 (state carries no plaintext secrets — unlike
//     ~/.config/ach/config.yaml which is 0600).
//
// The caller layer (cmd/ach-cli/cmd/hydrate.go and the W1-06 commit
// orchestrator) maps ErrSchemaMismatch → exit 5 and ErrEnvironmentGuard
// → exit 4 through *exit.CodedError. This package does not import
// internal/cli/exit — error sentinels stay at the data layer.
package state
