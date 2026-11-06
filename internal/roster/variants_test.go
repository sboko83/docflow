package roster

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
)

// variantsTestPolicy is the policy the variants tests merge over: one
// trusted access and one check access, so a minimal preset can name both
// an accepted and a rejected target.
const variantsTestPolicy = "version = 2\n\n" +
	"[access.codex]\nagents = [\"codex\"]\ntrusted = true\n\n" +
	"[access.zai]\nagents = [\"pi\"]\ncheck = true\n\n" +
	policyRoles

func writeVariantsFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// variantsCheckout builds a checkout root: format.FindRoot needs
// docs/docflow.md to see a project and read its variants layer.
func variantsCheckout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeVariantsFile(t, filepath.Join(root, "docs", "docflow.md"), "---\ndocflow: 3\n---\n## Queue\n\n## Backlog\n")
	return root
}

func TestVariantsPaths(t *testing.T) {
	if got, want := VariantsPath("roster.toml"), "roster.variants.toml"; got != want {
		t.Errorf("VariantsPath(roster.toml) = %q, want %q", got, want)
	}
	if got, want := VariantsPath("my.roster"), "my.roster.variants.toml"; got != want {
		t.Errorf("VariantsPath without suffix = %q, want %q", got, want)
	}
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	if got, want := VariantsPath(""), filepath.Join(config, "docflow", "roster.variants.toml"); got != want {
		t.Errorf("VariantsPath(\"\") = %q, want %q", got, want)
	}
	if got, want := ProjectVariantsPath("/repo"), filepath.Join("/repo", "docs", "roster.variants.toml"); got != want {
		t.Errorf("ProjectVariantsPath() = %q, want %q", got, want)
	}
	if got := ProjectVariantsPath(""); got != "" {
		t.Errorf("ProjectVariantsPath(\"\") = %q, want no project layer", got)
	}
}

// TestVariantsMergeLayers loads a policy with a preset in the policy, in
// the global variants file next to it and in the project variants file:
// all three merge, each keeps its source, and a state preset naming the
// project variant validates.
func TestVariantsMergeLayers(t *testing.T) {
	config := t.TempDir()
	rosterPath := filepath.Join(config, "roster.toml")
	writeVariantsFile(t, rosterPath, variantsTestPolicy+"\n[preset.base]\noff = [\"zai\"]\n")
	writeVariantsFile(t, filepath.Join(config, "roster.variants.toml"),
		"version = 2\n\n[preset.machine]\nlean = [\"zai\"]\n")
	root := variantsCheckout(t)
	writeVariantsFile(t, filepath.Join(root, "docs", "roster.variants.toml"),
		"version = 2\n\n[preset.project]\nminimal = [\"codex\"]\n")
	t.Chdir(root)

	policy, err := LoadPolicy(rosterPath)
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if got, want := policy.PresetNames(), []string{"base", "machine", "project"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PresetNames() = %v, want %v", got, want)
	}
	sources := map[string]string{"base": SourcePolicy, "machine": SourceVariants, "project": SourceProject}
	for name, want := range sources {
		preset, ok := policy.Preset(name)
		if !ok {
			t.Fatalf("Preset(%s) not found", name)
		}
		if preset.Source != want {
			t.Errorf("preset %s source = %q, want %q", name, preset.Source, want)
		}
	}
	project, _ := policy.Preset("project")
	if want := []string{"codex"}; !reflect.DeepEqual(project.Minimal, want) {
		t.Errorf("project minimal = %v, want %v", project.Minimal, want)
	}
	// A condition naming a preset of any layer is valid: --preset NAME
	// must mean the same preset everywhere.
	state := NewState("")
	state.Preset = "project"
	if err := state.Validate(policy); err != nil {
		t.Fatalf("Validate() with a project preset = %v", err)
	}
}

