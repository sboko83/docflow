package roster

import (
	"fmt"
	"os"
	"strings"

	"bsv-docflow/internal/clierrors"
)

// The policy operations of the workspace: the editor's writes to the
// stable roster.toml (the default table) and to the machine-owned
// variants files (the named policies over it). Every operation applies
// its change to a copy of the model, validates the result through the
// writer before the target is touched, and swaps the copy in only after
// the file is saved, so a refused operation leaves the files and the
// model as they were. The rows LoadWorkspace derived are not rebuilt —
// the editor reloads the workspace after a successful operation.

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

// AddAccess adds an access to the end of the policy: the name the CLI
// flags address, the channels that reach it and the optional kind. A new
// access has no cells yet. An unknown kind and a name the CLI flags cannot
// address are refused.
func (w *Workspace) AddAccess(name, kind string, agents []string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if len(agents) == 0 {
		return w.fail("access %s: agents is required", name)
	}
	for _, agent := range agents {
		if !contains(PolicyAgents, agent) {
			return w.fail("access %s: unknown agent '%s' (known: %s)", name, agent, strings.Join(PolicyAgents, ", "))
		}
	}
	if kind != "" && !contains(Kinds, kind) {
		return w.fail("access %s: kind must be %s", name, orList(Kinds))
	}
	if _, taken := w.Policy.Access(name); taken {
		return w.fail("access '%s' already exists", name)
	}
	return w.mutatePolicy(func(policy *Policy) error {
		if err := checkAccessName(w.PolicyPath, nil, "access", name); err != nil {
			return err
		}
		policy.Accesses = append(policy.Accesses, Access{Name: name, Agents: append([]string(nil), agents...), Kind: kind})
		policy.reindex()
		return nil
	})
}

// RemoveAccess deletes an access and everything that names it: its row,
// its entries in the variants files and its conditions in the state, so
// no layer keeps a dead name behind.
func (w *Workspace) RemoveAccess(name string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Access(name); !ok {
		return w.unknownAccess("access", name)
	}
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
			delete(state.Expires, name)
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
	_, ok := state.Expires[name]
	return ok
}

// dropAccess removes an access from the policy model.
func dropAccess(policy *Policy, name string) error {
	kept := make([]Access, 0, len(policy.Accesses))
	for _, access := range policy.Accesses {
		if access.Name != name {
			kept = append(kept, access)
		}
	}
	policy.Accesses = kept
	policy.reindex()
	for i := range policy.Presets {
		dropAccessFromPreset(&policy.Presets[i], name)
	}
	return nil
}

// dropAccessFromPreset removes an access from a preset: its off switch,
// its place in the order and its cell overrides.
func dropAccessFromPreset(preset *Preset, name string) {
	preset.Off = removeItem(preset.Off, name)
	preset.Order = removeItem(preset.Order, name)
	delete(preset.Roles, name)
}

// SetAccessCheck sets the check flag of an access. A check access never
// judges, so turning the flag on drops its review, acceptance and replan
// cells — in the policy and in every variant — instead of refusing.
func (w *Workspace) SetAccessCheck(name string, check bool) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Access(name); !ok {
		return w.unknownAccess("check", name)
	}
	if check {
		if err := w.cleanVariants(func(preset *Preset) {
			for role := range preset.Roles[name] {
				if judgeRole(role) {
					delete(preset.Roles[name], role)
				}
			}
			if len(preset.Roles[name]) == 0 {
				delete(preset.Roles, name)
			}
		}); err != nil {
			return err
		}
	}
	return w.mutatePolicy(func(policy *Policy) error {
		access, _ := policy.Access(name)
		access.Check = check
		if check {
			for role := range access.Roles {
				if judgeRole(role) {
					delete(access.Roles, role)
				}
			}
		}
		return nil
	})
}

// MoveAccess moves an access one step up or down the default priority:
// the order of roster.toml. A step at the edge changes nothing.
func (w *Workspace) MoveAccess(name, dir string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Access(name); !ok {
		return w.unknownAccess("order", name)
	}
	moved, err := moveName(w.Policy.AccessNames(), name, dir)
	if err != nil {
		return w.fail("%v", err)
	}
	return w.mutatePolicy(func(policy *Policy) error {
		policy.reorder(moved)
		return nil
	})
}

