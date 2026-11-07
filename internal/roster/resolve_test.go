package roster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// resolveDay pins the clock of every resolve test: before the zai expiry of
// the policy fixture, so a date-dependent rule fails loudly instead of
// passing on the day the test is written.
var resolveDay = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// resolveRoles is the [role] table a resolve test policy carries unless it
// wants different requirements.
const resolveRoles = `
[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "standard"
acceptance = "trusted"
replan     = "trusted"
`

// resolvePolicy parses a policy body with the standard role table.
func resolvePolicy(t *testing.T, body string) *Policy {
	t.Helper()
	policy, err := ParsePolicy("roster.toml", "version = 3\n"+body+resolveRoles)
	if err != nil {
		t.Fatalf("ParsePolicy() error = %v", err)
	}
	return policy
}

// resolveModel is one catalog model with the fields the resolver reads.
func resolveModel(access, id string, context int, cost *Cost) ModelInfo {
	return ModelInfo{
		Access:    access,
		ID:        id,
		Context:   context,
		MaxOut:    1000,
		Reasoning: true,
		Cost:      cost,
		Source:    "test",
	}
}

// resolveCatalog builds a catalog with one ready pi subscription access per
// model; a test that needs another channel edits catalog.Accesses.
func resolveCatalog(models ...ModelInfo) *Catalog {
	catalog := emptyCatalog()
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		catalog.Models = append(catalog.Models, model)
		if !seen[model.Access] {
			seen[model.Access] = true
			catalog.Accesses = append(catalog.Accesses, AccessInfo{
				Access: model.Access,
				Agents: []string{"pi"},
				Kind:   "subscription",
				Auth:   AuthReady,
				Source: "test",
			})
		}
	}
	catalog.Sort()
	return catalog
}

// candidateText renders a role's candidates one per comma: the access, its
// model id, the budget, then the route triple the plan would carry.
func candidateText(resolution *Resolution, role string) string {
	parts := make([]string, 0, len(resolution.Candidates(role)))
	for _, candidate := range resolution.Candidates(role) {
		effort := candidate.Effort
		if effort == "" {
			effort = "-"
		}
		parts = append(parts, fmt.Sprintf("%s/%s@%s %s:%s:%s", candidate.Access, candidate.ID, candidate.Budget, candidate.Agent, candidate.Model, effort))
	}
	return strings.Join(parts, ", ")
}

// skipReason finds the reason the resolver gave for one access and role
// ("" means the access-level reason).
func skipReason(resolution *Resolution, access, role string) (string, bool) {
	for _, skip := range resolution.Skipped {
		if skip.Access == access && skip.Role == role {
			return skip.Reason, true
		}
	}
	return "", false
}

