// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"slices"
	"strings"
)

// ssoAdminKind marks a user who is in one of SSOAdminGroups, keyed by the
// lower-cased email. It is written at login and rewritten at every IdP
// revalidation, so a user removed from the group loses admin at their next
// refresh.
const ssoAdminKind = "sso_admin"

// recordSSOAdmin writes or clears the user's SSO admin record from the
// `groups` claim Dex just returned. Groups Dex could not deliver (an unusable
// refreshed id_token) arrive empty and clear the record: admin is lost until
// the next refresh that delivers them.
func (d OAuthDeps) recordSSOAdmin(ctx context.Context, email string, groups []string) error {
	if len(d.SSOAdminGroups) == 0 {
		return nil
	}
	if slices.ContainsFunc(groups, func(g string) bool { return slices.Contains(d.SSOAdminGroups, strings.ToLower(g)) }) {
		return d.Store.Put(ctx, ssoAdminKind, email, true, d.RefreshTTL)
	}
	return d.Store.Del(ctx, ssoAdminKind, email)
}

// SSOAdmin reports whether the user is an admin through an SSO group.
func (d OAuthDeps) SSOAdmin(ctx context.Context, email string) (bool, error) {
	var admin bool
	ok, err := d.Store.Get(ctx, ssoAdminKind, email, &admin)
	return ok && admin, err
}
