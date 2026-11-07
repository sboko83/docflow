package roster

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
)

// The workspace is the roster as one model for a screen: the policy with
// its preset layers, the condition state, the catalog, the access rows,
// the variants with their source, the brief chains and the warnings. The
// loaders and the operations of this file return plain structs and import
// nothing from a terminal library, so a future web front end reads the
// same data instead of a second loader.

// Workspace is a loaded roster: the policy, the conditions next to it and
// the catalog cache joined into the rows a screen shows. Root is the
// checkout the project preset layer and the project variants file belong
// to; an empty Root means no project layer.
type Workspace struct {
	Root       string `json:"root,omitempty"`
	PolicyPath string `json:"policy"`
	StatePath  string `json:"state"`

	// Missing marks a roster file that does not exist yet: there are no
	// accesses and no chains, and PolicyPath names the file to create.
	Missing bool `json:"missing,omitempty"`
	// Legacy marks a version 1 roster: no policy is loaded and Problem
	// holds the loader error with the migration hint.
	Legacy bool `json:"legacy,omitempty"`
	// CatalogEmpty marks a catalog cache without accesses: the chains stay
	// empty and the screen offers `roster scan`.
	CatalogEmpty bool `json:"catalog_empty,omitempty"`
	// Problem is why the brief could not be built — a check access without
	// an independent reviewer, a role without a requirement. The accesses
	// stay listed, because the screen is where the human fixes the cause.
	Problem string `json:"problem,omitempty"`

	Accesses   []WorkspaceAccess  `json:"accesses"`
	Variants   []WorkspaceVariant `json:"variants"`
	Preset     string             `json:"preset,omitempty"`
	Roles      []RoleBrief        `json:"roles"`
	Warnings   []string           `json:"warnings,omitempty"`
	Conditions []Condition        `json:"conditions"`

	// CatalogAccesses and CatalogModels are the catalog's own view: the
	// accesses the machine knows with their channels, and the models of
	// every access with context, price and reasoning levels — what the
	// editor's pickers offer. They are read-only input; the policy
	// operations write the roster files.
	CatalogAccesses []WorkspaceCatalogAccess    `json:"catalog_accesses,omitempty"`
	CatalogModels   map[string][]WorkspaceModel `json:"catalog_models,omitempty"`

	Policy  *Policy  `json:"-"`
	State   *State   `json:"-"`
	Catalog *Catalog `json:"-"`

	// legacy keeps the error a version 1 roster produced, so a CLI command
	// returns exactly the error it returned before the workspace existed.
	legacy error
}

// PlanningRoute chooses the first heavy route of the active brief, falling
// back to its executor chain when heavy has no routes.
func (w *Workspace) PlanningRoute() (agents.Route, error) {
	for _, role := range []string{"heavy", "executor"} {
		for _, entry := range w.Roles {
			if entry.Role == role && len(entry.Routes) > 0 {
				return entry.Routes[0].Route(), nil
			}
		}
	}
	return agents.Route{}, fmt.Errorf("roster has no heavy or executor route: configure the roster and scan its catalog")
}

