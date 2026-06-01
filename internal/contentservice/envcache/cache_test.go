// SPDX-License-Identifier: Apache-2.0

package envcache

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestCache wires a miniredis-backed Cache and returns it together
// with the underlying miniredis handle so individual tests can prepopulate
// keys, close the server, or inspect TTLs.
func newTestCache(t *testing.T, loader Loader) (Cache, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	c, err := NewCachedEnvCache(loader, rdb)
	if err != nil {
		t.Fatalf("NewCachedEnvCache: %v", err)
	}
	return c, mr, rdb
}

// sampleRow constructs an EnvRow whose AuthorizedTeams carries the
// per-write sentinel (sentinel) — used as the cache-identity
// discriminator in place of the removed ResourceVersion field. Empty
// context slices keep the JSON wire format stable. ns/name are accepted
// for call-site readability but no longer stored on EnvRow.
func sampleRow(_, _, sentinel string) *EnvRow {
	return &EnvRow{
		AuthorizedTeams:  []string{sentinel},
		ContextPrompts:   []string{},
		ContextPlugins:   []string{},
		ContextArtifacts: []string{},
	}
}

// sentinel returns the per-write discriminator stored in AuthorizedTeams
// (see sampleRow), or "" when absent.
func sentinel(r *EnvRow) string {
	if r == nil || len(r.AuthorizedTeams) == 0 {
		return ""
	}
	return r.AuthorizedTeams[0]
}

func TestGet_Hit_FromCache(t *testing.T) {
	loader := func(_ context.Context, _, _ string) (*EnvRow, error) {
		t.Fatal("loader must NOT be invoked on cache hit")
		return nil, nil
	}
	c, mr, _ := newTestCache(t, loader)
	preset := sampleRow("test", "foo", "rv-1")
	b, err := json.Marshal(preset)
	if err != nil {
		t.Fatalf("marshal sample: %v", err)
	}
	mr.Set("ach:env:test/foo", string(b))

	got, err := c.Get(context.Background(), "test", "foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil row on hit")
	}
	if sentinel(got) != "rv-1" {
		t.Errorf("sentinel=%q, want rv-1", sentinel(got))
	}
}

func TestGet_Miss_LoaderHydrates(t *testing.T) {
	row := sampleRow("test", "foo", "rv-2")
	loader := func(_ context.Context, _, _ string) (*EnvRow, error) {
		return row, nil
	}
	c, mr, _ := newTestCache(t, loader)

	got, err := c.Get(context.Background(), "test", "foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || sentinel(got) != "rv-2" {
		t.Fatalf("loader hydration mismatch: got=%+v", got)
	}

	raw, err := mr.Get("ach:env:test/foo")
	if err != nil {
		t.Fatalf("miniredis Get after hydrate: %v", err)
	}
	var roundtrip EnvRow
	if err := json.Unmarshal([]byte(raw), &roundtrip); err != nil {
		t.Fatalf("JSON in cache not parseable as EnvRow: %v", err)
	}
	if sentinel(&roundtrip) != "rv-2" {
		t.Errorf("cached row mismatch: sentinel=%q", sentinel(&roundtrip))
	}

	ttl := mr.TTL("ach:env:test/foo")
	if ttl <= 0 {
		t.Errorf("expected positive TTL, got %v", ttl)
	}
	if ttl > 60*time.Second {
		t.Errorf("TTL exceeds 60s ceiling: %v", ttl)
	}
}

func TestGet_Miss_LoaderReturnsNilNil(t *testing.T) {
	loader := func(_ context.Context, _, _ string) (*EnvRow, error) {
		return nil, nil
	}
	c, mr, _ := newTestCache(t, loader)

	got, err := c.Get(context.Background(), "test", "absent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil row, got %+v", got)
	}
	if _, err := mr.Get("ach:env:test/absent"); err == nil {
		t.Errorf("absent row should NOT be cached, but miniredis has the key")
	}
}

