// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/ackstorm/ach/internal/litellm"
)

// fakeLiteLLM is a minimal Client implementation used by Snapshotter
// unit tests. Fields are read on every list call; tests mutate them
// between refreshes to drive the stale-preservation / partial-error
// branches. modelsErr/mcpsErr/agentsErr override the success-path
// return when non-nil — they take precedence over the slice fields.
//
// callCount tracks per-call invocations so the ctx-cancel test can
// assert that the ticker fired at least once after Start.
type fakeLiteLLM struct {
	mu         sync.Mutex
	models     []litellm.ModelInfoResponse
	mcps       []litellm.MCPServerEntry
	agents     []litellm.AgentEntry
	teams      []litellm.TeamListEntry
	modelsErr  error
	mcpsErr    error
	agentsErr  error
	teamsErr   error
	modelCalls atomic.Int64
}

func (f *fakeLiteLLM) DeleteAccessGroup(_ context.Context, _ string) error { return nil }
func (f *fakeLiteLLM) DeleteTag(_ context.Context, _ string) error         { return nil }

func (f *fakeLiteLLM) ListModels(_ context.Context) ([]litellm.ModelInfoResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelCalls.Add(1)
	return f.models, f.modelsErr
}
func (f *fakeLiteLLM) ListMCPServers(_ context.Context) ([]litellm.MCPServerEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mcps, f.mcpsErr
}
func (f *fakeLiteLLM) ListA2AAgents(_ context.Context) ([]litellm.AgentEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agents, f.agentsErr
}
func (f *fakeLiteLLM) ListUserKeys(_ context.Context, _ string) ([]litellm.UserKeyInfo, error) {
	return nil, nil
}
func (f *fakeLiteLLM) RevokeKey(_ context.Context, _ string) error { return nil }

// Phase 3 Plan 03-01 — interface widened. Snapshotter does not invoke
// these methods; stub them to satisfy the litellm.Client interface.
func (f *fakeLiteLLM) UserNew(_ context.Context, _ *litellm.UserNewRequest) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *fakeLiteLLM) UserInfoByEmail(_ context.Context, _ string) (*litellm.UserInfo, error) {
	return nil, nil
}
func (f *fakeLiteLLM) TeamMemberAdd(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeLiteLLM) KeyGenerate(_ context.Context, _ *litellm.KeyGenerateRequest) (*litellm.KeyGenerateResponse, error) {
	return nil, nil
}

// setErr atomically mutates all four error returns to the same value.
// Used by tests that want to drive every list call into the unreachable
// branch simultaneously.
func (f *fakeLiteLLM) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelsErr = err
	f.mcpsErr = err
	f.agentsErr = err
	f.teamsErr = err
}

// TestSnapshotter_ColdStart_ReturnsEmpty asserts that calling Snapshot()
// before Start has populated anything returns the zero value with all
// maps nil and Stale=false. The EnvironmentReconciler treats this as
// "every spec entry unresolved" — the correct first-reconcile behavior.
func TestSnapshotter_ColdStart_ReturnsEmpty(t *testing.T) {
	s := NewSnapshotter(&fakeLiteLLM{}, logr.Discard())
	snap := s.Snapshot()
	if snap.Models != nil {
		t.Errorf("cold start: Models = %v; want nil", snap.Models)
	}
	if snap.MCPServers != nil {
		t.Errorf("cold start: MCPServers = %v; want nil", snap.MCPServers)
	}
	if snap.A2AAgents != nil {
		t.Errorf("cold start: A2AAgents = %v; want nil", snap.A2AAgents)
	}
	if snap.Stale {
		t.Error("cold start: Stale = true; want false (cold start is distinct from stale)")
	}
	if !snap.RefreshedAt.IsZero() {
		t.Errorf("cold start: RefreshedAt = %v; want zero", snap.RefreshedAt)
	}
	if got := s.LiteLLMUnreachableCount(); got != 0 {
		t.Errorf("cold start: LiteLLMUnreachableCount = %d; want 0", got)
	}
}

