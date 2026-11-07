package roster

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// scenarioBrief resolves the acceptance scenario with the low preset on
// the pinned day and builds its brief.
func scenarioBrief(t *testing.T) *Brief {
	t.Helper()
	policy, err := LoadPolicy("testdata/policy-v3.toml")
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	catalog, err := LoadCatalog("testdata/catalog-scenario.json")
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	state := NewState("")
	state.Preset = "low"
	brief, err := buildBriefAt(policy, state, catalog, nil, resolveDay)
	if err != nil {
		t.Fatalf("buildBriefAt() error = %v", err)
	}
	return brief
}

// TestBriefScenarioGolden pins the whole rendered brief of the acceptance
// scenario: the conditions in force, every chain with the read budget of
// its primary route, the review matrix, the explanation and the warnings.
// A changed line is a changed promise to the planner.
func TestBriefScenarioGolden(t *testing.T) {
	got := scenarioBrief(t).Text()
	golden, err := os.ReadFile("testdata/brief-scenario.txt")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(golden) {
		t.Fatalf("brief text differs from testdata/brief-scenario.txt:\n--- got ---\n%s", got)
	}
}

// TestBriefScenarioValues pins the numbers behind the golden: the budget
// belongs to the primary route, the fallback does not get one, and the
// judge roles get diff/test scope instead.
func TestBriefScenarioValues(t *testing.T) {
	brief := scenarioBrief(t)
	budgets := map[string]int{}
	scopes := map[string]string{}
	for _, role := range brief.Roles {
		scopes[role.Role] = role.Scope
		if role.Budget != nil {
			budgets[role.Role] = role.Budget.Target
		}
	}
	want := map[string]int{"executor": 250000, "cheap": 152371}
	for role, target := range want {
		if budgets[role] != target {
			t.Errorf("%s budget = %d, want %d", role, budgets[role], target)
		}
	}
	if _, ok := budgets["review"]; ok {
		t.Errorf("review got a read budget, want diff/test scope")
	}
	if scopes["review"] != ScopeJudge || scopes["acceptance"] != ScopeJudge || scopes["replan"] != ScopeJudge {
		t.Errorf("judge scopes = %v, want %s", scopes, ScopeJudge)
	}
	if scopes["cheap"] != ScopeRead || scopes["executor"] != ScopeRead {
		t.Errorf("read scopes = %v, want %s", scopes, ScopeRead)
	}
	if got := brief.Roles[0].Role; got != Roles[0] {
		t.Errorf("first role = %q, want %q in policy role order", got, Roles[0])
	}
	if len(brief.Reviews) != 1 || brief.Reviews[0].Access != "zai" {
		t.Fatalf("review matrix = %+v, want one row for zai", brief.Reviews)
	}
}

