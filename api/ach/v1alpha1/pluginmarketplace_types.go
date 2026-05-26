// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MarketplaceFilters narrows the upstream marketplace catalog to a curated
// subset via anchored RE2 regex include/exclude patterns (Hub §12).
//
//   - filters.include OPTIONAL: when set, only names matched by at least one
//     anchored include pattern survive. When absent (or empty), all upstream
//     names pass through.
//   - filters.exclude OPTIONAL: when set, names matched by any anchored exclude
//     pattern are dropped AFTER include. exclude wins on conflict.
//
// CRD admission catches obviously-empty entries; full RE2 compile validation
// runs at reconcile (Synced=False, reason=InvalidConfig on failure).
type MarketplaceFilters struct {
	// Include is a list of anchored RE2 patterns that narrow the catalog.
	// When omitted or empty, all upstream names pass through.
	//
	// +optional
	Include []string `json:"include,omitempty"`

	// Exclude is a list of anchored RE2 patterns dropped from the catalog
	// after Include is applied.
	//
	// +optional
	Exclude []string `json:"exclude,omitempty"`
}

// PluginMarketplaceSpec defines the desired state of PluginMarketplace (Hub §12).
//
// The marketplace file is fetched via the chosen source type (§12.1).
// Body handling depends on the type:
//
//   - github / gitlab / bitbucket: the fetcher returns the full repo
//     tarball (Hub §10.1, Path-subset extraction deferred to v1beta1).
//     Stage-1 walks the tarball and extracts the first regular file
//     whose path ends with `/.claude-plugin/marketplace.json`. spec.<type>.path
//     is IGNORED for marketplaces (the file location is conventional).
//   - s3 / gcs / http: spec.<type>.key/object/url MUST point at the
//     marketplace.json body directly; the fetcher returns that body
//     verbatim with no extraction.
//
// CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
// CRD-04: spec.refresh.maxStaleness is REQUIRED.
type PluginMarketplaceSpec struct {
	// Type names the upstream source kind for the marketplace file.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=github;gitlab;bitbucket;s3;gcs;http
	Type string `json:"type"`

	// Refresh declares poll cadence and staleness bound (CRD-04).
	//
	// +kubebuilder:validation:Required
	Refresh RefreshBlock `json:"refresh"`

	// Filters narrows the upstream catalog (Hub §12). Optional.
	//
	// +optional
	Filters *MarketplaceFilters `json:"filters,omitempty"`

	// GitHub source. Required when spec.type == "github".
	//
	// +optional
	GitHub *GitHubSource `json:"github,omitempty"`

	// GitLab source. Required when spec.type == "gitlab".
	//
	// +optional
	GitLab *GitLabSource `json:"gitlab,omitempty"`

	// Bitbucket source. Required when spec.type == "bitbucket".
	//
	// +optional
	Bitbucket *BitbucketSource `json:"bitbucket,omitempty"`

	// S3 source. Required when spec.type == "s3".
	//
	// +optional
	S3 *S3Source `json:"s3,omitempty"`

	// GCS source. Required when spec.type == "gcs".
	//
	// +optional
	GCS *GCSSource `json:"gcs,omitempty"`

	// HTTP source. Required when spec.type == "http".
	//
	// +optional
	HTTP *HTTPSource `json:"http,omitempty"`
}

// PluginMarketplaceStatus defines the observed state of PluginMarketplace.
//
// In addition to the shared ExternalRefStatus, PluginMarketplace exposes a
// Synced condition (§6.6) with reasons NameConflict, UpstreamInvalid,
// InvalidConfig, UnsupportedPluginSource. Phase 1 ships the field
// surface; Phase 2 fills the reconciler logic.
type PluginMarketplaceStatus struct {
	ExternalRefStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:validation:XValidation:rule="has(self.spec.refresh) && has(self.spec.refresh.maxStaleness)",message="PluginMarketplace.spec.refresh.maxStaleness is REQUIRED (CRD-04)"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.refresh.interval) || duration(self.spec.refresh.interval) <= duration(self.spec.refresh.maxStaleness)",message="PluginMarketplace.spec.refresh.interval must be <= refresh.maxStaleness (CRD-03)"
// +kubebuilder:validation:XValidation:rule="(self.spec.type == 'github' && has(self.spec.github)) || (self.spec.type == 'gitlab' && has(self.spec.gitlab)) || (self.spec.type == 'bitbucket' && has(self.spec.bitbucket)) || (self.spec.type == 's3' && has(self.spec.s3)) || (self.spec.type == 'gcs' && has(self.spec.gcs)) || (self.spec.type == 'http' && has(self.spec.http))",message="PluginMarketplace.spec must include the subobject matching spec.type (CRD-03)"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// PluginMarketplace is the Schema for the pluginmarketplaces API (Hub §12).
type PluginMarketplace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PluginMarketplaceSpec   `json:"spec,omitempty"`
	Status PluginMarketplaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PluginMarketplaceList contains a list of PluginMarketplace.
type PluginMarketplaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PluginMarketplace `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PluginMarketplace{}, &PluginMarketplaceList{})
}
