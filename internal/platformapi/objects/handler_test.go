// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ackstorm/ach/internal/db"
)

// fakeStore is an in-memory Store for handler tests — no Postgres. Per-method
// injectable errors let tests exercise the error-mapping branches.
type fakeStore struct {
	rows      map[string]db.EnvironmentRow
	getErr    error
	listErr   error
	insertErr error
	updateErr error
	deleteErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[string]db.EnvironmentRow{}}
}

func (f *fakeStore) Get(_ context.Context, name string) (*db.EnvironmentRow, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	row, ok := f.rows[name]
	if !ok {
		return nil, nil
	}
	return &row, nil
}

func (f *fakeStore) List(_ context.Context) ([]db.EnvironmentRow, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]db.EnvironmentRow, 0, len(f.rows))
	for _, row := range f.rows {
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeStore) Insert(_ context.Context, row db.EnvironmentRow) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.rows[row.Name] = row
	return nil
}

func (f *fakeStore) Update(_ context.Context, row db.EnvironmentRow) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.rows[row.Name] = row
	return nil
}

func (f *fakeStore) Delete(_ context.Context, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.rows, name)
	return nil
}

// newServer mounts the objects routes against the fake store and returns an
// httptest.Server. The chi router resolves the {kind}/{name} URL params.
func newServer(deps Deps, s Store) *httptest.Server {
	r := chi.NewRouter()
	r.Group(mountWithStore(deps, s))
	return httptest.NewServer(r)
}

func defaultDeps() Deps {
	return Deps{Namespace: "ach"}
}

func decodeManifest(t *testing.T, body []byte) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode manifest: %v (body=%s)", err, body)
	}
	return m
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, body)
	}
	return env.Error.Code
}

func TestPostObject_CreatesUIRow(t *testing.T) {
	s := newFakeStore()
	srv := newServer(defaultDeps(), s)
	defer srv.Close()

	body := `{"metadata":{"name":"dev"},"spec":{"authorizedTeams":["t1"],"runtime":{"models":["gpt-4"]}}}`
	resp, err := http.Post(srv.URL+"/environments", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if _, ok := s.rows["dev"]; !ok {
		t.Fatal("row was not inserted into the store")
	}
	if got := s.rows["dev"].RuntimeModels; len(got) != 1 || got[0] != "gpt-4" {
		t.Fatalf("inserted runtime.models = %v, want [gpt-4]", got)
	}
}

func TestPostObject_ConflictWithKubernetes_409(t *testing.T) {
	s := newFakeStore()
	s.insertErr = db.ErrConflictWithCR
	srv := newServer(defaultDeps(), s)
	defer srv.Close()

	body := `{"metadata":{"name":"dev"},"spec":{"authorizedTeams":["t1"]}}`
	resp, err := http.Post(srv.URL+"/environments", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	raw := readBody(t, resp)
	if c := errCode(t, raw); c != "conflict_with_kubernetes_object" {
		t.Fatalf("code = %q, want conflict_with_kubernetes_object", c)
	}
}

func TestPatchObject_ImmutableViaUI_403(t *testing.T) {
	s := newFakeStore()
	s.rows["dev"] = db.EnvironmentRow{Namespace: "ach", Name: "dev", AuthorizedTeams: []string{"t1"}}
	s.updateErr = db.ErrImmutableViaUI
	srv := newServer(defaultDeps(), s)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/environments/dev",
		strings.NewReader(`{"spec":{"notice":"x"}}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if c := errCode(t, readBody(t, resp)); c != "immutable_via_ui" {
		t.Fatalf("code = %q, want immutable_via_ui", c)
	}
}

func TestUIWritesDisabled_403(t *testing.T) {
	s := newFakeStore()
	deps := defaultDeps()
	deps.DisableUIWrites = true
	srv := newServer(deps, s)
	defer srv.Close()

	body := `{"metadata":{"name":"dev"},"spec":{"authorizedTeams":["t1"]}}`
	resp, err := http.Post(srv.URL+"/environments", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if c := errCode(t, readBody(t, resp)); c != "ui_writes_disabled" {
		t.Fatalf("code = %q, want ui_writes_disabled", c)
	}
	if len(s.rows) != 0 {
		t.Fatal("write must not reach the store when UI writes are disabled")
	}
}

func TestGetObject_NotFound_404(t *testing.T) {
	s := newFakeStore()
	srv := newServer(defaultDeps(), s)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/environments/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if c := errCode(t, readBody(t, resp)); c != "not_found" {
		t.Fatalf("code = %q, want not_found", c)
	}
}

func TestGetObject_ReturnsManifest(t *testing.T) {
	s := newFakeStore()
	s.rows["dev"] = db.EnvironmentRow{
		Namespace:       "ach",
		Name:            "dev",
		AuthorizedTeams: []string{"t1"},
		RuntimeModels:   []string{"gpt-4"},
	}
	srv := newServer(defaultDeps(), s)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/environments/dev")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	m := decodeManifest(t, readBody(t, resp))
	if m.APIVersion != apiVersion || m.Kind != kindName {
		t.Fatalf("apiVersion/kind = %q/%q", m.APIVersion, m.Kind)
	}
	if m.Metadata.Name != "dev" || m.Metadata.Namespace != "ach" {
		t.Fatalf("metadata = %+v", m.Metadata)
	}
	if len(m.Spec.Runtime.Models) != 1 || m.Spec.Runtime.Models[0] != "gpt-4" {
		t.Fatalf("spec.runtime.models = %v", m.Spec.Runtime.Models)
	}
}

func TestUnknownKind_404(t *testing.T) {
	s := newFakeStore()
	srv := newServer(defaultDeps(), s)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/widgets")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if c := errCode(t, readBody(t, resp)); c != "not_found" {
		t.Fatalf("code = %q, want not_found", c)
	}
}

// TestPatchObject_MergesSpec proves the JSON-merge semantics: an existing object
// with runtime.models=[a] gets a patch setting context.prompts=[p]; the result
// must carry BOTH (the patch did not erase the unmentioned runtime field).
func TestPatchObject_MergesSpec(t *testing.T) {
	s := newFakeStore()
	s.rows["dev"] = db.EnvironmentRow{
		Namespace:       "ach",
		Name:            "dev",
		AuthorizedTeams: []string{"t1"},
		RuntimeModels:   []string{"a"},
	}
	srv := newServer(defaultDeps(), s)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/environments/dev",
		strings.NewReader(`{"spec":{"context":{"prompts":["p"]}}}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got := s.rows["dev"]
	if len(got.RuntimeModels) != 1 || got.RuntimeModels[0] != "a" {
		t.Fatalf("merge dropped runtime.models: %v", got.RuntimeModels)
	}
	if len(got.ContextPrompts) != 1 || got.ContextPrompts[0] != "p" {
		t.Fatalf("merge did not apply context.prompts: %v", got.ContextPrompts)
	}
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}