// SetCell writes one cell of the default table: the model and effort the
// access serves the role with, MODEL@EFFORT or a bare MODEL. The empty
// value removes the cell. A judge cell on a check access is refused.
func (w *Workspace) SetCell(access, role, value string) error {
	if err := w.ready(); err != nil {
		return err
	}
	known, ok := w.Policy.Access(access)
	if !ok {
		return w.unknownAccess("cell", access)
	}
	cell, err := w.checkCell(known, role, value)
	if err != nil {
		return err
	}
	return w.mutatePolicy(func(policy *Policy) error {
		target, _ := policy.Access(access)
		if cell.Empty() {
			delete(target.Roles, role)
			return nil
		}
		if target.Roles == nil {
			target.Roles = map[string]Cell{}
		}
		target.Roles[role] = cell
		return nil
	})
}

// checkCell validates a cell an operation is about to write: a known role,
// the MODEL@EFFORT shape and the check rule.
func (w *Workspace) checkCell(access *Access, role, value string) (Cell, error) {
	if !contains(Roles, role) {
		return Cell{}, w.fail("unknown role '%s' (known: %s)", role, strings.Join(Roles, ", "))
	}
	cell, err := ParseCell(value)
	if err != nil {
		return Cell{}, w.fail("access %s, role %s: %v", access.Name, role, err)
	}
	if !cell.Empty() && access.Check && judgeRole(role) {
		return Cell{}, w.fail("access %s: a check access never judges, so it serves no %s role", access.Name, role)
	}
	return cell, nil
}

