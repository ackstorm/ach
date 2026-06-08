// SPDX-License-Identifier: Apache-2.0

package contentkit

import "fmt"

// OversizeError is returned when a fetched object exceeds the configured
// size cap. The caller maps it to a PluginTooLarge status condition.
// Shared across plugin / skill / marketplace, so the message stays kind-neutral
// (the cap derives from ACH_PLUGIN_MAX_SIZE_MIB for plugins/marketplaces and
// ACH_SKILL_MAX_SIZE_MIB for skills). The two byte counts are the observed
// staging length (always > Cap by definition) and the cap itself in bytes.
type OversizeError struct {
	Bytes int64
	Cap   int64
}

// Error formats the human-readable status.message. Both numbers are byte
// counts; the message MUST NOT echo Secret values or auth header contents
// (threat T-02-05-04).
func (e *OversizeError) Error() string {
	return fmt.Sprintf("staged %d bytes exceeds the configured size cap of %d bytes", e.Bytes, e.Cap)
}
