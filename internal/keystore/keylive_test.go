// SPDX-License-Identifier: Apache-2.0

package keystore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keys"
)

func TestKeyLive(t *testing.T) {
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	boom := errors.New("connection refused")

	tests := []struct {
		name     string
		keyType  keys.BearerPrefix
		pk       *db.PkKeyInfo
		ek       *db.EkKeyInfo
		err      error
		wantLive bool
		wantErr  error
	}{
		{name: "active pk", keyType: keys.PrefixPk, pk: &db.PkKeyInfo{Status: "active", ExpiresAt: future}, wantLive: true},
		{name: "revoked pk", keyType: keys.PrefixPk, pk: &db.PkKeyInfo{Status: "revoked", ExpiresAt: future}},
		{name: "pk past its window", keyType: keys.PrefixPk, pk: &db.PkKeyInfo{Status: "active", ExpiresAt: past}},
		{name: "pk row gone", keyType: keys.PrefixPk},
		{name: "pk read fails", keyType: keys.PrefixPk, err: boom, wantErr: boom},
		{name: "perpetual ek", keyType: keys.PrefixEk, ek: &db.EkKeyInfo{Status: "active"}, wantLive: true},
		{name: "dated ek", keyType: keys.PrefixEk, ek: &db.EkKeyInfo{Status: "active", ExpiresAt: &future}, wantLive: true},
		{name: "suspended ek", keyType: keys.PrefixEk, ek: &db.EkKeyInfo{Status: "suspended"}},
		{name: "revoked ek", keyType: keys.PrefixEk, ek: &db.EkKeyInfo{Status: "revoked"}},
		{name: "ek past expires_at", keyType: keys.PrefixEk, ek: &db.EkKeyInfo{Status: "active", ExpiresAt: &past}},
		{name: "ek row gone", keyType: keys.PrefixEk},
		{name: "ek read fails", keyType: keys.PrefixEk, err: boom, wantErr: boom},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			live := keyLive(
				func(_ context.Context, id string) (*db.PkKeyInfo, error) {
					if id != "kid" {
						t.Errorf("pk read id = %q", id)
					}
					return tc.pk, tc.err
				},
				func(_ context.Context, id string) (*db.EkKeyInfo, error) {
					if id != "kid" {
						t.Errorf("ek read id = %q", id)
					}
					return tc.ek, tc.err
				})
			got, err := live(context.Background(), tc.keyType, "kid")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.wantLive {
				t.Fatalf("live = %v, want %v", got, tc.wantLive)
			}
		})
	}
}
