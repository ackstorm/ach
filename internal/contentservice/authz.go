// SPDX-License-Identifier: Apache-2.0

// authz.go ships the per-gate authorization functions that the
// pipeline orchestrator (pipeline.go) chains in D-04 cheaper-first
// order. Each gate returns *errResp (nil on pass-through) so the
// orchestrator can short-circuit at the first denial without unwinding
// a tower of nested if-else branches. The closed-set outcome pattern
// mirrors the Phase 2 reconciler conventions:
// internal/controller/ach/environment_controller.go:reconcileAccessGroup
// uses the same "typed result per branch, never fall through" idiom.
//
// Gates implemented here:
//
//	resolveAuthn      — bearer header → keystore.KeyInfo. 400/401/500.
//	resolveEnv        — x-ach-environment + bound-env policy + envcache.Get.
//	                    400/403/404/500.
//	enforceTeams      — pk_ only: TeamsResolver + intersection. 403/503.
//	                    ek_ short-circuits to pass.
//	enforceAllowlist  — pure: name ∈ envRow.context.<kind>. 403.
//	resolveContent    — kind-dispatched projection lookup (§12.3 CTE
//	                    for plugin). 404/500.
//	checkStaleness    — pure: now - lsr > max_staleness. 503.
//
// The pipeline.go orchestrator and per-gate doc comments cite the
// cheaper-first divergence (gate 5 BEFORE gate 6) — see pipeline.go
// for the canonical statement.

package contentservice

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/contentservice/envcache"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/teams"
	"github.com/ackstorm/ach/internal/refparse"
)

// contentRow holds the per-request resolved row that gate 7 (staleness)
// and gate 8 (file open) consume. The struct is unexported — only
// pipeline.go composes one from a per-kind db.* call.
//
// Fields:
//
//   - StorageLocation: absolute path the cache file lives at on the
//     shared PVC (CS-07).
//   - LastSuccessfulRefresh: nullable timestamp from the projection row;
//     nil → stale_cache_expired per CS-10 / OP-11.
//   - MaxStalenessSeconds: refresh budget for the staleness gate.
//   - ContentType: optional Prompt.spec.contentType override (Prompt
//     only — nil/empty falls back to application/octet-stream per
//     CS-06 + the pipeline's per-kind policy).
//   - Scope: artifact scope ("object" | "directory") — drives the path
//     suffix (.tar.gz vs bare) and the response Content-Type.
//   - Source: §12.3 resolution arm ("plugin" | "marketplace") for
//     plugin kind; empty for prompt/artifact.
type contentRow struct {
	StorageLocation       string
	LastSuccessfulRefresh *time.Time
	MaxStalenessSeconds   int64
	ContentType           *string
	Scope                 string
	Source                string
}

// resolveAuthn (gate 1 per D-04). Reads x-ach-key from r.Header,
// validates the prefix, and delegates to Deps.Resolver.Resolve.
//
// Returns *errResp on:
//   - empty header → 400 invalid_key_format
//   - missing pk_ / ek_ prefix → 400 invalid_key_format
//   - Resolver internal error → 500 internal_error
//   - Resolver returns (nil, nil) (revoked/expired/unknown) → 401 expired_or_revoked
//
// Happy path: returns the resolved *keystore.KeyInfo + nil errResp.
//
// Note: the Authn middleware in Phase 3 (internal/platformapi/middleware)
// does the equivalent work for the Platform API; Content Service runs
// authn INLINE in this gate rather than as a middleware so the audit /
// metric / envelope emission shape stays consistent with the rest of
// the §15.6 pipeline.
func resolveAuthn(ctx context.Context, d Deps, r *http.Request) (*keystore.KeyInfo, *errResp) {
	plaintext := r.Header.Get("x-ach-key")
	if plaintext == "" {
		return nil, errInvalidKeyFormat
	}
	if !strings.HasPrefix(plaintext, string(keys.PrefixPk)) && !strings.HasPrefix(plaintext, string(keys.PrefixEk)) {
		return nil, errInvalidKeyFormat
	}
	info, err := d.Resolver.Resolve(ctx, plaintext)
	if err != nil {
		return nil, errInternal
	}
	if info == nil {
		return nil, errExpiredOrRevoked
	}
	return info, nil
}

