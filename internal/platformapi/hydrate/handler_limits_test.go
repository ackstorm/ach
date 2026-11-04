// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/litellm"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

type fakeCaps struct {
	deps    map[string]litellm.ModelCaps
	aliases map[string]string
	err     error
}

func (f fakeCaps) ListDeploymentCapabilities(context.Context) (map[string]litellm.ModelCaps, error) {
	return f.deps, f.err
}

func (f fakeCaps) ModelGroupAliases(context.Context) (map[string]string, error) {
	return f.aliases, f.err
}

func ptr[T any](v T) *T { return &v }

// limitsResponse builds the runtime block the handler builds, through the same
// helpers; the Store is concrete (see TestHydrateResponse_NoticePassThrough).
func limitsResponse(src litellm.CapsSource, models ...string) HydrateResponse {
	env := &db.EnvironmentRow{Name: "demo", RuntimeModels: models}
	resp := HydrateResponse{
		SchemaVersion: SchemaVersion,
		Environment:   env.Name,
		Runtime:       toRuntimeBlockFromRow(env, "https://ach.test"),
		Context:       toContextBlockFromRow(env, "https://ach.test"),
	}
	caps := litellm.NewCapsCache(src, slog.New(slog.NewTextHandler(io.Discard, nil)))
	addModelLimits(context.Background(), caps, resp.Runtime.Models)
	return resp
}

func TestModelLimits_AliasResolvesToTargetDeployment(t *testing.T) {
	resp := limitsResponse(fakeCaps{
		deps: map[string]litellm.ModelCaps{"vertex.flash": {
			MaxInputTokens: ptr(524288.0), MaxOutputTokens: ptr(65536.0),
		}},
		aliases: map[string]string{"ackstorm.smart": "vertex.flash"},
	}, "ackstorm.smart", "vertex.flash")

	for _, m := range resp.Runtime.Models {
		if m.Limits == nil || m.Limits.Context == nil || *m.Limits.Context != 524288 ||
			m.Limits.Output == nil || *m.Limits.Output != 65536 {
			t.Errorf("%s limits = %+v, want 524288/65536", m.ID, m.Limits)
		}
	}
}

func TestModelLimits_OmittedWhenUnknown(t *testing.T) {
	resp := limitsResponse(fakeCaps{deps: map[string]litellm.ModelCaps{
		"none":        {},
		"output-only": {MaxOutputTokens: ptr(8192.0)},
		"zero":        {MaxInputTokens: ptr(0.0)},
	}}, "none", "output-only", "zero", "not-listed")

	got := map[string]*ModelLimits{}
	for _, m := range resp.Runtime.Models {
		got[m.ID] = m.Limits
	}
	for _, id := range []string{"none", "zero", "not-listed"} {
		if got[id] != nil {
			t.Errorf("%s limits = %+v, want omitted", id, got[id])
		}
	}
	if l := got["output-only"]; l == nil || l.Context != nil || l.Output == nil || *l.Output != 8192 {
		t.Errorf("output-only limits = %+v, want only output", l)
	}
}

func TestModelLimits_CapsFailureOmitsThem(t *testing.T) {
	resp := limitsResponse(fakeCaps{err: errors.New("litellm down")}, "ackstorm.smart")

	if len(resp.Runtime.Models) != 1 || resp.Runtime.Models[0].Limits != nil {
		t.Fatalf("models = %+v, want one model without limits", resp.Runtime.Models)
	}
}

func TestModelLimits_NilCapsOmitsThem(t *testing.T) {
	models := []RuntimeItem{{ID: "m"}}
	addModelLimits(context.Background(), nil, models)
	if models[0].Limits != nil {
		t.Fatalf("limits = %+v, want none", models[0].Limits)
	}
}

// TestModelLimits_DecodesWithCLIManifest runs the wire response through the
// strict decoder ach-cli hydrates with.
func TestModelLimits_DecodesWithCLIManifest(t *testing.T) {
	resp := limitsResponse(fakeCaps{deps: map[string]litellm.ModelCaps{
		"m": {MaxInputTokens: ptr(1000.0), MaxOutputTokens: ptr(100.0)},
	}}, "m")
	rec := httptest.NewRecorder()
	render.JSON(rec, http.StatusOK, resp)
	if !strings.Contains(rec.Body.String(), `"limits":{"context":1000,"output":100}`) {
		t.Fatalf("wire body = %s", rec.Body.String())
	}

	if _, err := manifest.Decode(rec.Body); err != nil {
		t.Fatalf("manifest.Decode: %v", err)
	}
}
