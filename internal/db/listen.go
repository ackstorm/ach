// SPDX-License-Identifier: Apache-2.0

// Package db pgx LISTEN/NOTIFY runnable with auto-reconnect (issue #34).
//
// Listener subscribes to one or more Postgres NOTIFY channels using a
// dedicated, long-lived pgx.Conn (NOT acquired from the pool — see revision-1
// note in the plan; a parked pool conn starves other queries). It runs until
// ctx is cancelled and auto-reconnects on transient errors with capped
// exponential backoff.
//
// Consumers call Subscribe(channel, handler) BEFORE Run(ctx). Subscriptions
// added after Run() are picked up on the next reconnect; the Listener does
// NOT replay missed events. Consumers MUST pair Listener with a periodic
// full-refresh (5m ticker) to recover from dropped LISTEN sessions —
// internal/forwarder/bipcache and internal/forwarder/envstore both
// implement this safety net.

package db

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler is invoked once per NOTIFY received on a subscribed channel. It
// runs in the Listener's goroutine — handlers MUST NOT block. Heavy work
// belongs in a goroutine the handler spawns.
type Handler func(payload string)

// Listener wraps a single pgx.Conn dedicated to LISTEN with a map of
// channel→handler. The conn is opened in runOnce; the Run loop reconnects on
// disconnect with capped exponential backoff.
type Listener struct {
	connString string
	log        logr.Logger

	mu   sync.RWMutex
	subs map[string]Handler // channel → handler
}

// NewListener takes a *pgxpool.Pool only to read the connection string from
// it; it does NOT hold a reference to the pool. The Listener opens its own
// dedicated conn via pgx.Connect in runOnce.
func NewListener(pool *pgxpool.Pool, log logr.Logger) *Listener {
	return &Listener{
		connString: pool.Config().ConnString(),
		log:        log,
		subs:       map[string]Handler{},
	}
}

// Subscribe registers a handler for the given channel. Channels must be
// valid identifiers (validChannel — see notify.go). Late Subscribe calls
// (after Run() started) are picked up on the next reconnect.
func (l *Listener) Subscribe(channel string, h Handler) {
	l.mu.Lock()
	l.subs[channel] = h
	l.mu.Unlock()
}

// Run blocks until ctx is cancelled. Internally it opens a fresh dedicated
// pgx.Conn, issues LISTEN for every subscribed channel, and dispatches
// notifications. On any error it logs and reconnects with backoff (100ms →
// 30s cap).
func (l *Listener) Run(ctx context.Context) error {
	backoff := 100 * time.Millisecond
	const backoffMax = 30 * time.Second
	for {
		err := l.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			l.log.Error(err, "listen session ended; reconnecting", "backoff", backoff.String())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < backoffMax {
			backoff *= 2
			if backoff > backoffMax {
				backoff = backoffMax
			}
		}
	}
}

func (l *Listener) runOnce(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, l.connString)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()

	l.mu.RLock()
	channels := make([]string, 0, len(l.subs))
	for c := range l.subs {
		channels = append(channels, c)
	}
	l.mu.RUnlock()

	for _, c := range channels {
		if !validChannel(c) {
			return &invalidChannelErr{name: c}
		}
		if _, err := conn.Exec(ctx, "LISTEN "+c); err != nil {
			return err
		}
	}

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		l.mu.RLock()
		h := l.subs[n.Channel]
		l.mu.RUnlock()
		if h != nil {
			h(n.Payload)
		}
	}
}

// RunRefreshLoop drives the standard cache-freshness lifecycle shared by
// the forwarder's bipcache and envstore: an initial refresh, a LISTEN
// subscription on channel for event-driven refresh, and a periodic
// ticker safety-net (LISTEN is at-most-once on session loss). Blocks
// until ctx is cancelled and returns ctx.Err(). refresh errors are
// logged (the caller's log carries the component name via WithName) and
// retried on the next NOTIFY/tick — never fatal.
func RunRefreshLoop(
	ctx context.Context,
	pool *pgxpool.Pool,
	channel string,
	interval time.Duration,
	log logr.Logger,
	refresh func(context.Context) error,
) error {
	if err := refresh(ctx); err != nil {
		log.Error(err, "initial refresh failed; will retry on next NOTIFY or tick")
	}

	lis := NewListener(pool, log.WithName("listen"))
	lis.Subscribe(channel, func(_ string) {
		if err := refresh(ctx); err != nil {
			log.Error(err, "notify-driven refresh failed")
		}
	})
	go func() { _ = lis.Run(ctx) }()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := refresh(ctx); err != nil {
				log.Error(err, "periodic refresh failed")
			}
		}
	}
}

type invalidChannelErr struct{ name string }

func (e *invalidChannelErr) Error() string {
	return "db.Listener: invalid channel name: " + e.name
}
