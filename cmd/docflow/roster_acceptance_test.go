package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/roster"
)

// The roster acceptance tests drive the in-process CLI on a temporary
// docflow: 3 project; the roster is the v2 draft of P036 ## Context
// (internal/roster/testdata/draft.toml) together with its catalog fixture
// in a temporary XDG_CONFIG_HOME and XDG cache. The v1 draft of the same
// scenario lives in draft-v1.toml and is read by the migration tests and
// by legacy_roster_test.go.

// rosterTable is the expected chain table of the v2 draft roster: zai is
// the check access, so it serves ordinary units but never judges; codex
// and claude are the trusted judges, so acceptance and replan hold both.
var rosterTable = map[string]string{
	"cheap":      "pi | zai/glm-5.3-flash | max; codex | gpt-6-luna | max; claude | claude-haiku-5-5 | -",
	"executor":   "pi | zai/glm-5.3-flash | max; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high",
	"heavy":      "pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high",
	"review":     "codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high",
	"acceptance": "codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high",
	"replan":     "codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high",
}

// rosterEnv isolates the CLI from the personal configuration and installs
// rosterText as $XDG_CONFIG_HOME/docflow/roster.toml; the catalog cache of
// the call is an empty directory under $XDG_CACHE_HOME.
func rosterEnv(t *testing.T, rosterText string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("DOCFLOW_AGENTS", filepath.Join(home, "missing.toml"))
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	t.Setenv("DOCFLOW_SKILLS_CHECK", "0")
	t.Setenv("DOCFLOW_NOTIFY", "0")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	writeLifecycleFile(t, home, "docflow/roster.toml", rosterText)
}

// rosterAcceptanceEnv installs the v2 draft roster together with its
// catalog: the catalog decides the models and the efforts the resolver
// picks, so the test never reads the cache of the machine it runs on.
func rosterAcceptanceEnv(t *testing.T) {
	t.Helper()
	rosterEnv(t, draftRoster(t))
	writeLifecycleFile(t, os.Getenv("HOME"), "cache/docflow/roster/catalog.json", rosterFixture(t, "draft-catalog.json"))
}

// rosterFixture reads a roster package fixture: the tests of this package
// read the same files the resolver tests do.
func rosterFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "roster", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// draftRoster is the v2 draft roster of P036 ## Context.
func draftRoster(t *testing.T) string {
	t.Helper()
	return rosterFixture(t, "draft.toml")
}

// jsonBriefChains renders the role chains of a JSON brief the way the plan
// header writes them, so the table stays readable.
func jsonBriefChains(t *testing.T, stdout string) map[string]string {
	t.Helper()
	var brief struct {
		Roles []struct {
			Role   string `json:"role"`
			Routes []struct {
				Agent  string `json:"agent"`
				Model  string `json:"model"`
				Effort string `json:"effort"`
			} `json:"routes"`
		} `json:"roles"`
	}
	if err := json.Unmarshal([]byte(stdout), &brief); err != nil {
		t.Fatalf("brief JSON: %v\n%s", err, stdout)
	}
	chains := make(map[string]string, len(brief.Roles))
	for _, entry := range brief.Roles {
		texts := make([]string, 0, len(entry.Routes))
		for _, route := range entry.Routes {
			texts = append(texts, agents.RouteText(agents.Route{Agent: route.Agent, Model: route.Model, Effort: route.Effort}))
		}
		chains[entry.Role] = strings.Join(texts, "; ")
	}
	return chains
}

// rosterUnits are one unit without a role and one per role key; the heavy
// unit goes first so a single-unit dry-run shows the role source.
const rosterHeavyUnit = "### [ ] P001.1.1 Heavy unit\nRead:\n- a.py\nDo:\n- edit a.py\nVerify: `true`\nRole: heavy\nCommit: P001.1.1: heavy\n\n"
const rosterPlainUnit = "### [ ] P001.1.2 Plain unit\nRead:\n- a.py\nDo:\n- edit a.py\nVerify: `true`\nCommit: P001.1.2: plain\n\n"
const rosterReviewUnit = "### [ ] P001.1.3 Review unit\nRead:\n- a.py\nDo:\n- review a.py\nVerify: `true`\nRole: review\nCommit: P001.1.3: review\n\n"
const rosterAcceptanceUnit = "### [ ] P001.1.4 Acceptance unit\nRead:\n- a.py\nDo:\n- accept a.py\nVerify: `true`\nRole: acceptance\nCommit: P001.1.4: acceptance\n\n"
const rosterCheapUnit = "### [ ] P001.1.5 Cheap unit\nRead:\n- a.py\nDo:\n- rename a.py\nVerify: `true`\nRole: cheap\nCommit: P001.1.5: cheap\n"

