package tui

import (
	"strings"
	"testing"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/runner"
)

func recoveryTestAgentConfig() map[string]any {
	return map[string]any{"tiers": map[string]any{
		"default": map[string]any{"agent": "codex", "model": "model-a", "effort": "high"},
	}}
}

// TestRecoveryDialogWarnsWhenPlanAssignmentChanged: the checkpoint's route is
// still the paused assignment, but the plan header no longer names it, so
// Resume cannot reuse the saved session. The dialog says so before R runs.
func TestRecoveryDialogWarnsWhenPlanAssignmentChanged(t *testing.T) {
	root := writeTestProject(t)
	model := loadedModel(t, root)
	model.agentConfig = func(*format.Plan, agents.RunArgs) (map[string]any, error) {
		return recoveryTestAgentConfig(), nil
	}
	recovery := runner.Recovery{
		Checkpoint: &runner.Checkpoint{
			Plan: "P001", Unit: "P001.1.2", Stage: "agent",
			Agent: "pi", Model: "old-model", Effort: "low",
		},
	}
	lines, _ := model.recoveryDialogLines(recovery)
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "Resume will start a new session for P001.1.2 because its assignment changed") {
		t.Fatalf("recovery dialog does not warn about the lost session:\n%s", text)
	}

	recovery.Checkpoint.Agent, recovery.Checkpoint.Model, recovery.Checkpoint.Effort = "codex", "model-a", "high"
	lines, _ = model.recoveryDialogLines(recovery)
	if text := strings.Join(lines, "\n"); strings.Contains(text, "new session") {
		t.Fatalf("a matching plan assignment still warns about a new session:\n%s", text)
	}
}