// resolveEnv (gates 2 + 3 per D-04). Validates the x-ach-environment
// header against the bearer kind, then loads the Environment projection
// row via Deps.EnvCache.
//
// pk_ semantics:
//   - empty header → 400 missing_environment (CS-02).
//
// ek_ semantics:
//   - empty header → use info.Environment (bound env).
//   - non-empty header AND header != info.Environment → 403 wrong_environment (CS-02).
//   - non-empty header AND header == info.Environment → use header (same as bound).
//
// After header resolution, the projection row is loaded via
// EnvCache.Get(Namespace, resolvedEnv). EnvCache.Get returning a miss
// (ok == false) → 404 environment_not_found. The lookup is an in-memory
// map read, so it cannot error — there is no 500 path here.
//
// CS-09: the cache snapshot includes drain-mode rows (deletion_timestamp
// != nil) — the Content Service serves until full finalizer drain.
// Downstream gates (resolveContent / checkStaleness) eventually
// short-circuit if the underlying projection row is hard-deleted.
func resolveEnv(d Deps, info *keystore.KeyInfo, headerEnv string) (*envcache.EnvRow, *errResp) {
	var resolved string
	switch info.KeyType {
	case keys.PrefixPk:
		if headerEnv == "" {
			return nil, errMissingEnvironment
		}
		resolved = headerEnv
	case keys.PrefixEk:
		if headerEnv != "" && headerEnv != info.Environment {
			return nil, errWrongEnvironment
		}
		resolved = info.Environment
	default:
		// Resolver guarantees PrefixPk or PrefixEk; defensive only.
		return nil, errInvalidKeyFormat
	}
	row, ok := d.EnvCache.Get(d.Namespace, resolved)
	if !ok {
		return nil, errEnvironmentNotFound
	}
	return row, nil
}

// enforceTeams (gate 4 per D-04). pk_ only; ek_ short-circuits to nil.
//
// Calls Deps.Teams.Resolve(ctx, info.OwnerEmail) and intersects the
// returned []string with envRow.AuthorizedTeams. Empty intersection →
// 403 unauthorized_team.
//
// Transport-error classification (per Phase 4 D-17 reuse contract):
//   - errors.Is(err, litellm.ErrNotFound) → treat as empty team list
//     (matches the upstream resolver's Phase 4 behavior — the user is
//     not a LiteLLM-registered actor, not a transport failure).
//   - Any other error → 503 litellm_unreachable AND
//     Deps.LiteLLMUnreachable{caller="content_service"}.Inc() (cross-
//     plan: shared collector registered by Plan 05-06's wiring).
//
// Note on user-team list: a nil result with err == nil is normalized to
// empty (matches Phase 4 D-17 semantics). The intersection is a linear
// scan because env.AuthorizedTeams is typically small (≤ 50 elements
// per CONTEXT canonical-refs).
func enforceTeams(ctx context.Context, d Deps, info *keystore.KeyInfo, envRow *envcache.EnvRow) *errResp {
	if info.KeyType != keys.PrefixPk {
		return nil
	}
	userTeams, err := d.Teams.Resolve(ctx, info.OwnerEmail)
	if err != nil {
		if errors.Is(err, litellm.ErrNotFound) {
			// Not-found means "no team membership" — caller continues
			// with empty userTeams for the intersection check below.
			userTeams = nil
		} else {
			if d.LiteLLMUnreachable != nil {
				d.LiteLLMUnreachable.WithLabelValues("content_service").Inc()
			}
			return errLitellmUnreachable
		}
	}
	if !teams.HasIntersect(userTeams, envRow.AuthorizedTeams) {
		return errUnauthorizedTeam
	}
	return nil
}

// enforceAllowlist (gate 5 per D-04 — cheaper-first divergence). Pure
// function, no I/O. Returns 403 unauthorized_content when the requested
// {name} is not in the matching envRow.Context<Kind> slice.
//
// SPEC-DIVERGENCE callout: per D-04 this gate runs BEFORE gate 6
// (content resolution) — the inverse of the spec §15.6 v10 fix step
// order. Side effect: "name not in env.context AND not in any CRD"
// yields 403 unauthorized_content (this gate fires) NOT 404
// content_not_found (which would only be reachable if the allowlist
// gate passed). The divergence is documented in pipeline.go and
// audit-dashboard-visible via the distinct outcome strings.
func enforceAllowlist(envRow *envcache.EnvRow, kind, name string) *errResp {
	var list []string
	switch kind {
	case kindPrompt:
		list = envRow.ContextPrompts
	case kindPlugin:
		list = envRow.ContextPlugins
	case kindArtifact:
		list = envRow.ContextArtifacts
	case kindSkill:
		list = envRow.ContextSkills
	default:
		// Unknown kind — router only registers known kinds, so this is
		// defensive. Map to the cheapest closed-set outcome.
		return errUnauthorizedContent
	}
	for _, n := range list {
		// Comparison is intentionally on the FULL ref (e.g. "shared@mkt-b"),
		// which matches the Environment's stored allowlist entry verbatim —
		// no parsing needed for the allowlist gate.
		if n == name {
			return nil
		}
	}
	return errUnauthorizedContent
}

