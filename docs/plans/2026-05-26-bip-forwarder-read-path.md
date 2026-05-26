# BIP Forwarder Read-Path Resolver — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Provide the Forwarder with a deterministic, operator-dumb resolution algorithm for `BackendIdentityPolicy` (BIP) CRs that target the same `(target.kind, target.name)` pair. Two layers: (A) pure resolver function landable today, fully unit-tested; (B) wiring into the actual `/mcp/<name>` and `/a2a/<name>` route handlers, which depends on the Forwarder skeleton arriving as part of the §2 domain port (`docs/plans/2026-05-25-ach-domain-port.md`, Phase 4 / Task 4.1+).

**Working directory:** `/home/jcm/Projects/ach` (single-binary cobra layout — see `CLAUDE.md` "Architecture" section).

**Branch policy:** Layer A may land on its own short-lived branch
`feat/bip-resolver` (independent of `feat/domain-port`). Layer B lands
inside `feat/domain-port` as additional tasks under that plan's Phase 4 —
this plan provides the wiring spec; the §2 plan owns the surrounding
Forwarder skeleton commits.

---

## Design source of truth

**Memo:** `/home/jcm/.claude/projects/-home-jcm-Projects-ach/memory/feedback_bip_no_shadow_logic.md`

Read it first. The one-line summary is: the operator stays DUMB on BIP
duplicates. No `DuplicateTarget` reconciler, no `Synced` status churn,
no shadow flip. The Forwarder resolves duplicates at READ time:

1. List all `BackendIdentityPolicy` CRs in the watched namespace via the
   informer cache.
2. Filter to entries where `spec.target.kind` AND `spec.target.name`
   match the incoming `/mcp/<name>` or `/a2a/<name>` route segment
   (kind derived from the URL prefix; name derived from the segment).
3. Sort matching CRs by `metadata.name` ASCENDING.
4. Take `Items[len-1]` — the alphabetically-LAST CR. Honor its
   `spec.forwardIdentityJWT`. Mint + attach the §9.1 ACH JWT iff `true`.
5. Zero matches: no JWT attached (Forwarder strips client
   `Authorization` header per Hub §9.3).

**Rationale:** Operators wanting different precedence rename CRs
(`zz-` prefix flips the winner). No babysitting on the operator side;
the user owns the CR set.

**Current state in tree (verified 2026-05-26):**

