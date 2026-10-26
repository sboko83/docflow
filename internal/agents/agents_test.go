package agents

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/format"
)

func TestDefaultAgentsBytesAndSchema(t *testing.T) {
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(DefaultAgents))); got != "fd1b53eed2296b7c5a1047ed7512d83b928111288a33da14ebed0d37eabb4ebc" {
		t.Fatalf("DefaultAgents SHA-256 = %s", got)
	}
	parsed, err := ParseTOML(DefaultAgents)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAgents("", false); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(ResultSchema)
	if err != nil {
		t.Fatal(err)
	}
	var gotSchema map[string]any
	if err := json.Unmarshal(encoded, &gotSchema); err != nil {
		t.Fatal(err)
	}
	var wantSchema map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &wantSchema); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotSchema, wantSchema) {
		t.Fatalf("ResultSchema = %#v, want %#v", gotSchema, wantSchema)
	}
	if len(mapAt(parsed, "agents")) != 3 {
		t.Fatalf("built-in profiles = %d, want 3", len(mapAt(parsed, "agents")))
	}
}

func TestParseTOMLSubset(t *testing.T) {
	text := "" +
		"enabled = true\n" +
		"disabled = false\n" +
		"count = 1_024\n" +
		"escaped = \"line\\nquote\\u0021\"\n" +
		"joined = \"\"\"first \\\n  second\"\"\"\n" +
		"items = [\"a\",\n  # array comments are accepted\n  \"b\",]\n" +
		"inline = { name = \"one\", count = 3 }\n" +
		"[agents.codex.permissions]\n" +
		"auto = [\"--approve\", \"-c\"]\n"
	got, err := ParseTOML(text)
	if err != nil {
		t.Fatal(err)
	}
	if got["enabled"] != true || got["disabled"] != false || got["count"] != int64(1024) {
		t.Fatalf("scalar values = %#v", got)
	}
	if got["escaped"] != "line\nquote!" || got["joined"] != "first second" {
		t.Fatalf("string values = %#v", got)
	}
	if !reflect.DeepEqual(got["items"], []any{"a", "b"}) {
		t.Fatalf("array = %#v", got["items"])
	}
	if !reflect.DeepEqual(got["inline"], map[string]any{"name": "one", "count": int64(3)}) {
		t.Fatalf("inline table = %#v", got["inline"])
	}
	permissions := mapAt(mapAt(mapAt(got, "agents"), "codex"), "permissions")
	if !reflect.DeepEqual(permissions["auto"], []any{"--approve", "-c"}) {
		t.Fatalf("nested permission table = %#v", permissions)
	}
}

func TestParseTOMLErrors(t *testing.T) {
	for _, text := range []string{
		"[agents.codex\n",
		"value = [\"one\"\n",
		"value = { key = \"x\" }\nvalue = 1\n",
		"value = \"unterminated\n",
		"[[agents]]\n",
	} {
		if _, err := ParseTOML(text); err == nil {
			t.Errorf("ParseTOML(%q) succeeded", text)
		}
	}
}

func TestUnclosedArrayTableDiagnostic(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"[[broken\n", "Expected ']]' at the end of an array declaration (at line 1, column 9)"},
		{"\n[[agents.codex]\n", "Expected ']]' at the end of an array declaration (at line 2, column 15)"},
		{"[[agents.codex", "Expected ']]' at the end of an array declaration (at end of document)"},
	} {
		_, err := ParseTOML(tc.text)
		if err == nil || err.Error() != tc.want {
			t.Errorf("ParseTOML(%q) error = %v; want %s", tc.text, err, tc.want)
		}
	}
}

func TestLoadAgentsMergeAndValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.toml")
	writeConfig(t, path, "[agents.claude]\nmodel = \"strong\"\n[tiers]\nfast = {model = \"x\"}\n")
	config, err := LoadAgents(path, true)
	if err != nil {
		t.Fatal(err)
	}
	claude := mapAt(mapAt(config, "agents"), "claude")
	builtin, err := ParseTOML(DefaultAgents)
	if err != nil {
		t.Fatal(err)
	}
	if claude["model"] != "strong" || !reflect.DeepEqual(claude["cmd"], mapAt(mapAt(builtin, "agents"), "claude")["cmd"]) {
		t.Fatalf("claude override = %#v", claude)
	}
	if !reflect.DeepEqual(mapAt(config, "tiers")["fast"], map[string]any{"agent": "claude", "model": "x"}) {
		t.Fatalf("fast tier = %#v", mapAt(config, "tiers")["fast"])
	}
	if mapAt(mapAt(config, "tiers"), "max")["effort"] != "xhigh" {
		t.Fatal("unmodified built-in max tier was lost")
	}

	writeConfig(t, path, "[agents.claude.model_extra]\nclaude-haiku-x = [\"--allowedTools\", \"Bash\"]\n[agents.claude.permissions]\nfull = [\"--permission-mode\", \"acceptEdits\"]\n")
	config, err = LoadAgents(path, true)
	if err != nil {
		t.Fatal(err)
	}
	claude = mapAt(mapAt(config, "agents"), "claude")
	if _, ok := mapAt(claude, "model_extra")["haiku"]; !ok || mapAt(claude, "model_extra")["claude-haiku-x"] == nil {
		t.Fatalf("model_extra was not merged by key: %#v", claude["model_extra"])
	}
	permissions := mapAt(claude, "permissions")
	if permissions["full"] == nil || permissions["auto"] == nil {
		t.Fatalf("permissions was not merged by key: %#v", permissions)
	}

	writeConfig(t, path, "[tiers]\ndefault = {agent = \"echo\"}\n[agents.echo]\ncmd = [\"echo\"]\n")
	config, err = LoadAgents(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if mapAt(mapAt(config, "tiers"), "default")["agent"] != "echo" {
		t.Fatalf("new tier = %#v", mapAt(config, "tiers")["default"])
	}
	if _, ok := mapAt(config, "agents")["claude"]; !ok {
		t.Fatal("built-in profiles were not retained")
	}
}

func TestLoadAgentsValidationErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.toml")
	assertBad := func(text, message string) {
		t.Helper()
		writeConfig(t, path, text)
		_, err := LoadAgents(path, true)
		if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), message) {
			t.Fatalf("LoadAgents(%q) error = %v, want path and %q", text, err, message)
		}
		var docflowErr clierrors.DocflowError
		if !errors.As(err, &docflowErr) || docflowErr.ExitCode() != clierrors.EXIT_FORMAT {
			t.Fatalf("LoadAgents error = %T %v, want format error", err, err)
		}
	}
	for _, test := range []struct{ text, message string }{
		{"default_agent = \"claude\"\n", "'default_agent' moved to [tiers.default]"},
		{"[agents.claude.models]\nfast = \"x\"\n", "'models' moved to [tiers]"},
		{"[agents.claude]\npermissions = \"auto\"\n", "'permissions' must be dict"},
		{"[agents.claude.permissions]\nauto = \"x\"\n", "'permissions' must be a table of string lists"},
		{"[agents.claude]\nmodle = \"x\"\n", "unknown key 'modle'"},
		{"[agents.claude]\ntimeout = \"1\"\n", "'timeout' must be int"},
		{"[agents.claude]\ntimeout = true\n", "'timeout' must be int"},
		{"[agents.x]\ncmd = []\n", "cmd must be a non-empty"},
		{"[agents.x]\ncmd = [\"a\", 1]\n", "'cmd' must be list"},
		{"[agents.claude]\nstream = \"x\"\n", "'stream' must be"},
		{"[agents.claude]\nmodel_extra = {haiku = \"x\"}\n", "'model_extra' must be a table of string lists"},
		{"[agents.claude]\nmodel_extra = \"x\"\n", "'model_extra' must be dict"},
		{"extra = 1\n", "unknown key 'extra'"},
		{"[tiers]\nodd = {agent = \"claude\"}\n", "unknown tier 'odd'"},
		{"[tiers]\nfast = {size = \"x\"}\n", "unknown key 'size'"},
		{"[tiers]\nfast = {effort = 1}\n", "'effort' must be str"},
		{"[tiers]\nfast = {agent = \"nope\"}\n", "agent 'nope' has no profile"},
	} {
		t.Run(test.message, func(t *testing.T) { assertBad(test.text, test.message) })
	}
	if _, err := LoadAgents(filepath.Join(filepath.Dir(path), "missing.toml"), true); err == nil || !strings.Contains(err.Error(), "agents config not found") {
		t.Fatalf("missing required file error = %v", err)
	}
	writeConfig(t, path, "[agents.claude\n")
	if _, err := LoadAgents(path, true); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("invalid TOML error = %v", err)
	}
}

