package roster

import (
	"fmt"
	"sort"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
)

// Candidate is the one route an access offers for a role: the single
// channel that reaches the access, the model its cell names, the price and
// context the catalog knows, and the effort to pass. Model is the text a
// plan route carries — ACCESS/ID for a pi or opencode channel, the bare id
// for codex and claude.
type Candidate struct {
	Role    string `json:"role"`
	Access  string `json:"access"`
	Agent   string `json:"agent"`
	ID      string `json:"id"`
	Model   string `json:"model"`
	Effort  string `json:"effort,omitempty"`
	Context int    `json:"context,omitempty"`
	MaxOut  int    `json:"max_out,omitempty"`
	Cost    *Cost  `json:"cost,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Auth    string `json:"auth,omitempty"`
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
// access/role pairings it rejected with the reason.
type Resolution struct {
	Policy  *Policy                `json:"-"`
	Roles   map[string][]Candidate `json:"roles"`
	Skipped []Skip                 `json:"skipped,omitempty"`
}

// Candidates returns the candidates of a role in chain order; an unknown
// role has none.
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

// resolveAt is Resolve with a pinned day. The chain of a role is its
// column of the table read top to bottom: the accesses in priority order
// (the policy order, or the order of the preset in force), each giving
// the cell it holds for the role. An access that is off, expired, unknown
// to the catalog or not signed in gives nothing; a cell whose model the
// catalog does not know is skipped with the reason, and so is a heavy
// cell whose context is unknown.
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
		list := make([]Candidate, 0, len(usable))
		for _, access := range usable {
			cell, ok := access.Roles[role]
			if !ok || cell.Empty() {
				resolution.Skipped = append(resolution.Skipped, Skip{Access: access.Name, Role: role, Reason: "no cell for " + role})
				continue
			}
			if access.Check && judgeRole(role) {
				resolution.Skipped = append(resolution.Skipped, Skip{Access: access.Name, Role: role, Reason: "check: an access that needs an independent review never judges"})
				continue
			}
			model, found := cellModel(models[access.Name], cell.Model)
			if !found {
				resolution.Skipped = append(resolution.Skipped, Skip{Access: access.Name, Role: role, Reason: fmt.Sprintf("model %s is not in the catalog; run `docflow roster scan`", cell.Model)})
				continue
			}
			if role == "heavy" && model.Context <= 0 {
				resolution.Skipped = append(resolution.Skipped, Skip{Access: access.Name, Role: role, Reason: fmt.Sprintf("model %s has an unknown context, and a heavy unit must not be guessed in", cell.Model)})
				continue
			}
			info := accesses[access.Name]
			agent := access.Agents[0]
			list = append(list, Candidate{
				Role:    role,
				Access:  access.Name,
				Agent:   agent,
				ID:      model.ID,
				Model:   routeModel(agent, access.Name, model.ID),
				Effort:  cell.Effort,
				Context: model.Context,
				MaxOut:  model.MaxOut,
				Cost:    model.Cost,
				Kind:    accessKind(access, info),
				Auth:    info.Auth,
				Check:   access.Check,
			})
		}
		resolution.Roles[role] = list
	}
	return resolution, nil
}

// indexCatalog indexes the catalog by access name and by the models of
// each access, sorted by id.
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

// cellModel looks a model up among the catalog models of an access.
func cellModel(models []ModelInfo, id string) (ModelInfo, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return ModelInfo{}, false
}

// accessProblem says why an access serves no role at all, or returns an
// empty string. The switch, the expiry, the missing catalog entry and a
// refused auth are the same for every role, so the reason is recorded
// once instead of once per role.
func accessProblem(access *Access, accesses map[string]AccessInfo, models map[string][]ModelInfo, today time.Time) string {
	switch {
	case access.Off:
		return "off"
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