// TestSnapshotter_FirstRefreshSuccess covers the steady-state success
// path: every list call returns data, the snapshot is published with
// Stale=false, RefreshedAt is non-zero, and the unreachable counter
// stays at 0.
func TestSnapshotter_FirstRefreshSuccess(t *testing.T) {
	fake := &fakeLiteLLM{
		models: []litellm.ModelInfoResponse{
			{ModelName: "claude-sonnet-4-5"},
			{ModelName: "gpt-5"},
		},
		mcps: []litellm.MCPServerEntry{
			{ServerName: "filesystem"},
		},
		agents: []litellm.AgentEntry{
			{AgentName: "researcher"},
		},
	}
	s := NewSnapshotter(fake, logr.Discard())
	before := time.Now()
	s.refresh(context.Background())
	snap := s.Snapshot()

	if got := len(snap.Models); got != 2 {
		t.Fatalf("Models length = %d; want 2", got)
	}
	if _, ok := snap.Models["claude-sonnet-4-5"]; !ok {
		t.Error("Models missing claude-sonnet-4-5")
	}
	if _, ok := snap.Models["gpt-5"]; !ok {
		t.Error("Models missing gpt-5")
	}
	if got := len(snap.MCPServers); got != 1 {
		t.Errorf("MCPServers length = %d; want 1", got)
	}
	if _, ok := snap.MCPServers["filesystem"]; !ok {
		t.Error("MCPServers missing filesystem")
	}
	if got := len(snap.A2AAgents); got != 1 {
		t.Errorf("A2AAgents length = %d; want 1", got)
	}
	if _, ok := snap.A2AAgents["researcher"]; !ok {
		t.Error("A2AAgents missing researcher")
	}
	if snap.Stale {
		t.Error("Stale = true after successful refresh; want false")
	}
	if snap.RefreshedAt.Before(before) {
		t.Errorf("RefreshedAt = %v; want >= %v", snap.RefreshedAt, before)
	}
	if got := s.LiteLLMUnreachableCount(); got != 0 {
		t.Errorf("LiteLLMUnreachableCount = %d after successful refresh; want 0", got)
	}
}

// TestSnapshotter_FirstRefreshLiteLLMUnreachable covers the D-14
// initial-failure branch: the first ever refresh errors, no prior
// snapshot exists, so an EMPTY Stale snapshot is published. The
// unreachable counter increments to 1.
func TestSnapshotter_FirstRefreshLiteLLMUnreachable(t *testing.T) {
	fake := &fakeLiteLLM{}
	fake.setErr(errors.New("network: dial tcp: connection refused"))
	s := NewSnapshotter(fake, logr.Discard())
	s.refresh(context.Background())
	snap := s.Snapshot()

	if !snap.Stale {
		t.Error("first-refresh failure: Stale = false; want true")
	}
	if len(snap.Models) != 0 {
		t.Errorf("first-refresh failure: Models length = %d; want 0", len(snap.Models))
	}
	if len(snap.MCPServers) != 0 {
		t.Errorf("first-refresh failure: MCPServers length = %d; want 0", len(snap.MCPServers))
	}
	if len(snap.A2AAgents) != 0 {
		t.Errorf("first-refresh failure: A2AAgents length = %d; want 0", len(snap.A2AAgents))
	}
	if got := s.LiteLLMUnreachableCount(); got != 1 {
		t.Errorf("LiteLLMUnreachableCount = %d after first-refresh failure; want 1", got)
	}
}

