package roster

import (
	"strings"
	"testing"
	"time"
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

// roleChainText renders a role chain one route per comma: access/id.
func roleChainText(chains *Chains, role string) string {
	parts := make([]string, 0, len(chains.Routes(role)))
	for _, route := range chains.Routes(role) {
		parts = append(parts, route.Access+"/"+route.ID)
	}
	return strings.Join(parts, ", ")
}

// TestResolveChainOrderAndWarnings: a chain is the column in access order,
// and the warnings name the empty chains and the chains of one route.
func TestResolveChainOrderAndWarnings(t *testing.T) {
	policy := resolvePolicy(t, `
[access.b]
agents = ["pi"]

[access.b.roles]
executor = "m@high"
heavy    = "m@high"

[access.a]
agents = ["pi"]

[access.a.roles]
executor = "m@high"
`)
	chains := buildChains(t, policy, nil, resolveCatalog(
		resolveModel("a", "m", 100000, nil),
		resolveModel("b", "m", 100000, nil),
	))
	if got := roleChainText(chains, "executor"); got != "b/m, a/m" {
		t.Fatalf("executor chain = %q", got)
	}
	want := "no fallback for heavy"
	if !strings.Contains(strings.Join(chains.Warnings, ", "), want) || !strings.Contains(strings.Join(chains.Warnings, ", "), "no routes for review") {
		t.Fatalf("warnings = %v", chains.Warnings)
	}
}

// TestReviewMatrix: a check access is reviewed by the review routes of the
// other accesses, never by itself.
func TestReviewMatrix(t *testing.T) {
	policy := resolvePolicy(t, `
[access.checker]
agents = ["pi"]
check = true

[access.checker.roles]
executor = "m@high"

[access.reviewer]
agents = ["pi"]

[access.reviewer.roles]
review = "m@high"
`)
	chains := buildChains(t, policy, nil, resolveCatalog(
		resolveModel("checker", "m", 100000, nil),
		resolveModel("reviewer", "m", 100000, nil),
	))
	if len(chains.Reviews) != 1 || chains.Reviews[0].Access != "checker" {
		t.Fatalf("review matrix = %+v", chains.Reviews)
	}
	if got := chains.Reviews[0].Reviewers; len(got) != 1 || got[0].Access != "reviewer" {
		t.Fatalf("reviewers = %+v", got)
	}
}

// TestReviewNoReviewer: a check access without an independent reviewer is
// an exit-code-2 error naming the ways out; a check access switched off
// needs no reviewer.
func TestReviewNoReviewer(t *testing.T) {
	policy := resolvePolicy(t, `
[access.checker]
agents = ["pi"]
check = true

[access.checker.roles]
executor = "m@high"

[access.spare]
agents = ["pi"]

[access.spare.roles]
executor = "m@high"
`)
	catalog := resolveCatalog(resolveModel("checker", "m", 100000, nil), resolveModel("spare", "m", 100000, nil))
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildChains(resolution)
	if err == nil {
		t.Fatal("BuildChains() error = nil, want the missing-reviewer error")
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != 2 {
		t.Fatalf("error = %v, want exit code 2", err)
	}
	for _, hint := range []string{
		"check access checker has no independent reviewer",
		"spare: no cell for review",
		"set check = false in [access.checker]",
		"give spare a review cell",
		"roster off checker --until DATE",
	} {
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("error = %v, want %q", err, hint)
		}
	}
	state := NewState("")
	state.SetOff("checker", time.Time{})
	if _, err := BuildChains(mustResolve(t, policy, state, catalog)); err != nil {
		t.Fatalf("checker off: BuildChains() error = %v", err)
	}
}

// TestResolveChainOneRoutePerAccess: an access is one quota, so a chain
// never repeats it.
func TestResolveChainOneRoutePerAccess(t *testing.T) {
	candidates := []Candidate{{Access: "a", ID: "x"}, {Access: "a", ID: "y"}, {Access: "b", ID: "x"}}
	got := dedupeAccesses(candidates)
	if len(got) != 2 || got[0].ID != "x" || got[1].Access != "b" {
		t.Fatalf("dedupeAccesses() = %+v", got)
	}
}

func mustResolve(t *testing.T, policy *Policy, state *State, catalog *Catalog) *Resolution {
	t.Helper()
	resolution, err := resolveAt(policy, state, catalog, resolveDay)
	if err != nil {
		t.Fatal(err)
	}
	return resolution
}
