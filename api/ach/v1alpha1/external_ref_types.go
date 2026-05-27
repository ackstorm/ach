// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RefreshBlock declares how often an external-reference resource is polled
// upstream and how stale a cached snapshot may be before content delivery
// is refused (Hub §10).
//
// CRD-04: maxStaleness is REQUIRED on every Plugin/PluginMarketplace/
// Prompt/Artifact. CRD-03: interval, when set, MUST NOT exceed maxStaleness;
// a resource with interval > maxStaleness would always be stale between
// refresh attempts.
type RefreshBlock struct {
	// Interval is how often the ACH Operator polls the upstream source.
	// Optional; when unset, the Operator uses an implementation default.
	// Format matches Kubernetes Duration (e.g. "15m", "1h", "30s").
	//
	// +optional
	Interval *metav1.Duration `json:"interval,omitempty"`

	// MaxStaleness bounds the age of a served cached snapshot. When
	// now - lastSuccessfulRefresh > maxStaleness, Content Service
	// returns 503 stale_cache_expired for the affected content
	// (§10). REQUIRED per CRD-04 — admission rejects a resource that
	// omits this field.
	//
	// +kubebuilder:validation:Required
	MaxStaleness metav1.Duration `json:"maxStaleness"`
}

// SourceAuthSecretRef references a Kubernetes Secret carrying credentials
// for fetching an upstream source. The Secret MUST live in the same
// namespace as the referring CR (no cross-namespace resolution in
// v1alpha1).
type SourceAuthSecretRef struct {
	// Name of the Kubernetes Secret in the CR's namespace.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key is the name of the Secret data key holding the bearer token.
	// Optional; when omitted on a git source type the operator falls
	// back to a provider-specific default key name:
	//   - github     → GITHUB_TOKEN
	//   - gitlab     → GITLAB_TOKEN
	//   - bitbucket  → BITBUCKET_TOKEN
	// (Matches the ecosystem env-var convention used by gh, glab,
	// terraform-provider-*, gitlab-runner, etc.) Other source types
	// (s3 / gcs / http) carry their own per-type key fields and do
	// NOT use this fallback.
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key,omitempty"`

	// AccessKeyIDKey is the data-key holding the AWS access-key-id
	// (S3 source only).
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	AccessKeyIDKey string `json:"accessKeyIdKey,omitempty"`

	// SecretAccessKeyKey is the data-key holding the AWS secret-access-key
	// (S3 source only).
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	SecretAccessKeyKey string `json:"secretAccessKeyKey,omitempty"`

	// HeaderName is the HTTP header name to attach for http sources
	// (e.g. "Authorization").
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	HeaderName string `json:"headerName,omitempty"`

	// HeaderValueKey is the data-key holding the value for HeaderName
	// (http source only).
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	HeaderValueKey string `json:"headerValueKey,omitempty"`
}

// GitHubSource describes a github-hosted upstream (Hub §10.1).
type GitHubSource struct {
	// Repo is the "<owner>/<name>" GitHub identifier.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Repo string `json:"repo"`

	// Path within the repo. Per-kind defaults apply (e.g. Plugin defaults
	// to repo root; PluginMarketplace defaults to .claude-plugin/marketplace.json).
	//
	// +optional
	Path string `json:"path,omitempty"`

	// Ref is a branch or tag name. No immutable commit refs in v1alpha1
	// (CRD-04, Hub §10).
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`

	// AuthSecretRef is optional. When set, the Secret named here MUST
	// exist in the CR's namespace at reconcile time and the operator
	// reads the bearer token from the named key (see SourceAuthSecretRef.Key).
	// When nil, the upstream fetch is anonymous — supported only for
	// public repositories. Anonymous + transport=rest is also supported
	// but subject to the provider's anonymous REST quota (GitHub:
	// 60 req/h/IP) — the bug FIX_GIT.txt fixes by defaulting transport
	// to git.
	//
	// +optional
	AuthSecretRef *SourceAuthSecretRef `json:"authSecretRef,omitempty"`

	// Transport selects the wire protocol used to fetch from this upstream.
	//
	//   "git"  — use git ls-remote + git clone (no per-IP REST rate-limit;
	//            recommended; default).
	//   "rest" — use the provider's REST API. Subject to per-IP anonymous
	//            quotas (GitHub: 60/h; GitLab: 60/min; Bitbucket: 60/h).
	//            Retained as a one-release escape hatch; will be removed.
	//
	// +kubebuilder:default=git
	// +kubebuilder:validation:Enum=git;rest
	// +optional
	Transport string `json:"transport,omitempty"`
}

// GitLabSource describes a gitlab-hosted upstream (Hub §10.1).
type GitLabSource struct {
	// Host of the GitLab instance. Defaults to gitlab.com when empty.
	//
	// +optional
	Host string `json:"host,omitempty"`

	// Project is the "<namespace>/<project>" GitLab identifier.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`

	// Path within the project repo.
	//
	// +optional
	Path string `json:"path,omitempty"`

	// Ref is a branch or tag name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`

	// AuthSecretRef is optional. When set, the Secret named here MUST
	// exist in the CR's namespace at reconcile time and the operator
	// reads the bearer token from the named key (see SourceAuthSecretRef.Key).
	// When nil, the upstream fetch is anonymous — supported only for
	// public projects. Anonymous + transport=rest is also supported
	// but subject to the provider's anonymous REST quota (GitLab:
	// 60 req/min/IP) — the bug FIX_GIT.txt fixes by defaulting transport
	// to git.
	//
	// +optional
	AuthSecretRef *SourceAuthSecretRef `json:"authSecretRef,omitempty"`

	// Transport selects the wire protocol used to fetch from this upstream.
	//
	//   "git"  — use git ls-remote + git clone (no per-IP REST rate-limit;
	//            recommended; default).
	//   "rest" — use the provider's REST API. Subject to per-IP anonymous
	//            quotas (GitHub: 60/h; GitLab: 60/min; Bitbucket: 60/h).
	//            Retained as a one-release escape hatch; will be removed.
	//
	// +kubebuilder:default=git
	// +kubebuilder:validation:Enum=git;rest
	// +optional
	Transport string `json:"transport,omitempty"`
}

