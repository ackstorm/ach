// SPDX-License-Identifier: Apache-2.0

package evidence

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalidArgument   = errors.New("evidence: invalid argument")
	ErrProjectNotAllowed = errors.New("evidence: project not allow-listed")
	ErrProjectRequired   = errors.New("evidence: trace exists in several projects; project is required")
	ErrNotFound          = errors.New("evidence: trace not found")
	ErrScopeMismatch     = errors.New("evidence: response outside the requested scope")
	ErrSourceUnavailable = errors.New("evidence: source unavailable")
)

const (
	StatusUnknown     = "unknown"
	StatePartial      = "partial"
	SourceUnavailable = "source_unavailable"
	// GroupCap bounds the grouped rows (tools, models) one report returns.
	GroupCap  = 100
	maxWindow = 31 * 24 * time.Hour
)

// Reader runs the caller-scoped evidence queries every edition serves.
type Reader struct {
	ch       *ClickHouse
	projects []string
}

func NewReader(ch *ClickHouse, projects []string) *Reader {
	return &Reader{ch: ch, projects: slices.Clone(projects)}
}

func (r *Reader) CH() *ClickHouse    { return r.ch }
func (r *Reader) Projects() []string { return r.projects }

// Window is a half-open [Start, End) source window.
type Window struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Timezone string    `json:"timezone"`
}

func (w Window) Valid() bool {
	return !w.Start.IsZero() && !w.End.IsZero() && w.End.After(w.Start) && w.End.Sub(w.Start) <= maxWindow
}

type Coverage struct {
	Numerator   int  `json:"numerator"`
	Denominator *int `json:"denominator"`
}

// Provenance travels with every page so the UI can tell unavailable from empty.
type Provenance struct {
	Window                Window         `json:"window"`
	Source                string         `json:"source"`
	Scope                 string         `json:"scope"`
	SubjectIdentifierType string         `json:"subject_identifier_type"`
	ObservedAt            *time.Time     `json:"observed_at"`
	Grain                 string         `json:"grain"`
	Denominator           string         `json:"denominator"`
	Coverage              Coverage       `json:"coverage"`
	Formula               string         `json:"formula"`
	FormulaVersion        int            `json:"formula_version"`
	Exclusions            map[string]int `json:"exclusions"`
	State                 string         `json:"state"`
	Sampled               bool           `json:"sampled"`
	Reason                *string        `json:"reason"`
	RetentionDays         *int           `json:"retention_days"`
	ObservedStart         *time.Time     `json:"observed_start"`
	ObservedEnd           *time.Time     `json:"observed_end"`
}

func NewProvenance(w Window, grain, denominator, formula string) Provenance {
	now := time.Now().UTC()
	return Provenance{
		Window: w, Source: "clickhouse", Scope: "self", SubjectIdentifierType: "session_email",
		ObservedAt: &now, Grain: grain, Denominator: denominator, Formula: formula, FormulaVersion: 1,
		Exclusions: map[string]int{}, State: "available",
	}
}

func UnavailableProvenance(w Window, grain, reason string) Provenance {
	p := NewProvenance(w, grain, "scoped source rows", "bounded-explicit-projection")
	p.ObservedAt, p.State, p.Reason = nil, StatePartial, &reason
	p.Exclusions = map[string]int{reason: 1}
	return p
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }

// Ratio is num/den, nil when either is absent, den is not positive or the
// result is not finite.
func Ratio(num, den *float64) *float64 {
	if num == nil || den == nil || *den <= 0 {
		return nil
	}
	v := *num / *den
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// Scoped expands __SCOPE__ / __UNOWNED_SCOPE__ / __KEY_OWNER_SCOPE__ over the
// projects, binding the project and the caller's email as typed parameters (never SQL text).
func Scoped(sql string, projects []string, s Scope, params map[string]string) string {
	own := make([]string, len(projects))
	unowned := make([]string, len(projects))
	keyOwner := make([]string, len(projects))
	for i, p := range projects {
		params[fmt.Sprintf("project%d", i)] = p
		params[fmt.Sprintf("subject%d", i)] = s.email
		own[i] = fmt.Sprintf("(project_id={project%d:String} AND user_id={subject%d:String})", i, i)
		unowned[i] = fmt.Sprintf("project_id={project%d:String}", i)
		keyOwner[i] = fmt.Sprintf("(project_id={project%d:String} AND metadata_values[indexOf(metadata_names, 'attributes.litellm.metadata.user_api_key_user_id')]={subject%d:String})", i, i)
	}
	sql = strings.ReplaceAll(sql, "__KEY_OWNER_SCOPE__", strings.Join(keyOwner, " OR "))
	sql = strings.ReplaceAll(sql, "__UNOWNED_SCOPE__", strings.Join(unowned, " OR "))
	return strings.ReplaceAll(sql, "__SCOPE__", strings.Join(own, " OR "))
}

// WindowParams binds the window as typed parameters.
func WindowParams(w Window, params map[string]string) {
	params["start"] = w.Start.UTC().Format(time.RFC3339Nano)
	params["end"] = w.End.UTC().Format(time.RFC3339Nano)
}
