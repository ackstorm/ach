// SPDX-License-Identifier: Apache-2.0

package teams

// HasIntersect reports whether the two string slices share at least one
// element. O(len(a)+len(b)) via a hash-set on the first slice. An empty
// slice on either side short-circuits to false. Shared by the env-access
// authorizedTeams ∩ callerTeams check across the hydrate, environments,
// envkeys, and store packages.
func HasIntersect(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; ok {
			return true
		}
	}
	return false
}