// TestSnapshotter_RefreshAfterPriorSuccess_LiteLLMUnreachable covers
// D-14's load-bearing branch: a successful refresh has populated the
// snapshot; the NEXT refresh fails; the prior maps are PRESERVED with
// Stale=true flipped. The counter increments to 1.
func TestSnapshotter_RefreshAfterPriorSuccess_LiteLLMUnreachable(t *testing.T) {
	fake := &fakeLiteLLM{
		models: []litellm.ModelInfoResponse{
			{ModelName: "m1"},
			{ModelName: "m2"},
		},
	}
	s := NewSnapshotter(fake, logr.Discard())
	s.refresh(context.Background())

	// First snapshot should have the two models, not stale.
	first := s.Snapshot()
	if len(first.Models) != 2 || first.Stale {
		t.Fatalf("setup: first snapshot Models=%d, Stale=%v; want 2, false",
			len(first.Models), first.Stale)
	}
	priorRefreshAt := first.RefreshedAt

	// Mutate fake to fail and refresh again.
	fake.setErr(errors.New("upstream 503"))
	s.refresh(context.Background())

	second := s.Snapshot()
	if !second.Stale {
		t.Error("second refresh after failure: Stale = false; want true")
	}
	if len(second.Models) != 2 {
		t.Fatalf("second refresh: Models length = %d; want 2 (prior preserved)", len(second.Models))
	}
	if _, ok := second.Models["m1"]; !ok {
		t.Error("second refresh: m1 missing from preserved snapshot")
	}
	if _, ok := second.Models["m2"]; !ok {
		t.Error("second refresh: m2 missing from preserved snapshot")
	}
	if !second.RefreshedAt.Equal(priorRefreshAt) {
		t.Errorf("second refresh: RefreshedAt mutated to %v; want preserved %v",
			second.RefreshedAt, priorRefreshAt)
	}
	if got := s.LiteLLMUnreachableCount(); got != 1 {
		t.Errorf("LiteLLMUnreachableCount = %d after one failed refresh; want 1", got)
	}
}

// TestSnapshotter_ErrNotFoundIsEmptyNotError covers the D-13 /
// Plan 02-01 contract: ErrNotFound from any of the three list calls
// is downgraded to an empty slice. The snapshot is fresh (not stale)
// and the counter does NOT increment.
func TestSnapshotter_ErrNotFoundIsEmptyNotError(t *testing.T) {
	fake := &fakeLiteLLM{
		modelsErr: litellm.ErrNotFound,
		// mcps / agents return (nil, nil) — the empty-set success path.
	}
	s := NewSnapshotter(fake, logr.Discard())
	s.refresh(context.Background())
	snap := s.Snapshot()

	if snap.Stale {
		t.Error("ErrNotFound triggered Stale=true; want false (ErrNotFound is not an error)")
	}
	if len(snap.Models) != 0 {
		t.Errorf("Models length = %d; want 0 (ErrNotFound downgrade)", len(snap.Models))
	}
	if got := s.LiteLLMUnreachableCount(); got != 0 {
		t.Errorf("LiteLLMUnreachableCount = %d after ErrNotFound-only refresh; want 0", got)
	}
}

// TestSnapshotter_PartialError_OneOfThreeFails asserts D-14's
// atomic-shape rule: if any of the three list calls fails with a
// non-ErrNotFound error, the entire tick is treated as a refresh
// failure. We do NOT merge partial results into the snapshot.
func TestSnapshotter_PartialError_OneOfThreeFails(t *testing.T) {
	fake := &fakeLiteLLM{
		models: []litellm.ModelInfoResponse{{ModelName: "m1"}},
		// mcps succeeds with empty.
		mcpsErr: errors.New("mcp upstream 500"),
		// agents succeeds with empty.
	}
	s := NewSnapshotter(fake, logr.Discard())
	s.refresh(context.Background())
	snap := s.Snapshot()

	if !snap.Stale {
		t.Error("partial error: Stale = false; want true (any-failure rule)")
	}
	if got := s.LiteLLMUnreachableCount(); got != 1 {
		t.Errorf("LiteLLMUnreachableCount = %d after partial-error refresh; want 1", got)
	}
}

