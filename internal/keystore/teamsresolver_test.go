// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ackstorm/ach/internal/litellm"
)

// fakeTeamsBase is the unit-test stand-in for the inner TeamsResolver
// (liteLLMTeamsResolver in production). It records call counts so
// single-flight + cache-hit behavior can be asserted.
type fakeTeamsBase struct {
	calls   int32 // accessed via atomic for the single-flight test
	mu      sync.Mutex
	respond func(email string) ([]string, error)
}

func (f *fakeTeamsBase) Resolve(_ context.Context, email string) ([]string, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	respond := f.respond
	f.mu.Unlock()
	return respond(email)
}

func (f *fakeTeamsBase) callCount() int32 {
	return atomic.LoadInt32(&f.calls)
}

// fakeLitellmClient is the in-file stand-in for litellm.Client used by
// TestLiteLLMTeamsResolver_NotFound / _Unreachable / _Happy. It overrides
// only UserInfoByEmail; every other method delegates to the noop client
// (or panics — we never call them from teamsresolver code paths).
type fakeLitellmClient struct {
	litellm.Client // embed for method coverage; tests override only UserInfoByEmail
	resp           *litellm.UserInfo
	err            error
}

func (f *fakeLitellmClient) UserInfoByEmail(_ context.Context, _ string) (*litellm.UserInfo, error) {
	return f.resp, f.err
}

func setupCachedTeams(t *testing.T, base TeamsResolver) (TeamsResolver, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	r, err := NewCachedTeamsResolver(base, rc)
	if err != nil {
		t.Fatalf("NewCachedTeamsResolver: %v", err)
	}
	return r, mr, rc
}

// T1 — NewCachedTeamsResolver(nil, rdb) rejects nil base.
func TestNewCachedTeamsResolver_NilBase(t *testing.T) {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rc.Close() }()
	_, err := NewCachedTeamsResolver(nil, rc)
	if err == nil || err.Error() != "keystore: nil base teams resolver" {
		t.Fatalf("expected 'nil base teams resolver' error, got %v", err)
	}
}

// T2 — NewCachedTeamsResolver(base, nil) rejects nil redis client.
func TestNewCachedTeamsResolver_NilRedis(t *testing.T) {
	base := &fakeTeamsBase{respond: func(string) ([]string, error) { return nil, nil }}
	_, err := NewCachedTeamsResolver(base, nil)
	if err == nil || err.Error() != "keystore: nil redis client" {
		t.Fatalf("expected 'nil redis client' error, got %v", err)
	}
}

// T3 — NewCachedTeamsResolver(base, rdb) returns a non-nil resolver.
func TestNewCachedTeamsResolver_Happy(t *testing.T) {
	base := &fakeTeamsBase{respond: func(string) ([]string, error) { return nil, nil }}
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rc.Close() }()
	r, err := NewCachedTeamsResolver(base, rc)
	if err != nil {
		t.Fatalf("NewCachedTeamsResolver: %v", err)
	}
	if r == nil {
		t.Fatalf("expected non-nil TeamsResolver")
	}
}

// T4 — NewLiteLLMTeamsResolver(nil) rejects nil litellm client.
//
// Contract: constructor-time error rather than panic; mirrors the
// NewDBResolver / NewCachedResolver shape for callsite uniformity.
func TestNewLiteLLMTeamsResolver_NilClient(t *testing.T) {
	_, err := NewLiteLLMTeamsResolver(nil)
	if err == nil || err.Error() != "keystore: nil litellm client" {
		t.Fatalf("expected 'nil litellm client' error, got %v", err)
	}
}

// T5 — Cache hit. Pre-populate Redis with the JSON-encoded team list;
// Resolve returns the decoded slice WITHOUT calling the base resolver.
func TestRedisCachedTeamsResolver_Hit(t *testing.T) {
	base := &fakeTeamsBase{respond: func(string) ([]string, error) {
		t.Fatalf("base.Resolve must NOT be called on cache hit")
		return nil, nil
	}}
	r, mr, _ := setupCachedTeams(t, base)
	email := "u@example.com"
	want := []string{"t1", "t2"}
	b, _ := json.Marshal(want)
	if err := mr.Set("ach:teams:"+email, string(b)); err != nil {
		t.Fatalf("miniredis Set: %v", err)
	}
	got, err := r.Resolve(context.Background(), email)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 2 || got[0] != "t1" || got[1] != "t2" {
		t.Fatalf("unexpected teams: %+v", got)
	}
	if base.callCount() != 0 {
		t.Fatalf("expected 0 base calls on cache hit, got %d", base.callCount())
	}
}