func TestConfigPath(t *testing.T) {
	t.Setenv("DOCFLOW_AGENTS", "/a.toml")
	if got := ConfigPath("/b.toml"); got != "/b.toml" {
		t.Fatalf("explicit config = %q", got)
	}
	if got := ConfigPath(""); got != "/a.toml" {
		t.Fatalf("DOCFLOW_AGENTS config = %q", got)
	}
	t.Setenv("DOCFLOW_AGENTS", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := ConfigPath(""); got != filepath.Join("/xdg", "docflow", "agents.toml") {
		t.Fatalf("XDG config = %q", got)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := ConfigPath("~/a.toml"); got != filepath.Join(home, "a.toml") {
		t.Fatalf("expanded config = %q", got)
	}
	if got := ConfigPath("~"); got != home {
		t.Fatalf("expanded home = %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := ConfigPath(""); got != filepath.Join(home, ".config", "docflow", "agents.toml") {
		t.Fatalf("default config = %q", got)
	}
	account, err := user.Current()
	// Windows account names are DOMAIN\user: no ~user form to expand.
	if err == nil && runtime.GOOS != "windows" {
		if got := ConfigPath("~" + account.Username); got != account.HomeDir {
			t.Fatalf("expanded named home = %q, want %q", got, account.HomeDir)
		}
	}
}

func TestResolveRoute(t *testing.T) {
	config, err := ParseTOML("[tiers]\nfast = {agent = \"a\", model = \"haiku\"}\ndefault = {agent = \"a\", model = \"sonnet\"}\nstrong = {agent = \"b\", model = \"big\", effort = \"high\"}\n[agents.a]\ncmd = [\"a\"]\nmodel = \"amodel\"\n[agents.b]\ncmd = [\"b\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	unit := func(model string) *format.Unit {
		if model == "" {
			return &format.Unit{}
		}
		return &format.Unit{Fields: []format.Field{{Key: "Model", Value: model}}}
	}
	tests := []struct {
		name string
		unit string
		args RunArgs
		want Route
	}{
		{"arg tier wins", "strong", RunArgs{Model: "fast"}, Route{Agent: "a", Model: "haiku"}},
		{"unit tier", "strong", RunArgs{}, Route{Agent: "b", Model: "big", Effort: "high"}},
		{"literal model", "", RunArgs{Model: "gpt-x"}, Route{Agent: "a", Model: "gpt-x"}},
		{"literal beats unit tier", "strong", RunArgs{Model: "m1"}, Route{Agent: "a", Model: "m1"}},
		{"default tier", "", RunArgs{}, Route{Agent: "a", Model: "sonnet"}},
		{"agent drops tier model", "strong", RunArgs{Agent: "a"}, Route{Agent: "a", Model: "amodel"}},
		{"agent without model", "strong", RunArgs{Agent: "b"}, Route{Agent: "b"}},
		{"agent keeps literal model", "strong", RunArgs{Model: "m1", Agent: "b"}, Route{Agent: "b", Model: "m1"}},
		{"effort override", "strong", RunArgs{Effort: "low"}, Route{Agent: "b", Model: "big", Effort: "low"}},
		{"effort with default", "", RunArgs{Effort: "max"}, Route{Agent: "a", Model: "sonnet", Effort: "max"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveRoute(config, unit(test.unit), test.args)
			if err != nil || got != test.want {
				t.Fatalf("ResolveRoute() = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
	if _, err := ResolveRoute(config, unit(""), RunArgs{Agent: "nope"}); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("unknown profile error = %v", err)
	}
}

func TestBuildCommand(t *testing.T) {
	agent := map[string]any{
		"cmd":         []any{"tool", "--schema", "{schema}", "--file={schema_file}"},
		"model_args":  []any{"--model", "{model}"},
		"resume_args": []any{"--resume", "{session}"},
	}
	argv, stdin, err := BuildCommand(agent, "P {model}", "", "/s.json", "", "", "auto")
	if err != nil || stdin == nil || *stdin != "P {model}" {
		t.Fatalf("BuildCommand stdin = %#v, %v", stdin, err)
	}
	if argv[3] != "--file=/s.json" || strings.Contains(strings.Join(argv, " "), "--model") {
		t.Fatalf("BuildCommand argv = %#v", argv)
	}
	var gotSchema map[string]any
	if err := json.Unmarshal([]byte(argv[2]), &gotSchema); err != nil {
		t.Fatalf("inline schema is invalid JSON: %v", err)
	}
	var wantSchema map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &wantSchema); err != nil || !reflect.DeepEqual(gotSchema, wantSchema) {
		t.Fatalf("inline schema = %#v, %v", gotSchema, err)
	}

	config, err := LoadAgents("", false)
	if err != nil {
		t.Fatal(err)
	}
	agents := mapAt(config, "agents")
	claude := mapAt(agents, "claude")
	if values, _ := stringList(claude["cmd"]); contains(values, "--permission-mode") {
		t.Fatalf("permission args unexpectedly live in cmd: %#v", values)
	}
	if !reflect.DeepEqual(mapAt(claude, "permissions"), map[string]any{
		"auto": []any{"--permission-mode", "auto", "--permission-prompts", "none"},
		"full": []any{"--permission-mode", "bypassPermissions"},
	}) {
		t.Fatalf("Claude permission table = %#v", claude["permissions"])
	}
	argv, _, err = BuildCommand(claude, "P", "haiku", "/s", "", "", "auto")
	if err != nil || len(argv) < 11 || !reflect.DeepEqual(argv[:5], []string{"claude", "-p", "--output-format", "stream-json", "--verbose"}) || !reflect.DeepEqual(argv[7:11], []string{"--permission-mode", "auto", "--permission-prompts", "none"}) || !reflect.DeepEqual(argv[len(argv)-6:], []string{"--model", "haiku", "--allowedTools", "Write", "Edit", "Bash"}) {
		t.Fatalf("claude haiku argv suffix = %#v, %v", argv, err)
	}
	argv, _, err = BuildCommand(claude, "P", "opus", "/s", "", "xhigh", "full")
	if err != nil || !contains(argv, "bypassPermissions") || contains(argv, "--permission-prompts") || contains(argv, "auto") || !contains(argv, "xhigh") {
		t.Fatalf("claude full argv = %#v, %v", argv, err)
	}
	codex := mapAt(agents, "codex")
	argv, stdin, err = BuildCommand(codex, "P", "gpt-6-luna", "/s.json", "t1", "low", "auto")
	if err != nil || stdin == nil || len(argv) < 7 || !reflect.DeepEqual(argv[:7], []string{"codex", "exec", "--json", "--output-schema", "/s.json", "--approve-for-me", "-c"}) || !contains(argv, "sandbox_workspace_write.network_access=true") {
		t.Fatalf("codex argv = %#v, stdin=%v, err=%v", argv, stdin, err)
	}
	if got := argv[len(argv)-6:]; !reflect.DeepEqual(got, []string{"-m", "gpt-6-luna", "-c", "model_reasoning_effort=low", "resume", "t1"}) {
		t.Fatalf("codex model/session args = %#v", got)
	}
	argv, _, err = BuildCommand(codex, "P", "", "/s.json", "", "", "full")
	if err != nil || !contains(argv, "--dangerously-bypass-approvals-and-sandbox") || contains(argv, "--approve-for-me") || contains(argv, "sandbox_workspace_write.network_access=true") {
		t.Fatalf("codex full argv = %#v, %v", argv, err)
	}
	pi := mapAt(agents, "pi")
	argv, stdin, err = BuildCommand(pi, "P", "zai/glm-5.3", "/s", "s1", "high", "auto")
	if err != nil || stdin == nil || !reflect.DeepEqual(argv, []string{"pi", "-p", "--mode", "json", "--model", "zai/glm-5.3", "--thinking", "high", "--session", "s1"}) {
		t.Fatalf("pi argv = %#v, stdin=%v, err=%v", argv, stdin, err)
	}
	argv, _, err = BuildCommand(pi, "P", "zai/glm-5.3", "/s", "", "", "full")
	if err != nil || !reflect.DeepEqual(argv, []string{"pi", "-p", "--mode", "json", "--approve", "--model", "zai/glm-5.3"}) {
		t.Fatalf("pi full argv = %#v, %v", argv, err)
	}

	promptAgent := map[string]any{"cmd": []any{"tool", "run", "{prompt}"}, "model_args": []any{"-m", "{model}"}}
	argv, stdin, err = BuildCommand(promptAgent, `do {model} {"a": 1}`, "m", "/s", "", "", "auto")
	if err != nil || stdin != nil || !reflect.DeepEqual(argv, []string{"tool", "run", `do {model} {"a": 1}`, "-m", "m"}) {
		t.Fatalf("prompt argument = %#v, stdin=%v, err=%v", argv, stdin, err)
	}

	bare := map[string]any{"cmd": []any{"tool"}}
	argv, _, err = BuildCommand(bare, "P", "", "/s", "", "", "auto")
	if err != nil || !reflect.DeepEqual(argv, []string{"tool"}) {
		t.Fatalf("bare profile auto = %#v, %v", argv, err)
	}
	if _, _, err := BuildCommand(bare, "P", "", "/s", "", "", "full"); err == nil || !strings.Contains(err.Error(), "no 'permissions' table") {
		t.Fatalf("bare profile full error = %v", err)
	}
	effortAgent := map[string]any{"cmd": []any{"t"}, "effort_args": []any{"-e", "{effort}"}}
	argv, _, err = BuildCommand(effortAgent, "P", "", "/s", "", "", "auto")
	if err != nil || !reflect.DeepEqual(argv, []string{"t"}) {
		t.Fatalf("effort args without effort = %#v, %v", argv, err)
	}
	argv, _, err = BuildCommand(effortAgent, "P", "", "/s", "", "high", "auto")
	if err != nil || !reflect.DeepEqual(argv, []string{"t", "-e", "high"}) {
		t.Fatalf("effort args with effort = %#v, %v", argv, err)
	}
}

const routePlanText = `---
id: P001
title: Plan
type: feature
status: active
created: 2026-10-20
executor: claude | claude-opus-5-5 | high
---
## Goal
Goal.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: First

### [x] P001.1.1 Done unit
Read:
- a.txt
Do:
- write a.txt
Verify: ` + "`test -f a.txt`" + `
Model: strong
Commit: P001.1.1: done unit

### [ ] P001.1.2 Inherits
Read:
- a.txt
Do:
- write b.txt
Verify: ` + "`test -f b.txt`" + `
Commit: P001.1.2: inherits

### [ ] P001.1.3 Overrides
Read:
- a.txt
Do:
- write c.txt
Verify: ` + "`test -f c.txt`" + `
Executor: codex | gpt-5.5 | -
Commit: P001.1.3: overrides
`

func parseRoutePlan(t *testing.T, text string) *format.Plan {
	t.Helper()
	plan, err := format.ParsePlan(text, "docs/plans/P001-plan.md")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPlanAgentsCoversChains(t *testing.T) {
	plan := parseRoutePlan(t, `---
id: P001
title: Plan
type: feature
status: active
created: 2026-10-24
executor: pi | zai/glm-5.3 | max
fallback: claude | claude-haiku-5-5 | low
heavy: pi | glm-5.3 | high; codex | gpt-6.1-sol | high
replan: pi | glm-5.3 | max; claude | claude-opus-5-5 | high
---
## Goal

Goal.

## Context

## Constraints

## Traps

## Decisions

## Phase 1: First

### [ ] P001.1.1 Role unit
Read:
- a.txt
Do:
- work
Verify: `+"`true`"+`
Role: heavy
Commit: P001.1.1: role unit

### [x] P001.1.2 Closed unit
Read:
- a.txt
Do:
- work
Verify: `+"`true`"+`
Executor: pi | glm-5.3 | high
Commit: P001.1.2: closed unit
`)
	// codex appears only as the second route of the heavy chain, and
	// claude only as the second route of the replan chain (no open unit
	// consumes fallback); a truncated chain drops its marker agent.
	got := PlanAgents(nil, plan, RunArgs{Executors: true})
	if want := []string{"claude", "codex", "pi"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PlanAgents() = %#v, want %#v", got, want)
	}
}

func TestRunRouteAndExecutorIntegration(t *testing.T) {
	plan := parseRoutePlan(t, routePlanText)
	args := RunArgs{Executors: true}
	if err := CheckExecutors(plan); err != nil {
		t.Fatal(err)
	}
	got, err := RunRoute(nil, plan, plan.Units[2], args)
	if err != nil || got != (Route{Agent: "codex", Model: "gpt-5.5"}) {
		t.Fatalf("RunRoute() = %#v, %v", got, err)
	}
	if RouteSource(plan, plan.Units[2]) != "unit" || RouteSource(plan, plan.Units[1]) != "plan" || RouteText(got) != "codex | gpt-5.5 | -" {
		t.Fatalf("route metadata = %q / %q / %q", RouteSource(plan, plan.Units[2]), RouteSource(plan, plan.Units[1]), RouteText(got))
	}
	config, err := RunConfig(plan, &args)
	if err != nil || !args.Executors || len(mapAt(config, "agents")) != 3 {
		t.Fatalf("RunConfig executor mode = %#v, %#v, %v", config, args, err)
	}
	args.Agent = "codex"
	if _, err := RunConfig(plan, &args); err == nil || !strings.Contains(err.Error(), "--agent would replace them") {
		t.Fatalf("executor flag error = %v", err)
	}
	legacy := strings.Replace(routePlanText, "executor: claude | claude-opus-5-5 | high\n", "", 1)
	legacy = strings.Replace(legacy, "Executor: codex | gpt-5.5 | -\n", "", 1)
	legacyPlan := parseRoutePlan(t, legacy)
	if _, err := RunRoute(nil, legacyPlan, legacyPlan.Units[1], RunArgs{Executors: true}); err == nil || !strings.Contains(err.Error(), "changed its executor assignment") {
		t.Fatalf("executor mode changed error = %v", err)
	}
	if got := PlanAgents(nil, plan, RunArgs{Executors: true}); !reflect.DeepEqual(got, []string{"claude", "codex"}) {
		t.Fatalf("PlanAgents() = %#v", got)
	}
}

func TestRunRoutePiTriple(t *testing.T) {
	text := strings.Replace(routePlanText, "Executor: codex | gpt-5.5 | -\n", "Executor: pi | zai/glm-5.3 | high\n", 1)
	plan := parseRoutePlan(t, text)
	if err := CheckExecutors(plan); err != nil {
		t.Fatal(err)
	}
	route, err := RunRoute(nil, plan, plan.Units[2], RunArgs{Executors: true})
	if err != nil || route != (Route{Agent: "pi", Model: "zai/glm-5.3", Effort: "high"}) {
		t.Fatalf("RunRoute() = %#v, %v", route, err)
	}
	config, err := LoadAgents("", false)
	if err != nil {
		t.Fatal(err)
	}
	pi := mapAt(mapAt(config, "agents"), "pi")
	if _, exists := pi["quota"]; exists {
		t.Fatalf("Pi profile unexpectedly has quota: %#v", pi["quota"])
	}
	argv, _, err := BuildCommand(pi, "P", route.Model, "/s", "", route.Effort, "auto")
	if err != nil || !reflect.DeepEqual(argv[len(argv)-4:], []string{"--model", "zai/glm-5.3", "--thinking", "high"}) {
		t.Fatalf("Pi command = %#v, %v", argv, err)
	}
}

func TestRunRouteModeFixedForRun(t *testing.T) {
	legacy := strings.Replace(routePlanText, "executor: claude | claude-opus-5-5 | high\n", "", 1)
	legacy = strings.Replace(legacy, "Executor: codex | gpt-5.5 | -\n", "", 1)
	legacyPlan := parseRoutePlan(t, legacy)
	if _, err := RunRoute(nil, legacyPlan, legacyPlan.Units[1], RunArgs{Executors: true}); err == nil || !strings.Contains(err.Error(), "changed its executor assignment") {
		t.Fatalf("legacy plan mode error = %v", err)
	}
	executorPlan := parseRoutePlan(t, routePlanText)
	if _, err := RunRoute(nil, executorPlan, executorPlan.Units[1], RunArgs{}); err == nil || !strings.Contains(err.Error(), "changed its executor assignment") {
		t.Fatalf("executor plan mode error = %v", err)
	}
}

func TestStrictRunnerSchema(t *testing.T) {
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(RunnerCycle))); got != "151eb2c681b82e74b1f245d4e63c12505f88a646415325b58b29a9ef46c4bcdd" {
		t.Fatalf("RunnerCycle SHA-256 = %s", got)
	}
	if !strings.Contains(RunnerCycle, `"reason": ""`) || !strings.Contains(RunnerCycle, `"blocker": "plan"`) {
		t.Fatalf("RunnerCycle missing result schema examples:\n%s", RunnerCycle)
	}
	properties := mapAt(ResultSchema, "properties")
	values, ok := ResultSchema["required"].([]any)
	if !ok || len(values) != len(properties) {
		t.Fatalf("required schema keys = %#v", ResultSchema["required"])
	}
	if ResultSchema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v", ResultSchema["additionalProperties"])
	}
}

func TestRunSelection(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, filepath.Join(root, "docs", "docflow.md"), "---\ndocflow: 3\nread_budget: 10000\n---\n## Queue\n- P001\n\n## Backlog\n")
	planPath := filepath.Join(root, "docs", "plans", "P001-plan.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte(routePlanText), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Docflow Tests"}, {"config", "user.email", "docflow-tests@example.com"}, {"add", "docs"}, {"commit", "-m", "fixture"}} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	selected, err := RunSelection(root, RunArgs{Plan: "P001"})
	if err != nil || selected.Kind != "unit" || selected.Unit.ID != "P001.1.2" {
		t.Fatalf("explicit plan selection = %#v, %v", selected, err)
	}
	selected, err = RunSelection(root, RunArgs{})
	if err != nil || selected.Kind != "unit" || selected.Unit.ID != "P001.1.2" {
		t.Fatalf("queued plan selection = %#v, %v", selected, err)
	}
	if _, err := RunSelection(root, RunArgs{Plan: "bad"}); err == nil || !strings.Contains(err.Error(), "not a plan id") {
		t.Fatalf("invalid plan id error = %v", err)
	}
}

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
