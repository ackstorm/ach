// SPDX-License-Identifier: Apache-2.0

package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// TestGuardrailModeAcceptsBothShapes: LiteLLM serialises `mode` as a bare
// string on one guardrail and an array on another IN THE SAME response
// (measured on api.ackstorm.ai 2026-07-28: credential-filter -> "pre_call",
// test1 -> ["pre_call"]). A typed `Mode string` fails the whole document, and
// the Snapshotter reads a decode error as a failed refresh — so one array-typed
// guardrail would blank the entire catalog rather than one row.
func TestGuardrailModeAcceptsBothShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want []string
	}{
		"scalar":  {`{"mode":"pre_call"}`, []string{"pre_call"}},
		"array":   {`{"mode":["pre_call","post_call"]}`, []string{"pre_call", "post_call"}},
		"absent":  {`{}`, nil},
		"null":    {`{"mode":null}`, nil},
		"empty[]": {`{"mode":[]}`, []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			var got struct {
				Mode GuardrailMode `json:"mode"`
			}
			if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.in, err)
			}
			if !slices.Equal([]string(got.Mode), tc.want) {
				t.Fatalf("mode = %v, want %v", got.Mode, tc.want)
			}
		})
	}

	var bad struct {
		Mode GuardrailMode `json:"mode"`
	}
	if err := json.Unmarshal([]byte(`{"mode":42}`), &bad); err == nil {
		t.Fatal("numeric mode must be a decode error, not silently dropped")
	}
}

// guardrailSrv serves the two list endpoints from the supplied bodies. A body
// of "" makes that endpoint return 404.
func guardrailSrv(t *testing.T, v1Body, v2Body string, v1Code, v2Code int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		var code int
		switch r.URL.Path {
		case "/guardrails/list":
			body, code = v1Body, v1Code
		case "/v2/guardrails/list":
			body, code = v2Body, v2Code
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
}

// TestListGuardrailsUnionsBothEndpoints: /guardrails/list carries only
// config-defined guardrails, /v2/guardrails/list only DB-defined ones. Neither
// is a superset (measured: on ackstorm the config list is empty and v2 holds
// both live guardrails), so the client unions them and dedupes by name.
func TestListGuardrailsUnionsBothEndpoints(t *testing.T) {
	srv := guardrailSrv(t,
		`{"guardrails":[{"guardrail_name":"config-guard","litellm_params":{"mode":"pre_call","default_on":true}}]}`,
		`{"guardrails":[
			{"guardrail_id":"uuid-1","guardrail_name":"db-guard","litellm_params":{"mode":["pre_call"],"default_on":false}},
			{"guardrail_id":"uuid-2","guardrail_name":"config-guard","litellm_params":{"mode":"pre_call","default_on":true}}
		]}`,
		http.StatusOK, http.StatusOK)
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).ListGuardrails(context.Background())
	if err != nil {
		t.Fatalf("ListGuardrails: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 deduped entries, got %d: %+v", len(got), got)
	}
	byName := map[string]GuardrailEntry{}
	for _, g := range got {
		byName[g.GuardrailName] = g
	}
	cfg, ok := byName["config-guard"]
	if !ok {
		t.Fatal("config-guard missing")
	}
	if !cfg.DefaultOn {
		t.Error("config-guard default_on want true")
	}
	// Present in BOTH endpoints, but agreeing (note the two mode shapes
	// normalise to the same slice) — an agreeing duplicate is not ambiguous,
	// and the first occurrence keeps its empty config-side GuardrailID.
	if cfg.Ambiguous {
		t.Error("config-guard: agreeing duplicate must not be flagged Ambiguous")
	}
	if cfg.GuardrailID != "" {
		t.Errorf("config-guard id = %q, want first-occurrence (config) value ''", cfg.GuardrailID)
	}
	db, ok := byName["db-guard"]
	if !ok {
		t.Fatal("db-guard missing")
	}
	if db.GuardrailID != "uuid-1" {
		t.Errorf("db-guard id = %q", db.GuardrailID)
	}
	if !slices.Equal([]string(db.Mode), []string{"pre_call"}) {
		t.Errorf("db-guard mode = %v", db.Mode)
	}
}

