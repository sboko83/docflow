package roster

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
)

// The version 3 policy reader lives in this file and only the migration
// uses it. Version 3 described the roles indirectly: a [route] table gave
// every model and effort a class (cheap, standard, strong), [role] named
// the smallest class a role takes, and the access budget
// (prefer/normal/lean/minimal/off) gated the roles an access served. The
// migration resolves that indirection once, with the v3 rules, and writes
// the result as the explicit access × role table of version 4.

// v3PolicyVersion is the version marker of the policy this file reads;
// the v1 and v2 migrations produce it before it becomes version 4.
const v3PolicyVersion = 3

// The v3 access budgets, from most to least available.
var v3Budgets = []string{"prefer", "normal", "lean", "minimal", "off"}

// tiers is the v3 class ladder: cheap < standard < strong.
var tiers = []string{"cheap", "standard", "strong"}

var tierRequirements = append(append([]string(nil), tiers...), "trusted")

func tierIndex(tier string) int {
	for i, name := range tiers {
		if name == tier {
			return i
		}
	}
	return len(tiers)
}

type v3Access struct {
	Name      string
	Agents    []string
	Kind      string
	Budget    string
	Trusted   bool
	Check     bool
	Models    []string
	ModelsSet bool
	Exclude   []string
	Expires   time.Time
}

type v3Route struct {
	Access, Model, Eff, Class string
}

type v3Preset struct {
	Name                       string
	Off, Lean, Minimal, Prefer []string
	Order                      []string
	Models                     map[string][]string
}

type v3Policy struct {
	Accesses []v3Access
	Routes   []v3Route
	Roles    map[string]string
	Presets  []v3Preset
}

func (p *v3Policy) access(name string) *v3Access {
	for i := range p.Accesses {
		if p.Accesses[i].Name == name {
			return &p.Accesses[i]
		}
	}
	return nil
}

func (p *v3Policy) clone() *v3Policy {
	copied := &v3Policy{Routes: append([]v3Route(nil), p.Routes...), Roles: p.Roles, Presets: p.Presets}
	for _, access := range p.Accesses {
		access.Agents = append([]string(nil), access.Agents...)
		access.Models = append([]string(nil), access.Models...)
		access.Exclude = append([]string(nil), access.Exclude...)
		copied.Accesses = append(copied.Accesses, access)
	}
	return copied
}

// v3Error is the located-enough error of the v3 reader: the file and what
// is wrong. A v3 file was validated when it was written; the reader only
// refuses what it cannot convert.
func v3Error(path, format string, args ...any) error {
	return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %s", path, fmt.Sprintf(format, args...)), Code: clierrors.EXIT_FORMAT}
}

// parseV3Policy reads version 3 policy text.
func parseV3Policy(path, text string) (*v3Policy, error) {
	root, err := agents.ParseTOML(text)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", path, err), Code: clierrors.EXIT_FORMAT}
	}
	if version, ok := root["version"].(int64); !ok || version != v3PolicyVersion {
		return nil, v3Error(path, "version must be %d", v3PolicyVersion)
	}
	index := scanLines(text)
	policy := &v3Policy{Roles: map[string]string{}}
	for _, key := range index.keysByLine("", root) {
		switch key {
		case "version", "access", "route", "role", "preset":
		default:
			return nil, v3Error(path, "unknown key '%s'", key)
		}
	}
	accesses, _ := root["access"].(map[string]any)
	for _, name := range index.keysByLine("access", accesses) {
		section, ok := accesses[name].(map[string]any)
		if !ok {
			return nil, v3Error(path, "access %s must be a table", name)
		}
		access, err := parseV3Access(path, name, section)
		if err != nil {
			return nil, err
		}
		policy.Accesses = append(policy.Accesses, access)
	}
	routes, _ := root["route"].(map[string]any)
	for _, key := range index.keysByLine("route", routes) {
		access, model, err := splitModelKey(key)
		if err != nil {
			return nil, v3Error(path, "route %s: %v", key, err)
		}
		if policy.access(access) == nil {
			return nil, v3Error(path, "route %s: unknown access '%s'", key, access)
		}
		section, ok := routes[key].(map[string]any)
		if !ok || len(section) == 0 {
			return nil, v3Error(path, "route %s: at least one effort is required", key)
		}
		for _, eff := range index.keysByLine("route."+key, section) {
			class, ok := section[eff].(string)
			if !ok || !contains(tiers, class) {
				return nil, v3Error(path, "route %s: effort %s must map to %s", key, eff, orList(tiers))
			}
			policy.Routes = append(policy.Routes, v3Route{Access: access, Model: model, Eff: eff, Class: class})
		}
	}
	roles, _ := root["role"].(map[string]any)
	for _, role := range Roles {
		requirement, ok := roles[role].(string)
		if !ok || !contains(tierRequirements, requirement) {
			return nil, v3Error(path, "role %s: requirement must be %s", role, orList(tierRequirements))
		}
		policy.Roles[role] = requirement
	}
	presets, _ := root["preset"].(map[string]any)
	for _, name := range index.keysByLine("preset", presets) {
		section, ok := presets[name].(map[string]any)
		if !ok {
			return nil, v3Error(path, "preset %s must be a table", name)
		}
		preset, err := parseV3Preset(path, name, section)
		if err != nil {
			return nil, err
		}
		policy.Presets = append(policy.Presets, preset)
	}
	return policy, nil
}

