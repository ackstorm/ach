// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func sandboxedPair() (achv1alpha1.AgentProfile, achv1alpha1.ACHAgent) {
	idle := int64(600)
	p := achv1alpha1.AgentProfile{Spec: achv1alpha1.AgentProfileSpec{
		Achagent:    achv1alpha1.AgentDefaults{Image: "img", Ach: &achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"}, Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"}},
		Persistence: &achv1alpha1.PersistenceSpec{Enabled: true},
		Sandbox:     &achv1alpha1.SandboxSpec{IdleSeconds: &idle, Sessions: achv1alpha1.SandboxSessionsSpec{Bucket: "b"}},
	}}
	a := achv1alpha1.ACHAgent{ObjectMeta: metav1.ObjectMeta{Name: "bot", Namespace: "ach"}, Spec: achv1alpha1.ACHAgentSpec{
		ProfileRef:    achv1alpha1.LocalObjectRef{Name: "p"},
		Identity:      achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "ek", Key: "ek"}},
		AgentDefaults: achv1alpha1.AgentDefaults{Placement: achv1alpha1.PlacementSandboxed},
	}}
	return p, a
}

func TestRenderSandbox_Block(t *testing.T) {
	p, a := sandboxedPair()
	cfg, err := Render(p, a, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg.Sandbox)
	want := `{"enabled":true,"warmPool":"achagent-bot","gatewayHost":"achagent-bot.ach.svc","idleSeconds":600,"sessions":{"bucket":"b"}}`
	if string(b) != want {
		t.Errorf("sandbox = %s, want %s", b, want)
	}
}

func TestRenderSandbox_StandaloneHasNoBlock(t *testing.T) {
	p, a := sandboxedPair()
	a.Spec.Placement = ""
	cfg, err := Render(p, a, "")
	if err != nil || cfg.Sandbox != nil {
		t.Errorf("standalone: sandbox=%v err=%v", cfg.Sandbox, err)
	}
}

func TestRenderSandbox_RequiresProfileBlock(t *testing.T) {
	p, a := sandboxedPair()
	p.Spec.Sandbox = nil
	if _, err := Render(p, a, ""); err == nil || !strings.Contains(err.Error(), "spec.sandbox") {
		t.Errorf("missing sandbox block: err = %v", err)
	}
}

// Cross-repo vector from ach-agent's "Contract for ach" (Python and Go must agree).
func TestSandboxVerifyKey_Vector(t *testing.T) {
	if got := SandboxVerifyKey(strings.Repeat("0", 64)); got != "lZLI1hcEx9ydNM7EoaQ213ri9oNsmILvrFE6AB2YO94" {
		t.Errorf("verify key = %s", got)
	}
}
