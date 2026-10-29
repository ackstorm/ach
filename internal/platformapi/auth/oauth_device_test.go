// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

type deviceAuthzBody struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Complete        string `json:"verification_uri_complete"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

func startDevice(t *testing.T, f *asFixture, cid string) deviceAuthzBody {
	t.Helper()
	w := f.do(t, "POST", "/platform/oauth/device_authorization", "client_id="+cid, formHdr)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("device_authorization: %d %s", w.Code, w.Body)
	}
	var b deviceAuthzBody
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	return b
}

func deviceTokenForm(cid, deviceCode string) string {
	return url.Values{"grant_type": {deviceGrantType}, "device_code": {deviceCode}, "client_id": {cid}}.Encode()
}

// confirmCode drives the verification page: POST the code, expect the Dex
// redirect, return the pending state + binding cookie like startAuthorize.
func confirmCode(t *testing.T, f *asFixture, userCode string) (state string, cookie map[string]string) {
	t.Helper()
	w := f.do(t, "POST", "/platform/oauth/device", "user_code="+url.QueryEscape(userCode), sameOriginForm)
	if w.Code != 302 {
		t.Fatalf("confirm %q: %d %s", userCode, w.Code, w.Body)
	}
	state = strings.TrimPrefix(w.Header().Get("Location"), "http://dex.test/auth?state=")
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "__Host-ach_oauth_"+state {
		t.Fatalf("binding cookie: %+v", cs)
	}
	return state, map[string]string{"Cookie": cs[0].Name + "=" + cs[0].Value}
}

func TestDeviceGrant_EndToEnd(t *testing.T) {
	f := withFakeDex(newAS(t), "U@X.com")
	pks := installFakePKs(f)
	cid := registerClient(t, f)

	b := startDevice(t, f, cid)
	if b.VerificationURI != "https://ach.test/platform/oauth/device" || b.Complete != b.VerificationURI+"?user_code="+b.UserCode ||
		b.ExpiresIn != 600 || b.Interval != 5 || len(b.UserCode) != 9 || b.UserCode[4] != '-' {
		t.Fatalf("body: %+v", b)
	}

	// GET renders the form with the code prefilled — nothing happens yet.
	w := f.do(t, "GET", "/platform/oauth/device?user_code="+b.UserCode, nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `value="`+b.UserCode+`"`) || !strings.Contains(w.Body.String(), "Confirm") {
		t.Fatalf("page: %d %s", w.Code, w.Body)
	}

	// The CLI polls before the user is done.
	w = f.do(t, "POST", "/platform/oauth/token", deviceTokenForm(cid, b.DeviceCode), formHdr)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "authorization_pending") {
		t.Fatalf("before approval: %d %s", w.Code, w.Body)
	}

	// Lower-case, no dash: same code.
	state, cookie := confirmCode(t, f, strings.ToLower(strings.ReplaceAll(b.UserCode, "-", "")))
	w = f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Return to your terminal") {
		t.Fatalf("callback: %d %s", w.Code, w.Body)
	}
	if cs := w.Result().Cookies(); len(cs) != 1 || cs[0].MaxAge >= 0 {
		t.Fatalf("binding cookie not cleared: %+v", cs)
	}
	// user_code index is gone: a second confirm of the same code fails.
	if w := f.do(t, "POST", "/platform/oauth/device", "user_code="+b.UserCode, sameOriginForm); w.Code != 200 || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("code reuse: %d %s", w.Code, w.Body)
	}

	w = f.do(t, "POST", "/platform/oauth/token", deviceTokenForm(cid, b.DeviceCode), formHdr)
	if w.Code != 200 {
		t.Fatalf("token: %d %s", w.Code, w.Body)
	}
	var tb tokenBody
	_ = json.Unmarshal(w.Body.Bytes(), &tb)
	if sub, err := f.deps.Signer.Verify(tb.AccessToken, "https://ach.test", "ach"); err != nil || sub != "u@x.com" || tb.RefreshToken == "" {
		t.Fatalf("sub=%q err=%v body=%+v", sub, err, tb)
	}
	if pks.minted != 1 {
		t.Fatalf("oauth pk_ row: minted=%d", pks.minted)
	}
	// single redemption
	if w := f.do(t, "POST", "/platform/oauth/token", deviceTokenForm(cid, b.DeviceCode), formHdr); w.Code != 400 || !strings.Contains(w.Body.String(), "expired_token") {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
}

// sameOriginForm is what a browser sends when the verification page's own
// form is submitted: a form POST always carries Origin.
var sameOriginForm = map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://ach.test"}

// TestDevicePage_RefusesACrossSiteConfirmation is the RFC 8628 §5.4 remote
// phishing regression: an attacker's page auto-submits the user_code of
// THEIR device to /oauth/device; the victim's browser must not be walked
// into Dex — no cookie, no redirect, the code stays pending.
func TestDevicePage_RefusesACrossSiteConfirmation(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	d := startDevice(t, f, registerClient(t, f))
	body := "user_code=" + url.QueryEscape(d.UserCode)
	for name, hdr := range map[string]map[string]string{
		"no origin":       formHdr,
		"foreign origin":  {"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://evil.test"},
		"foreign referer": {"Content-Type": "application/x-www-form-urlencoded", "Referer": "https://evil.test/x"},
	} {
		w := f.do(t, "POST", "/platform/oauth/device", body, hdr)
		if w.Code != 403 || w.Header().Get("Location") != "" || len(w.Result().Cookies()) != 0 {
			t.Fatalf("%s: %d loc=%q cookies=%d", name, w.Code, w.Header().Get("Location"), len(w.Result().Cookies()))
		}
	}
	var deviceCode string
	if ok, _ := f.store.Get(context.Background(), "device_user", d.UserCode, &deviceCode); !ok {
		t.Fatal("user_code index must survive a refused confirmation")
	}
	// Referer alone (no Origin) from our own page is accepted.
	if w := f.do(t, "POST", "/platform/oauth/device", body, map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Referer": "https://ach.test/platform/oauth/device"}); w.Code != 302 {
		t.Fatalf("same-origin referer: %d %s", w.Code, w.Body)
	}
}

func TestDeviceGrant_Rejects(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	a, b := registerClient(t, f), registerClient(t, f)

	if w := f.do(t, "POST", "/platform/oauth/device_authorization", "client_id=nope", formHdr); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_client") {
		t.Fatalf("unknown client: %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "POST", "/platform/oauth/device", "user_code=ZZZZ-ZZZZ", sameOriginForm); w.Code != 200 || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("unknown code: %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "POST", "/platform/oauth/token", deviceTokenForm(a, "never-issued"), formHdr); w.Code != 400 || !strings.Contains(w.Body.String(), "expired_token") {
		t.Fatalf("unknown device_code: %d %s", w.Code, w.Body)
	}

	// approved for a, redeemed by b
	d := startDevice(t, f, a)
	state, cookie := confirmCode(t, f, d.UserCode)
	f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie)
	if w := f.do(t, "POST", "/platform/oauth/token", deviceTokenForm(b, d.DeviceCode), formHdr); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_grant") {
		t.Fatalf("other client: %d %s", w.Code, w.Body)
	}

	// denied at Dex
	d = startDevice(t, f, a)
	state, cookie = confirmCode(t, f, d.UserCode)
	if w := f.do(t, "GET", "/platform/oauth/as-callback?error=access_denied&state="+state, nil, cookie); w.Code != 200 || !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatalf("denied callback: %d %s", w.Code, w.Body)
	}
	if w := f.do(t, "POST", "/platform/oauth/token", deviceTokenForm(a, d.DeviceCode), formHdr); w.Code != 400 || !strings.Contains(w.Body.String(), "access_denied") {
		t.Fatalf("denied token: %d %s", w.Code, w.Body)
	}
	var rec oauthDevice
	if ok, _ := f.store.Get(context.Background(), "device", d.DeviceCode, &rec); ok {
		t.Fatal("denied record must be gone after the client saw access_denied")
	}

	// the callback binding still applies to a device pending
	d = startDevice(t, f, a)
	state, _ = confirmCode(t, f, d.UserCode)
	if w := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, nil); w.Code != 400 {
		t.Fatalf("no binding cookie: %d", w.Code)
	}
}

func TestNormalizeUserCode(t *testing.T) {
	for in, want := range map[string]string{
		"BCDF-GHJK": "BCDF-GHJK", "bcdfghjk": "BCDF-GHJK", " bcdf ghjk ": "BCDF-GHJK", "BCDF": "", "": "",
	} {
		if got := normalizeUserCode(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
