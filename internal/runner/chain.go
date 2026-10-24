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
	if args.Executors {
		// RunRoute checks that executor mode stayed fixed and validates all
		// assignments before the runner builds the unit's full chain.
		if _, err := agents.RunRoute(config, plan, unit, args); err != nil {
			return nil, err
		}
		routes, err := unitRoleRoutes(plan, unit)
		if err != nil {
			return nil, err
		}
		return newExecutorChain(routes, config, unavailable, true), nil
	}
	primary, err := agents.RunRoute(config, plan, unit, args)
	if err != nil {
		return nil, err
	}
	return routeExecutorChain(config, plan, primary, args, unavailable)
}

func replanExecutorChain(config map[string]any, plan *format.Plan, args agents.RunArgs, unavailable map[string]time.Time) (*executorChain, error) {
	routes, err := chainRoutes(plan.Meta["replan"])
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("replan needs at least one route")
	}
	return newExecutorChain(routes, config, unavailable, args.Executors), nil
}

func routeExecutorChain(config map[string]any, plan *format.Plan, primary agents.Route, args agents.RunArgs, unavailable map[string]time.Time) (*executorChain, error) {
	routes := []agents.Route{primary}
	if args.Executors {
		fallback, err := fallbackRoutes(plan)
		if err != nil {
			return nil, err
		}
		routes = append(routes, fallback...)
	}
	return newExecutorChain(routes, config, unavailable, args.Executors), nil
}

func fallbackRoutes(plan *format.Plan) ([]agents.Route, error) {
	return chainRoutes(plan.Meta["fallback"])
}

// unitRoleRoutes resolves the ordered route chain of a unit: an explicit
// Executor triple followed by the plan fallback, the chain of the unit's
// role key, or the plan executor followed by fallback.
func unitRoleRoutes(plan *format.Plan, unit *format.Unit) ([]agents.Route, error) {
	if field := unit.Field("Role"); field != nil {
		routes, err := chainRoutes(plan.Meta[field.Value])
		if err != nil {
			return nil, err
		}
		if len(routes) == 0 {
			return nil, fmt.Errorf("role %s needs at least one route", field.Value)
		}
		return routes, nil
	}
	value := ""
	if field := unit.Field("Executor"); field != nil {
		value = field.Value
	} else if head, ok := plan.Meta["executor"]; ok {
		value = head
	} else {
		return nil, fmt.Errorf("no executor assignment")
	}
	primary, err := routeOf(value)
	if err != nil {
		return nil, err
	}
	fallback, err := fallbackRoutes(plan)
	if err != nil {
		return nil, err
	}
	return append([]agents.Route{primary}, fallback...), nil
}

func routeOf(value string) (agents.Route, error) {
	name, model, effort, err := lint.ParseExecutor(value)
	if err != nil {
		return agents.Route{}, err
	}
	route := agents.Route{Agent: name, Model: model}
	if effort != nil {
		route.Effort = *effort
	}
	return route, nil
}

// chainRoutes parses a "agent | model | effort; ..." chain into routes;
// empty segments are skipped.
func chainRoutes(value string) ([]agents.Route, error) {
	var routes []agents.Route
	for _, raw := range strings.Split(value, ";") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		route, err := routeOf(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, nil
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
		c.used[quotaName(route)] = true
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
	return c.markUntil(route, until), nil
}

func (c *executorChain) markUntil(route agents.Route, until time.Time) time.Time {
	key := availabilityKey(route)
	if current := c.unavailable[key]; current.After(until) {
		until = current
	}
	c.unavailable[key] = until
	return until
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
		name := quotaName(route)
		if !c.used[name] {
			continue
		}
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

func (c *executorChain) routeAgents() []string {
	result := make([]string, 0, len(c.routes))
	seen := make(map[string]bool, len(c.routes))
	for _, route := range c.routes {
		name := quotaName(route)
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

// zaiQuota names the z.ai subscription quota: pi routes on zai/ models
// draw from the z.ai subscription, not from a pi-wide pool.
const zaiQuota = "zai"

// quotaName maps a route to the quota its usage draws from: pi on a zai/
// model shares the z.ai subscription, every other route its agent quota.
func quotaName(route agents.Route) string {
	if route.Agent == "pi" && agentio.IsZaiModel(route.Model) {
		return zaiQuota
	}
	return route.Agent
}

func availabilityKey(route agents.Route) string {
	return route.Agent + "|" + route.Model
}

type quotaSkip struct {
	agent string
	until time.Time
}

func markQuotaRoutes(routes []agents.Route, quotas map[string]agentio.Quota, unavailable map[string]time.Time, now time.Time) []quotaSkip {
	if unavailable == nil {
		unavailable = make(map[string]time.Time)
	}
	seenRoutes := make(map[string]bool)
	order := []string{}
	byAgent := make(map[string]time.Time)
	for _, route := range routes {
		key := availabilityKey(route)
		if seenRoutes[key] {
			continue
		}
		seenRoutes[key] = true
		name := quotaName(route)
		until, exhausted := quotaExhaustedUntil(quotas[name], now)
		if !exhausted {
			continue
		}
		current := unavailable[key]
		if current.After(until) {
			until = current
		}
		if !current.After(now) || until.After(current) {
			unavailable[key] = until
			if _, found := byAgent[name]; !found {
				order = append(order, name)
			}
			if until.After(byAgent[name]) {
				byAgent[name] = until
			}
		}
	}
	result := make([]quotaSkip, 0, len(order))
	for _, agent := range order {
		result = append(result, quotaSkip{agent: agent, until: byAgent[agent]})
	}
	return result
}

func quotaExhaustedUntil(quota agentio.Quota, now time.Time) (time.Time, bool) {
	var until time.Time
	for _, window := range quota {
		if window.Used < 1 {
			continue
		}
		reset := agentio.Epoch(window.Reset)
		if reset == nil {
			continue
		}
		at := time.Unix(int64(*reset), 0)
		if at.After(now) && at.After(until) {
			until = at
		}
	}
	return until, !until.IsZero()
}

// zaiRefusalReset refreshes the z.ai quota and returns the latest reset
// among the exhausted windows: a limit refusal that names no reset time
// waits for the subscription to recover instead of the profile limit_wait.
// nil keeps the profile wait.
func zaiRefusalReset() *float64 {
	until, ok := quotaExhaustedUntil(agentio.ProbeZaiQuota(agentio.QuotaProbeTimeout), time.Now())
	if !ok {
		return nil
	}
	seconds := float64(until.Unix())
	return &seconds
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
