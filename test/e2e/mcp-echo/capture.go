// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	echojwt "github.com/ackstorm/ach/test/e2e/mcp-echo/jwt"
)

// capture is the singleton record of the last request the backend saw,
// shaped to mirror test/e2e/mock/main.go's surface so the existing e2e
// harness can poll /__capture/last with no special-casing.
type capture struct {
	mu                sync.Mutex
	Method            string
	Path              string
	Headers           map[string][]string
	BodyRaw           string
	Body              json.RawMessage
	AuthorizationSeen string
	JWTPresent        bool
	JWTClaims         echojwt.Verified
	At                time.Time
}

type captureView struct {
	Method            string              `json:"method"`
	Path              string              `json:"path"`
	Headers           map[string][]string `json:"headers"`
	Body              json.RawMessage     `json:"body"`
	BodyRaw           string              `json:"body_raw"`
	AuthorizationSeen string              `json:"authorization_seen"`
	// JWTPresent is true when the request carried a Bearer token the
	// verifier accepted; false on the optional-mode no-JWT path
	// (ACH_REQUIRE_JWT=false). The BIP closed-loop e2e asserts it is true
	// on the forwardIdentityJWT=true route and false on the =false route.
	JWTPresent bool             `json:"jwt_present"`
	JWTClaims  echojwt.Verified `json:"jwt_claims"`
	At         time.Time        `json:"at"`
}

func newCapture() *capture { return &capture{} }

func (c *capture) record(r *http.Request, body []byte, v echojwt.Verified, jwtPresent bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Method = r.Method
	c.Path = r.URL.Path
	c.Headers = copyHeaders(r.Header)
	c.AuthorizationSeen = r.Header.Get("Authorization")
	c.JWTPresent = jwtPresent
	c.JWTClaims = v
	c.BodyRaw = string(body)
	c.Body = nil
	if json.Valid(body) {
		c.Body = append(json.RawMessage(nil), body...)
	}
	c.At = time.Now().UTC()
}

func (c *capture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Reset all fields individually — `*c = capture{}` would zero the
	// embedded sync.Mutex while it's held, panicking on the deferred
	// Unlock.
	c.Method = ""
	c.Path = ""
	c.Headers = nil
	c.BodyRaw = ""
	c.Body = nil
	c.AuthorizationSeen = ""
	c.JWTPresent = false
	c.JWTClaims = echojwt.Verified{}
	c.At = time.Time{}
}

func (c *capture) snapshot() captureView {
	c.mu.Lock()
	defer c.mu.Unlock()
	return captureView{
		Method:            c.Method,
		Path:              c.Path,
		Headers:           copyHeaders(c.Headers),
		Body:              append(json.RawMessage(nil), c.Body...),
		BodyRaw:           c.BodyRaw,
		AuthorizationSeen: c.AuthorizationSeen,
		JWTPresent:        c.JWTPresent,
		JWTClaims:         c.JWTClaims,
		At:                c.At,
	}
}

func copyHeaders(in http.Header) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string{}, v...)
	}
	return out
}