// TestListGuardrailsConflictingDuplicateIsAmbiguous pins the collision rule.
// The same guardrail_name from both endpoints with DIFFERENT mode/default_on is
// a real possibility (nothing stops an admin registering a DB guardrail whose
// name matches a config-file one) and we could not measure which one LiteLLM
// actually enforces — the only licensed proxy available has an empty config
// list.
//
// So the entry survives (membership is unambiguous — Environments naming it must
// still resolve, or a display quirk would block ek_ provisioning) but is flagged
// Ambiguous, and Task 8 writes no attributes for it. The alternative — picking
// one silently — makes the catalog assert something we do not know.
func TestListGuardrailsConflictingDuplicateIsAmbiguous(t *testing.T) {
	srv := guardrailSrv(t,
		`{"guardrails":[{"guardrail_name":"dup","litellm_params":{"mode":"pre_call","default_on":false}}]}`,
		`{"guardrails":[{"guardrail_id":"uuid-9","guardrail_name":"dup","litellm_params":{"mode":["post_call"],"default_on":true}}]}`,
		http.StatusOK, http.StatusOK)
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).ListGuardrails(context.Background())
	if err != nil {
		t.Fatalf("ListGuardrails: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	if !got[0].Ambiguous {
		t.Error("conflicting duplicate must be flagged Ambiguous")
	}
	// Identity is still first-occurrence-wins, deterministically.
	if !slices.Equal([]string(got[0].Mode), []string{"pre_call"}) {
		t.Errorf("mode = %v, want the first occurrence's value", got[0].Mode)
	}
}

// TestListGuardrails404Degrades: /v2/guardrails/list is the newer route; a
// LiteLLM without the DB registry must still yield its config guardrails.
func TestListGuardrails404Degrades(t *testing.T) {
	srv := guardrailSrv(t,
		`{"guardrails":[{"guardrail_name":"only-config"}]}`, ``,
		http.StatusOK, http.StatusNotFound)
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).ListGuardrails(context.Background())
	if err != nil {
		t.Fatalf("ListGuardrails: %v", err)
	}
	if len(got) != 1 || got[0].GuardrailName != "only-config" {
		t.Fatalf("got %+v", got)
	}
}

// TestListGuardrailsNon404IsHardError is the important one. A partial union
// must NEVER be returned as authoritative: the Snapshotter would publish it as
// fresh, tombstoning catalog rows for the guardrails the failed endpoint owns
// and marking Environments that reference them unresolved — blocking ek_ mints
// on a transient 500. Only 404 degrades; everything else fails the refresh so
// the prior snapshot is preserved with Stale=true.
func TestListGuardrailsNon404IsHardError(t *testing.T) {
	for name, tc := range map[string]struct{ v1Code, v2Code int }{
		"v1 500":   {http.StatusInternalServerError, http.StatusOK},
		"v2 500":   {http.StatusOK, http.StatusInternalServerError},
		"v2 403":   {http.StatusOK, http.StatusForbidden},
		"both 500": {http.StatusInternalServerError, http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			srv := guardrailSrv(t,
				`{"guardrails":[{"guardrail_name":"survivor"}]}`,
				`{"guardrails":[{"guardrail_name":"other"}]}`,
				tc.v1Code, tc.v2Code)
			defer srv.Close()

			got, err := newTestClient(t, srv.URL).ListGuardrails(context.Background())
			if err == nil {
				t.Fatalf("want error, got %d entries: %+v", len(got), got)
			}
			if got != nil {
				t.Errorf("want nil result on hard error, got %+v", got)
			}
			if errors.Is(err, ErrNotFound) {
				t.Error("a 5xx must not be reported as ErrNotFound")
			}
		})
	}
}

// TestListGuardrailsDecodeErrorIsHardError: a malformed body is the same
// hazard as a 5xx — a partial union must not be published.
func TestListGuardrailsDecodeErrorIsHardError(t *testing.T) {
	srv := guardrailSrv(t,
		`{"guardrails":[{"guardrail_name":"survivor"}]}`, `{not json`,
		http.StatusOK, http.StatusOK)
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).ListGuardrails(context.Background()); err == nil {
		t.Fatal("malformed body must be a hard error")
	}
}

// TestListGuardrailsEmptyIsErrNotFound: REL-05 — an empty union is ErrNotFound,
// which the Snapshotter downgrades to an empty set. A proxy with zero
// guardrails is a valid empty closed-set, not a failure.
func TestListGuardrailsEmptyIsErrNotFound(t *testing.T) {
	srv := guardrailSrv(t, `{"guardrails":[]}`, `{"guardrails":[]}`,
		http.StatusOK, http.StatusOK)
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).ListGuardrails(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestListGuardrailsBoth404IsErrNotFound: a LiteLLM with neither route is
// "no guardrails", not an outage.
func TestListGuardrailsBoth404IsErrNotFound(t *testing.T) {
	srv := guardrailSrv(t, ``, ``, http.StatusNotFound, http.StatusNotFound)
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).ListGuardrails(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestIsHTTPForbidden covers the helper Task 5 uses to turn LiteLLM's
// premium-gate 403 into an actionable message.
func TestIsHTTPForbidden(t *testing.T) {
	if !IsHTTPForbidden(&APIError{StatusCode: http.StatusForbidden}) {
		t.Error("403 APIError not recognised")
	}
	if IsHTTPForbidden(&APIError{StatusCode: http.StatusNotFound}) {
		t.Error("404 wrongly recognised as forbidden")
	}
	if IsHTTPForbidden(errors.New("plain")) {
		t.Error("non-APIError wrongly recognised")
	}
	if IsHTTPForbidden(nil) {
		t.Error("nil wrongly recognised")
	}
}