// T6 — Cache miss → base → cache populated → second Resolve hits cache.
func TestRedisCachedTeamsResolver_MissThenHit(t *testing.T) {
	base := &fakeTeamsBase{respond: func(string) ([]string, error) {
		return []string{"t3"}, nil
	}}
	r, mr, _ := setupCachedTeams(t, base)
	email := "u@example.com"
	got, err := r.Resolve(context.Background(), email)
	if err != nil {
		t.Fatalf("Resolve(miss): %v", err)
	}
	if len(got) != 1 || got[0] != "t3" {
		t.Fatalf("unexpected miss result: %+v", got)
	}
	if !mr.Exists("ach:teams:" + email) {
		t.Fatalf("expected cache key 'ach:teams:%s' to exist after miss", email)
	}
	ttl := mr.TTL("ach:teams:" + email)
	if ttl != 60*time.Second {
		t.Fatalf("expected TTL == 60s, got %v", ttl)
	}
	// Second Resolve must hit cache (base call count stays at 1).
	got2, err := r.Resolve(context.Background(), email)
	if err != nil {
		t.Fatalf("Resolve(hit): %v", err)
	}
	if len(got2) != 1 || got2[0] != "t3" {
		t.Fatalf("unexpected second result: %+v", got2)
	}
	if base.callCount() != 1 {
		t.Fatalf("expected exactly 1 base call across miss+hit, got %d", base.callCount())
	}
}

// T7 — Empty-slice cached as valid result. Base returns []string{};
// Resolve returns []string{}; Redis stores `[]` JSON; second Resolve
// hits the cache.
func TestRedisCachedTeamsResolver_EmptySliceCached(t *testing.T) {
	base := &fakeTeamsBase{respond: func(string) ([]string, error) {
		return []string{}, nil
	}}
	r, mr, _ := setupCachedTeams(t, base)
	email := "noteams@example.com"
	got, err := r.Resolve(context.Background(), email)
	if err != nil {
		t.Fatalf("Resolve(miss): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected non-nil empty slice, got %+v (nil=%v)", got, got == nil)
	}
	// Verify cache wire format is exactly `[]` (valid JSON empty array,
	// distinguishable from a JSON `null`).
	raw, getErr := mr.Get("ach:teams:" + email)
	if getErr != nil {
		t.Fatalf("miniredis Get: %v", getErr)
	}
	if raw != "[]" {
		t.Fatalf("expected cache wire format `[]`, got %q", raw)
	}
	// Empty results use the short negative TTL (not the 60s ceiling) so a
	// transient empty (post-provisioning consistency window) self-heals fast
	// instead of poisoning the per-email entry and 403'ing for a full minute.
	if ttl := mr.TTL("ach:teams:" + email); ttl != negativeTeamsTTL {
		t.Fatalf("expected empty-result TTL == negativeTeamsTTL (%v), got %v", negativeTeamsTTL, ttl)
	}
	// Second Resolve hits cache.
	got2, err := r.Resolve(context.Background(), email)
	if err != nil {
		t.Fatalf("Resolve(hit): %v", err)
	}
	if got2 == nil || len(got2) != 0 {
		t.Fatalf("expected empty slice on hit, got %+v", got2)
	}
	if base.callCount() != 1 {
		t.Fatalf("expected exactly 1 base call across miss+hit, got %d", base.callCount())
	}
}

// T8 — LiteLLM 404 → liteLLMTeamsResolver swallows ErrNotFound and
// returns ([]string{}, nil). Matches Phase 3 teams/lookup.go pattern.
func TestLiteLLMTeamsResolver_NotFoundIsEmpty(t *testing.T) {
	ll := &fakeLitellmClient{resp: nil, err: litellm.ErrNotFound}
	r, err := NewLiteLLMTeamsResolver(ll)
	if err != nil {
		t.Fatalf("NewLiteLLMTeamsResolver: %v", err)
	}
	got, err := r.Resolve(context.Background(), "missing@example.com")
	if err != nil {
		t.Fatalf("expected nil error on ErrNotFound, got %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected []string{} on ErrNotFound, got %+v (nil=%v)", got, got == nil)
	}
}

// T9 — LiteLLM unreachable. liteLLMTeamsResolver propagates a generic
// error; redisCachedTeamsResolver propagates further. Cache MUST NOT be
// written.
func TestRedisCachedTeamsResolver_UnreachablePropagates(t *testing.T) {
	wantErr := errors.New("connection refused")
	base := &fakeTeamsBase{respond: func(string) ([]string, error) {
		return nil, wantErr
	}}
	r, mr, _ := setupCachedTeams(t, base)
	email := "u@example.com"
	got, err := r.Resolve(context.Background(), email)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped wantErr, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil teams on error, got %+v", got)
	}
	if mr.Exists("ach:teams:" + email) {
		t.Fatalf("expected cache key absent after base error")
	}
}