func parseV3Access(path, name string, section map[string]any) (v3Access, error) {
	access := v3Access{Name: name, Budget: "normal"}
	for key, value := range section {
		switch key {
		case "agents":
			items, _ := value.([]any)
			list, err := nameList(items)
			if err != nil || len(list) == 0 {
				return access, v3Error(path, "access %s: agents must be a non-empty array", name)
			}
			access.Agents = list
		case "kind":
			access.Kind, _ = value.(string)
		case "budget":
			budget, ok := value.(string)
			if !ok || !contains(v3Budgets, budget) {
				return access, v3Error(path, "access %s: budget must be %s", name, orList(v3Budgets))
			}
			access.Budget = budget
		case "trusted":
			access.Trusted, _ = value.(bool)
		case "check":
			access.Check, _ = value.(bool)
		case "models", "exclude":
			globs, err := globList(value)
			if err != nil {
				return access, v3Error(path, "access %s: %s: %v", name, key, err)
			}
			if key == "models" {
				access.Models, access.ModelsSet = globs, len(globs) > 0
			} else {
				access.Exclude = globs
			}
		case "expires":
			text, _ := value.(string)
			day, err := parseDay(text)
			if err != nil {
				return access, v3Error(path, "access %s: expires: %v", name, err)
			}
			access.Expires = day
		default:
			return access, v3Error(path, "unknown key '%s' in [access.%s]", key, name)
		}
	}
	if len(access.Agents) == 0 {
		return access, v3Error(path, "access %s: agents is required", name)
	}
	return access, nil
}

func parseV3Preset(path, name string, section map[string]any) (v3Preset, error) {
	preset := v3Preset{Name: name}
	for key, value := range section {
		switch key {
		case "off", "lean", "minimal", "prefer", "order":
			items, _ := value.([]any)
			list, err := nameList(items)
			if err != nil {
				return preset, v3Error(path, "preset %s: %s: %v", name, key, err)
			}
			switch key {
			case "off":
				preset.Off = list
			case "lean":
				preset.Lean = list
			case "minimal":
				preset.Minimal = list
			case "prefer":
				preset.Prefer = list
			case "order":
				preset.Order = list
			}
		case "models":
			table, ok := value.(map[string]any)
			if !ok {
				return preset, v3Error(path, "preset %s: models must be a table", name)
			}
			preset.Models = map[string][]string{}
			for access, globs := range table {
				list, err := globList(globs)
				if err != nil {
					return preset, v3Error(path, "preset %s: models.%s: %v", name, access, err)
				}
				preset.Models[access] = list
			}
		default:
			return preset, v3Error(path, "unknown key '%s' in [preset.%s]", key, name)
		}
	}
	return preset, nil
}

// parseV2Variants reads a version 2 variants file: [preset.NAME] tables of
// the v3 grammar.
func parseV2Variants(path, text string) ([]v3Preset, error) {
	root, err := agents.ParseTOML(text)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", path, err), Code: clierrors.EXIT_FORMAT}
	}
	if version, ok := root["version"].(int64); !ok || version != 2 {
		return nil, v3Error(path, "version must be 2")
	}
	index := scanLines(text)
	presets, _ := root["preset"].(map[string]any)
	list := make([]v3Preset, 0, len(presets))
	for _, name := range index.keysByLine("preset", presets) {
		section, ok := presets[name].(map[string]any)
		if !ok {
			return nil, v3Error(path, "preset %s must be a table", name)
		}
		preset, err := parseV3Preset(path, name, section)
		if err != nil {
			return nil, err
		}
		list = append(list, preset)
	}
	return list, nil
}

