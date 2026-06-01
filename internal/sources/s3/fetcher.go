// SPDX-License-Identifier: Apache-2.0

// Package s3 is the S3-compatible source fetcher (Hub §10.1).
//
// Behavior (Hub §10.1):
//
//  1. HeadObject for the configured Key to obtain the ETag (Bucket +
//     Key are both single-object scope; directory-scope listing for
//     Artifact spec.scope=directory is the RECONCILER's concern — this
//     fetcher always operates on a single key).
//  2. If req.PriorRev equals the ETag, returns
//     FetchResult{NotModified: true} — caller skips re-publish.
//  3. Otherwise GetObject (passes If-None-Match for safety; the SDK
//     surfaces 304-equivalent via NotModified-class errors) and returns
//     the streaming body.
//
// Authentication uses static credentials extracted from
// req.Secret.Data[AccessKeyIDKey + SecretAccessKeyKey]. Endpoint
// override is honored for HTTPS S3-compatible alternatives (e.g.
// MinIO with TLS); UsePathStyle=true when an override is set.
// Non-HTTPS endpoints are rejected at fetcher construction time
// unless the deployment opts in via the ACH_S3_ALLOW_HTTP_ENDPOINT
// env var (WR-01 / CLAUDE.md no-HTTP-escape-hatch invariant).
package s3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/sources"
)

// allowHTTPEndpointEnv is the deployment-wide escape hatch that permits
// a non-HTTPS spec.Endpoint override (WR-01). CLAUDE.md's "HTTPS-only
// via deployment-configured ACH_BASE_URL — no HTTP escape hatch"
// applies to the spec endpoint too: deployers who need plain HTTP for
// a local MinIO test cluster must set this env var to "true" on the
// operator pod (a single deliberate opt-in), rather than every CR
// author bypassing HTTPS unilaterally. Production deployments leave
// this unset, in which case any non-https:// spec.Endpoint is rejected
// at fetcher construction with sources.ErrUpstreamInvalid.
const allowHTTPEndpointEnv = "ACH_S3_ALLOW_HTTP_ENDPOINT"

// Fetcher implements [sources.Fetcher] for S3Source.
type Fetcher struct {
	spec *achv1alpha1.S3Source
}

// New constructs an S3 source fetcher. Returns ErrUpstreamInvalid when
// spec is nil, or when spec.Endpoint is set to a non-HTTPS scheme and
// the deployment-wide ACH_S3_ALLOW_HTTP_ENDPOINT escape hatch is not
// "true" (WR-01).
//
// The HTTPS-only check parallels the http source fetcher's strict
// https:// constraint (internal/sources/http/fetcher.go) and CLAUDE.md's
// constraint that there is no HTTP escape hatch. The escape hatch
// exists for local MinIO test clusters; production must not set it.
func New(spec *achv1alpha1.S3Source) (*Fetcher, error) {
	if spec == nil {
		return nil, fmt.Errorf("s3: spec is nil: %w", sources.ErrUpstreamInvalid)
	}
	if spec.Endpoint != "" && !strings.HasPrefix(spec.Endpoint, "https://") {
		if os.Getenv(allowHTTPEndpointEnv) != "true" {
			return nil, fmt.Errorf(
				"s3: endpoint %q is not https:// — set %s=true on the operator pod to opt in: %w",
				spec.Endpoint, allowHTTPEndpointEnv, sources.ErrUpstreamInvalid)
		}
	}
	return &Fetcher{spec: spec}, nil
}

