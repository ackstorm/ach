// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ArtifactSpec defines the desired state of Artifact (Hub §13).
//
// CRD-05: spec.scope is REQUIRED and is one of {object, directory}. For
// type=http only scope=object is permitted; CRD admission rejects
// directory scope on http sources.
// CRD-04: spec.refresh.maxStaleness is REQUIRED.
// CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
type ArtifactSpec struct {
	// Type names the upstream source kind.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=github;gitlab;bitbucket;s3;gcs;http
	Type string `json:"type"`

	// Scope picks single-object vs directory-bundle delivery (CRD-05).
	// object: serves exactly one upstream object verbatim.
	// directory: materializes the directory tree into a .tar.gz at refresh time.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=object;directory
	Scope string `json:"scope"`

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

// ArtifactStatus defines the observed state of Artifact.
type ArtifactStatus struct {
	ExternalRefStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:validation:XValidation:rule="has(self.spec.refresh) && has(self.spec.refresh.maxStaleness)",message="Artifact.spec.refresh.maxStaleness is REQUIRED (CRD-04)"
// +kubebuilder:validation:XValidation:rule="self.spec.type != 'http' || self.spec.scope == 'object'",message="Artifact.type=http requires scope=object; directory scope is not permitted for http (CRD-05)"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.refresh.interval) || duration(self.spec.refresh.interval) <= duration(self.spec.refresh.maxStaleness)",message="Artifact.spec.refresh.interval must be <= refresh.maxStaleness (CRD-03)"
// +kubebuilder:validation:XValidation:rule="(self.spec.type == 'github' && has(self.spec.github)) || (self.spec.type == 'gitlab' && has(self.spec.gitlab)) || (self.spec.type == 'bitbucket' && has(self.spec.bitbucket)) || (self.spec.type == 's3' && has(self.spec.s3)) || (self.spec.type == 'gcs' && has(self.spec.gcs)) || (self.spec.type == 'http' && has(self.spec.http))",message="Artifact.spec must include the subobject matching spec.type (CRD-03)"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Scope",type=string,JSONPath=".spec.scope"
// +kubebuilder:printcolumn:name="SourceReachable",type=string,JSONPath=".status.conditions[?(@.type=='SourceReachable')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Artifact is the Schema for the artifacts API (Hub §13).
type Artifact struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArtifactSpec   `json:"spec,omitempty"`
	Status ArtifactStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ArtifactList contains a list of Artifact.
type ArtifactList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Artifact `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Artifact{}, &ArtifactList{})
}
