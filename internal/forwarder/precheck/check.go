// SPDX-License-Identifier: Apache-2.0

package precheck

import (
	"context"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// EnvProvider is the narrow contract precheck consumes from the
// Postgres-backed Environment store (internal/forwarder/envstore,
// issue #34 C2). Tests substitute a tiny in-memory fake; production
// wires envstore.Store.
//
// Get returns (nil, false) on absence — caller maps that to
// ErrUnauthorizedResource per the D-15 narrow contract.
// List returns every active Environment (envstore.Store.List excludes
// drain-mode rows at the SQL layer per db.ListEnvironments).
type EnvProvider interface {
	Get(name string) (*db.EnvironmentRow, bool)
	List() []db.EnvironmentRow
}

// Deps wires the precheck package's dependencies. Kept narrow so tests
// can substitute fakes/mocks. EnvProvider replaces the previous
// controller-runtime cached client per issue #34 C5; the projection
// table is the source of truth and the cache reads from it.
type Deps struct {
	// EnvProvider is the Postgres-backed Environment cache (C2/C5).
	EnvProvider EnvProvider
	// TeamsResolver is the Redis-cached LiteLLM teams lookup (Plan 04-03).
	TeamsResolver keystore.TeamsResolver
}

// resourceKind selects which runtime list to consult on the Environment.
type resourceKind int

const (
	mcpResourceKind resourceKind = iota
	a2aResourceKind
)

// CheckMCP runs the §5.1 step-4 pre-check for /mcp/<name> requests.
//
//   - ek_:  name MUST appear in Environment.spec.runtime.mcpServers[].
//   - pk_:  caller's LiteLLM teams MUST intersect non-empty with
//     Environment.spec.authorizedTeams[] of at least one Environment whose
//     spec.runtime.mcpServers[] contains <name>.
//
// On success the first return is the set of teams that GRANTED the access —
// the Environment's authorizedTeams for ek_, the caller∩Environment
// intersection for pk_. The proxy filters and normalizes it into the JWT
// "groups" claim; the authorization decision itself is unchanged.
//
// On failure returns a typed sentinel; the caller (Plan 04-07 handler)
// translates the sentinel to an HTTP outcome via render.Error.
func CheckMCP(ctx context.Context, kc middleware.KeyContext, name string, deps Deps) ([]string, error) {
	return check(ctx, kc, name, deps, mcpResourceKind)
}

// CheckA2A runs the §5.1 step-4 pre-check for /a2a/<name> requests.
// Mirrors CheckMCP but reads Environment.spec.runtime.a2aAgents[]
// instead of .mcpServers[].
func CheckA2A(ctx context.Context, kc middleware.KeyContext, name string, deps Deps) ([]string, error) {
	return check(ctx, kc, name, deps, a2aResourceKind)
}

// runtimeList extracts the right resource slice per route family.
func runtimeList(row *db.EnvironmentRow, kind resourceKind) []string {
	switch kind {
	case mcpResourceKind:
		return row.RuntimeMCPServers
	case a2aResourceKind:
		return row.RuntimeA2AAgents
	}
	return nil
}

func check(ctx context.Context, kc middleware.KeyContext, name string, deps Deps, kind resourceKind) ([]string, error) {
	switch kc.KeyType {
	case keys.PrefixEk:
		return checkEk(ctx, kc, name, deps, kind)
	case keys.PrefixPk:
		return checkPk(ctx, kc, name, deps, kind)
	default:
		return nil, ErrInvalidKeyType
	}
}

// checkEk implements the ek_ path. The bound Environment name comes from
// kc.Environment (resolved by Phase 3 keystore.EkResolve). Missing,
// terminating, or name-not-in-list all fail closed as
// ErrUnauthorizedResource per D-15 narrow error surface.
//
// envstore.Store.List/Get already filter drain-mode rows out at the SQL
// layer (db.ListEnvironments excludes deletion_timestamp IS NOT NULL),
// so a Get miss here means "absent OR terminating" — both collapse to
// the narrow ErrUnauthorizedResource outcome regardless.
func checkEk(_ context.Context, kc middleware.KeyContext, name string, deps Deps, kind resourceKind) ([]string, error) {
	row, ok := deps.EnvProvider.Get(kc.Environment)
	if !ok {
		return nil, ErrUnauthorizedResource // D-15 narrow: missing env → 403 not 404
	}
	if row.DeletionTimestamp != nil {
		return nil, ErrUnauthorizedResource // terminating env cannot grant access (D-15)
	}
	for _, n := range runtimeList(row, kind) {
		if n == name {
			// An ek_ has no human behind it: its groups are the whole
			// authorizedTeams set of the one Environment it is bound to.
			//
			// This slice aliases the envstore cache row's backing array
			// (Store.Get copies the row struct, not the slice) — callers
			// MUST NOT mutate it in place (e.g. an in-place sort); build a
			// fresh slice instead, as filterShellTeams already does.
			return row.AuthorizedTeams, nil
		}
	}
	return nil, ErrUnauthorizedResource
}

// checkPk implements the pk_ path. The caller's LiteLLM teams come from
// the (cached) TeamsResolver; any error during resolve maps to
// ErrLiteLLMUnreachable (Forwarder → 503 per FWD-03). The intersection
// is computed in a single pass over the namespace's Environments —
// O(N_envs × N_teams), acceptable for typical N (<100 envs/namespace).
//
// Union semantics: the caller is authorized if AT LEAST ONE active
// Environment hosts <name> AND has a non-empty intersection of
// authorizedTeams with the caller's teams. Terminating envs are skipped.
func checkPk(ctx context.Context, kc middleware.KeyContext, name string, deps Deps, kind resourceKind) ([]string, error) {
	callerTeams, err := deps.TeamsResolver.Resolve(ctx, kc.OwnerEmail)
	if err != nil {
		return nil, ErrLiteLLMUnreachable
	}
	if len(callerTeams) == 0 {
		return nil, ErrUnauthorizedTeam // ∅ ∩ anything = ∅
	}
	teamSet := make(map[string]struct{}, len(callerTeams))
	for _, t := range callerTeams {
		teamSet[t] = struct{}{}
	}

	// envstore.Store.List excludes drain-mode rows at the SQL layer
	// (db.ListEnvironments filters deletion_timestamp IS NOT NULL), so
	// PC14's "terminating envs cannot grant access" is enforced by the
	// data layer; we still keep the in-row check below as defense in
	// depth in case a future EnvProvider implementation surfaces
	// drain-mode rows.
	//
	// The walk no longer stops at the first grant: the accumulated set is
	// what the proxy mints into the "groups" claim, so a caller entitled
	// through several Environments carries all of those teams. The
	// authorization verdict is unchanged — non-empty means authorized.
	var granted []string
	envs := deps.EnvProvider.List()
	for i := range envs {
		row := &envs[i]
		if row.DeletionTimestamp != nil {
			continue
		}
		hasName := false
		for _, n := range runtimeList(row, kind) {
			if n == name {
				hasName = true
				break
			}
		}
		if !hasName {
			continue
		}
		for _, t := range row.AuthorizedTeams {
			if _, ok := teamSet[t]; ok {
				granted = append(granted, t)
			}
		}
	}
	if len(granted) == 0 {
		return nil, ErrUnauthorizedTeam
	}
	return granted, nil
}