// Fetch implements [sources.Fetcher]. See package doc for behavior.
func (f *Fetcher) Fetch(ctx context.Context, req sources.FetchRequest) (*sources.FetchResult, error) {
	// 1. Extract static credentials from Secret. Both keys are required.
	if req.Secret == nil {
		return nil, fmt.Errorf("s3: auth secret %q is nil: %w",
			f.spec.AuthSecretRef.Name, sources.ErrUnauthorized)
	}
	accessKeyID := req.Secret.Data[f.spec.AuthSecretRef.AccessKeyIDKey]
	secretAccessKey := req.Secret.Data[f.spec.AuthSecretRef.SecretAccessKeyKey]
	if len(accessKeyID) == 0 {
		return nil, fmt.Errorf("s3: missing auth secret key %q: %w",
			f.spec.AuthSecretRef.AccessKeyIDKey, sources.ErrUnauthorized)
	}
	if len(secretAccessKey) == 0 {
		return nil, fmt.Errorf("s3: missing auth secret key %q: %w",
			f.spec.AuthSecretRef.SecretAccessKeyKey, sources.ErrUnauthorized)
	}

	// 2. Build aws.Config with static credentials. WithCredentialsProvider
	//    pins ACH to the configured keys; no IMDS / env-var fallback.
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(f.spec.Region),
		awsconfig.WithCredentialsProvider(awscreds.NewStaticCredentialsProvider(
			string(accessKeyID), string(secretAccessKey), "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("s3: LoadDefaultConfig: %v: %w",
			err, sources.ErrUpstreamInvalid)
	}

	// 3. Build the S3 client; honor spec.Endpoint override for MinIO etc.
	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		if f.spec.Endpoint != "" {
			o.BaseEndpoint = aws.String(f.spec.Endpoint)
			o.UsePathStyle = true
		}
	})

	// 4. HeadObject for ETag (no body transferred — cheap probe).
	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(f.spec.Bucket),
		Key:    aws.String(f.spec.Key),
	})
	if err != nil {
		return nil, classifyS3Err(err, "HeadObject")
	}
	etag := aws.ToString(head.ETag)
	// S3 wraps ETags in literal double quotes — strip for stable
	// comparison with req.PriorRev (which we also store unquoted).
	etag = strings.Trim(etag, `"`)

	// 5. Conditional-fetch via PriorRev equality.
	if req.PriorRev != "" && req.PriorRev == etag {
		return &sources.FetchResult{
			NotModified: true,
			UpstreamRev: etag,
		}, nil
	}

	// 6. GetObject for body. We also send the quoted ETag as
	//    If-None-Match — defense in depth in case PriorRev was lost
	//    but the object hasn't changed; S3 surfaces this as a
	//    NotModified-class error we map to NotModified=true.
	quotedETag := `"` + etag + `"`
	out, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket:      aws.String(f.spec.Bucket),
		Key:         aws.String(f.spec.Key),
		IfNoneMatch: aws.String(quotedETag),
	})
	if err != nil {
		// 304-equivalent: AWS surfaces a smithy *http.ResponseError with
		// StatusCode 304 (no typed error type for NotModified on
		// GetObject). Detect via HTTP status and translate to
		// NotModified=true.
		var httpErr *smithyhttp.ResponseError
		if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 304 {
			return &sources.FetchResult{
				NotModified: true,
				UpstreamRev: etag,
			}, nil
		}
		return nil, classifyS3Err(err, "GetObject")
	}

	return &sources.FetchResult{
		Body:        out.Body,
		UpstreamRev: etag,
		NotModified: false,
	}, nil
}

// classifyS3Err maps an aws-sdk-go-v2 error into one of the [sources]
// sentinel errors via typed checks (S3 returns typed errors for
// NoSuchKey / NoSuchBucket) PLUS HTTP-status inspection on the
// underlying smithy ResponseError.
func classifyS3Err(err error, op string) error {
	if err == nil {
		return nil
	}

	// 1. Typed error checks (S3 returns NoSuchKey + NoSuchBucket as
	//    distinct types).
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return fmt.Errorf("s3: %s: %w", op, sources.ErrNotFound)
	}
	var noSuchBucket *s3types.NoSuchBucket
	if errors.As(err, &noSuchBucket) {
		return fmt.Errorf("s3: %s: %w", op, sources.ErrNotFound)
	}
	var notFound *s3types.NotFound
	if errors.As(err, &notFound) {
		return fmt.Errorf("s3: %s: %w", op, sources.ErrNotFound)
	}

	// 2. HTTP-status inspection on the underlying response error. Only
	//    status >= 400 maps via the shared ladder; a <400 response error
	//    (pathological) falls through to the network-Unreachable default,
	//    preserving the original behavior.
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		if status := respErr.HTTPStatusCode(); status >= 400 {
			return sources.ClassifyHTTPStatus("s3", op, status)
		}
	}

	// 3. Default: network / connection / DNS errors classify as
	//    Unreachable. The SDK's `*types.HTTPResponseError` or net.OpError
	//    arrive here.
	return fmt.Errorf("s3: %s: %v: %w", op, err, sources.ErrUnreachable)
}

// Compile-time assertion.
var _ sources.Fetcher = (*Fetcher)(nil)
