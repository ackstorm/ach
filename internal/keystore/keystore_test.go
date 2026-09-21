// SPDX-License-Identifier: Apache-2.0

package keystore

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

	"github.com/ackstorm/ach/internal/credhash"
	"github.com/ackstorm/ach/internal/keys"
)

// fakeResolver is the unit-test stand-in for the inner Resolver
// (dbResolver in production). It records call counts so single-flight
// + cache-hit behavior can be asserted.
type fakeResolver struct {
	mu        sync.Mutex
	calls     int32 // accessed via atomic for the single-flight test
	respond   func(plaintext string) (*KeyInfo, error)
	respondMu sync.Mutex
}

func (f *fakeResolver) Resolve(_ context.Context, plaintext string) (*KeyInfo, error) {
	atomic.AddInt32(&f.calls, 1)
	f.respondMu.Lock()
	respond := f.respond
	f.respondMu.Unlock()
	return respond(plaintext)
}

func (f *fakeResolver) callCount() int32 {
	return atomic.LoadInt32(&f.calls)
}

// validBearer returns a syntactically-correct 29-char pk_ plaintext for
// tests that need to exercise the credhash path.
func validBearer(t *testing.T) string {
	t.Helper()
	s, err := keys.NewBearer(keys.PrefixPk)
	if err != nil {
		t.Fatalf("NewBearer: %v", err)
	}
	return s
}

func setupCached(t *testing.T, inner Resolver) (Resolver, *miniredis.Miniredis, []byte) {
	t.Helper()
	return setupCachedWith(t, inner)
}

// setupCachedWith is setupCached with extra Options (tests: WithClock).
func setupCachedWith(t *testing.T, inner Resolver, opts ...Option) (Resolver, *miniredis.Miniredis, []byte) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	pepper := []byte("test-pepper-32-bytes-aaaaaaaaaaaa")
	r, err := NewCachedResolver(inner, rc, pepper, opts...)
	if err != nil {
		t.Fatalf("NewCachedResolver: %v", err)
	}
	return r, mr, pepper
}

// mustHash hashes plaintext with the given pepper the same way the
// resolver derives its cache key.
func mustHash(t *testing.T, pepper []byte, plaintext string) string {
	t.Helper()
	hash, err := credhash.Hash(pepper, []byte(plaintext))
	if err != nil {
		t.Fatalf("credhash.Hash: %v", err)
	}
	return hash
}

// blockingResolver is a single-key inner Resolver that signals inFlight
// once entered, then blocks on hold until released — used to pin a fill
// mid-flight so the test can advance the fake clock underneath it. The
// inFlight channel is lazily created behind a sync.Once so it is safe to
// construct the struct directly (as the AC-08 tests do) without a
// dedicated constructor, and to read/close it from different goroutines.
type blockingResolver struct {
	info      *KeyInfo
	hold      chan struct{}
	inFlight  chan struct{}
	chOnce    sync.Once
	closeOnce sync.Once
}

func (b *blockingResolver) inFlightCh() chan struct{} {
	b.chOnce.Do(func() {
		if b.inFlight == nil {
			b.inFlight = make(chan struct{})
		}
	})
	return b.inFlight
}

func (b *blockingResolver) Resolve(context.Context, string) (*KeyInfo, error) {
	b.closeOnce.Do(func() { close(b.inFlightCh()) })
	<-b.hold
	return b.info, nil
}

// waitInFlight blocks until the resolver has been entered (or times out).
func (b *blockingResolver) waitInFlight(t *testing.T) {
	t.Helper()
	select {
	case <-b.inFlightCh():
	case <-time.After(2 * time.Second):
		t.Fatal("blockingResolver: timed out waiting for in-flight signal")
	}
}

// staticResolver always returns the same *KeyInfo.
type staticResolver struct {
	info *KeyInfo
}

func (s *staticResolver) Resolve(context.Context, string) (*KeyInfo, error) {
	return s.info, nil
}

// TestCachedResolverMiss — empty cache; inner returns *KeyInfo. After
// Resolve the cache is populated (verified by miniredis state inspection
// — key "ach:key:<hex>" exists with TTL ≤ 60s).
func TestCachedResolverMiss(t *testing.T) {
	plaintext := "pk_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	expires := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) {
		return &KeyInfo{KeyID: "pkid_x", KeyType: keys.PrefixPk, OwnerEmail: "a@b", ExpiresAt: &expires}, nil
	}}
	r, mr, pepper := setupCached(t, inner)
	info, err := r.Resolve(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info == nil || info.KeyID != "pkid_x" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if inner.callCount() != 1 {
		t.Fatalf("expected 1 inner call, got %d", inner.callCount())
	}
	hash, _ := credhash.Hash(pepper, []byte(plaintext))
	cacheKey := "ach:key:" + hash
	if !mr.Exists(cacheKey) {
		t.Fatalf("expected cache key %q to exist in miniredis", cacheKey)
	}
	ttl := mr.TTL(cacheKey)
	if ttl <= 0 || ttl > 60*time.Second {
		t.Fatalf("expected TTL in (0,60s], got %v", ttl)
	}
}

