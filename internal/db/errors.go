// SPDX-License-Identifier: Apache-2.0

// Package db shared error-classification helpers.
//
// isTransientPgErr is the SOLE classifier for pgconn errors that should be
// surfaced raw to the controller-runtime workqueue (so its exponential
// backoff can retry) instead of being wrapped as a terminal failure. The
// function previously lived in external_refs.go as a package-private helper;
// Phase 03-03 lifts it to this dedicated file so check_extend.go, ek_resolve.go,
// personal_keys.go, environment_keys.go, and active_keys.go can all reuse the
// same shared symbol without duplication.
//
// Both files live in the same `db` package, so moving the function changes
// neither its visibility nor any of its existing call sites.

package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isTransientPgErr returns true when err is a *pgconn.PgError with SQLSTATE
// class "08" (connection exception) or "57" (operator intervention). These
// are the canonical transient classes per Phase 1 W3; returning the raw
// error lets controller-runtime's exponential backoff handle the retry.
//
// Mirrors the classifyDrainErr pattern in
// internal/controller/ach/environment_controller.go lines 243-268.
func isTransientPgErr(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if len(pgErr.Code) < 2 {
		return false
	}
	class := pgErr.Code[:2]
	return class == "08" || class == "57"
}
