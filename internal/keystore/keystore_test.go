// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"encoding/json"
	"errors"
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
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	pepper := []byte("test-pepper-32-bytes-aaaaaaaaaaaa")
	r, err := NewCachedResolver(inner, rc, pepper)
	if err != nil {
		t.Fatalf("NewCachedResolver: %v", err)
	}
	return r, mr, pepper
}

// TestCachedResolverMiss — empty cache; inner returns *KeyInfo. After
// Resolve the cache is populated (verified by miniredis state inspection
// — key "ach:key:<hex>" exists with TTL ≤ 60s).
func TestCachedResolverMiss(t *testing.T) {
	plaintext := "pk_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	expires := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) {
		return &KeyInfo{KeyID: "pkid_x", KeyType: keys.PrefixPk, OwnerEmail: "a@b", Status: "active", ExpiresAt: &expires}, nil
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
	want := &KeyInfo{KeyID: "pkid_cached", KeyType: keys.PrefixPk, OwnerEmail: "cached@b", Status: "active"}
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
func TestCachedResolverSingleFlight(t *testing.T) {
	const N = 50
	plaintext := "pk_cccccccccccccccccccccccccc"
	start := make(chan struct{})
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) {
		<-start // hold the leader until all goroutines are racing
		return &KeyInfo{KeyID: "pkid_sf", KeyType: keys.PrefixPk, OwnerEmail: "a@b", Status: "active"}, nil
	}}
	r, _, _ := setupCached(t, inner)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.Resolve(context.Background(), plaintext)
		}()
	}
	// Give every goroutine a chance to start and block on the leader
	// before releasing.
	time.Sleep(50 * time.Millisecond)
	close(start)
	wg.Wait()
	if inner.callCount() != 1 {
		t.Fatalf("expected exactly 1 inner call (single-flight), got %d", inner.callCount())
	}
}

// TestCachedResolverTTLExact — the Redis SET TTL is exactly 60s, no
// longer / no shorter.
func TestCachedResolverTTLExact(t *testing.T) {
	plaintext := "pk_dddddddddddddddddddddddddd"
	expires := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	inner := &fakeResolver{respond: func(string) (*KeyInfo, error) {
		return &KeyInfo{KeyID: "pkid_y", KeyType: keys.PrefixPk, OwnerEmail: "a@b", Status: "active", ExpiresAt: &expires}, nil
	}}
	r, mr, pepper := setupCached(t, inner)
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
	stub := &stubDB{pkResp: &KeyInfo{KeyID: "pkid_a", KeyType: keys.PrefixPk, OwnerEmail: "a@b", Status: "active", ExpiresAt: &expires}}
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
	stub := &stubDB{ekResp: &KeyInfo{KeyID: "ekid_b", KeyType: keys.PrefixEk, OwnerEmail: "a@b", Status: "active", Environment: "prod"}}
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