// TestVariantsNameConflict checks that one preset name in roster.toml and
// in the variants file is an error naming both files, not a shadowing.
func TestVariantsNameConflict(t *testing.T) {
	config := t.TempDir()
	rosterPath := filepath.Join(config, "roster.toml")
	writeVariantsFile(t, rosterPath, variantsTestPolicy+"\n[preset.low]\noff = [\"zai\"]\n")
	variantsPath := filepath.Join(config, "roster.variants.toml")
	writeVariantsFile(t, variantsPath, "version = 2\n\n[preset.low]\nlean = [\"zai\"]\n")
	t.Chdir(t.TempDir())

	_, err := LoadPolicy(rosterPath)
	var formatError clierrors.FormatError
	if !errors.As(err, &formatError) {
		t.Fatalf("LoadPolicy() error = %v (%T), want a FormatError", err, err)
	}
	if formatError.Path != variantsPath || formatError.Line != 3 {
		t.Errorf("error at %s:%d, want %s:3", formatError.Path, formatError.Line, variantsPath)
	}
	for _, path := range []string{rosterPath, variantsPath} {
		if !strings.Contains(formatError.Text, path) {
			t.Errorf("error %q does not name %s", formatError.Text, path)
		}
	}
}

// TestVariantsUnknownAccess checks that a variant preset naming an access
// the policy does not declare fails like one in roster.toml.
func TestVariantsUnknownAccess(t *testing.T) {
	config := t.TempDir()
	rosterPath := filepath.Join(config, "roster.toml")
	writeVariantsFile(t, rosterPath, variantsTestPolicy)
	variantsPath := filepath.Join(config, "roster.variants.toml")
	writeVariantsFile(t, variantsPath, "version = 2\n\n[preset.bad]\noff = [\"nope\"]\n")
	t.Chdir(t.TempDir())

	_, err := LoadPolicy(rosterPath)
	var formatError clierrors.FormatError
	if !errors.As(err, &formatError) {
		t.Fatalf("LoadPolicy() error = %v (%T), want a FormatError", err, err)
	}
	if formatError.Path != variantsPath || formatError.Line != 4 {
		t.Errorf("error at %s:%d, want %s:4", formatError.Path, formatError.Line, variantsPath)
	}
	if !strings.Contains(formatError.Text, "unknown access 'nope'") {
		t.Errorf("error %q does not name the unknown access", formatError.Text)
	}
}

// TestVariantsAbsentLayers: without both variants files the policy loads
// its own presets alone; a variants file outside a checkout is not a
// project layer.
func TestVariantsAbsentLayers(t *testing.T) {
	config := t.TempDir()
	rosterPath := filepath.Join(config, "roster.toml")
	writeVariantsFile(t, rosterPath, variantsTestPolicy+"\n[preset.base]\noff = [\"zai\"]\n")
	t.Chdir(t.TempDir())

	policy, err := LoadPolicy(rosterPath)
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if got, want := policy.PresetNames(), []string{"base"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PresetNames() = %v, want %v", got, want)
	}
	if policy.Presets[0].Source != SourcePolicy {
		t.Errorf("base source = %q, want %q", policy.Presets[0].Source, SourcePolicy)
	}

	dir := t.TempDir()
	writeVariantsFile(t, filepath.Join(dir, "docs", "roster.variants.toml"),
		"version = 2\n\n[preset.stray]\noff = [\"zai\"]\n")
	t.Chdir(dir) // no docs/docflow.md: not a checkout
	policy, err = LoadPolicy(rosterPath)
	if err != nil {
		t.Fatalf("LoadPolicy() outside a checkout = %v", err)
	}
	if _, ok := policy.Preset("stray"); ok {
		t.Fatal("a variants file outside a checkout became the project layer")
	}
}

// TestVariantsSameFileInBothLayers: a policy kept inside docs/ shares its
// variants file with the project layer; the loader reads it once instead
// of calling the file a duplicate of itself.
func TestVariantsSameFileInBothLayers(t *testing.T) {
	root := variantsCheckout(t)
	rosterPath := filepath.Join(root, "docs", "roster.toml")
	writeVariantsFile(t, rosterPath, variantsTestPolicy)
	writeVariantsFile(t, filepath.Join(root, "docs", "roster.variants.toml"),
		"version = 2\n\n[preset.shared]\noff = [\"zai\"]\n")
	t.Chdir(root)

	policy, err := LoadPolicy(rosterPath)
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if got, want := policy.PresetNames(), []string{"shared"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PresetNames() = %v, want %v", got, want)
	}
	if preset, _ := policy.Preset("shared"); preset.Source != SourceVariants {
		t.Errorf("shared source = %q, want %q", preset.Source, SourceVariants)
	}
}

