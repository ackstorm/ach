// SPDX-License-Identifier: Apache-2.0

// pipeline.go ships the §15.6 D-04 7-gate orchestrator. Each gate
// short-circuits at the first denial and the surviving rows flow
// through to gate 8 (D-02 early open), which returns the open
// *os.File for stream.go to drain.
//
// SPEC DIVERGENCE — read this before touching the gate order:
//
//   Per D-04 / CONTEXT canonical-refs line 27, this pipeline runs the
//   ALLOWLIST gate (gate 5) BEFORE the CONTENT-RESOLUTION gate (gate 6).
//   That inverts the spec §15.6 v10 fix step order, which would resolve
//   the projection row first and only then check the allowlist.
//
//   The divergence is deliberate and user-confirmed (CONTEXT-LOG.md).
//   Rationale: cheaper-first — the allowlist check is a single linear
//   scan over a small (≤ 50-element) in-memory slice; the content
//   resolution arm makes a Postgres roundtrip per request. Running the
//   cheaper gate first denies unauthorized requests without the DB hit.
//
//   SIDE EFFECT: "name not in env.context AND not in any CRD" yields
//   403 unauthorized_content (gate 5 fires), NOT 404 content_not_found
//   (which would only be reachable if gate 5 passed). Audit-dashboard
//   parties care about this distinction — gate-5 firing is "Environment
//   misconfigured" while gate-6 firing is "cache drift / CRD deleted".
//   Both outcomes have distinct audit-event Outcome strings so the
//   distinction is grep-able.
//
//   The TestPipeline_EndToEnd integration suite locks both orderings:
//   the 403-unauthorized_content case uses a name NOT in env.context
//   but PRESENT in the CRD; the 404-content_not_found case uses a name
//   PRESENT in env.context but ABSENT from the CRD/marketplace tables.

package contentservice

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/keystore"
)

// resolvedRow is the value pipeline() hands back on success — the open
// *os.File plus the stat metadata stream() needs and the audit
// attributes serve() emits on the forwarded-success path.
type resolvedRow struct {
	File        *os.File
	Size        int64
	ContentType string
	KeyInfo     *keystore.KeyInfo
}

// pipelineErr bundles an *errResp with the (optional) *keystore.KeyInfo
// resolved by gate 1 so writeError can populate the audit Actor /
// KeyID attributes even on a downstream denial. nil KeyInfo means the
// denial fired before authn completed (gates 1).
type pipelineErr struct {
	errResp *errResp
	keyInfo *keystore.KeyInfo
}

