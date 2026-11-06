// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"
	"strings"
	"testing"
)

const validRegisterBody = `{"redirect_uris":["http://127.0.0.1:5000/cb"]}`

func errorField(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, `"error":"`+want+`"`) {
		t.Fatalf("error code: want %s, got %s", want, body)
	}
}

func TestRegister_BodyCapIs4KiB(t *testing.T) {
	f := newAS(t)
	const prefix, suffix = `{"redirect_uris":["http://127.0.0.1:5000/cb"],"padding":"`, `"}`
	atCap := prefix + strings.Repeat("x", 4<<10-len(prefix)-len(suffix)) + suffix
	if w := f.do(t, "POST", "/platform/oauth/register", atCap, nil); w.Code != 201 {
		t.Fatalf("body at the cap: %d %s", w.Code, w.Body)
	}
	over := prefix + strings.Repeat("x", 4<<10-len(prefix)-len(suffix)+1) + suffix
	if w := f.do(t, "POST", "/platform/oauth/register", over, nil); w.Code != 413 {
		t.Fatalf("oversized body: %d %s", w.Code, w.Body)
	}
}

// The retained bound counts the stored record, so a body within the cap whose
// record is not is refused like an oversized one.
func TestRegister_StoredRecordIsHeldToTheCap(t *testing.T) {
	f := newAS(t)
	body := `{"redirect_uris":["http://127.0.0.1:5000/cb?` + strings.Repeat("&", 3<<10) + `"]}`
	w := f.do(t, "POST", "/platform/oauth/register", body, nil)
	if w.Code != 413 {
		t.Fatalf("record over the cap: %d (%d bytes back)", w.Code, w.Body.Len())
	}
	errorField(t, w.Body.String(), "invalid_client_metadata")
}

func TestRegister_ClientNameCap(t *testing.T) {
	f := newAS(t)
	reg := func(n int) (int, string) {
		w := f.do(t, "POST", "/platform/oauth/register", map[string]any{
			"client_name": strings.Repeat("n", n), "redirect_uris": []string{"http://127.0.0.1:5000/cb"},
		}, nil)
		return w.Code, w.Body.String()
	}
	if code, body := reg(256); code != 201 {
		t.Fatalf("256 bytes: %d %s", code, body)
	}
	code, body := reg(257)
	if code != 400 {
		t.Fatalf("257 bytes: %d %s", code, body)
	}
	errorField(t, body, "invalid_client_metadata")
}

func TestRegister_PerClientBurstIs429(t *testing.T) {
	f := newAS(t)
	hdr := map[string]string{"X-Forwarded-For": "203.0.113.7"}
	for i := range 1000 {
		w := f.do(t, "POST", "/platform/oauth/register", validRegisterBody, hdr)
		if w.Code == 429 {
			errorField(t, w.Body.String(), "rate_limited")
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
			other := map[string]string{"X-Forwarded-For": "203.0.113.8"}
			if w := f.do(t, "POST", "/platform/oauth/register", validRegisterBody, other); w.Code != 201 {
				t.Fatalf("another client after one hit its limit: %d %s", w.Code, w.Body)
			}
			return
		}
		if w.Code != 201 {
			t.Fatalf("request %d: %d %s", i, w.Code, w.Body)
		}
	}
	t.Fatal("the per-client limit never answered 429")
}

// The per-client bucket is best-effort; the global one is the hard bound on
// what gets stored, whatever the number of clients.
func TestRegister_GlobalBucketBoundsAllClients(t *testing.T) {
	f := newAS(t)
	for i := range 2000 {
		hdr := map[string]string{"X-Forwarded-For": fmt.Sprintf("10.%d.%d.1", i/250, i%250)}
		if w := f.do(t, "POST", "/platform/oauth/register", validRegisterBody, hdr); w.Code == 429 {
			errorField(t, w.Body.String(), "rate_limited")
			return
		}
	}
	t.Fatal("the global limit never answered 429")
}

// Refused bodies store nothing, so they must not spend the global budget
// real registrations depend on.
func TestRegister_InvalidBodiesDoNotDrainTheGlobalBucket(t *testing.T) {
	f := newAS(t)
	for i := range 2000 {
		hdr := map[string]string{"X-Forwarded-For": fmt.Sprintf("10.%d.%d.1", i/250, i%250)}
		if w := f.do(t, "POST", "/platform/oauth/register", `{"redirect_uris":["http://evil.example/cb"]}`, hdr); w.Code != 400 {
			t.Fatalf("request %d: %d %s", i, w.Code, w.Body)
		}
	}
	if w := f.do(t, "POST", "/platform/oauth/register", validRegisterBody, map[string]string{"X-Forwarded-For": "203.0.113.9"}); w.Code != 201 {
		t.Fatalf("valid registration after junk: %d %s", w.Code, w.Body)
	}
}
