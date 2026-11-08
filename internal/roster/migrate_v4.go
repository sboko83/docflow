package roster

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
)

// MigratedFile is one file a migration rewrites: its path, the new text and,
// after a write, the backup of the old text.
type MigratedFile struct {
	Path   string `json:"path"`
	Backup string `json:"backup,omitempty"`
	Text   string `json:"text"`
}

// MigrateResult is the outcome of a roster migration: the resolved roster
// path, the migrated policy text, the backup after a write, the other
// files the migration rewrites (the variants files and the condition
// state) and the notes about what could not be carried over exactly.
type MigrateResult struct {
	Path   string         `json:"path"`
	Backup string         `json:"backup,omitempty"`
	Text   string         `json:"text"`
	Files  []MigratedFile `json:"files,omitempty"`
	Notes  []string       `json:"notes,omitempty"`
}

// Migrate converts older roster text into v4 policy text without a
// catalog: an access takes the models its policy names. The variants and
// the conditions are files of their own, which MigrateFile converts.
func Migrate(text string) (string, error) {
	v3text, err := migrateToV3("roster.toml", text)
	if err != nil {
		return "", err
	}
	policy, err := parseV3Policy("roster.toml", v3text)
	if err != nil {
		return "", err
	}
	converted, err := convertV3(policy, nil, nil, nil, nil)
	if err != nil {
		return "", err
	}
	return converted.policy, nil
}

