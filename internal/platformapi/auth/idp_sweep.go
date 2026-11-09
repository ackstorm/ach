// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// An ek_ never reaches Dex on its own, so a user the IdP dropped would keep
// their keys for as long as they never log in again. The sweep replays the
// Dex token of every owner whose ek_ was used within idpSweepWindow, at most
// once per idpCheckEvery across all replicas; a refusal is recorded by
// revalidateAtIdP. The replay also keeps the owner's Dex token from idling
// out (Dex validIfNotUsedFor).
const (
	idpSweepWindow = 7 * 24 * time.Hour
	idpCheckEvery  = 24 * time.Hour
	idpCheckKind   = "idp_check"
)

// RunIdPSweep sweeps now and then every tick until ctx ends.
func (d OAuthDeps) RunIdPSweep(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		d.sweepIdP(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (d OAuthDeps) sweepIdP(ctx context.Context) {
	owners, err := d.ekOwnersInUse(ctx)
	if err != nil {
		d.Auth.Logger.Warn("oauth: idp sweep: listing ek_ owners failed", "err", err)
		return
	}
	for _, email := range owners {
		claimed, err := d.Store.Claim(ctx, idpCheckKind, email, idpCheckEvery)
		if err != nil {
			d.Auth.Logger.Warn("oauth: idp sweep: claim failed", "owner", email, "err", err)
			return
		}
		if !claimed {
			continue
		}
		var rt string
		ok, err := d.Store.Get(ctx, dexRefreshKind, email, &rt)
		if err != nil {
			d.Auth.Logger.Warn("oauth: idp sweep: reading the Dex refresh token failed", "owner", email, "err", err)
			d.releaseIdPCheck(ctx, email)
			continue
		}
		if !ok {
			d.Auth.Logger.Info("oauth: idp sweep: no Dex refresh token stored; owner not verifiable at the IdP", "owner", email)
			continue
		}
		if _, err := d.revalidateAtIdP(ctx, email); err != nil && !errors.Is(err, errIdPRefused) {
			d.Auth.Logger.Warn("oauth: idp sweep: owner not verifiable at the IdP", "owner", email, "err", err)
			if errors.Is(err, ErrIdPUnreachable) {
				d.releaseIdPCheck(ctx, email)
			}
		}
	}
}

// releaseIdPCheck drops the owner's claim so the next sweep checks again.
func (d OAuthDeps) releaseIdPCheck(ctx context.Context, email string) {
	if err := d.Store.Del(ctx, idpCheckKind, email); err != nil {
		d.Auth.Logger.Warn("oauth: idp sweep: releasing the claim failed", "owner", email, "err", err)
	}
}

func (d OAuthDeps) ekOwnersInUse(ctx context.Context) ([]string, error) {
	if d.EkOwnersInUse != nil {
		return d.EkOwnersInUse(ctx)
	}
	return db.EkOwnersInUse(ctx, d.Auth.Pool, idpSweepWindow)
}
