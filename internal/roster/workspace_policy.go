package roster

import (
	"fmt"
	"os"
	"strings"

	"bsv-docflow/internal/clierrors"
)

// The policy operations of the workspace: the editor's writes to the
// stable roster.toml and to the machine-owned variants files. Every
// operation applies its change to a copy of the model, validates the
// result through the writer before the target is touched, and swaps the
// copy in only after the file is saved, so a refused operation leaves the
// files and the model as they were. The rows LoadWorkspace derived are
// not rebuilt — the editor reloads the workspace after a successful
// operation.

// mutatePolicy applies a change to a copy of the policy and saves it
// through SavePolicy: the rendered roster must parse before anything is
// written.
func (w *Workspace) mutatePolicy(change func(*Policy) error) error {
	if err := w.ready(); err != nil {
		return err
	}
	next := w.Policy.clone()
	if err := change(next); err != nil {
		return err
	}
	if err := SavePolicy(w.PolicyPath, next); err != nil {
		return err
	}
	w.Policy = next
	return nil
}

// AddAccess adds an access to the policy: the name the CLI flags address,
// the channels that reach it and the optional kind. A new access carries
// no models and the default budget. An unknown kind and a name the CLI
// flags cannot address are refused.
func (w *Workspace) AddAccess(name, kind string, agents []string) error {
	if len(agents) == 0 {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access %s: agents is required", w.PolicyPath, name), Code: clierrors.EXIT_FORMAT}
	}
	for _, agent := range agents {
		if !contains(PolicyAgents, agent) {
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access %s: unknown agent '%s' (known: %s)", w.PolicyPath, name, agent, strings.Join(PolicyAgents, ", ")), Code: clierrors.EXIT_FORMAT}
		}
	}
	if kind != "" && !contains(Kinds, kind) {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access %s: kind must be %s", w.PolicyPath, name, orList(Kinds)), Code: clierrors.EXIT_FORMAT}
	}
	if _, taken := w.Policy.Access(name); taken {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access '%s' already exists", w.PolicyPath, name), Code: clierrors.EXIT_FORMAT}
	}
	return w.mutatePolicy(func(policy *Policy) error {
		if err := checkAccessName(w.PolicyPath, nil, "access", name); err != nil {
			return err
		}
		access := Access{Name: name, Agents: append([]string(nil), agents...), Budget: DefaultBudget}
		if kind != "" {
			access.Kind = kind
		}
		policy.index[name] = len(policy.Accesses)
		policy.Accesses = append(policy.Accesses, access)
		return nil
	})
}

// RemoveAccess deletes an access and everything that names it: the routes
// of the access, its entries in the policy presets, its narrowing in the
// variants files and its conditions in the state, so no layer keeps a
// dead name behind.
func (w *Workspace) RemoveAccess(name string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Access(name); !ok {
		return w.unknownAccess("access", name)
	}
	// Validate the policy change before any file is touched: a refusal
	// must not leave the variants or the state half-cleaned.
	if err := w.validatePolicy(func(policy *Policy) error { return dropAccess(policy, name) }); err != nil {
		return err
	}
	if err := w.cleanVariants(func(preset *Preset) {
		dropAccessFromPreset(preset, name)
	}); err != nil {
		return err
	}
	// A state without an entry for the access stays untouched: no state
	// file is created for an access that had no conditions.
	if w.State != nil && stateNamesAccess(w.State, name) {
		if err := w.mutate(func(state *State) error {
			state.Off = removeItem(state.Off, name)
			delete(state.Until, name)
			delete(state.Budget, name)
			delete(state.Expires, name)
			delete(state.Models, name)
			delete(state.Exclude, name)
			return nil
		}); err != nil {
			return err
		}
	}
	return w.mutatePolicy(func(policy *Policy) error { return dropAccess(policy, name) })
}

// stateNamesAccess reports whether the state carries any condition for
// the access.
func stateNamesAccess(state *State, name string) bool {
	if contains(state.Off, name) {
		return true
	}
	if _, ok := state.Until[name]; ok {
		return true
	}
	if _, ok := state.Budget[name]; ok {
		return true
	}
	if _, ok := state.Expires[name]; ok {
		return true
	}
	if globs, ok := state.Models[name]; ok && len(globs) > 0 {
		return true
	}
	if globs, ok := state.Exclude[name]; ok && len(globs) > 0 {
		return true
	}
	return false
}

