package roster

import (
	"strings"
	"testing"
)

// buildChains resolves a policy and builds the role chains from it.
func buildChains(t *testing.T, policy *Policy, state *State, catalog *Catalog) *Chains {
	t.Helper()
	resolution, err := resolveAt(policy, state, catalog, resolveDay)
	if err != nil {
		t.Fatalf("resolveAt() error = %v", err)
	}
	chains, err := BuildChains(resolution)
	if err != nil {
		t.Fatalf("BuildChains() error = %v", err)
	}
	return chains
}

// roleChainText renders a role chain one route per comma: access/id@budget.
func roleChainText(chains *Chains, role string) string {
	parts := make([]string, 0, len(chains.Routes(role)))
	for _, route := range chains.Routes(role) {
		parts = append(parts, route.Access+"/"+route.ID+"@"+route.Budget)
	}
	return strings.Join(parts, ", ")
}

// TestResolveChainHappyPath pins the chain order and the warnings of a
// complete roster: prefer before normal before lean whatever the file
// order, lean outside heavy, and no fallback warning on a two-route chain.
func TestResolveChainHappyPath(t *testing.T) {
	// The file order is the reverse of the budget order on purpose.
	policy := resolvePolicy(t, `
[access.lean]
agents = ["pi"]
budget = "lean"

[access.normal]
agents = ["pi"]

[access.prefer]
agents = ["pi"]
budget = "prefer"
trusted = true

[model."lean/m"]
class = "strong"

[model."normal/m"]
class = "strong"

[model."prefer/m"]
class = "strong"
`)
	chains := buildChains(t, policy, nil, resolveCatalog(
		resolveModel("lean", "m", 100000, nil),
		resolveModel("normal", "m", 100000, nil),
		resolveModel("prefer", "m", 100000, nil),
	))
	want := map[string]string{
		"executor": "prefer/m@prefer, normal/m@normal, lean/m@lean",
		"heavy":    "prefer/m@prefer, normal/m@normal",
		"review":   "prefer/m@prefer, normal/m@normal, lean/m@lean",
	}
	for role, expected := range want {
		if got := roleChainText(chains, role); got != expected {
			t.Errorf("%s chain = %q, want %q", role, got, expected)
		}
	}
	for _, route := range chains.Routes("heavy") {
		if route.Budget == BudgetLean {
			t.Errorf("heavy chain holds the lean access %s", route.Access)
		}
	}
	if got, expected := strings.Join(chains.Warnings, ", "), "no fallback for acceptance, no fallback for replan"; got != expected {
		t.Fatalf("warnings = %q, want %q", got, expected)
	}
}

// TestResolveChainDuplicateAccess pins the unit of a chain: an access, not
// an agent|model pair. A subscription reached through two agents is one
// quota and one route, whichever channel the policy declares first.
func TestResolveChainDuplicateAccess(t *testing.T) {
	t.Run("two channels, one access", func(t *testing.T) {
		policy := resolvePolicy(t, `
[access.dual]
agents = ["pi", "opencode"]

[access.second]
agents = ["pi"]
budget = "lean"

[model."dual/m"]
class = "strong"

[model."second/m"]
class = "strong"
`)
		chains := buildChains(t, policy, nil, resolveCatalog(
			resolveModel("dual", "m", 100000, nil),
			resolveModel("second", "m", 100000, nil),
		))
		if got, expected := roleChainText(chains, "executor"), "dual/m@normal, second/m@lean"; got != expected {
			t.Fatalf("executor chain = %q, want %q", got, expected)
		}
		if agent := chains.Routes("executor")[0].Agent; agent != "pi" {
			t.Errorf("dual route agent = %q, want the first declared channel pi", agent)
		}
	})

	t.Run("opencode channel", func(t *testing.T) {
		policy := resolvePolicy(t, `
[access.opencode-go]
agents = ["opencode"]

[model."opencode-go/m"]
class = "standard"
`)
		chains := buildChains(t, policy, nil, resolveCatalog(resolveModel("opencode-go", "m", 100000, nil)))
		routes := chains.Routes("executor")
		if len(routes) != 1 {
			t.Fatalf("executor chain = %+v, want one route", routes)
		}
		route := routes[0]
		if route.Access != "opencode-go" || route.Agent != "opencode" || route.Model != "opencode-go/m" {
			t.Fatalf("executor route = %+v, want opencode-go/m through opencode", route)
		}
	})

	t.Run("repeated access keeps the first route", func(t *testing.T) {
		resolution := &Resolution{Roles: map[string][]Candidate{
			"executor": {
				{Access: "dual", Agent: "pi", Model: "dual/m", Budget: BudgetPrefer},
				{Access: "dual", Agent: "opencode", Model: "dual/m", Budget: BudgetPrefer},
				{Access: "second", Agent: "pi", Model: "second/m", Budget: BudgetLean},
			},
		}}
		chains, err := BuildChains(resolution)
		if err != nil {
			t.Fatalf("BuildChains() error = %v", err)
		}
		routes := chains.Routes("executor")
		if len(routes) != 2 || routes[0].Agent != "pi" || routes[1].Access != "second" {
			t.Fatalf("executor chain = %+v, want the first dual route and second", routes)
		}
	})
}

