package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The brief and explain tests drive the in-process CLI against the v2
// fixture of the roster package, installed as the personal roster with its
// catalog in the cache.

// tableGap separates the columns of a brief table: tableText pads every
// column to its widest cell plus two spaces.
var tableGap = regexp.MustCompile(` {2,}`)

// rosterBriefEnv installs the v2 policy fixture as the personal roster and
// the scenario catalog as the cache. The fixture expiry of zai is pushed
// past the test clock: the brief reads the real day, and a fixed date in
// the past would drop an access from every run after it.
func rosterBriefEnv(t *testing.T) string {
	t.Helper()
	home := rosterStateEnv(t)
	policy := readRosterFile(t, home, "docflow/roster.toml")
	patched := strings.Replace(policy, `expires = "2026-06-30"`, `expires = "2099-01-01"`, 1)
	if patched == policy {
		t.Fatal("policy-v2.toml no longer names the zai expiry the test patches")
	}
	writeLifecycleFile(t, home, "docflow/roster.toml", patched)
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "roster", "testdata", "catalog-scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleFile(t, home, "cache/docflow/roster/catalog.json", string(data))
	return home
}

// briefChains reads the chains table of a brief: role → chain, the routes
// joined with " > " again.
func briefChains(t *testing.T, text string) map[string]string {
	t.Helper()
	chains := map[string]string{}
	inside := false
	for _, line := range strings.Split(text, "\n") {
		if line == "chains:" {
			inside = true
			continue
		}
		if !inside {
			continue
		}
		if strings.HasSuffix(line, ":") {
			break
		}
		if strings.HasPrefix(line, "role ") {
			continue
		}
		columns := tableGap.Split(strings.TrimSpace(line), -1)
		if len(columns) < 4 {
			t.Fatalf("chain row %q has %d columns, want at least 4", line, len(columns))
		}
		chains[columns[0]] = strings.Join(columns[1:len(columns)-2], " ")
	}
	return chains
}

// scenarioChains is the acceptance scenario under the low preset, the
// chains the brief of the fixture must print.
var scenarioChains = map[string]string{
	"executor":   "opencode-go/deepseek-v4.1",
	"cheap":      "opencode-go/deepseek-v4.1-flash > zai/glm-5.3-flash",
	"heavy":      "-",
	"review":     "opencode-go/deepseek-v4.1",
	"acceptance": "codex/gpt-6.1-sol",
	"replan":     "codex/gpt-6.1-sol",
}

func TestRosterBriefPresetChains(t *testing.T) {
	home := rosterBriefEnv(t)
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "brief", "--preset", "low")
	if code != 0 || stderr != "" {
		t.Fatalf("roster brief = (%d, %q, %q)", code, stdout, stderr)
	}
	for _, section := range []string{"conditions:", "chains:", "review:", "why:", "warnings:"} {
		if !strings.Contains(stdout, section+"\n") {
			t.Errorf("brief has no %q section:\n%s", section, stdout)
		}
	}
	got := briefChains(t, stdout)
	for role, want := range scenarioChains {
		if got[role] != want {
			t.Errorf("%s chain = %q, want %q", role, got[role], want)
		}
	}
	// A read-only command leaves the condition state alone.
	if state := readRosterState(t, home); state != "" {
		t.Fatalf("roster brief wrote the state file:\n%s", state)
	}
}

// TestRosterBriefOneShotFlags checks that the in-memory flags produce the
// same chains as the preset they spell out and that they write no state.
func TestRosterBriefOneShotFlags(t *testing.T) {
	home := rosterBriefEnv(t)
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "brief",
		"--off", "claude,local", "--lean", "zai", "--minimal", "codex",
		"--prefer", "opencode-go", "--only", "zai=*flash*", "--expires", "zai=2099-01-01")
	if code != 0 || stderr != "" {
		t.Fatalf("roster brief flags = (%d, %q, %q)", code, stdout, stderr)
	}
	got := briefChains(t, stdout)
	for role, want := range scenarioChains {
		if got[role] != want {
			t.Errorf("%s chain = %q, want %q", role, got[role], want)
		}
	}
	if state := readRosterState(t, home); state != "" {
		t.Fatalf("one-shot flags wrote the state file:\n%s", state)
	}

	// Switching codex off leaves no trusted strong route: acceptance and
	// replan lose their chain while the rest of the scenario stays.
	code, stdout, stderr = callLifecycleCLI(t, t.TempDir(), "roster", "brief", "--preset", "low", "--off", "codex")
	if code != 0 || stderr != "" {
		t.Fatalf("roster brief --off codex = (%d, %q, %q)", code, stdout, stderr)
	}
	got = briefChains(t, stdout)
	if got["acceptance"] != "-" || got["replan"] != "-" {
		t.Errorf("judge chains with codex off = %q, %q, want both -", got["acceptance"], got["replan"])
	}
	if state := readRosterState(t, home); state != "" {
		t.Fatalf("--off codex wrote the state file:\n%s", state)
	}
}