// TestBriefJSON keeps the JSON brief usable for the CLI: the roles keep
// the policy order, the budget travels with its numbers and the judge
// scope is not hidden.
func TestBriefJSON(t *testing.T) {
	text, err := json.Marshal(scenarioBrief(t))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded struct {
		Roles []struct {
			Role   string      `json:"role"`
			Scope  string      `json:"scope"`
			Budget *UnitBudget `json:"budget"`
			Routes []Candidate `json:"routes"`
		} `json:"roles"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(text, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(decoded.Roles) != len(Roles) {
		t.Fatalf("roles = %d, want %d", len(decoded.Roles), len(Roles))
	}
	for i, role := range decoded.Roles {
		if role.Role != Roles[i] {
			t.Errorf("role %d = %q, want %q", i, role.Role, Roles[i])
		}
	}
	cheap := decoded.Roles[1]
	if cheap.Role != "cheap" {
		t.Fatalf("second role = %q, want cheap", cheap.Role)
	}
	if len(cheap.Routes) != 2 || cheap.Routes[1].Access != "zai" {
		t.Errorf("cheap routes = %+v, want the fallback zai", cheap.Routes)
	}
	if cheap.Budget == nil || cheap.Budget.Target != 152371 || cheap.Budget.Route != "opencode-go/deepseek-v4.1-flash" {
		t.Errorf("cheap budget = %+v, want 152371 on the primary route", cheap.Budget)
	}
	if !strings.Contains(string(text), `"scope":"diff/test"`) {
		t.Errorf("json has no diff/test scope: %s", text)
	}
	if len(decoded.Warnings) == 0 {
		t.Error("warnings are empty, want the incomplete chains of the scenario")
	}
}

// TestBriefUnknownContext pins the trap of the unit: a model whose window
// the catalog does not know gets "context unknown", not a guessed budget
// and not a place in a heavy chain.
func TestBriefUnknownContext(t *testing.T) {
	policy := resolvePolicy(t, `
[access.small]
agents = ["pi"]

[route."small/m"]
high = "standard"
`)
	catalog := resolveCatalog(resolveModel("small", "m", 0, nil))
	brief, err := buildBriefAt(policy, nil, catalog, []string{"executor"}, resolveDay)
	if err != nil {
		t.Fatalf("buildBriefAt() error = %v", err)
	}
	if len(brief.Roles) != 1 || brief.Roles[0].Budget == nil {
		t.Fatalf("roles = %+v, want one executor route with a budget", brief.Roles)
	}
	if target := brief.Roles[0].Budget.Target; target != 0 {
		t.Errorf("budget = %d, want 0 for an unknown context", target)
	}
	text := brief.Text()
	if !strings.Contains(text, "context unknown") {
		t.Errorf("brief text = %q, want it to say context unknown", text)
	}
	if !strings.Contains(text, "budget") || strings.Contains(text, "budget 20000") {
		t.Errorf("brief text = %q, want a dash instead of a clamped budget", text)
	}
}

// TestBriefWarningsFollowRoles pins the handoff of P055.3.2: the brief
// shows the warning of a role the plan uses and stays silent about a role
// it does not use.
func TestBriefWarningsFollowRoles(t *testing.T) {
	policy := resolvePolicy(t, `
[access.one]
agents = ["pi"]

[route."one/m"]
high = "standard"
`)
	catalog := resolveCatalog(resolveModel("one", "m", 100000, nil))
	brief, err := buildBriefAt(policy, nil, catalog, []string{"executor"}, resolveDay)
	if err != nil {
		t.Fatalf("buildBriefAt() error = %v", err)
	}
	if got, want := strings.Join(brief.Warnings, ", "), "no fallback for executor"; got != want {
		t.Errorf("warnings = %q, want %q", got, want)
	}
	for _, warning := range brief.Warnings {
		if strings.Contains(warning, "for heavy") || strings.Contains(warning, "for review") {
			t.Errorf("warnings = %q, want only the used role", brief.Warnings)
		}
	}
}

// TestBriefMultipleAccesses pins a chain of several accesses: the order is
// the resolver's, the primary route carries the budget and the fallback is
// part of the chain and of the explanation.
func TestBriefMultipleAccesses(t *testing.T) {
	// The file order is the reverse of the budget order on purpose.
	policy := resolvePolicy(t, `
[access.lean]
agents = ["pi"]
budget = "lean"

[access.prefer]
agents = ["pi"]
budget = "prefer"

[route."lean/m"]
high = "strong"

[route."prefer/m"]
high = "strong"
`)
	catalog := resolveCatalog(
		resolveModel("lean", "m", 100000, nil),
		resolveModel("prefer", "m", 100000, nil),
	)
	brief, err := buildBriefAt(policy, nil, catalog, []string{"executor"}, resolveDay)
	if err != nil {
		t.Fatalf("buildBriefAt() error = %v", err)
	}
	role := brief.Roles[0]
	if got, want := briefChain(role.Routes), "prefer/m > lean/m"; got != want {
		t.Fatalf("chain = %q, want %q", got, want)
	}
	if role.Budget == nil || role.Budget.Route != "prefer/m" {
		t.Fatalf("budget = %+v, want it computed from the primary route prefer/m", role.Budget)
	}
	if got, want := role.Budget.Target, 29313; got != want {
		t.Errorf("budget = %d, want %d", got, want)
	}
	if len(role.Why) != 3 {
		t.Fatalf("why = %q, want a primary line, a fallback line and a budget line", role.Why)
	}
	if !strings.Contains(role.Why[0], "primary prefer/m via pi") {
		t.Errorf("why = %q, want the primary route first", role.Why)
	}
	if !strings.Contains(role.Why[1], "fallback lean/m via pi") {
		t.Errorf("why = %q, want the fallback next", role.Why)
	}
	if len(brief.Warnings) != 0 {
		t.Errorf("warnings = %q, want none for a two-route chain", brief.Warnings)
	}
}

// TestBriefRoleOrder pins the stability of the output: the caller's order
// never decides the order of the rows, and a repeated role appears once.
func TestBriefRoleOrder(t *testing.T) {
	policy := resolvePolicy(t, `
[access.one]
agents = ["pi"]

[route."one/m"]
high = "standard"
`)
	catalog := resolveCatalog(resolveModel("one", "m", 100000, nil))
	brief, err := buildBriefAt(policy, nil, catalog, []string{"replan", "executor", "replan"}, resolveDay)
	if err != nil {
		t.Fatalf("buildBriefAt() error = %v", err)
	}
	if got, want := len(brief.Roles), 2; got != want {
		t.Fatalf("roles = %d, want %d", got, want)
	}
	if brief.Roles[0].Role != "executor" || brief.Roles[1].Role != "replan" {
		t.Errorf("roles = %q, %q, want executor then replan", brief.Roles[0].Role, brief.Roles[1].Role)
	}
}

// TestBriefUnknownRole pins the typo rule: a role name that no roster has
// is exit code 2 with the known roles, not a silently skipped row.
func TestBriefUnknownRole(t *testing.T) {
	policy := resolvePolicy(t, `
[access.one]
agents = ["pi"]

[route."one/m"]
high = "standard"
`)
	catalog := resolveCatalog(resolveModel("one", "m", 100000, nil))
	_, err := buildBriefAt(policy, nil, catalog, []string{"executorr"}, resolveDay)
	if err == nil {
		t.Fatal("buildBriefAt() error = nil, want an unknown-role error")
	}
	for _, part := range []string{"executorr", "executor", "replan"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error = %q, want it to mention %q", err, part)
		}
	}
}
