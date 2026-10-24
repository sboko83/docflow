// Package roster reads the personal executor roster and picks a route
// chain for every plan role. The runner never reads the roster: it only
// feeds roster show (print the chains) and roster apply (write them into
// a plan header), so a plan stays reproducible without the file.
package roster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/lint"
)

// Roles is the fixed set of roster roles in plan-header order. The
// executor role renders as the executor:/fallback: header pair, the rest
// as role keys.
var Roles = []string{"executor", "cheap", "heavy", "review", "acceptance", "replan"}

// tiers is the route tier ladder: cheap < standard < strong.
var tiers = []string{"cheap", "standard", "strong"}

var tierRequirements = append(append([]string(nil), tiers...), "trusted")

// providerFlags are the plan-composition flags of a provider.
type providerFlags struct {
	check   bool
	trusted bool
}

// route is one named [route.<name>] entry in file order.
type route struct {
	name     string
	triple   agents.Route
	tier     string
	provider string
}

// Roster is a validated roster: the spend order, effective provider flags,
// the per-role minimum tier, the routes in file order, and the picked
// chain of every role.
type Roster struct {
	Path      string
	Profile   string
	Spend     []string
	Providers map[string]providerFlags
	RoleTiers map[string]string
	Routes    []route
	Chains    map[string][]agents.Route
}

// DefaultPath returns the roster path the way agents.ConfigPath builds the
// agents path: $XDG_CONFIG_HOME/docflow/roster.toml, ~/.config otherwise.
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			base = "~/.config"
		} else {
			base = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(base, "docflow", "roster.toml")
}

// Load reads, parses and validates the roster at path ("" means
// DefaultPath); profileName selects a [profile.NAME] section that replaces
// the spend order and the flags of the providers it lists. Every problem
// comes back as an exit-code-2 error naming file and line.
func Load(path, profileName string) (*Roster, error) {
	if path == "" {
		path = DefaultPath()
	}
	path = expandUser(path)
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster file: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	root, err := agents.ParseTOML(string(text))
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", path, err), Code: clierrors.EXIT_FORMAT}
	}
	index := scanLines(string(text))
	r := &Roster{Path: path, Profile: profileName}
	if err := r.parse(index, root); err != nil {
		return nil, err
	}
	if err := applyProfile(r, index, root, profileName); err != nil {
		return nil, err
	}
	if err := r.checkRoutes(index); err != nil {
		return nil, err
	}
	if r.Chains, err = r.pickChains(index); err != nil {
		return nil, err
	}
	return r, nil
}

// failAt returns a located exit-code-2 error; a line of 0 from the index
// (no entry — exotic syntax) becomes line 1, the file head.
func failAt(path string, index *lineIndex, table, key, format string, args ...any) error {
	line := index.line(table, key)
	if line == 0 {
		line = 1
	}
	return clierrors.FormatError{Path: path, Line: line, Text: fmt.Sprintf(format, args...)}
}

func (r *Roster) parse(index *lineIndex, root map[string]any) error {
	for _, key := range index.keysByLine("", root) {
		switch key {
		case "spend", "provider", "profile", "role", "route":
		default:
			return failAt(r.Path, index, "", key, "unknown key '%s'", key)
		}
	}
	spend, err := parseSpend(r.Path, index, "", "spend", root["spend"])
	if err != nil {
		return err
	}
	r.Spend = spend
	providers, err := parseProviders(r.Path, index, "provider", "provider", root["provider"])
	if err != nil {
		return err
	}
	r.Providers = providers
	roleTiers, err := r.parseRoles(index, root["role"])
	if err != nil {
		return err
	}
	r.RoleTiers = roleTiers
	routes, err := parseRoutes(r.Path, index, root["route"])
	if err != nil {
		return err
	}
	r.Routes = routes
	return nil
}