// dropAccess removes an access from the policy model: the access, its
// routes and its entries in every preset of the policy.
func dropAccess(policy *Policy, name string) error {
	kept := make([]Access, 0, len(policy.Accesses))
	for _, access := range policy.Accesses {
		if access.Name != name {
			kept = append(kept, access)
		}
	}
	policy.Accesses = kept
	policy.index = make(map[string]int, len(kept))
	for i, access := range kept {
		policy.index[access.Name] = i
	}
	routes := make([]Route, 0, len(policy.Routes))
	for _, route := range policy.Routes {
		if route.Access != name {
			routes = append(routes, route)
		}
	}
	policy.Routes = routes
	for i := range policy.Presets {
		dropAccessFromPreset(&policy.Presets[i], name)
	}
	return nil
}

// dropAccessFromPreset removes an access from a preset: its condition
// entries and its model narrowing.
func dropAccessFromPreset(preset *Preset, name string) {
	for _, list := range []*[]string{&preset.Off, &preset.Lean, &preset.Minimal, &preset.Prefer, &preset.Order} {
		*list = removeItem(*list, name)
	}
	delete(preset.Models, name)
}

// SetAccessFlags sets the trusted and check flags of an access. The flags
// are mutually exclusive: an access that needs an independent review
// cannot be the final judge of its own units.
func (w *Workspace) SetAccessFlags(name string, trusted, check bool) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Access(name); !ok {
		return w.unknownAccess("flags", name)
	}
	if trusted && check {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access %s: check and trusted are mutually exclusive", w.PolicyPath, name), Code: clierrors.EXIT_FORMAT}
	}
	return w.mutatePolicy(func(policy *Policy) error {
		access, _ := policy.Access(name)
		access.Trusted, access.Check = trusted, check
		return nil
	})
}

// SetAccessBudget sets the base budget of an access in the policy. An
// empty level means the default; an unknown level is refused, and the
// parser refuses a minimal budget on an access that is not trusted.
func (w *Workspace) SetAccessBudget(name, level string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Access(name); !ok {
		return w.unknownAccess("budget", name)
	}
	if level != "" && !contains(Budgets, level) {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access %s: budget must be %s", w.PolicyPath, name, orList(Budgets)), Code: clierrors.EXIT_FORMAT}
	}
	return w.mutatePolicy(func(policy *Policy) error {
		access, _ := policy.Access(name)
		if level == "" {
			access.Budget = DefaultBudget
			return nil
		}
		access.Budget = level
		return nil
	})
}

// AddAccessModel adds a catalog model to an access: the id joins the
// allowlist and the model gets its first route — the first declared
// effort of the catalog (or "-" for a model without reasoning) at the
// automatic class by the name. A model must be in the catalog of the
// access: the pickers offer catalog models, and an id outside it would
// carry no context, price or levels.
func (w *Workspace) AddAccessModel(access, id string) error {
	if err := w.ready(); err != nil {
		return err
	}
	entry, ok := w.Policy.Access(access)
	if !ok {
		return w.unknownAccess("model", access)
	}
	model, ok := w.catalogModel(access, id)
	if !ok {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: model %s is not in the catalog of access %s: run `docflow roster scan` first", w.PolicyPath, id, access), Code: clierrors.EXIT_FORMAT}
	}
	// An allowlist the id already matches — a glob covers it too — makes
	// the addition a no-op, so it is refused instead of written twice.
	if entry.ModelsSet && matchAny(entry.Models, id) {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access %s already serves model %s", w.PolicyPath, access, id), Code: clierrors.EXIT_FORMAT}
	}
	return w.mutatePolicy(func(policy *Policy) error {
		entry, _ := policy.Access(access)
		entry.Models = append(entry.Models, id)
		entry.ModelsSet = true
		// A model the allowlist never named can still carry routes; the
		// first route is created only when none exists, or the rendered
		// route table would hold the same effort twice.
		if len(policy.routesOf(access, id)) > 0 {
			return nil
		}
		route := Route{Access: access, Model: id, Class: familyClass(id)}
		if len(model.Efforts) > 0 {
			route.Eff = model.Efforts[0]
		} else {
			route.Eff = "-"
		}
		policy.Routes = appendRoute(policy.Routes, route)
		return nil
	})
}