// TestResolveChainNoFallback pins a one-route chain as a warning: it does
// not stop the plan, and a role without any route is a warning too.
func TestResolveChainNoFallback(t *testing.T) {
	policy := resolvePolicy(t, `
[access.only]
agents = ["pi"]

[model."only/m"]
class = "strong"
`)
	chains := buildChains(t, policy, nil, resolveCatalog(resolveModel("only", "m", 100000, nil)))
	if got := len(chains.Routes("executor")); got != 1 {
		t.Fatalf("executor chain has %d routes, want 1", got)
	}
	want := "no fallback for executor, no fallback for cheap, no fallback for heavy, " +
		"no fallback for review, no routes for acceptance, no routes for replan"
	if got := strings.Join(chains.Warnings, ", "); got != want {
		t.Fatalf("warnings = %q, want %q", got, want)
	}
}

// TestResolveChainMinimalNotReview pins the budget levels outside a role:
// a minimal access judges acceptance and replan and never reviews.
func TestResolveChainMinimalNotReview(t *testing.T) {
	policy := resolvePolicy(t, `
[access.judge]
agents = ["pi"]
budget = "minimal"
trusted = true

[model."judge/m"]
class = "strong"
`)
	chains := buildChains(t, policy, nil, resolveCatalog(resolveModel("judge", "m", 100000, nil)))
	if routes := chains.Routes("review"); len(routes) != 0 {
		t.Fatalf("review chain = %+v, want none: minimal serves only acceptance and replan", routes)
	}
	if got, expected := roleChainText(chains, "acceptance"), "judge/m@minimal"; got != expected {
		t.Fatalf("acceptance chain = %q, want %q", got, expected)
	}
	if got, expected := roleChainText(chains, "replan"), "judge/m@minimal"; got != expected {
		t.Fatalf("replan chain = %q, want %q", got, expected)
	}
	if !strings.Contains(strings.Join(chains.Warnings, ", "), "no routes for review") {
		t.Errorf("warnings = %q, want the missing review route", chains.Warnings)
	}
}

