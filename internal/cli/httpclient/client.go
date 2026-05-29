// SPDX-License-Identifier: Apache-2.0

// Package httpclient is the CLI's outbound HTTP client. It wraps
// net/http with three primitives every cobra subcommand needs:
//
//   - x-ach-key carrier (pk_/ek_ resolved by the caller)
//   - §15.5 error envelope decode → *ServerError on non-2xx
//   - --verbose header dump via the sibling Redact / HeaderDump helpers
//
// Two methods diverge only on the response side:
//
//   - Do composes the request, fires it, and JSON-decodes a typed
//     2xx body into the provided out struct.
//   - DoRaw composes the same request but returns the live
//     *http.Response on 2xx so callers can io.Copy the body verbatim
//     (used by `ach hydrate` to stream byte-for-byte JSON to stdout
//     and by `ach whoami --verify` for the ek_ Accept-Encoding: gzip
//     probe).
//
// ExtraHeaders is a foundation contract — every Do/DoRaw call applies
// it before fire. Downstream consumers (whoami ek_, hydrate, future
// adapters) set it directly on the Client without needing a per-call
// override hook.
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultTimeout is the wall-clock cap on a single Do/DoRaw call
// (Claude's Discretion per 06-CONTEXT.md D-19). 60s covers the slow
// hydrate path comfortably while ensuring the CLI never hangs
// indefinitely on a wedged Platform API.
const defaultTimeout = 60 * time.Second

// Client is the per-process outbound HTTP client. Construct with at
// least BaseURL + APIKey populated; the zero value of HTTPClient,
// Verbose, Stderr, and ExtraHeaders is safe to leave unset.
type Client struct {
	// BaseURL is the deployment URL (deployments.<active>.url).
	BaseURL string

	// APIKey is the resolved pk_ or ek_ for the active deployment.
	// Carried in every request as `x-ach-key: <APIKey>` (Hub §5).
	APIKey string

	// HTTPClient is the underlying transport. nil defaults to a
	// fresh *http.Client{Timeout: defaultTimeout}.
	HTTPClient *http.Client

	// Verbose, when true, writes a redacted request-line + header
	// dump to Stderr before each request (D-15).
	Verbose bool

	// Stderr is the verbose-mode sink. nil defaults to os.Stderr at
	// the call site; tests inject a bytes.Buffer.
	Stderr io.Writer

	// ExtraHeaders are applied to every outbound request after the
	// canonical headers (x-ach-key, Content-Type, Accept). nil leaves
	// the request unchanged.
	ExtraHeaders http.Header
}

// ServerError is the §15.5 error envelope decoded into a typed value.
// It implements the standard error interface with the format
// "%d %s: %s (request_id=%s)" so cobra's RunE renderer prints a
// human-readable line and cmd/ach/main.go's errors.As branch can map
// it to an exit code (Pattern P12).
type ServerError struct {
	// Status is the HTTP status code.
	Status int
	// Code is `error.code` from the envelope (e.g. "not_admin").
	Code string
	// Message is `error.message` from the envelope.
	Message string
	// RequestID is `request_id` from the envelope (req_<ulid>).
	RequestID string
	// Underlying carries the wrapped decode failure when the body
	// couldn't be parsed as a §15.5 envelope (see ErrEnvelopeDecode).
	// Typically nil.
	Underlying error
}

// Error renders the §15.5 error envelope as a one-line string.
func (e *ServerError) Error() string {
	return fmt.Sprintf("%d %s: %s (request_id=%s)", e.Status, e.Code, e.Message, e.RequestID)
}

// Unwrap exposes Underlying so errors.Is(err, ErrEnvelopeDecode)
// works on malformed-envelope failures.
func (e *ServerError) Unwrap() error { return e.Underlying }