// RemoveAccessModel removes a model from an access: the id leaves the
// allowlist and its routes are dropped. The narrowing lists of the
// variants lose the exact id, so no layer keeps a dead glob.
func (w *Workspace) RemoveAccessModel(access, id string) error {
	if err := w.ready(); err != nil {
		return err
	}
	entry, ok := w.Policy.Access(access)
	if !ok {
		return w.unknownAccess("model", access)
	}
	if !contains(entry.Models, id) {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access %s does not serve model %s", w.PolicyPath, access, id), Code: clierrors.EXIT_FORMAT}
	}
	if err := w.cleanVariants(func(preset *Preset) {
		dropModelFromPreset(preset, access, id)
	}); err != nil {
		return err
	}
	return w.mutatePolicy(func(policy *Policy) error {
		entry, _ := policy.Access(access)
		entry.Models = removeItem(entry.Models, id)
		entry.ModelsSet = len(entry.Models) > 0
		kept := make([]Route, 0, len(policy.Routes))
		for _, route := range policy.Routes {
			if route.Access != access || route.Model != id {
				kept = append(kept, route)
			}
		}
		policy.Routes = kept
		for i := range policy.Presets {
			dropModelFromPreset(&policy.Presets[i], access, id)
		}
		return nil
	})
}

// dropModelFromPreset removes one exact model glob from a preset's
// narrowing of an access; an empty narrowing is dropped, because an empty
// list would allow no model at all.
func dropModelFromPreset(preset *Preset, access, id string) {
	globs := removeItem(preset.Models[access], id)
	if len(globs) == 0 {
		delete(preset.Models, access)
		return
	}
	preset.Models[access] = globs
}

// AddRouteLevel gives a model a route at its first effort that has no
// route yet, at the class of the model's first route. A model whose every
// declared effort is routed already has nothing to add.
func (w *Workspace) AddRouteLevel(access, model string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Access(access); !ok {
		return w.unknownAccess("route", access)
	}
	info, ok := w.catalogModel(access, model)
	if !ok {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: model %s is not in the catalog of access %s", w.PolicyPath, model, access), Code: clierrors.EXIT_FORMAT}
	}
	routes := w.Policy.routesOf(access, model)
	if len(routes) == 0 {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access %s does not serve model %s", w.PolicyPath, access, model), Code: clierrors.EXIT_FORMAT}
	}
	taken := make(map[string]bool, len(routes))
	class := ""
	for _, route := range routes {
		taken[route.Eff] = true
		if class == "" {
			class = route.Class
		}
	}
	effort := ""
	for _, candidate := range info.Efforts {
		if !taken[candidate] {
			effort = candidate
			break
		}
	}
	if effort == "" {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: model %s already has a route at every declared effort", w.PolicyPath, model), Code: clierrors.EXIT_FORMAT}
	}
	return w.mutatePolicy(func(policy *Policy) error {
		policy.Routes = appendRoute(policy.Routes, Route{Access: access, Model: model, Eff: effort, Class: class})
		return nil
	})
}

// RemoveRoute drops one route of a model by its effort key. A model whose
// last route goes away stays in the allowlist and is served at the
// automatic class with the ladder effort.
func (w *Workspace) RemoveRoute(access, model, effort string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Access(access); !ok {
		return w.unknownAccess("route", access)
	}
	if _, ok := w.routeOf(access, model, effort); !ok {
		return w.unknownRoute(access, model, effort)
	}
	return w.mutatePolicy(func(policy *Policy) error {
		kept := make([]Route, 0, len(policy.Routes))
		for _, route := range policy.Routes {
			if route.Access == access && route.Model == model && route.Eff == effort {
				continue
			}
			kept = append(kept, route)
		}
		policy.Routes = kept
		return nil
	})
}

// SetRouteClass changes the class of one route: cheap, standard or strong.
func (w *Workspace) SetRouteClass(access, model, effort, class string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if !contains(tiers, class) {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: route %s/%s: class must be %s", w.PolicyPath, access, model, orList(tiers)), Code: clierrors.EXIT_FORMAT}
	}
	if _, ok := w.Policy.Access(access); !ok {
		return w.unknownAccess("route", access)
	}
	if _, ok := w.routeOf(access, model, effort); !ok {
		return w.unknownRoute(access, model, effort)
	}
	return w.mutatePolicy(func(policy *Policy) error {
		for i := range policy.Routes {
			if policy.Routes[i].Access == access && policy.Routes[i].Model == model && policy.Routes[i].Eff == effort {
				policy.Routes[i].Class = class
				return nil
			}
		}
		return nil
	})
}

