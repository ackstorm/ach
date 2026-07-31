// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

type recordingExtender struct {
	calls chan [2]string
	err   error
}

func (r *recordingExtender) KeyExtend(_ context.Context, keyID, duration string) error {
	r.calls <- [2]string{keyID, duration}
	return r.err
}

// TestNewLiteLLMPkExtendHook_SendsWindowAsHours pins the duration string handed
// to LiteLLM: it must equal ACH's own sliding window, or the two expiries drift
// apart again — the exact bug this hook exists to close.
func TestNewLiteLLMPkExtendHook_SendsWindowAsHours(t *testing.T) {
	ext := &recordingExtender{calls: make(chan [2]string, 1)}
	hook := NewLiteLLMPkExtendHook(ext, 7*24*time.Hour, logr.Discard())

	hook(context.Background(), "llm-token-abc")

	select {
	case got := <-ext.calls:
		if got[0] != "llm-token-abc" {
			t.Errorf("keyID=%q; want llm-token-abc", got[0])
		}
		if got[1] != "168h" {
			t.Errorf("duration=%q; want 168h", got[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook never called KeyExtend")
	}
}

// TestNewLiteLLMPkExtendHook_SwallowsErrorWithoutBlocking asserts the
// best-effort contract: a LiteLLM failure neither propagates nor stalls the
// caller, because the hook hangs off the auth path of every service.
func TestNewLiteLLMPkExtendHook_SwallowsErrorWithoutBlocking(t *testing.T) {
	// Unbuffered: KeyExtend parks until this test reads, so if the hook ran
	// inline instead of in the background, hook() below would deadlock.
	ext := &recordingExtender{calls: make(chan [2]string), err: errors.New("litellm down")}
	hook := NewLiteLLMPkExtendHook(ext, time.Hour, logr.Discard())

	hook(context.Background(), "tok")

	select {
	case <-ext.calls:
	case <-time.After(5 * time.Second):
		t.Fatal("hook never called KeyExtend")
	}
}
