// SPDX-License-Identifier: Apache-2.0

package adapter

import (
	"fmt"
	"strings"
	"sync"
)

// registry is the canonical-ID → Adapter map; aliasIndex is the
// lowercase-alias → canonical-ID map. Both are guarded by mu — every
// Register call acquires the write lock and every Lookup / Iter call
// acquires the read lock.
//
// Population happens via init() side-effect in each adapter subpackage
// (CONTEXT.md D-07 + PATTERNS.md "init-registered registry"). A
// cobra layer (plan 07-W3-05) blank-imports each subpackage so the
// init() calls fire before main() reaches autodetection.
var (
	mu         sync.RWMutex
	registry   = map[string]Adapter{}
	aliasIndex = map[string]string{}
)

// Register adds a to the registry, indexing by canonical ID and every
// case-folded alias. Panics on duplicate canonical ID OR duplicate
// alias (across any previously-registered adapter). The panic is the
// right discipline for init-time wiring: a duplicate registration is a
// program bug, not a recoverable error, and surfaces immediately at
// process start.
//
// Aliases are case-folded via strings.ToLower at registration time so
// Lookup needs to fold the input exactly once. Empty IDs or aliases
// are also rejected via panic (the contract requires non-empty
// identifiers).
func Register(a Adapter) {
	if a == nil {
		panic("adapter.Register: nil Adapter")
	}
	id := a.ID()
	if id == "" {
		panic("adapter.Register: empty ID")
	}

	mu.Lock()
	defer mu.Unlock()

	if _, exists := registry[id]; exists {
		panic(fmt.Sprintf("adapter.Register: duplicate ID %q", id))
	}
	// Also reject when the new ID collides with a previously-registered
	// alias — the alias index is a flat namespace shared with canonical
	// IDs.
	if existing, exists := aliasIndex[strings.ToLower(id)]; exists {
		panic(fmt.Sprintf("adapter.Register: ID %q collides with existing alias of %q", id, existing))
	}

	// Validate aliases before mutating any state — partial registration
	// would leave the registry in an inconsistent shape.
	aliases := a.Aliases()
	for _, alias := range aliases {
		if alias == "" {
			panic(fmt.Sprintf("adapter.Register: empty alias for ID %q", id))
		}
		folded := strings.ToLower(alias)
		if existing, exists := aliasIndex[folded]; exists {
			panic(fmt.Sprintf("adapter.Register: alias %q for ID %q collides with adapter %q", alias, id, existing))
		}
		if _, exists := registry[folded]; exists && folded != id {
			panic(fmt.Sprintf("adapter.Register: alias %q for ID %q collides with canonical ID of another adapter", alias, id))
		}
	}

	registry[id] = a
	for _, alias := range aliases {
		aliasIndex[strings.ToLower(alias)] = id
	}
	aliasIndex[strings.ToLower(id)] = id
}

// Lookup case-folds id, alias-resolves it to a canonical ID, and
// returns the registered Adapter. Returns (nil, false) when no
// registered adapter matches.
//
// Resolution: (1) canonical or alias via the case-folded index; (2)
// return false. aliasIndex carries every canonical ID's own folded form
// too (indexed at Register time), so this single lookup covers both.
func Lookup(id string) (Adapter, bool) {
	if id == "" {
		return nil, false
	}
	folded := strings.ToLower(id)

	mu.RLock()
	defer mu.RUnlock()

	if canonical, ok := aliasIndex[folded]; ok {
		if a, ok := registry[canonical]; ok {
			return a, true
		}
	}

	return nil, false
}

// Iter returns a snapshot of every registered Adapter. The slice is a
// fresh allocation so the caller may mutate it without racing
// Register. Used by the autodetection layer (plan 07-W3-05) to ask
// every adapter "do you see your signals at <root>?" — the
// per-adapter Detect call is independent and may be parallelized by
// the caller.
func Iter() []Adapter {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]Adapter, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	return out
}

// resetForTesting clears the registry. Test-only helper, unexported.
// External packages cannot reach for it; the registry's
// init-time-only mutation discipline stays intact at runtime.
func resetForTesting() { //nolint:unused // used by registry_test.go
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]Adapter{}
	aliasIndex = map[string]string{}
}