// SetRouteEffort renames the effort key of one route. The new key must be
// a single word (or "-") and must not collide with another route of the
// same model; it is written as given, because the resolver snaps it to
// the levels the model declares.
func (w *Workspace) SetRouteEffort(access, model, effort, next string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if next == "" || strings.ContainsAny(next, " \t|") {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: route %s/%s: effort must be a single word", w.PolicyPath, access, model), Code: clierrors.EXIT_FORMAT}
	}
	if _, ok := w.Policy.Access(access); !ok {
		return w.unknownAccess("route", access)
	}
	if _, ok := w.routeOf(access, model, effort); !ok {
		return w.unknownRoute(access, model, effort)
	}
	if _, taken := w.routeOf(access, model, next); taken {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: model %s already has a route at effort '%s'", w.PolicyPath, model, next), Code: clierrors.EXIT_FORMAT}
	}
	return w.mutatePolicy(func(policy *Policy) error {
		for i := range policy.Routes {
			if policy.Routes[i].Access == access && policy.Routes[i].Model == model && policy.Routes[i].Eff == effort {
				policy.Routes[i].Eff = next
				return nil
			}
		}
		return nil
	})
}

// SetRole changes the requirement of one plan role: cheap, standard,
// strong or trusted.
func (w *Workspace) SetRole(role, requirement string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if !contains(Roles, role) {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: unknown role '%s' (known: %s)", w.PolicyPath, role, strings.Join(Roles, ", ")), Code: clierrors.EXIT_FORMAT}
	}
	if !contains(tierRequirements, requirement) {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: role %s: requirement must be %s", w.PolicyPath, role, orList(tierRequirements)), Code: clierrors.EXIT_FORMAT}
	}
	return w.mutatePolicy(func(policy *Policy) error {
		policy.Roles[role] = requirement
		return nil
	})
}

// CreateVariant adds a machine-owned variant: empty, or a copy of another
// machine-owned variant with its budgets, narrowing and order. A fresh
// variant orders every access of the policy, so the move buttons have the
// full list to work with. The name must be free in every layer.
func (w *Workspace) CreateVariant(name string, project bool, copyOf string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkVariantName(name); err != nil {
		return err
	}
	if preset, taken := w.Policy.Preset(name); taken {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: variant '%s' already exists in %s: pick another name", name, w.variantPath(preset.Source)), Code: clierrors.EXIT_FORMAT}
	}
	preset := Preset{Name: name, Order: append([]string(nil), w.Policy.AccessNames()...)}
	if copyOf != "" {
		source, ok := w.Policy.Preset(copyOf)
		if !ok {
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: variant '%s' cannot copy unknown variant '%s' (known: %s)", name, copyOf, orList(w.Policy.PresetNames())), Code: clierrors.EXIT_FORMAT}
		}
		if source.Source == SourcePolicy {
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: variant '%s' cannot copy '%s': the policy presets are hand-edited", name, copyOf), Code: clierrors.EXIT_FORMAT}
		}
		copied := source.clone()
		preset.Off, preset.Lean, preset.Minimal, preset.Prefer = copied.Off, copied.Lean, copied.Minimal, copied.Prefer
		preset.Order, preset.Models = copied.Order, copied.Models
	}
	path, err := w.variantsTarget(project)
	if err != nil {
		return err
	}
	presets, err := w.filePresets(path, presetSource(project))
	if err != nil {
		return err
	}
	if err := SaveVariants(path, append(presets, preset)); err != nil {
		return err
	}
	w.refreshPresets(presetSource(project), append(presets, preset))
	return nil
}

// RenameVariant renames a machine-owned variant. A variant in force keeps
// forcing: the state follows the new name.
func (w *Workspace) RenameVariant(oldName, newName string) error {
	if err := w.ready(); err != nil {
		return err
	}
	preset, ok := w.Policy.Preset(oldName)
	if !ok {
		return w.unknownVariant(oldName)
	}
	if preset.Source == SourcePolicy {
		return w.readonlyVariant(oldName)
	}
	if err := w.checkVariantName(newName); err != nil {
		return err
	}
	if taken, ok := w.Policy.Preset(newName); ok {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: variant '%s' already exists in %s: pick another name", newName, w.variantPath(taken.Source)), Code: clierrors.EXIT_FORMAT}
	}
	renamed := false
	if err := w.editVariants(oldName, func(entry *Preset) error {
		entry.Name = newName
		renamed = true
		return nil
	}); err != nil {
		return err
	}
	if renamed && w.State != nil && w.State.Preset == oldName {
		return w.mutate(func(state *State) error {
			state.Preset = newName
			return nil
		})
	}
	return nil
}

