// SPDX-License-Identifier: Apache-2.0

package precheck

import (
	"context"
	"fmt"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Deps wires the precheck package's dependencies. Kept narrow so tests
// can substitute fakes/mocks. The K8sClient is the cached client from
// mgr.GetClient() (Plan 04-08 wires it).
type Deps struct {
	// K8sClient is the controller-runtime cached client. precheck only
	// reads Environments via Get / List; no writes.
	K8sClient client.Client
	// TeamsResolver is the Redis-cached LiteLLM teams lookup (Plan 04-03).
	TeamsResolver keystore.TeamsResolver
	// Namespace scopes the Environment reads. Forwarder runs in a single
	// tenant namespace per deployment (Hub §5.1).
	Namespace string
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
// On failure returns a typed sentinel; the caller (Plan 04-07 handler)
// translates the sentinel to an HTTP outcome via render.Error.
func CheckMCP(ctx context.Context, kc middleware.KeyContext, name string, deps Deps) error {
	return check(ctx, kc, name, deps, mcpResourceKind)
}

// CheckA2A runs the §5.1 step-4 pre-check for /a2a/<name> requests.
// Mirrors CheckMCP but reads Environment.spec.runtime.a2aAgents[]
// instead of .mcpServers[].
func CheckA2A(ctx context.Context, kc middleware.KeyContext, name string, deps Deps) error {
	return check(ctx, kc, name, deps, a2aResourceKind)
}

// runtimeList extracts the right resource slice per route family.
func runtimeList(env *achv1alpha1.Environment, kind resourceKind) []string {
	switch kind {
	case mcpResourceKind:
		return env.Spec.Runtime.MCPServers
	case a2aResourceKind:
		return env.Spec.Runtime.A2AAgents
	}
	return nil
}

func check(ctx context.Context, kc middleware.KeyContext, name string, deps Deps, kind resourceKind) error {
	switch kc.KeyType {
	case keys.PrefixEk:
		return checkEk(ctx, kc, name, deps, kind)
	case keys.PrefixPk:
		return checkPk(ctx, kc, name, deps, kind)
	default:
		return ErrInvalidKeyType
	}
}

// checkEk implements the ek_ path. The bound Environment name comes from
// kc.Environment (resolved by Phase 3 keystore.EkResolve). Missing,
// terminating, or name-not-in-list all fail closed as
// ErrUnauthorizedResource per D-15 narrow error surface.
func checkEk(ctx context.Context, kc middleware.KeyContext, name string, deps Deps, kind resourceKind) error {
	env := &achv1alpha1.Environment{}
	key := types.NamespacedName{Namespace: deps.Namespace, Name: kc.Environment}
	if err := deps.K8sClient.Get(ctx, key, env); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrUnauthorizedResource // D-15 narrow: missing env → 403 not 404
		}
		return fmt.Errorf("precheck.checkEk: get env %s: %w", kc.Environment, err)
	}
	if !env.DeletionTimestamp.IsZero() {
		return ErrUnauthorizedResource // terminating env cannot grant access (D-15)
	}
	for _, n := range runtimeList(env, kind) {
		if n == name {
			return nil
		}
	}
	return ErrUnauthorizedResource
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
func checkPk(ctx context.Context, kc middleware.KeyContext, name string, deps Deps, kind resourceKind) error {
	callerTeams, err := deps.TeamsResolver.Resolve(ctx, kc.OwnerEmail)
	if err != nil {
		return ErrLiteLLMUnreachable
	}
	if len(callerTeams) == 0 {
		return ErrUnauthorizedTeam // ∅ ∩ anything = ∅
	}
	teamSet := make(map[string]struct{}, len(callerTeams))
	for _, t := range callerTeams {
		teamSet[t] = struct{}{}
	}

	var envList achv1alpha1.EnvironmentList
	if err := deps.K8sClient.List(ctx, &envList, client.InNamespace(deps.Namespace)); err != nil {
		return fmt.Errorf("precheck.checkPk: list envs: %w", err)
	}
	for i := range envList.Items {
		env := &envList.Items[i]
		if !env.DeletionTimestamp.IsZero() {
			continue // PC14: terminating envs cannot grant access
		}
		hasName := false
		for _, n := range runtimeList(env, kind) {
			if n == name {
				hasName = true
				break
			}
		}
		if !hasName {
			continue
		}
		for _, t := range env.Spec.AuthorizedTeams {
			if _, ok := teamSet[t]; ok {
				return nil
			}
		}
	}
	return ErrUnauthorizedTeam
}