// TestResolveCandidatesScenario is the acceptance scenario of the plan:
// claude and local off, codex minimal, zai lean and limited to flash,
// opencode-go first.
func TestResolveCandidatesScenario(t *testing.T) {
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
	resolution, err := resolveAt(policy, state, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	want := map[string]string{
		"executor":   "opencode-go/deepseek-v4.1@prefer pi:opencode-go/deepseek-v4.1:high",
		"cheap":      "opencode-go/deepseek-v4.1-flash@prefer pi:opencode-go/deepseek-v4.1-flash:high, zai/glm-5.3-flash@lean pi:zai/glm-5.3-flash:low",
		"heavy":      "",
		"review":     "opencode-go/deepseek-v4.1@prefer pi:opencode-go/deepseek-v4.1:high",
		"acceptance": "codex/gpt-6.1-sol@minimal codex:gpt-6.1-sol:medium",
		"replan":     "codex/gpt-6.1-sol@minimal codex:gpt-6.1-sol:medium",
	}
	for _, role := range Roles {
		if got := candidateText(resolution, role); got != want[role] {
			t.Errorf("%s candidates = %q, want %q", role, got, want[role])
		}
	}
	// A switch is one access-level reason, not one per role.
	switched := map[string]string{"claude": "budget off", "opencode": "budget off", "local": "budget off"}
	for _, skip := range resolution.Skipped {
		if skip.Role != "" {
			continue
		}
		want, ok := switched[skip.Access]
		if !ok {
			t.Errorf("unexpected access-level skip %v", skip)
			continue
		}
		if skip.Reason != want {
			t.Errorf("skip of %s = %q, want %q", skip.Access, skip.Reason, want)
		}
		delete(switched, skip.Access)
	}
	if len(switched) != 0 {
		t.Errorf("access-level skips missing: %v", switched)
	}
	// The reasons name the condition that kept the access out of a role.
	for _, tc := range []struct{ access, role, want string }{
		{"zai", "heavy", "budget lean serves no heavy role"},
		{"zai", "executor", "no model at class standard or above"},
		{"opencode-go", "acceptance", "no model at class strong or above"},
	} {
		reason, ok := skipReason(resolution, tc.access, tc.role)
		if !ok || reason != tc.want {
			t.Errorf("skip of %s/%s = (%q, %t), want %q", tc.access, tc.role, reason, ok, tc.want)
		}
	}
	// The primary route of the acceptance role is a trusted minimal
	// access, not the preferred one: minimal serves only acceptance and
	// replan, and the independent judge is a strong route.
	primary := resolution.Candidates("acceptance")[0]
	if primary.Access != "codex" || !primary.Trusted || primary.Check || primary.Budget != BudgetMinimal {
		t.Errorf("acceptance primary = %+v, want the trusted codex access", primary)
	}
}

// TestResolveCandidatesOrder pins the chain order the resolver hands to
// the chain builder: prefer, normal, lean, policy file order inside one
// budget level.
func TestResolveCandidatesOrder(t *testing.T) {
	policy := resolvePolicy(t, `
[access.lean]
agents = ["pi"]
budget = "lean"

[access.normal]
agents = ["pi"]

[access.prefer]
agents = ["pi"]
budget = "prefer"

[route."lean/m"]
high = "strong"

[route."normal/m"]
high = "strong"

[route."prefer/m"]
high = "strong"
`)
	catalog := resolveCatalog(
		resolveModel("lean", "m", 100000, nil),
		resolveModel("normal", "m", 100000, nil),
		resolveModel("prefer", "m", 100000, nil),
	)
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	if got, want := candidateText(resolution, "executor"), "prefer/m@prefer pi:prefer/m:high, normal/m@normal pi:normal/m:high, lean/m@lean pi:lean/m:high"; got != want {
		t.Fatalf("executor candidates = %q, want %q", got, want)
	}
	// lean serves no heavy unit, whatever the file order says.
	if got, want := candidateText(resolution, "heavy"), "prefer/m@prefer pi:prefer/m:high, normal/m@normal pi:normal/m:high"; got != want {
		t.Fatalf("heavy candidates = %q, want %q", got, want)
	}
}

// TestResolveCandidatesModelPick pins the model choice: the smallest class
// that meets the role, the lowest price, the larger context on a tie and a
// known price before an unknown one.
func TestResolveCandidatesModelPick(t *testing.T) {
	policy := resolvePolicy(t, `
[access.pick]
agents = ["pi"]

[route."pick/cheap"]
low = "cheap"

[route."pick/standard-expensive"]
high = "standard"

[route."pick/standard-cheap"]
high = "standard"

[route."pick/standard-big"]
high = "standard"

[route."pick/standard-unknown"]
high = "standard"

[route."pick/strong"]
high = "strong"
`)
	catalog := resolveCatalog(
		resolveModel("pick", "cheap", 100000, &Cost{Input: 0.1, Output: 0.1}),
		resolveModel("pick", "standard-expensive", 100000, &Cost{Input: 5, Output: 1}),
		resolveModel("pick", "standard-cheap", 100000, &Cost{Input: 1, Output: 0.5}),
		resolveModel("pick", "standard-big", 400000, &Cost{Input: 1, Output: 0.5}),
		resolveModel("pick", "standard-unknown", 100000, nil),
		resolveModel("pick", "strong", 100000, &Cost{Input: 10, Output: 1}),
	)
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	// The cheap model is below the executor requirement; the strong one is a
	// class too high; among the standard ones the larger context wins a
	// price tie, and an unknown price never beats a known one.
	if got, want := candidateText(resolution, "executor"), "pick/standard-big@normal pi:pick/standard-big:high"; got != want {
		t.Fatalf("executor candidates = %q, want %q", got, want)
	}
	if got, want := candidateText(resolution, "cheap"), "pick/cheap@normal pi:pick/cheap:low"; got != want {
		t.Fatalf("cheap candidates = %q, want %q", got, want)
	}
	if got, want := candidateText(resolution, "heavy"), "pick/strong@normal pi:pick/strong:high"; got != want {
		t.Fatalf("heavy candidates = %q, want %q", got, want)
	}
}

// TestResolveCandidatesContext pins the heavy rule: a model whose context
// the catalog does not know must not guess its way into the strongest
// role, while a weaker role may still take it.
func TestResolveCandidatesContext(t *testing.T) {
	policy := resolvePolicy(t, `
[access.known]
agents = ["pi"]

[access.unknown]
agents = ["pi"]

[route."known/strong"]
high = "strong"

[route."unknown/strong"]
high = "strong"
`)
	catalog := resolveCatalog(
		resolveModel("known", "strong", 500000, nil),
		resolveModel("unknown", "strong", 0, nil),
	)
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	if got, want := candidateText(resolution, "heavy"), "known/strong@normal pi:known/strong:high"; got != want {
		t.Fatalf("heavy candidates = %q, want %q", got, want)
	}
	if reason, ok := skipReason(resolution, "unknown", "heavy"); !ok || reason != "no model with a known context" {
		t.Fatalf("skip of unknown/heavy = (%q, %t), want the unknown-context reason", reason, ok)
	}
	if got, want := candidateText(resolution, "executor"), "known/strong@normal pi:known/strong:high, unknown/strong@normal pi:unknown/strong:high"; got != want {
		t.Fatalf("executor candidates = %q, want %q", got, want)
	}
}

// TestResolveCandidatesEffort pins the effort of a candidate: the route
// key when the policy routes the model, otherwise the class ladder of the
// automatic class, snapped to the declared levels, and no effort at all
// without reasoning. A case without a pin routes the model with the
// ladder key; the cheap-ladder case leaves the model unrouted and names
// an id the family rule classes as cheap.
func TestResolveCandidatesEffort(t *testing.T) {
	ladder := map[string]string{"cheap": "low", "standard": "high", "strong": "high"}
	cases := []struct {
		name      string
		role      string
		id        string // empty is "m", routed; a name is unrouted
		class     string
		reasoning bool
		efforts   []string
		pin       string
		want      string
	}{
		{"class ladder high", "executor", "", "standard", true, []string{"low", "medium", "high", "max"}, "", "high"},
		{"class ladder low snapped down", "cheap", "m-mini", "cheap", true, []string{"none", "minimal", "medium"}, "", "minimal"},
		{"pin overrides the ladder", "executor", "", "standard", true, []string{"low", "medium", "high"}, "low", "low"},
		{"pin snapped down", "executor", "", "standard", true, []string{"low", "medium"}, "max", "medium"},
		{"ladder snapped up", "executor", "", "standard", true, []string{"xhigh", "max"}, "", "xhigh"},
		{"pin without effort", "executor", "", "standard", true, []string{"high"}, "-", ""},
		{"without reasoning", "executor", "", "standard", false, []string{"high"}, "", ""},
		{"level outside the ladder stays", "executor", "", "standard", true, []string{"thinking"}, "", "high"},
		{"unknown support keeps the ladder", "executor", "", "standard", true, nil, "", "high"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.id
			routed := id == ""
			if id == "" {
				id = "m"
			}
			var body string
			if routed {
				key := tc.pin
				if key == "" {
					key = ladder[tc.class]
				}
				body = fmt.Sprintf("[access.a]\nagents = [\"pi\"]\n\n[route.\"a/%s\"]\n%s = %q\n", id, key, tc.class)
			} else {
				// No route: the automatic class by the name serves the role.
				body = "[access.a]\nagents = [\"pi\"]\n"
			}
			policy := resolvePolicy(t, body)
			model := resolveModel("a", id, 100000, nil)
			model.Reasoning = tc.reasoning
			model.Efforts = tc.efforts
			resolution, err := resolveAt(policy, nil, resolveCatalog(model), resolveDay)
			if err != nil {
				t.Fatalf("resolveAt() error = %v", err)
			}
			list := resolution.Candidates(tc.role)
			if len(list) != 1 {
				t.Fatalf("%s candidates = %v, want one", tc.role, list)
			}
			if list[0].Effort != tc.want {
				t.Fatalf("effort = %q, want %q", list[0].Effort, tc.want)
			}
		})
	}
}