// BitbucketSource describes a bitbucket-hosted upstream (Hub §10.1).
type BitbucketSource struct {
	// Workspace name on Bitbucket.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Workspace string `json:"workspace"`

	// Repo within the workspace.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Repo string `json:"repo"`

	// Path within the repo.
	//
	// +optional
	Path string `json:"path,omitempty"`

	// Ref is a branch or tag name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`

	// AuthSecretRef is optional. When set, the Secret named here MUST
	// exist in the CR's namespace at reconcile time and the operator
	// reads the bearer token from the named key (see SourceAuthSecretRef.Key).
	// When nil, the upstream fetch is anonymous — supported only for
	// public repositories on the git transport (transport=rest paired
	// with no auth typically fails because most Bitbucket Cloud REST
	// endpoints require auth even for public repos). Bitbucket Cloud
	// anonymous REST quota: 60 req/h/IP.
	//
	// +optional
	AuthSecretRef *SourceAuthSecretRef `json:"authSecretRef,omitempty"`

	// Transport selects the wire protocol used to fetch from this upstream.
	//
	//   "git"  — use git ls-remote + git clone (no per-IP REST rate-limit;
	//            recommended; default).
	//   "rest" — use the provider's REST API. Subject to per-IP anonymous
	//            quotas (GitHub: 60/h; GitLab: 60/min; Bitbucket: 60/h).
	//            Retained as a one-release escape hatch; will be removed.
	//
	// +kubebuilder:default=git
	// +kubebuilder:validation:Enum=git;rest
	// +optional
	Transport string `json:"transport,omitempty"`
}

// S3Source describes an S3-compatible object store upstream (Hub §10.1).
// No ref field — refresh polls the object's ETag (single key) or the
// prefix listing (directory scope).
type S3Source struct {
	// Bucket name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Bucket string `json:"bucket"`

	// Key is the object key (single object) or prefix (directory scope).
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// Region of the bucket.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// Endpoint for S3-compatible storage. Optional; defaults to AWS S3
	// when empty.
	//
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// AuthSecretRef points at the Secret carrying access-key-id and
	// secret-access-key (data keys named via accessKeyIdKey /
	// secretAccessKeyKey).
	//
	// +kubebuilder:validation:Required
	AuthSecretRef SourceAuthSecretRef `json:"authSecretRef"`
}

// GCSSource describes a Google Cloud Storage upstream (Hub §10.1).
// No ref field — refresh polls the object's generation (single object)
// or the prefix listing (directory scope).
type GCSSource struct {
	// Bucket name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Bucket string `json:"bucket"`

	// Object name (single object) or prefix (directory scope).
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Object string `json:"object"`

	// AuthSecretRef points at the Secret carrying a service-account JSON
	// blob (data key named via .key).
	//
	// +kubebuilder:validation:Required
	AuthSecretRef SourceAuthSecretRef `json:"authSecretRef"`
}

// HTTPSource describes a generic HTTP/HTTPS upstream (Hub §10.1).
// No ref field — refresh issues a conditional GET when the server
// supports If-Modified-Since / If-None-Match, otherwise a full GET.
//
// Phase 02.1: the original HTTPS-only invariant (T-02-02-03) was lifted
// to admit in-cluster development fixture-servers serving plaintext HTTP.
// Production deployments are expected to use https:// URLs by convention,
// but the constraint is no longer machine-enforced at admission or fetch.
type HTTPSource struct {
	// URL of the upstream resource. Accepts http:// or https://.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// AuthSecretRef optionally attaches an authentication header
	// (e.g. Authorization: Bearer ...). The data key named via
	// .headerValueKey supplies the header value at request time.
	//
	// +optional
	AuthSecretRef *SourceAuthSecretRef `json:"authSecretRef,omitempty"`
}

// ExternalRefStatus is the shared status surface for external-reference
// resources (Plugin, Prompt, Artifact, PluginMarketplace) per Hub §6.6.
type ExternalRefStatus struct {
	// ObservedGeneration is the metadata.generation of the CR the
	// reconciler most recently processed.
	//
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions exposes SourceReachable (and, for PluginMarketplace,
	// Synced) per §6.6.
	//
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// StorageLocation is the cached filesystem path the Content Service
	// serves from after the last successful refresh (§10.3). Empty until
	// the first successful refresh.
	//
	// +optional
	StorageLocation string `json:"storageLocation,omitempty"`

	// LastSuccessfulRefresh is the wall-clock time of the most recent
	// successful upstream fetch + atomic publish (§10.3 step 5).
	//
	// +optional
	LastSuccessfulRefresh *metav1.Time `json:"lastSuccessfulRefresh,omitempty"`

	// UpstreamRev is the per-source revision identifier the most recent
	// successful refresh recorded — for git sources this is the resolved
	// commit SHA; for S3 it is the object ETag; for GCS the object
	// generation; for HTTP a composite of ETag and Last-Modified
	// separated by a literal pipe. The Phase 2 reconciler reads this
	// value to pass as PriorRev on the next fetch for conditional-GET /
	// not-modified detection. Empty before the first successful refresh.
	//
	// +optional
	UpstreamRev string `json:"upstreamRev,omitempty"`
}
