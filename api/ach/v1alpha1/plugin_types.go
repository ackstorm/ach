// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PluginSpec defines the desired state of Plugin (Hub §11).
//
// A Plugin references an upstream location whose subtree contains a
// Claude Code plugin tree (root directory with .claude-plugin/plugin.json
// and component directories). ACH fetches the subtree and serves it as
// a .tar.gz archive (CRD-04, CRD-03, Hub §11).
//
// CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
// CRD-04: spec.refresh.maxStaleness is REQUIRED; spec.refresh.interval,
// when set, MUST NOT exceed spec.refresh.maxStaleness.
type PluginSpec struct {
	// Type names the upstream source kind. Drives which one of the
	// type-specific subobjects below is required.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=github;gitlab;bitbucket;s3;gcs;http
	Type string `json:"type"`

	// Refresh declares poll cadence and staleness bound (CRD-04).
	//
	// +kubebuilder:validation:Required
	Refresh RefreshBlock `json:"refresh"`

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

// PluginStatus defines the observed state of Plugin.
type PluginStatus struct {
	ExternalRefStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:validation:XValidation:rule="has(self.spec.refresh) && has(self.spec.refresh.maxStaleness)",message="Plugin.spec.refresh.maxStaleness is REQUIRED (CRD-04)"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.refresh.interval) || duration(self.spec.refresh.interval) <= duration(self.spec.refresh.maxStaleness)",message="Plugin.spec.refresh.interval must be <= refresh.maxStaleness (CRD-03)"
// +kubebuilder:validation:XValidation:rule="(self.spec.type == 'github' && has(self.spec.github)) || (self.spec.type == 'gitlab' && has(self.spec.gitlab)) || (self.spec.type == 'bitbucket' && has(self.spec.bitbucket)) || (self.spec.type == 's3' && has(self.spec.s3)) || (self.spec.type == 'gcs' && has(self.spec.gcs)) || (self.spec.type == 'http' && has(self.spec.http))",message="Plugin.spec must include the subobject matching spec.type (CRD-03)"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Reachable",type=string,JSONPath=".status.conditions[?(@.type=='SourceReachable')].status"
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Plugin is the Schema for the plugins API (Hub §11).
type Plugin struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PluginSpec   `json:"spec,omitempty"`
	Status PluginStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PluginList contains a list of Plugin.
type PluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Plugin `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Plugin{}, &PluginList{})
}
