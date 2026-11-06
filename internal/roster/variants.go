package roster

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/format"
)

// VariantsVersion is the format marker of a roster variants file: a
// top-level version = 2 and only [preset.NAME] tables. A variants file is
// machine-owned: the workspace writes it whole, so comments and a hand
// layout do not survive. roster.toml stays hand-edited and is never
// rewritten.
const VariantsVersion = 2

// Preset sources: the stable policy file, the global variants file named
// next to it and the project variants file of a checkout.
const (
	SourceVariants = "variants"
	SourceProject  = "project"
)

// VariantsPath returns the global variants path of a roster: the same
// directory and name with .toml replaced by .variants.toml, so
// ~/.config/docflow/roster.toml has roster.variants.toml next to it.
func VariantsPath(rosterPath string) string {
	policy := PolicyPath(rosterPath)
	if base, found := strings.CutSuffix(policy, ".toml"); found {
		return base + ".variants.toml"
	}
	return policy + ".variants.toml"
}

// ProjectVariantsPath returns the variants file of a checkout:
// docs/roster.variants.toml, committed with the project. An empty root
// has no project layer.
func ProjectVariantsPath(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, "docs", "roster.variants.toml")
}

// ProjectRoot returns the docflow checkout that holds the working
// directory, or "" outside one: the project variants layer belongs to a
// checkout, and a command started outside it sees no project presets.
func ProjectRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	root, err := format.FindRoot(cwd)
	if err != nil {
		return ""
	}
	return root
}

// mergeVariants merges the preset variants into a loaded policy: the
// global file next to the policy first, then the project file of the
// given checkout root ("" means no project layer). A missing file is an
// empty layer. A name an earlier layer already defines is a policy error
// naming both files, not a silent shadowing: --preset NAME must mean one
// preset everywhere.
func mergeVariants(policy *Policy, root string) (*Policy, error) {
	origins := make(map[string]string, len(policy.Presets))
	for _, preset := range policy.Presets {
		origins[preset.Name] = policy.Path
	}
	layers := []struct {
		path   string
		source string
	}{
		{VariantsPath(policy.Path), SourceVariants},
		{ProjectVariantsPath(root), SourceProject},
	}
	layerPaths := make([]string, 0, len(layers))
	for _, layer := range layers {
		if layer.path == "" {
			continue
		}
		// A policy kept inside docs/ or a symlinked checkout can make the
		// global and the project layer one file: reading it twice would
		// report the file as a duplicate of itself.
		seen := false
		for _, path := range layerPaths {
			if samePath(path, layer.path) {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		layerPaths = append(layerPaths, layer.path)
		text, err := os.ReadFile(layer.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster variants: %v", err), Code: clierrors.EXIT_FORMAT}
		}
		presets, err := parseVariantPresets(policy, layer.path, string(text), layer.source, origins)
		if err != nil {
			return nil, err
		}
		for _, preset := range presets {
			origins[preset.Name] = layer.path
			policy.Presets = append(policy.Presets, preset)
		}
	}
	return policy, nil
}

// samePath reports whether two paths name the same file: paths reach the
// loader both relative and absolute, and a checkout may be symlinked.
func samePath(left, right string) bool {
	if left == right {
		return true
	}
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

// parseVariantPresets reads a variants file against the policy it merges
// over: version = 2 and only [preset.NAME] tables of the roster.toml
// grammar. origins holds the preset names the earlier layers define and
// the files they come from.
func parseVariantPresets(policy *Policy, path, text, source string, origins map[string]string) ([]Preset, error) {
	if name, line, duplicated := repeatedTable(text); duplicated {
		return nil, clierrors.FormatError{Path: path, Line: line, Text: fmt.Sprintf("table [%s] is defined twice", name)}
	}
	root, err := agents.ParseTOML(text)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", path, err), Code: clierrors.EXIT_FORMAT}
	}
	index := scanLines(text)
	version, ok := root["version"].(int64)
	if !ok || version != VariantsVersion {
		return nil, failAt(path, index, "", "version", "version must be %d", VariantsVersion)
	}
	for _, key := range index.keysByLine("", root) {
		switch key {
		case "version", "preset":
		default:
			return nil, failAt(path, index, "", key, "unknown key '%s': a variants file holds only [preset.NAME] tables", key)
		}
	}
	value := root["preset"]
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return nil, failAt(path, index, "", "preset", "preset must be a table of [preset.<name>] sections")
	}
	headers := presetHeaders(text)
	presets := make([]Preset, 0, len(table))
	for _, name := range index.keysByLine("preset", table) {
		if !headers[name] {
			return nil, failAt(path, index, "preset", name, "preset %s must be a [preset.%s] table, not a key", name, name)
		}
		if previous, clash := origins[name]; clash {
			return nil, failAt(path, index, "preset."+name, "", "preset %s is defined in both %s and %s", name, previous, path)
		}
		section, ok := table[name].(map[string]any)
		if !ok {
			return nil, failAt(path, index, "preset", name, "preset %s must be a [preset.%s] table", name, name)
		}
		preset, err := parsePresetSection(policy, path, index, name, section)
		if err != nil {
			return nil, err
		}
		preset.Source = source
		presets = append(presets, preset)
	}
	return presets, nil
}

// presetHeaders collects the names of the [preset.NAME] table headers of
// a variants file. A preset held in an inline table or a dotted key is
// rejected: the file describes preset tables, not keys.
func presetHeaders(text string) map[string]bool {
	headers := make(map[string]bool)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "[") || strings.HasPrefix(line, "[[") {
			continue
		}
		end := strings.Index(line, "]")
		if end < 0 {
			continue
		}
		segments := headerSegments(line[1:end])
		if len(segments) == 2 && segments[0] == "preset" {
			headers[segments[1]] = true
		}
	}
	return headers
}

