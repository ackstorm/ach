// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

// Package classifier labels each work session with one taxonomy category and
// writes it to Langfuse as a score. Contract: the input is OpenTelemetry GenAI
// messages (otel.go); only the reader knows how Langfuse stores a session.
package classifier

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Writer stores a label; Langfuse in production, nil in dry-run.
type Writer interface {
	Write(ctx context.Context, tax Taxonomy, sessionID string, lb Label, model string) error
}

// Job is one pass over idle sessions, oldest first from a cursor, so labelled
// sessions never crowd out new ones. Sessions already scored are skipped in
// SQL (Reader.IdleSessions), so a restart does not re-label; the cursor covers
// the seconds before a written score reaches ClickHouse.
type Job struct {
	Tax      Taxonomy // categories and prompt: Builtin, or the deployment's file
	Reader   Reader
	Model    Model
	Writer   Writer        // nil = dry run: classify and log, write nothing
	Lookback time.Duration // how far back to look for sessions
	Idle     time.Duration // a session is ready when idle this long
	Batch    int           // max sessions per pass
	OnlyUser string        // when set, only this user's sessions (phase E)
	Log      *slog.Logger

	after Cursor // every session up to here is finished
	fails map[string]int
}

// maxAttempts bounds the passes one failing session can hold the cursor back.
const maxAttempts = 3

// Run passes forever, every interval, until ctx ends.
func (j *Job) Run(ctx context.Context, interval time.Duration) error {
	for {
		j.Pass(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// Pass classifies one batch. Sessions are read one after another (each input
// can be tens of MB). A failing session holds the cursor and is retried next
// pass, up to maxAttempts; sessions after it are still labelled, and their
// scores keep them out of the next listing. Logs carry ids, labels and
// counts, never content.
func (j *Job) Pass(ctx context.Context) {
	if j.fails == nil {
		j.fails = map[string]int{}
	}
	rows, err := j.Reader.IdleSessions(ctx, j.Tax.Version, time.Now().Add(-j.Lookback), j.Idle, j.OnlyUser, j.after, j.Batch*4)
	if err != nil {
		j.Log.Warn("classifier: list sessions", "err", err)
		return
	}
	n, held := 0, false
	for _, row := range rows {
		if n >= j.Batch || ctx.Err() != nil {
			break
		}
		n++
		if err := j.label(ctx, row.ID); err != nil {
			if j.fails[row.ID]++; j.fails[row.ID] < maxAttempts {
				j.Log.Warn("classifier: will retry", "session_id", row.ID, "err", err)
				held = true
				continue
			}
			j.Log.Error("classifier: giving up", "session_id", row.ID, "attempts", maxAttempts, "err", err)
		}
		delete(j.fails, row.ID)
		if !held {
			j.after = Cursor{Last: row.Last, ID: row.ID}
		}
	}
}

// label classifies one session and writes its score.
func (j *Job) label(ctx context.Context, id string) error {
	s, err := j.Reader.Session(ctx, id)
	if err != nil {
		return fmt.Errorf("read session: %w", err)
	}
	lb := Label{Category: Trivial}
	if !IsTrivial(s) {
		if lb, err = j.Model.Classify(ctx, j.Tax, BuildText(s)); err != nil {
			return fmt.Errorf("classify: %w", err)
		}
	}
	if j.Writer != nil {
		if err := j.Writer.Write(ctx, j.Tax, id, lb, j.Model.Name); err != nil {
			return fmt.Errorf("write score: %w", err)
		}
	}
	j.Log.Info("classifier: labelled", "session_id", id, "client", s.Client, "generations", s.Generations,
		"category", lb.Category, "confidence", lb.Confidence, "cost_usd", lb.Cost, "dry_run", j.Writer == nil)
	return nil
}
