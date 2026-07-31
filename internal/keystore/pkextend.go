// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
)

// keyExtender is the narrow slice of the LiteLLM client this file needs.
// *litellm.RESTClient satisfies it; KeyExtend is deliberately absent from the
// broad litellm.Client interface (see internal/litellm/client.go).
type keyExtender interface {
	KeyExtend(ctx context.Context, keyID, duration string) error
}

// pkExtendTimeout bounds the background LiteLLM POST /key/update. It is
// generous relative to a healthy LiteLLM but finite: the goroutine must never
// outlive the usefulness of the update it carries, since the next request past
// the 5-minute debounce issues a fresh one anyway.
const pkExtendTimeout = 10 * time.Second

// NewLiteLLMPkExtendHook returns the production PkExtendHook: it mirrors ACH's
// slid pk_ expiry onto the backing LiteLLM key via POST /key/update.
//
// window is ACH's own sliding window (db.PkSlidingWindow) rendered as a
// LiteLLM duration string; LiteLLM re-bases the key's expiry to now()+window,
// which is exactly what PkCheckAndExtend just wrote to the ACH row, so the two
// stay in step. Hours dodge day-unit differences, matching the mint path.
//
// The call runs in a background goroutine under pkExtendTimeout: it is
// best-effort by construction and MUST NOT add latency to, or fail, the auth
// path it hangs off. Failures are logged at V(1) — a persistently unreachable
// LiteLLM is already loud elsewhere, and this path retries on the next request
// past the debounce window.
func NewLiteLLMPkExtendHook(client keyExtender, window time.Duration, log logr.Logger) PkExtendHook {
	duration := fmt.Sprintf("%dh", int(window.Hours()))
	return func(ctx context.Context, litellmToken string) {
		go func() {
			ctx, cancel := context.WithTimeout(ctx, pkExtendTimeout)
			defer cancel()
			if err := client.KeyExtend(ctx, litellmToken, duration); err != nil {
				// No token material in the log line — litellmToken is the
				// opaque LiteLLM token, still credential-adjacent (§16.1).
				log.V(1).Info("pk_ LiteLLM expiry mirror failed", "err", err)
			}
		}()
	}
}
