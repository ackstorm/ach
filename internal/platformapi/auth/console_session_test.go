// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
)

// mountConsole adds the console session routes beside the AS and pins the
// clock so tests can walk past AccessTTL.
func (f *asFixture) mountConsole() {
	if f.deps.Now == nil {
		f.now = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
		f.deps.Now = func() time.Time { return f.now }
	}
	f.mount()
	f.r.Route("/platform/console/session", MountConsole(f.deps))
}

// consoleLogin walks GET /login → Dex (fake) → /as-callback and returns
// the console cookie the callback set.
func consoleLogin(t *testing.T, f *asFixture, next string) *http.Cookie {
	t.Helper()
	w := f.do(t, "GET", "/platform/console/session/login?next="+next, nil, nil)
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=") {
		t.Fatalf("login: %d %s %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	binding := w.Result().Cookies()[0]
	cb := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil,
		map[string]string{"Cookie": binding.Name + "=" + binding.Value})
	if cb.Code != 302 {
		t.Fatalf("callback: %d %s", cb.Code, cb.Body)
	}
	if loc := cb.Header().Get("Location"); loc != next {
		t.Fatalf("callback redirect %q, want %q", loc, next)
	}
	var console *http.Cookie
	for _, c := range cb.Result().Cookies() {
		switch c.Name {
		case ConsoleCookieName(false):
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
				t.Fatalf("console cookie attrs: %+v", c)
			}
			console = c
		case binding.Name:
			if c.MaxAge >= 0 {
				t.Fatalf("binding cookie not cleared: %+v", c)
			}
		}
	}
	if console == nil {
		t.Fatal("no console cookie")
	}
	return console
}

func TestConsole_LoginSetsSessionAndRevalidatesLater(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	pks := installFakePKs(f)
	f.mountConsole()
	c := consoleLogin(t, f, "/keys")

	email, ok, err := f.deps.ConsoleSession(context.Background(), c.Value)
	if err != nil || !ok || email != "u@x.com" {
		t.Fatalf("session: %q %v %v", email, ok, err)
	}
	if name := f.deps.ConsoleSessionName(context.Background(), c.Value); name != "Test User" {
		t.Fatalf("session must keep the IdP display name: %q", name)
	}
	if name := f.deps.ConsoleSessionName(context.Background(), "no-such-session"); name != "" {
		t.Fatalf("unknown session name = %q, want empty", name)
	}
	var rt string
	if ok, _ := f.store.Get(context.Background(), dexRefreshKind, "u@x.com", &rt); !ok || rt != "dex-rt-0" {
		t.Fatalf("login must store the shared Dex refresh token (D-22): %q", rt)
	}
	if len(f.dexSeen) != 0 {
		t.Fatal("no IdP round trip before AccessTTL")
	}
	// Entering the console provisions the user's oauth pk_ — the row the
	// cookie resolves to — exactly like a /token issue; a second login
	// reuses it (no console key, AC-02).
	if pks.minted != 1 || pks.rows["u@x.com"] == nil {
		t.Fatalf("oauth pk_ after console login: minted=%d row=%v", pks.minted, pks.rows["u@x.com"])
	}
	_ = consoleLogin(t, f, "/")
	if pks.minted != 1 {
		t.Fatalf("second login re-minted: %d", pks.minted)
	}

	// Past AccessTTL the IdP is asked; the rotated token is stored.
	f.now = f.now.Add(f.deps.AccessTTL + time.Second)
	f.dexRefresh = func(string) (string, error) { return "dex-rt-rotated", nil }
	if _, ok, err := f.deps.ConsoleSession(context.Background(), c.Value); err != nil || !ok {
		t.Fatalf("revalidate: %v %v", ok, err)
	}
	if ok, _ := f.store.Get(context.Background(), dexRefreshKind, "u@x.com", &rt); !ok || rt != "dex-rt-rotated" {
		t.Fatalf("dexrt after revalidation: %q", rt)
	}
	// …and not again until the next AccessTTL.
	if _, ok, _ := f.deps.ConsoleSession(context.Background(), c.Value); !ok || len(f.dexSeen) != 1 {
		t.Fatalf("second lookup within AccessTTL must not hit the IdP: seen=%v", f.dexSeen)
	}
}