// SaveVariants writes a whole variants file atomically: the presets are
// rendered in name order, a temporary file next to the target is written
// and read back by the variants parser, and only a file that reads back as
// the presets it was given replaces the target. Comments and a previous
// hand layout are not preserved. A parse or write error leaves the target
// as it was and removes the temporary file.
func SaveVariants(path string, presets []Preset) error {
	if path == "" {
		return clierrors.DocflowError{Message: "docflow: roster variants: path is empty", Code: clierrors.EXIT_FORMAT}
	}
	path = expandUser(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster variants: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(variantsText(presets)), 0o644); err != nil {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster variants: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	written, err := os.ReadFile(temporary)
	var parsed []Preset
	if err == nil {
		// A nil policy checks the grammar only: the access names a preset
		// refers to are checked when the layer is loaded over a policy.
		parsed, err = parseVariantPresets(nil, path, string(written), SourceVariants, nil)
	}
	if err == nil && variantsText(parsed) != string(written) {
		err = clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: the written file does not read back as the presets it was given", path), Code: clierrors.EXIT_FORMAT}
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster variants: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return nil
}

// variantsText renders the presets of a variants file: the version and one
// [preset.NAME] table per preset in name order. Empty conditions are
// dropped; a models entry with an empty list is kept, it allows no model.
func variantsText(presets []Preset) string {
	ordered := append([]Preset(nil), presets...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	var text strings.Builder
	text.WriteString("# roster variants: presets created in docflow tui; this file is machine-owned.\n")
	fmt.Fprintf(&text, "version = %d\n", VariantsVersion)
	for _, preset := range ordered {
		fmt.Fprintf(&text, "\n[preset.%s]\n", tomlKey(preset.Name))
		writeNameList(&text, "off", preset.Off)
		writeNameList(&text, "lean", preset.Lean)
		writeNameList(&text, "minimal", preset.Minimal)
		writeNameList(&text, "prefer", preset.Prefer)
		writeNameList(&text, "order", preset.Order)
		writeModelTable(&text, preset.Models)
	}
	return text.String()
}

func writeNameList(text *strings.Builder, key string, names []string) {
	if len(names) == 0 {
		return
	}
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, quote(name))
	}
	fmt.Fprintf(text, "%s = [%s]\n", key, strings.Join(quoted, ", "))
}

func writeModelTable(text *strings.Builder, models map[string][]string) {
	if len(models) == 0 {
		return
	}
	entries := make([]string, 0, len(models))
	for _, access := range sortedKeys(models) {
		quoted := make([]string, 0, len(models[access]))
		for _, glob := range models[access] {
			quoted = append(quoted, quote(glob))
		}
		entries = append(entries, tomlKey(access)+" = ["+strings.Join(quoted, ", ")+"]")
	}
	fmt.Fprintf(text, "models = { %s }\n", strings.Join(entries, ", "))
}
