// SPDX-License-Identifier: Apache-2.0

package agentrender

import (
	"fmt"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

func egressSecretEnvName(i int) string { return fmt.Sprintf("ACH_SECRET_EGRESS_%d", i) }

// EgressSecretEnv lists the harness-only env bindings for spec.egress, in service order.
// The harness validates the block (origins, placeholders, forwardEnv collisions) at load;
// the operator deliberately mirrors none of that.
func EgressSecretEnv(a achv1alpha1.ACHAgent) []ChannelSecretEnvRef {
	if a.Spec.Egress == nil {
		return nil
	}
	out := make([]ChannelSecretEnvRef, 0, len(a.Spec.Egress.Services))
	for i, s := range a.Spec.Egress.Services {
		out = append(out, ChannelSecretEnvRef{EnvName: egressSecretEnvName(i), SecretName: s.Auth.SecretKeyRef.Name, Key: s.Auth.SecretKeyRef.Key})
	}
	return out
}

func renderEgress(e *achv1alpha1.EgressSpec) *EgressBlock {
	if e == nil {
		return nil
	}
	out := &EgressBlock{}
	for i, s := range e.Services {
		out.Services = append(out.Services, EgressServiceBlock{
			Name:   s.Name,
			Origin: s.Origin,
			Auth: EgressAuthBlock{
				Header:         s.Auth.Header,
				Prefix:         s.Auth.Prefix,
				Secret:         SecretSourceBlock{Env: egressSecretEnvName(i)},
				PlaceholderEnv: s.Auth.PlaceholderEnv,
			},
		})
	}
	return out
}