func TestGet_Miss_LoaderError(t *testing.T) {
	wantErr := errors.New("db down")
	loader := func(_ context.Context, _, _ string) (*EnvRow, error) {
		return nil, wantErr
	}
	c, mr, _ := newTestCache(t, loader)

	got, err := c.Get(context.Background(), "test", "foo")
	if !errors.Is(err, wantErr) {
		t.Errorf("err mismatch: got %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Errorf("expected nil row on loader error, got %+v", got)
	}
	if _, err := mr.Get("ach:env:test/foo"); err == nil {
		t.Errorf("loader error must NOT populate cache")
	}
}

func TestGet_MalformedCache_FallsThrough(t *testing.T) {
	row := sampleRow("test", "foo", "rv-3")
	loader := func(_ context.Context, _, _ string) (*EnvRow, error) {
		return row, nil
	}
	c, mr, _ := newTestCache(t, loader)
	mr.Set("ach:env:test/foo", "garbage{not-json")

	got, err := c.Get(context.Background(), "test", "foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || sentinel(got) != "rv-3" {
		t.Errorf("expected loader row on malformed cache, got=%+v", got)
	}

	raw, err := mr.Get("ach:env:test/foo")
	if err != nil {
		t.Fatalf("miniredis Get post fall-through: %v", err)
	}
	var roundtrip EnvRow
	if err := json.Unmarshal([]byte(raw), &roundtrip); err != nil {
		t.Errorf("expected loader to overwrite malformed entry with valid JSON, got: %s", raw)
	}
	if sentinel(&roundtrip) != "rv-3" {
		t.Errorf("malformed key was not overwritten by loader; sentinel=%q", sentinel(&roundtrip))
	}
}

func TestGet_RedisDown_FallsThrough(t *testing.T) {
	row := sampleRow("test", "foo", "rv-4")
	loader := func(_ context.Context, _, _ string) (*EnvRow, error) {
		return row, nil
	}
	c, mr, _ := newTestCache(t, loader)
	mr.Close()

	got, err := c.Get(context.Background(), "test", "foo")
	if err != nil {
		t.Fatalf("Get with redis down: %v", err)
	}
	if got == nil || sentinel(got) != "rv-4" {
		t.Errorf("expected loader row when redis is down, got=%+v", got)
	}
}

// waitInSingleflight blocks until at least n goroutines have a
// golang.org/x/sync/singleflight frame on their stack (the held leader plus
// its joined followers, all parked in Group.Do). Makes the single-flight
// dedup test DETERMINISTIC under -p=GOMAXPROCS -race CPU oversubscription,
// where a fixed-ms settle starves and a straggler reaches Do after the leader
// is released (a spurious extra loader call).
func waitInSingleflight(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	buf := make([]byte, 1<<20)
	for {
		m := runtime.Stack(buf, true)
		count := 0
		for _, blk := range strings.Split(string(buf[:m]), "\n\ngoroutine ") {
			if strings.Contains(blk, "/sync/singleflight.") {
				count++
			}
		}
		if count >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %d/%d goroutines parked in singleflight", count, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestGet_Singleflight_DedupesConcurrentMisses(t *testing.T) {
	var calls atomic.Int64
	row := sampleRow("test", "foo", "rv-sf")
	// Leader-first deterministic synchronization. The prior fixed-100ms loader
	// sleep flaked under -race -shuffle: slow follower goroutines reached the
	// singleflight enqueue AFTER the leader's loader returned and the group
	// cleared, forming a second loader call. Instead, block the leader inside
	// the loader (group guaranteed in-flight + held) BEFORE launching the
	// followers; every follower then JOINS the in-flight group.
	leaderHold := make(chan struct{})
	leaderEntered := make(chan struct{})
	var once sync.Once
	loader := func(_ context.Context, _, _ string) (*EnvRow, error) {
		calls.Add(1)
		once.Do(func() { close(leaderEntered) })
		<-leaderHold
		return row, nil
	}
	c, _, _ := newTestCache(t, loader)

	const N = 50
	var wg sync.WaitGroup
	results := make([]*EnvRow, N)
	errs := make([]error, N)

	// 1. Leader-first: one Get blocks inside the loader → group in-flight.
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[0], errs[0] = c.Get(context.Background(), "test", "foo")
	}()
	<-leaderEntered

	// 2. Followers join the in-flight group; none can start a new loader call.
	for i := 1; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = c.Get(context.Background(), "test", "foo")
		}(i)
	}
	// 3. Deterministic barrier: wait until the held leader + all N-1 followers
	//    are parked inside singleflight.Do (robust under any CPU contention;
	//    a fixed-ms settle starves under -p=GOMAXPROCS -race).
	waitInSingleflight(t, N)
	close(leaderHold)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("loader invoked %d times, want 1", got)
	}
	for i := 0; i < N; i++ {
		if errs[i] != nil {
			t.Errorf("goroutine %d err: %v", i, errs[i])
			continue
		}
		if results[i] == nil || sentinel(results[i]) != "rv-sf" {
			t.Errorf("goroutine %d got %+v, want sentinel=rv-sf", i, results[i])
		}
	}
}

func TestNewCachedEnvCache_RefusesNilLoader(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	c, err := NewCachedEnvCache(nil, rdb)
	if err == nil {
		t.Fatal("expected error on nil loader, got nil")
	}
	if c != nil {
		t.Error("expected nil Cache on nil loader")
	}
	if want := "nil loader"; !contains(err.Error(), want) {
		t.Errorf("err=%q, want substring %q", err.Error(), want)
	}
}

func TestNewCachedEnvCache_RefusesNilRedis(t *testing.T) {
	loader := func(_ context.Context, _, _ string) (*EnvRow, error) {
		return nil, nil
	}
	c, err := NewCachedEnvCache(loader, nil)
	if err == nil {
		t.Fatal("expected error on nil redis client, got nil")
	}
	if c != nil {
		t.Error("expected nil Cache on nil redis")
	}
	if want := "nil redis"; !contains(err.Error(), want) {
		t.Errorf("err=%q, want substring %q", err.Error(), want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
