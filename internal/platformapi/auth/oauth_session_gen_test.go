// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// loginTokens walks the real flow (/authorize → Dex → /as-callback → /token),
// so the records carry whatever a login stamps on them.
func loginTokens(t *testing.T, f *asFixture, cid string) tokenBody {
	t.Helper()
	state, cookie := startAuthorize(t, f, cid)
	w := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie)
	if w.Code != 302 {
		t.Fatalf("callback: %d %s", w.Code, w.Body)
	}
	code := mustQuery(t, w.Header().Get("Location"), "code")
	w = f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, map[string]string{"code": code}), formHdr)
	if w.Code != 200 {
		t.Fatalf("token: %d %s", w.Code, w.Body)
	}
	var tok tokenBody
	if err := json.Unmarshal(w.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	return tok
}

func refreshForm(cid, token string) string {
	return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {cid}}.Encode()
}

func TestEndSessions_ReloginDoesNotReviveAnOldRefreshToken(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	cid := registerClient(t, f)
	old := loginTokens(t, f, cid)

	if err := f.deps.EndSessions(context.Background(), "u@x.com"); err != nil {
		t.Fatal(err)
	}
	fresh := loginTokens(t, f, cid)

	seen := len(f.dexSeen)
	w := f.do(t, "POST", "/platform/oauth/token", refreshForm(cid, old.RefreshToken), formHdr)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_grant") {
		t.Fatalf("old refresh after re-login: %d %s", w.Code, w.Body)
	}
	if len(f.dexSeen) != seen {
		t.Fatalf("an ended session must not reach Dex: %v", f.dexSeen)
	}
	if ok, _ := f.store.Get(context.Background(), "refresh", old.RefreshToken, new(oauthRefresh)); ok {
		t.Fatal("the refused record must be deleted")
	}
	if w := f.do(t, "POST", "/platform/oauth/token", refreshForm(cid, fresh.RefreshToken), formHdr); w.Code != 200 {
		t.Fatalf("token issued after the login: %d %s", w.Code, w.Body)
	}
}

func TestEndSessions_ReloginDoesNotReviveAnOldConsoleSession(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.mountConsole()
	old := consoleLogin(t, f, "/keys")

	if err := f.deps.EndSessions(context.Background(), "u@x.com"); err != nil {
		t.Fatal(err)
	}
	fresh := consoleLogin(t, f, "/keys")

	// Refused on the very next use, not at the next revalidation.
	if _, ok, err := f.deps.ConsoleSession(context.Background(), old.Value); err != nil || ok {
		t.Fatalf("old console session after re-login: ok=%v err=%v", ok, err)
	}
	if ok, _ := f.store.Get(context.Background(), consoleSessionKind, old.Value, new(consoleSession)); ok {
		t.Fatal("the refused web session must be deleted")
	}
	if email, ok, err := f.deps.ConsoleSession(context.Background(), fresh.Value); err != nil || !ok || email != "u@x.com" {
		t.Fatalf("session opened after the login: %q %v %v", email, ok, err)
	}
	f.now = f.now.Add(f.deps.AccessTTL + time.Minute)
	if _, ok, err := f.deps.ConsoleSession(context.Background(), fresh.Value); err != nil || !ok {
		t.Fatalf("revalidating the session opened after the login: ok=%v err=%v", ok, err)
	}
}

func TestLogin_LeavesTheUsersOtherSessionsAlone(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	cid := registerClient(t, f)
	first := loginTokens(t, f, cid)
	loginTokens(t, f, cid)
	if w := f.do(t, "POST", "/platform/oauth/token", refreshForm(cid, first.RefreshToken), formHdr); w.Code != 200 {
		t.Fatalf("first session after a second login: %d %s", w.Code, w.Body)
	}
}

// A code minted before EndSessions is redeemed after it: no new session.
func TestEndSessions_RefusesACodeFromBefore(t *testing.T) {
	f := newAS(t)
	installFakePKs(f)
	cid := registerClient(t, f)
	seedCode(t, f, cid)
	if err := f.deps.EndSessions(context.Background(), "u@x.com"); err != nil {
		t.Fatal(err)
	}
	w := f.do(t, "POST", "/platform/oauth/token", tokenForm(cid, nil), formHdr)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_grant") {
		t.Fatalf("code from before EndSessions: %d %s", w.Code, w.Body)
	}
}

// Records stored before the generation existed have no such field: they read
// as generation 0 and stay valid until the user's next EndSessions.
func TestSessionGen_RecordsWithoutTheFieldStayValidUntilEndSessions(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.mountConsole()
	cid := registerClient(t, f)
	ctx := context.Background()

	rawRefresh := json.RawMessage(`{"sub":"u@x.com","user_id":"litellm-user-1","client_id":"` + cid + `"}`)
	expires := f.now.Add(time.Hour).Format(time.RFC3339)
	rawWeb := json.RawMessage(`{"sub":"u@x.com","user_id":"litellm-user-1","revalidate_at":"` + expires + `","expires_at":"` + expires + `"}`)
	put := func(kind, id string, v any) {
		t.Helper()
		if err := f.store.Put(ctx, kind, id, v, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	put("refresh", "old-a", rawRefresh)
	put("refresh", "old-b", rawRefresh)
	put(consoleSessionKind, "old-web", rawWeb)
	put(dexRefreshKind, "u@x.com", "dex-rt-0")

	if w := f.do(t, "POST", "/platform/oauth/token", refreshForm(cid, "old-a"), formHdr); w.Code != 200 {
		t.Fatalf("record without the field: %d %s", w.Code, w.Body)
	}
	if _, ok, err := f.deps.ConsoleSession(ctx, "old-web"); err != nil || !ok {
		t.Fatalf("web session without the field: ok=%v err=%v", ok, err)
	}

	if err := f.deps.EndSessions(ctx, "u@x.com"); err != nil {
		t.Fatal(err)
	}
	loginTokens(t, f, cid)
	if w := f.do(t, "POST", "/platform/oauth/token", refreshForm(cid, "old-b"), formHdr); w.Code != 400 {
		t.Fatalf("record without the field after EndSessions: %d %s", w.Code, w.Body)
	}
	if _, ok, err := f.deps.ConsoleSession(ctx, "old-web"); err != nil || ok {
		t.Fatalf("web session without the field after EndSessions: ok=%v err=%v", ok, err)
	}
}

// The counter must outlive every record carrying it: if it expired while a
// session is in use it would restart at 0 and hand out numbers again, so a
// later EndSessions could land on a generation a live record already holds.
func TestSessionGen_UseKeepsTheCounterAlive(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	cid := registerClient(t, f)
	ctx := context.Background()
	if err := f.deps.EndSessions(ctx, "u@x.com"); err != nil {
		t.Fatal(err)
	}
	tok := loginTokens(t, f, cid)

	key := oauthKey("session_gen", "u@x.com")
	if err := f.store.RDB.Expire(ctx, key, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if w := f.do(t, "POST", "/platform/oauth/token", refreshForm(cid, tok.RefreshToken), formHdr); w.Code != 200 {
		t.Fatalf("refresh: %d %s", w.Code, w.Body)
	}
	if ttl := f.store.RDB.TTL(ctx, key).Val(); ttl < f.deps.RefreshTTL {
		t.Fatalf("counter TTL after a rotation: %v, want at least %v", ttl, f.deps.RefreshTTL)
	}
}
