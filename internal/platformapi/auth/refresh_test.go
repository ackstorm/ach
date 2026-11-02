// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"golang.org/x/oauth2"

	"github.com/ackstorm/ach/internal/keys"
	"github.com/ackstorm/ach/internal/keystore"
	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
)

func TestRefresh(t *testing.T) {
	const email = "pepe@x.com"
	setup := func(t *testing.T) (*asFixture, *fakeLiteLLM) {
		f := newAS(t)
		installFakePKs(f)
		flm := teamsFake("default", "ops")
		f.deps.Auth.LiteLLM = flm
		f.deps.Auth.Teams = TeamPolicy{Default: []string{"default"}, BySSOGroup: map[string][]string{"ops@x.com": {"ops"}}}
		if err := f.store.Put(context.Background(), dexRefreshKind, email, "dex-rt-0", f.deps.RefreshTTL); err != nil {
			t.Fatal(err)
		}
		return f, flm
	}
	call := func(f *asFixture, kt keys.BearerPrefix) *httptest.ResponseRecorder {
		uid := "u-1"
		ctx := pamw.WithKeyContext(context.Background(), &keystore.KeyInfo{KeyType: kt, OwnerEmail: email, LiteLLMUserID: &uid}, false)
		w := httptest.NewRecorder()
		f.deps.Refresh(w, httptest.NewRequest(http.MethodPost, "/platform/refresh", nil).WithContext(ctx))
		return w
	}

	t.Run("joins the team of a group the IdP now reports, rotates the Dex token", func(t *testing.T) {
		f, flm := setup(t)
		f.dexGroups = []string{"ops@x.com"}
		if w := call(f, keys.PrefixPk); w.Code != http.StatusNoContent {
			t.Fatalf("status %d %s", w.Code, w.Body)
		}
		if !reflect.DeepEqual(flm.rec.teamMemberAdds, []string{"id-ops"}) {
			t.Fatalf("adds = %v (default teams are the login's job)", flm.rec.teamMemberAdds)
		}
		var rt string
		_, _ = f.store.Get(context.Background(), dexRefreshKind, email, &rt)
		if rt != "dex-rt-0+" {
			t.Fatalf("Dex token not rotated: %q", rt)
		}
	})
	t.Run("ek_ is refused before Dex is touched", func(t *testing.T) {
		f, _ := setup(t)
		if w := call(f, keys.PrefixEk); w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
		if len(f.dexSeen) != 0 {
			t.Fatal("an ek_ must not replay the user's Dex token")
		}
	})
	t.Run("IdP refusal asks for a login and enrols nothing", func(t *testing.T) {
		f, flm := setup(t)
		f.dexRefresh = func(string) (string, error) {
			return "", &oauth2.RetrieveError{Response: &http.Response{StatusCode: 400}, ErrorCode: "invalid_grant"}
		}
		if w := call(f, keys.PrefixPk); w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
		if len(flm.rec.teamMemberAdds) != 0 {
			t.Fatalf("adds = %v", flm.rec.teamMemberAdds)
		}
	})
	t.Run("Dex unreachable is a 503", func(t *testing.T) {
		f, _ := setup(t)
		f.dexRefresh = func(string) (string, error) { return "", errors.New("dial tcp: connection refused") }
		if w := call(f, keys.PrefixPk); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d", w.Code)
		}
	})
}