func (r *Roster) parseRoles(index *lineIndex, value any) (map[string]string, error) {
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

func parseRoutes(path string, index *lineIndex, value any) ([]route, error) {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return nil, failAt(path, index, "route", "", "route must be a table of [route.<name>] sections")
	}
	routes := make([]route, 0, len(table))
	for _, name := range index.keysByLine("route", table) {
		section, ok := table[name].(map[string]any)
		if !ok {
			return nil, failAt(path, index, "route", name, "route %s must be a [route.%s] table", name, name)
		}
		rt := route{name: name}
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
		rt.provider = routeProvider(agent, model)
		tier, _ := section["tier"].(string)
		if !contains(tiers, tier) {
			return nil, failAt(path, index, "route."+name, "tier", "route %s: tier must be cheap, standard or strong", name)
		}
		rt.tier = tier
		routes = append(routes, rt)
	}
	return routes, nil
}

// routeProvider names the plan-composition provider of a route: for pi the
// model prefix before "/" (a model without "/" runs on the pi
// subscription itself), for the other agents the agent name.
func routeProvider(agent, model string) string {
	if agent != "pi" {
		return agent
	}
	if prefix, _, found := strings.Cut(model, "/"); found && prefix != "" {
		return prefix
	}
	return "pi"
}

// parseProviders reads a provider-flag table ([provider.<name>] or the
// overrides of [profile.<name>.provider.<name>]); tablePath locates keys
// in the line index, label names the table in messages.
func parseProviders(path string, index *lineIndex, tablePath, label string, value any) (map[string]providerFlags, error) {
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
	providers := make(map[string]providerFlags, len(table))
	for _, name := range index.keysByLine(tablePath, table) {
		section, ok := table[name].(map[string]any)
		if !ok {
			return nil, failAt(path, index, tablePath, name, "provider %s must be a [%s.%s] table", name, label, name)
		}
		var flags providerFlags
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

// parseSpend reads a spend array; tablePath is the table that holds the
// spend key — every error points at that key's line — and label names
// the spend in messages.
func parseSpend(path string, index *lineIndex, tablePath, label string, value any) ([]string, error) {
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

// applyProfile replaces the spend order and the flags of the providers the
// profile lists; providers outside the profile keep the top-level flags.
func applyProfile(r *Roster, index *lineIndex, root map[string]any, name string) error {
	if name == "" {
		return nil
	}
	table, ok := root["profile"].(map[string]any)
	if root["profile"] != nil && !ok {
		return failAt(r.Path, index, "profile", "", "profile must be a table of [profile.<name>] sections")
	}
	value, exists := table[name]
	if !exists {
		message := fmt.Sprintf("docflow: %s: unknown profile '%s'", r.Path, name)
		if known := index.keysByLine("profile", table); len(known) > 0 {
			message += fmt.Sprintf(" (defined: %s)", strings.Join(known, ", "))
		}
		return clierrors.DocflowError{Message: message, Code: clierrors.EXIT_FORMAT}
	}
	section, ok := value.(map[string]any)
	if !ok {
		return failAt(r.Path, index, "profile", name, "profile %s must be a [profile.%s] table", name, name)
	}
	tablePath := "profile." + name
	for _, key := range index.keysByLine(tablePath, section) {
		switch key {
		case "spend", "provider":
		default:
			return failAt(r.Path, index, tablePath, key, "unknown key '%s' in [profile.%s]", key, name)
		}
	}
	if _, ok := section["spend"]; ok {
		spend, err := parseSpend(r.Path, index, tablePath, "profile "+name, section["spend"])
		if err != nil {
			return err
		}
		r.Spend = spend
	}
	if _, ok := section["provider"]; ok {
		// Any type goes to parseProviders: a non-table value must fail
		// with the key's line, not pass silently as a missing override.
		flags, err := parseProviders(r.Path, index, tablePath+".provider", "profile "+name+".provider", section["provider"])
		if err != nil {
			return err
		}
		providers := make(map[string]providerFlags, len(r.Providers)+len(flags))
		for provider, value := range r.Providers {
			providers[provider] = value
		}
		for provider, value := range flags {
			providers[provider] = value
		}
		r.Providers = providers
	}
	return nil
}

// checkRoutes refuses routes whose provider is not in the effective spend
// order: such a route is never picked and only hides a typo.
func (r *Roster) checkRoutes(index *lineIndex) error {
	for _, rt := range r.Routes {
		if !contains(r.Spend, rt.provider) {
			return failAt(r.Path, index, "route."+rt.name, "run", "route %s: provider %s is not in spend", rt.name, rt.provider)
		}
	}
	return nil
}

// pickChains selects the chain of every roster role: providers in spend
// order, one route each — the smallest tier that meets the requirement,
// first in file on a tie. A chain shorter than two routes is a roster
// error: without a reserve the usage limit of the single provider stops
// the run until reset.
func (r *Roster) pickChains(index *lineIndex) (map[string][]agents.Route, error) {
	chains := make(map[string][]agents.Route, len(Roles))
	for _, role := range Roles {
		chain := r.selectChain(role)
		if len(chain) < 2 {
			return nil, failAt(r.Path, index, "role", role, "role %s: chain has %d route(s), need at least two providers", role, len(chain))
		}
		chains[role] = chain
	}
	return chains, nil
}

// selectChain picks the routes of one role. Providers with the check
// flag never review — whatever the role requires, trusted included: the
// point of check is that another provider judges this provider's units,
// and the tier of the requirement does not change that.
func (r *Roster) selectChain(role string) []agents.Route {
	reviews := role == "review"
	if r.RoleTiers[role] == "trusted" {
		var chain []agents.Route
		for _, name := range r.Spend {
			flags := r.Providers[name]
			if reviews && flags.check {
				continue
			}
			if !flags.trusted {
				continue
			}
			if rt := r.pickRoute(name, tierIndex("strong"), role); rt != nil {
				chain = append(chain, rt.triple)
			}
		}
		for _, name := range r.Spend {
			flags := r.Providers[name]
			if flags.trusted || flags.check {
				continue
			}
			if rt := r.pickRoute(name, tierIndex("strong"), role); rt != nil {
				chain = append(chain, rt.triple)
			}
		}
		return chain
	}
	minimum := tierIndex(r.RoleTiers[role])
	var chain []agents.Route
	for _, name := range r.Spend {
		if reviews && r.Providers[name].check {
			continue
		}
		if rt := r.pickRoute(name, minimum, role); rt != nil {
			chain = append(chain, rt.triple)
		}
	}
	return chain
}

// pickRoute returns the provider's route with the smallest tier not below
// minimum; on a tie the first route in file order wins, so routes stay in
// file order.
func (r *Roster) pickRoute(provider string, minimum int, role string) *route {
	var best *route
	for i := range r.Routes {
		rt := &r.Routes[i]
		if rt.provider != provider || tierIndex(rt.tier) < minimum {
			continue
		}
		if best == nil || tierIndex(rt.tier) < tierIndex(best.tier) {
			best = rt
		}
	}
	return best
}

func tierIndex(tier string) int {
	for i, name := range tiers {
		if name == tier {
			return i
		}
	}
	return len(tiers)
}

// Show renders the roster as plan-header lines: a spend summary line and
// the executor:/fallback: pair plus one chain line per role key.
func (r *Roster) Show() string {
	var checks, trusteds []string
	for _, name := range r.Spend {
		if r.Providers[name].check {
			checks = append(checks, name)
		}
		if r.Providers[name].trusted {
			trusteds = append(trusteds, name)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "spend: %s · check: %s · trusted: %s\n", strings.Join(r.Spend, ", "), orDash(checks), orDash(trusteds))
	executor := r.Chains["executor"]
	fmt.Fprintf(&b, "executor: %s\n", agents.RouteText(executor[0]))
	fmt.Fprintf(&b, "fallback: %s\n", chainText(executor[1:]))
	for _, role := range Roles[1:] {
		fmt.Fprintf(&b, "%s: %s\n", role, chainText(r.Chains[role]))
	}
	return b.String()
}

func chainText(routes []agents.Route) string {
	texts := make([]string, 0, len(routes))
	for _, route := range routes {
		texts = append(texts, agents.RouteText(route))
	}
	return strings.Join(texts, "; ")
}

func orDash(names []string) string {
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, ", ")
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func expandUser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
