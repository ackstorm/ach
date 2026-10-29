// SPDX-License-Identifier: Apache-2.0

package connection

import "github.com/ackstorm/ach/internal/litellm"

// Snapshot is the immutable value published by LiteLLMConnection reconciliation.
type Snapshot struct {
	Ready      bool
	Reason     string
	Client     litellm.Client
	Generation int64
}
