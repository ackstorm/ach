// SPDX-License-Identifier: Apache-2.0

package sources_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ackstorm/ach/internal/sources"
)

func TestClassifyHTTPStatus(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, sources.ErrUnauthorized},
		{http.StatusForbidden, sources.ErrUnauthorized},
		{http.StatusNotFound, sources.ErrNotFound},
		{http.StatusInternalServerError, sources.ErrUnreachable},
		{http.StatusBadGateway, sources.ErrUnreachable},
		{http.StatusBadRequest, sources.ErrUpstreamInvalid},
		{http.StatusTeapot, sources.ErrUpstreamInvalid},
	}
	for _, c := range cases {
		err := sources.ClassifyHTTPStatus("test", "op", c.status)
		if !errors.Is(err, c.want) {
			t.Errorf("status %d: got %v, want sentinel %v", c.status, err, c.want)
		}
	}
}

func TestClassifyHTTPStatus_2xxReturnsNil(t *testing.T) {
	if err := sources.ClassifyHTTPStatus("test", "op", http.StatusOK); err != nil {
		t.Errorf("200 should return nil, got %v", err)
	}
}

func TestDrainAndClose_NilSafe(t *testing.T) {
	sources.DrainAndClose(nil) // must not panic
}