// applyV3Preset applies a v3 preset: the access lists become budgets, the
// models narrow the allowlist and the order puts the named accesses first.
func applyV3Preset(policy *v3Policy, preset *v3Preset) {
	for _, list := range []struct {
		names  []string
		budget string
	}{{preset.Prefer, "prefer"}, {preset.Lean, "lean"}, {preset.Minimal, "minimal"}, {preset.Off, "off"}} {
		for _, name := range list.names {
			if access := policy.access(name); access != nil {
				access.Budget = list.budget
			}
		}
	}
	for name, globs := range preset.Models {
		if access := policy.access(name); access != nil {
			access.Models, access.ModelsSet = append([]string(nil), globs...), true
		}
	}
	if len(preset.Order) > 0 {
		ordered := make([]v3Access, 0, len(policy.Accesses))
		placed := map[string]bool{}
		for _, name := range preset.Order {
			if access := policy.access(name); access != nil && !placed[name] {
				placed[name] = true
				ordered = append(ordered, *access)
			}
		}
		for _, access := range policy.Accesses {
			if !placed[access.Name] {
				ordered = append(ordered, access)
			}
		}
		policy.Accesses = ordered
	}
}

// v3Table resolves a v3 policy into the version 4 table: the access order
// (budget level first, file or preset order inside a level, as the v3
// resolver sorted its candidates) and the cell the v3 resolver would pick
// for every access and role. Auth, expiry and the heavy context check are
// left out: they are the state of the machine, and the v4 resolver still
// applies them. models holds the catalog models of every access; an access
// the catalog does not know takes the models the policy names.
func v3Table(policy *v3Policy, models map[string][]ModelInfo) ([]string, map[string]map[string]Cell) {
	accesses := append([]v3Access(nil), policy.Accesses...)
	sort.SliceStable(accesses, func(i, j int) bool {
		return v3BudgetRank(accesses[i].Budget) < v3BudgetRank(accesses[j].Budget)
	})
	order := make([]string, 0, len(accesses))
	cells := make(map[string]map[string]Cell, len(accesses))
	for i := range accesses {
		access := &accesses[i]
		order = append(order, access.Name)
		known := models[access.Name]
		if len(known) == 0 {
			known = v3PolicyModels(policy, access)
		}
		for _, role := range Roles {
			cell, ok := v3Cell(policy, access, known, role)
			if !ok {
				continue
			}
			if cells[access.Name] == nil {
				cells[access.Name] = map[string]Cell{}
			}
			cells[access.Name][role] = cell
		}
	}
	return order, cells
}