// TestResolveCandidatesTrusted pins the review and judge rules: a check
// access never reviews, accepts or replans, and only a trusted access
// serves a role whose requirement is trusted.
func TestResolveCandidatesTrusted(t *testing.T) {
	policy := resolvePolicy(t, `
[access.checker]
agents = ["pi"]
check = true

[access.judge]
agents = ["pi"]
trusted = true

[access.plain]
agents = ["pi"]

[route."checker/m"]
high = "strong"

[route."judge/m"]
high = "strong"

[route."plain/m"]
high = "strong"
`)
	catalog := resolveCatalog(
		resolveModel("checker", "m", 100000, nil),
		resolveModel("judge", "m", 100000, nil),
		resolveModel("plain", "m", 100000, nil),
	)
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	if got, want := candidateText(resolution, "executor"), "checker/m@normal pi:checker/m:high, judge/m@normal pi:judge/m:high, plain/m@normal pi:plain/m:high"; got != want {
		t.Fatalf("executor candidates = %q, want %q", got, want)
	}
	if got, want := candidateText(resolution, "review"), "judge/m@normal pi:judge/m:high, plain/m@normal pi:plain/m:high"; got != want {
		t.Fatalf("review candidates = %q, want %q", got, want)
	}
	if got, want := candidateText(resolution, "acceptance"), "judge/m@normal pi:judge/m:high"; got != want {
		t.Fatalf("acceptance candidates = %q, want %q", got, want)
	}
	if got, want := candidateText(resolution, "replan"), "judge/m@normal pi:judge/m:high"; got != want {
		t.Fatalf("replan candidates = %q, want %q", got, want)
	}
	for _, role := range []string{"review", "acceptance", "replan"} {
		for _, candidate := range resolution.Candidates(role) {
			if candidate.Check {
				t.Errorf("%s candidates include the check access %s", role, candidate.Access)
			}
		}
	}
}

