// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/ackstorm/ach/internal/config"
)

const (
	profileFull     = "full"
	profileIdentity = "identity"
)

// profileFromEnv reads ACH_PROFILE: "full" (operator + governance, the
// default) or "identity" (platform-api + forwarder in front of one LiteLLM
// host, no CRDs, no Environments, no BIP).
func profileFromEnv() (string, error) {
	switch p := config.EnvOr("ACH_PROFILE", profileFull); p {
	case profileFull, profileIdentity:
		return p, nil
	default:
		return "", fmt.Errorf("ACH_PROFILE must be full or identity (got %q)", p)
	}
}
