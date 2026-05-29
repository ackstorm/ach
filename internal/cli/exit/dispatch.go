// SPDX-License-Identifier: Apache-2.0
package exit

import (
	"errors"
	"fmt"
	"io"

	"github.com/ackstorm/ach/internal/cli/httpclient"
)

// Dispatch resolves any error returned from cobra.Execute() to the
// process exit Code per CLI spec §9.3. nil → OK; *httpclient.ServerError
// → MapServerError; *CodedError → its Code; anything else → General.
//
// Use DispatchAndRender from main() when you also want to print the
// error to stderr before exiting.
func Dispatch(err error) Code {
	if err == nil {
		return OK
	}
	var sErr *httpclient.ServerError
	if errors.As(err, &sErr) {
		return MapServerError(sErr)
	}
	var cErr *CodedError
	if errors.As(err, &cErr) {
		return cErr.Code
	}
	return General
}

// DispatchAndRender resolves the exit Code and writes the error string
// to stderr (when err is non-nil). main() should call os.Exit(int(code))
// with the returned Code.
func DispatchAndRender(err error, stderr io.Writer) Code {
	code := Dispatch(err)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
	}
	return code
}
