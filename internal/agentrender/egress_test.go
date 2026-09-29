// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"reflect"
	"testing"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func egressAgent() achv1alpha1.ACHAgent {
	return achv1alpha1.ACHAgent{Spec: achv1alpha1.ACHAgentSpec{Egress: &achv1alpha1.EgressSpec{Services: []achv1alpha1.EgressService{
		{Name: "github", Origin: "https://api.github.com", Auth: achv1alpha1.EgressAuth{
			Header: "Authorization", Prefix: "Bearer ", PlaceholderEnv: "GH_TOKEN",
			SecretKeyRef: achv1alpha1.SecretKeyRef{Name: "gh", Key: "token"}}},
		{Name: "gitlab", Origin: "https://gitlab.example.com", Auth: achv1alpha1.EgressAuth{
			Header: "PRIVATE-TOKEN", SecretKeyRef: achv1alpha1.SecretKeyRef{Name: "gl", Key: "pat"}}},
	}}}}
}

func TestRenderEgress_BlockAndEnvNames(t *testing.T) {
	a := egressAgent()
	got := renderEgress(a.Spec.Egress)
	want := &EgressBlock{Services: []EgressServiceBlock{
		{Name: "github", Origin: "https://api.github.com", Auth: EgressAuthBlock{
			Header: "Authorization", Prefix: "Bearer ", PlaceholderEnv: "GH_TOKEN", Secret: SecretSourceBlock{Env: "ACH_SECRET_EGRESS_0"}}},
		{Name: "gitlab", Origin: "https://gitlab.example.com", Auth: EgressAuthBlock{
			Header: "PRIVATE-TOKEN", Secret: SecretSourceBlock{Env: "ACH_SECRET_EGRESS_1"}}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("block = %+v, want %+v", got, want)
	}
	refs := EgressSecretEnv(a)
	if len(refs) != 2 || refs[0] != (ChannelSecretEnvRef{EnvName: "ACH_SECRET_EGRESS_0", SecretName: "gh", Key: "token"}) ||
		refs[1] != (ChannelSecretEnvRef{EnvName: "ACH_SECRET_EGRESS_1", SecretName: "gl", Key: "pat"}) {
		t.Errorf("refs = %+v", refs)
	}
}

func TestRenderEgress_AbsentIsNil(t *testing.T) {
	if renderEgress(nil) != nil || EgressSecretEnv(achv1alpha1.ACHAgent{}) != nil {
		t.Error("no spec.egress must render nothing")
	}
}

func TestReferencedSecrets_IncludesEgress(t *testing.T) {
	got := ReferencedSecrets(achv1alpha1.AgentProfile{}, egressAgent())
	if !reflect.DeepEqual(got, map[string][]string{"gh": {"token"}, "gl": {"pat"}}) {
		t.Errorf("ReferencedSecrets = %v", got)
	}
}
