// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/oauthlogin"
	"github.com/ackstorm/ach/internal/keys"
)

// profileBearer returns the profile's own credential: the pk_ from a
// device-code login, or a current OAuth access token (refreshed — and the
// rotated pair persisted to path — when it is near expiry). "" when the
// profile has neither. Every command that falls back to "the profile's
// key" routes through here so an OAuth profile works everywhere a pk_ does.
func profileBearer(ctx context.Context, file *config.File, path string, dep *config.Profile) (string, error) {
	if dep.OAuth == nil {
		return dep.PK, nil
	}
	tok, updated, err := (&oauthlogin.Client{BaseURL: dep.URL}).CurrentAccessToken(ctx, dep.OAuth)
	if err != nil {
		return "", &exit.CodedError{
			Code: exit.General, Msg: fmt.Sprintf("oauth session: %v; run `ach-cli login`", err), Wrapped: err,
		}
	}
	if updated != nil {
		dep.OAuth = updated
		if err := config.Save(path, file); err != nil {
			return "", &exit.CodedError{Code: exit.ConfigFile, Msg: err.Error(), Wrapped: err}
		}
	}
	return tok, nil
}

// classifyBearer is keys.ClassifyBearer with an OAuth access token (a JWS)
// reading as a pk_: it is the user's personal credential on every path
// (x-ach-environment on content GETs, the --environment gate, whoami's
// /platform/whoami branch).
func classifyBearer(bearer string) (keys.BearerPrefix, error) {
	if keys.LooksLikeJWS(bearer) {
		return keys.PrefixPk, nil
	}
	return keys.ClassifyBearer(bearer)
}
