package roster

// The version 1 roster reader lives in this file and nowhere else: it
// feeds `docflow roster migrate`, the only command that still reads the
// old format. Every other command loads the version 2 policy through
// LoadPolicy, so a v1 file exits 2 with the migration hint.

import (
	"fmt"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/lint"
)

// legacyFlags are the plan-composition flags of a v1 provider.
type legacyFlags struct {
	check   bool
	trusted bool
}

// legacyRoute is one named [route.<name>] entry in file order.
type legacyRoute struct {
	name     string
	triple   agents.Route
	tier     string
	provider string
}

// legacyRoster is a parsed v1 roster: the spend order, the provider flags,
// the role requirements and the routes in file order.
type legacyRoster struct {
	Path      string
	Spend     []string
	Providers map[string]legacyFlags
	RoleTiers map[string]string
	Routes    []legacyRoute
}

// legacy is a parsed v1 roster file: the tables the v1 roster had and
// every profile, because a migration converts all profiles at once.
type legacy struct {
	path     string
	index    *lineIndex
	roster   *legacyRoster
	profiles []legacyProfile
}

// legacyProfile is one v1 [profile.NAME]: the spend order it replaces and
// the provider-flag overrides. providers holds the overrides and order
// their file order, so the migration output never depends on map
// iteration.
type legacyProfile struct {
	name      string
	spend     []string
	providers map[string]legacyFlags
	order     []string
}

// parseLegacy reads a v1 roster. Any version key means the text is not v1
// — version 2 is already migrated, another version is unknown — and the
// v1 tables are validated the way the v1 roster was, so a roster the old
// reader rejected does not migrate either.
func parseLegacy(rosterPath, text string) (*legacy, error) {
	root, err := agents.ParseTOML(text)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", rosterPath, err), Code: clierrors.EXIT_FORMAT}
	}
	index := scanLines(text)
	if value, ok := root["version"]; ok {
		if version, isInt := value.(int64); isInt && version == PolicyVersion {
			return nil, failAt(rosterPath, index, "", "version", "the roster is already version %d; nothing to migrate", PolicyVersion)
		}
		return nil, failAt(rosterPath, index, "", "version", "version %v is not the v1 roster format: only a roster without a version key is migrated", value)
	}
	value := &legacyRoster{Path: rosterPath}
	if err := value.parse(index, root); err != nil {
		return nil, err
	}
	if err := value.checkRoutes(index); err != nil {
		return nil, err
	}
	profiles, err := legacyProfiles(rosterPath, index, root["profile"])
	if err != nil {
		return nil, err
	}
	return &legacy{path: rosterPath, index: index, roster: value, profiles: profiles}, nil
}

// legacyProfiles reads every [profile.NAME] in file order. The v1 roster
// validated only the profile it applied; a migration reads them all, so a
// broken profile fails here instead of disappearing from the output.
func legacyProfiles(rosterPath string, index *lineIndex, value any) ([]legacyProfile, error) {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return nil, failAt(rosterPath, index, "profile", "", "profile must be a table of [profile.<name>] sections")
	}
	profiles := make([]legacyProfile, 0, len(table))
	for _, name := range index.keysByLine("profile", table) {
		section, ok := table[name].(map[string]any)
		if !ok {
			return nil, failAt(rosterPath, index, "profile", name, "profile %s must be a [profile.%s] table", name, name)
		}
		tablePath := "profile." + name
		profile := legacyProfile{name: name}
		for _, key := range index.keysByLine(tablePath, section) {
			switch key {
			case "spend", "provider":
			default:
				return nil, failAt(rosterPath, index, tablePath, key, "unknown key '%s' in [profile.%s]", key, name)
			}
		}
		if value, ok := section["spend"]; ok {
			spend, err := parseLegacySpend(rosterPath, index, tablePath, "profile "+name, value)
			if err != nil {
				return nil, err
			}
			profile.spend = spend
		}
		if value, ok := section["provider"]; ok {
			providers, err := parseLegacyProviders(rosterPath, index, tablePath+".provider", "profile "+name+".provider", value)
			if err != nil {
				return nil, err
			}
			profile.providers = providers
			overrides, _ := section["provider"].(map[string]any)
			profile.order = index.keysByLine(tablePath+".provider", overrides)
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func (r *legacyRoster) parse(index *lineIndex, root map[string]any) error {
	for _, key := range index.keysByLine("", root) {
		switch key {
		case "spend", "provider", "profile", "role", "route":
		default:
			return failAt(r.Path, index, "", key, "unknown key '%s'", key)
		}
	}
	spend, err := parseLegacySpend(r.Path, index, "", "spend", root["spend"])
	if err != nil {
		return err
	}
	r.Spend = spend
	providers, err := parseLegacyProviders(r.Path, index, "provider", "provider", root["provider"])
	if err != nil {
		return err
	}
	r.Providers = providers
	roleTiers, err := r.parseRoles(index, root["role"])
	if err != nil {
		return err
	}
	r.RoleTiers = roleTiers
	routes, err := parseLegacyRoutes(r.Path, index, root["route"])
	if err != nil {
		return err
	}
	r.Routes = routes
	return nil
}

func (r *legacyRoster) parseRoles(index *lineIndex, value any) (map[string]string, error) {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return nil, failAt(r.Path, index, "role", "", "role must be a table of [role] requirements")
	}
	roleTiers := make(map[string]string, len(Roles))
	for _, key := range index.keysByLine("role", table) {
		if !contains(Roles, key) {
			return nil, failAt(r.Path, index, "role", key, "unknown role '%s' (known: %s)", key, strings.Join(Roles, ", "))
		}
		requirement, ok := table[key].(string)
		if !ok || !contains(tierRequirements, requirement) {
			return nil, failAt(r.Path, index, "role", key, "role %s: requirement must be cheap, standard, strong or trusted", key)
		}
		roleTiers[key] = requirement
	}
	for _, role := range Roles {
		if _, ok := roleTiers[role]; !ok {
			return nil, failAt(r.Path, index, "role", "", "role %s: no requirement in [role]", role)
		}
	}
	return roleTiers, nil
}

func parseLegacyRoutes(path string, index *lineIndex, value any) ([]legacyRoute, error) {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return nil, failAt(path, index, "route", "", "route must be a table of [route.<name>] sections")
	}
	routes := make([]legacyRoute, 0, len(table))
	for _, name := range index.keysByLine("route", table) {
		section, ok := table[name].(map[string]any)
		if !ok {
			return nil, failAt(path, index, "route", name, "route %s must be a [route.%s] table", name, name)
		}
		rt := legacyRoute{name: name}
		for _, key := range index.keysByLine("route."+name, section) {
			switch key {
			case "run", "tier":
			default:
				return nil, failAt(path, index, "route."+name, key, "unknown key '%s' in [route.%s]", key, name)
			}
		}
		run, _ := section["run"].(string)
		agent, model, effort, err := lint.ParseExecutor(run)
		if err != nil {
			return nil, failAt(path, index, "route."+name, "run", "route %s: run: %v", name, err)
		}
		rt.triple = agents.Route{Agent: agent, Model: model}
		if effort != nil {
			rt.triple.Effort = *effort
		}
		rt.provider = legacyRouteProvider(agent, model)
		tier, _ := section["tier"].(string)
		if !contains(tiers, tier) {
			return nil, failAt(path, index, "route."+name, "tier", "route %s: tier must be cheap, standard or strong", name)
		}
		rt.tier = tier
		routes = append(routes, rt)
	}
	return routes, nil
}

