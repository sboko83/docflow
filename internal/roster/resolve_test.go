package roster

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// resolveDay pins the clock of every resolve test: before the zai expiry of
// the policy fixture, so a date-dependent rule fails loudly instead of
// passing on the day the test is written.
var resolveDay = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// resolvePolicy parses a v4 policy body.
func resolvePolicy(t *testing.T, body string) *Policy {
	t.Helper()
	policy, err := ParsePolicy("roster.toml", "version = 4\n"+body)
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

// candidateText renders a role's candidates one per comma: the cell
// reference, then the route triple the plan would carry.
func candidateText(resolution *Resolution, role string) string {
	parts := make([]string, 0, len(resolution.Candidates(role)))
	for _, candidate := range resolution.Candidates(role) {
		effort := candidate.Effort
		if effort == "" {
			effort = "-"
		}
		parts = append(parts, fmt.Sprintf("%s %s:%s:%s", modelAtEffort(candidate), candidate.Agent, candidate.Model, effort))
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

const resolveTwo = `
[access.zai]
agents = ["pi"]

[access.zai.roles]
executor = "glm-5.3@high"
heavy    = "glm-5.3@max"

[access.codex]
agents = ["codex"]

[access.codex.roles]
executor = "gpt-6-luna@medium"
review   = "gpt-6.1-sol"
`

func resolveTwoCatalog() *Catalog {
	catalog := resolveCatalog(
		resolveModel("zai", "glm-5.3", 200000, nil),
		resolveModel("codex", "gpt-6-luna", 272000, nil),
		resolveModel("codex", "gpt-6.1-sol", 500000, nil),
	)
	for i := range catalog.Accesses {
		if catalog.Accesses[i].Access == "codex" {
			catalog.Accesses[i].Agents = []string{"codex"}
		}
	}
	return catalog
}

// TestResolveColumnOrder: the chain of a role is its column read in the
// access order; a pi channel addresses ACCESS/ID, codex the bare id, and
// the effort is the cell's, passed as written.
func TestResolveColumnOrder(t *testing.T) {
	resolution, err := resolveAt(resolvePolicy(t, resolveTwo), nil, resolveTwoCatalog(), resolveDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateText(resolution, "executor"); got != "zai/glm-5.3@high pi:zai/glm-5.3:high, codex/gpt-6-luna@medium codex:gpt-6-luna:medium" {
		t.Fatalf("executor = %s", got)
	}
	if got := candidateText(resolution, "review"); got != "codex/gpt-6.1-sol codex:gpt-6.1-sol:-" {
		t.Fatalf("review = %s", got)
	}
	if reason, _ := skipReason(resolution, "zai", "review"); reason != "no cell for review" {
		t.Fatalf("zai review skip = %q", reason)
	}
}

// TestResolvePresetOrderOffAndCells: a preset reorders the accesses,
// switches some off and overrides or clears single cells.
func TestResolvePresetOrderOffAndCells(t *testing.T) {
	policy := resolvePolicy(t, resolveTwo)
	policy.Presets = []Preset{
		{Name: "codex-first", Order: []string{"codex"}},
		{Name: "no-zai", Off: []string{"zai"}},
		{Name: "cells", Roles: map[string]map[string]Cell{
			"zai":   {"executor": {}},
			"codex": {"heavy": {Model: "gpt-6.1-sol", Effort: "xhigh"}},
		}},
	}
	resolve := func(preset string) *Resolution {
		state := NewState("")
		state.Preset = preset
		resolution, err := resolveAt(policy, state, resolveTwoCatalog(), resolveDay)
		if err != nil {
			t.Fatal(err)
		}
		return resolution
	}
	if got := candidateText(resolve("codex-first"), "executor"); !strings.HasPrefix(got, "codex/") {
		t.Fatalf("codex-first executor = %s", got)
	}
	noZai := resolve("no-zai")
	if got := candidateText(noZai, "executor"); got != "codex/gpt-6-luna@medium codex:gpt-6-luna:medium" {
		t.Fatalf("no-zai executor = %s", got)
	}
	if reason, _ := skipReason(noZai, "zai", ""); reason != "off" {
		t.Fatalf("no-zai skip = %q", reason)
	}
	cells := resolve("cells")
	if got := candidateText(cells, "executor"); got != "codex/gpt-6-luna@medium codex:gpt-6-luna:medium" {
		t.Fatalf("cells executor = %s", got)
	}
	if got := candidateText(cells, "heavy"); got != "zai/glm-5.3@max pi:zai/glm-5.3:max, codex/gpt-6.1-sol@xhigh codex:gpt-6.1-sol:xhigh" {
		t.Fatalf("cells heavy = %s", got)
	}
	if zai, _ := policy.Access("zai"); zai.Cell("executor").Empty() {
		t.Fatal("the preset wrote through to the loaded policy")
	}
}

// TestResolveSkipReasons: a model missing from the catalog, a heavy model
// of unknown context, a refused auth and an expiry each say why.
func TestResolveSkipReasons(t *testing.T) {
	policy := resolvePolicy(t, `
[access.zai]
agents = ["pi"]
expires = "2026-05-01"

[access.zai.roles]
executor = "glm-5.3@high"

[access.codex]
agents = ["codex"]

[access.codex.roles]
executor = "gpt-7"
heavy    = "gpt-6-luna"

[access.claude]
agents = ["claude"]

[access.claude.roles]
executor = "claude-opus-5-5"
`)
	catalog := resolveCatalog(
		resolveModel("zai", "glm-5.3", 200000, nil),
		resolveModel("codex", "gpt-6-luna", 0, nil),
		resolveModel("claude", "claude-opus-5-5", 200000, nil),
	)
	for i := range catalog.Accesses {
		if catalog.Accesses[i].Access == "claude" {
			catalog.Accesses[i].Auth = AuthNotReady
		}
	}
	resolution, err := resolveAt(policy, nil, catalog, resolveDay)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ access, role, reason string }{
		{"zai", "", "expired on 2026-05-01"},
		{"codex", "executor", "model gpt-7 is not in the catalog"},
		{"codex", "heavy", "unknown context"},
		{"claude", "", "auth not ready"},
	} {
		reason, ok := skipReason(resolution, want.access, want.role)
		if !ok || !strings.Contains(reason, want.reason) {
			t.Errorf("skip %s/%s = %q, want %q", want.access, want.role, reason, want.reason)
		}
	}
	if got := candidateText(resolution, "executor"); got != "" {
		t.Fatalf("executor = %q, want none", got)
	}
}

// TestResolveConditionsOffAndExpiry: the state switches an access off and
// a state expiry tightens the policy one.
func TestResolveConditionsOffAndExpiry(t *testing.T) {
	policy := resolvePolicy(t, resolveTwo)
	state := NewState("")
	state.SetOff("codex", time.Time{})
	state.SetExpires("zai", time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC))
	resolution, err := resolveAt(policy, state, resolveTwoCatalog(), resolveDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateText(resolution, "executor"); got != "" {
		t.Fatalf("executor = %q, want none", got)
	}
	if reason, _ := skipReason(resolution, "zai", ""); !strings.Contains(reason, "expired") {
		t.Fatalf("zai skip = %q", reason)
	}
}

// TestResolveScenario resolves the acceptance fixture with its low policy.
func TestResolveScenario(t *testing.T) {
	policy, err := LoadPolicy("testdata/policy.toml")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCatalog("testdata/catalog-scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	state := NewState("")
	state.Preset = "low"
	resolution, err := resolveAt(policy, state, catalog, resolveDay)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"executor":   "opencode-go/deepseek-v4.1@high pi:opencode-go/deepseek-v4.1:high",
		"cheap":      "opencode-go/deepseek-v4.1-flash@high pi:opencode-go/deepseek-v4.1-flash:high, zai/glm-5.3-flash@low pi:zai/glm-5.3-flash:low",
		"heavy":      "",
		"review":     "opencode-go/deepseek-v4.1@high pi:opencode-go/deepseek-v4.1:high",
		"acceptance": "codex/gpt-6.1-sol@medium codex:gpt-6.1-sol:medium",
		"replan":     "codex/gpt-6.1-sol@medium codex:gpt-6.1-sol:medium",
	}
	for role, chain := range want {
		if got := candidateText(resolution, role); got != chain {
			t.Errorf("%s = %s, want %s", role, got, chain)
		}
	}
}
