// SPDX-License-Identifier: Apache-2.0

package v1alpha1

// GetExternalRefStatus returns a pointer to the embedded ExternalRefStatus
// so generic reconcile machinery can read/write the shared status surface
// (conditions, storageLocation, upstreamRev, lastSuccessfulRefresh,
// observedGeneration) without per-CR-type field access. Plugin, Prompt,
// and Artifact statuses are exactly one inlined ExternalRefStatus, so the
// returned pointer aliases the whole Status.

func (p *Plugin) GetExternalRefStatus() *ExternalRefStatus {
	return &p.Status.ExternalRefStatus
}

func (p *Prompt) GetExternalRefStatus() *ExternalRefStatus {
	return &p.Status.ExternalRefStatus
}

func (a *Artifact) GetExternalRefStatus() *ExternalRefStatus {
	return &a.Status.ExternalRefStatus
}
