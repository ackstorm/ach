// SPDX-License-Identifier: Apache-2.0

package evidence

import (
	"errors"

	"github.com/ackstorm/ach/internal/litellm"
)

// ErrNoScope means a read was attempted without a valid Scope.
var ErrNoScope = errors.New("evidence: scope is required")

// Scope says whose data a read may touch. The only way to get one is a
// constructor; the zero value is invalid, so a forgotten Scope fails loudly
// instead of reading everything. Iteration 1 has one shape (a user). A manager
// view adds another constructor (several emails / environments); queries bind
// through Scope, so they do not change.
type Scope struct{ email string }

// NewUserScope scopes reads to one person's Langfuse user_id (their email).
func NewUserScope(email string) (Scope, error) {
	e := litellm.NormalizeEmail(email)
	if e == "" {
		return Scope{}, ErrNoScope
	}
	return Scope{email: e}, nil
}

func (s Scope) Email() string { return s.email }
func (s Scope) Valid() bool   { return s.email != "" }