// WorkspaceAccess is one access row of the roster screen: the channels and
// flags of the policy, the kind, auth status and model count the catalog
// adds, the effective budget the conditions leave, and the conditions in
// force with their dates and their source.
type WorkspaceAccess struct {
	Access     string      `json:"access"`
	Agents     []string    `json:"agents,omitempty"`
	Kind       string      `json:"kind,omitempty"`
	Auth       string      `json:"auth,omitempty"`
	Models     int         `json:"models"`
	Budget     string      `json:"budget"`
	Check      bool        `json:"check,omitempty"`
	Trusted    bool        `json:"trusted,omitempty"`
	Off        bool        `json:"off,omitempty"`
	Allow      []string    `json:"allow,omitempty"`
	Deny       []string    `json:"deny,omitempty"`
	Until      string      `json:"until,omitempty"`
	Expires    string      `json:"expires,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// WorkspaceVariant is one preset of the roster with the layer it comes
// from: roster.toml is hand-edited and read-only for the workspace, the
// global and the project variants files are machine-owned.
type WorkspaceVariant struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
}

// WorkspaceCatalogAccess is one access the catalog knows: what the
// add-access picker offers and shows.
type WorkspaceCatalogAccess struct {
	Access string   `json:"access"`
	Agents []string `json:"agents,omitempty"`
	Kind   string   `json:"kind,omitempty"`
	Models int      `json:"models"`
}

// WorkspaceModel is one catalog model of an access: what the add-model
// picker offers and shows. A price of zero is omitted, so an unknown
// price never reads as free.
type WorkspaceModel struct {
	ID        string   `json:"id"`
	Context   int      `json:"context,omitempty"`
	MaxOut    int      `json:"max_out,omitempty"`
	Reasoning bool     `json:"reasoning,omitempty"`
	Efforts   []string `json:"efforts,omitempty"`
	Input     float64  `json:"input,omitempty"`
	Output    float64  `json:"output,omitempty"`
}

// LoadWorkspace reads the whole roster as a screen shows it: the policy
// with the global and project presets, the condition state (an expired off
// is dropped on the way in and the file is left for the next mutation),
// the catalog cache, the brief chains of every role, the variants and the
// warnings. A missing roster or a version 1 one is a flag, not an error:
// the screen has a hint and a migration for both.
func LoadWorkspace(rosterPath, root string) (*Workspace, error) {
	workspace, err := loadWorkspace(rosterPath, root)
	if err != nil {
		return nil, err
	}
	catalog, err := LoadCatalog("")
	if err != nil {
		return nil, err
	}
	workspace.Catalog = catalog
	workspace.CatalogEmpty = len(catalog.Accesses) == 0
	workspace.fillCatalogRows(catalog)
	if workspace.Policy == nil {
		return workspace, nil
	}
	if workspace.Accesses, err = workspace.accessRows(); err != nil {
		return nil, err
	}
	brief, err := BuildBrief(workspace.Policy, workspace.State, catalog, nil)
	if err != nil {
		workspace.Problem = err.Error()
		return workspace, nil
	}
	workspace.Roles = brief.Roles
	workspace.Warnings = brief.Warnings
	return workspace, nil
}

// fillCatalogRows copies the catalog into the picker rows: the accesses
// the machine knows with their channels and model counts, and the models
// of every access sorted by id, so a picker renders the same list a scan
// produced.
func (w *Workspace) fillCatalogRows(catalog *Catalog) {
	for _, info := range catalog.Accesses {
		count := 0
		for _, model := range catalog.Models {
			if model.Access == info.Access {
				count++
			}
		}
		w.CatalogAccesses = append(w.CatalogAccesses, WorkspaceCatalogAccess{
			Access: info.Access,
			Agents: append([]string(nil), info.Agents...),
			Kind:   info.Kind,
			Models: count,
		})
	}
	for _, model := range catalog.Models {
		if w.CatalogModels == nil {
			w.CatalogModels = make(map[string][]WorkspaceModel, len(catalog.Accesses))
		}
		entry := WorkspaceModel{
			ID:        model.ID,
			Context:   model.Context,
			MaxOut:    model.MaxOut,
			Reasoning: model.Reasoning,
			Efforts:   append([]string(nil), model.Efforts...),
		}
		if model.Cost != nil {
			entry.Input, entry.Output = model.Cost.Input, model.Cost.Output
		}
		w.CatalogModels[model.Access] = append(w.CatalogModels[model.Access], entry)
	}
	for _, models := range w.CatalogModels {
		sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	}
}

// LoadPolicyState reads the policy and its condition state alone: the pair
// a condition mutation writes. The catalog and the brief are left out, so
// a command that only edits a condition does not depend on a scan or on a
// complete [role] table; a version 1 roster fails here as LoadPolicy does.
func LoadPolicyState(rosterPath, root string) (*Workspace, error) {
	workspace, err := loadWorkspace(rosterPath, root)
	if err != nil {
		return nil, err
	}
	if workspace.legacy != nil {
		return nil, workspace.legacy
	}
	return workspace, nil
}

// loadWorkspace reads the roster files and the rows that need no catalog:
// the access list, the variants and the conditions in force.
func loadWorkspace(rosterPath, root string) (*Workspace, error) {
	policyPath := PolicyPath(rosterPath)
	workspace := &Workspace{
		Root:       root,
		PolicyPath: policyPath,
		StatePath:  StatePath(policyPath),
		Accesses:   []WorkspaceAccess{},
		Variants:   []WorkspaceVariant{},
		Roles:      []RoleBrief{},
		Warnings:   []string{},
		Conditions: []Condition{},
	}
	if _, err := os.Stat(policyPath); err != nil {
		if !os.IsNotExist(err) {
			return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster file: %v", err), Code: clierrors.EXIT_FORMAT}
		}
		workspace.Missing = true
		return workspace, nil
	}
	policy, err := loadPolicyAt(policyPath, root)
	if err != nil {
		if legacyRosterFile(policyPath) {
			workspace.Legacy = true
			workspace.Problem = err.Error()
			workspace.legacy = err
			return workspace, nil
		}
		return nil, err
	}
	state, err := LoadState(workspace.StatePath)
	if err != nil {
		return nil, err
	}
	if err := state.Validate(policy); err != nil {
		return nil, err
	}
	workspace.Policy = policy
	workspace.State = state
	workspace.Preset = state.Preset
	workspace.Conditions = Conditions(policy, state)
	workspace.Variants = workspace.variantRows()
	return workspace, nil
}

// legacyRosterFile reports whether the file at path is a version 1 roster:
// it has a top-level table only the old format declared, and a loader
// error about a file like that is the migration hint, not a broken policy.
func legacyRosterFile(path string) bool {
	text, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	root, err := agents.ParseTOML(string(text))
	if err != nil {
		return false
	}
	return hasLegacyTables(root)
}

// accessRows builds the access rows of the screen in policy file order.
// The budget and the off switch are the effective ones — the conditions
// merged over the policy — because that is what a run would use.
func (w *Workspace) accessRows() ([]WorkspaceAccess, error) {
	merged, err := Merge(w.Policy, w.State)
	if err != nil {
		return nil, err
	}
	known := make(map[string]AccessInfo, len(w.Catalog.Accesses))
	for _, entry := range w.Catalog.Accesses {
		known[entry.Access] = entry
	}
	rows := make([]WorkspaceAccess, 0, len(w.Policy.Accesses))
	for i := range w.Policy.Accesses {
		access := &w.Policy.Accesses[i]
		row := WorkspaceAccess{
			Access:  access.Name,
			Agents:  append([]string(nil), access.Agents...),
			Budget:  access.Budget,
			Check:   access.Check,
			Trusted: access.Trusted,
			Models:  w.Catalog.modelCount(access.Name),
		}
		if entry, ok := known[access.Name]; ok {
			row.Kind = entry.Kind
			row.Auth = entry.Auth
		}
		if effective, ok := merged.Access(access.Name); ok {
			row.Budget = effective.Budget
			// The allowlist is the effective one: a condition replaces the
			// policy list instead of widening it, and the screen must show
			// what a run would use. Denials add up over both layers.
			if effective.ModelsSet {
				row.Allow = append([]string{}, effective.Models...)
			}
			row.Deny = append([]string{}, effective.Exclude...)
			if !effective.Expires.IsZero() {
				row.Expires = dayText(effective.Expires)
			}
		}
		if w.State != nil {
			if until, dated := w.State.Until[access.Name]; dated {
				row.Until = dayText(until)
			}
		}
		row.Off = row.Budget == BudgetOff
		for _, condition := range w.Conditions {
			if condition.Access == access.Name {
				row.Conditions = append(row.Conditions, condition)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// variantRows lists the presets of every layer in policy file order with
// the file that defines them.
func (w *Workspace) variantRows() []WorkspaceVariant {
	rows := make([]WorkspaceVariant, 0, len(w.Policy.Presets))
	for _, preset := range w.Policy.Presets {
		rows = append(rows, WorkspaceVariant{
			Name:   preset.Name,
			Source: preset.Source,
			Path:   w.variantPath(preset.Source),
		})
	}
	return rows
}

// variantPath names the file a preset layer lives in.
func (w *Workspace) variantPath(source string) string {
	switch source {
	case SourceVariants:
		return VariantsPath(w.PolicyPath)
	case SourceProject:
		return ProjectVariantsPath(w.Root)
	}
	return w.PolicyPath
}

// ready reports why the workspace cannot be edited: a missing roster, a
// version 1 one or a model built by hand without a policy.
func (w *Workspace) ready() error {
	switch {
	case w.Missing:
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: no roster at %s: create the policy before editing the conditions", w.PolicyPath), Code: clierrors.EXIT_FORMAT}
	case w.legacy != nil:
		return w.legacy
	case w.Legacy:
		return clierrors.DocflowError{Message: w.Problem, Code: clierrors.EXIT_FORMAT}
	case w.Policy == nil:
		return clierrors.DocflowError{Message: "docflow: roster: no policy to edit the conditions of", Code: clierrors.EXIT_FORMAT}
	}
	return nil
}

// mutate applies a change to a copy of the conditions, checks the result
// against the policy and saves it through State.Save: a refused condition
// leaves the state file and the model as they were. The rows LoadWorkspace
// derived are not rebuilt here; a screen reloads the workspace after a
// successful write.
func (w *Workspace) mutate(change func(*State) error) error {
	if err := w.ready(); err != nil {
		return err
	}
	if w.State == nil {
		w.State = NewState(w.StatePath)
	}
	next := w.State.clone()
	if err := change(next); err != nil {
		return err
	}
	if err := next.Validate(w.Policy); err != nil {
		return err
	}
	if err := next.Save(w.StatePath); err != nil {
		return err
	}
	w.State = next
	w.Preset = next.Preset
	return nil
}

// checkAccess refuses a condition that names an access the policy does not
// declare: an unknown name is a typo, not a condition to write into the
// file and read as "no effect" later.
func (w *Workspace) checkAccess(condition, access string) error {
	if _, ok := w.Policy.Access(access); ok {
		return nil
	}
	return clierrors.DocflowError{
		Message: fmt.Sprintf("docflow: %s: %s: unknown access '%s' (known: %s)", w.StatePath, condition, access, orList(w.Policy.AccessNames())),
		Code:    clierrors.EXIT_FORMAT,
	}
}

// SetOff switches an access off; a non-zero until bounds the condition to
// the named day and a zero one leaves it open-ended.
func (w *Workspace) SetOff(access string, until time.Time) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkAccess("off", access); err != nil {
		return err
	}
	return w.mutate(func(state *State) error {
		state.SetOff(access, until)
		return nil
	})
}

// SetOn lifts the off conditions of an access and nothing else.
func (w *Workspace) SetOn(access string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkAccess("on", access); err != nil {
		return err
	}
	return w.mutate(func(state *State) error {
		state.SetOn(access)
		return nil
	})
}

// SetBudget overrides the budget of an access. A level the policy cannot
// take — an unknown one, or minimal on an access that is not trusted — is
// refused before the file is written.
func (w *Workspace) SetBudget(access, level string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkAccess("budget", access); err != nil {
		return err
	}
	return w.mutate(func(state *State) error { return state.SetBudget(access, level) })
}

// SetExpires bounds an access to the named day: it is usable through that
// day and gone after it.
func (w *Workspace) SetExpires(access string, day time.Time) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkAccess("expires", access); err != nil {
		return err
	}
	return w.mutate(func(state *State) error {
		state.SetExpires(access, day)
		return nil
	})
}

// Allow narrows an access to the model globs of the conditions.
func (w *Workspace) Allow(access string, globs []string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkAccess("allow", access); err != nil {
		return err
	}
	return w.mutate(func(state *State) error { return state.Allow(access, globs) })
}

// Deny hides model globs from an access.
func (w *Workspace) Deny(access string, globs []string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkAccess("deny", access); err != nil {
		return err
	}
	return w.mutate(func(state *State) error { return state.Deny(access, globs) })
}

// UnsetGlob removes one pattern from the condition allowlist and the
// condition denylist of an access: the screen edits the temporary layer,
// and the hand-edited policy keeps the globs it declares. An empty list
// is dropped instead of written, so the state file stays readable.
func (w *Workspace) UnsetGlob(access, glob string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkAccess("allow", access); err != nil {
		return err
	}
	return w.mutate(func(state *State) error {
		for _, table := range []map[string][]string{state.Models, state.Exclude} {
			kept := removeItem(table[access], glob)
			if len(kept) == 0 {
				delete(table, access)
				continue
			}
			table[access] = kept
		}
		return nil
	})
}

// SetPreset makes a preset of any layer the active one: --preset NAME must
// mean the same preset everywhere, so a name no layer declares is refused.
func (w *Workspace) SetPreset(name string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if _, ok := w.Policy.Preset(name); !ok {
		return clierrors.DocflowError{
			Message: fmt.Sprintf("docflow: %s: preset: unknown preset '%s' (known: %s)", w.StatePath, name, orList(w.Policy.PresetNames())),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	return w.mutate(func(state *State) error {
		state.Preset = name
		return nil
	})
}

// ClearPreset drops the active preset and keeps the other conditions.
func (w *Workspace) ClearPreset() error {
	return w.mutate(func(state *State) error {
		state.Preset = ""
		return nil
	})
}

// Reset removes the condition file: reset means "no conditions", not an
// empty file. It reads no policy, so it works on a broken or a version 1
// roster too — dropping the conditions is the way out of one.
func (w *Workspace) Reset() error {
	path := w.StatePath
	if path == "" {
		path = DefaultStatePath()
	}
	path = expandUser(path)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster state: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	w.State = NewState(path)
	w.Preset = ""
	return nil
}

// SaveVariant freezes the conditions in force as a new preset and writes it
// into the global variants file next to the policy or, with project set,
// into the project one (docs/roster.variants.toml). A preset carries
// budgets, an access order and model allowlists only, so a condition it
// cannot express — a dated off, an expiry, a denial, a budget normal — is
// an error instead of a variant that would mean something else. The name
// must be free in every layer, and the project file obeys the checkout
// lock: it lives in the working tree.
func (w *Workspace) SaveVariant(name string, project bool) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.checkVariantName(name); err != nil {
		return err
	}
	if preset, ok := w.Policy.Preset(name); ok {
		return clierrors.DocflowError{
			Message: fmt.Sprintf("docflow: %s: variant '%s' already exists in %s: pick another name", w.StatePath, name, w.variantPath(preset.Source)),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	preset, err := w.conditionPreset(name)
	if err != nil {
		return err
	}
	path, source := VariantsPath(w.PolicyPath), SourceVariants
	if project {
		path, source = ProjectVariantsPath(w.Root), SourceProject
		if path == "" {
			return clierrors.DocflowError{Message: "docflow: roster variants: no project root for a project variant", Code: clierrors.EXIT_FORMAT}
		}
		if err := checkRunLock(w.Root); err != nil {
			return err
		}
	}
	presets, err := w.filePresets(path, source)
	if err != nil {
		return err
	}
	return SaveVariants(path, append(presets, *preset))
}

// DeleteVariant removes a variant from the machine-owned file that defines
// it. A preset of roster.toml is refused: the hand-edited policy is never
// rewritten, because a rewrite would drop its comments and its order. The
// variant in force is cleared first, so the conditions never name a preset
// that is gone.
func (w *Workspace) DeleteVariant(name string) error {
	if err := w.ready(); err != nil {
		return err
	}
	preset, ok := w.Policy.Preset(name)
	if !ok {
		return clierrors.DocflowError{
			Message: fmt.Sprintf("docflow: roster: unknown variant '%s' (known: %s)", name, orList(w.Policy.PresetNames())),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	if preset.Source == SourcePolicy {
		return clierrors.DocflowError{
			Message: fmt.Sprintf("docflow: %s: variant '%s' is defined in roster.toml, which docflow never rewrites: delete the [preset.%s] table by hand", w.PolicyPath, name, name),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	path, source := VariantsPath(w.PolicyPath), SourceVariants
	if preset.Source == SourceProject {
		path, source = ProjectVariantsPath(w.Root), SourceProject
		if path == "" {
			return clierrors.DocflowError{Message: "docflow: roster variants: no project root for a project variant", Code: clierrors.EXIT_FORMAT}
		}
		if err := checkRunLock(w.Root); err != nil {
			return err
		}
	}
	presets, err := w.filePresets(path, source)
	if err != nil {
		return err
	}
	kept := make([]Preset, 0, len(presets))
	found := false
	for _, entry := range presets {
		if entry.Name == name {
			found = true
			continue
		}
		kept = append(kept, entry)
	}
	if !found {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: variant '%s' is not in the file", path, name), Code: clierrors.EXIT_FORMAT}
	}
	if w.State != nil && w.State.Preset == name {
		if err := w.ClearPreset(); err != nil {
			return err
		}
	}
	return SaveVariants(path, kept)
}

// checkVariantName rejects a name the file and --preset cannot address: an
// empty name or one with whitespace is not a single preset name.
func (w *Workspace) checkVariantName(name string) error {
	if name == "" || strings.ContainsAny(name, " \t") {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster: variant name must be a single word, got '%s'", name), Code: clierrors.EXIT_FORMAT}
	}
	return nil
}

// filePresets reads the presets a variants file holds; a missing file is an
// empty layer, so the first variant creates it.
func (w *Workspace) filePresets(path, source string) ([]Preset, error) {
	text, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []Preset{}, nil
	}
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster variants: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return parseVariantPresets(w.Policy, path, string(text), source, nil)
}

// conditionPreset turns the conditions in force into a preset: the budgets
// and the model allowlists the conditions and the active preset give every
// access against the policy, and the access order they put in force. A
// condition without a preset key is an error, never a silent loss: a
// variant the human saved must mean what the screen showed.
func (w *Workspace) conditionPreset(name string) (*Preset, error) {
	merged, err := Merge(w.Policy, w.State)
	if err != nil {
		return nil, err
	}
	state := w.State
	if state == nil {
		state = NewState(w.StatePath)
	}
	problems := make([]string, 0)
	for _, access := range sortedKeys(state.Until) {
		problems = append(problems, fmt.Sprintf("off until %s for %s: a preset has no dates", dayText(state.Until[access]), access))
	}
	for _, access := range sortedKeys(state.Expires) {
		problems = append(problems, fmt.Sprintf("expires %s for %s: a preset has no dates", dayText(state.Expires[access]), access))
	}
	for _, access := range sortedKeys(state.Exclude) {
		problems = append(problems, fmt.Sprintf("denied globs for %s: a preset has no deny key", access))
	}
	preset := &Preset{Name: name, Models: map[string][]string{}}
	for i := range w.Policy.Accesses {
		access := &w.Policy.Accesses[i]
		effective, ok := merged.Access(access.Name)
		if !ok || effective.Budget == access.Budget {
			continue
		}
		key := presetBudgetKey(effective.Budget)
		if key == "" {
			problems = append(problems, fmt.Sprintf("budget %s for %s: a preset names off, lean, minimal and prefer only", effective.Budget, access.Name))
			continue
		}
		preset.addAccess(key, access.Name)
	}
	for i := range w.Policy.Accesses {
		access := &w.Policy.Accesses[i]
		effective, ok := merged.Access(access.Name)
		if !ok || (effective.ModelsSet == access.ModelsSet && slices.Equal(effective.Models, access.Models)) {
			continue
		}
		preset.Models[access.Name] = append([]string{}, effective.Models...)
	}
	if len(problems) > 0 {
		return nil, clierrors.DocflowError{
			Message: fmt.Sprintf("docflow: %s: cannot save the conditions as a variant: %s", w.StatePath, strings.Join(problems, "; ")),
			Code:    clierrors.EXIT_FORMAT,
		}
	}
	// The order the conditions put in force is part of what the screen
	// shows: the active preset may reorder the accesses, and a variant that
	// dropped the order would route differently.
	if order := merged.AccessNames(); !slices.Equal(order, w.Policy.AccessNames()) {
		preset.Order = order
	}
	if len(preset.Models) == 0 {
		preset.Models = nil
	}
	return preset, nil
}

// presetBudgetKey names the preset key a budget uses; an empty string means
// the preset grammar cannot carry the level (normal is the policy default).
func presetBudgetKey(level string) string {
	switch level {
	case BudgetOff, BudgetLean, BudgetMinimal, BudgetPrefer:
		return level
	}
	return ""
}

// addAccess appends an access to the list one preset condition key holds.
func (p *Preset) addAccess(key, access string) {
	switch key {
	case BudgetOff:
		p.Off = append(p.Off, access)
	case BudgetLean:
		p.Lean = append(p.Lean, access)
	case BudgetMinimal:
		p.Minimal = append(p.Minimal, access)
	case BudgetPrefer:
		p.Prefer = append(p.Prefer, access)
	}
}

// ExplainChange is one header key where the plan and the brief disagree:
// the value in the plan and the value the brief gives now. An empty Old
// means the key is absent, an empty New means apply would remove it.
type ExplainChange struct {
	Key string `json:"key"`
	Old string `json:"old"`
	New string `json:"new"`
}

// ExplainReport is the difference between a plan header and the roster
// brief: the keys apply would rewrite and the warnings of the plan roles.
type ExplainReport struct {
	Plan     string          `json:"plan"`
	Path     string          `json:"path"`
	Changes  []ExplainChange `json:"changes"`
	Warnings []string        `json:"warnings"`
}

// ExplainPlan compares the header of a plan with the brief of this
// workspace and reports the keys apply would rewrite: the same difference
// `roster explain` prints. It only reads.
func (w *Workspace) ExplainPlan(planID string) (ExplainReport, error) {
	if planID == "" {
		return ExplainReport{}, clierrors.DocflowError{Message: "docflow: roster explain: PLAN is required", Code: clierrors.EXIT_FORMAT}
	}
	if err := w.ready(); err != nil {
		return ExplainReport{}, err
	}
	plan, err := cycle.FindPlan(w.Root, planID)
	if err != nil {
		return ExplainReport{}, err
	}
	brief, err := BuildBrief(w.Policy, w.State, w.Catalog, PlanRoles(plan))
	if err != nil {
		return ExplainReport{}, err
	}
	return ExplainReport{
		Plan:     plan.Meta["id"],
		Path:     plan.Path,
		Changes:  headerChanges(plan, brief.Header()),
		Warnings: append([]string{}, brief.Warnings...),
	}, nil
}

// ApplyPlan writes the chains of this workspace roster into a plan header
// through Apply: the same refusal while a run holds the checkout lock and
// the same rollback when the written plan fails lint.
func (w *Workspace) ApplyPlan(planID string, dryRun bool) (Result, ApplyReport, error) {
	if err := w.ready(); err != nil {
		return Result{}, ApplyReport{}, err
	}
	return Apply(w.Root, planID, w.Policy, w.State, w.Catalog, dryRun)
}

// ExplainPlan is (*Workspace).ExplainPlan at the roster the workspace
// loads for the given project: the default roster, the state next to it and
// the catalog cache.
func ExplainPlan(root, planID string) (ExplainReport, error) {
	workspace, err := LoadWorkspace("", root)
	if err != nil {
		return ExplainReport{}, err
	}
	return workspace.ExplainPlan(planID)
}

// ApplyPlan is (*Workspace).ApplyPlan at the roster the workspace loads for
// the given project.
func ApplyPlan(root, planID string, dryRun bool) (Result, ApplyReport, error) {
	workspace, err := LoadWorkspace("", root)
	if err != nil {
		return Result{}, ApplyReport{}, err
	}
	return workspace.ApplyPlan(planID, dryRun)
}

// headerChanges compares the header of the plan with the brief: one entry
// per key that differs. A key the plan holds but the brief leaves empty is
// a removal; a key both miss is not a change.
func headerChanges(plan *format.Plan, header map[string]string) []ExplainChange {
	changes := make([]ExplainChange, 0)
	for _, key := range headerKeys {
		wanted, has := header[key]
		old, exists := plan.Meta[key]
		if !has && !exists {
			continue
		}
		if has && exists && old == wanted {
			continue
		}
		changes = append(changes, ExplainChange{Key: key, Old: old, New: wanted})
	}
	return changes
}
