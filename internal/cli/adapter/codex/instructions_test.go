// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"testing"

	"github.com/ackstorm/ach/internal/cli/adapter"
)

func TestCodexIsNotAnInstructionTarget(t *testing.T) {
	if _, ok := any(&Adapter{}).(adapter.InstructionTarget); ok {
		t.Fatal("codex must not implement InstructionTarget")
	}
}
