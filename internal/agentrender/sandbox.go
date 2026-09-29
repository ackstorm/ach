// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// Fixed sandboxed-placement contract values (ach-agent "Contract for ach"). They equal the
// harness defaults, so the config.json sandbox block omits them.
const (
	SandboxGatewayPort = int32(8095)
	SandboxEgressPort  = int32(8096)
	SandboxEnginePort  = int32(8082)
	SandboxHealthPort  = int32(8081)
	SandboxHome        = "/home/agent"
	// SandboxKeyEnv is the harness env var carrying K. Harness only, never the sandbox.
	SandboxKeyEnv = "ACH_SANDBOX_KEY"
)

// SandboxBlock mirrors config.json `sandbox`. Fields left at the harness default are omitted.
type SandboxBlock struct {
	Enabled     bool                 `json:"enabled"`
	WarmPool    string               `json:"warmPool"`
	GatewayHost string               `json:"gatewayHost"`
	IdleSeconds *int64               `json:"idleSeconds,omitempty"`
	Sessions    SandboxSessionsBlock `json:"sessions"`
}

type SandboxSessionsBlock struct {
	Bucket          string `json:"bucket"`
	MaxArchiveBytes *int64 `json:"maxArchiveBytes,omitempty"`
}

// SandboxName is the SandboxTemplate + SandboxWarmPool name; the harness Service is
// achagent-<name> too, so gatewayHost and warmPool derive from one place.
func SandboxName(agentName string) string { return "achagent-" + agentName }

// SandboxedPlacement reports whether the agent runs sandboxed.
func SandboxedPlacement(p achv1alpha1.AgentProfile, a achv1alpha1.ACHAgent) bool {
	return ResolvePlacement(a.Spec.Placement, p.Spec.Achagent.Placement) == achv1alpha1.PlacementSandboxed
}

func renderSandbox(p achv1alpha1.AgentProfile, a achv1alpha1.ACHAgent) (*SandboxBlock, error) {
	if !SandboxedPlacement(p, a) {
		return nil, nil
	}
	sb := p.Spec.Sandbox
	if sb == nil {
		return nil, fmt.Errorf("placement sandboxed requires the profile's spec.sandbox")
	}
	return &SandboxBlock{
		Enabled:     true,
		WarmPool:    SandboxName(a.Name),
		GatewayHost: fmt.Sprintf("%s.%s.svc", SandboxName(a.Name), a.Namespace),
		IdleSeconds: sb.IdleSeconds,
		Sessions:    SandboxSessionsBlock{Bucket: sb.Sessions.Bucket, MaxArchiveBytes: sb.Sessions.MaxArchiveBytes},
	}, nil
}

// SandboxVerifyKey derives the PUBLIC Ed25519 key the sandbox verifies harness bearers with:
// base64url_nopad(ed25519.NewKeyFromSeed(HMAC_SHA256(key=K, "ach-sandbox-engine")).Public()).
// K is the Secret's string value used as raw HMAC key bytes. Not a secret.
func SandboxVerifyKey(k string) string {
	mac := hmac.New(sha256.New, []byte(k))
	mac.Write([]byte("ach-sandbox-engine"))
	pub := ed25519.NewKeyFromSeed(mac.Sum(nil)).Public().(ed25519.PublicKey)
	return base64.RawURLEncoding.EncodeToString(pub)
}