// TestRosterBriefBadFlags checks the flag shapes and the policy checks the
// one-shot conditions go through.
func TestRosterBriefBadFlags(t *testing.T) {
	rosterBriefEnv(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown access", []string{"--off", "nope"}, "unknown access 'nope'"},
		{"unknown preset", []string{"--preset", "nope"}, "unknown preset 'nope'"},
		{"bad expires", []string{"--expires", "zai"}, "--expires wants ACCESS=DATE"},
		{"bad only", []string{"--only", "zai"}, "--only wants ACCESS=GLOB"},
		{"minimal untrusted", []string{"--minimal", "zai"}, "budget minimal requires trusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"roster", "brief"}, tc.args...)
			code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), args...)
			if code != 2 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("roster brief = (%d, %q, %q), want exit 2 naming %q", code, stdout, stderr, tc.want)
			}
		})
	}
}

// TestRosterBriefJSON reads the same view as JSON: the roles with their
// chains, budgets and scopes, the conditions and the warnings.
func TestRosterBriefJSON(t *testing.T) {
	rosterBriefEnv(t)
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "brief", "--preset", "low", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("roster brief --json = (%d, %q, %q)", code, stdout, stderr)
	}
	var brief struct {
		Conditions []struct{ Access, Kind, Value, Source string }
		Roles      []struct {
			Role   string
			Scope  string
			Budget *struct{ Target int }
		}
		Warnings []string
	}
	if err := json.Unmarshal([]byte(stdout), &brief); err != nil {
		t.Fatalf("brief JSON: %v\n%s", err, stdout)
	}
	if len(brief.Conditions) == 0 {
		t.Error("brief JSON has no conditions in force")
	}
	roles := map[string]string{}
	budgets := map[string]int{}
	for _, role := range brief.Roles {
		roles[role.Role] = role.Scope
		if role.Budget != nil {
			budgets[role.Role] = role.Budget.Target
		}
	}
	if budgets["executor"] != 250000 {
		t.Errorf("executor budget = %d, want 250000", budgets["executor"])
	}
	if roles["executor"] != "read" || roles["review"] != "diff/test" {
		t.Errorf("scopes = %v, want read executor and diff/test review", roles)
	}
	if _, ok := budgets["review"]; ok {
		t.Error("review got a read budget in the JSON")
	}
	if !contains(brief.Warnings, "no routes for heavy") {
		t.Errorf("warnings = %v, want the empty heavy chain", brief.Warnings)
	}
}

// explainPlanMeta is the stale header of the explain test: an executor
// pair from another roster, a cheap chain no unit uses.
const explainPlanMeta = "executor: codex | gpt-6-luna | high\n" +
	"fallback: codex | gpt-6.1-sol | high\n" +
	"cheap: pi | zai/glm-5.3-flash | low\n"

// explainProject is a project with the roster acceptance plan, which has
// heavy, review and acceptance units.
func explainProject(t *testing.T) string {
	t.Helper()
	return rosterProject(t, rosterPlan(explainPlanMeta))
}

// plainPlan is a plan without a single Role: only the executor and replan
// chains belong to it.
func plainPlan(meta string) string {
	return "---\nid: P001\ntitle: Roster\ntype: feature\nstatus: active\ncreated: 2026-10-12\n" + meta + "---\n" +
		"## Goal\nGoal.\n\n## Phase 1: First\n\n" +
		"### [ ] P001.1.1 Plain unit\nRead:\n- a.py\nDo:\n- edit a.py\nVerify: `true`\nCommit: P001.1.1: plain\n"
}

func TestRosterExplainChanges(t *testing.T) {
	rosterBriefEnv(t)
	root := explainProject(t)
	before := readRosterFile(t, root, rosterPlanPath)
	revBefore := gitRevision(t, root)

	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "explain", "P001", "--preset", "low")
	if code != 0 || stderr != "" {
		t.Fatalf("roster explain = (%d, %q, %q)", code, stdout, stderr)
	}
	want := `plan: P001
changes:
executor: codex | gpt-6-luna | high → pi | opencode-go/deepseek-v4.1 | high
fallback: codex | gpt-6.1-sol | high → -
cheap: pi | zai/glm-5.3-flash | low → -
review: - → pi | opencode-go/deepseek-v4.1 | high
acceptance: - → codex | gpt-6.1-sol | medium
replan: - → codex | gpt-6.1-sol | medium
warnings:
- no fallback for executor
- no routes for heavy
- no fallback for review
- no fallback for acceptance
- no fallback for replan
`
	if stdout != want {
		t.Fatalf("roster explain stdout =\n%s\nwant\n%s", stdout, want)
	}
	if after := readRosterFile(t, root, rosterPlanPath); after != before {
		t.Fatal("roster explain rewrote the plan")
	}
	if revAfter := gitRevision(t, root); revAfter != revBefore {
		t.Fatalf("roster explain committed: %s -> %s", revBefore, revAfter)
	}
}

