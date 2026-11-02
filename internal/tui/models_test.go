package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/runner"
)

func modelsTestProject(t *testing.T) string {
	t.Helper()
	root := writeTestProject(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@localhost"},
		{"config", "commit.gpgsign", "false"},
		{"add", "-A"},
		{"commit", "-qm", "fixture"},
	} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	return root
}

func modelsTestConfig() map[string]any {
	return map[string]any{
		"tiers":  map[string]any{"default": map[string]any{"agent": "fake", "model": ""}},
		"agents": map[string]any{"pi": map[string]any{"cmd": []any{"pi"}}},
	}
}

func loadedModelsModel(t *testing.T) Model {
	t.Helper()
	root := modelsTestProject(t)
	model := loadedModel(t, root)
	model.modelsConfig = func(*format.Plan, agents.RunArgs) (map[string]any, error) {
		return modelsTestConfig(), nil
	}
	model.modelsRoster = func(*format.Plan) (map[string][]agents.Route, error) {
		return map[string][]agents.Route{"executor": {{Agent: "codex", Model: "gpt-6", Effort: "high"}}}, nil
	}
	return resize(t, model, 120, 40)
}

func modelsRowIndex(model Model, key string) int {
	if model.models == nil {
		return -1
	}
	for i, row := range model.models.rows {
		if row.key == key {
			return i
		}
	}
	return -1
}

func TestModelsEditorSavesAndCancelKeepsSnapshot(t *testing.T) {
	model := loadedModelsModel(t)
	model = send(t, model, keyMsg("m"))
	if model.models == nil {
		t.Fatalf("m did not open the model editor: %s", model.notice)
	}
	screen := model.String()
	if !strings.Contains(screen, "Models and executors") || !strings.Contains(screen, "P001.1.2") {
		t.Fatalf("model screen does not show the plan assignments:\n%s", screen)
	}
	if !strings.Contains(screen, "personal roster") || !strings.Contains(screen, "codex | gpt-6 | high") {
		t.Fatalf("model screen does not show the personal roster:\n%s", screen)
	}
	index := modelsRowIndex(model, "P001.1.2")
	if index < 0 {
		t.Fatal("P001.1.2 is missing from the model screen")
	}
	model.models.cursor = index
	model = send(t, model, keyMsg("enter"))
	if !model.models.editing {
		t.Fatal("Enter did not open the assignment editor")
	}
	model.models.input = ""
	for _, text := range []string{"pi | fast | high"} {
		model = send(t, model, teaText(text))
	}
	model = send(t, model, keyMsg("enter"))
	if model.models.editing {
		t.Fatalf("Enter did not confirm the edit: %s", model.models.notice)
	}
	configuration, err := runner.LoadConfiguration(model.root)
	if err != nil {
		t.Fatal(err)
	}
	routes := configuration.Units["P001.1.2"]
	if len(routes) != 1 || routes[0].Agent != "pi" || routes[0].Model != "fast" || routes[0].Effort != "high" {
		t.Fatalf("saved unit override = %#v", routes)
	}
	written, err := os.ReadFile(mustConfigurationPath(t, model.root))
	if err != nil {
		t.Fatal(err)
	}

	// Opening the editor and cancelling must not touch the saved snapshot.
	model = send(t, model, keyMsg("enter"))
	model.models.input = "codex | other | low"
	model = send(t, model, keyMsg("esc"))
	if model.models.editing {
		t.Fatal("Esc did not close the editor")
	}
	after, err := os.ReadFile(mustConfigurationPath(t, model.root))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(written) {
		t.Fatalf("cancelled editor changed the snapshot:\n%s\n---\n%s", written, after)
	}
}

func TestModelsEditorRefusesEditDuringRun(t *testing.T) {
	model := loadedModelsModel(t)
	model = send(t, model, keyMsg("m"))
	model.runState = runner.StateRunning
	model.models.cursor = modelsRowIndex(model, "P001.1.2")
	model = send(t, model, keyMsg("enter"))
	if model.models.editing {
		t.Fatal("the model editor opened while a run was active")
	}
	if !strings.Contains(model.models.notice, "cannot change while a run is active") {
		t.Fatalf("notice = %q", model.models.notice)
	}
	model.runState = runner.StatePaused
	model = send(t, model, keyMsg("enter"))
	if !model.models.editing {
		t.Fatalf("the model editor refused an edit in Paused: %s", model.models.notice)
	}
}

