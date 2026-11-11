// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"testing"
)

func TestSSOAdmin(t *testing.T) {
	const email = "u@x.com"
	ctx := context.Background()
	newF := func(t *testing.T, loginGroups []string) *asFixture {
		f := newAS(t)
		f.deps.SSOAdminGroups = []string{"platform-admins"}
		withFakeDex(f, email)
		f.deps.DexExchange = func(context.Context, string, string) (string, string, []string, string, error) {
			return email, "Test User", loginGroups, "dex-rt-0", nil
		}
		f.mount()
		return f
	}
	login := func(t *testing.T, f *asFixture) {
		t.Helper()
		state, cookie := startAuthorize(t, f, registerClient(t, f))
		if w := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie); w.Code != 302 {
			t.Fatalf("callback: %d %s", w.Code, w.Body)
		}
	}
	isAdmin := func(t *testing.T, f *asFixture) bool {
		t.Helper()
		ok, err := f.deps.SSOAdmin(ctx, email)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	t.Run("login in the group grants admin; a refresh without it takes it away", func(t *testing.T) {
		f := newF(t, []string{"Platform-Admins"})
		login(t, f)
		if !isAdmin(t, f) {
			t.Fatal("group member is not admin after login")
		}
		f.dexGroups = []string{"platform-admins"}
		if _, err := f.deps.revalidateAtIdP(ctx, email); err != nil || !isAdmin(t, f) {
			t.Fatalf("still in the group: err=%v admin=%v", err, isAdmin(t, f))
		}
		f.dexGroups = []string{"devs"}
		if _, err := f.deps.revalidateAtIdP(ctx, email); err != nil || isAdmin(t, f) {
			t.Fatalf("removed from the group: err=%v admin=%v", err, isAdmin(t, f))
		}
	})
	t.Run("groups Dex could not deliver drop admin until the next refresh", func(t *testing.T) {
		f := newF(t, []string{"platform-admins"})
		login(t, f)
		f.dexGroups = nil
		if _, err := f.deps.revalidateAtIdP(ctx, email); err != nil || isAdmin(t, f) {
			t.Fatalf("err=%v admin=%v", err, isAdmin(t, f))
		}
	})
	t.Run("login outside the group is not admin", func(t *testing.T) {
		f := newF(t, []string{"devs"})
		login(t, f)
		if isAdmin(t, f) {
			t.Fatal("non-member is admin")
		}
	})
}
