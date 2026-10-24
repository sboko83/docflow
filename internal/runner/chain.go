package runner

import (
	"fmt"
	"strings"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
)

type executorChain struct {
	routes      []agents.Route
	config      map[string]any
	unavailable map[string]time.Time
	used        map[string]bool
	enabled     bool
}

func unitExecutorChain(config map[string]any, plan *format.Plan, unit *format.Unit, args agents.RunArgs, unavailable map[string]time.Time) (*executorChain, error) {
	primary, err := agents.RunRoute(config, plan, unit, args)
	if err != nil {
		return nil, err
	}
	routes := []agents.Route{primary}
	if args.Executors && plan.Meta["fallback"] != "" {
		for _, raw := range strings.Split(plan.Meta["fallback"], ";") {
			name, model, effort, err := lint.ParseExecutor(strings.TrimSpace(raw))
			if err != nil {
				return nil, err
			}
			route := agents.Route{Agent: name, Model: model}
			if effort != nil {
				route.Effort = *effort
			}
			routes = append(routes, route)
		}
	}
	return newExecutorChain(routes, config, unavailable, args.Executors), nil
}

func newExecutorChain(routes []agents.Route, config map[string]any, unavailable map[string]time.Time, enabled bool) *executorChain {
	if unavailable == nil {
		unavailable = make(map[string]time.Time)
	}
	chain := &executorChain{
		config: config, unavailable: unavailable, used: make(map[string]bool), enabled: enabled,
	}
	seen := make(map[string]bool, len(routes))
	for _, route := range routes {
		key := routeIdentity(route)
		if seen[key] {
			continue
		}
		seen[key] = true
		chain.routes = append(chain.routes, route)
	}
	return chain
}

func (c *executorChain) pick(now time.Time) (agents.Route, bool) {
	for _, route := range c.routes {
		until, blocked := c.unavailable[availabilityKey(route)]
		if blocked && now.Before(until) {
			continue
		}
		c.used[route.Agent] = true
		return route, true
	}
	return agents.Route{}, false
}

func (c *executorChain) firstRoute() (agents.Route, bool) {
	if len(c.routes) == 0 {
		return agents.Route{}, false
	}
	return c.routes[0], true
}

func (c *executorChain) mark(route agents.Route, refusal *agentio.Refusal, now time.Time) (time.Time, error) {
	if refusal == nil || (refusal.Kind != agentio.RefusalLimit && refusal.Kind != agentio.RefusalOutage) {
		return time.Time{}, nil
	}
	_, until, err := refusalSchedule(profile(c.config, route.Agent), refusal, now)
	if err != nil {
		return time.Time{}, err
	}
	key := availabilityKey(route)
	if current := c.unavailable[key]; current.After(until) {
		until = current
	}
	c.unavailable[key] = until
	return until, nil
}

func (c *executorChain) earliest() (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, route := range c.routes {
		until, blocked := c.unavailable[availabilityKey(route)]
		if !blocked {
			continue
		}
		if !found || until.Before(earliest) {
			earliest, found = until, true
		}
	}
	return earliest, found
}

func (c *executorChain) agentsUsed() []string {
	result := make([]string, 0, len(c.used))
	seen := make(map[string]bool, len(c.used))
	for _, route := range c.routes {
		if c.used[route.Agent] && !seen[route.Agent] {
			seen[route.Agent] = true
			result = append(result, route.Agent)
		}
	}
	return result
}

func (c *executorChain) routeAgents() []string {
	result := make([]string, 0, len(c.routes))
	seen := make(map[string]bool, len(c.routes))
	for _, route := range c.routes {
		if !seen[route.Agent] {
			seen[route.Agent] = true
			result = append(result, route.Agent)
		}
	}
	return result
}

func availabilityKey(route agents.Route) string {
	return route.Agent + "|" + route.Model
}

func routeIdentity(route agents.Route) string {
	return fmt.Sprintf("%s|%s|%s", route.Agent, route.Model, route.Effort)
}

func routeName(route agents.Route) string {
	model := route.Model
	if model == "" {
		model = "-"
	}
	return route.Agent + " " + model
}
