// SPDX-License-Identifier: Apache-2.0

package db

import "errors"

// The projection tables' `origin` column (migration 000005) is either 'cr'
// (owned by the operator reconciling a CR) or 'ui' (owned by the UI write path,
// internal/platformapi/objects). SQL uses the literals directly.
//
// GitOps-wins model (G2): the operator is always authoritative. A CR applied
// over a UI-owned row TAKES IT OVER (origin flips 'ui'→'cr', locked=TRUE); the
// UI can only manage rows the operator does not own (origin='ui').

// ErrImmutableViaUI is returned by the UI-side update/delete helpers when the
// target row is operator-owned (origin='cr'). The platform-api maps it to
// HTTP 403 immutable_via_ui — the object is managed by Kubernetes/GitOps and
// must be edited there, not via the UI.
var ErrImmutableViaUI = errors.New("db: object is operator-owned (origin=cr); immutable via UI")

// ErrConflictWithCR is returned by the UI-side insert helper when a row with
// the same (namespace, name) already exists and is operator-owned. The
// platform-api maps it to HTTP 409 conflict_with_kubernetes_object.
var ErrConflictWithCR = errors.New("db: a Kubernetes-owned object with that name already exists")

// ErrUIAlreadyExists is returned by the UI-side insert helper when a UI-owned
// row with the same (namespace, name) already exists. The platform-api maps it
// to HTTP 409 (use PATCH to modify an existing object).
var ErrUIAlreadyExists = errors.New("db: a UI-managed object with that name already exists")

// ErrUINotFound is returned by the UI-side update/delete helpers when no row
// with the given (namespace, name) exists. The platform-api maps it to 404.
var ErrUINotFound = errors.New("db: no object with that name")