// T10 — Singleflight dedup. N concurrent Resolve calls for the same
// email collapse to exactly ONE base call. Robust synchronization
// strategy mirrors TestCachedResolverSingleFlight:
//
//  1. Spawn N goroutines; each atomically signals "I'm about to call
//     Resolve" via the `entered` counter BEFORE calling r.Resolve.
//  2. Main spins on runtime.Gosched until entered == N.
//  3. Brief settle so any goroutine between entered.Add and the
//     singleflight enqueue inside r.Resolve actually enqueues.
//  4. close(leaderHold) releases the leader; followers join the
//     in-flight result.
func TestRedisCachedTeamsResolver_SingleFlight(t *testing.T) {
	const N = 50
	leaderHold := make(chan struct{})
	base := &fakeTeamsBase{respond: func(string) ([]string, error) {
		<-leaderHold
		return []string{"t-sf"}, nil
	}}
	r, _, _ := setupCachedTeams(t, base)
	email := "u@example.com"

	var entered atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entered.Add(1)
			_, _ = r.Resolve(context.Background(), email)
		}()
	}
	for entered.Load() < N {
		runtime.Gosched()
	}
	// Brief settle covers the tiny gap between entered.Add and the
	// singleflight enqueue inside r.Resolve.
	time.Sleep(100 * time.Millisecond)

	close(leaderHold)
	wg.Wait()
	if base.callCount() != 1 {
		t.Fatalf("expected exactly 1 base call (single-flight), got %d", base.callCount())
	}
}

// T11 — Cache TTL bound. After a fresh Resolve, advance miniredis time
// by 70 seconds; the next Resolve must re-call the base (cache entry
// has expired beyond the 60s ceiling).
func TestRedisCachedTeamsResolver_TTLExpires(t *testing.T) {
	base := &fakeTeamsBase{respond: func(string) ([]string, error) {
		return []string{"t-ttl"}, nil
	}}
	r, mr, _ := setupCachedTeams(t, base)
	email := "u@example.com"
	if _, err := r.Resolve(context.Background(), email); err != nil {
		t.Fatalf("Resolve(1): %v", err)
	}
	if base.callCount() != 1 {
		t.Fatalf("expected 1 base call after first Resolve, got %d", base.callCount())
	}
	// Advance miniredis past the 60s TTL ceiling.
	mr.FastForward(70 * time.Second)
	if _, err := r.Resolve(context.Background(), email); err != nil {
		t.Fatalf("Resolve(2): %v", err)
	}
	if base.callCount() != 2 {
		t.Fatalf("expected 2 base calls after TTL expiry, got %d", base.callCount())
	}
}

// T12 — Compile-time canary. The file declares both interface-implementing
// canaries; this test just exercises construction to ensure the package
// still compiles after future edits (the canaries themselves fire at
// build time).
func TestTeamsResolver_CompileCanaries(t *testing.T) {
	// liteLLMTeamsResolver via NewLiteLLMTeamsResolver — implements TeamsResolver
	ll := &fakeLitellmClient{resp: &litellm.UserInfo{Teams: []string{"t-canary"}}}
	base, err := NewLiteLLMTeamsResolver(ll)
	if err != nil {
		t.Fatalf("NewLiteLLMTeamsResolver: %v", err)
	}
	var _ TeamsResolver = base // assignability check

	// redisCachedTeamsResolver via NewCachedTeamsResolver — implements TeamsResolver
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rc.Close() }()
	cached, err := NewCachedTeamsResolver(base, rc)
	if err != nil {
		t.Fatalf("NewCachedTeamsResolver: %v", err)
	}
	var _ TeamsResolver = cached

	// Exercise the base.Resolve path once to catch a wiring regression.
	got, err := base.Resolve(context.Background(), "any@example.com")
	if err != nil {
		t.Fatalf("liteLLMTeamsResolver.Resolve: %v", err)
	}
	if len(got) != 1 || got[0] != "t-canary" {
		t.Fatalf("unexpected canary result: %+v", got)
	}
}

// Bonus — exercise the liteLLMTeamsResolver "nil info" branch (LiteLLM
// returns *UserInfo with nil Teams, not an error). Plan requires the
// resolver to return ([]string{}, nil) in this case.
func TestLiteLLMTeamsResolver_NilTeamsIsEmpty(t *testing.T) {
	ll := &fakeLitellmClient{resp: &litellm.UserInfo{UserID: "u1", Teams: nil}}
	r, err := NewLiteLLMTeamsResolver(ll)
	if err != nil {
		t.Fatalf("NewLiteLLMTeamsResolver: %v", err)
	}
	got, err := r.Resolve(context.Background(), "u@example.com")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected []string{}, got %+v", got)
	}
}