// TestReviewMatrix pins the review matrix: a check access is paired with
// the review routes of other accesses, and an access whose only model is
// below the review class does not become a reviewer.
func TestReviewMatrix(t *testing.T) {
	policy := resolvePolicy(t, `
[access.checker]
agents = ["pi"]
check = true

[access.reviewer]
agents = ["pi"]
budget = "prefer"

[access.weak]
agents = ["pi"]

[model."checker/m"]
class = "standard"

[model."reviewer/m"]
class = "standard"

[model."weak/m"]
class = "cheap"
`)
	chains := buildChains(t, policy, nil, resolveCatalog(
		resolveModel("checker", "m", 100000, nil),
		resolveModel("reviewer", "m", 100000, nil),
		resolveModel("weak", "m", 100000, nil),
	))
	if got, expected := roleChainText(chains, "review"), "reviewer/m@prefer"; got != expected {
		t.Fatalf("review chain = %q, want %q", got, expected)
	}
	if len(chains.Reviews) != 1 {
		t.Fatalf("review matrix = %+v, want one row", chains.Reviews)
	}
	row := chains.Reviews[0]
	if row.Access != "checker" {
		t.Fatalf("review matrix row = %q, want checker", row.Access)
	}
	if len(row.Reviewers) != 1 || row.Reviewers[0].Access != "reviewer" {
		t.Fatalf("reviewers of checker = %+v, want the reviewer access", row.Reviewers)
	}
	for _, reviewer := range row.Reviewers {
		if reviewer.Access == row.Access {
			t.Errorf("check access %s reviews itself", row.Access)
		}
	}
}

// TestReviewNoReviewer pins a check access without a non-check reviewer as
// an exit-code-2 error that names the condition, the access and the ways
// out instead of self-reviewing silently.
func TestReviewNoReviewer(t *testing.T) {
	assertNoReviewer := func(t *testing.T, policy *Policy, catalog *Catalog, hints ...string) {
		t.Helper()
		resolution, err := resolveAt(policy, nil, catalog, resolveDay)
		if err != nil {
			t.Fatalf("resolveAt() error = %v", err)
		}
		_, err = BuildChains(resolution)
		if err == nil {
			t.Fatal("BuildChains() error = nil, want the missing-reviewer error")
		}
		if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != 2 {
			t.Fatalf("BuildChains() error = %v, want exit code 2", err)
		}
		if !strings.Contains(err.Error(), "no independent reviewer") {
			t.Fatalf("error = %v, want the missing-reviewer condition", err)
		}
		for _, hint := range hints {
			if !strings.Contains(err.Error(), hint) {
				t.Errorf("error = %v, want the hint %q", err, hint)
			}
		}
	}

	t.Run("single check access", func(t *testing.T) {
		policy := resolvePolicy(t, `
[access.checker]
agents = ["pi"]
check = true

[model."checker/m"]
class = "standard"
`)
		assertNoReviewer(t, policy, resolveCatalog(resolveModel("checker", "m", 100000, nil)),
			"check access checker",
			"set check = false in [access.checker]",
			"roster allow a non-check access <model-glob>",
			"roster off checker --until DATE",
		)
	})

	t.Run("reviewer switched off", func(t *testing.T) {
		policy := resolvePolicy(t, `
[access.checker]
agents = ["pi"]
check = true

[access.spare]
agents = ["pi"]
budget = "off"

[model."checker/m"]
class = "standard"

[model."spare/m"]
class = "standard"
`)
		assertNoReviewer(t, policy, resolveCatalog(
			resolveModel("checker", "m", 100000, nil),
			resolveModel("spare", "m", 100000, nil),
		), "spare: budget off", "roster allow spare <model-glob>")
	})

	t.Run("every access is check", func(t *testing.T) {
		policy := resolvePolicy(t, `
[access.first]
agents = ["pi"]
check = true

[access.second]
agents = ["pi"]
check = true

[model."first/m"]
class = "standard"

[model."second/m"]
class = "standard"
`)
		assertNoReviewer(t, policy, resolveCatalog(
			resolveModel("first", "m", 100000, nil),
			resolveModel("second", "m", 100000, nil),
		), "check access first", "set check = false in [access.first]")
	})
}

