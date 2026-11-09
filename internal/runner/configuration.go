package runner

import (
	"errors"
	"sort"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
)

// EffectiveConfiguration is the resolved assignment table of one plan: every
// open unit with the chain the run will use and the role chains it may ask
// review and acceptance to follow.
type EffectiveConfiguration struct {
	Plan        string                    `json:"plan,omitempty"`
	Permissions string                    `json:"permissions,omitempty"`
	Roles       map[string][]agents.Route `json:"roles,omitempty"`
	Units       []UnitConfiguration       `json:"units"`
}

// UnitConfiguration is one row of the effective table.
type UnitConfiguration struct {
	Unit   string         `json:"unit"`
	Title  string         `json:"title,omitempty"`
	Status string         `json:"status,omitempty"`
	Role   string         `json:"role,omitempty"`
	Routes []agents.Route `json:"routes"`
}

// EffectiveAssignments resolves the plan assignments: the unit's own route or
// chain, otherwise the plan header's role, executor and fallback. The result
// is the table shown when a run starts or recovers.
func EffectiveAssignments(plan *format.Plan, args agents.RunArgs, agentConfig map[string]any) (*EffectiveConfiguration, error) {
	if plan == nil {
		return nil, errors.New("runner: no plan for the effective assignments")
	}
	effective := &EffectiveConfiguration{
		Plan:        plan.Meta["id"],
		Permissions: format.PlanPermissions(plan),
		Roles:       map[string][]agents.Route{},
		Units:       []UnitConfiguration{},
	}
	roles := map[string]bool{}
	for _, unit := range plan.Units {
		if doneStatus(unit.Status) {
			continue
		}
		assignment := UnitConfiguration{Unit: unit.ID, Title: unit.Title, Status: unit.Status}
		if field := unit.Field("Role"); field != nil {
			assignment.Role = field.Value
			roles[field.Value] = true
		}
		routes, err := effectiveUnitRoutes(plan, unit, args, agentConfig)
		if err != nil {
			return nil, err
		}
		assignment.Routes = append([]agents.Route(nil), routes...)
		effective.Units = append(effective.Units, assignment)
	}
	roleNames := make([]string, 0, len(roles))
	for role := range roles {
		roleNames = append(roleNames, role)
	}
	sort.Strings(roleNames)
	for _, role := range roleNames {
		routes, err := effectiveRoleRoutes(plan, role)
		if err != nil {
			return nil, err
		}
		if len(routes) > 0 {
			effective.Roles[role] = append([]agents.Route(nil), routes...)
		}
	}
	return effective, nil
}

func effectiveUnitRoutes(plan *format.Plan, unit *format.Unit, args agents.RunArgs, agentConfig map[string]any) ([]agents.Route, error) {
	if lint.ExecutorMode(plan) {
		// The plan header assigns the chain: the table shows it even before
		// run args fix the mode, so dialogs resolve the same routes as a run.
		return agents.UnitRoleRoutes(plan, unit)
	}
	route, err := agents.ResolveRoute(agentConfig, unit, args)
	if err != nil {
		return nil, err
	}
	return []agents.Route{route}, nil
}

func effectiveRoleRoutes(plan *format.Plan, role string) ([]agents.Route, error) {
	value := plan.Meta[role]
	if value == "" {
		return nil, nil
	}
	return agents.ChainRoutes(value)
}

// AssignmentLines renders the expanded assignment table of the start and
// recovery dialogs: the effective routes of every open unit and role.
func AssignmentLines(effective *EffectiveConfiguration) []string {
	return assignmentLines(effective)
}

// assignmentLines renders the expanded assignment table of the start dialog.
func assignmentLines(effective *EffectiveConfiguration) []string {
	if effective == nil || len(effective.Units) == 0 {
		return nil
	}
	lines := []string{"", "assignments (all open units):"}
	for _, unit := range effective.Units {
		label := unit.Unit
		if unit.Role != "" {
			label += " [" + unit.Role + "]"
		}
		lines = append(lines, "  "+label+": "+routeChainText(unit.Routes))
	}
	roles := make([]string, 0, len(effective.Roles))
	for role := range effective.Roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		lines = append(lines, "  role "+role+": "+routeChainText(effective.Roles[role]))
	}
	lines = append(lines, "", "The first route is assigned; after a provider limit the run switches to the next fallback, so the actual executor can differ.")
	return lines
}

func routeChainText(routes []agents.Route) string {
	parts := make([]string, 0, len(routes))
	for _, route := range routes {
		parts = append(parts, agents.RouteText(route))
	}
	return strings.Join(parts, "; ")
}