// SetVariantBudget puts an access of a machine-owned variant on a budget
// level, or, with an empty level, lifts the condition: a preset carries
// only the deviations from normal, so normal is expressed by clearing.
func (w *Workspace) SetVariantBudget(name, access, level string) error {
	return w.editNamedPreset(name, func(entry *Preset) error {
		if _, ok := w.Policy.Access(access); !ok {
			return w.unknownAccess("budget", access)
		}
		switch level {
		case "":
			for _, list := range []*[]string{&entry.Off, &entry.Lean, &entry.Minimal, &entry.Prefer} {
				*list = removeItem(*list, access)
			}
			return nil
		case BudgetPrefer, BudgetLean, BudgetMinimal, BudgetOff:
			for _, list := range []*[]string{&entry.Off, &entry.Lean, &entry.Minimal, &entry.Prefer} {
				*list = removeItem(*list, access)
			}
			return presetBudgetList(entry, level).add(access)
		default:
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: variant %s: access %s: a preset carries prefer, lean, minimal or off, not '%s': clear the condition for normal", name, access, level), Code: clierrors.EXIT_FORMAT}
		}
	})
}

// presetBudgetList returns the condition list a preset budget level maps
// to: off, lean, minimal or prefer.
func presetBudgetList(preset *Preset, level string) *accessList {
	switch level {
	case BudgetOff:
		return &accessList{&preset.Off}
	case BudgetLean:
		return &accessList{&preset.Lean}
	case BudgetMinimal:
		return &accessList{&preset.Minimal}
	default:
		return &accessList{&preset.Prefer}
	}
}

// accessList appends to the list it holds.
type accessList struct {
	list *[]string
}

func (l *accessList) add(name string) error {
	*l.list = append(*l.list, name)
	return nil
}

// ToggleVariantModel narrows or widens a machine-owned variant to one
// model of an access. Without a narrowing every model of the access is
// allowed; the first toggle starts the narrowing with every model except
// the named one, further toggles move the id in or out, and a narrowing
// that holds every model of the access again is dropped. The full set is
// the allowlist of the access when it has one, the catalog models
// otherwise.
func (w *Workspace) ToggleVariantModel(name, access, id string) error {
	return w.editNamedPreset(name, func(entry *Preset) error {
		full, ok := w.servedModels(access)
		if !ok {
			return w.unknownAccess("models", access)
		}
		globs, narrowed := entry.Models[access]
		if !narrowed {
			kept := make([]string, 0, len(full))
			for _, model := range full {
				if model != id {
					kept = append(kept, model)
				}
			}
			if len(kept) == len(full) {
				return clierrors.DocflowError{Message: fmt.Sprintf("docflow: variant %s: access %s does not serve model %s", name, access, id), Code: clierrors.EXIT_FORMAT}
			}
			if entry.Models == nil {
				entry.Models = make(map[string][]string, 1)
			}
			entry.Models[access] = kept
			return nil
		}
		if contains(globs, id) {
			globs = removeItem(globs, id)
		} else {
			globs = append(append([]string(nil), globs...), id)
		}
		if sameItems(globs, full) {
			delete(entry.Models, access)
			return nil
		}
		entry.Models[access] = globs
		return nil
	})
}

// servedModels lists the models an access serves: the allowlist when it
// has one, the catalog models otherwise. The second result is false for
// an unknown access.
func (w *Workspace) servedModels(access string) ([]string, bool) {
	policyAccess, ok := w.Policy.Access(access)
	if !ok {
		return nil, false
	}
	if policyAccess.ModelsSet {
		return append([]string(nil), policyAccess.Models...), true
	}
	models := make([]string, 0, 8)
	if w.Catalog != nil {
		for _, model := range w.Catalog.Models {
			if model.Access == access {
				models = append(models, model.ID)
			}
		}
	}
	return models, true
}