// ErrEnvelopeDecode is wrapped by *ServerError.Underlying when the
// non-2xx response body could not be parsed as a §15.5 envelope. It
// lets callers distinguish "server returned a structured error" from
// "server returned garbage on a 4xx/5xx" without inspecting the
// Code/Message zero values.
var ErrEnvelopeDecode = errors.New("httpclient: error envelope decode failed")

// Do issues req at method+path against BaseURL with the x-ach-key
// header set + the JSON body (if any). On 2xx, decodes the response
// body into out when non-nil. On non-2xx, decodes the §15.5 envelope
// into a *ServerError and returns it.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeServerError(resp)
	}
	if out == nil {
		// Drain body to allow connection reuse.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	// No DisallowUnknownFields: callers decode into lean local views
	// (e.g. render.EnvView projects only name/namespace/status from the
	// richer /platform/environments payload, which also carries
	// authorizedTeams/context/runtime/conditions/origin/locked). Rejecting
	// unknown fields would couple every CLI view to the full server shape
	// and break on any additive server field — the opposite of forward
	// compatibility.
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("httpclient: decode response body: %w", err)
	}
	return nil
}

// DoRaw is Do without the response decode. On 2xx it returns the
// live *http.Response — the caller owns Close() and is expected to
// io.Copy the Body verbatim (preserves server bytes for byte-for-byte
// stdout dumps in `ach hydrate`). On non-2xx, body is consumed for
// the envelope decode and returned as *ServerError; the returned
// *http.Response is nil.
func (c *Client) DoRaw(ctx context.Context, method, path string, body any) (*http.Response, error) {
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		sErr := decodeServerError(resp)
		_ = resp.Body.Close()
		return nil, sErr
	}
	return resp, nil
}

// do is the shared request composition used by Do and DoRaw. It
// builds the URL, JSON-encodes the body, sets the canonical headers,
// applies ExtraHeaders, optionally writes the verbose dump, and
// fires via HTTPClient.
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("httpclient: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	url := strings.TrimRight(c.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("httpclient: build request: %w", err)
	}
	req.Header.Set("x-ach-key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, vs := range c.ExtraHeaders {
		// Reset then set each value so multi-value headers survive.
		req.Header.Del(name)
		for _, v := range vs {
			req.Header.Add(name, v)
		}
	}
	if c.Verbose {
		c.dumpVerbose(req)
	}
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpclient: do: %w", err)
	}
	return resp, nil
}

// dumpVerbose writes the request line + header dump to c.Stderr. nil
// Stderr falls through to /dev/null at this layer (the cobra root is
// expected to inject os.Stderr explicitly when Verbose is wired from
// the --verbose flag); the silent fallback prevents test fixtures
// from accidentally leaking dumps when they don't set Stderr.
func (c *Client) dumpVerbose(req *http.Request) {
	if c.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(c.Stderr, "%s %s %s\n", req.Method, req.URL.RequestURI(), req.Proto)
	_, _ = io.WriteString(c.Stderr, HeaderDump(req.Header))
}

// decodeServerError reads the response body and tries to decode it
// as a §15.5 envelope. Returns a populated *ServerError on success
// and an envelope-decode-wrapping *ServerError on failure.
func decodeServerError(resp *http.Response) *ServerError {
	sErr := &ServerError{Status: resp.StatusCode}
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		sErr.Underlying = fmt.Errorf("%w: read body: %v", ErrEnvelopeDecode, readErr)
		return sErr
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	// No DisallowUnknownFields (CR-01): tolerate additive server error
	// envelope fields (e.g. a future retry_after). The envelope decode
	// extracts only code/message/request_id; an unknown field must not
	// turn a structured server error into an opaque ErrEnvelopeDecode.
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&envelope); err != nil {
		sErr.Underlying = fmt.Errorf("%w: %v", ErrEnvelopeDecode, err)
		return sErr
	}
	sErr.Code = envelope.Error.Code
	sErr.Message = envelope.Error.Message
	sErr.RequestID = envelope.RequestID
	return sErr
}
