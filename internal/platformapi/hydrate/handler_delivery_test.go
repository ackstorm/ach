// SPDX-License-Identifier: Apache-2.0

package hydrate

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ackstorm/ach/internal/cli/manifest"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/platformapi/render"
)

func TestContextBlock_MarksOnlyNativePluginCRs(t *testing.T) {
	row := &db.EnvironmentRow{ContextPlugins: []string{"memory", "caveman", "memory@some-marketplace"}}
	block := toContextBlockFromRow(row, "http://ach", map[string]bool{"memory": true})

	got := map[string]string{}
	for _, p := range block.Plugins {
		got[p.Name] = p.Delivery
	}
	if got["memory"] != "native" {
		t.Errorf("memory delivery = %q; want native", got["memory"])
	}
	if got["caveman"] != "" {
		t.Errorf("caveman delivery = %q; want empty", got["caveman"])
	}
	// A marketplace plugin has no Plugin CR, so it is never native, whatever its name.
	if got["memory@some-marketplace"] != "" {
		t.Errorf("memory@some-marketplace delivery = %q; want empty", got["memory@some-marketplace"])
	}

	rec := httptest.NewRecorder()
	render.JSON(rec, http.StatusOK, HydrateResponse{SchemaVersion: SchemaVersion, Environment: "dev", Context: block})
	body := rec.Body.String()
	if strings.Count(body, `"delivery"`) != 1 || !strings.Contains(body, `"delivery":"native"`) {
		t.Fatalf("wire body = %s; want delivery on the native plugin only", body)
	}
	m, err := manifest.Decode(rec.Body)
	if err != nil {
		t.Fatalf("the CLI's strict decoder rejects the response: %v", err)
	}
	if m.Context.Plugins[0].Delivery != manifest.DeliveryNative {
		t.Errorf("decoded delivery = %q; want %q", m.Context.Plugins[0].Delivery, manifest.DeliveryNative)
	}
}
