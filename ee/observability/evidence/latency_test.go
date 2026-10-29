// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	core "github.com/ackstorm/ach/internal/observability/evidence"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func readLatency(t *testing.T, bodies ...string) (LatencyReport, *[]call) {
	t.Helper()
	ch, calls := scriptedCH(t, bodies...)
	rep, err := NewReader(core.NewReader(ch, []string{"p1"})).Latency(context.Background(), scope(t, "a@x"), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	return rep, calls
}

const latencyRow = `{"generation_count":900,"timing_coverage":850,"ttft_coverage":0,"tokens_per_sec_coverage":0,"failed":4,
"p50_ms":1000,"p95_ms":5000,"p99_ms":9000,"avg_ms":1500,"ttft_p50_ms":null,"ttft_p95_ms":null,"tokens_per_sec_p50":null,
"hist0":0,"hist1":0,"hist2":0,"hist3":850,"hist4":0,"hist5":0,"hist6":0,"hist7":0,"hist8":0,
"first":"2026-10-01T00:00:00.000000Z","last":"2026-10-02T00:00:00.000000Z"}`

func TestLatencyPreservesUnmeasuredTTFTAndErrorVsUnknown(t *testing.T) {
	// ports: test_latency_preserves_unmeasured_ttft_and_error_vs_unknown
	rep, calls := readLatency(t, page(latencyRow), page(`{"model":"model-a","requests":900,"failed":4,"p50_ms":1000,"p95_ms":5000,"tokens_per_sec_p50":null}`))
	if !rep.Available || rep.RowCount == nil || *rep.RowCount != 900 || rep.Latency.TTFTP50 != nil {
		t.Errorf("rep = %+v", rep)
	}
	if rep.Outcomes[0] != (Outcome{"error", 4}) || rep.Outcomes[1] != (Outcome{"unknown", 896}) {
		t.Errorf("outcomes = %+v: a generation without an error is unknown, not success", rep.Outcomes)
	}
	sum := 0
	for _, b := range rep.Histogram {
		sum += b.Count
	}
	if sum != 850 || len(rep.Histogram) != 9 || rep.Histogram[8].UpperMS != nil {
		t.Errorf("histogram sum = %d bins=%d: only timed generations, last bin open", sum, len(rep.Histogram))
	}
	if len(rep.ByModel) != 1 || rep.ByModel[0].Model != "model-a" {
		t.Errorf("by_model = %+v", rep.ByModel)
	}
	if len(*calls) != 3 {
		t.Fatalf("calls = %d, want summary, by_model, guardrails", len(*calls))
	}
	for i, c := range (*calls)[:2] {
		if !strings.Contains(c.Body, " FINAL") || !strings.Contains(c.Body, "user_id={subject0:String}") || !strings.Contains(c.Body, "type = 'GENERATION'") {
			t.Errorf("call %d is not a scoped generation read", i)
		}
	}
}

func TestLatencyTTFTIsTheStreamedFirstChunk(t *testing.T) {
	for _, sql := range []string{latencySQL, latencyModelsSQL} {
		if !strings.Contains(sql, "toFloat64OrNull(metadata_values[indexOf(metadata_names, 'attributes.gen_ai.response.time_to_first_chunk')]) * 1000 AS ttft_ms") {
			t.Error("TTFT must come from time_to_first_chunk; a missing key is NULL, never 0")
		}
		if strings.Contains(sql, "completion_start_time") {
			t.Error("completion_start_time is never written by the gateway")
		}
	}
	rep, _ := readLatency(t, page(latencyRow), page(`{"model":"m","requests":1,"failed":0,"ttft_p50_ms":1210}`))
	if rep.ByModel[0].TTFTP50 == nil || *rep.ByModel[0].TTFTP50 != 1210 {
		t.Errorf("by_model ttft = %v", rep.ByModel[0].TTFTP50)
	}
}

func TestLatencyFinishCountsAreNullWithoutCoverage(t *testing.T) {
	rep, _ := readLatency(t, page(latencyRow))
	if rep.Finish != nil {
		t.Errorf("no finish reasons recorded must be null, not zeros: %+v", rep.Finish)
	}
	row := strings.Replace(latencyRow, `"failed":4,`, `"failed":4,"finish_coverage":800,"truncated":9,"content_filtered":1,`, 1)
	rep, _ = readLatency(t, page(row))
	if rep.Finish == nil || *rep.Finish != (FinishCounts{800, 9, 1}) {
		t.Errorf("finish = %+v", rep.Finish)
	}
}

