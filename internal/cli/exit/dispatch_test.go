// SPDX-License-Identifier: Apache-2.0
package exit_test

import (
	"errors"
	"testing"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/httpclient"
)

func TestDispatch_OK(t *testing.T) {
	code := exit.Dispatch(nil)
	if code != exit.OK {
		t.Errorf("nil err → exit.OK; got %d", code)
	}
}

func TestDispatch_ServerError(t *testing.T) {
	sErr := &httpclient.ServerError{Status: 403, Code: "not_admin"}
	code := exit.Dispatch(sErr)
	if code != exit.AuthN {
		t.Errorf("not_admin → AuthN; got %d", code)
	}
}

func TestDispatch_CodedError(t *testing.T) {
	cErr := &exit.CodedError{Code: exit.ConfigFile, Msg: "bad config"}
	code := exit.Dispatch(cErr)
	if code != exit.ConfigFile {
		t.Errorf("CodedError preserves Code; got %d", code)
	}
}

func TestDispatch_Fallthrough(t *testing.T) {
	code := exit.Dispatch(errors.New("unrecognized"))
	if code != exit.General {
		t.Errorf("unknown err → General; got %d", code)
	}
}
