// Copyright 2026 ACKstorm
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

// Package gcs is the Google Cloud Storage source fetcher (Hub §10.1).
//
// Behavior (Hub §10.1):
//
//  1. ObjectHandle.Attrs to obtain the object's Generation (GCS's
//     monotonically-increasing version counter).
//  2. If req.PriorRev equals the generation (decimal-formatted),
//     returns FetchResult{NotModified: true}.
//  3. Otherwise ObjectHandle.NewReader for the streaming body.
//
// Authentication uses the service-account JSON blob extracted from
// req.Secret.Data[spec.AuthSecretRef.Key].
package gcs

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"cloud.google.com/go/storage"
	googleapi "google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// Fetcher implements [sources.Fetcher] for GCSSource.
type Fetcher struct {
	spec *achv1alpha1.GCSSource
}

// New constructs a GCS source fetcher. Returns ErrUpstreamInvalid when
// spec is nil.
func New(spec *achv1alpha1.GCSSource) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("gcs: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	return &Fetcher{spec: spec}, nil
}

// Fetch implements [sources.Fetcher]. See package doc for behavior.
func (f *Fetcher) Fetch(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	// 1. Extract SA JSON from Secret.
	if req.Secret == nil {
		return nil, fmt.Errorf("gcs: auth secret %q is nil: %w",
			f.spec.AuthSecretRef.Name, sources.ErrUnauthorized)
	}
	saJSON := req.Secret.Data[f.spec.AuthSecretRef.Key]
	if len(saJSON) == 0 {
		return nil, fmt.Errorf("gcs: missing auth secret key %q: %w",
			f.spec.AuthSecretRef.Key, sources.ErrUnauthorized)
	}

	// 2. Build storage client with credentials JSON.
	client, err := storage.NewClient(ctx, option.WithCredentialsJSON(saJSON))
	if err != nil {
		return nil, fmt.Errorf("gcs: NewClient: %v: %w", err, sources.ErrUpstreamInvalid)
	}
	// Note: client.Close() releases gRPC connections. We DO NOT close
	// here because the returned *storage.Reader holds an open stream
	// that needs the client alive. The reader has its own Close which
	// the reconciler must call via defer.
	//
	// On the NotModified / error branches we close the client before
	// returning.

	obj := client.Bucket(f.spec.Bucket).Object(f.spec.Object)

	// 3. Probe object attrs for the generation.
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		_ = client.Close()
		return nil, classifyGCSErr(err, "Attrs")
	}
	rev := strconv.FormatInt(attrs.Generation, 10)

	// 4. Conditional-fetch.
	if req.PriorRev != "" && req.PriorRev == rev {
		_ = client.Close()
		return &sources.FetchResult{
			NotModified: true,
			UpstreamRev: rev,
		}, nil
	}

	// 5. Open reader for the body. *storage.Reader satisfies
	//    io.ReadCloser; caller closes via defer. We CANNOT close the
	//    client here because the reader holds an underlying connection;
	//    wrap the reader so closing it also closes the client.
	r, err := obj.NewReader(ctx)
	if err != nil {
		_ = client.Close()
		return nil, classifyGCSErr(err, "NewReader")
	}
	return &sources.FetchResult{
		Body:        &readerWithClose{Reader: r, client: client},
		UpstreamRev: rev,
		NotModified: false,
	}, nil
}

// readerWithClose chains the *storage.Reader's Close to the parent
// *storage.Client's Close so the caller's `defer body.Close()` releases
// both.
type readerWithClose struct {
	*storage.Reader
	client *storage.Client
}

func (r *readerWithClose) Close() error {
	readerErr := r.Reader.Close()
	clientErr := r.client.Close()
	if readerErr != nil {
		return readerErr
	}
	return clientErr
}

// classifyGCSErr maps a storage SDK error into one of the [sources]
// sentinel errors. storage.ErrObjectNotExist is the canonical "not
// found" signal; googleapi.Error carries HTTP status for everything
// else.
func classifyGCSErr(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, storage.ErrObjectNotExist) {
		return fmt.Errorf("gcs: %s: %w", op, sources.ErrNotFound)
	}
	if errors.Is(err, storage.ErrBucketNotExist) {
		return fmt.Errorf("gcs: %s: %w", op, sources.ErrNotFound)
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == 401, apiErr.Code == 403:
			return fmt.Errorf("gcs: %s %d: %w", op, apiErr.Code, sources.ErrUnauthorized)
		case apiErr.Code == 404:
			return fmt.Errorf("gcs: %s 404: %w", op, sources.ErrNotFound)
		case apiErr.Code >= 500:
			return fmt.Errorf("gcs: %s %d: %w", op, apiErr.Code, sources.ErrUnreachable)
		case apiErr.Code >= 400:
			return fmt.Errorf("gcs: %s %d: %w", op, apiErr.Code, sources.ErrUpstreamInvalid)
		}
	}
	return fmt.Errorf("gcs: %s: %v: %w", op, err, sources.ErrUnreachable)
}

// Compile-time assertion.
var _ sources.Fetcher = (*Fetcher)(nil)
