// SPDX-License-Identifier: Apache-2.0

// Envtest fake LiteLLM for the §7 AccessGroupSynced reconciler tests.
// The fake delegates to NoopClient for non-§7 methods; CreateAccessGroup,
// BindTeamToAccessGroup, and ListAccessGroupBindings are tallied + can
// optionally inject errors. Each test resets via accessGroupFake.Reset()
// before driving the reconciler.

package ach

import (
	"context"
	"errors"
	"sync"

	"github.com/go-logr/logr"

	"github.com/ackstorm/ach/internal/litellm"
)

// accessGroupFakeImpl is the per-suite singleton. Tests interact via the
// package-level `accessGroupFake` variable (initialized in TestMain).
type accessGroupFakeImpl struct {
	*litellm.NoopClient

	mu sync.Mutex

	// Per-(env, team) call counters.
	createCalls map[string]int
	bindCalls   map[string]map[string]int
	listCalls   map[string]int

	// Injection knobs. Set BEFORE creating the Environment CR.
	createErrByEnv map[string]error
	bindErrByPair  map[string]map[string]error
	bindings       map[string][]string
}

func newAccessGroupFake() *accessGroupFakeImpl {
	return &accessGroupFakeImpl{
		NoopClient:     litellm.NewNoopClient(logr.Discard()),
		createCalls:    map[string]int{},
		bindCalls:      map[string]map[string]int{},
		listCalls:      map[string]int{},
		createErrByEnv: map[string]error{},
		bindErrByPair:  map[string]map[string]error{},
		bindings:       map[string][]string{},
	}
}

func (f *accessGroupFakeImpl) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = map[string]int{}
	f.bindCalls = map[string]map[string]int{}
	f.listCalls = map[string]int{}
	f.createErrByEnv = map[string]error{}
	f.bindErrByPair = map[string]map[string]error{}
	f.bindings = map[string][]string{}
}

func (f *accessGroupFakeImpl) CreateAccessGroup(_ context.Context, name string, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls[name]++
	if err := f.createErrByEnv[name]; err != nil {
		return err
	}
	return nil
}

func (f *accessGroupFakeImpl) BindTeamToAccessGroup(_ context.Context, accessGroup, teamID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bindCalls[accessGroup]; !ok {
		f.bindCalls[accessGroup] = map[string]int{}
	}
	f.bindCalls[accessGroup][teamID]++
	if m := f.bindErrByPair[accessGroup]; m != nil {
		if err := m[teamID]; err != nil {
			return err
		}
	}
	already := false
	for _, t := range f.bindings[accessGroup] {
		if t == teamID {
			already = true
			break
		}
	}
	if !already {
		f.bindings[accessGroup] = append(f.bindings[accessGroup], teamID)
	}
	return nil
}

func (f *accessGroupFakeImpl) ListAccessGroupBindings(_ context.Context, accessGroup string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls[accessGroup]++
	if existing := f.bindings[accessGroup]; existing != nil {
		out := make([]string, len(existing))
		copy(out, existing)
		return out, nil
	}
	return nil, nil
}

func (f *accessGroupFakeImpl) CreateCallsFor(env string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls[env]
}

func (f *accessGroupFakeImpl) BindCallsFor(env, team string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bindCalls[env][team]
}

func (f *accessGroupFakeImpl) ListCallsFor(env string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls[env]
}

func (f *accessGroupFakeImpl) InjectCreateErr(env string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createErrByEnv[env] = err
}

func (f *accessGroupFakeImpl) InjectBindErr(env, team string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bindErrByPair[env]; !ok {
		f.bindErrByPair[env] = map[string]error{}
	}
	f.bindErrByPair[env][team] = err
}

// SeedBinding pretends a prior reconcile already bound the team — used
// by the idempotency / drift tests.
func (f *accessGroupFakeImpl) SeedBinding(env, team string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindings[env] = append(f.bindings[env], team)
}

var _ litellm.Client = (*accessGroupFakeImpl)(nil)

// errFakeBindFailed is a stable error string for negative-path tests.
var errFakeBindFailed = errors.New("fake: bind failed")
