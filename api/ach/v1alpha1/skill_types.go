// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SkillSpec defines the desired state of Skill.
//
// A Skill references an upstream location whose subtree contains an agent
// skill directory (a root directory with a SKILL.md manifest plus optional
// scripts/, references/, and assets/ subdirectories; see
// https://agentskills.io/specification). ACH fetches the subtree and serves
// it as a .tar.gz archive. Unlike Plugin, no component filter is applied —
// the fetched skill tree is served verbatim.
//
// CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
// CRD-04: spec.refresh.maxStaleness is REQUIRED; spec.refresh.interval,
// when set, MUST NOT exceed spec.refresh.maxStaleness.
type SkillSpec struct {
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

// SkillStatus defines the observed state of Skill.
type SkillStatus struct {
	ExternalRefStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:validation:XValidation:rule="has(self.spec.refresh) && has(self.spec.refresh.maxStaleness)",message="Skill.spec.refresh.maxStaleness is REQUIRED (CRD-04)"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.refresh.interval) || duration(self.spec.refresh.interval) <= duration(self.spec.refresh.maxStaleness)",message="Skill.spec.refresh.interval must be <= refresh.maxStaleness (CRD-03)"
// +kubebuilder:validation:XValidation:rule="(self.spec.type == 'github' && has(self.spec.github)) || (self.spec.type == 'gitlab' && has(self.spec.gitlab)) || (self.spec.type == 'bitbucket' && has(self.spec.bitbucket)) || (self.spec.type == 's3' && has(self.spec.s3)) || (self.spec.type == 'gcs' && has(self.spec.gcs)) || (self.spec.type == 'http' && has(self.spec.http))",message="Skill.spec must include the subobject matching spec.type (CRD-03)"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Reachable",type=string,JSONPath=".status.conditions[?(@.type=='SourceReachable')].status"
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Skill is the Schema for the skills API.
type Skill struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SkillSpec   `json:"spec,omitempty"`
	Status SkillStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SkillList contains a list of Skill.
type SkillList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Skill `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Skill{}, &SkillList{})
}