// pipeline runs the §15.6 7-gate sequence. Returns (*resolvedRow, nil)
// on success or (nil, *pipelineErr) on first denial. Gate-by-gate:
//
//  1. Authn          — resolveAuthn       — 400/401/500
//     2/3. Env header + row — resolveEnv    — 400/403/404/500
//  4. Teams          — enforceTeams       — 403/503 (pk_ only)
//  5. Allowlist      — enforceAllowlist   — 403 (cheaper-first divergence)
//  6. Content        — resolveContent     — 404/500
//  7. Staleness      — checkStaleness     — 503
//  8. File open      — os.Open (early per D-02) — 404/500
//
// Gate 8 is the D-02 early open: the *os.File is acquired BEFORE the
// caller returns, so the inode is pinned for the lifetime of the
// response stream. Even if the Operator atomically rename(2)s a new
// version of the file mid-stream, the open FD continues to read the
// original inode (SC#4 — verified by
// TestPipeline_InFlightReadSurvivesRename).
//
// Per-kind Content-Type policy (CS-06):
//   - prompt:   *contentRow.ContentType if non-nil-non-empty else
//     application/octet-stream.
//   - plugin:   application/gzip (always).
//   - artifact: application/gzip when scope=directory, else
//     application/octet-stream.
func pipeline(ctx context.Context, d Deps, kind string, r *http.Request) (*resolvedRow, *pipelineErr) {
	// Gate 1 — Authn.
	info, errR := resolveAuthn(ctx, d, r)
	if errR != nil {
		return nil, &pipelineErr{errResp: errR}
	}

	// Gates 2 + 3 — env header + envcache row.
	envRow, errR := resolveEnv(ctx, d, info, r.Header.Get("x-ach-environment"))
	if errR != nil {
		return nil, &pipelineErr{errResp: errR, keyInfo: info}
	}

	// Gate 4 — pk_ team intersection (ek_ short-circuits).
	if errR := enforceTeams(ctx, d, info, envRow); errR != nil {
		return nil, &pipelineErr{errResp: errR, keyInfo: info}
	}

	// Gate 5 — allowlist (CHEAPER-FIRST divergence; see file doc).
	name := chi.URLParam(r, "name")
	if errR := enforceAllowlist(envRow, kind, name); errR != nil {
		return nil, &pipelineErr{errResp: errR, keyInfo: info}
	}

	// Gate 6 — content resolution (§12.3 CTE for plugin).
	row, errR := resolveContent(ctx, d, kind, name)
	if errR != nil {
		return nil, &pipelineErr{errResp: errR, keyInfo: info}
	}

	// Gate 7 — staleness.
	if errR := checkStaleness(row); errR != nil {
		return nil, &pipelineErr{errResp: errR, keyInfo: info}
	}

	// Gate 8 — D-02 early open. Compose the on-disk path from the
	// resolved row's scope (artifact) and open EARLY so a subsequent
	// rename(2) does not unhook the inode mid-response.
	//
	// Plugin special case: the resolved row's StorageLocation is used
	// directly instead of recomputing via ResolvePath. A scoped ref such
	// as "shared@mkt-b" carries '@' in the URL-param name, which would
	// produce "plugin/shared@mkt-b.tar.gz" — the wrong path. The operator
	// materialises marketplace plugins under "plugin/<name>.tar.gz" (no
	// marketplace qualifier on disk), so the absolute StorageLocation from
	// the projection row is always authoritative for plugins.
	var path string
	if kind == kindPlugin {
		// SECURITY: StorageLocation is not derived from a validated {name}
		// (plugins skip ResolvePath), so contain it under CacheRoot before
		// os.Open — an untrusted/future origin='ui' row pointing outside the
		// cache (e.g. /etc/passwd) must 404, never serve.
		p, ok := PluginStoragePathWithinRoot(d.CacheRoot, row.StorageLocation)
		if !ok {
			return nil, &pipelineErr{errResp: errContentNotFound(), keyInfo: info}
		}
		path = p
	} else {
		var err error
		path, err = ResolvePath(d.CacheRoot, kind, name, row.Scope)
		if err != nil {
			// validateName / kind / scope errors are caller-side
			// invariants — treat as 404 (router only registers known
			// kinds; envRow.context allowlist already passed). On scope
			// invalid this is a projection-write bug; still 404 from the
			// client's perspective.
			if errors.Is(err, ErrInvalidName) {
				return nil, &pipelineErr{errResp: errContentNotFound(), keyInfo: info}
			}
			return nil, &pipelineErr{errResp: errInternal(), keyInfo: info}
		}
	}
	f, err := os.Open(path) // #nosec G304 — plugin path contained under CacheRoot by PluginStoragePathWithinRoot; other kinds validated by ResolvePath
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
			// Projection row claims the file exists; the cache file is
			// missing. PVC drift / refresh-in-progress. Map to 404
			// content_not_found — the client retry will likely succeed
			// once the Operator's next refresh completes.
			return nil, &pipelineErr{errResp: errContentNotFound(), keyInfo: info}
		}
		return nil, &pipelineErr{errResp: errInternal(), keyInfo: info}
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, &pipelineErr{errResp: errInternal(), keyInfo: info}
	}

	contentType := contentTypeFor(kind, row)
	return &resolvedRow{
		File:        f,
		Size:        fi.Size(),
		ContentType: contentType,
		KeyInfo:     info,
	}, nil
}

// contentTypeFor implements the per-kind Content-Type policy (CS-06).
// Pure function — pipeline.go gate 8 calls this once after the file is
// open and the stat has succeeded.
//
//   - prompt:   *row.ContentType if non-nil-and-non-empty else
//     application/octet-stream. (The pre-Plan-05-05 prompt
//     default was text/markdown; Plan 05-05 / CS-06 changes
//     the fallback to application/octet-stream so the
//     policy is uniform across kinds when the override is
//     absent. Existing prompt CRs that need text/markdown
//     must set spec.contentType.)
//   - plugin:   application/gzip (plugins are always .tar.gz on disk).
//   - artifact: application/gzip when row.Scope == "directory" else
//     application/octet-stream.
func contentTypeFor(kind string, row *contentRow) string {
	switch kind {
	case kindPrompt:
		if row.ContentType != nil && *row.ContentType != "" {
			return *row.ContentType
		}
		return contentTypeOctet
	case kindPlugin:
		return contentTypeGzip
	case kindSkill:
		return contentTypeGzip
	case kindArtifact:
		if row.Scope == "directory" {
			return contentTypeGzip
		}
		return contentTypeOctet
	}
	return contentTypeOctet
}
