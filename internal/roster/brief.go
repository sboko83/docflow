package roster

import (
	"fmt"
	"strings"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
)

// The scopes of a role in the brief: a read role sizes a unit by its read
// budget, a judge role reads a diff and test output instead.
const (
	ScopeRead  = "read"
	ScopeJudge = "diff/test"
)

// RoleBrief is one role of the brief: its chain in use order, the read
// budget of the primary route and the explanation of both. Routes[0] is
// the primary route and the rest are the fallbacks; Budget is nil for a
// judge role, which gets no read budget.
type RoleBrief struct {
	Role   string      `json:"role"`
	Scope  string      `json:"scope"`
	Routes []Candidate `json:"routes,omitempty"`
	Budget *UnitBudget `json:"budget,omitempty"`
	Why    []string    `json:"why"`
}

// Brief is the planning view of the roster: the conditions in force, the
// chain and unit budget of every role the plan uses, the review matrix
// and the warnings of incomplete chains. It deliberately holds no
// catalog: a plan is written from chains and budgets, and listing every
// scanned model would turn the brief into a catalog dump.
type Brief struct {
	Conditions []Condition `json:"conditions,omitempty"`
	Roles      []RoleBrief `json:"roles"`
	Reviews    []Review    `json:"reviews,omitempty"`
	Warnings   []string    `json:"warnings,omitempty"`
}

// BuildBrief resolves the roster and builds the brief of the given roles;
// an empty list means every role. The clock is the resolver's own: an
// expiry is read when the plan is written, not while it runs.
func BuildBrief(policy *Policy, state *State, catalog *Catalog, roles []string) (*Brief, error) {
	return buildBriefAt(policy, state, catalog, roles, time.Now().UTC())
}

// buildBriefAt is BuildBrief with a pinned day, so a test decides which
// side of an expiry it reads.
func buildBriefAt(policy *Policy, state *State, catalog *Catalog, roles []string, today time.Time) (*Brief, error) {
	selected, err := briefRoles(roles)
	if err != nil {
		return nil, err
	}
	resolution, err := resolveAt(policy, state, catalog, today)
	if err != nil {
		return nil, err
	}
	chains, err := BuildChains(resolution)
	if err != nil {
		return nil, err
	}
	brief := &Brief{
		Conditions: Conditions(policy, state),
		Roles:      make([]RoleBrief, 0, len(selected)),
		Reviews:    chains.Reviews,
	}
	for _, role := range selected {
		routes := chains.Routes(role)
		entry := RoleBrief{Role: role, Scope: briefScope(role), Routes: routes}
		if len(routes) > 0 {
			entry.Budget = unitBudget(role, routes[0])
		}
		entry.Why = roleWhy(role, routes, entry.Budget)
		brief.Roles = append(brief.Roles, entry)
	}
	brief.Warnings = filterWarnings(chains.Warnings, selected)
	return brief, nil
}

// Header renders the plan-header keys the brief would write: executor and
// its fallback from the executor chain, then one key per role chain. A
// chain with no route is left out of the map instead of written as a
// dash, which is how apply removes a key. The routes take the plan form
// the resolver already put in Candidate.Model: ACCESS/ID for pi and
// opencode, the bare id for codex and claude.
func (b *Brief) Header() map[string]string {
	header := make(map[string]string, len(b.Roles))
	for _, entry := range b.Roles {
		if entry.Role == Roles[0] {
			if len(entry.Routes) == 0 {
				continue
			}
			header["executor"] = agents.RouteText(entry.Routes[0].Route())
			if len(entry.Routes) > 1 {
				header["fallback"] = headerChain(entry.Routes[1:])
			}
			continue
		}
		if len(entry.Routes) > 0 {
			header[entry.Role] = headerChain(entry.Routes)
		}
	}
	return header
}

// headerChain renders a chain as the semicolon-separated plan value.
func headerChain(routes []Candidate) string {
	texts := make([]string, 0, len(routes))
	for _, route := range routes {
		texts = append(texts, agents.RouteText(route.Route()))
	}
	return strings.Join(texts, "; ")
}

// briefRoles narrows the selected roles to the known ones in policy role
// order, so the brief reads the same whatever order the caller listed
// them in. An unknown role is an error: a plan key nobody can resolve is
// a typo, not a role to skip.
func briefRoles(roles []string) ([]string, error) {
	if len(roles) == 0 {
		return append([]string(nil), Roles...), nil
	}
	wanted := make(map[string]bool, len(roles))
	for _, role := range roles {
		known := false
		for _, name := range Roles {
			if name == role {
				known = true
				break
			}
		}
		if !known {
			return nil, clierrors.DocflowError{
				Message: fmt.Sprintf("docflow: roster: unknown role '%s' (known: %s)", role, strings.Join(Roles, ", ")),
				Code:    clierrors.EXIT_FORMAT,
			}
		}
		wanted[role] = true
	}
	selected := make([]string, 0, len(wanted))
	for _, role := range Roles {
		if wanted[role] {
			selected = append(selected, role)
		}
	}
	return selected, nil
}