// TestResolveCandidatesExpiry pins the expiry rules: the named day is the
// last usable one, and a state off condition holds through its until day.
func TestResolveCandidatesExpiry(t *testing.T) {
	policy := resolvePolicy(t, `
[access.dated]
agents = ["pi"]
expires = "2026-06-30"

[access.switched]
agents = ["pi"]

[route."dated/m"]
high = "standard"

[route."switched/m"]
high = "standard"
`)
	catalog := resolveCatalog(
		resolveModel("dated", "m", 100000, nil),
		resolveModel("switched", "m", 100000, nil),
	)
	state := NewState("")
	state.Off = []string{"switched"}
	state.Until = map[string]time.Time{"switched": time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)}

	lastDay, err := resolveAt(policy, state, catalog, time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("resolveAt(last day) error = %v", err)
	}
	if got, want := candidateText(lastDay, "executor"), "dated/m@normal pi:dated/m:high"; got != want {
		t.Fatalf("executor candidates on the last day = %q, want %q", got, want)
	}

	nextDay, err := resolveAt(policy, state, catalog, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("resolveAt(next day) error = %v", err)
	}
	if got, want := candidateText(nextDay, "executor"), "switched/m@normal pi:switched/m:high"; got != want {
		t.Fatalf("executor candidates after the expiry = %q, want %q", got, want)
	}
	if reason, ok := skipReason(nextDay, "dated", ""); !ok || reason != "expired on 2026-06-30" {
		t.Fatalf("skip of dated = (%q, %t), want the expiry reason", reason, ok)
	}
}

// TestResolveCandidatesAuth pins the auth filter: a refused access is out,
// an undecided one is not a refusal, and an access the scan never saw has
// no candidate.
func TestResolveCandidatesAuth(t *testing.T) {
	policy := resolvePolicy(t, `
[access.refused]
agents = ["pi"]

[access.undecided]
agents = ["pi"]

[access.missing]
agents = ["pi"]

[route."refused/m"]
high = "standard"

[route."undecided/m"]
high = "standard"

[route."missing/m"]
high = "standard"
`)
	catalog := resolveCatalog(
		resolveModel("refused", "m", 100000, nil),
		resolveModel("undecided", "m", 100000, nil),
	)
	for i := range catalog.Accesses {
		switch catalog.Accesses[i].Access {
		case "refused":
			catalog.Accesses[i].Auth = AuthNotReady
		case "undecided":
			catalog.Accesses[i].Auth = AuthUnknown
		}
	}
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	if got, want := candidateText(resolution, "executor"), "undecided/m@normal pi:undecided/m:high"; got != want {
		t.Fatalf("executor candidates = %q, want %q", got, want)
	}
	if reason, ok := skipReason(resolution, "refused", ""); !ok || reason != "auth not ready" {
		t.Errorf("skip of refused = (%q, %t), want auth not ready", reason, ok)
	}
	if reason, ok := skipReason(resolution, "missing", ""); !ok || !strings.Contains(reason, "no models in the catalog") {
		t.Errorf("skip of missing = (%q, %t), want the empty-catalog hint", reason, ok)
	}
	if candidate := resolution.Candidates("executor")[0]; candidate.Auth != AuthUnknown {
		t.Errorf("undecided auth = %q, want unknown carried into the candidate", candidate.Auth)
	}
}

