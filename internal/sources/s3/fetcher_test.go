// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package s3

import (
	"context"
	"errors"
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

// TestFetch_NilSecret asserts a nil Secret classifies as ErrUnauthorized.
func TestFetch_NilSecret(t *testing.T) {
	t.Parallel()

	f := newTestFetcher()
	_, err := f.Fetch(context.Background(), sources.FetchRequest{Secret: nil})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for nil Secret; got %v", err)
	}
}

// TestFetch_MissingAccessKeyID asserts a Secret without the configured
// AccessKeyIDKey classifies as ErrUnauthorized. T-02-02-01: error
// mentions the key NAME but never a value.
func TestFetch_MissingAccessKeyID(t *testing.T) {
	t.Parallel()

	f := newTestFetcher()
	secret := &corev1.Secret{Data: map[string][]byte{
		"secret-access-key": []byte("sk-present"),
	}}
	_, err := f.Fetch(context.Background(), sources.FetchRequest{Secret: secret})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized; got %v", err)
	}
	if !strings.Contains(err.Error(), "access-key-id") {
		t.Errorf("error should mention key NAME 'access-key-id'; got %q", err.Error())
	}
}

// TestFetch_MissingSecretAccessKey asserts the same for the SecretAccessKeyKey.
func TestFetch_MissingSecretAccessKey(t *testing.T) {
	t.Parallel()

	f := newTestFetcher()
	secret := &corev1.Secret{Data: map[string][]byte{
		"access-key-id": []byte("ak-present"),
	}}
	_, err := f.Fetch(context.Background(), sources.FetchRequest{Secret: secret})
	if !errors.Is(err, sources.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized; got %v", err)
	}
	if !strings.Contains(err.Error(), "secret-access-key") {
		t.Errorf("error should mention key NAME 'secret-access-key'; got %q", err.Error())
	}
}

// newTestFetcher constructs a Fetcher with a valid spec for the
// negative-path tests.
func newTestFetcher() *Fetcher {
	f, _ := New(&achv1alpha1.S3Source{
		Bucket: "bucket",
		Key:    "key",
		Region: "us-east-1",
		AuthSecretRef: achv1alpha1.SourceAuthSecretRef{
			Name:               "secret",
			AccessKeyIDKey:     "access-key-id",
			SecretAccessKeyKey: "secret-access-key",
		},
	})
	return f
}

// TestNew_HTTPSEndpointAccepted asserts that an https:// endpoint
// override passes the WR-01 constructor gate.
func TestNew_HTTPSEndpointAccepted(t *testing.T) {
	t.Setenv(allowHTTPEndpointEnv, "") // ensure escape hatch not set
	f, err := New(&achv1alpha1.S3Source{
		Bucket:   "bucket",
		Key:      "key",
		Region:   "us-east-1",
		Endpoint: "https://minio.internal:9000",
		AuthSecretRef: achv1alpha1.SourceAuthSecretRef{
			Name:               "secret",
			AccessKeyIDKey:     "access-key-id",
			SecretAccessKeyKey: "secret-access-key",
		},
	})
	if err != nil {
		t.Fatalf("https:// endpoint should be accepted; got %v", err)
	}
	if f == nil {
		t.Fatal("expected non-nil Fetcher")
	}
}

// TestNew_HTTPEndpointRejectedWithoutOptIn asserts WR-01: a non-HTTPS
// endpoint is rejected when the deployment-wide opt-in env var is
// absent.
func TestNew_HTTPEndpointRejectedWithoutOptIn(t *testing.T) {
	t.Setenv(allowHTTPEndpointEnv, "") // explicitly unset
	_, err := New(&achv1alpha1.S3Source{
		Bucket:   "bucket",
		Key:      "key",
		Region:   "us-east-1",
		Endpoint: "http://minio.internal:9000",
		AuthSecretRef: achv1alpha1.SourceAuthSecretRef{
			Name:               "secret",
			AccessKeyIDKey:     "access-key-id",
			SecretAccessKeyKey: "secret-access-key",
		},
	})
	if !errors.Is(err, sources.ErrUpstreamInvalid) {
		t.Fatalf("expected ErrUpstreamInvalid for http:// endpoint; got %v", err)
	}
	if !strings.Contains(err.Error(), allowHTTPEndpointEnv) {
		t.Errorf("error should mention the opt-in env var %q; got %q",
			allowHTTPEndpointEnv, err.Error())
	}
}

// TestNew_HTTPEndpointAcceptedWithOptIn asserts the deployment escape
// hatch works: setting ACH_S3_ALLOW_HTTP_ENDPOINT=true permits a
// non-HTTPS endpoint (for local MinIO test clusters).
func TestNew_HTTPEndpointAcceptedWithOptIn(t *testing.T) {
	t.Setenv(allowHTTPEndpointEnv, "true")
	f, err := New(&achv1alpha1.S3Source{
		Bucket:   "bucket",
		Key:      "key",
		Region:   "us-east-1",
		Endpoint: "http://minio.internal:9000",
		AuthSecretRef: achv1alpha1.SourceAuthSecretRef{
			Name:               "secret",
			AccessKeyIDKey:     "access-key-id",
			SecretAccessKeyKey: "secret-access-key",
		},
	})
	if err != nil {
		t.Fatalf("expected http:// to be accepted with opt-in; got %v", err)
	}
	if f == nil {
		t.Fatal("expected non-nil Fetcher")
	}
}
