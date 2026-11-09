// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func dexAnswer(status int, code, desc string) error {
	return &oauth2.RetrieveError{Response: &http.Response{StatusCode: status}, ErrorCode: code, ErrorDescription: desc}
}

// Only Dex's upstream rejection records the owner as refused; Dex refusing
// the token itself (reuse, expiry) or failing does not.
func TestRevalidate_RecordsOnlyTheUpstreamRejection(t *testing.T) {
	cases := []struct {
		name    string
		dexErr  error
		wantErr error
		want    []string
	}{
		{"upstream rejected", dexAnswer(400, "invalid_grant", dexUpstreamRejection), errIdPRefused, []string{"u@x.com=true"}},
		{"token claimed twice", dexAnswer(400, "invalid_grant", "Refresh token is invalid or has already been claimed by another client."), errIdPRefused, nil},
		{"dex token expired", dexAnswer(400, "invalid_grant", "Refresh token expired."), errIdPRefused, nil},
		{"dex server error", dexAnswer(500, "invalid_request", ""), ErrIdPUnreachable, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAS(t)
			installFakePKs(f)
			_ = f.store.Put(context.Background(), dexRefreshKind, "u@x.com", "dex-rt", time.Hour)
			f.dexRefresh = func(string) (string, error) { return "", c.dexErr }
			if _, err := f.deps.revalidateAtIdP(context.Background(), "u@x.com"); !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if !reflect.DeepEqual(f.refusals, c.want) {
				t.Fatalf("refusals = %v, want %v", f.refusals, c.want)
			}
		})
	}
}

// A refusal that could not be recorded keeps the Dex token, so the next
// replay meets the same upstream rejection and records it then.
func TestRevalidate_KeepsTheDexTokenWhenTheRefusalIsNotRecorded(t *testing.T) {
	f := newAS(t)
	installFakePKs(f)
	ctx := context.Background()
	_ = f.store.Put(ctx, dexRefreshKind, "u@x.com", "dex-rt", time.Hour)
	f.dexRefresh = func(string) (string, error) {
		return "", dexAnswer(400, "invalid_grant", dexUpstreamRejection)
	}
	f.deps.IdPRefusal = func(context.Context, string, bool) error { return errors.New("db down") }
	if _, err := f.deps.revalidateAtIdP(ctx, "u@x.com"); !errors.Is(err, errIdPRefused) {
		t.Fatalf("err = %v, want errIdPRefused", err)
	}
	var rt string
	if ok, err := f.store.Get(ctx, dexRefreshKind, "u@x.com", &rt); !ok || err != nil || rt != "dex-rt" {
		t.Fatalf("Dex token = %q (ok=%v err=%v), want it kept", rt, ok, err)
	}
}

// A login the IdP accepted lifts an earlier refusal.
func TestASCallback_LiftsTheIdPRefusal(t *testing.T) {
	f := withFakeDex(newAS(t), "u@x.com")
	installFakePKs(f)
	f.deps.Provision = func(context.Context, string, []string) (string, error) { return "user-1", nil }
	f.mount()
	cid := registerClient(t, f)
	state, cookie := startAuthorize(t, f, cid)
	if w := f.do(t, "GET", "/platform/oauth/as-callback?code=dexcode&state="+state, nil, cookie); w.Code >= 400 {
		t.Fatalf("callback: %d %s", w.Code, w.Body)
	}
	if !reflect.DeepEqual(f.refusals, []string{"u@x.com=false"}) {
		t.Fatalf("refusals = %v, want [u@x.com=false]", f.refusals)
	}
}
