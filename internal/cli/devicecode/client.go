// SPDX-License-Identifier: Apache-2.0

package devicecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// InitResponse mirrors the server-side init.InitResponse wire shape
// (see internal/platformapi/auth/cli/init.go). expires_in is the
// session TTL in whole seconds; poll_interval is the server's
// recommended cadence between successive /token POSTs.
type InitResponse struct {
	SessionID       string `json:"session_id"`
	VerificationURL string `json:"verification_url"`
	PollInterval    int    `json:"poll_interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// TokenResponse mirrors the server-side token.TokenResponse. Plaintext
// crosses this boundary EXACTLY ONCE per session (the server enforces
// one-shot via Redis GETDEL on /token success).
type TokenResponse struct {
	KeyID      string `json:"key_id"`
	Plaintext  string `json:"plaintext"`
	OwnerEmail string `json:"owner_email"`
}

// tokenRequest is the wire body for POST /platform/auth/cli/token.
type tokenRequest struct {
	SessionID string `json:"session_id"`
}

// ErrLoginTimeout is returned by PollToken when totalTimeout elapses
// before the server returns 200. Distinct from context.DeadlineExceeded
// so the CLI can render a user-friendly "login timed out — please rerun
// `ach login`" message instead of the generic ctx error.
var ErrLoginTimeout = errors.New("devicecode: login timed out before completion")

// httpTimeout is the per-request wall-clock cap on a single Init or
// poll-tick HTTP call. Deliberately shorter than internal/cli/httpclient's
// 60s default — the device-code endpoints are anonymous and the server
// returns 202 within a few milliseconds, so 30s is generous.
const httpTimeout = 30 * time.Second

// max5xxRetries bounds the consecutive-5xx retry budget inside
// PollToken. A 4th 5xx in a row bubbles up as *ServerError.
const max5xxRetries = 3

// Opener is the package-level seam that opens a URL in the user's
// default browser. The production default dispatches to xdg-open /
// open / rundll32 per GOOS. Unit tests (and the `ach login --no-browser`
// flag in cmd/ach/cmd/login.go) override this var with a no-op.
//
// Calls are best-effort; a non-nil return is surfaced to the caller
// which is expected to fall back to printing the URL.
var Opener func(url string) error = openInBrowser

// Init issues POST /platform/auth/cli/init with an empty `{}` body
// (the server accepts both no-body and `{}`; we send the explicit form
// for clarity and to set Content-Type predictably). Returns the
// decoded InitResponse on 2xx; returns *httpclient.ServerError on
// any non-2xx. Honors ctx for cancellation throughout.
func Init(ctx context.Context, baseURL string) (*InitResponse, error) {
	url := strings.TrimRight(baseURL, "/") + "/platform/auth/cli/init"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("devicecode: build init request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("devicecode: init request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeServerError(resp)
	}

	var ir InitResponse
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&ir); err != nil {
		return nil, fmt.Errorf("devicecode: decode init response: %w", err)
	}
	return &ir, nil
}

// PollToken polls POST /platform/auth/cli/token at pollInterval cadence
// until one of four outcomes:
//
//   - 200 → returns the decoded TokenResponse (success path).
//   - non-pending 4xx / persistent 5xx → returns *httpclient.ServerError.
//   - ctx cancelled → returns ctx.Err().
//   - totalTimeout elapsed → returns ErrLoginTimeout.
//
// 202 pending responses extend the loop; the function sleeps for
// pollInterval (interruptible by ctx and totalTimeout) and re-issues.
// Up to max5xxRetries consecutive 5xx responses are tolerated; the
// next 5xx after that bubbles up.
func PollToken(ctx context.Context, baseURL, sessionID string, pollInterval, totalTimeout time.Duration) (*TokenResponse, error) {
	if pollInterval <= 0 {
		return nil, fmt.Errorf("devicecode: pollInterval must be > 0; got %v", pollInterval)
	}
	deadline := time.After(totalTimeout)
	url := strings.TrimRight(baseURL, "/") + "/platform/auth/cli/token"

	var consecutive5xx int
	for {
		// Honor ctx + deadline BEFORE each request (so a flapping
		// server doesn't burn the deadline behind our back).
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case <-deadline:
			return nil, ErrLoginTimeout
		default:
		}

		tr, sErr, transientErr := pollOnce(ctx, url, sessionID)
		switch {
		case tr != nil:
			return tr, nil
		case sErr != nil:
			// 5xx → bounded retry.
			if sErr.Status >= 500 && sErr.Status < 600 {
				consecutive5xx++
				if consecutive5xx > max5xxRetries {
					return nil, sErr
				}
				// Fall through to the wait branch.
			} else if sErr.Status == http.StatusAccepted {
				// 202 pending — reset 5xx counter and loop.
				consecutive5xx = 0
			} else {
				// Any other 4xx (incl. 404 session_not_found / 410
				// session_expired aliased to 404 at the wire) is
				// terminal.
				return nil, sErr
			}
		case transientErr != nil:
			// Transport error — treat as transient like 5xx so a
			// momentary network blip doesn't fail the whole login.
			consecutive5xx++
			if consecutive5xx > max5xxRetries {
				return nil, transientErr
			}
		}

		// Wait for the next poll tick OR ctx cancel OR deadline.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, ErrLoginTimeout
		case <-time.After(pollInterval):
		}
	}
}

// pollOnce issues one POST /platform/auth/cli/token call. The three
// return values are mutually exclusive:
//
//   - tr != nil           → 200 success (caller returns to user).
//   - sErr != nil         → server returned 202/4xx/5xx with a
//     decodable envelope. Status field carries the wire status so the
//     caller can branch on pending vs terminal vs transient.
//   - transientErr != nil → transport error (server unreachable, TLS
//     handshake fail, etc.).
func pollOnce(ctx context.Context, url, sessionID string) (*TokenResponse, *httpclient.ServerError, error) {
	body, err := json.Marshal(tokenRequest{SessionID: sessionID})
	if err != nil {
		return nil, nil, fmt.Errorf("devicecode: marshal token request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("devicecode: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("devicecode: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusOK:
		var tr TokenResponse
		if derr := json.NewDecoder(resp.Body).Decode(&tr); derr != nil {
			return nil, nil, fmt.Errorf("devicecode: decode token response: %w", derr)
		}
		return &tr, nil, nil
	case resp.StatusCode == http.StatusAccepted:
		// 202 — drain body (server emits {status:"pending"}) and
		// surface as ServerError so the loop can branch on Status.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, &httpclient.ServerError{Status: http.StatusAccepted, Code: "pending"}, nil
	default:
		return nil, decodeServerError(resp), nil
	}
}

// decodeServerError mirrors httpclient.decodeServerError but is
// inlined here because the upstream version is unexported. Reading
// the response body up-front (rather than passing the *Response) lets
// callers defer the Close() in their own flow.
func decodeServerError(resp *http.Response) *httpclient.ServerError {
	sErr := &httpclient.ServerError{Status: resp.StatusCode}
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		sErr.Underlying = fmt.Errorf("%w: read body: %v", httpclient.ErrEnvelopeDecode, readErr)
		return sErr
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		sErr.Underlying = fmt.Errorf("%w: %v", httpclient.ErrEnvelopeDecode, err)
		return sErr
	}
	sErr.Code = envelope.Error.Code
	sErr.Message = envelope.Error.Message
	sErr.RequestID = envelope.RequestID
	return sErr
}

// newHTTPClient returns a fresh stdlib *http.Client with httpTimeout.
// Anonymous endpoints don't need cookie persistence — the bare client
// is sufficient.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: httpTimeout}
}

// openInBrowser is the production Opener. It dispatches to the
// platform-native URL-open command:
//
//   - linux: xdg-open <url>
//   - darwin: open <url>
//   - windows: rundll32 url.dll,FileProtocolHandler <url>
//
// All other GOOS values return an error so the caller falls back to
// printing the URL.
func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("devicecode: unsupported GOOS %q for browser open", runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("devicecode: start browser: %w", err)
	}
	// Release the process — we don't wait. The caller falls through to
	// the poll loop while the browser does its thing.
	return cmd.Process.Release()
}
