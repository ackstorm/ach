// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ackstorm/ach/internal/keystore"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

// keyRecheckInterval spaces the key re-checks of a running request. It is the
// bound on how long a tunnel or stream outlives its key: the check reads the
// key row itself, not the 60-second resolver cache, so a request ends within
// about one interval of the suspend or revoke.
const keyRecheckInterval = 60 * time.Second

// keyRecheckTimeout bounds one re-check, so a stalled store counts as a failed
// check instead of holding the watcher.
const keyRecheckTimeout = 5 * time.Second

// errKeyNotLive is the cancel cause of a request whose key ended while it ran.
var errKeyNotLive = errors.New("key suspended, revoked or expired")

// KeyWatch ends a request with an ACH identity when its key does. Authn runs
// once per request, while a WebSocket tunnel or a streaming response can run
// far longer than a suspend or revoke takes to apply. Requests with no ACH
// identity pass unwatched: LiteLLM owns those keys.
func KeyWatch(live keystore.KeyLive, log *slog.Logger) func(http.Handler) http.Handler {
	return keyWatch(keyRecheckInterval, live, log)
}

func keyWatch(every time.Duration, live keystore.KeyLive, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			kc, ok := middleware.KeyContextFromCtx(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			ctx, stop := watchKey(r.Context(), every, kc.KeyID, func(ctx context.Context) (bool, error) {
				return live(ctx, kc.KeyType, kc.KeyID)
			}, log)
			defer stop()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// watchKey returns a child of parent that is cancelled with errKeyNotLive once
// check gives a definite "not live", first after every and then every interval
// until stop. A failed check leaves the request alone. stop must run when the
// request ends; a request shorter than every costs one timer and starts nothing.
//
// Cancelling is all it takes: ReverseProxy closes both sides of an upgraded
// tunnel and aborts a streaming response when the request context ends.
func watchKey(parent context.Context, every time.Duration, keyID string, check func(context.Context) (bool, error), log *slog.Logger) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	timer := time.AfterFunc(every, func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			checkCtx, cancelCheck := context.WithTimeout(ctx, keyRecheckTimeout)
			live, err := check(checkCtx)
			cancelCheck()
			switch {
			case ctx.Err() != nil: // the request ended first: the answer is moot
				return
			case err != nil:
				log.Warn("forwarder: key re-check failed; leaving the request running",
					"key_id", keyID, "request_id", middleware.RequestIDFromCtx(ctx), "err", err)
			case !live:
				log.Info("forwarder: key is no longer live; ending its request",
					"key_id", keyID, "request_id", middleware.RequestIDFromCtx(ctx))
				cancel(errKeyNotLive)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	return ctx, func() {
		timer.Stop()
		cancel(nil)
	}
}
