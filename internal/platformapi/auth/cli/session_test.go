// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestRedis spins up an in-process miniredis instance + a go-redis
// client wired to it; both are torn down via t.Cleanup. Mirrors the
// keystore_test.go setupCached harness so the Phase 6 helpers slot into
// the same test discipline.
func newTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	return rc, mr
}

// makeCompleteSession builds a non-sentinel Session (KeyID set) for
// tests that exercise the consumed-path.
func makeCompleteSession() Session {
	return Session{
		KeyID:      "pkid_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Plaintext:  "pk_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		OwnerEmail: "alice@example.com",
		CreatedAt:  "2026-05-28T00:00:00Z",
	}
}

// makeSentinelSession builds a pending-sentinel Session (KeyID == "")
// used by InitHandler — the marker TokenHandler interprets as "still
// pending; do not consume yet".
func makeSentinelSession() Session {
	return Session{
		CreatedAt: "2026-05-28T00:00:00Z",
	}
}

// TestPutWritesAtPrefixedKeyWithTTL — Put stores the JSON-marshaled
// Session at "ach:cli-session:<id>" with TTL ~5 minutes.
func TestPutWritesAtPrefixedKeyWithTTL(t *testing.T) {
	rc, mr := newTestRedis(t)
	ctx := context.Background()

	sess := makeCompleteSession()
	if err := Put(ctx, rc, "abc", sess, DefaultSessionTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}

	wantKey := sessionKeyPrefix + "abc"
	if !mr.Exists(wantKey) {
		t.Fatalf("expected redis key %q to exist", wantKey)
	}
	ttl := mr.TTL(wantKey)
	// TTL must be in (4m, 5m]: just-set keys lose nothing on the test
	// clock; assert the floor so a future shorter default is caught.
	if ttl <= 4*time.Minute || ttl > DefaultSessionTTL {
		t.Errorf("ttl: got %v, want (4m, 5m]", ttl)
	}
}

