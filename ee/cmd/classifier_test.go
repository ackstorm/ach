// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package cmd

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/ackstorm/ach/ee/classifier"
)

func TestClassifierRefusesWithoutEnterprise(t *testing.T) {
	t.Setenv("ACH_ENTERPRISE", "")
	err := classifierCmd.RunE(classifierCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "ACH Enterprise feature") {
		t.Fatalf("RunE = %v", err)
	}
}

func TestClassifierModelAPIIsChatOrDecisions(t *testing.T) {
	for _, k := range []string{"CLICKHOUSE_URL", "CLICKHOUSE_USER", "CLICKHOUSE_PASSWORD", "MODEL_URL", "MODEL_KEY"} {
		t.Setenv("ACH_CLASSIFIER_"+k, "x")
	}
	for api, want := range map[string]string{"": classifier.APIChat, "decisions": classifier.APIDecisions, "jev": ""} {
		t.Setenv("ACH_CLASSIFIER_MODEL_API", api)
		job, _, err := classifierFromEnv(slog.Default())
		if (err == nil) != (want != "") || (err == nil && job.Model.API != want) {
			t.Errorf("ACH_CLASSIFIER_MODEL_API=%q: err=%v", api, err)
		}
	}
}