// TestVariantsRejectForeignKeys keeps a variants file to its format:
// version = 2 and [preset.NAME] tables only. An unknown key names file and
// line instead of being ignored.
func TestVariantsRejectForeignKeys(t *testing.T) {
	policy, err := ParsePolicy("roster.toml", variantsTestPolicy)
	if err != nil {
		t.Fatalf("ParsePolicy() error = %v", err)
	}
	cases := []struct {
		name string
		text string
		want string
	}{
		{"top-level table", "version = 2\n\n[access.codex]\nagents = [\"codex\"]\n", "unknown key 'access'"},
		{"version", "version = 1\n\n[preset.x]\noff = [\"zai\"]\n", "version must be 2"},
		{"preset key", "version = 2\n\n[preset.x]\ntrusted = true\n", "unknown key 'trusted'"},
		{"inline preset", "version = 2\n\n[preset]\nx = { off = [\"zai\"] }\n", "preset x must be a [preset.x] table"},
		{"minimal untrusted", "version = 2\n\n[preset.x]\nminimal = [\"zai\"]\n", "minimal access 'zai' is not trusted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseVariantPresets(policy, "roster.variants.toml", tc.text, SourceVariants, nil)
			var formatError clierrors.FormatError
			if !errors.As(err, &formatError) {
				t.Fatalf("error = %v (%T), want a FormatError", err, err)
			}
			if formatError.Path != "roster.variants.toml" {
				t.Errorf("error path = %q, want roster.variants.toml", formatError.Path)
			}
			if !strings.Contains(formatError.Text, tc.want) {
				t.Errorf("error %q does not contain %q", formatError.Text, tc.want)
			}
		})
	}
}

// TestSaveVariantsRoundTrip writes the whole file and loads it back: the
// machine file keeps every condition, an empty models list means "no
// model" and survives the round trip.
func TestSaveVariantsRoundTrip(t *testing.T) {
	config := t.TempDir()
	rosterPath := filepath.Join(config, "roster.toml")
	writeVariantsFile(t, rosterPath, variantsTestPolicy)
	path := filepath.Join(config, "roster.variants.toml")
	presets := []Preset{
		{Name: "second", Off: []string{"zai"}, Order: []string{"codex", "zai"}},
		{Name: "first", Lean: []string{"zai"}, Models: map[string][]string{"zai": {"*flash*"}}},
		{Name: "none-models", Models: map[string][]string{"codex": {}}},
	}
	if err := SaveVariants(path, presets); err != nil {
		t.Fatalf("SaveVariants() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "version = 2\n") {
		t.Errorf("written file has no version marker:\n%s", data)
	}
	t.Chdir(t.TempDir())
	policy, err := LoadPolicy(rosterPath)
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if got, want := policy.PresetNames(), []string{"first", "none-models", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PresetNames() = %v, want the written name order %v", got, want)
	}
	first, _ := policy.Preset("first")
	if first.Source != SourceVariants {
		t.Errorf("first source = %q, want %q", first.Source, SourceVariants)
	}
	if want := map[string][]string{"zai": {"*flash*"}}; !reflect.DeepEqual(first.Models, want) {
		t.Errorf("first models = %v, want %v", first.Models, want)
	}
	none, _ := policy.Preset("none-models")
	if globs, ok := none.Models["codex"]; !ok || len(globs) != 0 {
		t.Errorf("none-models codex = %v (present %v), want an empty list", globs, ok)
	}
}

// TestSaveVariantsRollback: a file the parser rejects never replaces the
// target and leaves no temporary file behind.
func TestSaveVariantsRollback(t *testing.T) {
	cases := []struct {
		name    string
		presets []Preset
	}{
		{"duplicate table", []Preset{
			{Name: "same", Off: []string{"zai"}},
			{Name: "same", Lean: []string{"zai"}},
		}},
		// A name the header grammar cannot carry would reload as another
		// name: the round trip rejects it instead of writing a file it
		// cannot read back.
		{"unreadable preset name", []Preset{{Name: `bad"name`, Off: []string{"zai"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "roster.variants.toml")
			original := "# hand comment\nversion = 2\n\n[preset.keep]\noff = [\"zai\"]\n"
			writeVariantsFile(t, path, original)
			if err := SaveVariants(path, tc.presets); err == nil {
				t.Fatalf("SaveVariants() = nil error, want %s to fail", tc.name)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != original {
				t.Fatalf("target after a failed write:\n%s\nwant the original:\n%s", data, original)
			}
			if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
				t.Fatalf("temporary file left behind: %v", err)
			}
		})
	}
}