// MoveVariantAccess moves an access of a machine-owned variant one step
// up or down the order. The order the variant carries is the full access
// list: a step at the edge is nowhere to move and changes nothing.
func (w *Workspace) MoveVariantAccess(name, access, dir string) error {
	return w.editNamedPreset(name, func(entry *Preset) error {
		if dir != "up" && dir != "down" {
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: variant %s: move must be up or down, got '%s'", name, dir), Code: clierrors.EXIT_FORMAT}
		}
		if _, ok := w.Policy.Access(access); !ok {
			return w.unknownAccess("order", access)
		}
		order := entry.Order
		if len(order) == 0 {
			order = w.Policy.AccessNames()
		}
		at := -1
		for i, candidate := range order {
			if candidate == access {
				at = i
				break
			}
		}
		if at < 0 {
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: variant %s: access %s is not in the order %v", name, access, order), Code: clierrors.EXIT_FORMAT}
		}
		next := at - 1
		if dir == "down" {
			next = at + 1
		}
		if next < 0 || next >= len(order) {
			return nil
		}
		moved := append([]string(nil), order...)
		moved[at], moved[next] = moved[next], moved[at]
		entry.Order = moved
		return nil
	})
}

// editNamedPreset applies an edit to a variant of a machine-owned layer
// and saves its file through the helper of the layer.
func (w *Workspace) editNamedPreset(name string, edit func(*Preset) error) error {
	if err := w.ready(); err != nil {
		return err
	}
	preset, ok := w.Policy.Preset(name)
	if !ok {
		return w.unknownVariant(name)
	}
	if preset.Source == SourcePolicy {
		return w.readonlyVariant(name)
	}
	return w.editVariants(name, edit)
}

// editVariants loads the file that defines the named variant, applies the
// edit and saves the file. The in-memory presets of the layer are
// refreshed with what was written.
func (w *Workspace) editVariants(name string, edit func(*Preset) error) error {
	preset, _ := w.Policy.Preset(name)
	path, source := w.variantsFile(preset.Source)
	presets, err := w.filePresets(path, source)
	if err != nil {
		return err
	}
	found := false
	for i := range presets {
		if presets[i].Name == name {
			found = true
			if err := edit(&presets[i]); err != nil {
				return err
			}
			break
		}
	}
	if !found {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: variant '%s' is not in the file", path, name), Code: clierrors.EXIT_FORMAT}
	}
	if err := SaveVariants(path, presets); err != nil {
		return err
	}
	w.refreshPresets(source, presets)
	return nil
}

// cleanVariants applies an edit to every preset of both machine-owned
// files that exist: the sweep an access or a model removal needs, so no
// layer keeps a dead name.
func (w *Workspace) cleanVariants(edit func(*Preset)) error {
	for _, source := range []string{SourceVariants, SourceProject} {
		path, _ := w.variantsFile(source)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if source == SourceProject {
			if err := checkRunLock(w.Root); err != nil {
				return err
			}
		}
		presets, err := w.filePresets(path, source)
		if err != nil {
			return err
		}
		for i := range presets {
			edit(&presets[i])
		}
		if err := SaveVariants(path, presets); err != nil {
			return err
		}
		w.refreshPresets(source, presets)
	}
	return nil
}

// variantsFile names the file and the source of a variants layer.
func (w *Workspace) variantsFile(source string) (string, string) {
	if source == SourceProject {
		return ProjectVariantsPath(w.Root), SourceProject
	}
	return VariantsPath(w.PolicyPath), SourceVariants
}

// variantsTarget resolves the file a new variant goes to and takes the
// checkout lock for the project layer.
func (w *Workspace) variantsTarget(project bool) (string, error) {
	path := VariantsPath(w.PolicyPath)
	if project {
		path = ProjectVariantsPath(w.Root)
		if path == "" {
			return "", clierrors.DocflowError{Message: "docflow: roster variants: no project root for a project variant", Code: clierrors.EXIT_FORMAT}
		}
		if err := checkRunLock(w.Root); err != nil {
			return "", err
		}
	}
	return path, nil
}

// presetSource names the source of a layer flag.
func presetSource(project bool) string {
	if project {
		return SourceProject
	}
	return SourceVariants
}

// refreshPresets replaces the in-memory presets of one layer with the
// list the file now holds, keeping the layer's position in the merged
// order.
func (w *Workspace) refreshPresets(source string, presets []Preset) {
	if w.Policy == nil {
		return
	}
	kept := make([]Preset, 0, len(w.Policy.Presets)+len(presets))
	inserted := false
	for _, preset := range w.Policy.Presets {
		if preset.Source == source {
			if inserted {
				continue
			}
			inserted = true
			kept = append(kept, presets...)
			continue
		}
		kept = append(kept, preset)
	}
	if !inserted {
		kept = append(kept, presets...)
	}
	w.Policy.Presets = kept
}