// TestSnapshotter_ConcurrentReads exercises the atomic.Pointer
// publication-safety contract under -race: 100 reader goroutines call
// Snapshot() in a tight loop while 1 writer goroutine repeatedly
// invokes refresh. No data race, no panic, and every read returns a
// consistent snapshot (either prior or new — never torn).
func TestSnapshotter_ConcurrentReads(t *testing.T) {
	fake := &fakeLiteLLM{
		models: []litellm.ModelInfoResponse{
			{ModelName: "m1"},
			{ModelName: "m2"},
		},
	}
	s := NewSnapshotter(fake, logr.Discard())
	s.refresh(context.Background())

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					snap := s.Snapshot()
					// Either nil (impossible after the initial refresh
					// above) or a population consistent with the writer's
					// successive refreshes — never a torn map.
					if snap.Models != nil && len(snap.Models) != 2 {
						t.Errorf("torn read: Models length = %d", len(snap.Models))
						return
					}
				}
			}
		}()
	}

	// Single writer drives 100 refreshes back-to-back.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.refresh(context.Background())
		}
		close(stop)
	}()

	wg.Wait()
}

// TestSnapshotter_StartRespectsCtxCancel asserts the manager.Runnable
// lifecycle contract: Start returns nil when ctx is canceled, and at
// least the initial refresh (plus typically a ticker fire) has run.
func TestSnapshotter_StartRespectsCtxCancel(t *testing.T) {
	fake := &fakeLiteLLM{}
	s := NewSnapshotter(fake, logr.Discard())
	s.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- s.Start(ctx) }()

	// Sleep long enough for the initial refresh + at least 1 ticker fire.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start returned %v; want nil on ctx cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return within 1s of ctx cancel")
	}

	// Initial refresh + ≥1 ticker fire = ≥2 ListModels calls.
	if got := fake.modelCalls.Load(); got < 2 {
		t.Errorf("ListModels call count = %d; want >= 2 (initial + ≥1 ticker fire)", got)
	}
}

