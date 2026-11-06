package runner

import (
	"context"
	"strings"
	"testing"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
)

func configAgents() map[string]any {
	return map[string]any{"agents": map[string]any{
		"pi":     map[string]any{"cmd": []any{"pi"}},
		"codex":  map[string]any{"cmd": []any{"codex"}},
		"claude": map[string]any{"cmd": []any{"claude"}},
	}}
}

func roleTestPlan(t *testing.T) *format.Plan {
	t.Helper()
	plan, err := format.ParsePlan(rolePlan, "docs/plans/P001-plan.md")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestEffectiveAssignmentsFromPlan(t *testing.T) {
	plan := roleTestPlan(t)
	effective, err := EffectiveAssignments(plan, agents.RunArgs{Executors: true}, configAgents())
	if err != nil {
		t.Fatal(err)
	}
	byUnit := map[string][]string{}
	for _, unit := range effective.Units {
		for _, route := range unit.Routes {
			byUnit[unit.Unit] = append(byUnit[unit.Unit], agents.RouteText(route))
		}
	}
	if got := byUnit["P001.1.1"]; len(got) != 2 || got[0] != "pi | zai/glm-5.3 | max" || got[1] != "codex | gpt-6.1-sol | high" {
		t.Fatalf("role unit routes = %v", got)
	}
	if got := byUnit["P001.1.2"]; len(got) != 3 || got[0] != "claude | claude-opus-5-5 | high" || got[1] != "codex | gpt-6-luna | max" {
		t.Fatalf("explicit unit chain = %v", got)
	}
	if got := byUnit["P001.1.3"]; len(got) != 3 || got[0] != "pi | zai/glm-5.3 | max" || got[1] != "codex | gpt-6-luna | max" {
		t.Fatalf("inheriting unit chain = %v", got)
	}
	if got := effective.Roles["heavy"]; len(got) != 2 || got[0].Agent != "pi" {
		t.Fatalf("role chain = %#v", effective.Roles["heavy"])
	}
	lines := strings.Join(AssignmentLines(effective), "\n")
	for _, want := range []string{"P001.1.1 [heavy]: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high", "role heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("assignment table does not contain %q:\n%s", want, lines)
		}
	}
}

func TestEffectiveAssignmentsSkipFinishedUnits(t *testing.T) {
	plan := roleTestPlan(t)
	if err := plan.SetStatus("P001.1.1", "x"); err != nil {
		t.Fatal(err)
	}
	effective, err := EffectiveAssignments(plan, agents.RunArgs{Executors: true}, configAgents())
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range effective.Units {
		if unit.Unit == "P001.1.1" {
			t.Fatal("a finished unit stayed in the effective table")
		}
	}
}

func TestStartPreviewShowsEveryOpenAssignment(t *testing.T) {
	f := newRunFixture(t)
	f.write(planFile, rolePlan)
	preview, err := StartPreview(f.root, Args{})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(preview.Assignments, "\n")
	for _, want := range []string{
		"P001.1.1 [heavy]: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high",
		"P001.1.2: claude | claude-opus-5-5 | high; codex | gpt-6-luna | max",
		"role heavy:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("start assignments do not contain %q:\n%s", want, text)
		}
	}
}

func TestStaleConfigurationFileIsIgnored(t *testing.T) {
	f := newRunFixture(t)
	f.write(".git/docflow/tui/configuration.json", `{"version":1,"units":{"P001.1.1":[{"agent":"fake","model":"stale-model","effort":"high"}]}}`)
	args := Args{RunArgs: agents.RunArgs{Config: f.config, Model: "fast"}}
	preview, err := StartPreview(f.root, args)
	if err != nil {
		t.Fatalf("StartPreview refused an old configuration file: %v", err)
	}
	if text := strings.Join(preview.Assignments, "\n"); strings.Contains(text, "stale-model") {
		t.Fatalf("the old configuration file reached the start assignments:\n%s", text)
	}
	f.steps(writeReply("a.txt"))
	f.check(f.cli("run", "--config", f.config, "--model", "fast", "--no-wait", "--max-units", "1"), 0, "P001.1.1 done:")
	calls := f.calls()
	if len(calls) != 1 || !strings.Contains(strings.Join(calls[0].Argv, " "), "--model fast") {
		t.Fatalf("agent argv = %#v; want the plan assignment", calls)
	}
}