// TestPeekIsNonDestructive — Peek returns the deserialized Session AND
// remaining TTL without deleting the key. A second Peek MUST observe
// the same payload (key still present).
func TestPeekIsNonDestructive(t *testing.T) {
	rc, mr := newTestRedis(t)
	ctx := context.Background()

	sess := makeCompleteSession()
	if err := Put(ctx, rc, "peek-id", sess, DefaultSessionTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ttl, err := Peek(ctx, rc, "peek-id")
	if err != nil {
		t.Fatalf("Peek (1): %v", err)
	}
	if got == nil || got.KeyID != sess.KeyID || got.Plaintext != sess.Plaintext || got.OwnerEmail != sess.OwnerEmail {
		t.Errorf("Peek (1) payload mismatch: got %+v want %+v", got, sess)
	}
	if ttl <= 0 || ttl > DefaultSessionTTL {
		t.Errorf("Peek (1) ttl: got %v, want (0, 5m]", ttl)
	}
	// Key MUST still exist after Peek (non-destructive).
	if !mr.Exists(sessionKeyPrefix + "peek-id") {
		t.Fatalf("Peek deleted the key; expected non-destructive read")
	}

	// Second Peek returns the same value.
	got2, _, err := Peek(ctx, rc, "peek-id")
	if err != nil {
		t.Fatalf("Peek (2): %v", err)
	}
	if got2 == nil || got2.KeyID != sess.KeyID {
		t.Errorf("Peek (2) payload mismatch: got %+v want %+v", got2, sess)
	}
}

// TestConsumeRemovesKey — Consume returns the deserialized Session AND
// deletes the key (GETDEL semantics). A second Consume MUST return
// ErrNotFound.
func TestConsumeRemovesKey(t *testing.T) {
	rc, mr := newTestRedis(t)
	ctx := context.Background()

	sess := makeCompleteSession()
	if err := Put(ctx, rc, "consume-id", sess, DefaultSessionTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := Consume(ctx, rc, "consume-id")
	if err != nil {
		t.Fatalf("Consume (1): %v", err)
	}
	if got == nil || got.KeyID != sess.KeyID || got.Plaintext != sess.Plaintext {
		t.Errorf("Consume (1) payload mismatch: got %+v want %+v", got, sess)
	}
	if mr.Exists(sessionKeyPrefix + "consume-id") {
		t.Fatalf("Consume did NOT delete the key; expected GETDEL semantics")
	}

	// Second Consume on the same id → ErrNotFound.
	got2, err := Consume(ctx, rc, "consume-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Consume (2): got err=%v, want ErrNotFound", err)
	}
	if got2 != nil {
		t.Errorf("Consume (2): got=%+v, want nil", got2)
	}
}

// TestPeekMissingReturnsNotFound — Peek on a never-existed id returns
// (nil, 0, ErrNotFound).
func TestPeekMissingReturnsNotFound(t *testing.T) {
	rc, _ := newTestRedis(t)
	ctx := context.Background()

	got, ttl, err := Peek(ctx, rc, "missing-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Peek: got err=%v, want ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("Peek: got=%+v, want nil", got)
	}
	if ttl != 0 {
		t.Errorf("Peek: got ttl=%v, want 0", ttl)
	}
}

// TestConsumeMissingReturnsNotFound — Consume on a never-existed id
// returns (nil, ErrNotFound).
func TestConsumeMissingReturnsNotFound(t *testing.T) {
	rc, _ := newTestRedis(t)
	ctx := context.Background()

	got, err := Consume(ctx, rc, "missing-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Consume: got err=%v, want ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("Consume: got=%+v, want nil", got)
	}
}

// TestPeekCorruptJSONPreservesKey — Peek on a key whose value is
// non-JSON garbage returns (nil, _, ErrCorruptSession) AND leaves the
// key in place (non-destructive contract on Peek).
func TestPeekCorruptJSONPreservesKey(t *testing.T) {
	rc, mr := newTestRedis(t)
	ctx := context.Background()

	// Write garbage directly through miniredis (bypass Put).
	if err := mr.Set(sessionKeyPrefix+"garbage", "not-json-{"); err != nil {
		t.Fatalf("miniredis Set: %v", err)
	}

	got, _, err := Peek(ctx, rc, "garbage")
	if !errors.Is(err, ErrCorruptSession) {
		t.Errorf("Peek: got err=%v, want ErrCorruptSession", err)
	}
	if got != nil {
		t.Errorf("Peek: got=%+v, want nil", got)
	}
	if !mr.Exists(sessionKeyPrefix + "garbage") {
		t.Errorf("Peek deleted the key on corrupt JSON; Peek must be non-destructive")
	}
}

// TestConsumeCorruptJSONDeletesKey — Consume on a key whose value is
// non-JSON garbage returns (nil, ErrCorruptSession) AND deletes the
// key (GETDEL is atomic; the value is gone whether json.Unmarshal
// succeeds or not — D-19 destructive contract).
func TestConsumeCorruptJSONDeletesKey(t *testing.T) {
	rc, mr := newTestRedis(t)
	ctx := context.Background()

	if err := mr.Set(sessionKeyPrefix+"garbage2", "not-json-{"); err != nil {
		t.Fatalf("miniredis Set: %v", err)
	}

	got, err := Consume(ctx, rc, "garbage2")
	if !errors.Is(err, ErrCorruptSession) {
		t.Errorf("Consume: got err=%v, want ErrCorruptSession", err)
	}
	if got != nil {
		t.Errorf("Consume: got=%+v, want nil", got)
	}
	if mr.Exists(sessionKeyPrefix + "garbage2") {
		t.Errorf("Consume did NOT delete the key on corrupt JSON; GETDEL is atomic")
	}
}

// TestNewSessionIDShapeAndUniqueness — NewSessionID returns a
// 32-character base64url-encoded string (24 bytes of entropy); two
// consecutive calls return distinct values.
func TestNewSessionIDShapeAndUniqueness(t *testing.T) {
	a, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID (a): %v", err)
	}
	b, err := NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID (b): %v", err)
	}
	if a == b {
		t.Errorf("two consecutive NewSessionID returned the same value: %q", a)
	}
	// 24 random bytes → base64.RawURLEncoding length = ceil(24*8/6) = 32.
	if len(a) != 32 {
		t.Errorf("NewSessionID(a) length: got %d, want 32", len(a))
	}
	if len(b) != 32 {
		t.Errorf("NewSessionID(b) length: got %d, want 32", len(b))
	}
	// base64url alphabet: A-Z a-z 0-9 - _ (no padding).
	for _, r := range a {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_') {
			t.Errorf("NewSessionID contains non-base64url char: %q", r)
			break
		}
	}
}

// TestSentinelSessionRoundTrip — a Put / Peek round-trip on a sentinel
// (KeyID == "") payload preserves the empty KeyID — TokenHandler relies
// on this discriminator to decide pending vs complete.
func TestSentinelSessionRoundTrip(t *testing.T) {
	rc, _ := newTestRedis(t)
	ctx := context.Background()

	if err := Put(ctx, rc, "pending-id", makeSentinelSession(), DefaultSessionTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, _, err := Peek(ctx, rc, "pending-id")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if got == nil {
		t.Fatalf("Peek: got nil session")
	}
	if got.KeyID != "" {
		t.Errorf("sentinel KeyID corrupted: got %q, want empty", got.KeyID)
	}
	if got.Plaintext != "" {
		t.Errorf("sentinel Plaintext corrupted: got %q, want empty", got.Plaintext)
	}
}