func TestPausedAssignmentChangeWarnsNewSession(t *testing.T) {
	model := loadedModelsModel(t)
	head := strings.TrimSpace(testGitOutput(t, model.root, "rev-parse", "HEAD"))
	if err := runner.WriteCheckpoint(model.root, runner.Checkpoint{
		Plan: "P001", Unit: "P001.1.2", Head: head, PlanHash: "fixture", Stage: "agent",
		Agent: "pi", Model: "old", Effort: "low", Session: "s1", Calls: 1,
	}); err != nil {
		t.Fatal(err)
	}
	model.runState = runner.StatePaused
	model.checkpointed = true
	model = send(t, model, keyMsg("m"))
	model.models.cursor = modelsRowIndex(model, "P001.1.2")
	model = send(t, model, keyMsg("enter"))
	model.models.input = "pi | new | high"
	model = send(t, model, keyMsg("enter"))
	if !strings.Contains(model.models.notice, "new session for P001.1.2") {
		t.Fatalf("notice = %q; want an explanation that Resume starts a new session", model.models.notice)
	}
}

func TestModelsEditorResetRestoresPlan(t *testing.T) {
	model := loadedModelsModel(t)
	configuration := runner.NewRunConfiguration()
	configuration.Units["P001.1.2"] = []agents.Route{{Agent: "pi", Model: "override", Effort: "high"}}
	if _, err := runner.SaveConfiguration(model.root, model.snapshot.Queue[0].Doc, agents.RunArgs{}, modelsTestConfig(), configuration); err != nil {
		t.Fatal(err)
	}
	model = send(t, model, keyMsg("m"))
	model.models.cursor = modelsRowIndex(model, "P001.1.2")
	model = send(t, model, keyMsg("enter"))
	model.models.input = "-"
	model = send(t, model, keyMsg("enter"))
	loaded, err := runner.LoadConfiguration(model.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Units["P001.1.2"]; ok {
		t.Fatalf("reset left the unit override in place: %#v", loaded.Units)
	}
}

func mustConfigurationPath(t *testing.T, root string) string {
	t.Helper()
	path, err := runner.ConfigurationPath(root)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func testGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func teaText(text string) tea.KeyPressMsg {
	runes := []rune(text)
	msg := tea.KeyPressMsg{Text: text}
	if len(runes) > 0 {
		msg.Code = runes[0]
	}
	return msg
}

// An override saved for a unit that a later run completed must disappear from
// the reopened editor instead of refusing the next save.
func TestModelsEditorDropsCompletedUnitOverrides(t *testing.T) {
	model := loadedModelsModel(t)
	root := model.root
	model = send(t, model, keyMsg("m"))
	model.models.cursor = modelsRowIndex(model, "P001.1.2")
	if model.models.cursor < 0 {
		t.Fatalf("P001.1.2 is missing from the model screen: %s", model.models.notice)
	}
	model = send(t, model, keyMsg("enter"))
	model.models.input = "pi | fast | high"
	model = send(t, model, keyMsg("enter"))
	if model.models.editing {
		t.Fatalf("the edit was not saved: %s", model.models.notice)
	}
	saved, err := runner.LoadConfiguration(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Units["P001.1.2"]; !ok {
		t.Fatalf("fixture did not save the override: %#v", saved.Units)
	}

	// The unit is completed outside the workspace, as a run does.
	planPath := filepath.Join(root, "docs", "plans", "P001-cache.md")
	plan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(plan), "### [~] P001.1.2", "### [x] P001.1.2", 1)
	if err := os.WriteFile(planPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	testGitOutput(t, root, "add", "-A")
	testGitOutput(t, root, "commit", "-qm", "finish unit")

	model = loadedModel(t, root)
	model.modelsConfig = func(*format.Plan, agents.RunArgs) (map[string]any, error) {
		return modelsTestConfig(), nil
	}
	model.modelsRoster = func(*format.Plan) (map[string][]agents.Route, error) {
		return nil, nil
	}
	model = resize(t, model, 120, 40)
	model = send(t, model, keyMsg("m"))
	if model.models == nil {
		t.Fatalf("m did not open the model editor: %s", model.notice)
	}
	if index := modelsRowIndex(model, "P001.1.2"); index >= 0 {
		screen := model.String()
		t.Fatalf("the completed unit is still a row:\n%s", screen)
	}
	if modelsRowIndex(model, "P001.2.1") < 0 {
		t.Fatal("an open unit disappeared from the model screen")
	}

	// Resetting another unit rewrites the file without the stale entry.
	model.models.cursor = modelsRowIndex(model, "P001.2.1")
	model = send(t, model, keyMsg("enter"))
	model.models.input = "-"
	model = send(t, model, keyMsg("enter"))
	if model.models.editing {
		t.Fatalf("the reset was not saved: %s", model.models.notice)
	}
	loaded, err := runner.LoadConfiguration(model.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Units["P001.1.2"]; ok {
		t.Fatalf("saving another edit kept the completed unit's override: %#v", loaded.Units)
	}
}
