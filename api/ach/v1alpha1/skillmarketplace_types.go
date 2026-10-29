// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SkillMarketplaceSpec defines the desired state of SkillMarketplace.
//
// A SkillMarketplace fetches ONE upstream repo subtree as a single tar.gz and
// discovers many agent skills inside it by convention (agentskills.io has NO
// marketplace.json index — unlike PluginMarketplace). Body handling depends on
// the source type:
//
//   - github / gitlab / bitbucket: the fetcher returns the repo archive
//     (a tar.gz of the repo subtree). Stage-1 walks it for every top-level
//     directory containing a valid SKILL.md (name == dir basename); the REST
//     "<repo>-<sha>/" archive-root wrapper is stripped automatically.
//   - s3 / gcs / http: spec.<type>.key/object/url MUST point at a pre-archived
//     `.tar.gz` body directly; these fetchers do NOT walk directories. Stage-1
//     validates the fetched body is gzip (malformed → Synced=False,
//     reason=UpstreamInvalid).
//
// CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
// CRD-04: spec.refresh.maxStaleness is REQUIRED.
type SkillMarketplaceSpec struct {
	// Type names the upstream source kind for the marketplace archive.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=github;gitlab;bitbucket;s3;gcs;http
	Type string `json:"type"`

	// Refresh declares poll cadence and staleness bound (CRD-04).
	//
	// +kubebuilder:validation:Required
	Refresh RefreshBlock `json:"refresh"`

	// Filters narrows the discovered skill set via anchored RE2 patterns.
	// Optional.
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

// SkillMarketplaceSkillRef is the per-skill entry surfaced on
// SkillMarketplace status — operators reading the CR need at-a-glance
// visibility into which skill names the most recent reconcile materialized AND
// the upstream revision they pin against.
type SkillMarketplaceSkillRef struct {
	// Name is the skill's identifier within the collection
	// (the SKILL.md frontmatter name == the top-level directory basename).
	Name string `json:"name"`

	// UpstreamRev is the resolved revision the materialized tarball
	// was fetched at — a 40-hex commit SHA for git-backed sources, an
	// S3 ETag for S3, a generation for GCS, an ETag|Last-Modified
	// composite for HTTP. Empty only when the upstream fetcher did not
	// report a revision.
	//
	// +optional
	UpstreamRev string `json:"upstreamRev,omitempty"`
}

// SkillMarketplaceStatus defines the observed state of SkillMarketplace.
//
// In addition to the shared ExternalRefStatus, SkillMarketplace exposes a
// Synced condition with reasons UpstreamInvalid, InvalidConfig (plus per-skill
// soft-skip reasons in the message), plus the materialized skill set
// (Skills / SkillsCount) populated on each successful reconcile.
type SkillMarketplaceStatus struct {
	ExternalRefStatus `json:",inline"`

	// Skills lists the entries in the upstream collection that the most
	// recent reconcile successfully materialized into skill_marketplace_skills
	// (+ the per-marketplace cache). Ordered by Name. Entries that failed
	// Stage-2 are NOT included here — those surface in the Synced condition's
	// message field. Empty before the first successful reconcile.
	//
	// +optional
	// +listType=map
	// +listMapKey=name
	Skills []SkillMarketplaceSkillRef `json:"skills,omitempty"`

	// SkillsCount is the size of Skills, denormalized so the kubectl print
	// column can show it without a JSONPath length() expression. Equal to
	// len(Skills).
	//
	// +optional
	SkillsCount int `json:"skillsCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:validation:XValidation:rule="has(self.spec.refresh) && has(self.spec.refresh.maxStaleness)",message="SkillMarketplace.spec.refresh.maxStaleness is REQUIRED (CRD-04)"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.refresh.interval) || duration(self.spec.refresh.interval) <= duration(self.spec.refresh.maxStaleness)",message="SkillMarketplace.spec.refresh.interval must be <= refresh.maxStaleness (CRD-03)"
// +kubebuilder:validation:XValidation:rule="(self.spec.type == 'github' && has(self.spec.github)) || (self.spec.type == 'gitlab' && has(self.spec.gitlab)) || (self.spec.type == 'bitbucket' && has(self.spec.bitbucket)) || (self.spec.type == 's3' && has(self.spec.s3)) || (self.spec.type == 'gcs' && has(self.spec.gcs)) || (self.spec.type == 'http' && has(self.spec.http))",message="SkillMarketplace.spec must include the subobject matching spec.type (CRD-03)"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Reachable",type=string,JSONPath=".status.conditions[?(@.type=='SourceReachable')].status"
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="Skills",type=integer,JSONPath=".status.skillsCount"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// SkillMarketplace is the Schema for the skillmarketplaces API. One upstream
// repo of agent skills (discovered by convention) → many skills.
type SkillMarketplace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SkillMarketplaceSpec   `json:"spec,omitempty"`
	Status SkillMarketplaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SkillMarketplaceList contains a list of SkillMarketplace.
type SkillMarketplaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SkillMarketplace `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SkillMarketplace{}, &SkillMarketplaceList{})
}