func TestLatencyGuardrailsScopeToTheKeyOwner(t *testing.T) {
	// Guardrail rows carry no user_id; the gateway writes the key owner instead.
	rep, calls := readLatency(t, page(latencyRow), page(), page(`{"name":"credential-filter","checked":41816,"intervened":4}`))
	g := (*calls)[2].Body
	if !strings.Contains(g, "'attributes.litellm.metadata.user_api_key_user_id')]={subject0:String}") || strings.Contains(g, "__") {
		t.Errorf("guardrail read is not scoped to the key owner:\n%s", g)
	}
	if len(rep.Guardrails) != 1 || rep.Guardrails[0] != (GuardrailCount{"credential-filter", 41816, 4}) || rep.GuardrailsReason != nil {
		t.Errorf("guardrails = %+v reason=%v", rep.Guardrails, rep.GuardrailsReason)
	}
	rep, _ = readLatency(t, page(latencyRow), page(), `{"exception":"boom"}`)
	if rep.Guardrails != nil || rep.GuardrailsReason == nil {
		t.Errorf("a failed guardrail read must be unavailable, not zero interventions: %+v", rep.Guardrails)
	}
}

func TestLatencyTokensPerSecIsEndToEnd(t *testing.T) {
	// ports: test_latency_tokens_per_sec_is_end_to_end
	for _, sql := range []string{latencySQL, latencyModelsSQL} {
		for _, frag := range []string{
			"usage_details['output'] / (dateDiff('microsecond', start_time, end_time) / 1000000.0)",
			"mapContains(usage_details, 'output')", "end_time > start_time", "usage_details['output'] > 0",
		} {
			if !strings.Contains(sql, frag) {
				t.Errorf("sql missing %q", frag)
			}
		}
		if strings.Contains(sql, "completion_start_time, end_time") {
			t.Error("tokens/s must be end-to-end, not streaming speed")
		}
	}
}

func TestLatencyModelFailureKeepsSummary(t *testing.T) {
	// ports: read_latency by_model_reason — a failed breakdown does not hide the summary
	rep, _ := readLatency(t, page(latencyRow), `{"exception":"boom"}`)
	if !rep.Available || rep.ByModelReason == nil || len(rep.ByModel) != 0 {
		t.Errorf("rep = %+v", rep)
	}
}

func TestLatencyModelCap(t *testing.T) {
	rows := make([]string, core.GroupCap+1)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"model":"m%d","requests":1,"failed":0}`, i)
	}
	rep, _ := readLatency(t, page(latencyRow), page(rows...))
	if len(rep.ByModel) != core.GroupCap || !rep.ByModelTruncated {
		t.Errorf("by_model = %d truncated=%v", len(rep.ByModel), rep.ByModelTruncated)
	}
}

func TestLatencyFailedSourceIsNotObservedEmpty(t *testing.T) {
	// ports: test_failed_source_is_not_observed_empty
	rep, calls := readLatency(t, `{"exception":"boom"}`)
	if rep.Available || rep.RowCount != nil || rep.Reason == nil || len(*calls) != 1 {
		t.Errorf("rep = %+v calls=%d", rep, len(*calls))
	}
}

func TestLatencyMatchesWireContract(t *testing.T) {
	for name, bodies := range map[string][]string{
		"available":   {page(strings.Replace(latencyRow, `"failed":4,`, `"failed":4,"finish_coverage":1,"truncated":0,"content_filtered":0,`, 1)), page(`{"model":"a","requests":2,"failed":0,"p50_ms":1,"p95_ms":2,"ttft_p50_ms":1,"tokens_per_sec_p50":42.5}`), page(`{"name":"g","checked":2,"intervened":1}`)},
		"unavailable": {`{"exception":"boom"}`},
	} {
		rep, _ := readLatency(t, bodies...)
		b, _ := json.Marshal(rep)
		var v any
		_ = json.Unmarshal(b, &v)
		s, err := jsonschema.NewCompiler().Compile("testdata/contracts/latency.schema.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Validate(v); err != nil {
			t.Errorf("%s: latency does not match the contract: %v\n%s", name, err, b)
		}
	}
}