// TestCachedResolverHit — pre-populate miniredis with a serialized
// KeyInfo; the cached resolver returns the decoded value without calling
// inner.
func TestCachedResolverHit(t *testing.T) {
	plaintext := "pk_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) {
		t.Fatalf("inner.Resolve should NOT be called on cache hit")
		return nil, nil
	}}
	r, mr, pepper := setupCached(t, inner)
	hash, _ := credhash.Hash(pepper, []byte(plaintext))
	want := &KeyInfo{KeyID: "pkid_cached", KeyType: keys.PrefixPk, OwnerEmail: "cached@b"}
	b, _ := json.Marshal(want)
	if err := mr.Set("ach:key:"+hash, string(b)); err != nil {
		t.Fatalf("miniredis Set: %v", err)
	}
	info, err := r.Resolve(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info == nil || info.KeyID != "pkid_cached" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if inner.callCount() != 0 {
		t.Fatalf("expected 0 inner calls on cache hit, got %d", inner.callCount())
	}
}

// TestCachedResolverSingleFlight — N concurrent Resolve calls for the
// same plaintext collapse to exactly ONE inner call.
//
// Synchronization strategy (the prior "spin on entered==N, then sleep
// 100ms" was flaky under -race -shuffle: got 2/5/18). Two facts drive
// the redesign:
//
//   - A singleflight JOIN cannot be observed from our code: followers
//     block inside the singleflight internals, never in the resolver or
//     fake. So we cannot assert "all followers enqueued" directly.
//   - But coalescing is GUARANTEED for any follower that reaches sf.Do
//     while the leader's entry is still in-flight. So we keep the leader
//     held and assert the invariant WHILE it is held.
//
// Steps:
//  1. Spawn exactly ONE leader; the fake signals leaderInFlight (the
//     singleflight entry now exists) then blocks on leaderHold. This
//     removes the leader/follower ambiguity that produced got>2.
//  2. Wait for leaderInFlight — callCount is now provably 1.
//  3. Spawn N-1 followers; wait until every follower goroutine is
//     launched, then a generous settle so each clears the credhash +
//     Redis-GET prelude and parks inside sf.Do (joining the leader).
//  4. Assert callCount==1 WHILE the leader is still held: no follower
//     can have started a second inner call, because the entry is open.
//  5. Release the leader; after wg.Wait, re-assert callCount==1.
//
// The settle in step 3 is the only timing element; sized at 2s it is
// ~1000x the real prelude even under -race, and a wrong value can only
// fail loud (a straggler becomes a 2nd leader → callCount>1), never
// pass falsely.
func TestCachedResolverSingleFlight(t *testing.T) {
	const N = 50
	plaintext := "pk_cccccccccccccccccccccccccc"
	leaderHold := make(chan struct{})
	leaderInFlight := make(chan struct{})
	var inFlightOnce sync.Once
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) {
		inFlightOnce.Do(func() { close(leaderInFlight) })
		<-leaderHold // hold the in-flight entry until followers have joined
		return &KeyInfo{KeyID: "pkid_sf", KeyType: keys.PrefixPk, OwnerEmail: "a@b"}, nil
	}}
	r, _, _ := setupCached(t, inner)

	var wg sync.WaitGroup

	// 1+2. Leader: spawn, then wait until it provably holds the sf entry.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = r.Resolve(context.Background(), plaintext)
	}()
	<-leaderInFlight
	if got := inner.callCount(); got != 1 {
		t.Fatalf("after leader in-flight: expected exactly 1 inner call, got %d", got)
	}

	// 3. Followers: every concurrent caller on the same key must join.
	var launched atomic.Int32
	for i := 0; i < N-1; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			launched.Add(1)
			_, _ = r.Resolve(context.Background(), plaintext)
		}()
	}
	for launched.Load() < N-1 {
		runtime.Gosched()
	}
	// Generous settle: followers clear the prelude and park in sf.Do.
	time.Sleep(2 * time.Second)

	// 4. Invariant holds while the leader is still in-flight.
	if got := inner.callCount(); got != 1 {
		t.Fatalf("while leader held: expected exactly 1 inner call (single-flight), got %d", got)
	}

	// 5. Release and confirm nothing started a late second call.
	close(leaderHold)
	wg.Wait()
	if got := inner.callCount(); got != 1 {
		t.Fatalf("after release: expected exactly 1 inner call (single-flight), got %d", got)
	}
}

