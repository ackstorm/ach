// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// sessionKeyPrefix is the Redis namespace shared across init/token
// handlers and the D-20 sso.CallbackHandler writeback (Phase 6 D-19).
//
// Wire shape: "ach:cli-session:<session_id>" where session_id is a
// 32-char base64url-encoded value minted by NewSessionID. Value is the
// JSON encoding of Session. TTL is bounded by DefaultSessionTTL so a
// dropped CLI cannot leak a pending pk_ payload indefinitely.
const sessionKeyPrefix = "ach:cli-session:"

// DefaultSessionTTL is the upper bound on the lifetime of a single
// device-code session per D-02 (Claude's discretion — recommend 5
// min). InitHandler writes the sentinel with this TTL; TokenHandler's
// pending-poll branch re-puts it with this TTL so polling does not
// race the TTL bust. The post-callback completed Session inherits the
// same ceiling — once Consume succeeds the key is gone (GETDEL).
const DefaultSessionTTL = 5 * time.Minute

// DefaultPollInterval is the recommended poll cadence the CLI honors
// between successive /token POSTs (D-02 Claude's discretion). Surfaced
// via the InitResponse.poll_interval field so the client doesn't have
// to hard-code it.
const DefaultPollInterval = 2 * time.Second

// Session is the JSON-serialized payload stored at
// sessionKeyPrefix+id. Populated either as a pending sentinel by
// InitHandler (all fields zero except CreatedAt) or as a completed
// payload by sso.CallbackHandler once the Dex round-trip lands the pk_.
//
// Discriminator (TokenHandler relies on this): KeyID == "" → still
// pending; KeyID != "" → ready to consume. The pk_ Plaintext is the
// load-bearing payload — it appears EXACTLY ONCE in the /token 200
// response body and MUST NOT flow through any logger (Pattern S5).
type Session struct {
	KeyID      string `json:"key_id"`
	Plaintext  string `json:"plaintext"`
	OwnerEmail string `json:"owner_email"`
	CreatedAt  string `json:"created_at"`
}

// ErrNotFound is returned by Peek / Consume when the requested session
// is absent — TTL bust, never-existed, or already-consumed (GETDEL).
// Handlers map this to 404 session_not_found per D-02; the alias
// covers what the spec earmarks for 410 session_expired (planner
// decision documented in CONTEXT.md D-02 — pragmatic since the helper
// cannot tell TTL-bust from never-existed).
var ErrNotFound = errors.New("cli: session not found")

// ErrCorruptSession is returned by Peek / Consume when the stored
// value at sessionKeyPrefix+id is non-JSON. Handlers map this to 500
// internal_error — corruption signals a Redis-side write that
// bypassed Put, not a normal flow.
var ErrCorruptSession = errors.New("cli: session payload corrupt")

// NewSessionID returns a base64url-encoded 24-byte random identifier
// (32 ASCII chars, no padding). 24 bytes = 192 bits of entropy — well
// above the brute-force ceiling inside the 5-minute TTL window
// (T-06-02-01 mitigation).
func NewSessionID() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// Put writes s at sessionKeyPrefix+id with TTL. JSON-marshal failure
// surfaces as the returned error; the Redis SET error (transport,
// auth) surfaces verbatim.
//
// Both InitHandler (sentinel write) and sso.CallbackHandler (D-20
// completed-Session writeback) call Put. TokenHandler's pending-poll
// branch also calls Put to refresh the sentinel TTL so a slow user
// does not lose the session mid-flight.
func Put(ctx context.Context, rdb *redis.Client, id string, s Session, ttl time.Duration) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, sessionKeyPrefix+id, b, ttl).Err()
}

// Peek returns the deserialized Session and remaining TTL WITHOUT
// deleting the key. TokenHandler uses Peek on every poll to check the
// pending-sentinel discriminator (KeyID == ""); only once the
// discriminator flips does it call Consume.
//
// Absent key → (nil, 0, ErrNotFound).
// Corrupt JSON → (nil, 0, ErrCorruptSession); key is LEFT IN PLACE
// (Peek is non-destructive — only Consume removes a corrupted entry).
// Transport / Redis error → (nil, 0, raw error).
func Peek(ctx context.Context, rdb *redis.Client, id string) (*Session, time.Duration, error) {
	key := sessionKeyPrefix + id
	val, err := rdb.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	var s Session
	if uerr := json.Unmarshal([]byte(val), &s); uerr != nil {
		return nil, 0, ErrCorruptSession
	}
	ttl, ttlErr := rdb.TTL(ctx, key).Result()
	if ttlErr != nil {
		// TTL error is non-fatal — return the payload with a zero TTL
		// rather than masking a successful Peek behind a transient
		// PTTL failure. Caller branches on payload presence.
		ttl = 0
	}
	return &s, ttl, nil
}

// Consume atomically reads + deletes the session at
// sessionKeyPrefix+id via Redis GETDEL (Redis 6.2+). One-shot
// semantics: a second Consume on the same id returns ErrNotFound.
//
// Absent key → (nil, ErrNotFound).
// Corrupt JSON → (nil, ErrCorruptSession); GETDEL is atomic so the
// key is ALREADY GONE by the time json.Unmarshal runs — corruption
// does not leak.
// Transport / Redis error → (nil, raw error).
func Consume(ctx context.Context, rdb *redis.Client, id string) (*Session, error) {
	val, err := rdb.GetDel(ctx, sessionKeyPrefix+id).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var s Session
	if uerr := json.Unmarshal([]byte(val), &s); uerr != nil {
		return nil, ErrCorruptSession
	}
	return &s, nil
}
