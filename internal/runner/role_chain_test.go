package runner

import (
	"testing"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
)

const rolePlan = `---
id: P001
title: Plan
type: feature
status: active
created: 2026-10-24
executor: pi | zai/glm-5.3 | max
fallback: codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
heavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
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
Verify: ` + "`true`" + `
Role: heavy
Commit: P001.1.1: role unit

### [ ] P001.1.2 Explicit unit
Read:
- a.txt
Do:
- work
Verify: ` + "`true`" + `
Executor: claude | claude-opus-5-5 | high
Commit: P001.1.2: explicit unit

### [ ] P001.1.3 Inheriting unit
Read:
- a.txt
Do:
- work
Verify: ` + "`true`" + `
Commit: P001.1.3: inheriting unit
`

func routeIdentities(routes []agents.Route) []string {
	result := make([]string, len(routes))
	for i, route := range routes {
		result[i] = routeIdentity(route)
	}
	return result
}

func TestUnitRoleRoutes(t *testing.T) {
	plan, err := format.ParsePlan(rolePlan, "docs/plans/P001-plan.md")
	if err != nil {
		t.Fatal(err)
	}
	role, err := unitRoleRoutes(plan, plan.Unit("P001.1.1"))
	if err != nil {
		t.Fatal(err)
	}
	if got := routeIdentities(role); len(got) != 2 || got[0] != "pi|zai/glm-5.3|max" || got[1] != "codex|gpt-6.1-sol|high" {
		t.Fatalf("role unit routes = %v", got)
	}
	explicit, err := unitRoleRoutes(plan, plan.Unit("P001.1.2"))
	if err != nil {
		t.Fatal(err)
	}
	if got := routeIdentities(explicit); len(got) != 3 || got[0] != "claude|claude-opus-5-5|high" || got[1] != "codex|gpt-6-luna|max" {
		t.Fatalf("explicit unit routes = %v", got)
	}
	inherited, err := unitRoleRoutes(plan, plan.Unit("P001.1.3"))
	if err != nil {
		t.Fatal(err)
	}
	if got := routeIdentities(inherited); len(got) != 3 || got[0] != "pi|zai/glm-5.3|max" || got[1] != "codex|gpt-6-luna|max" {
		t.Fatalf("inherited unit routes = %v", got)
	}
	chain, err := unitExecutorChain(nil, plan, plan.Unit("P001.1.1"), agents.RunArgs{Executors: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := routeIdentities(chain.routes); len(got) != 2 || got[0] != "pi|zai/glm-5.3|max" || got[1] != "codex|gpt-6.1-sol|high" {
		t.Fatalf("unit executor chain = %v", got)
	}
	if route, err := agents.RunRoute(nil, plan, plan.Unit("P001.1.1"), agents.RunArgs{Executors: true}); err != nil || route != (agents.Route{Agent: "pi", Model: "zai/glm-5.3", Effort: "max"}) {
		t.Fatalf("RunRoute(role) = %#v, %v", route, err)
	}
	if source := agents.RouteSource(plan, plan.Unit("P001.1.1")); source != "role" {
		t.Fatalf("RouteSource(role) = %q", source)
	}
	replan, err := replanExecutorChain(nil, plan, agents.RunArgs{Executors: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := routeIdentities(replan.routes); len(got) != 2 || got[0] != "claude|claude-opus-5-5|high" || got[1] != "codex|gpt-6.1-sol|high" {
		t.Fatalf("replan executor chain = %v", got)
	}
}