// TestResolveCandidatesChannel pins the channel and the route model text:
// the first declared agent reaches the access, a pi or opencode channel
// addresses ACCESS/ID, a codex or claude channel the bare id.
func TestResolveCandidatesChannel(t *testing.T) {
	policy := resolvePolicy(t, `
[access.dual]
agents = ["opencode", "pi"]

[access.codex]
agents = ["codex"]

[access.claude]
agents = ["claude"]

[route."dual/m"]
high = "standard"

[route."codex/m"]
high = "standard"

[route."claude/m"]
high = "standard"
`)
	catalog := resolveCatalog(
		resolveModel("dual", "m", 100000, nil),
		resolveModel("codex", "m", 100000, nil),
		resolveModel("claude", "m", 100000, nil),
	)
	for i := range catalog.Accesses {
		switch catalog.Accesses[i].Access {
		case "codex":
			catalog.Accesses[i].Agents = []string{"codex"}
		case "claude":
			catalog.Accesses[i].Agents = []string{"claude"}
		}
	}
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	want := []struct{ access, agent, model string }{
		{"dual", "opencode", "dual/m"},
		{"codex", "codex", "m"},
		{"claude", "claude", "m"},
	}
	list := resolution.Candidates("executor")
	if len(list) != len(want) {
		t.Fatalf("executor candidates = %v, want %d", list, len(want))
	}
	for i, expected := range want {
		candidate := list[i]
		if candidate.Access != expected.access || candidate.Agent != expected.agent || candidate.Model != expected.model {
			t.Errorf("candidate %d = %s via %s as %s, want %s via %s as %s", i, candidate.Access, candidate.Agent, candidate.Model, expected.access, expected.agent, expected.model)
		}
	}
}

