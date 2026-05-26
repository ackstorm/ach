// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package http

import (
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// TestNew_NilSpec asserts the nil-spec branch returns ErrUpstreamInvalid.
func TestNew_NilSpec(t *testing.T) {
	t.Parallel()

	f, err := New(nil)
	if f != nil {
		t.Errorf("expected nil Fetcher; got %T", f)
	}
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Fatalf("expected ErrUpstreamInvalid; got %v", err)
	}
}

// TestNew_AcceptsHTTPAndHTTPS asserts that both http:// and https:// URLs
// construct cleanly. The original Phase 02 HTTPS-only invariant
// (T-02-02-03) was lifted in Phase 02.1 to admit in-cluster e2e
// fixture-servers serving plaintext HTTP.
func TestNew_AcceptsHTTPAndHTTPS(t *testing.T) {
	t.Parallel()

	cases := []string{
		"https://example.com/x",
		"http://marketplace-fixture.ach-system.svc.cluster.local/marketplace.json",
	}
	for _, url := range cases {
		url := url
		t.Run(url, func(t *testing.T) {
			t.Parallel()
			f, err := New(&achv1alpha1.HTTPSource{URL: url})
			if err != nil {
				t.Fatalf("expected no error for url=%q; got %v", url, err)
			}
			if f == nil {
				t.Fatalf("expected non-nil Fetcher for url=%q", url)
			}
		})
	}
}

// TestFetch_200_FreshETag asserts a 200 OK response builds the composite
// UpstreamRev as "ETag|Last-Modified" and returns Body to the caller
// without closing it.
func TestFetch_200_FreshETag(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Last-Modified", "Mon, 17 May 2026 09:00:00 GMT")
		w.WriteHeader(nethttp.StatusOK)
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv)
	res, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if err != nil {
		t.Fatalf("Fetch unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil FetchResult")
	}
	if res.NotModified {
		t.Errorf("expected NotModified=false on 200 OK")
	}
	if res.Body == nil {
		t.Fatal("expected non-nil Body on 200 OK")
	}
	defer res.Body.Close()
	if !strings.Contains(res.UpstreamRev, `"abc123"`) {
		t.Errorf("UpstreamRev should contain ETag; got %q", res.UpstreamRev)
	}
	if !strings.Contains(res.UpstreamRev, "|") {
		t.Errorf("UpstreamRev should contain '|' separator; got %q", res.UpstreamRev)
	}
	if !strings.Contains(res.UpstreamRev, "Mon, 17 May 2026") {
		t.Errorf("UpstreamRev should contain Last-Modified; got %q", res.UpstreamRev)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != "payload" {
		t.Errorf("expected body=payload; got %q", string(body))
	}
}

// TestFetch_304_NotModified asserts conditional-GET (If-None-Match)
// cycles cleanly: the server sees the If-None-Match header, returns
// 304, and the fetcher returns NotModified=true with the prior rev.
func TestFetch_304_NotModified(t *testing.T) {
	t.Parallel()

	const etag = `"abc123"`
	srv := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		got := r.Header.Get("If-None-Match")
		if got != etag {
			t.Errorf("server: expected If-None-Match=%q; got %q", etag, got)
		}
		w.WriteHeader(nethttp.StatusNotModified)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv)
	priorRev := etag + "|Mon, 17 May 2026 09:00:00 GMT"
	res, err := f.Fetch(context.Background(), sources.FetchRequest{PriorRev: priorRev})
	if err != nil {
		t.Fatalf("Fetch unexpected error: %v", err)
	}
	if !res.NotModified {
		t.Errorf("expected NotModified=true on 304")
	}
	if res.Body != nil {
		t.Errorf("expected nil Body on 304; got %T", res.Body)
	}
	if res.UpstreamRev != priorRev {
		t.Errorf("expected UpstreamRev preserved; got %q vs %q", res.UpstreamRev, priorRev)
	}
}

// TestFetch_304_AlsoSetsIfModifiedSince asserts the second conditional
// header (If-Modified-Since) is also attached when the prior rev encodes
// a Last-Modified component.
func TestFetch_304_AlsoSetsIfModifiedSince(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Header.Get("If-Modified-Since") == "" {
			t.Error("server: expected non-empty If-Modified-Since header")
		}
		w.WriteHeader(nethttp.StatusNotModified)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv)
	_, err := f.Fetch(context.Background(), sources.FetchRequest{
		PriorRev: `"e"|Mon, 17 May 2026 09:00:00 GMT`,
	})
	if err != nil {
		t.Fatalf("Fetch unexpected error: %v", err)
	}
}