// CreateVariant adds a variant to the global variants file or, with
// project set, to the project one: empty — the default table as it is —
// or a copy of another variant. The name must be free in every layer.
func (w *Workspace) CreateVariant(name string, project bool, copyOf string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkVariantName(name); err != nil {
		return err
	}
	if preset, taken := w.Policy.Preset(name); taken {
		return w.fail("variant '%s' already exists in %s: pick another name", name, w.variantPath(preset.Source))
	}
	preset := Preset{Name: name}
	if copyOf != "" {
		source, ok := w.Policy.Preset(copyOf)
		if !ok {
			return w.fail("variant '%s' cannot copy unknown variant '%s' (known: %s)", name, copyOf, orList(w.Policy.PresetNames()))
		}
		preset = source.clone()
		preset.Name = name
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

// RenameVariant renames a variant. A variant in force keeps forcing: the
// state follows the new name.
func (w *Workspace) RenameVariant(oldName, newName string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Preset(oldName); !ok {
		return w.unknownVariant(oldName)
	}
	if err := w.checkVariantName(newName); err != nil {
		return err
	}
	if taken, ok := w.Policy.Preset(newName); ok {
		return w.fail("variant '%s' already exists in %s: pick another name", newName, w.variantPath(taken.Source))
	}
	if err := w.editVariants(oldName, func(entry *Preset) error {
		entry.Name = newName
		return nil
	}); err != nil {
		return err
	}
	if w.State != nil && w.State.Preset == oldName {
		return w.mutate(func(state *State) error {
			state.Preset = newName
			return nil
		})
	}
	return nil
}

// SetVariantOff switches an access off in a variant, or back on.
func (w *Workspace) SetVariantOff(name, access string, off bool) error {
	return w.editNamedPreset(name, func(entry *Preset) error {
		if _, ok := w.Policy.Access(access); !ok {
			return w.unknownAccess("off", access)
		}
		if off {
			entry.Off = addUnique(entry.Off, access)
		} else {
			entry.Off = removeItem(entry.Off, access)
		}
		return nil
	})
}

// MoveVariantAccess moves an access of a variant one step up or down its
// order. A variant without an order starts from the policy order; an
// order equal to the policy order is dropped, so the variant follows
// later moves of the default table.
func (w *Workspace) MoveVariantAccess(name, access, dir string) error {
	return w.editNamedPreset(name, func(entry *Preset) error {
		if _, ok := w.Policy.Access(access); !ok {
			return w.unknownAccess("order", access)
		}
		order := fullOrder(w.Policy.AccessNames(), entry.Order)
		moved, err := moveName(order, access, dir)
		if err != nil {
			return w.fail("variant %s: %v", name, err)
		}
		entry.Order = moved
		if sameOrder(moved, w.Policy.AccessNames()) {
			entry.Order = nil
		}
		return nil
	})
}

// SetVariantCell overrides one cell of the default table in a variant:
// MODEL@EFFORT, or the empty value to clear the cell in this variant. With
// inherit the override is dropped and the variant takes the default cell.
func (w *Workspace) SetVariantCell(name, access, role, value string, inherit bool) error {
	return w.editNamedPreset(name, func(entry *Preset) error {
		known, ok := w.Policy.Access(access)
		if !ok {
			return w.unknownAccess("cell", access)
		}
		if inherit {
			if !contains(Roles, role) {
				return w.fail("unknown role '%s' (known: %s)", role, strings.Join(Roles, ", "))
			}
			delete(entry.Roles[access], role)
			if len(entry.Roles[access]) == 0 {
				delete(entry.Roles, access)
			}
			return nil
		}
		cell, err := w.checkCell(known, role, value)
		if err != nil {
			return err
		}
		if entry.Roles == nil {
			entry.Roles = map[string]map[string]Cell{}
		}
		if entry.Roles[access] == nil {
			entry.Roles[access] = map[string]Cell{}
		}
		entry.Roles[access][role] = cell
		return nil
	})
}

// fullOrder expands a preset order to every access: the named ones first,
// the rest in policy order.
func fullOrder(policy, order []string) []string {
	full := make([]string, 0, len(policy))
	for _, name := range order {
		if contains(policy, name) && !contains(full, name) {
			full = append(full, name)
		}
	}
	for _, name := range policy {
		if !contains(full, name) {
			full = append(full, name)
		}
	}
	return full
}

// moveName moves a name one step up or down a list; a step at the edge
// returns the list unchanged.
func moveName(order []string, name, dir string) ([]string, error) {
	if dir != "up" && dir != "down" {
		return nil, fmt.Errorf("move must be up or down, got '%s'", dir)
	}
	at := -1
	for i, candidate := range order {
		if candidate == name {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, fmt.Errorf("access %s is not in the order %v", name, order)
	}
	next := at - 1
	if dir == "down" {
		next = at + 1
	}
	moved := append([]string(nil), order...)
	if next < 0 || next >= len(order) {
		return moved, nil
	}
	moved[at], moved[next] = moved[next], moved[at]
	return moved, nil
}

// sameOrder reports whether two lists hold the same names in the same order.
func sameOrder(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// editNamedPreset applies an edit to a variant and saves its file.
func (w *Workspace) editNamedPreset(name string, edit func(*Preset) error) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Preset(name); !ok {
		return w.unknownVariant(name)
	}
	return w.editVariants(name, edit)
}

// editVariants loads the file that defines the named variant, applies the
// edit and saves the file. The in-memory presets of the layer are
// refreshed with what was written.
func (w *Workspace) editVariants(name string, edit func(*Preset) error) error {
	preset, _ := w.Policy.Preset(name)
	path, source := w.variantsFile(preset.Source)
	if source == SourceProject {
		if err := checkRunLock(w.Root); err != nil {
			return err
		}
	}
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

// cleanVariants applies an edit to every preset of both variants files
// that exist: the sweep an access removal needs, so no layer keeps a dead
// name.
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
	for i := range presets {
		presets[i].Source = source
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
		return w.fail("the policy does not render back as itself")
	}
	return nil
}

// fail is an exit-code-2 error about the policy file.
func (w *Workspace) fail(format string, args ...any) error {
	return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %s", w.PolicyPath, fmt.Sprintf(format, args...)), Code: clierrors.EXIT_FORMAT}
}

// unknownAccess names the access an operation could not find.
func (w *Workspace) unknownAccess(operation, name string) error {
	return w.fail("%s: unknown access '%s' (known: %s)", operation, name, orList(w.Policy.AccessNames()))
}

// unknownVariant names the variant an operation could not find.
func (w *Workspace) unknownVariant(name string) error {
	return clierrors.DocflowError{
		Message: fmt.Sprintf("docflow: roster: unknown variant '%s' (known: %s)", name, orList(w.Policy.PresetNames())),
		Code:    clierrors.EXIT_FORMAT,
	}
}