// MigrateFile converts the roster at path ("" means DefaultPath) to version
// 4 together with the files around it: the global variants file, the
// project variants file of the current checkout and the condition state.
// The v3 classes resolve against the scanned catalog, so the cells name
// the models the v3 resolver picked on this machine. With write, every
// changed file keeps its old text in FILE.bak and is replaced through a
// temporary file and a rename; without it nothing is written.
func MigrateFile(rosterPath string, write bool) (MigrateResult, error) {
	resolved := PolicyPath(rosterPath)
	text, err := os.ReadFile(resolved)
	if err != nil {
		return MigrateResult{}, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster file: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	v3text, err := migrateToV3(resolved, string(text))
	if err != nil {
		return MigrateResult{}, err
	}
	policy, err := parseV3Policy(resolved, v3text)
	if err != nil {
		return MigrateResult{}, err
	}
	catalog, err := LoadCatalog("")
	if err != nil {
		return MigrateResult{}, err
	}
	_, models := indexCatalog(catalog)
	var catalogNote string
	if len(catalog.Accesses) == 0 {
		// The v3 rules pick models by class, price and context from the
		// catalog; without one the cells come from the names the policy
		// spells out, which is a guess the human should know about.
		catalogNote = "the catalog cache is empty: the cells come from the models the policy names; run `docflow roster scan` first for the exact v3 choice"
	}
	globalPath := VariantsPath(resolved)
	global, globalCurrent, err := readV2Variants(globalPath)
	if err != nil {
		return MigrateResult{}, err
	}
	projectPath := ProjectVariantsPath(ProjectRoot())
	if projectPath != "" && samePath(projectPath, globalPath) {
		projectPath = ""
	}
	project, _, err := readV2Variants(projectPath)
	if err != nil {
		return MigrateResult{}, err
	}
	converted, err := convertV3(policy, models, global, project, globalCurrent)
	if err != nil {
		return MigrateResult{}, err
	}
	result := MigrateResult{Path: resolved, Text: converted.policy, Notes: converted.notes}
	if catalogNote != "" {
		result.Notes = append([]string{catalogNote}, result.Notes...)
	}
	if global != nil || len(policy.Presets) > 0 {
		result.Files = append(result.Files, MigratedFile{Path: globalPath, Text: converted.global})
	}
	if project != nil {
		result.Files = append(result.Files, MigratedFile{Path: projectPath, Text: converted.project})
	}
	statePath := StatePath(resolved)
	stateText, notes, changed, err := migrateStateFile(statePath, converted.off)
	if err != nil {
		return MigrateResult{}, err
	}
	result.Notes = append(result.Notes, notes...)
	if changed {
		result.Files = append(result.Files, MigratedFile{Path: statePath, Text: stateText})
	}
	if !write {
		return result, nil
	}
	backup, err := replaceFile(resolved, converted.policy)
	if err != nil {
		return MigrateResult{}, err
	}
	result.Backup = backup
	for i := range result.Files {
		backup, err := replaceFile(result.Files[i].Path, result.Files[i].Text)
		if err != nil {
			return MigrateResult{}, err
		}
		result.Files[i].Backup = backup
	}
	return result, nil
}

// replaceFile keeps the old text of a file in FILE.bak (when there is
// one) and writes the new text through a temporary file and a rename.
func replaceFile(path, text string) (string, error) {
	backup := ""
	if old, err := os.ReadFile(path); err == nil {
		backup = path + ".bak"
		if err := os.WriteFile(backup, old, 0o644); err != nil {
			return "", clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: backup: %v", err), Code: clierrors.EXIT_FORMAT}
		}
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(text), 0o644); err != nil {
		_ = os.Remove(temporary)
		return "", clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return "", clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return backup, nil
}

// readV2Variants reads a version 2 variants file. A missing file or an
// empty path gives nil. A file already at the current version is not
// converted: its presets come back as they are (current), so the presets
// roster.toml moves into the file join them instead of replacing them.
func readV2Variants(path string) ([]v3Preset, []Preset, error) {
	if path == "" {
		return nil, nil, nil
	}
	text, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster variants: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	if root, err := agents.ParseTOML(string(text)); err == nil {
		if version, ok := root["version"].(int64); ok && version == VariantsVersion {
			current, err := parseVariantPresets(nil, path, string(text), SourceVariants, nil)
			return nil, current, err
		}
	}
	presets, err := parseV2Variants(path, string(text))
	if err != nil {
		return nil, nil, err
	}
	if presets == nil {
		presets = []v3Preset{}
	}
	return presets, nil, nil
}

// converted is the v4 text a v3 policy and its presets become, plus the
// accesses the v3 policy had at budget off (they become off conditions)
// and the notes for the human.
type converted struct {
	policy  string
	global  string
	project string
	off     []string
	notes   []string
}

// convertV3 converts a v3 policy, the presets of its global and project
// variants files and the presets of roster.toml (which move to the global
// file) to version 4. The base table is the v3 resolution of the policy;
// a preset becomes the difference between its own resolution and the
// base: the accesses it switches off, its order when it differs, and the
// cells it changes.
func convertV3(policy *v3Policy, models map[string][]ModelInfo, global, project []v3Preset, current []Preset) (converted, error) {
	var out converted
	base := policy.clone()
	for i := range base.Accesses {
		access := &base.Accesses[i]
		if access.Budget == "off" {
			// v4 has no permanent off in the policy: the switch becomes a
			// condition, and the access keeps the cells it would serve.
			out.off = append(out.off, access.Name)
			access.Budget = "normal"
			out.notes = append(out.notes, fmt.Sprintf("access %s had budget off: it is switched off in the conditions (`docflow roster on %s` brings it back)", access.Name, access.Name))
		}
	}
	// The order is the v3 one, where an access at budget off ranks last;
	// the cells are those it would serve once switched back on.
	order, _ := v3Table(policy, models)
	_, cells := v3Table(base, models)
	v4 := &Policy{Path: "roster.toml", Version: PolicyVersion}
	for _, name := range order {
		source := base.access(name)
		access := Access{Name: name, Agents: source.Agents, Kind: source.Kind, Check: source.Check, Expires: source.Expires, Roles: cells[name]}
		if len(access.Roles) == 0 {
			out.notes = append(out.notes, fmt.Sprintf("access %s: no model qualified for any role; set its cells in `docflow roster web`", name))
		}
		v4.Accesses = append(v4.Accesses, access)
	}
	v4.reindex()
	out.policy = policyText(v4)
	if _, err := ParsePolicy("roster.toml", out.policy); err != nil {
		return out, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: the migrated roster is not a valid version %d policy: %v", PolicyVersion, err), Code: clierrors.EXIT_FORMAT}
	}
	// Only the converted presets are checked against the new policy: the
	// presets a file already holds at the current version are carried over
	// as they are, and their own load reports what they name.
	var problem error
	convert := func(presets []v3Preset, current []Preset) string {
		list := make([]Preset, 0, len(presets))
		for i := range presets {
			list = append(list, convertV3Preset(policy, base, models, order, cells, &presets[i]))
		}
		if _, err := parseVariantPresets(v4, "roster.variants.toml", variantsText(list), SourceVariants, nil); err != nil && problem == nil {
			problem = err
		}
		return variantsText(append(current, list...))
	}
	out.global = convert(append(append([]v3Preset(nil), policy.Presets...), global...), append([]Preset(nil), current...))
	out.project = convert(project, nil)
	if problem != nil {
		return out, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster migrate: the migrated variants are not valid: %v", problem), Code: clierrors.EXIT_FORMAT}
	}
	return out, nil
}

// convertV3Preset resolves a v3 preset over the base policy and keeps what
// differs from the base table.
func convertV3Preset(original, base *v3Policy, models map[string][]ModelInfo, baseOrder []string, baseCells map[string]map[string]Cell, preset *v3Preset) Preset {
	// The order comes from the policy as it was (an access at budget off
	// ranks last, as in the base order); the cells from the policy with
	// those accesses switched back on, as in the base cells.
	ordered := original.clone()
	applyV3Preset(ordered, preset)
	order, _ := v3Table(ordered, models)
	applied := base.clone()
	applyV3Preset(applied, preset)
	_, cells := v3Table(applied, models)
	result := Preset{Name: preset.Name, Off: append([]string(nil), preset.Off...)}
	withoutOff := func(names []string) []string {
		kept := make([]string, 0, len(names))
		for _, name := range names {
			if !contains(preset.Off, name) {
				kept = append(kept, name)
			}
		}
		return kept
	}
	if kept := withoutOff(order); !sameOrder(kept, withoutOff(baseOrder)) {
		result.Order = kept
	}
	for _, name := range baseOrder {
		if contains(preset.Off, name) {
			continue
		}
		for _, role := range Roles {
			was, had := baseCells[name][role]
			now, has := cells[name][role]
			if had == has && was == now {
				continue
			}
			if result.Roles == nil {
				result.Roles = map[string]map[string]Cell{}
			}
			if result.Roles[name] == nil {
				result.Roles[name] = map[string]Cell{}
			}
			result.Roles[name][role] = now // the zero cell clears the base cell
		}
	}
	return result
}

// migrateStateFile converts a condition state of the v3 roster: an off
// budget becomes an off switch, the other budgets and the model
// allowlists and denials are dropped with a note, and the accesses the
// policy had at budget off are switched off. changed is false when the
// file needs no rewrite.
func migrateStateFile(path string, off []string) (string, []string, bool, error) {
	text, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if len(off) == 0 {
			return "", nil, false, nil
		}
		state := NewState(path)
		for _, access := range off {
			state.SetOff(access, time.Time{})
		}
		return state.text(), nil, true, nil
	}
	if err != nil {
		return "", nil, false, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster state: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	root, err := agents.ParseTOML(string(text))
	if err != nil {
		return "", nil, false, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", path, err), Code: clierrors.EXIT_FORMAT}
	}
	var notes []string
	removed := map[string]any{}
	for _, key := range []string{"budget", "models", "exclude"} {
		if value, ok := root[key]; ok {
			removed[key] = value
			delete(root, key)
		}
	}
	if len(removed) == 0 && len(off) == 0 {
		return "", nil, false, nil
	}
	state, err := ParseState(path, stateTextWithout(string(text), removed))
	if err != nil {
		return "", nil, false, err
	}
	if budgets, ok := removed["budget"].(map[string]any); ok {
		for _, access := range sortedKeys(budgets) {
			if budgets[access] == "off" {
				state.SetOff(access, time.Time{})
				continue
			}
			notes = append(notes, fmt.Sprintf("condition budget %s = %v dropped: version %d has no budgets, a variant reorders or clears cells instead", access, budgets[access], PolicyVersion))
		}
	}
	for _, key := range []string{"models", "exclude"} {
		if table, ok := removed[key].(map[string]any); ok {
			for _, access := range sortedKeys(table) {
				notes = append(notes, fmt.Sprintf("condition %s for %s dropped: version %d names one model per cell", key, access, PolicyVersion))
			}
		}
	}
	for _, access := range off {
		state.SetOff(access, time.Time{})
	}
	return state.text(), notes, true, nil
}

// stateTextWithout drops the tables of the removed condition keys from a
// state text, so the v4 state parser reads the rest.
func stateTextWithout(text string, removed map[string]any) string {
	var out []string
	skipping := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			name := strings.Trim(trimmed, "[] ")
			_, skipping = removed[name]
		}
		if skipping {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
