// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package cmd

import (
	"strings"
	"testing"
)

func TestClassifierRefusesWithoutEnterprise(t *testing.T) {
	t.Setenv("ACH_ENTERPRISE", "")
	err := classifierCmd.RunE(classifierCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "ACH Enterprise feature") {
		t.Fatalf("RunE = %v", err)
	}
}
