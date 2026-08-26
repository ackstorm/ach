// SPDX-License-Identifier: Apache-2.0

package proxy

import "testing"

// G1: shell teams are ACH-internal plumbing and never reach a backend.
func TestFilterShellTeams_DropsShellPrefixes(t *testing.T) {
	got := filterShellTeams([]string{
		"team-a",
		"ach-env-demo",
		"ach-user-alice@example.com",
		"team-b",
	})
	if len(got) != 2 || got[0] != "team-a" || got[1] != "team-b" {
		t.Fatalf("got %v; want [team-a team-b]", got)
	}
}

// G2: the result is deduplicated and sorted so the claim is deterministic
// for a given input set (EnvProvider.List iterates a map).
func TestFilterShellTeams_DedupesAndSorts(t *testing.T) {
	got := filterShellTeams([]string{"team-b", "team-a", "team-b", "team-a"})
	if len(got) != 2 || got[0] != "team-a" || got[1] != "team-b" {
		t.Fatalf("got %v; want [team-a team-b]", got)
	}
}

// G3: nothing surviving yields nil, not an empty slice — the signer omits
// the claim on len == 0 and an absent claim is what backends expect.
func TestFilterShellTeams_EmptyResultIsNil(t *testing.T) {
	if got := filterShellTeams([]string{"ach-env-demo"}); got != nil {
		t.Fatalf("got %v; want nil", got)
	}
	if got := filterShellTeams(nil); got != nil {
		t.Fatalf("got %v; want nil", got)
	}
}
