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

// Candidate is the one route an access offers for a role: the single
// channel that reaches the access, the model of the cheapest class that
// meets the role, its price and context, and the effort to pass. Model is
// the text a plan route carries — ACCESS/ID for a pi or opencode channel,
// the bare id for codex and claude, the same split the v1 roster used and
// the migration reads back.
type Candidate struct {
	Role    string `json:"role"`
	Access  string `json:"access"`
	Agent   string `json:"agent"`
	ID      string `json:"id"`
	Model   string `json:"model"`
	Class   string `json:"class"`
	Effort  string `json:"effort,omitempty"`
	Context int    `json:"context,omitempty"`
	MaxOut  int    `json:"max_out,omitempty"`
	Cost    *Cost  `json:"cost,omitempty"`
	Budget  string `json:"budget"`
	Kind    string `json:"kind,omitempty"`
	Auth    string `json:"auth,omitempty"`
	Trusted bool   `json:"trusted,omitempty"`
	Check   bool   `json:"check,omitempty"`
}

// Route renders the candidate as the three fields a plan route holds.
func (c Candidate) Route() agents.Route {
	return agents.Route{Agent: c.Agent, Model: c.Model, Effort: c.Effort}
}

// Skip is one pairing the resolver rejected: the access, the role it
// cannot serve (empty when the access serves no role at all) and why.
type Skip struct {
	Access string `json:"access"`
	Role   string `json:"role,omitempty"`
	Reason string `json:"reason"`
}

// Resolution is the resolver's answer: the merged policy the candidates
// were taken from, the candidates of every role in chain order, and the
// access/role pairings it rejected with the reason. Building chains,
// checking reviewers and computing budgets are separate steps: one access
// contributes at most one candidate per role.
type Resolution struct {
	Policy  *Policy                `json:"-"`
	Roles   map[string][]Candidate `json:"roles"`
	Skipped []Skip                 `json:"skipped,omitempty"`
}

// Candidates returns the candidates of a role in chain order: prefer
// before normal before lean, policy access order inside one budget level.
// An unknown role has none.
func (r *Resolution) Candidates(role string) []Candidate {
	if r == nil {
		return nil
	}
	return r.Roles[role]
}

// Resolve collects the candidates of every role from the policy merged
// with the temporary conditions and the scanned catalog. The clock is the
// resolver's own, so an expiry is decided when the plan is written, not
// while it runs: the runner never reads the roster.
func Resolve(policy *Policy, state *State, catalog *Catalog) (*Resolution, error) {
	return resolveAt(policy, state, catalog, time.Now().UTC())
}

// resolveAt is Resolve with a pinned day, so a test decides which side of
// an expiry it reads. The state is merged the same way Merge does, only
// at that day: an off condition and an expired access drop out through
// the merged budget.
func resolveAt(policy *Policy, state *State, catalog *Catalog, today time.Time) (*Resolution, error) {
	if policy == nil {
		return nil, clierrors.DocflowError{Message: "docflow: roster: no policy to resolve", Code: clierrors.EXIT_FORMAT}
	}
	merged := policy.clone()
	if state != nil {
		if err := state.Validate(policy); err != nil {
			return nil, err
		}
		merged = mergeAt(merged, state, today)
	}
	if err := checkMinimalBudgets(merged); err != nil {
		return nil, err
	}
	accesses, models := indexCatalog(catalog)
	resolution := &Resolution{Policy: merged, Roles: make(map[string][]Candidate, len(Roles))}
	usable := make([]*Access, 0, len(merged.Accesses))
	for i := range merged.Accesses {
		access := &merged.Accesses[i]
		if reason := accessProblem(access, accesses, models, today); reason != "" {
			resolution.Skipped = append(resolution.Skipped, Skip{Access: access.Name, Reason: reason})
			continue
		}
		usable = append(usable, access)
	}
	for _, role := range Roles {
		requirement, ok := merged.Roles[role]
		if !ok {
			return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster: role %s: no requirement in [role]", role), Code: clierrors.EXIT_FORMAT}
		}
		minimum := tierIndex(requirement)
		if requirement == "trusted" {
			// The trusted requirement names an independent judge, and an
			// independent judge is a strong route: the same rule the v1
			// roster applied to its trusted roles.
			minimum = tierIndex("strong")
		}
		list := make([]Candidate, 0, len(usable))
		for _, access := range usable {
			if reason := roleProblem(access, role, requirement); reason != "" {
				resolution.Skipped = append(resolution.Skipped, Skip{Access: access.Name, Role: role, Reason: reason})
				continue
			}
			info := accesses[access.Name]
			model, route, problem := pickModel(merged, access, models[access.Name], minimum, role)
			if problem != "" {
				resolution.Skipped = append(resolution.Skipped, Skip{Access: access.Name, Role: role, Reason: problem})
				continue
			}
			agent := access.Agents[0]
			list = append(list, Candidate{
				Role:    role,
				Access:  access.Name,
				Agent:   agent,
				ID:      model.ID,
				Model:   routeModel(agent, access.Name, model.ID),
				Class:   route.Class,
				Effort:  routeEffort(model, route),
				Context: model.Context,
				MaxOut:  model.MaxOut,
				Cost:    model.Cost,
				Budget:  access.Budget,
				Kind:    accessKind(access, info),
				Auth:    info.Auth,
				Trusted: access.Trusted,
				Check:   access.Check,
			})
		}
		sortByBudget(list)
		resolution.Roles[role] = list
	}
	return resolution, nil
}