- `api/ach/v1alpha1/backendidentitypolicy_types.go` — Spec/Status already
  carry the correct doc comments ("Forwarder resolves duplicate
  deterministically by sorting matching CRs by metadata.name ASC and
  using the LAST entry"). The CRD-08 admission rule already enforces
  `spec.forwardIdentityJWT` is REQUIRED.
- `internal/controller/ach/backendidentitypolicy_controller.go` — Phase 1
  finalizer-only reconciler. **Stale comments still mention "Phase 4 can
  layer real Synced=DuplicateTarget reconciliation"** — this plan
  scrubs them as Task 6.2.
- `cmd/ach/cmd/forwarder.go` — Phase 1 stub: `/healthz` only.
- `examples/09-backendidentitypolicy-context7.yaml` and
  `examples/10-backendidentitypolicy-duplicate.yaml` — exemplar pair
  already in tree (`bip-context7-jwt-on` vs `zz-bip-context7-jwt-off`,
  same `MCPServer/context7` target, opposite `forwardIdentityJWT`).
- ach-old has NO existing `DuplicateTarget` reconciler to port (verified
  by `grep`). The §2 domain-port plan's "do not port" directive is
  preventive, not a deletion task. This plan documents that in CLAUDE.md
  so future agents don't grow one.

---

## Required reading (before starting)

| Working on...                                  | MUST read first                                                                                              |
|------------------------------------------------|--------------------------------------------------------------------------------------------------------------|
| Anything in this plan                          | `/home/jcm/.claude/projects/-home-jcm-Projects-ach/memory/feedback_bip_no_shadow_logic.md` (design source)   |
| Layer A resolver code                          | `api/ach/v1alpha1/backendidentitypolicy_types.go` (Spec/Status shape, BackendTargetRef fields)               |
| Layer A unit tests                             | `examples/09-...yaml` + `examples/10-...yaml` (exemplar inputs the tests assert on)                          |
| Layer A envtest hook                           | `internal/controller/ach/backendidentitypolicy_finalizer_test.go` (envtest pattern, `WatchNamespace`)        |
| Layer B wiring                                 | `docs/plans/2026-05-25-ach-domain-port.md` Phase 4 (forwarder skeleton tasks — confirm landed before Layer B) |
| Layer B route handlers                         | `cmd/ach/cmd/forwarder.go` (stub today; the §2 port replaces `runForwarder` body)                            |
| Layer B e2e                                    | `test/e2e/phase1_invariants_test.go` (existing e2e patterns, kind+Helm conventions)                          |
| CLAUDE.md amendments                           | `CLAUDE.md` "Repository-specific patterns" section (where to insert the new bullet)                          |
| Toolchain (every command)                      | `CLAUDE.md` "Toolchain — host has NO Go (always Docker)" (`./scripts/dev.sh` prefix is mandatory)            |
| Waiting patterns (Layer B e2e)                 | `CLAUDE.md` "Waiting for state — use blessed make targets"                                                   |
| Pre-push gate                                  | `scripts/pre-push-check.sh` (15 gates, SPDX header, govulncheck ack-list)                                    |

---

## Layer split

| Layer | Scope                                                                | Depends on §2 forwarder skeleton? | Lands where                                       |
|-------|----------------------------------------------------------------------|-----------------------------------|---------------------------------------------------|
| A     | Pure `ResolveBIP` function + unit tests + envtest visibility check   | NO — independent                  | Branch `feat/bip-resolver`; merge to `main`       |
| B     | Wire `ResolveBIP` into `/mcp/<name>` + `/a2a/<name>` dispatchers, e2e| YES — Phase 4 skeleton must exist | Inside `feat/domain-port` after Task 4.1 (or later) |

**Why split:** Layer A is pure logic with no Forwarder dependency. It
provides the testable contract that Layer B consumes verbatim. Landing
A first means the §2 domain port can integrate a known-good resolver
rather than re-deriving it during the larger port.

---

## Layer A — Pure resolver (lands today, independent of §2)

Goal: ship `internal/forwarder/bipresolver/resolver.go` (or equivalent
package path — see Task A.1 for the binding decision) with a
table-tested pure function plus an envtest that exercises the
"both Synced columns empty" invariant via the real informer client.

### Task A.1: Choose and create the package path

**Decision:** `internal/forwarder/bipresolver/` — sits under the
`internal/forwarder/` tree that the §2 domain port will populate. Until
§2 lands, this subpackage is the only file under `internal/forwarder/`,
which is fine — it imports only `api/ach/v1alpha1` and stdlib, so it
compiles independently.

**Files to create:**
- `internal/forwarder/bipresolver/doc.go` (package doc — paste the
  feedback-memo summary verbatim)
- `internal/forwarder/bipresolver/resolver.go` (skeleton from Task A.2)
- `internal/forwarder/bipresolver/resolver_test.go` (table tests from
  Task A.3)

**Steps:**

1. `mkdir -p internal/forwarder/bipresolver`
2. Write `doc.go` with SPDX header + package comment pointing at
   `feedback_bip_no_shadow_logic.md` (use the relative tree path so the
   memo location stays internal — `/home/jcm/.claude/...` is per-user
   and shouldn't appear in source).
3. Confirm `./scripts/dev.sh go build ./internal/forwarder/...` PASS
   (empty package is a valid build target).
4. Commit: `chore(forwarder): scaffold internal/forwarder/bipresolver package`

### Task A.2: Define resolver function signature (no body)

The signature is the contract Layer B will call. We freeze it before
writing tests so the table-test file can be reviewed against a stable
shape.

**File:** `internal/forwarder/bipresolver/resolver.go`

**Signature (paste verbatim):**

```go
// SPDX-License-Identifier: Apache-2.0

// Package bipresolver implements the §9.3 BackendIdentityPolicy read-time
// resolver. The operator stays dumb on duplicate (kind, name) targets;
// this function is the single source of truth for what the Forwarder
// does at request time.
//
// Algorithm (feedback_bip_no_shadow_logic.md):
//   1. Filter the cached BIP list to entries whose spec.target.kind AND
//      spec.target.name equal the route's (Kind, Name).
//   2. Sort the survivors by metadata.name ASCENDING.
//   3. Return the LAST element (or nil if zero matches).
//
// Callers MUST treat (winner == nil) as "no JWT" — the §9.3 contract
// says the Forwarder strips the client Authorization header in that
// case. forwardIdentityJWT=false on the winner is operationally
// indistinguishable from no CR (still strip, still no mint).
package bipresolver

import (
	"sort"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// RouteTarget is the (kind, name) pair extracted from the inbound
// HTTP path by the route dispatcher. Kind is "MCPServer" for /mcp/<name>
// and "A2AAgent" for /a2a/<name>; Name is the bare URL segment
// (DNS-1123-validated by CRD admission, so no further sanitization
// here).
type RouteTarget struct {
	Kind string
	Name string
}

// Resolve returns the BackendIdentityPolicy CR whose
// forwardIdentityJWT value the Forwarder MUST honor for the given
// route, or nil if no CR matches the target. The result pointer is a
// reference into bips.Items — callers MUST NOT mutate it.
//
// Algorithm: filter by exact (kind, name) match, sort survivors by
// metadata.name ASC, take Items[len-1]. Tie-breaking is not possible
// because metadata.name is unique within a namespace.
//
// Pure function: no I/O, no clock, no logging. The whole package is
// safe to call from a hot request path.
func Resolve(bips *achv1alpha1.BackendIdentityPolicyList, route RouteTarget) *achv1alpha1.BackendIdentityPolicy {
	// TODO Task A.4
	return nil
}
```

**Steps:**

1. Write the file as above.
2. `./scripts/dev.sh go build ./internal/forwarder/bipresolver/...` PASS
3. `./scripts/dev.sh make lint-changed` PASS (will flag the unused
   `sort` import — fine, addressed in Task A.4).
4. **Do not commit yet** — Task A.3 lands the tests first per TDD.

### Task A.3: Write table-driven unit tests (TDD — RED phase)

**File:** `internal/forwarder/bipresolver/resolver_test.go`

**Coverage matrix:**

| Case | Input list                                          | Route                       | Expected winner             |
|------|-----------------------------------------------------|-----------------------------|-----------------------------|
| 1    | empty                                               | `MCPServer/context7`        | nil                         |
| 2    | one CR, exact match                                 | `MCPServer/context7`        | that CR                     |
| 3    | one CR, kind mismatch (`A2AAgent` vs `MCPServer`)   | `MCPServer/context7`        | nil                         |
| 4    | one CR, name mismatch (`other` vs `context7`)       | `MCPServer/context7`        | nil                         |
| 5    | two CRs, same target, sorted ASC (`a-...`, `b-...`) | `MCPServer/context7`        | the `b-...` one             |
| 6    | two CRs, same target, input order REVERSED          | `MCPServer/context7`        | still the `b-...` one       |
| 7    | the exemplar pair from `examples/09` + `examples/10`| `MCPServer/context7`        | `zz-bip-context7-jwt-off`   |
| 8    | three CRs: two same target, one different target    | `MCPServer/context7`        | LAST of the two matching    |
| 9    | mixed-kind: `MCPServer/foo` + `A2AAgent/foo`        | `A2AAgent/foo`              | only the `A2AAgent` one     |
| 10   | name with embedded hyphens + dots (DNS-1123 edge)   | `MCPServer/sub.example`     | matching CR (no name munging)|

**Skeleton (paste verbatim, fill in cases):**

```go
// SPDX-License-Identifier: Apache-2.0

package bipresolver

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func bip(name, targetKind, targetName string, forward bool) achv1alpha1.BackendIdentityPolicy {
	return achv1alpha1.BackendIdentityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ach-system"},
		Spec: achv1alpha1.BackendIdentityPolicySpec{
			Target:             achv1alpha1.BackendTargetRef{Kind: targetKind, Name: targetName},
			ForwardIdentityJWT: forward,
		},
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name        string
		items       []achv1alpha1.BackendIdentityPolicy
		route       RouteTarget
		wantNil     bool
		wantWinner  string // metadata.name; ignored if wantNil
		wantForward bool   // ignored if wantNil
	}{
		{
			name:    "empty list",
			items:   nil,
			route:   RouteTarget{Kind: "MCPServer", Name: "context7"},
			wantNil: true,
		},
		// ... cases 2-10 per the matrix above
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list := &achv1alpha1.BackendIdentityPolicyList{Items: tc.items}
			got := Resolve(list, tc.route)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("want nil, got %q", got.Name)
				}
				return
			}
			if got == nil {
				t.Fatalf("want %q, got nil", tc.wantWinner)
			}
			if got.Name != tc.wantWinner {
				t.Fatalf("want winner %q, got %q", tc.wantWinner, got.Name)
			}
			if got.Spec.ForwardIdentityJWT != tc.wantForward {
				t.Fatalf("want forward=%v, got %v", tc.wantForward, got.Spec.ForwardIdentityJWT)
			}
		})
	}
}
```

**Steps:**

1. Write the file, filling in all 10 cases.
2. `./scripts/dev.sh go test -run TestResolve ./internal/forwarder/bipresolver/`
   → expected RED (all non-nil cases fail; function returns nil today).
3. **Do not commit yet** — RED is the contract for Task A.4.

### Task A.4: Implement `Resolve` (GREEN phase)

**File:** `internal/forwarder/bipresolver/resolver.go` (replace TODO body).

**Body:**

```go
func Resolve(bips *achv1alpha1.BackendIdentityPolicyList, route RouteTarget) *achv1alpha1.BackendIdentityPolicy {
	if bips == nil || len(bips.Items) == 0 {
		return nil
	}
	matches := make([]*achv1alpha1.BackendIdentityPolicy, 0, len(bips.Items))
	for i := range bips.Items {
		t := bips.Items[i].Spec.Target
		if t.Kind == route.Kind && t.Name == route.Name {
			matches = append(matches, &bips.Items[i])
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})
	return matches[len(matches)-1]
}
```

**Steps:**

1. Replace the body.
2. `./scripts/dev.sh go test -run TestResolve ./internal/forwarder/bipresolver/`
   → expected GREEN, all 10 cases pass.
3. `./scripts/dev.sh make unit-pkg PKG=./internal/forwarder/bipresolver/...`
   → PASS.
4. `./scripts/dev.sh make lint-changed` → PASS.
5. Commit: `feat(forwarder): add BIP read-time resolver (alphabetically-LAST CR wins)`

### Task A.5: Property/randomized test for sort determinism

Goal: catch any accidental drift to a non-stable sort (e.g. swapping
`sort.Slice` for `sort.SliceStable` is fine here because names are
unique within a namespace, but a future refactor to a different key
must keep determinism).

**File:** `internal/forwarder/bipresolver/resolver_property_test.go`

**Skeleton:**

```go
// SPDX-License-Identifier: Apache-2.0

package bipresolver

import (
	"math/rand/v2"
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// TestResolveDeterministic shuffles a fixed input list N times and
// asserts the winner is identical every iteration.
func TestResolveDeterministic(t *testing.T) {
	base := []achv1alpha1.BackendIdentityPolicy{
		bip("alpha-bip", "MCPServer", "x", true),
		bip("middle-bip", "MCPServer", "x", false),
		bip("zz-last", "MCPServer", "x", true),
		bip("other-target", "MCPServer", "y", false),
	}
	route := RouteTarget{Kind: "MCPServer", Name: "x"}
	const iters = 100
	for i := 0; i < iters; i++ {
		shuffled := append([]achv1alpha1.BackendIdentityPolicy(nil), base...)
		rand.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		got := Resolve(&achv1alpha1.BackendIdentityPolicyList{Items: shuffled}, route)
		if got == nil || got.Name != "zz-last" {
			t.Fatalf("iter %d: want zz-last, got %v", i, got)
		}
	}
}
```

**Steps:**

1. Write the file.
2. `./scripts/dev.sh go test -run TestResolveDeterministic ./internal/forwarder/bipresolver/` → PASS.
3. Commit: `test(forwarder): assert resolver determinism across input order shuffles`

### Task A.6: Envtest — assert duplicate CRs leave Synced column empty

Goal: prove the operator-side invariant (no `DuplicateTarget` condition
ever appears) holds against the real informer pipeline, not just unit
mocks. This is the envtest equivalent of the demo expectation in
`examples/10-backendidentitypolicy-duplicate.yaml`.

**File:** `internal/controller/ach/backendidentitypolicy_duplicate_envtest_test.go`

**Pattern:** copy structure from
`internal/controller/ach/backendidentitypolicy_finalizer_test.go`.
Create two BIPs targeting the same `(MCPServer, context7)`, wait through
a reconcile cycle, then read both back and assert
`Status.Conditions` is `nil` or empty for both.

**Skeleton:**

```go
// SPDX-License-Identifier: Apache-2.0

// Envtest assertion of feedback_bip_no_shadow_logic.md: two BIPs on the
// same (kind, name) target both pass admission, both keep their
// finalizer, and NEITHER receives a Synced=DuplicateTarget condition —
// the operator stays dumb on duplicates by design. The Forwarder resolves
// the duplicate at READ time (see internal/forwarder/bipresolver/).

package ach

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func TestBackendIdentityPolicyDuplicateLeavesStatusEmpty(t *testing.T) {
	ctx := context.Background()
	pair := []*achv1alpha1.BackendIdentityPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "bip-dup-on", Namespace: WatchNamespace},
			Spec: achv1alpha1.BackendIdentityPolicySpec{
				Target:             achv1alpha1.BackendTargetRef{Kind: "MCPServer", Name: "dup-target"},
				ForwardIdentityJWT: true,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "zz-bip-dup-off", Namespace: WatchNamespace},
			Spec: achv1alpha1.BackendIdentityPolicySpec{
				Target:             achv1alpha1.BackendTargetRef{Kind: "MCPServer", Name: "dup-target"},
				ForwardIdentityJWT: false,
			},
		},
	}
	for _, cr := range pair {
		if err := k8sClient.Create(ctx, cr); err != nil {
			t.Fatalf("create %s: %v", cr.Name, err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, cr) })
	}

	// Allow a reconcile cycle. The Phase 1 reconciler only adds the
	// finalizer; no Status write should ever occur.
	time.Sleep(2 * time.Second)

	for _, cr := range pair {
		var got achv1alpha1.BackendIdentityPolicy
		if err := k8sClient.Get(ctx, client.ObjectKey{Name: cr.Name, Namespace: cr.Namespace}, &got); err != nil {
			t.Fatalf("get %s: %v", cr.Name, err)
		}
		if len(got.Status.Conditions) != 0 {
			t.Fatalf("CR %s: expected zero status conditions (no DuplicateTarget), got %d: %+v",
				cr.Name, len(got.Status.Conditions), got.Status.Conditions)
		}
	}
}
```

**Steps:**

1. Write the file.
2. `./scripts/dev.sh make envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestBackendIdentityPolicyDuplicateLeavesStatusEmpty`
   → PASS.
3. Commit: `test(controller): envtest — duplicate BIPs leave Synced column empty`

### Task A.7: Verify Layer A is shippable on its own

**Steps:**

1. `./scripts/dev.sh make unit` → PASS (full unit suite).
2. `./scripts/dev.sh make lint` → PASS (full sweep — SPDX headers, etc.).
3. `./scripts/dev.sh make envtest-run` → PASS (full envtest, including
   Task A.6).
4. `make pre-push` → all 15 gates PASS.
5. Open PR `feat/bip-resolver` → `main`. Title:
   `feat(forwarder): BIP read-time resolver (operator stays dumb on duplicates)`.
6. PR body MUST link the design memo path AND `examples/10-...yaml`
   so reviewers understand the contract isn't being unilaterally
   invented by this PR.

---

## Layer B — Wiring into route dispatcher (lands inside `feat/domain-port`)

Goal: have `/mcp/<name>` and `/a2a/<name>` request handlers call
`bipresolver.Resolve` against the cached BIP list, then mint+attach the
§9.1 ACH JWT iff the winner's `forwardIdentityJWT == true`. Layer B is
NOT shippable independently — it requires the Forwarder skeleton from
the §2 plan (Phase 4 — route dispatcher, JWT minting, upstream proxy).

### Pre-flight (Layer B)

Confirm BEFORE starting:

1. Layer A merged to `main` (PR from `feat/bip-resolver`).
2. `feat/domain-port` branched from `main` (rebased to include Layer A).
3. `docs/plans/2026-05-25-ach-domain-port.md` Phase 4 Task 4.1 ("port
   forwarder skeleton from ach-old/cmd/forwarder/main.go to
   cmd/ach/cmd/forwarder.go RunE") has landed — `runForwarder` is no
   longer the `/healthz`-only stub.
4. The §2 port has produced a `forwarder.Server` (or whatever the
   port names it) with a route dispatcher hook point for `/mcp/`
   and `/a2a/` prefixes.

If any of those is false, STOP. Land them first.

### Task B.1: Add a BIP cache provider to the Forwarder

The resolver is pure. Layer B needs a live, namespace-scoped, cached
`BackendIdentityPolicyList` it can pass into `Resolve` on every request.
The Forwarder reads from a controller-runtime cache (read-only client
or informer), NOT the API server, to keep request latency O(1).

**File:** `internal/forwarder/bipcache/bipcache.go` (new subpackage —
keeps the cache concern out of `bipresolver`, which stays pure).

**Sketch:**

```go
// SPDX-License-Identifier: Apache-2.0

// Package bipcache wires a controller-runtime cache (informer) for
// BackendIdentityPolicy CRs in the Forwarder's watched namespace.
// The package exists so internal/forwarder/bipresolver/ can stay pure
// (no client.Client dependency).

package bipcache

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// Provider exposes the live cached BIP list. List() is hot-path —
// callers MUST NOT block on it.
type Provider interface {
	List(ctx context.Context) (*achv1alpha1.BackendIdentityPolicyList, error)
}

// CachedProvider is the controller-runtime-backed implementation.
// The namespace is fixed at construction time and applied as a
// client.InNamespace ListOption.
type CachedProvider struct {
	c         client.Reader
	namespace string
}

func New(c client.Reader, namespace string) *CachedProvider {
	return &CachedProvider{c: c, namespace: namespace}
}

func (p *CachedProvider) List(ctx context.Context) (*achv1alpha1.BackendIdentityPolicyList, error) {
	var out achv1alpha1.BackendIdentityPolicyList
	if err := p.c.List(ctx, &out, client.InNamespace(p.namespace)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Ensure the controller-runtime cache is configured for BIPs.
// Callers wire this into the manager's cache.Cache.GetInformer flow
// at Forwarder startup so the first request doesn't pay a cold-start
// API-server roundtrip.
var _ cache.Cache // future hook; package import keeps the type accessible
```

**Steps:**

1. Create the package.
2. Unit test (`bipcache_test.go`): use a fake `client.Reader` (the
   `sigs.k8s.io/controller-runtime/pkg/client/fake` builder) to confirm
   `List` returns only BIPs in the configured namespace.
3. `./scripts/dev.sh make unit-pkg PKG=./internal/forwarder/bipcache/...` PASS.
4. Commit: `feat(forwarder): add bipcache.Provider for hot-path BIP list reads`

### Task B.2: Add a dispatcher helper that combines cache + resolver

**File:** `internal/forwarder/dispatch/dispatch.go` (or wherever the §2
port lands the route dispatcher — adapt the path to fit; if §2 has
already created `internal/forwarder/dispatch/`, add to it).

**Sketch:**

```go
// SPDX-License-Identifier: Apache-2.0

package dispatch

import (
	"context"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"

	"github.com/ackstorm/ach/internal/forwarder/bipcache"
	"github.com/ackstorm/ach/internal/forwarder/bipresolver"
)

// ResolveBIPForRoute is the single call site Layer B handlers use.
// Returns (winner, nil) where winner may be nil (no JWT mint).
// Returns (_, err) only on cache failure — request handlers MUST
// 503 on err rather than silently proceed without a policy decision.
func ResolveBIPForRoute(
	ctx context.Context,
	cache bipcache.Provider,
	route bipresolver.RouteTarget,
) (*achv1alpha1.BackendIdentityPolicy, error) {
	list, err := cache.List(ctx)
	if err != nil {
		return nil, err
	}
	return bipresolver.Resolve(list, route), nil
}
```

**Steps:**

1. Create the file.
2. Unit test with a fake `Provider` returning a deterministic list:
   confirm wiring (`cache.List` → `Resolve`) preserves the table-tested
   ordering.
3. `./scripts/dev.sh make unit-pkg PKG=./internal/forwarder/dispatch/...` PASS.
4. Commit: `feat(forwarder): add dispatch.ResolveBIPForRoute (cache + resolver glue)`

### Task B.3: Wire dispatcher into `/mcp/<name>` handler

This task edits whatever file the §2 port creates as the MCP route
handler. The contract is the same regardless of file location:

```go
// Inside the /mcp/<name> request handler, AFTER extracting <name>
// from the path but BEFORE any upstream call:
route := bipresolver.RouteTarget{Kind: "MCPServer", Name: bareName}
winner, err := dispatch.ResolveBIPForRoute(r.Context(), s.bipCache, route)
if err != nil {
	http.Error(w, "BIP cache unavailable", http.StatusServiceUnavailable)
	return
}

// §9.3 contract: always strip the client Authorization first.
r.Header.Del("Authorization")

if winner != nil && winner.Spec.ForwardIdentityJWT {
	jwt, err := s.jwtMinter.MintForRoute(r.Context(), route, /* sub, etc. */)
	if err != nil {
		http.Error(w, "JWT mint failed", http.StatusInternalServerError)
		return
	}
	r.Header.Set("Authorization", "Bearer "+jwt)
}
// ... proceed with upstream proxy
```

**Steps:**

1. Edit the MCP handler in the §2-ported tree.
2. Run focused envtest / unit on the handler.
3. Commit: `feat(forwarder): wire BIP resolver into /mcp/<name> dispatcher`

### Task B.4: Wire dispatcher into `/a2a/<name>` handler

Identical to B.3 with `Kind: "A2AAgent"` and `bareName` extracted from
the `/a2a/` prefix. Use the same `dispatch.ResolveBIPForRoute` call.

**Steps:**

1. Edit the A2A handler.
2. Commit: `feat(forwarder): wire BIP resolver into /a2a/<name> dispatcher`

### Task B.5: End-to-end test — rename flips the winner

**File:** `test/e2e/bip_duplicate_e2e_test.go`

Cluster pattern: kind + Helm (existing `make cluster-up` / `make
e2e-keep` flow; see `CLAUDE.md` "E2E debug loop"). Use the
`examples/09-...yaml` and `examples/10-...yaml` fixtures verbatim.

**Scenario:**

1. Bring up the cluster with operator + forwarder (Helm chart).
2. Apply `examples/09-backendidentitypolicy-context7.yaml` AND
   `examples/10-backendidentitypolicy-duplicate.yaml`.
3. Wait for both BIPs Ready via the blessed wait pattern. Note: BIPs
   don't carry a `Ready` condition (Phase 1 reconciler is finalizer-only,
   per memo), so the wait is simply "finalizer added" — use
   `make wait-cr-ready` with a condition expression that tolerates
   absent Status.Conditions, OR poll the finalizer with the bounded
   retry pattern from CLAUDE.md "Waiting for state". Prefer adding a
   new `make wait-bip-ready` target (`scripts/wait-bip-ready.sh`) that
   asserts the finalizer is present within `WAIT_TIMEOUT`.
4. Issue a synthetic request to `/mcp/context7` against the Forwarder
   (via port-forward or in-cluster curl pod — pattern is in
   `test/e2e/phase1_invariants_test.go`).
5. Assert the upstream-side observation: NO `Authorization` header
   reaches the upstream (because `zz-bip-context7-jwt-off` wins —
   alphabetically LAST — and its `forwardIdentityJWT: false`).
6. Rename the LAST CR — `kubectl get bip zz-bip-context7-jwt-off -o yaml
   | sed 's/zz-bip-context7-jwt-off/aa-bip-context7-jwt-off/' | kubectl
   apply -f -` (or delete+recreate). Now `bip-context7-jwt-on` is the
   alphabetically-LAST winner.
7. Allow informer resync (≤ default 30s; or use a focused wait on the
   new CR's finalizer).
8. Re-issue the request. Assert the upstream now sees
   `Authorization: Bearer eyJ...` with a valid §9.1 JWT.

**Acceptance:**
- Step 5 PASS (no JWT when zz-...-off wins).
- Step 8 PASS (JWT minted when aa-...-on wins).
- `kubectl get bip` between steps shows BOTH CRs with empty `SYNCED`
  column at every point (verifies the operator stayed dumb).

**Steps:**

1. Write the e2e test.
2. `./scripts/dev.sh make e2e-focus FOCUS="BIPDuplicate"` PASS.
3. Commit: `test(e2e): BIP rename flips forwarder dispatcher winner`

### Task B.6: Add an upstream-observable assertion helper

Layer B e2e needs a way to see what `Authorization` header the upstream
received. Options:

- Stand up a tiny "echo" upstream pod in the test namespace that logs
  inbound headers, then `kubectl logs` and grep. Simplest, no new code.
- Add a Forwarder-internal observability hook (metric or structured log
  line) — heavier; only adopt if the echo-pod pattern proves flaky.

**Recommended:** echo-pod (`hashicorp/http-echo` or `mendhak/http-https-echo`).
Bake the YAML into `test/e2e/fixtures/upstream-echo.yaml`. The
`BIPDuplicate` test deploys it, points the `MCPServer/context7` route
at it (via a `Service` + `ExternalName` indirection, or via a forwarder
config override env var the §2 port introduces).

**Steps:**

1. Write `test/e2e/fixtures/upstream-echo.yaml`.
2. Helper function `assertUpstreamAuthHeader(t, ns, wantPrefix string)`
   in the test file: kubectl-logs the echo pod, grep for
   `^Authorization: ` line, assert prefix.
3. Commit: `test(e2e): add echo-upstream fixture for header-observability assertions`

---

## Task 6 — Documentation amendments (lands with Layer A)

### Task 6.1: Amend `CLAUDE.md` "Repository-specific patterns" section

Add a new bullet AFTER the existing `govulncheck ack-list` bullet:

```markdown
- **BIP duplicate resolution is read-time, operator-dumb**: the
  `BackendIdentityPolicy` reconciler does NOT do conflict resolution
  for duplicate `(target.kind, target.name)` pairs. The Forwarder
  resolves duplicates at READ time via
  `internal/forwarder/bipresolver/`: list all matching BIPs, sort by
  `metadata.name` ASC, take `Items[len-1]`. Operators wanting different
  precedence rename their CRs (`zz-` prefix flips the winner). Do NOT
  add a `DuplicateTarget` reconciler, a `Synced=DuplicateTarget`
  condition, or any shadow-flip logic — this is an explicit design
  choice (see exemplar pair: `examples/09-...yaml` +
  `examples/10-...yaml`).
```

**Steps:**

1. Edit `CLAUDE.md`.
2. Confirm with `git diff CLAUDE.md` that the diff is just the new
   bullet.
3. Commit: `docs(claude): note BIP duplicate resolution is read-time, operator-dumb`

### Task 6.2: Scrub stale "Phase 4 DuplicateTarget" comments

**Files to scrub:**

- `internal/controller/ach/backendidentitypolicy_controller.go` — three
  occurrences of "Phase 4 can layer real Synced=DuplicateTarget
  reconciliation" / "the §6.6 BackendIdentityPolicy-specific
  Synced=DuplicateTarget reason is a Phase 4 reconciliation outcome" /
  "Steady state — no status write in Phase 1 (Synced=DuplicateTarget
  is Phase 4's owner...)". Replace each with language matching the
  design memo: "the operator never writes a DuplicateTarget condition
  — the Forwarder resolves duplicates at read time (see
  `internal/forwarder/bipresolver/`)".

**Steps:**

1. Edit the controller file, rewording each comment.
2. `./scripts/dev.sh make lint-changed` PASS.
3. `./scripts/dev.sh make unit-pkg PKG=./internal/controller/ach/...` PASS
   (no test changes expected — comments only).
4. Sweep planning corpus for the same language:
   ```bash
   grep -rn "DuplicateTarget\|shadow.*BIP\|alphabetically.*lower" \
     docs/ references/ .planning/ 2>/dev/null
   ```
   Update any hits to match the new contract (or note they are
   historical and add a `(superseded 2026-05-26)` marker).
5. Commit: `docs(controller): scrub stale Phase 4 DuplicateTarget language`

### Task 6.3: Cross-reference this plan from the §2 domain-port plan

Append a paragraph to
`docs/plans/2026-05-25-ach-domain-port.md` (Phase 4 introduction)
referencing this plan as the authoritative source for the BIP wiring
tasks Layer B will pick up:

```markdown
> **BIP read-path note:** the BackendIdentityPolicy read-time resolver
> ships independently in
> `docs/plans/2026-05-26-bip-forwarder-read-path.md` Layer A. Phase 4
> here MUST consume that resolver via `internal/forwarder/bipresolver/`
> rather than re-deriving the algorithm; the §2 port does NOT bring
> any DuplicateTarget reconciler from ach-old (verified absent
> 2026-05-26). See Layer B tasks in the BIP plan for the wiring spec.
```

**Steps:**

1. Edit the §2 plan.
2. Commit: `docs(plans): cross-link domain-port plan to BIP read-path plan`

---

## Acceptance checklist (run before merging each layer)

### Layer A PR (`feat/bip-resolver` → `main`)

- [ ] `./scripts/dev.sh make unit` PASS
- [ ] `./scripts/dev.sh make lint` PASS
- [ ] `./scripts/dev.sh make envtest-run` PASS (includes Task A.6)
- [ ] `./scripts/dev.sh make security` PASS (gosec + govulncheck)
- [ ] `make pre-push` PASS (all 15 gates)
- [ ] `kubectl get bip` on the exemplar pair (manual smoke against a
      kind cluster) shows BOTH CRs with empty `SYNCED` column
- [ ] `CLAUDE.md` updated (Task 6.1)
- [ ] Stale `Phase 4 DuplicateTarget` comments scrubbed (Task 6.2)
- [ ] §2 plan cross-referenced (Task 6.3)
- [ ] PR body links the design memo path AND `examples/10-...yaml`

### Layer B (lands in `feat/domain-port` PR; verify before that PR ships)

- [ ] Forwarder route dispatcher (`/mcp/<name>` AND `/a2a/<name>`) calls
      `dispatch.ResolveBIPForRoute`
- [ ] `kubectl get bip` on duplicate pair shows BOTH CRs with empty
      `SYNCED` column at every point in the e2e
- [ ] e2e test (Task B.5) PASS: rename the alphabetically-LAST CR;
      Forwarder behavior tracks the new winner's `forwardIdentityJWT`
- [ ] No `DuplicateTarget` reason or condition is ever written to any
      BIP `Status` during the e2e (assert via `kubectl get bip ... -o
      jsonpath='{.status.conditions}'` between every step)
- [ ] Phase 4 of the §2 plan still passes its own acceptance gates
      (this plan's Layer B does not regress §2 deliverables)

---

## Out of scope (explicitly NOT in this plan)

- **Operator-side BIP conflict reconciliation.** The whole point of
  this plan is to NOT build that. If a future feature request asks
  for it, reject and reference `feedback_bip_no_shadow_logic.md`.
- **`Status.Conditions` writes on BIPs in any form.** Reconciler stays
  finalizer-only.
- **CRD field changes.** The Spec/Status shape in
  `backendidentitypolicy_types.go` is already correct; the only
  doc-comment touch in this plan is Task 6.2 (controller comments,
  not API types).
- **JWT minting itself.** Layer B assumes the §2 port has produced
  a `JWTMinter`; if it hasn't, that's a §2 task, not this plan's.
- **`MCPServer` / `A2AAgent` CRD scaffolding.** BIPs target backends
  by bare name; the target CRD does not need to exist for the policy to
  apply (per the §9.3 contract, repeated in `examples/09-...yaml`
  preamble). Adding those CRDs is a different feature.
- **Renaming the existing `bip` shortName.** The CRD already exposes
  `bip` (`+kubebuilder:resource:scope=Namespaced,shortName=bip`); no
  change.

---

## Risk register

| Risk                                                                    | Mitigation                                                                                                       |
|-------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------|
| Future agent re-introduces a `DuplicateTarget` reconciler "for tidiness"| Task 6.1 amends CLAUDE.md with an explicit prohibition; Task 6.2 scrubs the stale invitation                     |
| Layer B handler order accidentally swaps "strip then mint" with "mint then strip" | The handler sketch in Task B.3 strips Authorization FIRST, unconditionally; reviewers MUST flag deviations |
| Cache cold-start latency on first request                               | `bipcache.New` is wired into manager startup so the informer is warm before the listener accepts traffic         |
| Informer lag after CR rename hides the new winner from the e2e          | Task B.5 step 7 explicitly waits for informer resync OR a focused finalizer-presence wait — no naked polling     |
| Operator namespace ≠ Forwarder watched namespace                        | `bipcache.New(c, namespace)` is namespace-scoped; the §2 port MUST pass the same namespace the reconciler watches|
| Stale `Synced=DuplicateTarget` printcolumn confuses operators           | The CRD printcolumn currently shows `Synced` — which stays empty by design. Acceptable; documented in `examples/10-...yaml`. Do NOT remove the column (would be a CRD-breaking change for any future condition type that DOES write `Synced`) |

---

## Time estimate

- Layer A: ~90 min wall-clock (resolver + tests + envtest + scrubs + PR)
- Layer B: ~120 min wall-clock once §2 Phase 4 skeleton is in place

Total: ~3.5 hours of focused work, split across two PRs at two
different times.

---

## Phase-level commit count target

- Layer A: 7 commits (A.1, A.4, A.5, A.6, 6.1, 6.2, 6.3 — A.2/A.3 fold
  into A.4 per TDD)
- Layer B: 6 commits (B.1, B.2, B.3, B.4, B.5, B.6)

Total: 13 commits across the two layers.