// TestSnapshotter_FailureFollowedBySuccess_FastRetry covers issue #30:
// when the initial refresh fails (typically because the LiteLLMConnection
// reconciler hasn't completed its first probe yet and the lazy connection
// returns ErrNotReady), the Snapshotter must retry on its backoff
// cadence rather than waiting a full s.interval. We seed the fake with
// an error, then clear it asynchronously, and assert the snapshot flips
// to Stale=false well inside one steady-state interval.
func TestSnapshotter_FailureFollowedBySuccess_FastRetry(t *testing.T) {
	fake := &fakeLiteLLM{
		models: []litellm.ModelInfoResponse{{ModelName: "m1"}},
	}
	fake.setErr(errors.New("litellm connection not ready: Connecting"))
	s := NewSnapshotter(fake, logr.Discard())
	// Pick a steady interval large enough that — if the retry-on-failure
	// path is broken (i.e. Start waits a full s.interval after a failed
	// refresh, the pre-fix behavior) — this test would have to wait 5s
	// before the second refresh, blowing past the 1s assertion below
	// and failing loudly. With the fix, the second refresh fires after
	// initialRetryBackoff (1s).
	s.interval = 5 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- s.Start(ctx) }()

	// First refresh runs synchronously in Start before the first sleep,
	// so the stale-empty snapshot lands within microseconds. Confirm.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if snap := s.Snapshot(); snap.Stale {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !s.Snapshot().Stale {
		t.Fatal("setup: initial refresh did not publish a stale snapshot")
	}

	// Clear the error — the next refresh must succeed.
	fake.setErr(nil)

	// With initialRetryBackoff=1s, the next refresh fires ~1s after
	// the initial failure. Allow a comfortable margin but cap WELL
	// below s.interval (5s) so we'd fail loudly if Start were waiting
	// the full interval.
	pollDeadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(pollDeadline) {
		snap := s.Snapshot()
		if !snap.Stale && len(snap.Models) == 1 {
			return // success — fast-retry recovered within 2.5s
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := s.Snapshot()
	t.Fatalf("snapshot did not recover within %v: Stale=%v Models=%d",
		2500*time.Millisecond, snap.Stale, len(snap.Models))
}

// TestSnapshotter_TeamsLandInSnapshot asserts that team aliases from
// ListAllTeams are stored in LiteLLMSnapshot.Teams after a successful
// refresh. Empty-alias entries must be skipped (toSet contract).
func TestSnapshotter_TeamsLandInSnapshot(t *testing.T) {
	fake := &fakeLiteLLM{
		teams: []litellm.TeamListEntry{
			{TeamAlias: "default"},
			{TeamAlias: ""}, // empty alias — must be skipped
			{TeamAlias: "alpha"},
		},
	}
	s := NewSnapshotter(fake, logr.Discard())
	if ok := s.refresh(context.Background()); !ok {
		t.Fatalf("refresh returned false on healthy fake client")
	}
	snap := s.Snapshot()

	if got := len(snap.Teams); got != 2 {
		t.Fatalf("Teams length = %d; want 2 (empty alias skipped)", got)
	}
	if _, ok := snap.Teams["default"]; !ok {
		t.Error("Teams missing 'default'")
	}
	if _, ok := snap.Teams["alpha"]; !ok {
		t.Error("Teams missing 'alpha'")
	}
	if _, ok := snap.Teams[""]; ok {
		t.Error("Teams contains empty alias; toTeamSet must skip it")
	}
}

// TestEnableCatalog_NilPoolIsInert asserts that EnableCatalog records
// the connector identity and is chainable, but that a nil pool leaves
// refresh behaviour completely unchanged (existing unit tests stay green
// and the snapshot is published normally).
func TestEnableCatalog_NilPoolIsInert(t *testing.T) {
	f := &fakeLiteLLM{models: []litellm.ModelInfoResponse{{ModelName: "gpt-4o"}}}
	s := NewSnapshotter(f, logr.Discard()).EnableCatalog(nil, "ach-system", "default")

	if s.catalogNS != "ach-system" || s.connectorName != "default" {
		t.Fatalf("EnableCatalog did not record connector identity: %+v", s)
	}
	// refresh must still succeed and publish the snapshot with a nil pool.
	if ok := s.refresh(context.Background()); !ok {
		t.Fatalf("refresh returned false on healthy fake client")
	}
	if _, ok := s.Snapshot().Models["gpt-4o"]; !ok {
		t.Fatalf("snapshot missing gpt-4o after refresh")
	}
}

// ListTeamsByAlias is a no-op shim — Client interface compliance.
func (f *fakeLiteLLM) ListTeamsByAlias(_ context.Context, _ string) ([]litellm.TeamListEntry, error) {
	return nil, nil
}

// ListAllTeams returns the configured teams slice (or error).
func (f *fakeLiteLLM) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.teams, f.teamsErr
}

// EnsureDefaultTeam is a no-op shim — Client interface compliance.
func (f *fakeLiteLLM) EnsureDefaultTeam(_ context.Context) error { return nil }

// CreateTeam/UpdateTeam/DeleteTeam/GetTeamInfo are no-op shims — Client
// interface compliance.
func (f *fakeLiteLLM) CreateTeam(_ context.Context, _ *litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) UpdateTeam(_ context.Context, _ *litellm.TeamUpdateRequest) (*litellm.TeamListEntry, error) {
	return nil, nil
}
func (f *fakeLiteLLM) DeleteTeam(_ context.Context, _ string) error { return nil }
func (f *fakeLiteLLM) GetTeamInfo(_ context.Context, _ string) (*litellm.TeamListEntry, error) {
	return nil, nil
}

// §7 stubs — interface satisfaction only (issue #17: /v1 surface).
func (f *fakeLiteLLM) CreateAccessGroup(_ context.Context, _ litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) GetAccessGroupByName(_ context.Context, _ string) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) UpdateAccessGroup(_ context.Context, _ string, _ litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	return nil, nil
}
func (f *fakeLiteLLM) DeleteAccessGroupByID(_ context.Context, _ string) error { return nil }
