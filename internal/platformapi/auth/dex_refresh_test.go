// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"
)

// TestDexRefresh_FailedRefreshReachesDexOnce: a refused refresh must present
// the Dex token once. A second presentation of the same token is what Dex
// reads as reuse.
func TestDexRefresh_FailedRefreshReachesDexOnce(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if _, _, ok := r.BasicAuth(); !ok {
			t.Errorf("client secret not in the Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
	}))
	defer srv.Close()

	d := OAuthDeps{Auth: Deps{OAuth2Cfg: &oauth2.Config{
		ClientID: "ach", ClientSecret: "s3cret",
		Endpoint: oauth2.Endpoint{TokenURL: srv.URL},
	}}}
	if _, _, err := d.dexRefresh(context.Background(), "dex-refresh"); err == nil {
		t.Fatal("want an error from a refused refresh")
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("Dex token endpoint hit %d times, want 1", n)
	}
}
