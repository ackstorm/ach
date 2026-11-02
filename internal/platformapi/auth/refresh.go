// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"net/http"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/keys"
	pamw "github.com/ackstorm/ach/internal/platformapi/middleware"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// Refresh is POST /platform/refresh (`ach-cli refresh`): re-read the
// caller's identity at the IdP and apply the login's team enrolment
// (platformApi.teams users + sso) without a browser. It replays the
// user's shared Dex refresh token, so it also revalidates the session
// exactly like a /token refresh. Teams are additive and best-effort, as at
// login; default teams are left to the login. Enrolment runs only on
// demand: the /token refresh and console revalidation never enrol, so the
// LiteLLM team lookups stay off the hourly path.
//
// pk_ callers only (an ek_ has no IdP session): 401 invalid_key_type.
// IdP refused: 401 ach_login_required. Dex unreachable: 503
// idp_unreachable. 204 otherwise.
func (d OAuthDeps) Refresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := pamw.RequestIDFromCtx(ctx)
	kc, _ := pamw.KeyContextFromCtx(ctx)
	if kc.KeyType != keys.PrefixPk || kc.LiteLLMUserID == nil {
		render.Error(w, http.StatusUnauthorized, audit.OutcomeInvalidKeyType, "refresh requires a personal identity", reqID)
		return
	}
	groups, err := d.revalidateAtIdP(ctx, kc.OwnerEmail)
	switch {
	case errors.Is(err, errIdPRefused):
		render.Error(w, http.StatusUnauthorized, "ach_login_required", "the identity provider no longer honours this session; run ach-cli login", reqID)
		return
	case errors.Is(err, ErrIdPUnreachable):
		render.Error(w, http.StatusServiceUnavailable, "idp_unreachable", "identity provider unreachable", reqID)
		return
	case err != nil:
		d.Auth.Logger.Error("refresh: revalidation failed", "err", err)
		render.Error(w, http.StatusInternalServerError, audit.OutcomeInternalError, "", reqID)
		return
	}
	enrollExtraTeams(ctx, d.Auth, *kc.LiteLLMUserID, kc.OwnerEmail, groups)
	w.WriteHeader(http.StatusNoContent)
}
