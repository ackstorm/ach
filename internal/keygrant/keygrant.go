// SPDX-License-Identifier: Apache-2.0

// Package keygrant is the one rule deciding whether KeyGrants allow an
// exchange tuple (identity, Environment, "<namespace>/<serviceaccount>"): a
// single grant must name all three; lists from different grants are never
// combined. The exchange handler, the exchange-JWT resolver and the operator's
// cleanup sweep all decide through it.
package keygrant

import (
	"slices"

	"github.com/ackstorm/ach/internal/db"
)

// Covering returns the grants that allow the tuple.
func Covering(grants []db.KeyGrantRow, identity, env, requester string) []db.KeyGrantRow {
	var out []db.KeyGrantRow
	for _, g := range grants {
		if g.Identity == identity && slices.Contains(g.Environments, env) && slices.Contains(g.ServiceAccounts, requester) {
			out = append(out, g)
		}
	}
	return out
}

// Miss names, for the audit record only, why grants already filtered to one
// identity allow nothing for env. The HTTP answer is always not_granted.
func Miss(grants []db.KeyGrantRow, env string) string {
	if len(grants) == 0 {
		return "no_grant_for_identity"
	}
	for _, g := range grants {
		if slices.Contains(g.Environments, env) {
			return "requester_not_granted"
		}
	}
	return "environment_not_granted"
}

// Plan returns the non-revoked exchange keys no grant allows any more.
func Plan(grants []db.KeyGrantRow, keys []db.EkKeyInfo) (revoke []db.EkKeyInfo) {
	for _, k := range keys {
		if len(Covering(grants, k.OwnerEmail, k.Environment, k.Requester)) == 0 {
			revoke = append(revoke, k)
		}
	}
	return revoke
}
