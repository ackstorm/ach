// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
)

// consoleSessionKind is the OAuthStore kind of a browser session: one more
// record beside pending/code/refresh — no second store (D-07).
const consoleSessionKind = "websession"

// consoleSession is what the opaque cookie points at. RevalidateAt paces
// the IdP check (every AccessTTL, like a token refresh); ExpiresAt bounds
// the session's absolute life to RefreshTTL.
type consoleSession struct {
	oauthUser
	RevalidateAt time.Time `json:"revalidate_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// ConsoleCookieName follows bindingCookieName: __Host- on an https base.
func ConsoleCookieName(insecure bool) string {
	if insecure {
		return "ach_console"
	}
	return "__Host-ach_console"
}

func consoleCookie(value string, insecure bool, maxAge int) *http.Cookie {
	return &http.Cookie{Name: ConsoleCookieName(insecure), Value: value, Path: "/",
		HttpOnly: true, Secure: !insecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

// safeNext keeps the post-login redirect on this origin: a local absolute
// path, never a scheme or a protocol-relative host.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return "/"
	}
	return next
}

// now is the clock: Deps.Now when a test pinned it, the wall clock
// otherwise. ConsoleSession is called as a method value bound OUTSIDE
// MountOAuth/MountConsole (the Authn cookie adapter), so it cannot rely on
// those having defaulted Now on their own copy.
func (d OAuthDeps) now() time.Time {
	if d.Now == nil {
		return time.Now()
	}
	return d.Now()
}

// MountConsole registers the console's session endpoints under
// /platform/console/session. Outside the Authn group: login is anonymous
// and logout must see the raw cookie to delete the record.
func MountConsole(d OAuthDeps) func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/login", d.consoleLogin)
		r.Post("/logout", d.consoleLogout)
	}
}

// consoleLogin parks a Console pending and sends the browser to Dex — the
// same leg /authorize uses, minus the client.
func (d OAuthDeps) consoleLogin(w http.ResponseWriter, r *http.Request) {
	pendingID, err := NewSessionID()
	if err != nil {
		htmlError(w, 500, "")
		return
	}
	binding, err := NewSessionID()
	if err != nil {
		htmlError(w, 500, "")
		return
	}
	dexVerifier := oauth2.GenerateVerifier()
	p := oauthPending{Console: true, RedirectURI: safeNext(r.URL.Query().Get("next")),
		DexVerifier: dexVerifier, Binding: bindingHash(binding)}
	if err := d.Store.Put(r.Context(), "pending", pendingID, p, oauthPendingTTL); err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	http.SetCookie(w, bindingCookie(pendingID, binding, d.Auth.InsecureCookie, int(oauthPendingTTL.Seconds())))
	http.Redirect(w, r, d.dexLogin(pendingID, dexVerifier), http.StatusFound)
}

// consoleFinish is the as-callback tail for a Console pending: make sure
// the user's purpose='oauth' pk_ exists (the row the cookie resolves to —
// the same provisioning a /token issue runs, no console key), mint the
// session, set the cookie, drop the binding cookie, go back to the app.
func (d OAuthDeps) consoleFinish(w http.ResponseWriter, r *http.Request, p oauthPending, pendingID string, u oauthUser) {
	if err := d.ensureOAuthPK(r.Context(), u.Sub, u.UserID); err != nil {
		if errors.Is(err, ErrMintLiteLLM) {
			htmlError(w, 503, "LiteLLM is unreachable; try again shortly")
			return
		}
		d.Auth.Logger.Error("console: ensure pk_ failed", "err", err)
		htmlError(w, 500, "could not provision the personal credential")
		return
	}
	sid, err := NewSessionID()
	if err != nil {
		htmlError(w, 500, "")
		return
	}
	now := d.now()
	s := consoleSession{oauthUser: u, RevalidateAt: now.Add(d.AccessTTL), ExpiresAt: now.Add(d.RefreshTTL)}
	if err := d.Store.Put(r.Context(), consoleSessionKind, sid, s, d.RefreshTTL); err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	http.SetCookie(w, bindingCookie(pendingID, "", d.Auth.InsecureCookie, -1))
	http.SetCookie(w, consoleCookie(sid, d.Auth.InsecureCookie, int(d.RefreshTTL.Seconds())))
	http.Redirect(w, r, p.RedirectURI, http.StatusFound)
}

// consoleLogout deletes the web session only. The shared Dex refresh token
// and the oauth pk_ stay: ach-cli and every other tool keep working (D-22).
func (d OAuthDeps) consoleLogout(w http.ResponseWriter, r *http.Request) {
	if !pamw.SameSite(r, d.Issuer) {
		oauthError(w, 403, "csrf_rejected", "cross-site request refused")
		return
	}
	if c, err := r.Cookie(ConsoleCookieName(d.Auth.InsecureCookie)); err == nil {
		_ = d.Store.Del(r.Context(), consoleSessionKind, c.Value)
	}
	http.SetCookie(w, consoleCookie("", d.Auth.InsecureCookie, -1))
	w.WriteHeader(http.StatusNoContent)
}

// ConsoleSessionName is the IdP display name stored with a console session,
// "" when absent or unknown. Display only: Authn has already validated the
// same cookie through ConsoleSession, so no revalidation happens here.
func (d OAuthDeps) ConsoleSessionName(ctx context.Context, sid string) string {
	var s consoleSession
	if ok, err := d.Store.Get(ctx, consoleSessionKind, sid, &s); err != nil || !ok {
		return ""
	}
	return s.Name
}

// ConsoleSession resolves a cookie value to the signed-in user. Every
// AccessTTL the IdP is asked through the shared Dex refresh token
// (revalidateAtIdP) and the user's oauth pk_ row is kept alive
// (ensureOAuthPK) — exactly what a /token refresh does for ach-cli, so a
// browser-only user is not logged out when the row's 7-day sliding
// window (db.PkSlidingWindow) runs out under a 30-day cookie. A refusal
// ends the session (and, as for any other tool, the user's oauth pk_); an
// unreachable Dex or LiteLLM is middleware.ErrTemporarilyUnavailable and
// the session is kept.
func (d OAuthDeps) ConsoleSession(ctx context.Context, sid string) (string, bool, error) {
	var s consoleSession
	ok, err := d.Store.Get(ctx, consoleSessionKind, sid, &s)
	if err != nil || !ok {
		return "", false, err
	}
	now := d.now()
	if !now.Before(s.ExpiresAt) {
		_ = d.Store.Del(ctx, consoleSessionKind, sid)
		return "", false, nil
	}
	if now.Before(s.RevalidateAt) {
		return s.Sub, true, nil
	}
	if err := d.revalidateAtIdP(ctx, s.Sub); err != nil {
		if errors.Is(err, errIdPRefused) {
			_ = d.Store.Del(ctx, consoleSessionKind, sid)
			return "", false, nil
		}
		if errors.Is(err, ErrIdPUnreachable) {
			return "", false, pamw.ErrTemporarilyUnavailable
		}
		return "", false, err
	}
	if err := d.ensureOAuthPK(ctx, s.Sub, s.UserID); err != nil {
		if errors.Is(err, ErrMintLiteLLM) {
			return "", false, pamw.ErrTemporarilyUnavailable
		}
		return "", false, err
	}
	s.RevalidateAt = now.Add(d.AccessTTL)
	if err := d.Store.Put(ctx, consoleSessionKind, sid, s, s.ExpiresAt.Sub(now)); err != nil {
		return "", false, err
	}
	return s.Sub, true, nil
}