// TestResolveCandidatesMinimalUntrusted pins the policy error: minimal on
// an access that is not trusted is never a silent switch-off.
func TestResolveCandidatesMinimalUntrusted(t *testing.T) {
	policy := resolvePolicy(t, `
[access.a]
agents = ["pi"]

[route."a/m"]
high = "standard"
`)
	access, ok := policy.Access("a")
	if !ok {
		t.Fatal("access a is missing")
	}
	access.Budget = BudgetMinimal
	access.Trusted = false
	_, err := resolveAt(policy, nil, resolveCatalog(resolveModel("a", "m", 100000, nil)), resolveDay)
	if err == nil {
		t.Fatal("resolveAt() error = nil, want a policy error")
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != 2 {
		t.Fatalf("resolveAt() error = %v, want exit code 2", err)
	}
	if !strings.Contains(err.Error(), "minimal requires trusted") {
		t.Fatalf("error = %v, want the minimal/trusted rule", err)
	}
}

// TestResolveCandidatesFamilyClass pins the automatic class of a model the
// policy does not name: the family decides, a strong marker over a cheap
// one, and a word split keeps "gemini" out of "mini".
func TestResolveCandidatesFamilyClass(t *testing.T) {
	for id, want := range map[string]string{
		"acme-flash":         "cheap",
		"acme-mini":          "cheap",
		"acme-lite":          "cheap",
		"acme-pro":           "strong",
		"gpt-6.1-sol":        "strong",
		"claude-opus-5-5":    "strong",
		"gemini-2.5":         "standard",
		"deepseek-v4.1":      "standard",
		"kimi-k3.1-pro":      "strong",
		"strata/swift-iq3_s": "standard",
	} {
		if got := familyClass(id); got != want {
			t.Errorf("familyClass(%s) = %q, want %q", id, got, want)
		}
	}
	policy := resolvePolicy(t, `
[access.fam]
agents = ["pi"]
`)
	catalog := resolveCatalog(
		resolveModel("fam", "acme-v2-flash", 100000, nil),
		resolveModel("fam", "acme-v2", 100000, nil),
		resolveModel("fam", "acme-v2-pro", 100000, nil),
		resolveModel("fam", "gemini-2.5", 100000, nil),
	)
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	if got, want := candidateText(resolution, "cheap"), "fam/acme-v2-flash@normal pi:fam/acme-v2-flash:low"; got != want {
		t.Fatalf("cheap candidates = %q, want %q", got, want)
	}
	if got, want := candidateText(resolution, "executor"), "fam/acme-v2@normal pi:fam/acme-v2:high"; got != want {
		t.Fatalf("executor candidates = %q, want %q", got, want)
	}
	if got, want := candidateText(resolution, "heavy"), "fam/acme-v2-pro@normal pi:fam/acme-v2-pro:high"; got != want {
		t.Fatalf("heavy candidates = %q, want %q", got, want)
	}
}

// TestResolveCandidatesAllowlist pins the model filters: the access
// allowlist and denials of the policy, and the state allowlist that
// replaces it and the denials that add up over it.
func TestResolveCandidatesAllowlist(t *testing.T) {
	policy := resolvePolicy(t, `
[access.listed]
agents = ["pi"]
models = ["a-*"]
exclude = ["a-bad*"]

[route."listed/a-good"]
high = "standard"

[route."listed/a-bad"]
low = "cheap"

[route."listed/other"]
high = "standard"
`)
	catalog := resolveCatalog(
		resolveModel("listed", "a-good", 100000, nil),
		resolveModel("listed", "a-bad", 100000, nil),
		resolveModel("listed", "other", 100000, nil),
	)
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	if got, want := candidateText(resolution, "executor"), "listed/a-good@normal pi:listed/a-good:high"; got != want {
		t.Fatalf("executor candidates = %q, want %q", got, want)
	}

	// An allowlist in the state replaces the policy one: it can narrow a
	// stable allowlist and never widen it.
	narrowed := NewState("")
	narrowed.Models = map[string][]string{"listed": {"other"}}
	resolution, err = resolveAt(policy, narrowed, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt(narrowed) error = %v", err)
	}
	if got, want := candidateText(resolution, "cheap"), "listed/other@normal pi:listed/other:high"; got != want {
		t.Fatalf("cheap candidates under a state allowlist = %q, want %q", got, want)
	}

	// A denial in the state extends the policy denials: everything is out.
	denied := NewState("")
	denied.Exclude = map[string][]string{"listed": {"*"}}
	resolution, err = resolveAt(policy, denied, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt(denied) error = %v", err)
	}
	if list := resolution.Candidates("executor"); len(list) != 0 {
		t.Fatalf("executor candidates under a denial = %v, want none", list)
	}
	if reason, ok := skipReason(resolution, "listed", "executor"); !ok || reason != "no model the access allows" {
		t.Fatalf("skip of listed/executor = (%q, %t), want the allowlist reason", reason, ok)
	}
}

// TestResolveCandidatesEmptyAllowlist pins the narrowing layer: a preset
// or condition that allows no model leaves the access without candidates,
// while an access without the key serves every model.
func TestResolveCandidatesEmptyAllowlist(t *testing.T) {
	policy := resolvePolicy(t, `
[access.a]
agents = ["pi"]

[route."a/m"]
high = "standard"

[preset.none]
models = { a = [] }
`)
	catalog := resolveCatalog(resolveModel("a", "m", 100000, nil))

	plain, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	if got, want := candidateText(plain, "executor"), "a/m@normal pi:a/m:high"; got != want {
		t.Fatalf("executor candidates without a condition = %q, want %q", got, want)
	}

	preset := NewState("")
	preset.Preset = "none"
	narrowed, err := resolveAt(policy, preset, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt(preset) error = %v", err)
	}
	if list := narrowed.Candidates("executor"); len(list) != 0 {
		t.Fatalf("executor candidates under an empty allowlist = %v, want none", list)
	}
	if reason, ok := skipReason(narrowed, "a", "executor"); !ok || reason != "no model the access allows" {
		t.Fatalf("skip of a/executor = (%q, %t), want the allowlist reason", reason, ok)
	}

	state := NewState("")
	state.Models = map[string][]string{"a": {}}
	narrowed, err = resolveAt(policy, state, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt(state) error = %v", err)
	}
	if list := narrowed.Candidates("executor"); len(list) != 0 {
		t.Fatalf("executor candidates under a state empty allowlist = %v, want none", list)
	}
}

// TestResolveCandidatesMigration resolves a migrated v1 roster: the pi and
// opencode channel addresses ACCESS/ID, and the "-" effort of the v1 route
// stays "no effort" instead of becoming a class-ladder guess.
func TestResolveCandidatesMigration(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "legacy-roster.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text, err := Migrate(string(data))
	if err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	policy, err := ParsePolicy("migrated.toml", text)
	if err != nil {
		t.Fatalf("the migrated roster does not parse: %v", err)
	}
	catalog := resolveCatalog(
		resolveModel("opencode-go", "kimi-k3.1-pro", 200000, nil),
		resolveModel("zai", "glm-5.3-flash", 200000, nil),
		resolveModel("zai", "glm-5.3", 200000, nil),
		resolveModel("codex", "gpt-6-luna", 200000, nil),
		resolveModel("codex", "gpt-6.1-sol", 500000, nil),
		resolveModel("pi", "some-default", 128000, nil),
		resolveModel("strata", "swift-1.5-iq3_s", 32768, nil),
	)
	for i := range catalog.Accesses {
		if catalog.Accesses[i].Access == "codex" {
			catalog.Accesses[i].Agents = []string{"codex"}
		}
	}
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	want := "opencode-go/kimi-k3.1-pro@normal pi:opencode-go/kimi-k3.1-pro:high, " +
		"zai/glm-5.3-flash@normal pi:zai/glm-5.3-flash:max, " +
		"codex/gpt-6-luna@normal codex:gpt-6-luna:medium, " +
		"pi/some-default@normal pi:pi/some-default:-, " +
		"strata/swift-1.5-iq3_s@normal pi:strata/swift-1.5-iq3_s:-"
	if got := candidateText(resolution, "cheap"); got != want {
		t.Fatalf("cheap candidates = %q, want %q", got, want)
	}
}

// TestResolvePicksMinimalSuitableRoute pins the route as the candidate: a
// model routed cheap@low and standard@high serves a cheap role through
// low and an executor role through high, and the unit budget follows the
// class of the selected route. No route meets a strong minimum, so the
// model drops out of the heavy role entirely.
func TestResolvePicksMinimalSuitableRoute(t *testing.T) {
	policy := resolvePolicy(t, `
[access.a]
agents = ["pi"]

[route."a/m"]
low  = "cheap"
high = "standard"
`)
	model := resolveModel("a", "m", 100000, nil)
	model.Efforts = []string{"low", "high"}
	resolution, err := resolveAt(policy, nil, resolveCatalog(model), resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	cheap := resolution.Candidates("cheap")
	if len(cheap) != 1 || cheap[0].Class != "cheap" || cheap[0].Effort != "low" {
		t.Fatalf("cheap candidate = %v, want m@low at cheap", cheap)
	}
	executor := resolution.Candidates("executor")
	if len(executor) != 1 || executor[0].Class != "standard" || executor[0].Effort != "high" {
		t.Fatalf("executor candidate = %v, want m@high at standard", executor)
	}
	if heavy := resolution.Candidates("heavy"); len(heavy) != 0 {
		t.Fatalf("heavy candidates = %v, want none: no route reaches strong", heavy)
	}
	cheapBudget := unitBudget("cheap", cheap[0])
	executorBudget := unitBudget("executor", executor[0])
	if cheapBudget.Route != "a/m@low" || cheapBudget.Class != "cheap" {
		t.Fatalf("cheap budget route = %q class %q, want a/m@low at cheap", cheapBudget.Route, cheapBudget.Class)
	}
	if executorBudget.Route != "a/m@high" || executorBudget.Class != "standard" {
		t.Fatalf("executor budget route = %q class %q, want a/m@high at standard", executorBudget.Route, executorBudget.Class)
	}
	if executorBudget.Target <= cheapBudget.Target {
		t.Fatalf("executor budget %d = cheap budget %d, want the stronger class to raise it", executorBudget.Target, cheapBudget.Target)
	}
}
