// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package registry_test

import (
	"errors"
	"strings"
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
	"github.com/ackstorm/ach/internal/sources/registry"
)

// TestRegistryCoversEnum asserts every spec.type enum value declared on
// the Plugin / PluginMarketplace / Artifact / Prompt CRDs maps to a
// usable Fetcher via [registry.For]. The set is the closed enum
// {github, gitlab, bitbucket, s3, gcs, http} per Hub §10.1.
//
// This test would break the moment a new enum value was added to the
// CRD without a matching dispatch arm in [registry.For] — the
// safety-net for the CRD-03 / OP-03 closed-set invariant.
func TestRegistryCoversEnum(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec sources.SourceSpec
	}{
		{
			name: "github",
			spec: sources.SourceSpec{
				Type: "github",
				GitHub: &achv1alpha1.GitHubSource{
					Repo: "owner/repo",
					Ref:  "main",
					AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
						Name: "secret",
						Key:  "token",
					},
				},
			},
		},
		{
			name: "gitlab",
			spec: sources.SourceSpec{
				Type: "gitlab",
				GitLab: &achv1alpha1.GitLabSource{
					Project: "group/project",
					Ref:     "main",
					AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
						Name: "secret",
						Key:  "token",
					},
				},
			},
		},
		{
			name: "bitbucket",
			spec: sources.SourceSpec{
				Type: "bitbucket",
				Bitbucket: &achv1alpha1.BitbucketSource{
					Workspace: "workspace",
					Repo:      "repo",
					Ref:       "main",
					AuthSecretRef: &achv1alpha1.SourceAuthSecretRef{
						Name: "secret",
						Key:  "token",
					},
				},
			},
		},
		{
			name: "s3",
			spec: sources.SourceSpec{
				Type: "s3",
				S3: &achv1alpha1.S3Source{
					Bucket: "bucket",
					Key:    "key",
					Region: "us-east-1",
					AuthSecretRef: achv1alpha1.SourceAuthSecretRef{
						Name:               "secret",
						AccessKeyIDKey:     "access-key-id",
						SecretAccessKeyKey: "secret-access-key",
					},
				},
			},
		},
		{
			name: "gcs",
			spec: sources.SourceSpec{
				Type: "gcs",
				GCS: &achv1alpha1.GCSSource{
					Bucket: "bucket",
					Object: "object",
					AuthSecretRef: achv1alpha1.SourceAuthSecretRef{
						Name: "secret",
						Key:  "sa.json",
					},
				},
			},
		},
		{
			name: "http",
			spec: sources.SourceSpec{
				Type: "http",
				HTTP: &achv1alpha1.HTTPSource{
					URL: "https://example.com/marketplace.json",
				},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, err := registry.For(tc.spec)
			if err != nil {
				t.Fatalf("registry.For(type=%s) unexpected error: %v", tc.name, err)
			}
			if f == nil {
				t.Fatalf("registry.For(type=%s) returned nil Fetcher", tc.name)
			}
		})
	}
}

// TestFor_UnknownType asserts an out-of-enum Type returns
// ErrUnknownSource (CRD-03 defense in depth).
func TestFor_UnknownType(t *testing.T) {
	t.Parallel()

	spec := sources.SourceSpec{Type: "ftp"}
	f, err := registry.For(spec)
	if f != nil {
		t.Fatalf("expected nil Fetcher for unknown type; got %T", f)
	}
	if !errors.Is(err, sources.ErrUnknownSource) {
		t.Fatalf("expected ErrUnknownSource; got %v", err)
	}
	// Confirm the error message names the offending type for operator-
	// readable logs (kubectl describe shows this verbatim).
	if !strings.Contains(err.Error(), "ftp") {
		t.Errorf("error message should mention the unknown type 'ftp'; got %q", err.Error())
	}
}

// TestFor_NilSubobject asserts that when spec.Type names a known kind
// but the matching subobject is nil, the registry returns a clean error
// (NOT a panic). Plan 02-02 documents this as defense in depth above the
// CRD CEL XValidation rule that enforces subobject presence.
func TestFor_NilSubobject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec sources.SourceSpec
		kind string
	}{
		{"github nil", sources.SourceSpec{Type: "github", GitHub: nil}, "GitHub"},
		{"gitlab nil", sources.SourceSpec{Type: "gitlab", GitLab: nil}, "GitLab"},
		{"bitbucket nil", sources.SourceSpec{Type: "bitbucket", Bitbucket: nil}, "Bitbucket"},
		{"s3 nil", sources.SourceSpec{Type: "s3", S3: nil}, "S3"},
		{"gcs nil", sources.SourceSpec{Type: "gcs", GCS: nil}, "GCS"},
		{"http nil", sources.SourceSpec{Type: "http", HTTP: nil}, "HTTP"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, err := registry.For(tc.spec)
			if f != nil {
				t.Fatalf("expected nil Fetcher when subobject is nil; got %T", f)
			}
			if err == nil {
				t.Fatalf("expected non-nil error when subobject is nil")
			}
			if !strings.Contains(err.Error(), tc.spec.Type) {
				t.Errorf("error message should mention the type %q; got %q", tc.spec.Type, err.Error())
			}
			if !strings.Contains(err.Error(), "nil") {
				t.Errorf("error message should mention 'nil'; got %q", err.Error())
			}
		})
	}
}
