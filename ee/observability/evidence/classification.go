// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package evidence

import (
	"cmp"
	"context"
	"slices"

	"github.com/ackstorm/ach/ee/classifier"
	core "github.com/ackstorm/ach/internal/observability/evidence"
)

// Classification splits the caller's sessions in the window by the work
// category the conversation classifier gave them: one Langfuse score per
// session (classifier.ScoreName, metadata category + taxonomy) in
// default.scores. A score has no user: the scope comes from joining it to the
// caller's own generations, so only sessions the caller has in the window are
// ever labelled. Generations, tokens and cost are the caller's in-window
// generations of each session. A session with no score yet (still active, or
// not classified for this user) is counted in coverage, never hidden.
//
// Only project_id, timestamp, name, id, event_ts, is_deleted, session_id and
// metadata of default.scores are read: the grant covers those columns (FINAL
// needs the sort key), never comment or string_value.
const classificationSQL = `
WITH own AS (
  SELECT project_id, ifNull(session_id, '') AS sid, count() AS gens,
         sumIf(usage_details['input'], mapContains(usage_details, 'input')) AS in_tokens,
         sumIf(usage_details['output'], mapContains(usage_details, 'output')) AS out_tokens,
         sumIf(toFloat64(cost_details['total']), mapContains(cost_details, 'total')) AS spend
  FROM default.events_full AS source FINAL
  WHERE is_deleted = 0 AND (__SCOPE__)
    AND source.start_time >= parseDateTime64BestEffort({start:String}, 6, 'UTC')
    AND source.start_time < parseDateTime64BestEffort({end:String}, 6, 'UTC')
    AND type = 'GENERATION'
  GROUP BY project_id, sid
),
labels AS (
  -- argMax: a re-run keeps the score id, but Langfuse's sort key also holds
  -- the timestamp's day, so FINAL alone may leave two rows for one session.
  SELECT project_id, assumeNotNull(session_id) AS sid, argMax(metadata['category'], event_ts) AS label
  FROM default.scores FINAL
  WHERE is_deleted = 0 AND (__UNOWNED_SCOPE__) AND name = {score:String}
    AND metadata['taxonomy'] = {taxonomy:String} AND session_id != ''
  GROUP BY project_id, sid
)
SELECT multiIf(own.sid = '', '(no session)', labels.label = '', '(unlabelled)', labels.label) AS category,
       count() AS sessions, sum(own.gens) AS generations,
       sum(own.in_tokens) AS input_tokens, sum(own.out_tokens) AS output_tokens, sum(own.spend) AS cost
FROM own
LEFT JOIN labels ON own.project_id = labels.project_id AND own.sid = labels.sid
GROUP BY category
FORMAT JSON`

const (
	noSession  = "(no session)"
	unlabelled = "(unlabelled)"
)

type ClassificationReport struct {
	Window     core.Window             `json:"window"`
	Provenance core.Provenance         `json:"provenance"`
	Taxonomy   string                  `json:"taxonomy"`
	Categories []CategoryUsage         `json:"categories"`
	Coverage   *ClassificationCoverage `json:"coverage"` // null when the source is unavailable
}

// CategoryUsage is one category's labelled sessions and their generations.
type CategoryUsage struct {
	Category     string  `json:"category"` // taxonomy id, e.g. technology.genai_development
	Domain       string  `json:"domain"`   // its prefix, e.g. technology
	Sessions     int     `json:"sessions"`
	Generations  int     `json:"generations"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

// ClassificationCoverage is labelled sessions over sessions in the window.
// Generations with no session id can never be classified and are counted
// apart, outside both.
type ClassificationCoverage struct {
	Sessions               int      `json:"sessions"`
	Labelled               int      `json:"labelled"`
	Share                  *float64 `json:"share"` // labelled / sessions; null with no sessions
	SessionlessGenerations int      `json:"sessionless_generations"`
}

func (r *Reader) Classification(ctx context.Context, s core.Scope, w core.Window) (ClassificationReport, error) {
	if !s.Valid() {
		return ClassificationReport{}, core.ErrNoScope
	}
	if !w.Valid() {
		return ClassificationReport{}, core.ErrInvalidArgument
	}
	if len(r.Projects()) == 0 {
		return ClassificationReport{}, core.ErrProjectNotAllowed
	}
	rep := ClassificationReport{Window: w, Taxonomy: r.tax.Version, Categories: []CategoryUsage{}}
	params := map[string]string{"score": classifier.ScoreName, "taxonomy": r.tax.Version}
	core.WindowParams(w, params)
	var rows []struct {
		Category     string  `json:"category"`
		Sessions     float64 `json:"sessions"`
		Generations  float64 `json:"generations"`
		InputTokens  float64 `json:"input_tokens"`
		OutputTokens float64 `json:"output_tokens"`
		Cost         float64 `json:"cost"`
	}
	// e.g. ACCESS_DENIED before the default.scores grant lands: unavailable, not empty.
	failed := r.CH().Query(ctx, core.Scoped(classificationSQL, r.Projects(), s, params), params, len(r.tax.Categories)+2, &rows) != nil
	for _, row := range rows {
		failed = failed || (row.Category != noSession && row.Category != unlabelled && r.tax.Index(row.Category) < 0)
	}
	if failed {
		rep.Provenance = core.UnavailableProvenance(w, "session", core.SourceUnavailable)
		return rep, nil
	}
	cov := ClassificationCoverage{}
	for _, row := range rows {
		switch row.Category {
		case noSession:
			cov.SessionlessGenerations = int(row.Generations)
		case unlabelled:
			cov.Sessions += int(row.Sessions)
		default:
			cov.Sessions += int(row.Sessions)
			cov.Labelled += int(row.Sessions)
			rep.Categories = append(rep.Categories, CategoryUsage{
				Category: row.Category, Domain: classifier.Domain(row.Category), Sessions: int(row.Sessions),
				Generations: int(row.Generations), InputTokens: int(row.InputTokens), OutputTokens: int(row.OutputTokens), Cost: row.Cost,
			})
		}
	}
	slices.SortFunc(rep.Categories, func(a, b CategoryUsage) int {
		return cmp.Or(b.Sessions-a.Sessions, cmp.Compare(a.Category, b.Category))
	})
	if cov.Sessions > 0 {
		cov.Share = core.Ptr(float64(cov.Labelled) / float64(cov.Sessions))
	}
	rep.Coverage = &cov
	rep.Provenance = core.NewProvenance(w, "session", "scoped sessions with a session id in requested window", "full-window-final-aggregate")
	rep.Provenance.Coverage = core.Coverage{Numerator: cov.Labelled, Denominator: &cov.Sessions}
	rep.Provenance.Exclusions = map[string]int{"sessionless_generations": cov.SessionlessGenerations}
	return rep, nil
}