func TestConsole_IdPRefusalEndsTheSessionAndRevokesTheOAuthPK(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	pks := installFakePKs(f)
	f.mountConsole()
	c := consoleLogin(t, f, "/")
	minted := pks.rows["u@x.com"].KeyID
	f.now = f.now.Add(f.deps.AccessTTL + time.Second)
	f.dexRefresh = func(string) (string, error) {
		return "", &oauth2.RetrieveError{Response: &http.Response{StatusCode: 400}, ErrorCode: "invalid_grant"}
	}
	if _, ok, err := f.deps.ConsoleSession(context.Background(), c.Value); err != nil || ok {
		t.Fatalf("refused: ok=%v err=%v", ok, err)
	}
	if len(pks.revoked) != 1 || pks.revoked[0] != minted {
		t.Fatalf("oauth pk_ must be revoked: %v", pks.revoked)
	}
	if ok, _ := f.store.Get(context.Background(), consoleSessionKind, c.Value, new(consoleSession)); ok {
		t.Fatal("session must be gone")
	}
	if ok, _ := f.store.Get(context.Background(), dexRefreshKind, "u@x.com", new(string)); ok {
		t.Fatal("dex refresh token must be gone")
	}
}

func TestConsole_DexUnreachableIsTemporary(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.mountConsole()
	c := consoleLogin(t, f, "/")
	f.now = f.now.Add(f.deps.AccessTTL + time.Second)
	f.dexRefresh = func(string) (string, error) {
		return "", &oauth2.RetrieveError{Response: &http.Response{StatusCode: 503}}
	}
	if _, _, err := f.deps.ConsoleSession(context.Background(), c.Value); !errors.Is(err, pamw.ErrTemporarilyUnavailable) {
		t.Fatalf("want ErrTemporarilyUnavailable, got %v", err)
	}
	if ok, _ := f.store.Get(context.Background(), consoleSessionKind, c.Value, new(consoleSession)); !ok {
		t.Fatal("session must survive an unreachable IdP")
	}
}

func TestConsole_SessionExpiresAtRefreshTTL(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.mountConsole()
	c := consoleLogin(t, f, "/")
	f.now = f.now.Add(f.deps.RefreshTTL)
	if _, ok, err := f.deps.ConsoleSession(context.Background(), c.Value); err != nil || ok {
		t.Fatalf("past RefreshTTL: ok=%v err=%v", ok, err)
	}
	if len(f.dexSeen) != 0 {
		t.Fatal("an expired session is not revalidated")
	}
}

