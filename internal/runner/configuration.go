package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/lint"
)

const configurationVersion = 1

// RunConfiguration is the interactive run's explicit assignment overlay. It
// never rewrites the plan or the personal roster: only the roles and units a
// person confirmed are kept, and the effective snapshot records what the next
// start will actually use.
type RunConfiguration struct {
	Version   int                       `json:"version"`
	Roles     map[string][]agents.Route `json:"roles,omitempty"`
	Units     map[string][]agents.Route `json:"units,omitempty"`
	Effective *EffectiveConfiguration   `json:"effective,omitempty"`
}

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

// NewRunConfiguration returns an empty overlay with initialized maps.
func NewRunConfiguration() *RunConfiguration {
	return &RunConfiguration{Version: configurationVersion, Roles: map[string][]agents.Route{}, Units: map[string][]agents.Route{}}
}

// ConfigurationPath names the saved interactive configuration. It lives under
// the git directory, so changing a model never makes the working tree dirty.
func ConfigurationPath(root string) (string, error) {
	state, err := cycle.StateDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(state), "tui", "configuration.json"), nil
}

// LoadConfiguration reads the saved overlay. A missing file is an empty
// overlay, not an error: the next start then uses the plan assignments.
func LoadConfiguration(root string) (*RunConfiguration, error) {
	path, err := ConfigurationPath(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NewRunConfiguration(), nil
	}
	if err != nil {
		return nil, err
	}
	var configuration RunConfiguration
	if err := json.Unmarshal(data, &configuration); err != nil {
		return nil, fmt.Errorf("runner: invalid run configuration: %w", err)
	}
	if configuration.Version != configurationVersion {
		return nil, fmt.Errorf("runner: unsupported run configuration version %d", configuration.Version)
	}
	configuration.normalize()
	return &configuration, nil
}

func (c *RunConfiguration) normalize() {
	if c.Roles == nil {
		c.Roles = map[string][]agents.Route{}
	}
	if c.Units == nil {
		c.Units = map[string][]agents.Route{}
	}
}

func (c *RunConfiguration) hasOverrides() bool {
	return c != nil && (len(c.Roles) > 0 || len(c.Units) > 0)
}