// briefScope names what a unit of the role reads: the repository or the
// diff and test output. Review, acceptance and replan judge work, so a
// read budget would be a number nobody applies to them.
func briefScope(role string) string {
	if roleReads(role) {
		return ScopeRead
	}
	return ScopeJudge
}

// roleWhy explains a role of the brief: the primary route and its
// fallbacks, then the read budget arithmetic or the judge scope. An empty
// chain says so instead of leaving a blank where a route belongs.
func roleWhy(role string, routes []Candidate, budget *UnitBudget) []string {
	if len(routes) == 0 {
		return []string{"no route: no access serves this role under the current conditions"}
	}
	why := make([]string, 0, len(routes)+1)
	why = append(why, routeWhy("primary", routes[0]))
	for _, route := range routes[1:] {
		why = append(why, routeWhy("fallback", route))
	}
	if budget == nil {
		why = append(why, "diff/test scope: a judge reads the diff and the test output, not the repository, so it has no read budget")
		return why
	}
	why = append(why, budget.Why)
	return why
}

// routeWhy explains one route of a chain: the access it spends, the
// channel that reaches it, its budget level and the model class. The
// effort is part of the model reference — in v3 the route is the pair of
// a model and an effort.
func routeWhy(kind string, route Candidate) string {
	return fmt.Sprintf("%s %s via %s: %s budget, %s class",
		kind, modelAtEffort(route), route.Agent, route.Budget, route.Class)
}

// modelAtEffort renders "ACCESS/MODEL@EFFORT", the reference of the route
// the resolver picked: in v3 the effort belongs to the route, so the
// brief names the pair. A route without an effort renders without the
// suffix.
func modelAtEffort(route Candidate) string {
	if route.Effort == "" {
		return route.Access + "/" + route.ID
	}
	return route.Access + "/" + route.ID + "@" + route.Effort
}

// filterWarnings keeps the chain warnings of the selected roles: a plan
// that has no heavy unit must not be told it has no heavy route. The
// chain builder stays the single owner of the warning texts.
func filterWarnings(warnings []string, roles []string) []string {
	wanted := make(map[string]bool, len(roles))
	for _, role := range roles {
		wanted[role] = true
	}
	kept := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		role, ok := warningRole(warning)
		if ok && !wanted[role] {
			continue
		}
		kept = append(kept, warning)
	}
	return kept
}

// warningRole names the role a chain warning belongs to: every warning
// ends with " for <role>".
func warningRole(warning string) (string, bool) {
	for _, role := range Roles {
		if strings.HasSuffix(warning, " for "+role) {
			return role, true
		}
	}
	return "", false
}

// Text renders the brief for a terminal: the conditions, one row per
// role with its chain, budget and scope, the review matrix, the why
// lines and the warnings. No section is left empty: an absent one is a
// named "none" line instead.
func (b *Brief) Text() string {
	if b == nil {
		return ""
	}
	var out strings.Builder
	if len(b.Conditions) == 0 {
		out.WriteString("conditions: none\n")
	} else {
		out.WriteString("conditions:\n")
		out.WriteString(ConditionTable(b.Conditions))
	}
	rows := [][]string{{"role", "chain", "budget", "scope"}}
	for _, role := range b.Roles {
		rows = append(rows, []string{role.Role, briefChain(role.Routes), briefBudget(role), role.Scope})
	}
	out.WriteString("chains:\n")
	out.WriteString(tableText(rows))
	if len(b.Reviews) == 0 {
		out.WriteString("review: none\n")
	} else {
		rows = [][]string{{"access", "reviewers"}}
		for _, review := range b.Reviews {
			rows = append(rows, []string{review.Access, briefChain(review.Reviewers)})
		}
		out.WriteString("review:\n")
		out.WriteString(tableText(rows))
	}
	out.WriteString("why:\n")
	for _, role := range b.Roles {
		for _, line := range role.Why {
			fmt.Fprintf(&out, "- %s: %s\n", role.Role, line)
		}
	}
	if len(b.Warnings) == 0 {
		out.WriteString("warnings: none\n")
	} else {
		out.WriteString("warnings:\n")
		for _, warning := range b.Warnings {
			fmt.Fprintf(&out, "- %s\n", warning)
		}
	}
	return out.String()
}

// briefChain renders the chain one route per " > ", the primary first;
// an empty chain prints a dash so the row keeps its place. Every route
// reads ACCESS/MODEL@EFFORT: the chain names the route, not just the
// model.
func briefChain(routes []Candidate) string {
	if len(routes) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(routes))
	for _, route := range routes {
		parts = append(parts, modelAtEffort(route))
	}
	return strings.Join(parts, " > ")
}

// briefBudget prints the read budget of a role: an unknown budget and a
// judge role both print a dash, the scope column says which one it is.
func briefBudget(role RoleBrief) string {
	if role.Budget == nil || role.Budget.Target <= 0 {
		return "-"
	}
	return tokenText(role.Budget.Target)
}
