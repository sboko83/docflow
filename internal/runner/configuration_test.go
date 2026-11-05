package runner

import (
	"context"
	"os"
	"path/filepath"
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

func TestConfigurationEffectiveOverrides(t *testing.T) {
	plan := roleTestPlan(t)
	configuration := NewRunConfiguration()
	configuration.Roles["heavy"] = []agents.Route{{Agent: "claude", Model: "claude-opus-5-5", Effort: "high"}}
	configuration.Units["P001.1.3"] = []agents.Route{{Agent: "pi", Model: "zai/glm-5.3", Effort: "max"}}
	effective, err := EffectiveAssignments(plan, agents.RunArgs{Executors: true}, configAgents(), configuration)
	if err != nil {
		t.Fatal(err)
	}
	byUnit := map[string][]string{}
	for _, unit := range effective.Units {
		for _, route := range unit.Routes {
			byUnit[unit.Unit] = append(byUnit[unit.Unit], agents.RouteText(route))
		}
	}
	if got := byUnit["P001.1.1"]; len(got) != 1 || got[0] != "claude | claude-opus-5-5 | high" {
		t.Fatalf("role override = %v", got)
	}
	if got := byUnit["P001.1.2"]; len(got) != 3 || got[0] != "claude | claude-opus-5-5 | high" {
		t.Fatalf("explicit unit kept its own chain = %v", got)
	}
	if got := byUnit["P001.1.3"]; len(got) != 1 || got[0] != "pi | zai/glm-5.3 | max" {
		t.Fatalf("unit override = %v", got)
	}
	if got := effective.Roles["heavy"]; len(got) != 1 || got[0].Agent != "claude" {
		t.Fatalf("role chain = %#v", effective.Roles["heavy"])
	}
}

func TestConfigurationValidation(t *testing.T) {
	plan := roleTestPlan(t)
	donePlan := roleTestPlan(t)
	if err := donePlan.SetStatus("P001.1.1", "x"); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*RunConfiguration)
		want   string
	}{
		{"finished-unit", func(c *RunConfiguration) {
			c.Units["P001.1.1"] = []agents.Route{{Agent: "pi", Model: "m", Effort: "high"}}
		}, "cannot be reassigned"},
		{"unknown-unit", func(c *RunConfiguration) {
			c.Units["P001.9.9"] = []agents.Route{{Agent: "pi", Model: "m", Effort: "high"}}
		}, "not in P001"},
		{"unknown-role", func(c *RunConfiguration) {
			c.Roles["missing"] = []agents.Route{{Agent: "pi", Model: "m", Effort: "high"}}
		}, "role \"missing\" is not used"},
		{"unknown-agent", func(c *RunConfiguration) {
			c.Units["P001.1.1"] = []agents.Route{{Agent: "other", Model: "m", Effort: "high"}}
		}, "unknown agent"},
		{"missing-profile", func(c *RunConfiguration) {
			c.Units["P001.1.1"] = []agents.Route{{Agent: "opencode", Model: "m", Effort: "high"}}
		}, "has no profile"},
		{"empty-chain", func(c *RunConfiguration) {
			c.Units["P001.1.1"] = nil
		}, "at least one route"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := NewRunConfiguration()
			test.change(configuration)
			target := plan
			if test.name == "finished-unit" {
				target = donePlan
			}
			err := ValidateConfiguration(configuration, target, configAgents())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateConfiguration() error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestConfigurationRoundTripKeepsPlan(t *testing.T) {
	f := newRunFixture(t)
	plan, err := format.ParsePlan(f.read(planFile), planFile)
	if err != nil {
		t.Fatal(err)
	}
	before := f.git("status", "--porcelain")
	agentConfig, err := agents.RunConfig(plan, &agents.RunArgs{Config: f.config})
	if err != nil {
		t.Fatal(err)
	}
	configuration := NewRunConfiguration()
	configuration.Units["P001.1.1"] = []agents.Route{{Agent: "fake", Model: "override", Effort: "high"}}
	saved, err := SaveConfiguration(f.root, plan, agents.RunArgs{Config: f.config}, agentConfig, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Effective == nil || len(saved.Effective.Units) == 0 {
		t.Fatal("SaveConfiguration did not write an effective snapshot")
	}
	path, err := ConfigurationPath(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(path), "/.git/docflow/tui/configuration.json") {
		t.Fatalf("configuration path = %s; want under the git directory", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfiguration(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Units["P001.1.1"]) != 1 || loaded.Units["P001.1.1"][0].Model != "override" {
		t.Fatalf("loaded configuration = %#v", loaded)
	}
	if after := f.git("status", "--porcelain"); after != before {
		t.Fatalf("configuration changed the working tree: %q -> %q", before, after)
	}
}

func TestControlledRunUsesSavedConfiguration(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	controlRunSelf(t, f, nil)
	plan, err := format.ParsePlan(f.read(planFile), planFile)
	if err != nil {
		t.Fatal(err)
	}
	agentConfig, err := agents.RunConfig(plan, &agents.RunArgs{Config: f.config})
	if err != nil {
		t.Fatal(err)
	}
	configuration := NewRunConfiguration()
	configuration.Units["P001.1.1"] = []agents.Route{{Agent: "fake", Model: "override-model", Effort: "high"}}
	if _, err := SaveConfiguration(f.root, plan, agents.RunArgs{Config: f.config}, agentConfig, configuration); err != nil {
		t.Fatal(err)
	}
	f.steps(writeReply("a.txt"))
	control := NewControl(64, 2)
	startControlledRun(t, f, control, Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)})
	awaitDone(t, control)
	if got := control.State(); got != StateFinished {
		log, _ := os.ReadFile(filepath.Join(f.root, ".git/docflow/run/P001.1.1.1.out"))
		logErr, _ := os.ReadFile(filepath.Join(f.root, ".git/docflow/run/P001.1.1.1.out.stderr"))
		configText, _ := os.ReadFile(f.config)
		t.Fatalf("state = %s, want Finished; err=%v events=%v calls=%v log=%s stderr=%s config=%s", got, control.Err(), fixtureEvents(t, f), f.calls(), log, logErr, configText)
	}
	calls := f.calls()
	if len(calls) == 0 || !strings.Contains(strings.Join(calls[0].Argv, " "), "--model override-model") {
		t.Fatalf("agent argv = %#v; want the configured model", calls)
	}
	events := fixtureEvents(t, f)
	found := false
	configIndex, attemptIndex := -1, -1
	for i, record := range events {
		switch record["event"] {
		case "config":
			configIndex = i
		case "attempt":
			if attemptIndex < 0 {
				attemptIndex = i
			}
		}
		if record["event"] != "config" {
			continue
		}
		effective, ok := record["effective"].(map[string]any)
		if !ok {
			continue
		}
		units, _ := effective["units"].([]any)
		for _, row := range units {
			unit, _ := row.(map[string]any)
			if unit["unit"] != "P001.1.1" {
				continue
			}
			routes, _ := unit["routes"].([]any)
			if len(routes) == 1 {
				route, _ := routes[0].(map[string]any)
				found = route["model"] == "override-model"
			}
		}
	}
	if !found {
		t.Fatalf("config event does not carry the effective override: %#v", events)
	}
	if configIndex < 0 || attemptIndex < 0 || configIndex > attemptIndex {
		t.Fatalf("config event index %d, first attempt index %d; the assignment must be journaled before the call", configIndex, attemptIndex)
	}
}

func TestStartPreviewShowsEveryOpenAssignment(t *testing.T) {
	f := newRunFixture(t)
	plan, err := format.ParsePlan(f.read(planFile), planFile)
	if err != nil {
		t.Fatal(err)
	}
	agentConfig, err := agents.RunConfig(plan, &agents.RunArgs{Config: f.config})
	if err != nil {
		t.Fatal(err)
	}
	configuration := NewRunConfiguration()
	configuration.Units["P001.1.1"] = []agents.Route{{Agent: "fake", Model: "assigned-model", Effort: "high"}}
	if _, err := SaveConfiguration(f.root, plan, agents.RunArgs{Config: f.config}, agentConfig, configuration); err != nil {
		t.Fatal(err)
	}
	preview, err := StartPreview(f.root, Args{RunArgs: agents.RunArgs{Config: f.config}})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(preview.Assignments, "\n")
	for _, want := range []string{"P001.1.1", "P001.1.2", "fake | assigned-model | high", "fallback"} {
		if !strings.Contains(text, want) {
			t.Fatalf("start assignments do not contain %q:\n%s", want, text)
		}
	}
}

// A saved overlay outlives the run that completed its unit: reading it must
// drop the finished unit instead of refusing the next controlled start.
func TestCompletedUnitOverridesArePrunedOnRead(t *testing.T) {
	f := newRunFixture(t)
	args := Args{RunArgs: agents.RunArgs{Config: f.config}}
	configuration := NewRunConfiguration()
	configuration.Units["P001.1.1"] = []agents.Route{{Agent: "fake", Model: "stale-model", Effort: "high"}}
	configuration.Roles["ghost"] = []agents.Route{{Agent: "fake", Model: "role-model"}}
	if err := WriteConfiguration(f.root, configuration); err != nil {
		t.Fatal(err)
	}
	f.replace(planFile, "### [ ] P001.1.1", "### [x] P001.1.1")
	plan, err := format.ParsePlan(f.read(planFile), planFile)
	if err != nil {
		t.Fatal(err)
	}
	agentConfig, err := agents.RunConfig(plan, &agents.RunArgs{Config: f.config})
	if err != nil {
		t.Fatal(err)
	}
	// Validation stays strict: only reading prunes the stale entries.
	loaded := NewRunConfiguration()
	loaded.Units["P001.1.1"] = []agents.Route{{Agent: "fake", Model: "stale-model", Effort: "high"}}
	if err := ValidateConfiguration(loaded, plan, agentConfig); err == nil {
		t.Fatal("ValidateConfiguration accepted an override on a finished unit")
	}

	path, err := ConfigurationPath(f.root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := StartPreview(f.root, args)
	if err != nil {
		t.Fatalf("StartPreview refused a stale override: %v", err)
	}
	if text := strings.Join(preview.Assignments, "\n"); strings.Contains(text, "stale-model") {
		t.Fatalf("start assignments still show the finished unit:\n%s", text)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("StartPreview rewrote the saved configuration:\n%s\n---\n%s", before, after)
	}

	pruned, err := loadControlledConfiguration(f.root, plan, args.RunArgs, agentConfig)
	if err != nil {
		t.Fatalf("loadControlledConfiguration refused a stale override: %v", err)
	}
	if _, ok := pruned.Units["P001.1.1"]; ok {
		t.Fatalf("the finished unit survived in the overlay: %#v", pruned.Units)
	}
	if _, ok := pruned.Roles["ghost"]; ok {
		t.Fatalf("a role the plan does not use survived: %#v", pruned.Roles)
	}
	if pruned.Effective == nil {
		t.Fatal("the pruned overlay has no effective table")
	}
	for _, unit := range pruned.Effective.Units {
		if unit.Unit == "P001.1.1" {
			t.Fatal("the effective table still carries the finished unit")
		}
	}
	effective, err := EffectiveAssignments(plan, args.RunArgs, agentConfig, pruned)
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range effective.Units {
		if unit.Unit == "P001.1.1" {
			t.Fatal("EffectiveAssignments still carries the finished unit")
		}
	}
}

// The unit a controlled run just finished must not block the next start: the
// saved overlay is pruned, not rejected.
func TestControlledStartAfterCompletedUnit(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	args := Args{RunArgs: agents.RunArgs{Config: f.config}, Attempts: 1, MaxUnits: intPointer(1)}
	controlRunSelf(t, f, nil)
	plan, err := format.ParsePlan(f.read(planFile), planFile)
	if err != nil {
		t.Fatal(err)
	}
	agentConfig, err := agents.RunConfig(plan, &agents.RunArgs{Config: f.config})
	if err != nil {
		t.Fatal(err)
	}
	configuration := NewRunConfiguration()
	configuration.Units["P001.1.1"] = []agents.Route{{Agent: "fake", Model: "override-model", Effort: "high"}}
	if _, err := SaveConfiguration(f.root, plan, args.RunArgs, agentConfig, configuration); err != nil {
		t.Fatal(err)
	}
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
	saved, err := LoadConfiguration(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Units["P001.1.1"]; !ok {
		t.Fatal("the fixture lost the stale override the run leaves behind")
	}

	preview, err := StartPreview(f.root, args)
	if err != nil {
		t.Fatalf("the start preview refused the completed unit's override: %v", err)
	}
	if text := strings.Join(preview.Assignments, "\n"); strings.Contains(text, "override-model") {
		t.Fatalf("the start preview still shows the finished unit's route:\n%s", text)
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

// Resume reads the overlay again: a stale entry left by a finished unit must
// not turn R into an overlay refusal.
func TestResumeAfterCompletedUnitIgnoresStaleOverride(t *testing.T) {
	skipSlow(t)
	f := newRunFixture(t)
	f.replace(planFile, "### [ ] P001.1.1", "### [x] P001.1.1")
	stale := NewRunConfiguration()
	stale.Units["P001.1.1"] = []agents.Route{{Agent: "fake", Model: "stale-model", Effort: "high"}}
	if err := WriteConfiguration(f.root, stale); err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("ResumeRun refused the stale override: %v", err)
	}
	awaitDone(t, control)
	if got := control.State(); got != StateFinished {
		t.Fatalf("resumed state = %s, want Finished; err=%v", got, control.Err())
	}
	if _, unit, err := cycle.FindUnit(f.root, "P001.1.2"); err != nil || unit.Status != "x" {
		t.Fatalf("resumed unit status = %q, err=%v", unit.Status, err)
	}
	saved, err := LoadConfiguration(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Units["P001.1.1"]; ok {
		t.Fatalf("the resume kept the finished unit's override: %#v", saved.Units)
	}
}