// legacyRouteProvider names the v1 provider of a route: for pi the model
// prefix before "/" (a model without "/" runs on the pi subscription
// itself), for the other agents the agent name.
func legacyRouteProvider(agent, model string) string {
	if agent != "pi" {
		return agent
	}
	if prefix, _, found := strings.Cut(model, "/"); found && prefix != "" {
		return prefix
	}
	return "pi"
}

// parseLegacyProviders reads a provider-flag table ([provider.<name>] or
// the overrides of [profile.<name>.provider.<name>]); tablePath locates
// keys in the line index, label names the table in messages.
func parseLegacyProviders(path string, index *lineIndex, tablePath, label string, value any) (map[string]legacyFlags, error) {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		// Locate the key that carries the wrong type — the provider key
		// of the enclosing table — not the head of the file.
		parent, key := "", tablePath
		if dot := strings.LastIndex(tablePath, "."); dot >= 0 {
			parent, key = tablePath[:dot], tablePath[dot+1:]
		}
		return nil, failAt(path, index, parent, key, "%s must be a table of [%s.<name>] sections", label, label)
	}
	providers := make(map[string]legacyFlags, len(table))
	for _, name := range index.keysByLine(tablePath, table) {
		section, ok := table[name].(map[string]any)
		if !ok {
			return nil, failAt(path, index, tablePath, name, "provider %s must be a [%s.%s] table", name, label, name)
		}
		var flags legacyFlags
		for _, key := range index.keysByLine(tablePath+"."+name, section) {
			flag, ok := section[key].(bool)
			switch {
			case !ok:
				return nil, failAt(path, index, tablePath+"."+name, key, "provider %s: %s must be a boolean", name, key)
			case key == "check":
				flags.check = flag
			case key == "trusted":
				flags.trusted = flag
			default:
				return nil, failAt(path, index, tablePath+"."+name, key, "unknown key '%s' in [%s.%s]", key, label, name)
			}
		}
		providers[name] = flags
	}
	return providers, nil
}

// parseLegacySpend reads a spend array; tablePath is the table that holds
// the spend key — every error points at that key's line — and label names
// the spend in messages.
func parseLegacySpend(path string, index *lineIndex, tablePath, label string, value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, failAt(path, index, tablePath, "spend", "%s must be an array of provider names in spend order", label)
	}
	spend := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for n, item := range items {
		name, ok := item.(string)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, failAt(path, index, tablePath, "spend", "%s: item %d must be a provider name", label, n+1)
		}
		if seen[name] {
			return nil, failAt(path, index, tablePath, "spend", "%s: duplicate provider '%s'", label, name)
		}
		seen[name] = true
		spend = append(spend, name)
	}
	return spend, nil
}

// checkRoutes refuses routes whose provider is not in the top-level spend
// order: such a route was never picked and only hid a typo.
func (r *legacyRoster) checkRoutes(index *lineIndex) error {
	for _, rt := range r.Routes {
		if !contains(r.Spend, rt.provider) {
			return failAt(r.Path, index, "route."+rt.name, "run", "route %s: provider %s is not in spend", rt.name, rt.provider)
		}
	}
	return nil
}
