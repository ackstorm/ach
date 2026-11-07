// SPDX-License-Identifier: Apache-2.0

package http

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// TestFetch_RedirectForwardsAuthHeaderOnlyToSameHost asserts the Secret-valued
// header follows a same-host redirect and is dropped on a cross-host one.
func TestFetch_RedirectForwardsAuthHeaderOnlyToSameHost(t *testing.T) {
	t.Parallel()

	const name = "X-Api-Key"
	var other, same string
	otherSrv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		other = r.Header.Get(name)
	}))
	defer otherSrv.Close()
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/cross":
			nethttp.Redirect(w, r, otherSrv.URL+"/x", nethttp.StatusFound)
		case "/same":
			nethttp.Redirect(w, r, "/landed", nethttp.StatusFound)
		default:
			same = r.Header.Get(name)
		}
	}))
	defer srv.Close()

	fetch := func(path string) {
		t.Helper()
		f, err := New(&achv1alpha1.HTTPSource{
			URL:           srv.URL + path,
			AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{Name: "s", HeaderName: name, HeaderValueKey: "k"},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		res, err := f.Fetch(context.Background(), sources.FetchRequest{
			Secret: &corev1.Secret{Data: map[string][]byte{"k": []byte("tok")}},
		})
		if err != nil {
			t.Fatalf("Fetch %s: %v", path, err)
		}
		if res.Body != nil {
			_ = res.Body.Close()
		}
	}
	fetch("/cross")
	if other != "" {
		t.Errorf("secret header reached another host: %q", other)
	}
	fetch("/same")
	if same != "tok" {
		t.Errorf("same-host redirect lost the header: %q", same)
	}
}

// TestCheckRedirect_DropsAuthHeaderOnSchemeChange asserts a same-host redirect
// from https to http does not carry the Secret-valued header.
func TestCheckRedirect_DropsAuthHeaderOnSchemeChange(t *testing.T) {
	const name = "X-Source-Token"
	f, err := New(&achv1alpha1.HTTPSource{
		URL:           "https://host.test/a",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{Name: "s", HeaderName: name, HeaderValueKey: "k"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	orig, err := nethttp.NewRequest(nethttp.MethodGet, "https://host.test/a", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	next, err := nethttp.NewRequest(nethttp.MethodGet, "http://host.test/b", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	next.Header.Set(name, "value")
	if err := f.httpClient.CheckRedirect(next, []*nethttp.Request{orig}); err != nil {
		t.Fatalf("CheckRedirect: %v", err)
	}
	if got := next.Header.Get(name); got != "" {
		t.Fatalf("header survived the scheme change: %q", got)
	}
}
