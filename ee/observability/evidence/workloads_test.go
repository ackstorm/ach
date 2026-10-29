// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	core "github.com/ackstorm/ach/internal/observability/evidence"
)

func readWorkloads(t *testing.T, bodies ...string) (WorkloadReport, *[]call) {
	t.Helper()
	ch, calls := scriptedCH(t, bodies...)
	rep, err := NewReader(core.NewReader(ch, []string{"p1"})).Workloads(context.Background(), scope(t, "a@x"), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	return rep, calls
}

func TestWorkloadsCountsWholePopulationAndUnknown(t *testing.T) {
	// ports: test_workloads_counts_complete_generation_population_and_unknown
	rep, calls := readWorkloads(t, page(`{"name":"codex","count":600,"input_tokens":9000,"output_tokens":300,"cost":1.25}`, `{"name":"unknown","count":800,"input_tokens":0,"output_tokens":0,"cost":0}`))
	if rep.GenerationCount == nil || *rep.GenerationCount != 1400 || rep.SourceCoverage.Runtime != 600 {
		t.Errorf("rep = %+v", rep)
	}
	if len(rep.Runtimes) != 2 || rep.Runtimes[0] != (RuntimeUsage{Name: "unknown", Count: 800}) ||
		rep.Runtimes[1] != (RuntimeUsage{Name: "codex", Count: 600, InputTokens: 9000, OutputTokens: 300, Cost: 1.25}) {
		t.Errorf("runtimes = %+v, want ranked by count with their usage", rep.Runtimes)
	}
	b := (*calls)[0].Body
	for _, frag := range []string{"FINAL", "user_id={subject0:String}", "attributes.http.user_agent", "user_id = ''",
		"mapContains(usage_details, 'input')", "mapContains(usage_details, 'output')", "mapContains(cost_details, 'total')"} {
		if !strings.Contains(b, frag) {
			t.Errorf("sql missing %q", frag)
		}
	}
}

// The User-Agent is on the unowned gateway root, never on the user's own
// generation: attribution is per trace, and says so.
func TestWorkloadsAttributesThroughTheTrace(t *testing.T) {
	// ports: none — live data: 0 of 5400 generations carry a User-Agent, 5146 of 5146 traces carry exactly one
	rep, _ := readWorkloads(t, page(`{"name":"claude_code","count":3}`))
	if rep.RuntimeAttribution != "same_trace" {
		t.Errorf("runtime_attribution = %q", rep.RuntimeAttribution)
	}
}

func TestWorkloadsRejectsUnknownRuntimeName(t *testing.T) {
	// ports: read_workloads allow-list — a label outside the parser's set means the SQL and the parser disagree
	rep, _ := readWorkloads(t, page(`{"name":"curl","count":3}`))
	if rep.GenerationCount != nil || rep.Provenance.Reason == nil {
		t.Errorf("rep = %+v", rep)
	}
}

func TestWorkloadsFailedSourceIsNotObservedEmpty(t *testing.T) {
	// ports: test_failed_source_is_not_observed_empty
	rep, _ := readWorkloads(t, `{"exception":"boom"}`)
	if rep.GenerationCount != nil || rep.Provenance.Reason == nil || *rep.Provenance.Reason != core.SourceUnavailable || rep.Runtimes == nil {
		t.Errorf("rep = %+v", rep)
	}
}

func TestWorkloadsMatchesWireContract(t *testing.T) {
	rep, _ := readWorkloads(t, page(`{"name":"opencode","count":5,"input_tokens":10,"output_tokens":2,"cost":0.01}`))
	b, _ := json.Marshal(rep)
	var v any
	_ = json.Unmarshal(b, &v)
	s, err := jsonschema.NewCompiler().Compile("testdata/contracts/workloads.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(v); err != nil {
		t.Errorf("%v\n%s", err, b)
	}
}
