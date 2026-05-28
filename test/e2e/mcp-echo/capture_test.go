// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"testing"

	echojwt "github.com/ackstorm/ach/test/e2e/mcp-echo/jwt"
)

func TestCapture_RecordAndSnapshot(t *testing.T) {
	c := newCapture()
	req, _ := http.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer xxx")
	c.record(req, []byte(`{"hello":"world"}`), echojwt.Verified{
		Iss: "https://hub.example",
		Sub: "ach-system/alice@example.com",
		Aud: "mcp:demo-mcp-echo",
		Kid: "k1",
		Iat: 1,
		Exp: 121,
	})

	snap := c.snapshot()
	if snap.AuthorizationSeen != "Bearer xxx" {
		t.Fatalf("authorization not captured: %q", snap.AuthorizationSeen)
	}
	if snap.JWTClaims.Iss != "https://hub.example" {
		t.Fatalf("claims not captured: %+v", snap.JWTClaims)
	}
	if snap.BodyRaw != `{"hello":"world"}` {
		t.Fatalf("body not captured: %q", snap.BodyRaw)
	}
}

func TestCapture_Reset(t *testing.T) {
	c := newCapture()
	req, _ := http.NewRequest("GET", "/", nil)
	c.record(req, nil, echojwt.Verified{Sub: "x"})
	c.reset()
	if c.snapshot().JWTClaims.Sub != "" {
		t.Fatalf("reset did not clear claims")
	}
}