const rosterUnits = rosterHeavyUnit + rosterPlainUnit + rosterReviewUnit + rosterAcceptanceUnit

// rosterConditionUnits use the roles the acceptance scenario keeps: the
// heavy chain is empty there, so the plan has no heavy unit.
const rosterConditionUnits = rosterPlainUnit + rosterReviewUnit + rosterAcceptanceUnit + rosterCheapUnit

func rosterPlan(meta string) string {
	return rosterPlanUnits(meta, rosterUnits)
}

func rosterPlanUnits(meta, units string) string {
	return "---\nid: P001\ntitle: Roster\ntype: feature\nstatus: active\ncreated: 2026-10-12\n" + meta + "---\n" +
		"## Goal\nGoal.\n\n## Context\n\n## Constraints\n\n## Prerequisites\n\n## Traps\n\n## Decisions\n- roster decision — why — alternative\n\n## Phase 1: First\n\n" + units
}

const rosterPlanPath = "docs/plans/P001-slug.md"

func rosterProject(t *testing.T, plan string) string {
	t.Helper()
	root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": plan}, true)
	initLifecycleGit(t, root)
	return root
}

func readRosterFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func commitRosterProject(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "roster"}} {
		if output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
}

// headerLines picks the role-chain lines of a plan header.
func headerLines(text string) []string {
	var lines []string
	header, _, _ := strings.Cut(text, "\n---\n")
	for line := range strings.SplitSeq(header, "\n") {
		for _, key := range []string{"executor", "fallback", "cheap", "heavy", "review", "acceptance", "replan"} {
			if strings.HasPrefix(line, key+": ") {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func TestRosterAcceptanceBriefMatchesTable(t *testing.T) {
	rosterAcceptanceEnv(t)
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "brief", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("roster brief = (%d, %q, %q)", code, stdout, stderr)
	}
	got := jsonBriefChains(t, stdout)
	if len(got) != len(rosterTable) {
		t.Fatalf("roles = %v, want %d roles:\n%s", got, len(rosterTable), stdout)
	}
	for role, want := range rosterTable {
		if got[role] != want {
			t.Errorf("%s chain = %q, want %q", role, got[role], want)
		}
	}
}

func TestRosterAcceptanceApplyLintDryRun(t *testing.T) {
	rosterAcceptanceEnv(t)
	root := rosterProject(t, rosterPlan(""))
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001")
	if code != 0 || !strings.Contains(stdout, "commit: P001: apply roster") {
		t.Fatalf("roster apply = (%d, %q, %q)", code, stdout, stderr)
	}
	executor, fallback, _ := strings.Cut(rosterTable["executor"], "; ")
	// No unit has Role: cheap, so apply writes no cheap: key.
	want := []string{"executor: " + executor, "fallback: " + fallback}
	for _, role := range []string{"heavy", "review", "acceptance", "replan"} {
		want = append(want, role+": "+rosterTable[role])
	}
	if got := headerLines(readRosterFile(t, root, rosterPlanPath)); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("header =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("lint after apply = (%d, %q, %q)", code, stdout, stderr)
	}
	commitRosterProject(t, root)

	code, stdout, stderr = callLifecycleCLI(t, root, "run", "--dry-run", "--all")
	if code != 0 {
		t.Fatalf("dry-run --all = (%d, %q, %q)", code, stdout, stderr)
	}
	first := func(chain string) string { return strings.SplitN(chain, "; ", 2)[0] }
	spaced := func(chain string) string { return strings.ReplaceAll(chain, " | ", " ") }
	for _, line := range []string{
		"P001.1.1 [ ] Heavy unit: " + first(rosterTable["heavy"]) + " (role heavy)",
		"P001.1.2 [ ] Plain unit: " + executor + " (plan)",
		"P001.1.3 [ ] Review unit: " + first(rosterTable["review"]) + " (role review)",
		"P001.1.4 [ ] Acceptance unit: " + first(rosterTable["acceptance"]) + " (role acceptance)",
		"fallback: " + spaced(fallback),
		"heavy: " + spaced(rosterTable["heavy"]),
		"review: " + spaced(rosterTable["review"]),
		"acceptance: " + spaced(rosterTable["acceptance"]),
		"replan: " + spaced(rosterTable["replan"]),
	} {
		if !strings.Contains(stdout, line+"\n") {
			t.Errorf("dry-run --all lacks %q:\n%s", line, stdout)
		}
	}

	// Single-unit dry-run of the next unit (a Role unit) names the role.
	code, stdout, stderr = callLifecycleCLI(t, root, "run", "--dry-run")
	if code != 0 || !strings.Contains(stdout, "unit: P001.1.1 Heavy unit\nagent: pi\nmodel: zai/glm-5.3\neffort: max\nfrom: role heavy\n") {
		t.Fatalf("single dry-run = (%d, %q, %q)", code, stdout, stderr)
	}

	before := readRosterFile(t, root, rosterPlanPath)
	code, stdout, stderr = callLifecycleCLI(t, root, "roster", "apply", "P001")
	if code != 1 || !strings.Contains(stdout, "header already matches the roster") {
		t.Fatalf("repeat apply = (%d, %q, %q)", code, stdout, stderr)
	}
	if readRosterFile(t, root, rosterPlanPath) != before {
		t.Fatal("repeat apply changed the plan")
	}
}

// TestRosterAcceptancePresetReordersChains: a v2 preset reorders the
// policy accesses for one call, and the v1 --profile flag is refused with
// a pointer to it.
func TestRosterAcceptancePresetReordersChains(t *testing.T) {
	rosterAcceptanceEnv(t)
	root := rosterProject(t, rosterPlan(""))
	if code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001"); code != 0 {
		t.Fatalf("roster apply = (%d, %q, %q)", code, stdout, stderr)
	}
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001", "--preset", "codex-first")
	if code != 0 || !strings.Contains(stdout, "executor: pi | zai/glm-5.3-flash | max → codex | gpt-6-luna | max") {
		t.Fatalf("roster apply --preset = (%d, %q, %q)", code, stdout, stderr)
	}
	want := []string{
		"executor: codex | gpt-6-luna | max",
		"fallback: pi | zai/glm-5.3-flash | max; claude | claude-sonnet-5-5 | high",
		"heavy: codex | gpt-6.1-sol | high; pi | zai/glm-5.3 | max; claude | claude-opus-5-5 | high",
		"review: " + rosterTable["review"],
		"acceptance: " + rosterTable["acceptance"],
		"replan: " + rosterTable["replan"],
	}
	if got := headerLines(readRosterFile(t, root, rosterPlanPath)); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("header =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("lint after preset apply = (%d, %q, %q)", code, stdout, stderr)
	}
	if code, _, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001", "--profile", "codex-first"); code != 2 || !strings.Contains(stderr, "--preset") {
		t.Fatalf("roster apply --profile on v2 = (%d, %q), want 2 and the --preset hint", code, stderr)
	}
}

func TestRosterAcceptanceMigrateTwoToThree(t *testing.T) {
	rosterAcceptanceEnv(t)
	meta := "executor: pi | zai/glm-5.3 | max\nfallback: codex | gpt-6-luna | max; claude | claude-opus-5-5 | high\nreplan: claude | claude-opus-5-5 | high\n"
	plan := "---\nid: P001\ntitle: Legacy\ntype: feature\nstatus: active\ncreated: 2026-10-12\n" + meta + "---\n" +
		"## Goal\nGoal.\n\n## Context\n\n## Constraints\n\n## Prerequisites\n\n## Traps\n\n## Decisions\n- legacy decision — why — alternative\n\n## Phase 1: First\n\n" +
		"### [ ] P001.1.1 Unit\nRead:\n- a.py\nDo:\n- edit a.py\nVerify: `true`\nCommit: P001.1.1: unit\n"
	root := rosterProject(t, plan)
	docflow := strings.Replace(readRosterFile(t, root, "docs/docflow.md"), "docflow: 3", "docflow: 2", 1)
	writeLifecycleFile(t, root, "docs/docflow.md", docflow)
	// The archive entry quotes the same chains; migrate must leave it alone.
	archived := "---\nid: P000\ntitle: Legacy\nclosed: 2026-10-01\n---\n## Outcome\nRan with:\n" + meta +
		"\n## Decisions\n- legacy decision — why — alternative\n\n## Gotchas\n- none\n\n## Follow-ups\n- none\n"
	writeLifecycleFile(t, root, "docs/archive/P000-legacy.md", archived)

	// roster apply skips the version check on purpose (P036.2.2), so it is
	// not among the refusing commands.
	for _, args := range [][]string{{"lint"}, {"status"}, {"run", "--dry-run"}} {
		code, stdout, stderr := callLifecycleCLI(t, root, args...)
		if code != 2 || !strings.Contains(stdout+stderr, "format version 2, expected 3; run docflow migrate") {
			t.Errorf("%v on docflow: 2 = (%d, %q, %q)", args, code, stdout, stderr)
		}
	}
	if readRosterFile(t, root, rosterPlanPath) != plan {
		t.Fatal("a command on the unmigrated project changed the plan")
	}

	code, stdout, stderr := callLifecycleCLI(t, root, "migrate")
	if code != 0 || stdout != "migrated 2 -> 3\ndocs/plans/P001-slug.md: replan carries the fallback chain\n" || stderr != "" {
		t.Fatalf("migrate = (%d, %q, %q)", code, stdout, stderr)
	}
	if got := readRosterFile(t, root, "docs/docflow.md"); got != strings.Replace(docflow, "docflow: 2", "docflow: 3", 1) {
		t.Fatalf("docflow.md after migrate:\n%s", got)
	}
	wantPlan := strings.Replace(plan, "replan: claude | claude-opus-5-5 | high\n", "replan: claude | claude-opus-5-5 | high; codex | gpt-6-luna | max\n", 1)
	if got := readRosterFile(t, root, rosterPlanPath); got != wantPlan {
		t.Fatalf("plan after migrate:\n%s", got)
	}
	if got := readRosterFile(t, root, "docs/archive/P000-legacy.md"); got != archived {
		t.Fatalf("migrate changed the archive:\n%s", got)
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("lint after migrate = (%d, %q, %q)", code, stdout, stderr)
	}
}

// TestRosterApplyConditionsMatchBrief: the one-shot flags of the brief go
// to apply unchanged, writes the brief header for the roles the plan uses,
// and leaves the condition state alone.
func TestRosterApplyConditionsMatchBrief(t *testing.T) {
	home := rosterBriefEnv(t)
	root := rosterProject(t, rosterPlanUnits("", rosterConditionUnits))
	flags := []string{"--off", "claude,local", "--lean", "zai", "--minimal", "codex",
		"--prefer", "opencode-go", "--only", "zai=*flash*", "--expires", "zai=2099-01-01"}

	briefArgs := append([]string{"roster", "brief", "--json"}, flags...)
	code, stdout, stderr := callLifecycleCLI(t, root, briefArgs...)
	if code != 0 || stderr != "" {
		t.Fatalf("roster brief = (%d, %q, %q)", code, stdout, stderr)
	}
	var brief roster.Brief
	if err := json.Unmarshal([]byte(stdout), &brief); err != nil {
		t.Fatalf("brief JSON: %v\n%s", err, stdout)
	}
	header := brief.Header()

	applyArgs := append([]string{"roster", "apply", "P001"}, flags...)
	code, stdout, stderr = callLifecycleCLI(t, root, applyArgs...)
	if code != 0 || !strings.Contains(stdout, "commit: P001: apply roster") {
		t.Fatalf("roster apply = (%d, %q, %q)", code, stdout, stderr)
	}
	plan, err := cycle.FindPlan(root, "P001")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range roster.PlanRoles(plan) {
		switch role {
		case "executor":
			if got, want := plan.Meta["executor"], header["executor"]; got != want {
				t.Errorf("executor = %q, want the brief value %q", got, want)
			}
			if got, want := plan.Meta["fallback"], header["fallback"]; got != want {
				t.Errorf("fallback = %q, want the brief value %q", got, want)
			}
		default:
			if got, want := plan.Meta[role], header[role]; got != want {
				t.Errorf("%s = %q, want the brief value %q", role, got, want)
			}
		}
	}
	text := readRosterFile(t, root, rosterPlanPath)
	for _, want := range []string{
		"executor: pi | opencode-go/deepseek-v4.1 | high",
		"review: pi | opencode-go/deepseek-v4.1 | high",
		"acceptance: codex | gpt-6.1-sol | medium",
		"replan: codex | gpt-6.1-sol | medium",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plan after apply has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "claude") {
		t.Errorf("--off claude still wrote a claude route:\n%s", text)
	}
	if !strings.Contains(text, "cheap: pi | opencode-go/deepseek-v4.1-flash | high; pi | zai/glm-5.3-flash | low") {
		t.Errorf("--only zai=*flash* changed the cheap chain:\n%s", text)
	}
	if strings.Contains(text, "zai/glm-5.3 |") {
		t.Errorf("--only zai=*flash* wrote another zai model:\n%s", text)
	}
	for _, line := range headerLines(text) {
		if strings.Contains(line, "codex") && !strings.HasPrefix(line, "acceptance: ") && !strings.HasPrefix(line, "replan: ") {
			t.Errorf("--minimal codex put codex into %q", line)
		}
	}
	if strings.Contains(text, "heavy:") {
		t.Errorf("apply wrote a heavy key for a plan without heavy units:\n%s", text)
	}
	if state := readRosterState(t, home); state != "" {
		t.Fatalf("roster apply wrote the condition state:\n%s", state)
	}
	if docflow := readRosterFile(t, root, "docs/docflow.md"); !strings.Contains(docflow, "docflow: 3") {
		t.Fatalf("apply changed the project format:\n%s", docflow)
	}

	commitRosterProject(t, root)
	code, stdout, stderr = callLifecycleCLI(t, root, "run", "--dry-run", "--all")
	if code != 0 {
		t.Fatalf("dry-run --all = (%d, %q, %q)", code, stdout, stderr)
	}
	for _, want := range []string{
		"P001.1.2 [ ] Plain unit: pi | opencode-go/deepseek-v4.1 | high (plan)",
		"P001.1.3 [ ] Review unit: pi | opencode-go/deepseek-v4.1 | high (role review)",
		"P001.1.4 [ ] Acceptance unit: codex | gpt-6.1-sol | medium (role acceptance)",
		"P001.1.5 [ ] Cheap unit: pi | opencode-go/deepseek-v4.1-flash | high (role cheap)",
	} {
		if !strings.Contains(stdout, want+"\n") {
			t.Errorf("dry-run --all lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "claude") {
		t.Errorf("run --dry-run --all found a claude route after --off claude:\n%s", stdout)
	}
}

// codexOnlyRoster is a v2 policy with one access: switching codex off leaves
// every chain empty.
const codexOnlyRoster = `version = 2

[access.codex]
agents  = ["codex"]
kind    = "subscription"
budget  = "normal"
trusted = true
models  = ["gpt-6.1-sol"]

[model."codex/gpt-6.1-sol"]
class = "strong"

[role]
cheap      = "standard"
executor   = "standard"
heavy      = "strong"
review     = "standard"
acceptance = "trusted"
replan     = "trusted"
`

// TestRosterApplyRefusesEmptyExecutorChain reproduces codex off → apply:
// apply fails before the write, so the plan keeps its codex executor and
// run never falls back to the legacy claude/sonnet tier.
func TestRosterApplyRefusesEmptyExecutorChain(t *testing.T) {
	rosterEnv(t, codexOnlyRoster)
	writeLifecycleFile(t, os.Getenv("HOME"), "cache/docflow/roster/catalog.json", rosterFixture(t, "catalog-scenario.json"))
	plan := rosterPlanUnits("executor: codex | gpt-6.1-sol | high\n", rosterPlainUnit)
	root := rosterProject(t, plan)
	before := readRosterFile(t, root, rosterPlanPath)

	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001", "--off", "codex")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "needs an executor route") {
		t.Fatalf("apply with codex off = (%d, %q, %q), want code 2 and the missing-executor error", code, stdout, stderr)
	}
	if after := readRosterFile(t, root, rosterPlanPath); after != before {
		t.Fatalf("a refused apply changed the plan:\n%s", after)
	}
	if !strings.Contains(before, "executor: codex | gpt-6.1-sol | high") {
		t.Fatalf("the plan lost its codex executor:\n%s", before)
	}
	if code, _, _ := callLifecycleCLI(t, root, "lint"); code != 0 {
		t.Fatalf("lint after the refused apply = %d", code)
	}
	if docflow := readRosterFile(t, root, "docs/docflow.md"); !strings.Contains(docflow, "docflow: 3") {
		t.Fatalf("apply changed the project format:\n%s", docflow)
	}
	code, stdout, stderr = callLifecycleCLI(t, root, "run", "--dry-run", "--all")
	if code != 0 {
		t.Fatalf("dry-run --all = (%d, %q, %q)", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "P001.1.2 [ ] Plain unit: codex | gpt-6.1-sol | high (plan)\n") {
		t.Errorf("dry-run --all lost the codex route:\n%s", stdout)
	}
	if strings.Contains(stdout, "claude") || strings.Contains(stdout, "sonnet") {
		t.Errorf("run fell back to a legacy route after the refused apply:\n%s", stdout)
	}
}

func TestRosterAcceptanceRunLock(t *testing.T) {
	rosterAcceptanceEnv(t)
	root := rosterProject(t, rosterPlan(""))
	output, err := exec.Command("git", "-C", root, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleFile(t, strings.TrimSpace(string(output)), "docflow/run/run.lock", "")
	before := readRosterFile(t, root, rosterPlanPath)
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001")
	if code != 5 || !strings.Contains(stderr, "a run is in progress") {
		t.Fatalf("roster apply under run.lock = (%d, %q, %q)", code, stdout, stderr)
	}
	if readRosterFile(t, root, rosterPlanPath) != before {
		t.Fatal("roster apply under run.lock changed the plan")
	}
}