// TestCachedResolverTTLExact — the Redis SET TTL is exactly 60s, no
// longer / no shorter. Uses a fake clock frozen at a single instant: with
// zero elapsed time between the anchor and the fill, ttl − elapsed == ttl.
func TestCachedResolverTTLExact(t *testing.T) {
	plaintext := "pk_dddddddddddddddddddddddddd"
	now := time.Now()
	clock := func() time.Time { return now }
	expires := now.Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) {
		return &KeyInfo{KeyID: "pkid_y", KeyType: keys.PrefixPk, OwnerEmail: "a@b", ExpiresAt: &expires}, nil
	}}
	r, mr, pepper := setupCachedWith(t, inner, WithClock(clock))
	if _, err := r.Resolve(context.Background(), plaintext); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	hash, _ := credhash.Hash(pepper, []byte(plaintext))
	ttl := mr.TTL("ach:key:" + hash)
	if ttl != 60*time.Second {
		t.Fatalf("expected TTL == 60s, got %v", ttl)
	}
}

// TestCachedResolverEmptyPepper — NewCachedResolver refuses an empty pepper.
func TestCachedResolverEmptyPepper(t *testing.T) {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rc.Close() }()
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) { return nil, nil }}
	_, err := NewCachedResolver(inner, rc, nil)
	if err == nil {
		t.Fatalf("expected error on nil pepper")
	}
	_, err = NewCachedResolver(inner, rc, []byte{})
	if err == nil {
		t.Fatalf("expected error on empty pepper")
	}
}

// TestCachedResolverInnerError — inner returns an error; the error
// surfaces to the caller and Redis is NOT populated.
func TestCachedResolverInnerError(t *testing.T) {
	plaintext := validBearer(t)
	wantErr := errors.New("db down")
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) { return nil, wantErr }}
	r, mr, pepper := setupCached(t, inner)
	_, err := r.Resolve(context.Background(), plaintext)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped wantErr, got %v", err)
	}
	hash, _ := credhash.Hash(pepper, []byte(plaintext))
	if mr.Exists("ach:key:" + hash) {
		t.Fatalf("expected cache key absent on inner error")
	}
}

// TestCachedResolverNilInfoNoCache — inner returns (nil, nil); the
// resolver does NOT cache a nil KeyInfo (would let revoked credentials
// survive in cache as a positive empty result).
func TestCachedResolverNilInfoNoCache(t *testing.T) {
	plaintext := validBearer(t)
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) { return nil, nil }}
	r, mr, pepper := setupCached(t, inner)
	info, err := r.Resolve(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info != nil {
		t.Fatalf("expected nil info, got %+v", info)
	}
	hash, _ := credhash.Hash(pepper, []byte(plaintext))
	if mr.Exists("ach:key:" + hash) {
		t.Fatalf("expected nil KeyInfo NOT to be cached")
	}
}

// stubPgxOp is the per-plaintext stub the dbResolver test fake uses to
// emulate db.PkCheckAndExtend / db.EkResolve.
type stubDB struct {
	pkResp *KeyInfo
	ekResp *KeyInfo
	pkErr  error
	ekErr  error
	pkSeen int
	ekSeen int
}

// pkLookup / ekLookup satisfy the dbLookupFn type used by dbResolver.
func (s *stubDB) pkLookup(_ context.Context, _ string) (*KeyInfo, error) {
	s.pkSeen++
	return s.pkResp, s.pkErr
}
func (s *stubDB) ekLookup(_ context.Context, _ string) (*KeyInfo, error) {
	s.ekSeen++
	return s.ekResp, s.ekErr
}

