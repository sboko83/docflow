package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/agents"
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