// TestReviewSameAccessRefused pins independence: a check access that ends
// up on the review side is not its own reviewer, so it fails instead of
// passing silently.
func TestReviewSameAccessRefused(t *testing.T) {
	policy := resolvePolicy(t, `
[access.checker]
agents = ["pi"]
check = true

[model."checker/m"]
class = "strong"
`)
	checker := Candidate{Role: "review", Access: "checker", Agent: "pi", Model: "checker/m", Class: "strong", Budget: BudgetNormal, Check: true}
	resolution := &Resolution{
		Policy: policy,
		Roles: map[string][]Candidate{
			"executor": {checker},
			"review":   {checker},
		},
	}
	if _, err := BuildChains(resolution); err == nil || !strings.Contains(err.Error(), "check access checker") {
		t.Fatalf("BuildChains() error = %v, want the same-access reviewer refusal", err)
	}
}

// TestReviewMinimalNotReviewer pins the minimal budget on the reviewer
// side: a trusted strong access switched to minimal judges acceptance but
// gives no review route, so it never reviews a check access.
func TestReviewMinimalNotReviewer(t *testing.T) {
	policy := resolvePolicy(t, `
[access.judge]
agents = ["pi"]
budget = "minimal"
trusted = true

[access.checker]
agents = ["pi"]
check = true

[access.reviewer]
agents = ["pi"]

[model."judge/m"]
class = "strong"

[model."checker/m"]
class = "standard"

[model."reviewer/m"]
class = "standard"
`)
	chains := buildChains(t, policy, nil, resolveCatalog(
		resolveModel("judge", "m", 100000, nil),
		resolveModel("checker", "m", 100000, nil),
		resolveModel("reviewer", "m", 100000, nil),
	))
	if got, expected := roleChainText(chains, "review"), "reviewer/m@normal"; got != expected {
		t.Fatalf("review chain = %q, want %q", got, expected)
	}
	if got, expected := roleChainText(chains, "acceptance"), "judge/m@minimal"; got != expected {
		t.Fatalf("acceptance chain = %q, want %q", got, expected)
	}
	if len(chains.Reviews) != 1 || len(chains.Reviews[0].Reviewers) != 1 {
		t.Fatalf("review matrix = %+v, want one row with one reviewer", chains.Reviews)
	}
	if reviewer := chains.Reviews[0].Reviewers[0]; reviewer.Access != "reviewer" {
		t.Fatalf("reviewer of checker = %s, want reviewer", reviewer.Access)
	}
}

// TestResolveChainScenario pins the plan's acceptance scenario at the
// chain level: claude and local off, codex minimal, zai lean and limited
// to flash, opencode-go first. The check access zai gets opencode-go as
// its reviewer, heavy has no route, and every incomplete chain is a
// warning rather than a refusal.
func TestResolveChainScenario(t *testing.T) {
	policy, err := LoadPolicy("testdata/policy-v2.toml")
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	catalog, err := LoadCatalog("testdata/catalog-scenario.json")
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	state := NewState("")
	state.Preset = "low"
	chains := buildChains(t, policy, state, catalog)
	want := map[string]string{
		"executor":   "opencode-go/deepseek-v4.1@prefer",
		"cheap":      "opencode-go/deepseek-v4.1-flash@prefer, zai/glm-5.3-flash@lean",
		"heavy":      "",
		"review":     "opencode-go/deepseek-v4.1@prefer",
		"acceptance": "codex/gpt-6.1-sol@minimal",
		"replan":     "codex/gpt-6.1-sol@minimal",
	}
	for role, expected := range want {
		if got := roleChainText(chains, role); got != expected {
			t.Errorf("%s chain = %q, want %q", role, got, expected)
		}
	}
	if len(chains.Reviews) != 1 || chains.Reviews[0].Access != "zai" {
		t.Fatalf("review matrix = %+v, want one row for zai", chains.Reviews)
	}
	if reviewers := chains.Reviews[0].Reviewers; len(reviewers) != 1 || reviewers[0].Access != "opencode-go" {
		t.Fatalf("reviewers of zai = %+v, want opencode-go", reviewers)
	}
	warnings := strings.Join(chains.Warnings, ", ")
	for _, warning := range []string{"no fallback for executor", "no routes for heavy", "no fallback for review"} {
		if !strings.Contains(warnings, warning) {
			t.Errorf("warnings = %q, want %q", warnings, warning)
		}
	}
}
