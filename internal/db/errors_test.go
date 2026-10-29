// SPDX-License-Identifier: Apache-2.0

// Unit tests for the shared isTransientPgErr classifier. Package-internal
// (`package db`, not `package db_test`) so we can exercise the package-private
// symbol directly without a Docker-backed integration container.
//
// No build tag — these tests run under the default `make test` and require
// no Postgres.

package db

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsTransientPgErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "class_08_connection_exception",
			err:  &pgconn.PgError{Code: "08006"}, // connection_failure
			want: true,
		},
		{
			name: "class_57_operator_intervention",
			err:  &pgconn.PgError{Code: "57014"}, // query_canceled
			want: true,
		},
		{
			name: "class_23_constraint_violation_is_terminal",
			err:  &pgconn.PgError{Code: "23505"}, // unique_violation
			want: false,
		},
		{
			name: "class_42_syntax_or_access_rule_is_terminal",
			err:  &pgconn.PgError{Code: "42P01"}, // undefined_table
			want: false,
		},
		{
			name: "non_pgconn_error",
			err:  errors.New("not a pg error"),
			want: false,
		},
		{
			name: "nil_error_is_defensive_false",
			err:  nil,
			want: false,
		},
		{
			name: "pgconn_with_short_code",
			err:  &pgconn.PgError{Code: "0"}, // pathologically short
			want: false,
		},
		{
			name: "wrapped_class_08_still_transient",
			// errors.As traverses the wrap chain — verifies the
			// classifier doesn't accidentally rely on direct typing.
			err:  &wrappedErr{inner: &pgconn.PgError{Code: "08001"}},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isTransientPgErr(tc.err)
			if got != tc.want {
				t.Errorf("isTransientPgErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// wrappedErr is a minimal error wrapper to verify errors.As traversal.
type wrappedErr struct{ inner error }

func (w *wrappedErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w *wrappedErr) Unwrap() error { return w.inner }
