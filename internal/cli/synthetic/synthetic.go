// SPDX-License-Identifier: Apache-2.0

package synthetic

import (
	"os"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/keys"
)

// SyntheticProfileLabel is the profile name recorded (e.g. in state.json)
// when synthetic mode is active.
const SyntheticProfileLabel = "(env)"

// Gate declares a command's disposition under synthetic mode.
type Gate int

const (
	// GateAPI — every command that calls the Hub: allowed.
	GateAPI Gate = iota + 1
	// GateSession — login, logout, token, profile *: refused (they read or
	// write the config file).
	GateSession
	// GateKeysCreate — keys create that saves the new key into the profile
	// (no --no-save): refused, there is no file to save into.
	GateKeysCreate
)

// Params is what a command passes to GuardCommand. Env vars are read via
// Getenv.
type Params struct {
	Gate        Gate
	ProfileFlag string // --profile
	KeyFlag     string // --key
}

// Getenv is the env-var read seam (tests use t.Setenv).
var Getenv = os.Getenv

// IsActive reports whether ACH_URL and ACH_KEY are both set.
func IsActive() bool {
	return Getenv("ACH_URL") != "" && Getenv("ACH_KEY") != ""
}

// IsRawKey reports whether s is an ek-… key rather than a saved key name.
func IsRawKey(s string) bool {
	p, err := keys.ClassifyBearer(s)
	return err == nil && p == keys.PrefixEk
}

// GuardCommand returns an exit-1 error when the invocation cannot run in
// synthetic mode, nil otherwise (always nil outside synthetic mode).
func GuardCommand(p Params) error {
	if !IsActive() {
		return nil
	}
	refuse := func(msg string) error {
		return &exit.CodedError{Code: exit.General, Msg: msg + " (ACH_URL + ACH_KEY are set: no config file is used)"}
	}
	switch p.Gate {
	case GateSession:
		return refuse("this command is not available in synthetic mode")
	case GateKeysCreate:
		return refuse("keys create needs --no-save in synthetic mode")
	}
	if p.ProfileFlag != "" || Getenv("ACH_PROFILE") != "" {
		return refuse("--profile / ACH_PROFILE cannot be used in synthetic mode")
	}
	key := p.KeyFlag
	if key == "" {
		key = Getenv("ACH_KEY")
	}
	if !IsRawKey(key) {
		return refuse("--key / ACH_KEY must be a raw ek-… key in synthetic mode; a saved key name needs a profile")
	}
	return nil
}