// validatePolicy applies a change to a copy and checks the rendered
// roster with the parser, writing nothing: the pre-check an operation
// with several files runs before it touches the first one.
func (w *Workspace) validatePolicy(change func(*Policy) error) error {
	next := w.Policy.clone()
	if err := change(next); err != nil {
		return err
	}
	parsed, err := ParsePolicy(w.PolicyPath, policyText(next))
	if err != nil {
		return err
	}
	if policyText(parsed) != policyText(next) {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: the policy does not render back as itself", w.PolicyPath), Code: clierrors.EXIT_FORMAT}
	}
	return nil
}

// unknownAccess names the access an operation could not find.
func (w *Workspace) unknownAccess(operation, name string) error {
	return clierrors.DocflowError{
		Message: fmt.Sprintf("docflow: %s: %s: unknown access '%s' (known: %s)", w.PolicyPath, operation, name, orList(w.Policy.AccessNames())),
		Code:    clierrors.EXIT_FORMAT,
	}
}

// unknownVariant names the variant an operation could not find.
func (w *Workspace) unknownVariant(name string) error {
	return clierrors.DocflowError{
		Message: fmt.Sprintf("docflow: roster: unknown variant '%s' (known: %s)", name, orList(w.Policy.PresetNames())),
		Code:    clierrors.EXIT_FORMAT,
	}
}

// readonlyVariant refuses an edit to a preset the hand-edited policy
// defines: roster.toml is rewritten only by the policy operations, which
// know nothing about single presets.
func (w *Workspace) readonlyVariant(name string) error {
	return clierrors.DocflowError{
		Message: fmt.Sprintf("docflow: %s: variant '%s' is defined in roster.toml, which the variant operations do not rewrite: edit the [preset.%s] table through the policy or by hand", w.PolicyPath, name, name),
		Code:    clierrors.EXIT_FORMAT,
	}
}

// catalogModel looks a model up in the catalog of one access; a workspace
// loaded without a catalog knows no models.
func (w *Workspace) catalogModel(access, id string) (ModelInfo, bool) {
	if w.Catalog == nil {
		return ModelInfo{}, false
	}
	for _, model := range w.Catalog.Models {
		if model.Access == access && model.ID == id {
			return model, true
		}
	}
	return ModelInfo{}, false
}

// routeOf looks a route up by its access, model and effort key.
func (w *Workspace) routeOf(access, model, effort string) (Route, bool) {
	for _, route := range w.Policy.routesOf(access, model) {
		if route.Eff == effort {
			return route, true
		}
	}
	return Route{}, false
}

// unknownRoute names the route an operation could not find.
func (w *Workspace) unknownRoute(access, model, effort string) error {
	routes := w.Policy.routesOf(access, model)
	if len(routes) == 0 {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: access %s does not serve model %s", w.PolicyPath, access, model), Code: clierrors.EXIT_FORMAT}
	}
	return clierrors.DocflowError{
		Message: fmt.Sprintf("docflow: %s: model %s has no route at effort '%s' (routed: %s)", w.PolicyPath, model, effort, orList(routeEfforts(routes))),
		Code:    clierrors.EXIT_FORMAT,
	}
}

// routeEfforts lists the effort keys of a model's routes in file order.
func routeEfforts(routes []Route) []string {
	efforts := make([]string, 0, len(routes))
	for _, route := range routes {
		efforts = append(efforts, route.Eff)
	}
	return efforts
}

// appendRoute inserts a route next to the other routes of the same model,
// so the file keeps one [route] section per model; a first route goes to
// the end.
func appendRoute(routes []Route, route Route) []Route {
	at := -1
	for i, r := range routes {
		if r.Access == route.Access && r.Model == route.Model {
			at = i
		}
	}
	if at < 0 {
		return append(routes, route)
	}
	out := make([]Route, 0, len(routes)+1)
	out = append(out, routes[:at+1]...)
	out = append(out, route)
	return append(out, routes[at+1:]...)
}

// sameItems reports whether two lists hold the same names in any order.
func sameItems(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]bool, len(left))
	for _, name := range left {
		seen[name] = true
	}
	for _, name := range right {
		if !seen[name] {
			return false
		}
	}
	return true
}