// TestRosterExplainPlanRoles checks the warning filter: a plan without
// heavy units is not told it has no heavy route.
func TestRosterExplainPlanRoles(t *testing.T) {
	rosterBriefEnv(t)
	root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": plainPlan(explainPlanMeta)}, true)
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "explain", "P001", "--preset", "low")
	if code != 0 || stderr != "" {
		t.Fatalf("roster explain = (%d, %q, %q)", code, stdout, stderr)
	}
	if strings.Contains(stdout, "heavy") {
		t.Errorf("explain of a plan without heavy units mentions heavy:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no fallback for executor") {
		t.Errorf("explain lost the executor warning:\n%s", stdout)
	}
}

// TestRosterExplainUpToDate checks the equal case: no changes, exit 0.
func TestRosterExplainUpToDate(t *testing.T) {
	rosterBriefEnv(t)
	meta := "executor: pi | opencode-go/deepseek-v4.1 | high\n" +
		"replan: codex | gpt-6.1-sol | medium\n"
	root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": plainPlan(meta)}, true)
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "explain", "P001", "--preset", "low")
	if code != 0 || stderr != "" {
		t.Fatalf("roster explain = (%d, %q, %q)", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "changes: none\n") {
		t.Fatalf("explain of a matching header:\n%s", stdout)
	}
}

// TestRosterExplainJSON reads the report as JSON: one entry per change and
// the warnings of the plan roles.
func TestRosterExplainJSON(t *testing.T) {
	rosterBriefEnv(t)
	root := explainProject(t)
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "explain", "P001", "--preset", "low", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("roster explain --json = (%d, %q, %q)", code, stdout, stderr)
	}
	var report ExplainReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("explain JSON: %v\n%s", err, stdout)
	}
	changes := map[string]ExplainChange{}
	for _, change := range report.Changes {
		changes[change.Key] = change
	}
	if got := changes["executor"]; got.Old != "codex | gpt-6-luna | high" || got.New != "pi | opencode-go/deepseek-v4.1 | high" {
		t.Errorf("executor change = %+v", got)
	}
	if got := changes["fallback"]; got.Old == "" || got.New != "" {
		t.Errorf("fallback change = %+v, want a removal", got)
	}
	if _, ok := changes["heavy"]; ok {
		t.Error("heavy changed although neither the plan nor the brief has a route")
	}
	if report.Plan != "P001" || !strings.HasSuffix(report.Path, rosterPlanPath) {
		t.Errorf("report names %q at %q", report.Plan, report.Path)
	}
	if !contains(report.Warnings, "no routes for heavy") {
		t.Errorf("warnings = %v, want the empty heavy chain", report.Warnings)
	}
}

// TestRosterBriefShowV1 keeps the old format on the old path: show keeps
// its summary, brief and the one-shot flags ask for the migration, and the
// old profile flag stays for v1.
func TestRosterBriefShowV1(t *testing.T) {
	rosterEnv(t, draftV1Roster(t))
	root := t.TempDir()
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "show")
	if code != 0 || !strings.HasPrefix(stdout, "spend: ") || stderr != "" {
		t.Fatalf("roster show on v1 = (%d, %q, %q)", code, stdout, stderr)
	}
	if code, _, stderr := callLifecycleCLI(t, root, "roster", "brief"); code != 2 || !strings.Contains(stderr, "roster migrate") {
		t.Fatalf("roster brief on v1 = (%d, %q), want exit 2 with the migration hint", code, stderr)
	}
	if code, _, stderr := callLifecycleCLI(t, root, "roster", "show", "--preset", "low"); code != 2 || !strings.Contains(stderr, "roster migrate") {
		t.Fatalf("roster show --preset on v1 = (%d, %q), want the migration hint", code, stderr)
	}
}

// TestRosterBriefShowV2Alias checks that show is brief on a v2 roster and
// that the v1 profile flag is refused with the new name.
func TestRosterBriefShowV2Alias(t *testing.T) {
	rosterBriefEnv(t)
	root := t.TempDir()
	code, brief, stderr := callLifecycleCLI(t, root, "roster", "brief", "--preset", "low")
	if code != 0 || stderr != "" {
		t.Fatalf("roster brief = (%d, %q, %q)", code, brief, stderr)
	}
	code, show, stderr := callLifecycleCLI(t, root, "roster", "show", "--preset", "low")
	if code != 0 || stderr != "" {
		t.Fatalf("roster show = (%d, %q, %q)", code, show, stderr)
	}
	if show != brief {
		t.Fatalf("roster show is not the brief:\n--- show ---\n%s\n--- brief ---\n%s", show, brief)
	}
	code, _, stderr = callLifecycleCLI(t, root, "roster", "show", "--profile", "x")
	if code != 2 || !strings.Contains(stderr, "--preset") {
		t.Fatalf("roster show --profile on v2 = (%d, %q), want exit 2 naming --preset", code, stderr)
	}
}

// gitRevision is the current commit of the test project.
func gitRevision(t *testing.T, root string) string {
	t.Helper()
	output, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(output))
}