// TestFetch_401_Unauthorized asserts 401 maps to ErrUnauthorized.
func TestFetch_401_Unauthorized(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusUnauthorized)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv)
	_, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized; got %v", err)
	}
}

// TestFetch_403_Unauthorized asserts 403 also maps to ErrUnauthorized.
func TestFetch_403_Unauthorized(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusForbidden)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv)
	_, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized; got %v", err)
	}
}

// TestFetch_404_NotFound asserts 404 maps to ErrNotFound.
func TestFetch_404_NotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNotFound)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv)
	_, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if !errors.Is(err, sources.ErrNotFound) {
		t.Fatalf("expected ErrNotFound; got %v", err)
	}
}

// TestFetch_500_Unreachable asserts 5xx maps to ErrUnreachable.
func TestFetch_500_Unreachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusInternalServerError)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv)
	_, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if !errors.Is(err, sources.ErrUnreachable) {
		t.Fatalf("expected ErrUnreachable; got %v", err)
	}
}

// TestFetch_400_UpstreamInvalid asserts other 4xx (e.g. 400 Bad Request)
// map to ErrUpstreamInvalid.
func TestFetch_400_UpstreamInvalid(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusBadRequest)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv)
	_, err := f.Fetch(context.Background(), sources.FetchRequest{})
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Fatalf("expected ErrUpstreamInvalid; got %v", err)
	}
}

// TestFetch_AuthHeaderAttached asserts the configured auth header is
// attached on every request when AuthSecretRef is set.
func TestFetch_AuthHeaderAttached(t *testing.T) {
	t.Parallel()

	const headerName = "X-Custom-Auth"
	const headerValue = "secret-token-abc"
	srv := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if got := r.Header.Get(headerName); got != headerValue {
			t.Errorf("server: expected %s=%q; got %q", headerName, headerValue, got)
		}
		w.WriteHeader(nethttp.StatusOK)
	}))
	defer srv.Close()

	f, err := New(&achv1alpha1.HTTPSource{
		URL: srv.URL,
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name:           "secret",
			HeaderName:     headerName,
			HeaderValueKey: "token",
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.setHTTPClientForTesting(srv.Client())

	res, err := f.Fetch(context.Background(), sources.FetchRequest{
		Secret: &corev1.Secret{Data: map[string][]byte{"token": []byte(headerValue)}},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Body != nil {
		_ = res.Body.Close()
	}
}

// TestFetch_AuthHeader_MissingKey asserts a missing auth-secret key
// classifies as ErrUnauthorized (T-02-02-01: error message mentions
// key name, never value).
func TestFetch_AuthHeader_MissingKey(t *testing.T) {
	t.Parallel()

	f, err := New(&achv1alpha1.HTTPSource{
		URL: "https://example.com",
		AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
			Name:           "secret",
			HeaderName:     "Authorization",
			HeaderValueKey: "token",
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = f.Fetch(context.Background(), sources.FetchRequest{
		Secret: &corev1.Secret{Data: map[string][]byte{}},
	})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for missing key; got %v", err)
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error should mention key NAME 'token'; got %q", err.Error())
	}
}

// TestSplitPriorRev unit-tests the conditional-GET composite parser.
func TestSplitPriorRev(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in          string
		wantEtag    string
		wantLastMod string
	}{
		{"", "", ""},
		{"|", "", ""},
		{"abc|Mon", "abc", "Mon"},
		{`"abc"|Mon, 17 May 2026 09:00:00 GMT`, `"abc"`, "Mon, 17 May 2026 09:00:00 GMT"},
		{"abc", "abc", ""}, // legacy single-component form
		{"|Mon", "", "Mon"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			gotEtag, gotLastMod := splitPriorRev(tc.in)
			if gotEtag != tc.wantEtag || gotLastMod != tc.wantLastMod {
				t.Errorf("splitPriorRev(%q) = (%q, %q); want (%q, %q)",
					tc.in, gotEtag, gotLastMod, tc.wantEtag, tc.wantLastMod)
			}
		})
	}
}

// newTestFetcher builds a Fetcher pointing at srv.URL with the test
// server's TLS-trusting client injected.
func newTestFetcher(t *testing.T, srv *httptest.Server) *Fetcher {
	t.Helper()
	f, err := New(&achv1alpha1.HTTPSource{URL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.setHTTPClientForTesting(srv.Client())
	return f
}