// WriteConfiguration stores the overlay and its effective snapshot atomically.
func WriteConfiguration(root string, configuration *RunConfiguration) error {
	if configuration == nil {
		return errors.New("runner: no run configuration to write")
	}
	configuration.Version = configurationVersion
	configuration.normalize()
	path, err := ConfigurationPath(root)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".configuration-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SaveConfiguration validates the confirmed overlay against the plan and the
// agent profiles, resolves the effective assignments, and writes one snapshot.
// A rejected edit leaves the previous file untouched.
func SaveConfiguration(root string, plan *format.Plan, args agents.RunArgs, agentConfig map[string]any, configuration *RunConfiguration) (*RunConfiguration, error) {
	if configuration == nil {
		configuration = NewRunConfiguration()
	}
	if err := ValidateConfiguration(configuration, plan, agentConfig); err != nil {
		return nil, err
	}
	effective, err := EffectiveAssignments(plan, args, agentConfig, configuration)
	if err != nil {
		return nil, err
	}
	configuration.Effective = effective
	if err := WriteConfiguration(root, configuration); err != nil {
		return nil, err
	}
	return configuration, nil
}

// ValidateConfiguration checks an overlay with the same route grammar and
// agent profiles the runner uses. Finished and skipped units cannot be
// reassigned, and a role or unit with no route is refused instead of silently
// falling back to the plan.
func ValidateConfiguration(configuration *RunConfiguration, plan *format.Plan, agentConfig map[string]any) error {
	if configuration == nil {
		return nil
	}
	if configuration.Version != 0 && configuration.Version != configurationVersion {
		return fmt.Errorf("runner: unsupported run configuration version %d", configuration.Version)
	}
	if plan == nil {
		return errors.New("runner: a run configuration needs a plan")
	}
	usedRoles := map[string]bool{}
	for _, unit := range plan.Units {
		if field := unit.Field("Role"); field != nil {
			usedRoles[field.Value] = true
		}
	}
	for role := range configuration.Roles {
		if !usedRoles[role] {
			return fmt.Errorf("runner: role %q is not used by %s", role, plan.Meta["id"])
		}
	}
	for role, routes := range configuration.Roles {
		if err := validateRouteChain(routes, agentConfig); err != nil {
			return fmt.Errorf("runner: role %s: %w", role, err)
		}
	}
	for id, routes := range configuration.Units {
		unit := plan.Unit(id)
		if unit == nil {
			return fmt.Errorf("runner: unit %s is not in %s", id, plan.Meta["id"])
		}
		if doneStatus(unit.Status) {
			return fmt.Errorf("runner: unit %s is %s and cannot be reassigned", id, unit.Status)
		}
		if err := validateRouteChain(routes, agentConfig); err != nil {
			return fmt.Errorf("runner: unit %s: %w", id, err)
		}
	}
	return nil
}

func validateRouteChain(routes []agents.Route, agentConfig map[string]any) error {
	if len(routes) == 0 {
		return errors.New("the chain needs at least one route")
	}
	for i, route := range routes {
		_, _, _, parseErr := lint.ParseExecutor(agents.RouteText(route))
		if parseErr != nil && !strings.Contains(parseErr.Error(), "unknown agent") {
			return fmt.Errorf("route %d: %w", i+1, parseErr)
		}
		// The built-in grammar knows the shipped agents; a legacy plan may
		// still use a profile of its own config, so the profile is the
		// authority for the agent name.
		if agentConfig != nil {
			profiles, _ := agentConfig["agents"].(map[string]any)
			if _, ok := profiles[route.Agent]; !ok {
				if parseErr != nil {
					return fmt.Errorf("route %d: %w", i+1, parseErr)
				}
				return fmt.Errorf("route %d: agent %q has no profile", i+1, route.Agent)
			}
		} else if parseErr != nil {
			return fmt.Errorf("route %d: %w", i+1, parseErr)
		}
	}
	return nil
}

// EffectiveAssignments resolves the plan assignments under the overlay. A unit
// override wins over a role override; without either the plan's own route or
// chain is used. The result is the only table the TUI shows before a start.
func EffectiveAssignments(plan *format.Plan, args agents.RunArgs, agentConfig map[string]any, configuration *RunConfiguration) (*EffectiveConfiguration, error) {
	if plan == nil {
		return nil, errors.New("runner: no plan for the effective assignments")
	}
	if configuration == nil {
		configuration = NewRunConfiguration()
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
		routes, err := effectiveUnitRoutes(plan, unit, args, agentConfig, configuration)
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
		routes, err := effectiveRoleRoutes(plan, role, configuration)
		if err != nil {
			return nil, err
		}
		if len(routes) > 0 {
			effective.Roles[role] = append([]agents.Route(nil), routes...)
		}
	}
	return effective, nil
}

func effectiveUnitRoutes(plan *format.Plan, unit *format.Unit, args agents.RunArgs, agentConfig map[string]any, configuration *RunConfiguration) ([]agents.Route, error) {
	if routes, ok := configuration.unitOverride(plan, unit); ok {
		return routes, nil
	}
	if lint.ExecutorMode(plan) {
		return agents.UnitRoleRoutes(plan, unit)
	}
	route, err := agents.ResolveRoute(agentConfig, unit, args)
	if err != nil {
		return nil, err
	}
	return []agents.Route{route}, nil
}

func effectiveRoleRoutes(plan *format.Plan, role string, configuration *RunConfiguration) ([]agents.Route, error) {
	if routes := configuration.Roles[role]; len(routes) > 0 {
		return routes, nil
	}
	value := plan.Meta[role]
	if value == "" {
		return nil, nil
	}
	return agents.ChainRoutes(value)
}

// unitOverride returns the confirmed chain for a unit: its own override or the
// chain of its Role.
func (c *RunConfiguration) unitOverride(plan *format.Plan, unit *format.Unit) ([]agents.Route, bool) {
	if c == nil || unit == nil {
		return nil, false
	}
	if routes := c.Units[unit.ID]; len(routes) > 0 {
		return routes, true
	}
	if field := unit.Field("Role"); field != nil {
		if routes := c.Roles[field.Value]; len(routes) > 0 {
			return routes, true
		}
	}
	return nil, false
}

// loadControlledConfiguration loads and re-resolves the overlay before a
// controlled run, so the saved effective snapshot always matches the current
// plan. A plain CLI run keeps its old assignment rules.
func loadControlledConfiguration(root string, plan *format.Plan, args agents.RunArgs, agentConfig map[string]any) (*RunConfiguration, error) {
	path, err := ConfigurationPath(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		// A person who never opened the model editor has no snapshot to
		// refresh; the plan assignments are already the effective table.
		return NewRunConfiguration(), nil
	}
	configuration, err := LoadConfiguration(root)
	if err != nil {
		return nil, err
	}
	if configuration.hasOverrides() {
		if err := ValidateConfiguration(configuration, plan, agentConfig); err != nil {
			return nil, err
		}
	}
	effective, err := EffectiveAssignments(plan, args, agentConfig, configuration)
	if err != nil {
		return nil, err
	}
	configuration.Effective = effective
	if err := WriteConfiguration(root, configuration); err != nil {
		return nil, err
	}
	return configuration, nil
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
	return strings.Join(parts, " ; ")
}
