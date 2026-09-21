// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
)

// cookieOpts: the platform-api shape — x-ach-key resolved + the console
// cookie, with an in-memory session map and one known oauth pk_ row.
func cookieOpts(sessions map[string]string) AuthnOptions {
	o := opts(achKey)
	o.Cookie = &CookieAuth{Name: "ach_console", Issuer: "https://ach.test",
		Session: func(_ context.Context, sid string) (string, bool, error) {
			e, ok := sessions[sid]
			return e, ok, nil
		},
		PK: func(_ context.Context, email string) (*db.PkKeyInfo, error) {
			if email == "u@x.com" {
				return &db.PkKeyInfo{KeyID: "pkid_1", OwnerEmail: email, Status: "active"}, nil
			}
			return nil, nil
		}}
	return o
}

func withCookie(req *http.Request) *http.Request {
	req.AddCookie(&http.Cookie{Name: "ach_console", Value: "sid1"})
	return req
}

func TestAuthn_CookieProducesTheSameKeyContext(t *testing.T) {
	var got KeyContext
	inner := func(_ http.ResponseWriter, r *http.Request) { got, _ = KeyContextFromCtx(r.Context()) }
	rec := httptest.NewRecorder()
	RequestID(Authn(&slotResolver{}, map[string]struct{}{"u@x.com": {}}, nil, cookieOpts(map[string]string{"sid1": "u@x.com"}))(http.HandlerFunc(inner))).
		ServeHTTP(rec, withCookie(httptest.NewRequest(http.MethodGet, "/platform/keys", nil)))
	if rec.Code != 200 || got.KeyID != "pkid_1" || got.OwnerEmail != "u@x.com" || got.KeyType != keys.PrefixPk || !got.IsAdmin {
		t.Fatalf("%d %+v", rec.Code, got)
	}
}

func TestAuthn_CookieMutationNeedsSameSite(t *testing.T) {
	o := cookieOpts(map[string]string{"sid1": "u@x.com"})
	for hdr, want := range map[string]int{"": 403, "cross-site": 403, "same-origin": 200} {
		req := withCookie(httptest.NewRequest(http.MethodPost, "/platform/keys", nil))
		if hdr != "" {
			req.Header.Set("Sec-Fetch-Site", hdr)
		}
		if rec := serveAuthn(&slotResolver{}, o, req, nil); rec.Code != want {
			t.Fatalf("Sec-Fetch-Site=%q: %d, want %d", hdr, rec.Code, want)
		}
	}
	// Safe methods never need it.
	if rec := serveAuthn(&slotResolver{}, o, withCookie(httptest.NewRequest(http.MethodGet, "/platform/keys", nil)), nil); rec.Code != 200 {
		t.Fatalf("GET: %d", rec.Code)
	}
}

func TestAuthn_CookieAndCredentialIsAmbiguous(t *testing.T) {
	res := &slotResolver{info: pkInfo()}
	o := cookieOpts(map[string]string{"sid1": "u@x.com"})
	for _, set := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("x-ach-key", "pk_x") },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer sk-raw") },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer aaa.bbb.ccc") },
	} {
		req := withCookie(httptest.NewRequest(http.MethodGet, "/platform/keys", nil))
		set(req)
		rec := serveAuthn(res, o, req, nil)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "ambiguous_credentials") || res.last != "" {
			t.Fatalf("%d %s resolved=%q", rec.Code, rec.Body, res.last)
		}
	}
}

func TestAuthn_CookieUnknownOrExpired(t *testing.T) {
	rec := serveAuthn(&slotResolver{}, cookieOpts(map[string]string{}), withCookie(httptest.NewRequest(http.MethodGet, "/platform/keys", nil)), nil)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "session_expired") || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// A session whose user has no live oauth pk_ row is 401 too.
	rec = serveAuthn(&slotResolver{}, cookieOpts(map[string]string{"sid1": "nobody@x.com"}), withCookie(httptest.NewRequest(http.MethodGet, "/platform/keys", nil)), nil)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "expired_or_revoked") {
		t.Fatalf("no pk row: %d %s", rec.Code, rec.Body)
	}
}

func TestAuthn_CookieIdPUnavailableIs503(t *testing.T) {
	o := cookieOpts(nil)
	o.Cookie.Session = func(context.Context, string) (string, bool, error) { return "", false, ErrTemporarilyUnavailable }
	rec := serveAuthn(&slotResolver{}, o, withCookie(httptest.NewRequest(http.MethodGet, "/platform/keys", nil)), nil)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "temporarily_unavailable") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// Without opts.Cookie (forwarder, content-service) a console cookie is
// just an unread header: nothing presented → 401.
func TestAuthn_CookieIgnoredWhenNotConfigured(t *testing.T) {
	rec := serveAuthn(&slotResolver{}, opts(achKey), withCookie(httptest.NewRequest(http.MethodGet, "/v1/models", nil)), nil)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "missing_key") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
