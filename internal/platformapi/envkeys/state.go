// SPDX-License-Identifier: Apache-2.0

package envkeys

import "github.com/ackstorm/ach/internal/db"

// accessVerdict is the owner's current Environment access (D-30) as the
// list derives it: one TeamsResolver lookup per owner, one Environment
// row per distinct environment, never persisted.
type accessVerdict int

const (
	accessGranted accessVerdict = iota
	accessLost
	accessUnverified // LiteLLM unreachable: not Invalid (§7.2), just unverified
)

// EffectiveState folds the persisted status (ListKeys already says
// 'expired' when the clock does) and the access verdict into the visible
// state, by priority Revoked → Expired → Suspended → Invalid → Active
// (§7.1), plus the secondary facts as reasons.
func EffectiveState(it db.KeyListItem, access accessVerdict) (string, []string) {
	if it.Type != "ek" {
		return it.Status, nil
	}
	var reasons []string
	switch it.Status {
	case statusRevoked:
		return statusRevoked, nil
	case statusExpired:
		reasons = append(reasons, statusExpired)
	case statusSuspended:
		reasons = append(reasons, statusSuspended)
	}
	switch access {
	case accessLost:
		reasons = append(reasons, "no_access")
	case accessUnverified:
		reasons = append(reasons, "access_unverified")
	}
	if it.Status == statusActive && access == accessLost {
		return statusInvalid, reasons
	}
	return it.Status, reasons
}