// TestDBResolverPkHappy — valid pk_ plaintext dispatches to the pk
// lookup; result is wrapped with KeyType=PrefixPk and the ExpiresAt
// pointer.
func TestDBResolverPkHappy(t *testing.T) {
	plaintext := validBearer(t)
	expires := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	stub := &stubDB{pkResp: &KeyInfo{KeyID: "pkid_a", KeyType: keys.PrefixPk, OwnerEmail: "a@b", ExpiresAt: &expires}}
	r := newDBResolverWith([]byte("pepper-aaaaaaaaaaaaaaaaaaaaaaaaaa"), stub.pkLookup, stub.ekLookup)
	info, err := r.Resolve(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info == nil || info.KeyType != keys.PrefixPk || info.ExpiresAt == nil {
		t.Fatalf("unexpected info: %+v", info)
	}
	if stub.pkSeen != 1 || stub.ekSeen != 0 {
		t.Fatalf("expected pkLookup called once, ekLookup not at all; pk=%d ek=%d", stub.pkSeen, stub.ekSeen)
	}
}

// TestDBResolverEkHappy — valid ek_ plaintext dispatches to the ek
// lookup; result is wrapped with KeyType=PrefixEk and Environment set.
func TestDBResolverEkHappy(t *testing.T) {
	plaintext, err := keys.NewBearer(keys.PrefixEk)
	if err != nil {
		t.Fatalf("NewBearer(Ek): %v", err)
	}
	stub := &stubDB{ekResp: &KeyInfo{KeyID: "ekid_b", KeyType: keys.PrefixEk, OwnerEmail: "a@b", Environment: "prod"}}
	r := newDBResolverWith([]byte("pepper-bbbbbbbbbbbbbbbbbbbbbbbbbb"), stub.pkLookup, stub.ekLookup)
	info, err := r.Resolve(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info == nil || info.KeyType != keys.PrefixEk || info.Environment != "prod" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if stub.ekSeen != 1 || stub.pkSeen != 0 {
		t.Fatalf("expected ekLookup called once, pkLookup not at all; pk=%d ek=%d", stub.pkSeen, stub.ekSeen)
	}
}

// TestDBResolverPkInvalid — pk lookup returns (nil, nil); dbResolver
// returns (nil, nil) too. The caller will render 401 expired_or_revoked.
func TestDBResolverPkInvalid(t *testing.T) {
	plaintext := validBearer(t)
	stub := &stubDB{pkResp: nil}
	r := newDBResolverWith([]byte("pepper-cccccccccccccccccccccccccc"), stub.pkLookup, stub.ekLookup)
	info, err := r.Resolve(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info != nil {
		t.Fatalf("expected nil info on (nil, nil) pk lookup, got %+v", info)
	}
}

// TestDBResolverMalformedBearer — ClassifyBearer rejects "pk_too_short";
// dbResolver returns (nil, nil) without invoking either DB lookup.
func TestDBResolverMalformedBearer(t *testing.T) {
	stub := &stubDB{}
	r := newDBResolverWith([]byte("pepper-dddddddddddddddddddddddddd"), stub.pkLookup, stub.ekLookup)
	info, err := r.Resolve(context.Background(), "pk_TOO_SHORT")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info != nil {
		t.Fatalf("expected nil info on malformed bearer, got %+v", info)
	}
	if stub.pkSeen != 0 || stub.ekSeen != 0 {
		t.Fatalf("expected zero DB calls; pk=%d ek=%d", stub.pkSeen, stub.ekSeen)
	}
}

// TestDBResolverPkErrorWrapped — pk lookup returns a transient error;
// dbResolver wraps it with the package prefix.
func TestDBResolverPkErrorWrapped(t *testing.T) {
	plaintext := validBearer(t)
	stub := &stubDB{pkErr: errors.New("connection refused")}
	r := newDBResolverWith([]byte("pepper-eeeeeeeeeeeeeeeeeeeeeeeeee"), stub.pkLookup, stub.ekLookup)
	_, err := r.Resolve(context.Background(), plaintext)
	if err == nil || !strings.Contains(err.Error(), "keystore: dbResolver") {
		t.Fatalf("expected wrapped err containing 'keystore: dbResolver', got %v", err)
	}
}

// AC-08 stale-fill race: a fill that was in flight when a suspend committed
// re-inserts the PRE-commit row — but only for the time the fill's DB read
// has already "used up". No entry may outlive 60 s from its DB read.
func TestCachedResolverAnchorsTTLBeforeTheLookup(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	release := make(chan struct{})
	inner := &blockingResolver{info: &KeyInfo{KeyID: "ekid_1", KeyType: keys.PrefixEk}, hold: release}
	r, mr, pepper := setupCachedWith(t, inner, WithClock(clock))
	cacheKey := cacheKeyPrefix + mustHash(t, pepper, "ek_1")

	done := make(chan struct{})
	go func() { _, _ = r.Resolve(context.Background(), "ek_1"); close(done) }()
	inner.waitInFlight(t)
	now = now.Add(45 * time.Second) // the DB read is slow; a suspend commits meanwhile
	close(release)
	<-done
	if ttl := mr.TTL(cacheKey); ttl <= 0 || ttl > 15*time.Second {
		t.Fatalf("cache TTL %v, want ≤ 15s (60s − 45s elapsed)", ttl)
	}
}

func TestCachedResolverSkipsCachingWhenTheWindowIsSpent(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	release := make(chan struct{})
	inner := &blockingResolver{info: &KeyInfo{KeyID: "ekid_1", KeyType: keys.PrefixEk}, hold: release}
	r, mr, pepper := setupCachedWith(t, inner, WithClock(clock))
	done := make(chan struct{})
	go func() { _, _ = r.Resolve(context.Background(), "ek_1"); close(done) }()
	inner.waitInFlight(t)
	now = now.Add(61 * time.Second)
	close(release)
	<-done
	if mr.Exists(cacheKeyPrefix + mustHash(t, pepper, "ek_1")) {
		t.Fatal("an entry older than the ceiling was cached")
	}
}

// AC-10: expiry is enforced on warm hits and caps the fill's TTL.
func TestCachedResolverHonoursExpiresAt(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	exp := now.Add(20 * time.Second)
	inner := &staticResolver{info: &KeyInfo{KeyID: "ekid_1", KeyType: keys.PrefixEk, ExpiresAt: &exp}}
	r, mr, pepper := setupCachedWith(t, inner, WithClock(clock))
	if info, err := r.Resolve(context.Background(), "ek_1"); err != nil || info == nil {
		t.Fatalf("live: %v %v", info, err)
	}
	cacheKey := cacheKeyPrefix + mustHash(t, pepper, "ek_1")
	if ttl := mr.TTL(cacheKey); ttl > 20*time.Second {
		t.Fatalf("TTL %v not capped at expires_at", ttl)
	}
	now = now.Add(21 * time.Second) // warm entry still in Redis (miniredis clock is separate)
	if info, err := r.Resolve(context.Background(), "ek_1"); err != nil || info != nil {
		t.Fatalf("expired warm hit must be nil: %v %v", info, err)
	}
}

func TestDBResolverEkCarriesExpiresAt(t *testing.T) {
	plaintext, err := keys.NewBearer(keys.PrefixEk)
	if err != nil {
		t.Fatalf("NewBearer(Ek): %v", err)
	}
	exp := time.Now().Add(time.Hour)
	r := newDBResolverWith([]byte("pepper"), nil, func(context.Context, string) (*KeyInfo, error) {
		return &KeyInfo{KeyID: "ekid_1", KeyType: keys.PrefixEk, ExpiresAt: &exp}, nil
	})
	info, _ := r.Resolve(context.Background(), plaintext)
	if info == nil || info.ExpiresAt == nil {
		t.Fatalf("%+v", info)
	}
}

// AC-08 regression (review round 1, T2): the TTL anchor must be captured
// once, inside the singleflight LEADER's closure, and shared by every
// caller joined on the same key — not re-captured independently by each
// caller before joining. A follower joining after the clock has advanced
// must not compute (and SET) a larger `remaining` than the leader's,
// which would silently re-extend the entry past the leader's correctly
// computed 60s-from-DB-read ceiling (Redis SET is last-write-wins).
func TestCachedResolverSharesLeaderAnchorAcrossFollowers(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }
	release := make(chan struct{})
	inner := &blockingResolver{info: &KeyInfo{KeyID: "ekid_1", KeyType: keys.PrefixEk}, hold: release}
	r, mr, pepper := setupCachedWith(t, inner, WithClock(clock))
	cacheKey := cacheKeyPrefix + mustHash(t, pepper, "ek_1")

	leaderDone := make(chan struct{})
	go func() { _, _ = r.Resolve(context.Background(), "ek_1"); close(leaderDone) }()
	inner.waitInFlight(t) // the leader anchored at `now` and is blocked in the DB read

	now = now.Add(30 * time.Second) // the clock moves before a follower joins

	followerDone := make(chan struct{})
	go func() { _, _ = r.Resolve(context.Background(), "ek_1"); close(followerDone) }()
	time.Sleep(200 * time.Millisecond) // let the follower join the in-flight singleflight entry

	now = now.Add(15 * time.Second) // the DB read keeps running; 45s elapsed since the leader's anchor
	close(release)
	<-leaderDone
	<-followerDone

	if ttl := mr.TTL(cacheKey); ttl <= 0 || ttl > 15*time.Second {
		t.Fatalf("cache TTL %v, want ≤15s (60s − 45s elapsed from the LEADER's anchor — a follower-anchored bug would show ≈45s instead)", ttl)
	}
}
