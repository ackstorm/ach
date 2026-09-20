// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// RFC 8628 Device Authorization Grant — the headless login (an SSH host
// with no browser): the CLI shows a code, the user enters it in a browser
// anywhere, Dex logs them in through the same as-callback leg the
// authorization-code flow uses, and the CLI polls /token for the same JWT +
// refresh pair every other grant issues.
const (
	deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	deviceTTL       = 10 * time.Minute
	deviceInterval  = 5 * time.Second

	deviceStatusPending  = "pending"
	deviceStatusApproved = "approved"
	deviceStatusDenied   = "denied"
)

// userCodeAlphabet: 20 symbols with no look-alikes (no vowels, no 0/O, 1/I).
// 8 symbols ≈ 34.6 bits inside a 10-minute window.
const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

// oauthDevice is one device authorization, keyed by device_code. The
// user_code → device_code index lives under "device_user".
type oauthDevice struct {
	ClientID string `json:"client_id"`
	UserCode string `json:"user_code"`
	Status   string `json:"status"`
	Sub      string `json:"sub,omitempty"`
	UserID   string `json:"user_id,omitempty"`
}

func newUserCode() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	out := make([]byte, 0, 9)
	for i, x := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, userCodeAlphabet[int(x)%len(userCodeAlphabet)])
	}
	return string(out), nil
}

// normalizeUserCode: what the user typed → the stored form (upper-case,
// one dash). Lets "bcdfghjk" and "BCDF GHJK" both match.
func normalizeUserCode(raw string) string {
	s := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(raw)))
	if len(s) != 8 {
		return ""
	}
	return s[:4] + "-" + s[4:]
}

func (d OAuthDeps) deviceAuthorization(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", "form body required")
		return
	}
	var client oauthClient
	ok, err := d.Store.Get(r.Context(), "client", r.PostForm.Get("client_id"), &client)
	if err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	if !ok {
		oauthError(w, 400, "invalid_client", "unknown client_id")
		return
	}
	deviceCode, err := NewSessionID()
	if err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	userCode, err := newUserCode()
	if err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	rec := oauthDevice{ClientID: client.ClientID, UserCode: userCode, Status: deviceStatusPending}
	if err := d.Store.Put(r.Context(), "device", deviceCode, rec, deviceTTL); err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	if err := d.Store.Put(r.Context(), "device_user", userCode, deviceCode, deviceTTL); err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	uri := strings.TrimRight(d.Issuer, "/") + "/platform/oauth/device"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"device_code":               deviceCode,
		"user_code":                 userCode,
		"verification_uri":          uri,
		"verification_uri_complete": uri + "?user_code=" + userCode,
		"expires_in":                int(deviceTTL / time.Second),
		"interval":                  int(deviceInterval / time.Second),
	})
}

// devicePage is the verification page. GET renders the form with the code
// prefilled from ?user_code (verification_uri_complete); the user confirms
// it matches the terminal before anything happens — a forwarded link never
// approves on its own (RFC 8628 §5.4). POST looks the code up and parks a
// pending authorization exactly as /authorize does, then sends the browser
// to Dex.
func (d OAuthDeps) devicePage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderDevicePage(w, r.URL.Query().Get("user_code"), "")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderDevicePage(w, "", "form body required")
		return
	}
	userCode := normalizeUserCode(r.PostForm.Get("user_code"))
	var deviceCode string
	ok := false
	if userCode != "" {
		var err error
		if ok, err = d.Store.Get(r.Context(), "device_user", userCode, &deviceCode); err != nil {
			htmlError(w, 500, "store unavailable")
			return
		}
	}
	if !ok {
		renderDevicePage(w, r.PostForm.Get("user_code"), "code not found or expired — check your terminal")
		return
	}
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
	p := oauthPending{DeviceCode: deviceCode, DexVerifier: dexVerifier, Binding: bindingHash(binding)}
	if err := d.Store.Put(r.Context(), "pending", pendingID, p, oauthPendingTTL); err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	http.SetCookie(w, bindingCookie(pendingID, binding, d.Auth.InsecureCookie, int(oauthPendingTTL.Seconds())))
	http.Redirect(w, r, d.dexLogin(pendingID, dexVerifier), http.StatusFound)
}

// deviceFinish is the as-callback tail for a device pending: the device
// record flips to approved (or denied), the user_code index dies, and the
// browser is told to go back to the terminal — no client redirect exists.
func (d OAuthDeps) deviceFinish(w http.ResponseWriter, r *http.Request, p oauthPending, pendingID string, status, email, userID string) {
	http.SetCookie(w, bindingCookie(pendingID, "", d.Auth.InsecureCookie, -1))
	var rec oauthDevice
	ok, err := d.Store.Get(r.Context(), "device", p.DeviceCode, &rec)
	if err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	if !ok || rec.Status != deviceStatusPending {
		htmlError(w, 400, "this device code has expired — start again from your terminal")
		return
	}
	rec.Status, rec.Sub, rec.UserID = status, email, userID
	// Remaining TTL: the CLI stops polling at expires_in whatever happens here.
	if err := d.Store.Put(r.Context(), "device", p.DeviceCode, rec, deviceTTL); err != nil {
		htmlError(w, 500, "store unavailable")
		return
	}
	_ = d.Store.Del(r.Context(), "device_user", rec.UserCode)
	d.Auth.Logger.Info("oauth: device authorization "+status, "client_id", rec.ClientID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status == deviceStatusApproved {
		_, _ = fmt.Fprint(w, "<h1>Signed in to ACH</h1><p>Return to your terminal — you can close this tab.</p>")
		return
	}
	_, _ = fmt.Fprint(w, "<h1>Sign-in cancelled</h1><p>Nothing was granted. You can close this tab.</p>")
}

// deviceToken is the /token branch for grant_type=…:device_code.
func (d OAuthDeps) deviceToken(w http.ResponseWriter, r *http.Request, clientID string) {
	deviceCode := r.PostForm.Get("device_code")
	var rec oauthDevice
	ok, err := d.Store.Get(r.Context(), "device", deviceCode, &rec)
	if err != nil {
		oauthError(w, 500, "server_error", "")
		return
	}
	if !ok {
		oauthError(w, 400, "expired_token", "")
		return
	}
	if rec.ClientID != clientID {
		oauthError(w, 400, "invalid_grant", "")
		return
	}
	switch rec.Status {
	case deviceStatusPending:
		oauthError(w, 400, "authorization_pending", "")
		return
	case deviceStatusDenied:
		_ = d.Store.Del(r.Context(), "device", deviceCode)
		oauthError(w, 400, "access_denied", "")
		return
	}
	// approved: single redemption
	if ok, err := d.Store.Take(r.Context(), "device", deviceCode, &rec); err != nil || !ok {
		oauthError(w, 400, "expired_token", "")
		return
	}
	d.issue(w, r, rec.Sub, rec.UserID, clientID)
}

func renderDevicePage(w http.ResponseWriter, code, problem string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, `<!doctype html><title>Sign in to ACH</title>
<h1>Sign in to ACH</h1>
<p>Confirm this is the code shown in your terminal, then continue.</p>
<form method="post">
<input name="user_code" value="%s" autocomplete="off" autocapitalize="characters" spellcheck="false" size="12" required>
<button type="submit">Confirm</button>
</form>
<p>%s</p>
`, html.EscapeString(code), html.EscapeString(problem))
}
