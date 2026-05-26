// SPDX-License-Identifier: Apache-2.0

// Package registry is the per-source-type [sources.Fetcher] dispatcher.
//
// Lives in a sub-package (not in package sources) to avoid a circular
// import: per-source-type subpackages import internal/sources for
// FetchRequest / FetchResult / Fetcher; the dispatcher imports BOTH
// internal/sources and the six per-source-type subpackages. Putting
// the dispatcher in package sources would close the cycle. See Plan
// 02-02 SUMMARY "Deviations from Plan" for the rationale.
package registry

import (
	"fmt"

	"github.com/ackstorm/ach/internal/sources"
	bitbucketsrc "github.com/ackstorm/ach/internal/sources/bitbucket"
	gcssrc "github.com/ackstorm/ach/internal/sources/gcs"
	githubsrc "github.com/ackstorm/ach/internal/sources/github"
	gitlabsrc "github.com/ackstorm/ach/internal/sources/gitlab"
	httpsrc "github.com/ackstorm/ach/internal/sources/http"
	s3src "github.com/ackstorm/ach/internal/sources/s3"
)

// For returns the [sources.Fetcher] for spec.Type. The matching per-type
// subobject pointer MUST be non-nil (CRD admission via CEL XValidation
// already enforces this per CRD-03; this function defensively returns a
// clean error if a malformed CR ever slips through, so the reconciler
// can report SourceReachable=False, reason=InvalidConfig instead of
// nil-dereferencing inside the SDK call).
//
// Returns [sources.ErrUnknownSource] for any spec.Type outside the
// closed enum set {"github", "gitlab", "bitbucket", "s3", "gcs", "http"}.
func For(spec sources.SourceSpec) (sources.Fetcher, error) {
	switch spec.Type {
	case "github":
		if spec.GitHub == nil {
			return nil, fmt.Errorf("sources: For(%q): spec.GitHub is nil (CEL admission should have rejected this)", spec.Type)
		}
		return githubsrc.New(spec.GitHub)
	case "gitlab":
		if spec.GitLab == nil {
			return nil, fmt.Errorf("sources: For(%q): spec.GitLab is nil (CEL admission should have rejected this)", spec.Type)
		}
		return gitlabsrc.New(spec.GitLab)
	case "bitbucket":
		if spec.Bitbucket == nil {
			return nil, fmt.Errorf("sources: For(%q): spec.Bitbucket is nil (CEL admission should have rejected this)", spec.Type)
		}
		return bitbucketsrc.New(spec.Bitbucket)
	case "s3":
		if spec.S3 == nil {
			return nil, fmt.Errorf("sources: For(%q): spec.S3 is nil (CEL admission should have rejected this)", spec.Type)
		}
		return s3src.New(spec.S3)
	case "gcs":
		if spec.GCS == nil {
			return nil, fmt.Errorf("sources: For(%q): spec.GCS is nil (CEL admission should have rejected this)", spec.Type)
		}
		return gcssrc.New(spec.GCS)
	case "http":
		if spec.HTTP == nil {
			return nil, fmt.Errorf("sources: For(%q): spec.HTTP is nil (CEL admission should have rejected this)", spec.Type)
		}
		return httpsrc.New(spec.HTTP)
	default:
		return nil, fmt.Errorf("sources: For(%q): %w", spec.Type, sources.ErrUnknownSource)
	}
}