// v3PolicyModels lists the models a v3 policy names for an access without
// a catalog: the routed models and the exact ids of its allowlist.
func v3PolicyModels(policy *v3Policy, access *v3Access) []ModelInfo {
	seen := map[string]bool{}
	var list []ModelInfo
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			list = append(list, ModelInfo{Access: access.Name, ID: id, Reasoning: true})
		}
	}
	for _, route := range policy.Routes {
		if route.Access == access.Name {
			add(route.Model)
		}
	}
	for _, glob := range access.Models {
		if !strings.ContainsAny(glob, "*?[") {
			add(glob)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// v3Cell is the v3 resolver's choice for one access and role.
func v3Cell(policy *v3Policy, access *v3Access, models []ModelInfo, role string) (Cell, bool) {
	requirement := policy.Roles[role]
	if !v3ServesBudget(access.Budget, role) || (access.Check && judgeRole(role)) {
		return Cell{}, false
	}
	if requirement == "trusted" && !access.Trusted {
		return Cell{}, false
	}
	minimum := tierIndex(requirement)
	if requirement == "trusted" {
		minimum = tierIndex("strong")
	}
	var best ModelInfo
	var bestRoute v3Route
	for _, model := range models {
		if !v3ModelAllowed(access, model.ID) {
			continue
		}
		route, ok := v3ServingRoute(policy, access.Name, model.ID, minimum)
		if !ok {
			continue
		}
		if best.ID == "" || v3BetterModel(model, route.Class, best, bestRoute.Class) {
			best, bestRoute = model, route
		}
	}
	if best.ID == "" {
		return Cell{}, false
	}
	return Cell{Model: best.ID, Effort: v3RouteEffort(best, bestRoute)}, true
}

func v3BudgetRank(budget string) int {
	for i, name := range v3Budgets {
		if name == budget {
			return i
		}
	}
	return len(v3Budgets)
}

func v3ServesBudget(budget, role string) bool {
	switch budget {
	case "prefer", "normal":
		return true
	case "lean":
		return role == "cheap" || role == "executor" || role == "review"
	case "minimal":
		return role == "acceptance" || role == "replan"
	}
	return false
}

func v3ModelAllowed(access *v3Access, id string) bool {
	if access.ModelsSet && !matchAny(access.Models, id) {
		return false
	}
	return !matchAny(access.Exclude, id)
}

func matchAny(globs []string, id string) bool {
	for _, glob := range globs {
		if matched, err := path.Match(glob, id); err == nil && matched {
			return true
		}
	}
	return false
}

// v3ServingRoute picks the route of a model with the smallest class that
// still meets the minimum; a model without routes takes its family class.
func v3ServingRoute(policy *v3Policy, access, id string, minimum int) (v3Route, bool) {
	var routes []v3Route
	for _, route := range policy.Routes {
		if route.Access == access && route.Model == id {
			routes = append(routes, route)
		}
	}
	if len(routes) == 0 {
		class := familyClass(id)
		return v3Route{Access: access, Model: id, Class: class}, tierIndex(class) >= minimum
	}
	best := -1
	for i, route := range routes {
		if tierIndex(route.Class) < minimum {
			continue
		}
		if best < 0 || tierIndex(route.Class) < tierIndex(routes[best].Class) {
			best = i
		}
	}
	if best < 0 {
		return v3Route{}, false
	}
	return routes[best], true
}

func v3BetterModel(a ModelInfo, aClass string, b ModelInfo, bClass string) bool {
	if aClass != bClass {
		return tierIndex(aClass) < tierIndex(bClass)
	}
	if (a.Cost == nil) != (b.Cost == nil) {
		return a.Cost != nil
	}
	if a.Cost != nil {
		aPrice, bPrice := a.Cost.Input+a.Cost.Output, b.Cost.Input+b.Cost.Output
		if aPrice != bPrice {
			return aPrice < bPrice
		}
	}
	if a.Context != b.Context {
		return a.Context > b.Context
	}
	return a.ID < b.ID
}

// familyClass is the v3 automatic class of a model without a route:
// flash, mini and lite are cheap; pro, sol and opus are strong.
func familyClass(id string) string {
	words := strings.FieldsFunc(strings.ToLower(id), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	has := func(names ...string) bool {
		for _, name := range names {
			if contains(words, name) {
				return true
			}
		}
		return false
	}
	switch {
	case has("pro", "sol", "opus"):
		return "strong"
	case has("flash", "mini", "lite"):
		return "cheap"
	}
	return "standard"
}

// classEffort is the v3 effort ladder of a class.
var classEffort = map[string]string{"cheap": "low", "standard": "high", "strong": "high"}

func v3RouteEffort(model ModelInfo, route v3Route) string {
	if !model.Reasoning {
		return ""
	}
	target := route.Eff
	if target == "" {
		target = classEffort[route.Class]
	}
	if target == "" || target == "-" {
		return ""
	}
	return snapEffort(target, model.Efforts)
}

// snapEffort brings a wanted level to the levels the model declares: the
// target itself, the highest declared level below it, or the lowest above.
func snapEffort(target string, efforts []string) string {
	if len(efforts) == 0 || contains(efforts, target) {
		return target
	}
	want := ladderIndex(target)
	if want < 0 {
		return target
	}
	below, above := "", ""
	for _, effort := range efforts {
		index := ladderIndex(effort)
		if index < 0 {
			continue
		}
		switch {
		case index < want:
			if below == "" || index > ladderIndex(below) {
				below = effort
			}
		case index > want:
			if above == "" || index < ladderIndex(above) {
				above = effort
			}
		}
	}
	if below != "" {
		return below
	}
	if above != "" {
		return above
	}
	return target
}

func ladderIndex(level string) int {
	for i, name := range effortLadder {
		if name == level {
			return i
		}
	}
	return -1
}
