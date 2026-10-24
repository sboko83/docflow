package runner

import (
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
)

func TestExecutorChainOrderAndPrimaryRecovery(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	primary := agents.Route{Agent: "pi", Model: "zai/glm-5.3", Effort: "max"}
	fallback := agents.Route{Agent: "codex", Model: "gpt-5.5"}
	reset := float64(now.Add(time.Minute).Unix())
	config := map[string]any{"agents": map[string]any{"pi": map[string]any{}}}
	chain := newExecutorChain([]agents.Route{primary, fallback, primary}, config, nil, true)
	if len(chain.routes) != 2 {
		t.Fatalf("routes = %v, want duplicate removed", chain.routes)
	}
	if got, ok := chain.pick(now); !ok || routeIdentity(got) != routeIdentity(primary) {
		t.Fatalf("first route = %v, %v; want primary", got, ok)
	}
	if _, err := chain.mark(primary, &agentio.Refusal{Kind: agentio.RefusalLimit, Reset: &reset}, now); err != nil {
		t.Fatal(err)
	}
	if got, ok := chain.pick(now); !ok || routeIdentity(got) != routeIdentity(fallback) {
		t.Fatalf("fallback route = %v, %v; want fallback", got, ok)
	}
	if got, ok := chain.pick(now.Add(2 * time.Minute)); !ok || routeIdentity(got) != routeIdentity(primary) {
		t.Fatalf("recovered route = %v, %v; want primary after reset", got, ok)
	}
}

func TestExecutorChainAvailabilityKeyAndEarliest(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	primary := agents.Route{Agent: "pi", Model: "zai/glm-5.3", Effort: "max"}
	otherEffort := agents.Route{Agent: "pi", Model: "zai/glm-5.3", Effort: "high"}
	fallback := agents.Route{Agent: "codex", Model: "gpt-5.5"}
	config := map[string]any{"agents": map[string]any{
		"pi":    map[string]any{"limit_wait": float64(1800)},
		"codex": map[string]any{},
	}}
	chain := newExecutorChain([]agents.Route{primary, otherEffort, fallback}, config, nil, true)
	reset := float64(now.Add(3 * time.Minute).Unix())
	if _, err := chain.mark(primary, &agentio.Refusal{Kind: agentio.RefusalLimit, Reset: &reset}, now); err != nil {
		t.Fatal(err)
	}
	if got, ok := chain.pick(now); !ok || routeIdentity(got) != routeIdentity(fallback) {
		t.Fatalf("route after shared agent/model refusal = %v, %v; want codex", got, ok)
	}
	if _, err := chain.mark(fallback, &agentio.Refusal{Kind: agentio.RefusalOutage}, now); err != nil {
		t.Fatal(err)
	}
	want := now.Add(4 * time.Minute)
	got, ok := chain.earliest()
	if !ok || !got.Equal(want) {
		t.Fatalf("earliest = %v, %v; want %v", got, ok, want)
	}
}

func TestExecutorChainThrottleDoesNotMarkRoute(t *testing.T) {
	now := time.Now()
	route := agents.Route{Agent: "pi", Model: "zai/glm-5.3"}
	chain := newExecutorChain([]agents.Route{route}, nil, nil, true)
	if _, err := chain.mark(route, &agentio.Refusal{Kind: agentio.RefusalThrottle}, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := chain.earliest(); ok {
		t.Fatal("throttle made route unavailable")
	}
	if got, ok := chain.pick(now); !ok || routeIdentity(got) != routeIdentity(route) {
		t.Fatalf("route after throttle = %v, %v", got, ok)
	}
}