// resolveContent (gate 6 per D-04). Kind-dispatched projection row
// lookup. For plugin, parses the ref via refparse and calls
// db.ResolvePluginByName with the (name, marketplace): a bare name
// resolves a Plugin CRD row ONLY (no marketplace fallback); a scoped
// name@marketplace resolves the exact (marketplace_name, name) row. No
// tiebreak.
//
// Returns:
//
//   - (row, nil) on hit;
//   - (nil, errContentNotFound) when the projection row is absent (for a
//     bare name: no Plugin CRD row; for a scoped name: no marketplace row);
//   - (nil, errInternal) on any other DB error.
//
// CS-09: soft-deleted rows (DeletionTimestamp != nil) are STILL
// returned. The staleness gate (gate 7) may eventually 503 once the
// last_successful_refresh ages out, OR the row is hard-deleted by
// finalizer drain (which manifests as 404 here).
func resolveContent(ctx context.Context, d Deps, kind, name string) (*contentRow, *errResp) {
	switch kind {
	case kindPrompt:
		row, err := db.GetPromptByName(ctx, d.Pool, d.Namespace, name)
		if err != nil {
			return nil, errInternal
		}
		if row == nil {
			return nil, errContentNotFound
		}
		return &contentRow{
			StorageLocation:       row.StorageLocation,
			LastSuccessfulRefresh: row.LastSuccessfulRefresh,
			MaxStalenessSeconds:   row.MaxStalenessSeconds,
			ContentType:           row.ContentType,
		}, nil
	case kindPlugin:
		// Reject malformed refs (e.g. "name@" → empty marketplace) instead
		// of silently treating them as a bare CRD lookup — the grammar
		// requires a non-empty marketplace whenever '@' is present.
		if !refparse.Valid(name) {
			return nil, errContentNotFound
		}
		pname, marketplace, _ := refparse.Parse(name)
		res, err := db.ResolvePluginByName(ctx, d.Pool, d.Namespace, pname, marketplace)
		if err != nil {
			return nil, errInternal
		}
		if res == nil {
			return nil, errContentNotFound
		}
		return &contentRow{
			StorageLocation:       res.StorageLocation,
			LastSuccessfulRefresh: res.LastSuccessfulRefresh,
			MaxStalenessSeconds:   res.MaxStalenessSeconds,
			Source:                res.Source,
		}, nil
	case kindArtifact:
		row, err := db.GetArtifactByName(ctx, d.Pool, d.Namespace, name)
		if err != nil {
			return nil, errInternal
		}
		if row == nil {
			return nil, errContentNotFound
		}
		return &contentRow{
			StorageLocation:       row.StorageLocation,
			LastSuccessfulRefresh: row.LastSuccessfulRefresh,
			MaxStalenessSeconds:   row.MaxStalenessSeconds,
			Scope:                 row.Scope,
		}, nil
	case kindSkill:
		// Mirror the plugin arm: a bare name resolves a Skill CRD row; a scoped
		// name@marketplace resolves the exact skill_marketplace_skills row. The
		// contentRow carries Source so pipeline.go serves a marketplace skill
		// from the absolute StorageLocation (skill-marketplace/<mkt>/<name>.tar.gz)
		// while a bare skill keeps the deterministic skill/<name>.tar.gz ResolvePath.
		if !refparse.Valid(name) {
			return nil, errContentNotFound
		}
		sname, marketplace, _ := refparse.Parse(name)
		res, err := db.ResolveSkillByName(ctx, d.Pool, d.Namespace, sname, marketplace)
		if err != nil {
			return nil, errInternal
		}
		if res == nil {
			return nil, errContentNotFound
		}
		return &contentRow{
			StorageLocation:       res.StorageLocation,
			LastSuccessfulRefresh: res.LastSuccessfulRefresh,
			MaxStalenessSeconds:   res.MaxStalenessSeconds,
			Source:                res.Source,
		}, nil
	}
	// Defensive — chi router only registers known kinds.
	return nil, errContentNotFound
}

// checkStaleness (gate 7 per D-04). Pure function. Returns
// errStaleCacheExpired when:
//
//   - row.LastSuccessfulRefresh == nil (PVC-loss / OP-11 first reconcile
//     hasn't completed), OR
//   - now - *row.LastSuccessfulRefresh > MaxStalenessSeconds (spec'd
//     refresh budget exceeded — CS-10).
//
// Otherwise nil (gate passes).
func checkStaleness(row *contentRow) *errResp {
	if row.LastSuccessfulRefresh == nil {
		return errStaleCacheExpired
	}
	if time.Since(*row.LastSuccessfulRefresh) > time.Duration(row.MaxStalenessSeconds)*time.Second {
		return errStaleCacheExpired
	}
	return nil
}
