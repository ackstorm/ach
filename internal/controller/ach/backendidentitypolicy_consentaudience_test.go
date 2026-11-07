// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

// Admission refuses a consentAudience that ACH itself accepts as a token
// audience, and admits an ordinary one.
func TestBIPConsentAudience_Admission(t *testing.T) {
	ctx := context.Background()
	for i, tc := range []struct {
		aud     string
		refused bool
	}{
		{"ach", true}, {"ach-ek", true}, {"mcp:echo", true}, {"a2a:agent", true},
		{"gitlab", false}, {"ach-memory", false},
	} {
		cr := &achv1alpha1.BackendIdentityPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "bip-consent-aud-" + string(rune('a'+i)), Namespace: WatchNamespace},
			Spec: achv1alpha1.BackendIdentityPolicySpec{
				Target:             achv1alpha1.BackendTargetRef{Kind: "MCPServer", Name: "consent-aud-" + string(rune('a'+i))},
				ForwardIdentityJWT: true,
				ConsentBroker:      "https://broker.example.com",
				ConsentAudience:    tc.aud,
			},
		}
		err := k8sClient.Create(ctx, cr)
		if tc.refused {
			if err == nil || !strings.Contains(err.Error(), "consentAudience must not be") {
				t.Errorf("aud %q: err = %v, want refusal", tc.aud, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("aud %q refused: %v", tc.aud, err)
			continue
		}
		c := cr
		t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), c) })
	}
}
