// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/db"
)

func TestMigrateErrorsNeverCarryDSNOrPassword(t *testing.T) {
	for _, pw := range []string{"p%zzw", "pa/ss", "pa?ss", "pa[ss", "pa#ss", "plainpw"} {
		for _, dsn := range []string{
			"postgres://ach:" + pw + "@127.0.0.1:1/ach?sslmode=disable",
			"postgresql://ach:" + pw + "@[::1/ach",
		} {
			err := db.Migrate(dsn, t.TempDir())
			if err == nil {
				t.Fatalf("%q: expected an error", dsn)
			}
			for _, leak := range []string{pw, dsn} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error for %q leaks %q: %v", dsn, leak, err)
				}
			}
		}
	}
}

// A DSN without a user (a "/" straight after the scheme-less colon) is refused
// with the fixed message, never handed to the driver.
func TestMigrateRefusesDSNWithoutUser(t *testing.T) {
	for _, dsn := range []string{"ach:/secret@host:5432/ach", "ach:123/secret@host:5432/ach"} {
		err := db.Migrate(dsn, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "not a valid URL") || strings.Contains(err.Error(), "secret") {
			t.Errorf("%q: err = %v, want the fixed message", dsn, err)
		}
	}
}
