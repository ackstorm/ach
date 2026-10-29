// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/ee/classifier"
	"github.com/ackstorm/ach/internal/config"
	"github.com/ackstorm/ach/internal/edition"
	"github.com/ackstorm/ach/internal/observability/evidence"
)

var classifierOnce bool

var classifierCmd = &cobra.Command{
	Use:   "classifier",
	Short: "Label work sessions with a taxonomy category and write it to Langfuse",
	Long: `Reads idle sessions from Langfuse ClickHouse as the read-only classification
user, classifies each with one model call and writes one NUMERIC score per
session (ach_classification_category). Without ACH_CLASSIFIER_LANGFUSE_URL it
is a dry run: it classifies and logs, and writes nothing.

Environment:
  ACH_CLASSIFIER_CLICKHOUSE_URL        ClickHouse HTTP endpoint (required)
  ACH_CLASSIFIER_CLICKHOUSE_USER       read-only user: input/output on events_full, SELECT on scores (required)
  ACH_CLASSIFIER_CLICKHOUSE_PASSWORD   (required)
  ACH_CLASSIFIER_LANGFUSE_PROJECT_ID   default: default
  ACH_CLASSIFIER_MODEL_URL             OpenAI-compatible base, e.g. https://api.example.com/v1 (required)
  ACH_CLASSIFIER_MODEL_KEY             gateway key (required)
  ACH_CLASSIFIER_MODEL                 default: ackstorm.smart
  ACH_CLASSIFIER_LANGFUSE_URL          Langfuse host; set = scores are written (both keys required),
                                       empty = dry run
  ACH_CLASSIFIER_LANGFUSE_PUBLIC_KEY
  ACH_CLASSIFIER_LANGFUSE_SECRET_KEY
  ACH_CLASSIFIER_ONLY_USER             only this user's sessions (rollout)
  ACH_CLASSIFIER_INTERVAL              default: 10m
  ACH_CLASSIFIER_IDLE                  a session is ready when idle this long, default: 30m
  ACH_CLASSIFIER_LOOKBACK              default: 72h
  ACH_CLASSIFIER_BATCH                 sessions per pass, default: 50
  ACH_CLASSIFIER_TAXONOMY_FILE         YAML with the deployment's categories and rules;
                                       empty = built-in taxonomy (v3)
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := edition.Require("ach classifier", os.Getenv); err != nil {
			return err
		}
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		job, interval, err := classifierFromEnv(logger)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		logger.Info("classifier starting", "model", job.Model.Name, "dry_run", job.Writer == nil,
			"only_user", job.OnlyUser != "", "taxonomy", job.Tax.Version, "categories", len(job.Tax.Categories))
		if classifierOnce {
			job.Pass(ctx)
			return nil
		}
		return job.Run(ctx, interval)
	},
}

func init() {
	classifierCmd.Flags().BoolVar(&classifierOnce, "once", false, "run one pass and exit")
	edition.RegisterCommand(classifierCmd)
}

// classifierFromEnv fails at startup on a half-configured pod. Secrets are
// never logged.
func classifierFromEnv(logger *slog.Logger) (*classifier.Job, time.Duration, error) {
	req := map[string]string{}
	for _, k := range []string{"ACH_CLASSIFIER_CLICKHOUSE_URL", "ACH_CLASSIFIER_CLICKHOUSE_USER",
		"ACH_CLASSIFIER_CLICKHOUSE_PASSWORD", "ACH_CLASSIFIER_MODEL_URL", "ACH_CLASSIFIER_MODEL_KEY"} {
		v, err := config.MustEnvNonEmpty(k)
		if err != nil {
			return nil, 0, err
		}
		req[k] = v
	}
	dur := func(k, def string) (time.Duration, error) {
		d, err := time.ParseDuration(config.EnvOr(k, def))
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("%s: must be a positive duration", k)
		}
		return d, nil
	}
	interval, err := dur("ACH_CLASSIFIER_INTERVAL", "10m")
	if err != nil {
		return nil, 0, err
	}
	idle, err := dur("ACH_CLASSIFIER_IDLE", "30m")
	if err != nil {
		return nil, 0, err
	}
	lookback, err := dur("ACH_CLASSIFIER_LOOKBACK", "72h")
	if err != nil {
		return nil, 0, err
	}
	batch, err := config.MustEnvIntPositive("ACH_CLASSIFIER_BATCH", 50)
	if err != nil {
		return nil, 0, err
	}
	ch := evidence.NewClickHouse(evidence.Config{
		URL: strings.TrimRight(req["ACH_CLASSIFIER_CLICKHOUSE_URL"], "/"), User: req["ACH_CLASSIFIER_CLICKHOUSE_USER"],
		Password: req["ACH_CLASSIFIER_CLICKHOUSE_PASSWORD"],
		// One raw session input per read, measured up to 22.5 MB; the server profile caps memory at 2 GB.
		MaxResultBytes: 64 << 20, MaxMemoryBytes: 1_800_000_000,
	}, nil)
	tax, err := classifier.FromEnv()
	if err != nil {
		return nil, 0, err
	}
	job := &classifier.Job{
		Tax:    tax,
		Reader: classifier.Reader{CH: ch, Project: config.EnvOr("ACH_CLASSIFIER_LANGFUSE_PROJECT_ID", "default")},
		Model: classifier.Model{
			URL:  strings.TrimRight(req["ACH_CLASSIFIER_MODEL_URL"], "/"),
			Key:  req["ACH_CLASSIFIER_MODEL_KEY"],
			Name: config.EnvOr("ACH_CLASSIFIER_MODEL", "ackstorm.smart"),
		},
		Lookback: lookback, Idle: idle, Batch: batch,
		OnlyUser: os.Getenv("ACH_CLASSIFIER_ONLY_USER"), Log: logger,
	}
	lfURL := os.Getenv("ACH_CLASSIFIER_LANGFUSE_URL")
	pub, sec := os.Getenv("ACH_CLASSIFIER_LANGFUSE_PUBLIC_KEY"), os.Getenv("ACH_CLASSIFIER_LANGFUSE_SECRET_KEY")
	// The URL alone decides: the key pair can sit in the Secret during a dry run.
	if lfURL != "" {
		if pub == "" || sec == "" {
			return nil, 0, errors.New("ACH_CLASSIFIER_LANGFUSE_URL is set: " +
				"ACH_CLASSIFIER_LANGFUSE_PUBLIC_KEY and _SECRET_KEY are required")
		}
		job.Writer = classifier.Langfuse{URL: strings.TrimRight(lfURL, "/"), PublicKey: pub, SecretKey: sec}
	}
	return job, interval, nil
}
