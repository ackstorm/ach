// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

// TestHydrateResponse_NoticePassThrough asserts that an Environment row
// carrying spec.notice surfaces that text verbatim in the §15.2 hydrate
// response body under the `notice` key.
//
// The hydrate Store is the concrete *store.Store (a pgxpool reader with no
// interface seam), so the full HydrateHandler is only exercisable against a
// real Postgres (integration). This test instead drives the same response
// construction the handler performs — the `Notice: env.Notice` mapping plus
// the real render.JSON serialization the handler writes through — directly
// from a *db.EnvironmentRow, then decodes the emitted body. It is the same
// pattern the package's existing handler_escape_test.go uses (calling the
// real builders directly rather than faking the store).
func TestHydrateResponse_NoticePassThrough(t *testing.T) {
	const baseURL = "https://ach.example.com"
	env := &db.EnvironmentRow{Name: "demo", Notice: "hi"}

	resp := HydrateResponse{
		SchemaVersion: SchemaVersion,
		Environment:   env.Name,
		Runtime:       toRuntimeBlockFromRow(env, baseURL),
		Context:       toContextBlockFromRow(env, baseURL),
		Notice:        env.Notice,
	}

	rec := httptest.NewRecorder()
	render.JSON(rec, http.StatusOK, resp)

	var got struct {
		Notice string `json:"notice"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode hydrate body: %v\nbody=%s", err, rec.Body.String())
	}
	if got.Notice != "hi" {
		t.Errorf("response notice = %q, want %q", got.Notice, "hi")
	}
}