func TestConsole_LogoutDeletesOnlyTheWebSession(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.mountConsole()
	c := consoleLogin(t, f, "/")
	hdr := map[string]string{"Cookie": c.Name + "=" + c.Value, "Sec-Fetch-Site": "same-origin"}
	w := f.do(t, "POST", "/platform/console/session/logout", nil, hdr)
	if w.Code != 204 {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	if cs := w.Result().Cookies(); len(cs) != 1 || cs[0].Name != c.Name || cs[0].MaxAge >= 0 {
		t.Fatalf("logout must clear the cookie: %+v", cs)
	}
	if _, ok, _ := f.deps.ConsoleSession(context.Background(), c.Value); ok {
		t.Fatal("session survived logout")
	}
	if ok, _ := f.store.Get(context.Background(), dexRefreshKind, "u@x.com", new(string)); !ok {
		t.Fatal("logout must NOT delete the shared Dex refresh token (D-22)")
	}
	// Cross-site logout is refused (D-28); no cookie is an idempotent 204.
	c2 := consoleLogin(t, f, "/")
	if w := f.do(t, "POST", "/platform/console/session/logout", nil, map[string]string{"Cookie": c2.Name + "=" + c2.Value, "Sec-Fetch-Site": "cross-site"}); w.Code != 403 {
		t.Fatalf("cross-site logout: %d", w.Code)
	}
	if _, ok, _ := f.deps.ConsoleSession(context.Background(), c2.Value); !ok {
		t.Fatal("a refused logout must not touch the session")
	}
	if w := f.do(t, "POST", "/platform/console/session/logout", nil, map[string]string{"Sec-Fetch-Site": "same-origin"}); w.Code != 204 {
		t.Fatalf("logout without a cookie: %d", w.Code)
	}
}

func TestConsole_IdPDenialAtLoginIs401(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.mountConsole()
	w := f.do(t, "GET", "/platform/console/session/login?next=/", nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	binding := w.Result().Cookies()[0]
	cb := f.do(t, "GET", "/platform/oauth/as-callback?error=access_denied&state="+state, nil,
		map[string]string{"Cookie": binding.Name + "=" + binding.Value})
	if cb.Code != 401 || len(cb.Result().Cookies()) != 0 {
		t.Fatalf("denied: %d cookies=%d", cb.Code, len(cb.Result().Cookies()))
	}
}

func TestConsole_NextIsLocalOnly(t *testing.T) {
	for in, want := range map[string]string{"/keys": "/keys", "//evil": "/", "https://e/": "/", "": "/", "/a\\b": "/"} {
		if got := safeNext(in); got != want {
			t.Fatalf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
	// End to end: a foreign next lands on "/".
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.mountConsole()
	w := f.do(t, "GET", "/platform/console/session/login?next=https://evil.test/", nil, nil)
	state := strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	binding := w.Result().Cookies()[0]
	cb := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil,
		map[string]string{"Cookie": binding.Name + "=" + binding.Value})
	if cb.Code != 302 || cb.Header().Get("Location") != "/" {
		t.Fatalf("foreign next: %d %q", cb.Code, cb.Header().Get("Location"))
	}
}

// The /token refresh grant keeps its contract through the shared
// revalidateAtIdP: the console and the CLI rotate the same Dex token.
func TestConsole_SharesTheDexChainWithTokenRefresh(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.mountConsole()
	c := consoleLogin(t, f, "/")
	// A CLI refresh rotates the shared token…
	if err := f.deps.revalidateAtIdP(context.Background(), "u@x.com"); err != nil {
		t.Fatal(err)
	}
	var rt string
	_, _ = f.store.Get(context.Background(), dexRefreshKind, "u@x.com", &rt)
	if rt != "dex-rt-0+" {
		t.Fatalf("rotated: %q", rt)
	}
	// …and the console's next revalidation replays the rotated one.
	f.now = f.now.Add(f.deps.AccessTTL + time.Second)
	if _, ok, err := f.deps.ConsoleSession(context.Background(), c.Value); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if last := f.dexSeen[len(f.dexSeen)-1]; last != "dex-rt-0+" {
		t.Fatalf("console replayed %q, want the rotated token", last)
	}
}

// The production wiring binds ConsoleSession as a method value on a deps
// copy that never went through MountOAuth/MountConsole (the Authn cookie
// adapter), so a nil Now must mean the wall clock — not a panic.
func TestConsole_SessionLookupWithoutAPinnedClock(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.mountConsole()
	c := consoleLogin(t, f, "/")
	unpinned := f.deps
	unpinned.Now = nil
	if _, ok, err := unpinned.ConsoleSession(context.Background(), c.Value); err != nil || !ok {
		t.Fatalf("nil Now: ok=%v err=%v", ok, err)
	}
}

// A browser-only user must not be logged out when the oauth pk_ row's
// 7-day sliding window runs out under the 30-day cookie: the console's
// periodic revalidation keeps the row alive the way a /token refresh does.
func TestConsole_RevalidationKeepsTheOAuthPKAlive(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	pks := installFakePKs(f)
	f.mountConsole()
	c := consoleLogin(t, f, "/")
	if pks.minted != 1 {
		t.Fatalf("minted=%d", pks.minted)
	}
	// The row is about to expire (inside oauthPKMinRemaining) when the next
	// revalidation runs → replaced, exactly like /token would.
	pks.rows["u@x.com"].ExpiresAt = f.now.Add(f.deps.AccessTTL + time.Minute)
	f.now = f.now.Add(f.deps.AccessTTL + time.Second)
	if _, ok, err := f.deps.ConsoleSession(context.Background(), c.Value); err != nil || !ok {
		t.Fatalf("revalidate: %v %v", ok, err)
	}
	if pks.minted != 2 || len(pks.revoked) != 1 {
		t.Fatalf("near-expiry row must be re-minted: minted=%d revoked=%v", pks.minted, pks.revoked)
	}
	// LiteLLM down during that check (the healthy row's "does LiteLLM still
	// list it" probe fails): temporary, session kept.
	pks.rows["u@x.com"].ExpiresAt = f.now.Add(48 * time.Hour)
	pks.litellmErr = errors.New("litellm down")
	f.now = f.now.Add(f.deps.AccessTTL + time.Second)
	if _, _, err := f.deps.ConsoleSession(context.Background(), c.Value); !errors.Is(err, pamw.ErrTemporarilyUnavailable) {
		t.Fatalf("litellm down: %v", err)
	}
	if ok, _ := f.store.Get(context.Background(), consoleSessionKind, c.Value, new(consoleSession)); !ok {
		t.Fatal("session must survive a LiteLLM outage")
	}
}
