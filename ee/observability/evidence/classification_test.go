// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/ackstorm/ach/ee/classifier"
	core "github.com/ackstorm/ach/internal/observability/evidence"
)

func readClassification(t *testing.T, bodies ...string) (ClassificationReport, *[]call) {
	t.Helper()
	ch, calls := scriptedCH(t, bodies...)
	rep, err := NewReader(core.NewReader(ch, []string{"p1"})).Classification(context.Background(), scope(t, "a@x"), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	return rep, calls
}

func TestClassificationSplitsSessionsAndCoverage(t *testing.T) {
	rep, calls := readClassification(t, page(
		`{"category":"technology.genai_development","sessions":2,"generations":30,"input_tokens":9000,"output_tokens":300,"cost":1.5}`,
		`{"category":"trivial","sessions":1,"generations":1,"input_tokens":10,"output_tokens":1,"cost":0}`,
		`{"category":"(unlabelled)","sessions":5,"generations":40,"input_tokens":1,"output_tokens":1,"cost":2}`,
		`{"category":"(no session)","sessions":1,"generations":7,"input_tokens":1,"output_tokens":1,"cost":0.1}`,
	))
	if len(rep.Categories) != 2 || rep.Categories[0] != (CategoryUsage{"technology.genai_development", "technology", 2, 30, 9000, 300, 1.5}) ||
		rep.Categories[1].Category != "trivial" {
		t.Errorf("categories = %+v, want the two labels ranked by sessions, placeholders out", rep.Categories)
	}
	c := rep.Coverage
	if c == nil || c.Sessions != 8 || c.Labelled != 3 || c.Share == nil || *c.Share != 3.0/8 || c.SessionlessGenerations != 7 {
		t.Errorf("coverage = %+v, want 3 of 8 sessions, 7 sessionless generations apart", c)
	}
	if rep.Taxonomy != "v3" || rep.Provenance.State != "available" || rep.Provenance.Coverage.Numerator != 3 || *rep.Provenance.Coverage.Denominator != 8 {
		t.Errorf("rep = %+v", rep)
	}
	q, b := (*calls)[0].Q, (*calls)[0].Body
	if q.Get("param_score") != "ach_classification_category" || q.Get("param_taxonomy") != "v3" || q.Get("param_subject0") != "a@x" {
		t.Errorf("params = %v", q)
	}
	for _, frag := range []string{"user_id={subject0:String}", "default.scores FINAL", "is_deleted = 0", "type = 'GENERATION'", "LEFT JOIN labels"} {
		if !strings.Contains(b, frag) {
			t.Errorf("sql missing %q", frag)
		}
	}
	// Only the granted scores columns: never the free-text ones.
	for _, col := range []string{"comment", "string_value", "value"} {
		if strings.Contains(b, col) {
			t.Errorf("sql reads scores.%s", col)
		}
	}
}

// A deployment's own taxonomy: the report reads that version's scores and its
// categories are known, where the built-in one would reject them.
func TestClassificationReadsTheDeploymentTaxonomy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "taxonomy.yaml")
	yaml := "categories:\n  - id: operations.fleet_planning\n    fits: Routes and delivery schedules.\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	tax, err := classifier.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ch, calls := scriptedCH(t, page(`{"category":"operations.fleet_planning","sessions":1,"generations":2,"input_tokens":0,"output_tokens":0,"cost":0}`))
	rep, err := NewReader(core.NewReader(ch, []string{"p1"})).WithTaxonomy(tax).Classification(context.Background(), scope(t, "a@x"), testWindow())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Taxonomy != tax.Version || rep.Provenance.State != "available" || len(rep.Categories) != 1 {
		t.Errorf("rep = %+v, want %s with its one category", rep, tax.Version)
	}
	if got := (*calls)[0].Q.Get("param_taxonomy"); got != tax.Version {
		t.Errorf("param_taxonomy = %q, want %q", got, tax.Version)
	}
}

// Nothing classified yet (the classifier runs for one user during rollout):
// coverage 0 of N, not an empty or unavailable report.
func TestClassificationNothingLabelledIsZeroCoverage(t *testing.T) {
	rep, _ := readClassification(t, page(`{"category":"(unlabelled)","sessions":4,"generations":9,"input_tokens":0,"output_tokens":0,"cost":0}`))
	if c := rep.Coverage; c == nil || c.Sessions != 4 || c.Labelled != 0 || c.Share == nil || *c.Share != 0 || len(rep.Categories) != 0 {
		t.Errorf("rep = %+v", rep)
	}
	rep, _ = readClassification(t, page())
	if c := rep.Coverage; c == nil || c.Sessions != 0 || c.Share != nil || rep.Provenance.State != "available" {
		t.Errorf("empty window = %+v", rep)
	}
}

// ACCESS_DENIED before the default.scores grant lands arrives as an exception.
func TestClassificationFailedSourceIsUnavailable(t *testing.T) {
	for _, body := range []string{`{"exception":"Code: 497. ACCESS_DENIED"}`,
		page(`{"category":"made.up","sessions":1,"generations":1,"input_tokens":0,"output_tokens":0,"cost":0}`)} {
		rep, _ := readClassification(t, body)
		if rep.Coverage != nil || rep.Provenance.Reason == nil || *rep.Provenance.Reason != core.SourceUnavailable || rep.Categories == nil {
			t.Errorf("%s: rep = %+v", body, rep)
		}
	}
}

func TestClassificationMatchesWireContract(t *testing.T) {
	s, err := jsonschema.NewCompiler().Compile("testdata/contracts/classification.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		page(`{"category":"other","sessions":1,"generations":2,"input_tokens":3,"output_tokens":4,"cost":0.5}`),
		page(),
		`{"exception":"boom"}`,
	} {
		rep, _ := readClassification(t, body)
		b, _ := json.Marshal(rep)
		var v any
		_ = json.Unmarshal(b, &v)
		if err := s.Validate(v); err != nil {
			t.Errorf("%v\n%s", err, b)
		}
	}
}