// checkMinimalBudgets refuses a minimal budget on an untrusted access. The
// policy parser and the state validation already do; the resolver repeats
// it because a merged policy can also come from a caller that built it.
func checkMinimalBudgets(policy *Policy) error {
	for i := range policy.Accesses {
		access := &policy.Accesses[i]
		if access.Budget == BudgetMinimal && !access.Trusted {
			return clierrors.DocflowError{
				Message: fmt.Sprintf("docflow: roster: access %s: budget minimal requires trusted = true, minimal serves only acceptance and replan", access.Name),
				Code:    clierrors.EXIT_FORMAT,
			}
		}
	}
	return nil
}

// indexCatalog indexes the catalog by access name and by the models of
// each access, sorted by id so the order a caller's catalog came in never
// decides which model the resolver picks.
func indexCatalog(catalog *Catalog) (map[string]AccessInfo, map[string][]ModelInfo) {
	accesses := make(map[string]AccessInfo)
	models := make(map[string][]ModelInfo)
	if catalog == nil {
		return accesses, models
	}
	for _, info := range catalog.Accesses {
		if _, ok := accesses[info.Access]; !ok {
			accesses[info.Access] = info
		}
	}
	for _, model := range catalog.Models {
		models[model.Access] = append(models[model.Access], model)
	}
	for _, list := range models {
		sort.SliceStable(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	}
	return accesses, models
}

// accessProblem says why an access serves no role at all, or returns an
// empty string. The switch, the expiry, the missing catalog entry and a
// refused auth are the same for every role, so the reason is recorded
// once instead of once per role.
func accessProblem(access *Access, accesses map[string]AccessInfo, models map[string][]ModelInfo, today time.Time) string {
	switch {
	case access.Budget == BudgetOff:
		return "budget off"
	case !access.Expires.IsZero() && today.After(access.Expires):
		return "expired on " + dayText(access.Expires)
	}
	info, ok := accesses[access.Name]
	if !ok || len(models[access.Name]) == 0 {
		return "no models in the catalog; run `docflow roster scan`"
	}
	if info.Auth == AuthNotReady {
		return "auth not ready"
	}
	return ""
}

// roleProblem says why an access cannot serve a role, or returns an empty
// string: the budget level gates the roles, a check access never judges,
// and an untrusted access never accepts or replans.
func roleProblem(access *Access, role, requirement string) string {
	if !servesBudget(access.Budget, role) {
		return fmt.Sprintf("budget %s serves no %s role", access.Budget, role)
	}
	if access.Check && judgeRole(role) {
		return "check: an access that needs an independent review never judges"
	}
	if requirement == "trusted" && !access.Trusted {
		return "not trusted: the role needs an independent judge"
	}
	return ""
}

// judgeRole reports whether a role reviews, accepts or replans: the three
// roles a check access is excluded from.
func judgeRole(role string) bool {
	switch role {
	case "review", "acceptance", "replan":
		return true
	}
	return false
}

// servesBudget reports whether an access at the given budget level serves
// the role: prefer and normal serve every role, lean skips heavy,
// acceptance and replan, minimal serves only acceptance and replan.
func servesBudget(budget, role string) bool {
	switch budget {
	case BudgetPrefer, BudgetNormal:
		return true
	case BudgetLean:
		switch role {
		case "cheap", "executor", "review":
			return true
		}
	case BudgetMinimal:
		switch role {
		case "acceptance", "replan":
			return true
		}
	}
	return false
}

// pickModel returns the best model an access can offer for a role, the
// route it is served through, or the reason no model qualifies. The model
// must pass the access allowlist and the denials, its route must meet the
// role minimum, and a heavy unit needs a known context: an unknown
// context must not be guessed into the strongest role.
func pickModel(policy *Policy, access *Access, models []ModelInfo, minimum int, role string) (ModelInfo, Route, string) {
	var best ModelInfo
	bestRoute := Route{}
	allowed, tooWeak, noContext := 0, 0, 0
	for _, model := range models {
		if !modelAllowed(access, model.ID) {
			continue
		}
		allowed++
		route, ok := servingRoute(policy, access.Name, model.ID, minimum)
		if !ok {
			tooWeak++
			continue
		}
		if role == "heavy" && model.Context <= 0 {
			noContext++
			continue
		}
		if best.ID == "" || betterModel(model, route.Class, best, bestRoute.Class) {
			best, bestRoute = model, route
		}
	}
	switch {
	case best.ID != "":
		return best, bestRoute, ""
	case allowed == 0:
		return ModelInfo{}, Route{}, "no model the access allows"
	case noContext > 0:
		// A model met the class bar; the role still cannot use it, because
		// its context is unknown and a heavy unit must not be guessed in.
		return ModelInfo{}, Route{}, "no model with a known context"
	default:
		return ModelInfo{}, Route{}, fmt.Sprintf("no model at class %s or above", tiers[minimum])
	}
}

// servingRoute reports how an access serves a model for a role: of the
// model's [route] entries the one with the smallest class that still
// meets the role minimum — a cheap route answers a cheap role before a
// strong one, and a tie keeps the file order, so the choice is stable.
// A model without routes is served at its automatic class with the class
// ladder effort (an empty Eff on the returned pseudo-route); a model
// whose routes all fall below the minimum is not suitable.
func servingRoute(policy *Policy, access, id string, minimum int) (Route, bool) {
	routes := policy.routesOf(access, id)
	if len(routes) == 0 {
		class := familyClass(id)
		return Route{Access: access, Model: id, Class: class}, tierIndex(class) >= minimum
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
		return Route{}, false
	}
	return routes[best], true
}

// betterModel reports whether a beats b for the same role: the smaller
// class first, then the lower published price, then the larger context
// and finally the id. A price of a subscription is the published API
// price, a tiebreak inside one access — never a comparison against a
// metered access, whose budget level already decides the order.
func betterModel(a ModelInfo, aClass string, b ModelInfo, bClass string) bool {
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

// modelAllowed reports whether an access may serve a model: an allowlist
// on the access narrows it, an exclusion removes a model from it, and the
// state already merged its own allowlist and denials into both. A
// condition that wrote an empty allowlist allows no model, while an access
// without the key serves every model.
func modelAllowed(access *Access, id string) bool {
	if access.ModelsSet && !matchAny(access.Models, id) {
		return false
	}
	return !matchAny(access.Exclude, id)
}

// matchAny reports whether id matches one of the globs; the patterns were
// checked when the policy or the state was parsed.
func matchAny(globs []string, id string) bool {
	for _, glob := range globs {
		if matched, err := path.Match(glob, id); err == nil && matched {
			return true
		}
	}
	return false
}

// familyClass is the automatic class of a model whose policy fixes no
// route: flash, mini and lite are cheap; pro, sol and opus are strong;
// everything else is standard. The id is split into words, so "gemini" is
// not a "mini", and a strong marker wins over a cheap one.
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

// classEffort is the effort ladder of a class: the promotion a level
// makes over the class itself. It serves a model that has no route in the
// policy; a routed model takes the effort of its route.
var classEffort = map[string]string{
	"cheap":    "low",
	"standard": "high",
	"strong":   "high",
}

// routeEffort resolves the effort of a candidate: the route key when the
// model serves through a fixed route ("-" means no effort at all),
// otherwise the class ladder of the automatic class, snapped to the
// levels the model declares. A model without reasoning takes no effort,
// and an unknown effort is passed as the key says: the catalog cannot
// tell whether the model supports it.
func routeEffort(model ModelInfo, route Route) string {
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
// target itself when supported, otherwise the highest declared level
// below it, otherwise the lowest declared level above it. A level outside
// the ladder (an OpenCode variant id like "thinking") says nothing about
// its position, so it never wins a comparison.
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

// ladderIndex is the position of a level on the effort ladder, or -1.
func ladderIndex(level string) int {
	for i, name := range effortLadder {
		if name == level {
			return i
		}
	}
	return -1
}

// routeModel renders the model a plan route carries: a pi or opencode
// channel addresses a provider model as ACCESS/ID, while codex and claude
// take the bare id.
func routeModel(agent, access, id string) string {
	if agent == "pi" || agent == "opencode" {
		return access + "/" + id
	}
	return id
}

// accessKind is the kind an access reports: the policy value when it has
// one, the catalog value otherwise.
func accessKind(access *Access, info AccessInfo) string {
	if access.Kind != "" {
		return access.Kind
	}
	return info.Kind
}

// budgetRank orders candidates: prefer first, then normal, then lean;
// minimal closes, because only acceptance and replan see it.
func budgetRank(budget string) int {
	for i, name := range Budgets {
		if name == budget {
			return i
		}
	}
	return len(Budgets)
}

// sortByBudget orders a role's candidates by budget level and keeps the
// policy access order inside one level: the chain builder takes the list
// as it comes.
func sortByBudget(list []Candidate) {
	sort.SliceStable(list, func(i, j int) bool { return budgetRank(list[i].Budget) < budgetRank(list[j].Budget) })
}