func TestControlledRunUsesEffectiveAssignments(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	f.write(".git/docflow/tui/configuration.json", `{"version":1,"units":{"P001.1.1":[{"agent":"fake","model":"stale-model","effort":"high"}]}}`)
	f.steps(writeReply("a.txt"))
	control := NewControl(64, 2)
	args := Args{RunArgs: agents.RunArgs{Config: f.config, Model: "fast"}, Attempts: 1, MaxUnits: intPointer(1)}
	startControlledRun(t, f, control, args)
	awaitDone(t, control)
	if got := control.State(); got != StateFinished {
		t.Fatalf("state = %s, want Finished; err=%v", got, control.Err())
	}
	calls := f.calls()
	if len(calls) == 0 || !strings.Contains(strings.Join(calls[0].Argv, " "), "--model fast") {
		t.Fatalf("agent argv = %#v; want the plan assignment", calls)
	}
	events := fixtureEvents(t, f)
	var configIndex, attemptIndex = -1, -1
	for i, record := range events {
		switch record["event"] {
		case "config":
			configIndex = i
			if record["configured_roles"] != nil || record["configured_units"] != nil {
				t.Fatalf("config event still carries the removed overrides: %v", record)
			}
		case "attempt":
			if attemptIndex < 0 {
				attemptIndex = i
			}
		}
	}
	if configIndex < 0 || attemptIndex < 0 || configIndex > attemptIndex {
		t.Fatalf("config event index %d, first attempt index %d; the assignment must be journaled before the call", configIndex, attemptIndex)
	}
	config := events[configIndex]
	effective, ok := config["effective"].(map[string]any)
	if !ok {
		t.Fatalf("config event has no effective table: %v", config)
	}
	units, _ := effective["units"].([]any)
	if len(units) == 0 {
		t.Fatalf("effective table is empty: %v", effective)
	}
	found := false
	for _, row := range units {
		unit, _ := row.(map[string]any)
		if unit["unit"] != "P001.1.1" {
			continue
		}
		routes, _ := unit["routes"].([]any)
		if len(routes) == 1 {
			route, _ := routes[0].(map[string]any)
			found = route["model"] == "fast"
		}
	}
	if !found {
		t.Fatalf("config event does not carry the effective plan assignment: %#v", events)
	}
}

// The unit a controlled run just finished must not block the next start.
func TestControlledStartAfterCompletedUnit(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}
	controlRunSelf(t, f, nil)
	f.steps(writeReply("a.txt"))
	first := NewControl(64, 2)
	startControlledRun(t, f, first, args)
	awaitDone(t, first)
	if got := first.State(); got != StateFinished {
		t.Fatalf("first run state = %s, want Finished; err=%v", got, first.Err())
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.1"); err != nil || unit.Status != "x" {
		t.Fatalf("unit status = %q, err=%v", unit.Status, err)
	}

	preview, err := StartPreview(f.root, args)
	if err != nil {
		t.Fatalf("the start preview refused the next unit: %v", err)
	}
	if text := strings.Join(preview.Assignments, "\n"); strings.Contains(text, "P001.1.1") {
		t.Fatalf("the start preview still shows the finished unit:\n%s", text)
	}

	f.steps(writeReply("b.txt"))
	second := NewControl(64, 2)
	startControlledRun(t, f, second, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1})
	awaitDone(t, second)
	if got := second.State(); got != StateFinished {
		t.Fatalf("second run state = %s, want Finished; err=%v", got, second.Err())
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.2"); err != nil || unit.Status != "x" {
		t.Fatalf("second unit status = %q, err=%v", unit.Status, err)
	}
}

// Resume must ignore a leftover configuration.json instead of refusing the
// checkpoint or applying its route.
func TestResumeIgnoresStaleConfigurationFile(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	f.replace(planFile, "### [ ] P001.1.1", "### [x] P001.1.1")
	f.write(".git/docflow/tui/configuration.json", `{"version":1,"units":{"P001.1.2":[{"agent":"fake","model":"stale-model","effort":"high"}]}}`)
	head := f.git("rev-parse", "HEAD")
	if err := WriteCheckpoint(f.root, Checkpoint{
		Plan: "P001", Unit: "P001.1.2", Head: head,
		PlanHash: PlanFingerprint([]byte(f.read(planFile))),
		Stage:    "agent", Agent: "fake", Model: "", Effort: "", Calls: 1,
	}); err != nil {
		t.Fatal(err)
	}
	controlRunSelf(t, f, nil)
	f.steps(writeReply("b.txt"))
	control := NewControl(64, 2)
	if err := control.ResumeRun(context.Background(), f.root, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1}, ""); err != nil {
		t.Fatalf("ResumeRun refused an old configuration file: %v", err)
	}
	awaitDone(t, control)
	if got := control.State(); got != StateFinished {
		t.Fatalf("resumed state = %s, want Finished; err=%v", got, control.Err())
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.2"); err != nil || unit.Status != "x" {
		t.Fatalf("resumed unit status = %q, err=%v", unit.Status, err)
	}
	calls := f.calls()
	if len(calls) != 1 || strings.Contains(strings.Join(calls[0].Argv, " "), "stale-model") {
		t.Fatalf("resume applied the old configuration file: %#v", calls)
	}
}
