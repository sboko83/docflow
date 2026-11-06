package roster

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/clierrors"
)

// The workspace tests read the v2 fixture policy and the scenario catalog:
// the same pair the brief and apply tests use, so a model row and a role
// chain can be compared with the documented brief.

// workspacePolicy writes the v2 fixture with the zai expiry pushed past the
// test clock as a roster of its own and returns its path.
func workspacePolicy(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/policy-v2.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), `expires = "2026-06-30"`, `expires = "2099-01-01"`, 1)
	if text == string(data) {
		t.Fatal("policy-v2.toml no longer names the zai expiry the test patches")
	}
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// workspaceCatalog installs the scenario catalog where LoadCatalog("")
// reads it.
func workspaceCatalog(t *testing.T) {
	t.Helper()
	catalog, err := LoadCatalog("testdata/catalog-scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if err := catalog.Save(filepath.Join(cache, "docflow", "roster", "catalog.json")); err != nil {
		t.Fatal(err)
	}
}

// writeRosterFile writes text at path, creating the directory it needs.
func writeRosterFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadWorkspaceOrFail loads the workspace of a roster and fails with the
// loader error.
func loadWorkspaceOrFail(t *testing.T, rosterPath, root string) *Workspace {
	t.Helper()
	workspace, err := LoadWorkspace(rosterPath, root)
	if err != nil {
		t.Fatalf("LoadWorkspace() error = %v", err)
	}
	return workspace
}

// accessRow finds one access row of the model.
func accessRow(t *testing.T, workspace *Workspace, name string) WorkspaceAccess {
	t.Helper()
	for _, row := range workspace.Accesses {
		if row.Access == name {
			return row
		}
	}
	t.Fatalf("no access row for %q in %+v", name, workspace.Accesses)
	return WorkspaceAccess{}
}

// conditionNames renders the conditions of an access as "kind value source"
// lines, so a test compares the whole set instead of one field.
func conditionNames(conditions []Condition) []string {
	lines := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		lines = append(lines, condition.Kind+" "+condition.Value+" "+condition.Source)
	}
	return lines
}

// TestLoadWorkspaceModel reads the roster, the state, the catalog and the
// preset layers into one model: the access rows, the variants with their
// source, the active preset, the brief chains and the warnings.
func TestLoadWorkspaceModel(t *testing.T) {
	config := t.TempDir()
	rosterPath := filepath.Join(config, "roster.toml")
	data, err := os.ReadFile("testdata/policy-v2.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), `expires = "2026-06-30"`, `expires = "2099-01-01"`, 1)
	writeRosterFile(t, rosterPath, text)
	writeRosterFile(t, filepath.Join(config, "roster.variants.toml"), "version = 2\n\n[preset.machine]\nlean = [\"zai\"]\n")
	root := variantsCheckout(t)
	writeRosterFile(t, filepath.Join(root, "docs", "roster.variants.toml"), "version = 2\n\n[preset.project]\nminimal = [\"codex\"]\n")
	statePath := StatePath(rosterPath)
	writeRosterFile(t, statePath, "version = 2\n\npreset = \"low\"\n\n[models]\nopencode-go = [\"deepseek-v4.1\"]\n")
	workspaceCatalog(t)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	workspace := loadWorkspaceOrFail(t, rosterPath, root)
	if workspace.Missing || workspace.Legacy || workspace.CatalogEmpty {
		t.Fatalf("flags = missing %v legacy %v empty catalog %v", workspace.Missing, workspace.Legacy, workspace.CatalogEmpty)
	}
	if workspace.Problem != "" {
		t.Fatalf("Problem = %q, want the brief to build", workspace.Problem)
	}
	if workspace.Preset != "low" {
		t.Errorf("Preset = %q, want low", workspace.Preset)
	}
	if got, want := len(workspace.Accesses), len(workspace.Policy.Accesses); got != want {
		t.Fatalf("access rows = %d, want one per policy access (%d)", got, want)
	}

	opencode := accessRow(t, workspace, "opencode-go")
	if want := []string{"pi", "opencode"}; !reflect.DeepEqual(opencode.Agents, want) {
		t.Errorf("opencode-go agents = %v, want %v", opencode.Agents, want)
	}
	if opencode.Kind != "subscription" || opencode.Auth != "ready" || opencode.Models != 2 {
		t.Errorf("opencode-go catalog row = %+v, want the scenario subscription with 2 models", opencode)
	}
	if opencode.Budget != BudgetPrefer || !opencode.Trusted || opencode.Check || opencode.Off {
		t.Errorf("opencode-go flags = %+v, want prefer, trusted and on", opencode)
	}
	want := []string{"budget prefer roster", "models deepseek-* roster", "models deepseek-v4.1 state"}
	if got := conditionNames(opencode.Conditions); !reflect.DeepEqual(got, want) {
		t.Errorf("opencode-go conditions = %v, want %v", got, want)
	}

	zai := accessRow(t, workspace, "zai")
	if zai.Budget != BudgetLean || !zai.Check || zai.Trusted {
		t.Errorf("zai flags = %+v, want lean and check", zai)
	}
	want = []string{"budget lean roster", "expires 2099-01-01 roster", "models glm-5.3* roster", "exclude glm-5.3-thinking roster"}
	if got := conditionNames(zai.Conditions); !reflect.DeepEqual(got, want) {
		t.Errorf("zai conditions = %v, want %v", got, want)
	}

	// The preset switches local off in memory: the row shows the effective
	// budget a run would use, not the one the policy file carries.
	local := accessRow(t, workspace, "local")
	if !local.Off || local.Budget != BudgetOff || !local.Check {
		t.Errorf("local row = %+v, want the preset off and the check flag", local)
	}

	wantVariants := []WorkspaceVariant{
		{Name: "low", Source: SourcePolicy, Path: rosterPath},
		{Name: "machine", Source: SourceVariants, Path: filepath.Join(config, "roster.variants.toml")},
		{Name: "project", Source: SourceProject, Path: ProjectVariantsPath(root)},
	}
	if !reflect.DeepEqual(workspace.Variants, wantVariants) {
		t.Errorf("variants = %+v, want %+v", workspace.Variants, wantVariants)
	}

	if len(workspace.Roles) != len(Roles) {
		t.Fatalf("roles = %d, want the %d plan roles", len(workspace.Roles), len(Roles))
	}
	executor := workspace.Roles[0]
	if executor.Role != "executor" || len(executor.Routes) == 0 || executor.Routes[0].Access != "opencode-go" {
		t.Fatalf("executor brief = %+v, want the opencode-go primary", executor)
	}
	if executor.Budget == nil || executor.Budget.Target != 250000 {
		t.Errorf("executor budget = %+v, want 250000 for the scenario", executor.Budget)
	}
	if len(executor.Why) == 0 {
		t.Error("executor brief has no why lines")
	}
	heavy := workspace.Roles[2]
	if heavy.Role != "heavy" || len(heavy.Routes) != 0 {
		t.Errorf("heavy brief = %+v, want an empty chain", heavy)
	}
	if !contains(workspace.Warnings, "no routes for heavy") {
		t.Errorf("warnings = %v, want the empty heavy chain", workspace.Warnings)
	}
	if !contains(conditionNames(workspace.Conditions), "preset low state") {
		t.Errorf("conditions = %v, want the active preset row", conditionNames(workspace.Conditions))
	}

	// Loading is a read: the condition file keeps its text until the next
	// mutation rewrites it.
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("LoadWorkspace rewrote %s", statePath)
	}
}

// TestLoadWorkspaceExpiredOff: an off whose until day has passed is gone
// from the model, while the file keeps it for the next mutation.
func TestLoadWorkspaceExpiredOff(t *testing.T) {
	rosterPath := workspacePolicy(t)
	statePath := StatePath(rosterPath)
	text := "version = 2\n\noff = [\"codex\"]\n\n[until]\ncodex = \"2020-01-01\"\n"
	writeRosterFile(t, statePath, text)
	workspaceCatalog(t)

	workspace := loadWorkspaceOrFail(t, rosterPath, "")
	if len(workspace.State.Off) != 0 {
		t.Fatalf("off = %v, want the expired switch dropped", workspace.State.Off)
	}
	if codex := accessRow(t, workspace, "codex"); codex.Off {
		t.Errorf("codex row = %+v, want it on with the switch expired", codex)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != text {
		t.Errorf("the expired off was rewritten on a read:\n%s", after)
	}
}

// TestLoadWorkspaceMissingRoster: a roster that is not there is a hint,
// not an error; the catalog still loads and every write refuses.
func TestLoadWorkspaceMissingRoster(t *testing.T) {
	workspaceCatalog(t)
	rosterPath := filepath.Join(t.TempDir(), "roster.toml")
	workspace := loadWorkspaceOrFail(t, rosterPath, "")
	if !workspace.Missing || workspace.Policy != nil {
		t.Fatalf("missing roster = %+v, want the missing flag and no policy", workspace)
	}
	if workspace.CatalogEmpty {
		t.Error("the catalog did not load next to a missing roster")
	}
	err := workspace.SetOff("claude", time.Time{})
	if err == nil || !strings.Contains(err.Error(), rosterPath) {
		t.Fatalf("SetOff() error = %v, want the missing roster path", err)
	}
	if _, statErr := os.Stat(StatePath(rosterPath)); !os.IsNotExist(statErr) {
		t.Fatalf("a refused write created the state file: %v", statErr)
	}
}

// TestLoadWorkspaceLegacyRoster: a version 1 roster is a flag with the
// migration hint, never a panic or a half-read model.
func TestLoadWorkspaceLegacyRoster(t *testing.T) {
	data, err := os.ReadFile("testdata/legacy-roster.toml")
	if err != nil {
		t.Fatal(err)
	}
	rosterPath := filepath.Join(t.TempDir(), "roster.toml")
	writeRosterFile(t, rosterPath, string(data))
	workspaceCatalog(t)

	workspace := loadWorkspaceOrFail(t, rosterPath, "")
	if !workspace.Legacy || workspace.Policy != nil {
		t.Fatalf("legacy roster = %+v, want the legacy flag and no policy", workspace)
	}
	if !strings.Contains(workspace.Problem, "docflow roster migrate") {
		t.Errorf("Problem = %q, want the migration hint", workspace.Problem)
	}
	if len(workspace.Accesses) != 0 {
		t.Errorf("accesses = %+v, want none before the migration", workspace.Accesses)
	}
	// The condition commands keep failing on a v1 file with the same error
	// the CLI printed before the workspace existed.
	if _, err := LoadPolicyState(rosterPath, ""); err == nil || !strings.Contains(err.Error(), "roster migrate") {
		t.Fatalf("LoadPolicyState() error = %v, want the migration hint", err)
	}
}

// TestLoadWorkspaceEmptyCatalog: without the cache the chains are empty
// and the screen says so with its own flag.
func TestLoadWorkspaceEmptyCatalog(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	workspace := loadWorkspaceOrFail(t, workspacePolicy(t), "")
	if !workspace.CatalogEmpty {
		t.Fatal("CatalogEmpty = false, want the flag without a cache")
	}
	if len(workspace.Roles) != len(Roles) {
		t.Fatalf("roles = %d, want every role with an empty chain", len(workspace.Roles))
	}
	for _, role := range workspace.Roles {
		if len(role.Routes) != 0 {
			t.Errorf("role %s has routes without a catalog: %+v", role.Role, role.Routes)
		}
	}
	if !contains(workspace.Warnings, "no routes for executor") {
		t.Errorf("warnings = %v, want the empty chains named", workspace.Warnings)
	}
}

// TestLoadWorkspaceBrokenBrief: a check access nobody can review is a
// problem of the model, not a failed load — the screen must show the
// accesses to fix it.
func TestLoadWorkspaceBrokenBrief(t *testing.T) {
	rosterPath := workspacePolicy(t)
	state := "version = 2\n\noff = [\"opencode-go\", \"codex\", \"claude\", \"opencode\", \"local\"]\n"
	writeRosterFile(t, StatePath(rosterPath), state)
	workspaceCatalog(t)

	workspace := loadWorkspaceOrFail(t, rosterPath, "")
	if !strings.Contains(workspace.Problem, "no independent reviewer") {
		t.Fatalf("Problem = %q, want the missing reviewer", workspace.Problem)
	}
	if len(workspace.Accesses) != len(workspace.Policy.Accesses) {
		t.Errorf("accesses = %d, want the rows to survive a broken brief", len(workspace.Accesses))
	}
	if len(workspace.Roles) != 0 {
		t.Errorf("roles = %+v, want none from a broken brief", workspace.Roles)
	}
}

// TestWorkspaceOperationsRefuseUnknownAccess: every condition operation
// checks the access against the policy before it writes.
func TestWorkspaceOperationsRefuseUnknownAccess(t *testing.T) {
	rosterPath := workspacePolicy(t)
	statePath := StatePath(rosterPath)
	before := "version = 2\n\noff = [\"local\"]\n"
	writeRosterFile(t, statePath, before)
	workspaceCatalog(t)
	workspace := loadWorkspaceOrFail(t, rosterPath, "")

	operations := []struct {
		name string
		run  func() error
		want string
	}{
		{"off", func() error { return workspace.SetOff("nope", time.Time{}) }, "known: opencode-go"},
		{"on", func() error { return workspace.SetOn("nope") }, "known: opencode-go"},
		{"budget", func() error { return workspace.SetBudget("nope", BudgetLean) }, "known: opencode-go"},
		{"expires", func() error { return workspace.SetExpires("nope", time.Now().UTC().AddDate(0, 0, 7)) }, "known: opencode-go"},
		{"allow", func() error { return workspace.Allow("nope", []string{"*flash*"}) }, "known: opencode-go"},
		{"deny", func() error { return workspace.Deny("nope", []string{"*thinking*"}) }, "known: opencode-go"},
		{"preset", func() error { return workspace.SetPreset("nope") }, "known: low"},
	}
	for _, operation := range operations {
		err := operation.run()
		if err == nil {
			t.Errorf("%s accepted an unknown name", operation.name)
			continue
		}
		if !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), operation.want) {
			t.Errorf("%s error = %v, want the unknown name and %q", operation.name, err, operation.want)
		}
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Errorf("a refused operation rewrote the state:\n%s", after)
	}
}

// TestWorkspaceMinimalNeedsTrusted: the budget follows the policy rule, and
// a refused level leaves the file as it was.
func TestWorkspaceMinimalNeedsTrusted(t *testing.T) {
	rosterPath := workspacePolicy(t)
	statePath := StatePath(rosterPath)
	workspaceCatalog(t)
	workspace := loadWorkspaceOrFail(t, rosterPath, "")

	err := workspace.SetBudget("zai", BudgetMinimal)
	if err == nil || !strings.Contains(err.Error(), "minimal requires trusted") {
		t.Fatalf("SetBudget(zai, minimal) error = %v, want the trusted refusal", err)
	}
	if _, statErr := os.Stat(statePath); !os.IsNotExist(statErr) {
		t.Fatalf("a refused budget created the state file: %v", statErr)
	}
	if err := workspace.SetBudget("codex", BudgetMinimal); err != nil {
		t.Fatalf("SetBudget(codex, minimal) error = %v, want it accepted on a trusted access", err)
	}
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Budget["codex"] != BudgetMinimal {
		t.Errorf("budget = %v, want codex minimal", state.Budget)
	}
}

// TestWorkspaceConditionOperations: off with a date, on, budget, expiry,
// allow, deny, preset selection and reset each write the state file.
func TestWorkspaceConditionOperations(t *testing.T) {
	rosterPath := workspacePolicy(t)
	statePath := StatePath(rosterPath)
	workspaceCatalog(t)
	workspace := loadWorkspaceOrFail(t, rosterPath, "")
	day := time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC)

	read := func() *State {
		t.Helper()
		state, err := LoadState(statePath)
		if err != nil {
			t.Fatalf("LoadState() error = %v", err)
		}
		return state
	}

	if err := workspace.SetOff("claude", day); err != nil {
		t.Fatalf("SetOff() error = %v", err)
	}
	if state := read(); !contains(state.Off, "claude") || !state.Until["claude"].Equal(day) {
		t.Fatalf("off = %v until %v, want claude until %s", state.Off, state.Until, dayText(day))
	}
	if err := workspace.SetOn("claude"); err != nil {
		t.Fatalf("SetOn() error = %v", err)
	}
	if state := read(); len(state.Off) != 0 || len(state.Until) != 0 {
		t.Fatalf("on left %v %v", state.Off, state.Until)
	}
	if err := workspace.SetBudget("opencode-go", BudgetLean); err != nil {
		t.Fatalf("SetBudget() error = %v", err)
	}
	if err := workspace.SetExpires("zai", day); err != nil {
		t.Fatalf("SetExpires() error = %v", err)
	}
	if err := workspace.Allow("zai", []string{"*flash*"}); err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if err := workspace.Deny("zai", []string{"glm-5.3-thinking"}); err != nil {
		t.Fatalf("Deny() error = %v", err)
	}
	state := read()
	if state.Budget["opencode-go"] != BudgetLean {
		t.Errorf("budget = %v, want opencode-go lean", state.Budget)
	}
	if !state.Expires["zai"].Equal(day) {
		t.Errorf("expires = %v, want zai %s", state.Expires, dayText(day))
	}
	if want := []string{"*flash*"}; !reflect.DeepEqual(state.Models["zai"], want) {
		t.Errorf("models = %v, want %v", state.Models, want)
	}
	if want := []string{"glm-5.3-thinking"}; !reflect.DeepEqual(state.Exclude["zai"], want) {
		t.Errorf("exclude = %v, want %v", state.Exclude, want)
	}

	if err := workspace.SetPreset("low"); err != nil {
		t.Fatalf("SetPreset() error = %v", err)
	}
	if state := read(); state.Preset != "low" || workspace.Preset != "low" {
		t.Fatalf("preset = %q (%q), want low", state.Preset, workspace.Preset)
	}
	if err := workspace.ClearPreset(); err != nil {
		t.Fatalf("ClearPreset() error = %v", err)
	}
	if state := read(); state.Preset != "" || len(state.Budget) == 0 {
		t.Fatalf("clearing the preset dropped other conditions: %+v", state)
	}
	if err := workspace.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("Reset() left the state file: %v", err)
	}
	if workspace.State == nil || len(workspace.State.Off) != 0 || workspace.Preset != "" {
		t.Errorf("Reset() left the model conditions: %+v", workspace.State)
	}
}

// TestWorkspaceResetWithoutPolicy: reset reads no policy, so a version 1
// roster can be dropped from its conditions too.
func TestWorkspaceResetWithoutPolicy(t *testing.T) {
	rosterPath := filepath.Join(t.TempDir(), "roster.toml")
	writeRosterFile(t, rosterPath, "spend = [\"zai\"]\n")
	statePath := StatePath(rosterPath)
	writeRosterFile(t, statePath, "version = 2\n\noff = [\"zai\"]\n")

	workspace := &Workspace{StatePath: statePath}
	if err := workspace.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("Reset() left the state file: %v", err)
	}
}

// TestWorkspaceSaveVariantGlobal: the conditions in force become a preset
// in the machine-owned global file, and --preset finds it there.
func TestWorkspaceSaveVariantGlobal(t *testing.T) {
	rosterPath := workspacePolicy(t)
	workspaceCatalog(t)
	workspace := loadWorkspaceOrFail(t, rosterPath, "")
	// The conditions of the screen: one access off, one budget override and
	// a narrowed allowlist. The access is codex, whose policy budget is not
	// off already, or the variant would carry no difference.
	for _, step := range []func() error{
		func() error { return workspace.SetOff("codex", time.Time{}) },
		func() error { return workspace.SetBudget("opencode-go", BudgetLean) },
		func() error { return workspace.Allow("zai", []string{"*flash*"}) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if err := workspace.SaveVariant("saved", false); err != nil {
		t.Fatalf("SaveVariant() error = %v", err)
	}
	variantsPath := VariantsPath(rosterPath)
	if _, err := os.Stat(variantsPath); err != nil {
		t.Fatalf("SaveVariant() wrote no file: %v", err)
	}
	policy, err := LoadPolicy(rosterPath)
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	preset, ok := policy.Preset("saved")
	if !ok {
		t.Fatalf("the saved variant is not a preset of the policy: %v", policy.PresetNames())
	}
	if preset.Source != SourceVariants {
		t.Errorf("saved source = %q, want %q", preset.Source, SourceVariants)
	}
	if want := []string{"codex"}; !reflect.DeepEqual(preset.Off, want) {
		t.Errorf("saved off = %v, want %v", preset.Off, want)
	}
	if want := []string{"opencode-go"}; !reflect.DeepEqual(preset.Lean, want) {
		t.Errorf("saved lean = %v, want %v", preset.Lean, want)
	}
	if want := []string{"*flash*"}; !reflect.DeepEqual(preset.Models["zai"], want) {
		t.Errorf("saved models = %v, want zai %v", preset.Models, want)
	}
	// The --preset path of brief and apply: a state naming the variant
	// validates against the merged policy.
	state := NewState("")
	state.Preset = "saved"
	if err := state.Validate(policy); err != nil {
		t.Fatalf("a state naming the saved variant = %v", err)
	}
	catalog, err := LoadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	brief, err := BuildBrief(policy, state, catalog, nil)
	if err != nil {
		t.Fatalf("BuildBrief() with the saved variant = %v", err)
	}
	if len(brief.Roles[0].Routes) == 0 || brief.Roles[0].Routes[0].Access != "opencode-go" {
		t.Errorf("executor chain with the saved variant = %+v, want opencode-go", brief.Roles[0].Routes)
	}
}

// TestWorkspaceSaveVariantProject: the project file receives the variant
// too, and the name stays unique across the layers.
func TestWorkspaceSaveVariantProject(t *testing.T) {
	rosterPath := workspacePolicy(t)
	// The project variants file is written under the checkout lock, and the
	// lock lives in the git directory: the project has to be a repository.
	root := applyProject(t, applyPlan())
	workspaceCatalog(t)
	workspace := loadWorkspaceOrFail(t, rosterPath, root)
	if err := workspace.SetOff("codex", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveVariant("project-low", true); err != nil {
		t.Fatalf("SaveVariant(project) error = %v", err)
	}
	path := ProjectVariantsPath(root)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("SaveVariant(project) wrote no file: %v", err)
	}
	// The project layer belongs to the checkout of the model, not to the
	// working directory of the test.
	policy, err := loadPolicyAt(rosterPath, root)
	if err != nil {
		t.Fatalf("loadPolicyAt() error = %v", err)
	}
	preset, ok := policy.Preset("project-low")
	if !ok || preset.Source != SourceProject {
		t.Fatalf("project preset = %+v (%v), want a project variant", preset, ok)
	}
	if err := workspace.SaveVariant("project-low", true); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("second SaveVariant() error = %v, want the name conflict naming %s", err, path)
	}
	if err := workspace.SaveVariant("low", true); err == nil || !strings.Contains(err.Error(), "roster.toml") {
		t.Fatalf("SaveVariant(low) error = %v, want the roster.toml conflict", err)
	}
	if err := workspace.SaveVariant("two words", false); err == nil || !strings.Contains(err.Error(), "single word") {
		t.Fatalf("SaveVariant(two words) error = %v, want the name rule", err)
	}
}

// TestWorkspaceSaveVariantRefusesInexpressible: a condition a preset cannot
// carry is an error, not a variant that means something else.
func TestWorkspaceSaveVariantRefusesInexpressible(t *testing.T) {
	setups := []struct {
		name   string
		state  string
		result string
	}{
		{"dated off", "version = 2\n\noff = [\"claude\"]\n\n[until]\nclaude = \"2099-12-31\"\n", "a preset has no dates"},
		{"expiry", "version = 2\n\n[expires]\nzai = \"2099-12-31\"\n", "a preset has no dates"},
		{"denial", "version = 2\n\n[exclude]\nzai = [\"glm-5.3-thinking\"]\n", "a preset has no deny key"},
		{"normal budget", "version = 2\n\n[budget]\nzai = \"normal\"\n", "a preset names off, lean, minimal and prefer only"},
	}
	for _, setup := range setups {
		t.Run(setup.name, func(t *testing.T) {
			rosterPath := workspacePolicy(t)
			writeRosterFile(t, StatePath(rosterPath), setup.state)
			workspaceCatalog(t)
			workspace := loadWorkspaceOrFail(t, rosterPath, "")
			err := workspace.SaveVariant("frozen", false)
			if err == nil || !strings.Contains(err.Error(), setup.result) {
				t.Fatalf("SaveVariant() error = %v, want %q", err, setup.result)
			}
			if _, statErr := os.Stat(VariantsPath(rosterPath)); !os.IsNotExist(statErr) {
				t.Fatalf("a refused variant wrote the file: %v", statErr)
			}
		})
	}
}

// TestWorkspaceSaveVariantKeepsOrder: the access order the active preset
// puts in force is part of the variant, so the saved routes stay the same.
func TestWorkspaceSaveVariantKeepsOrder(t *testing.T) {
	policy := "version = 2\n\n" +
		"[access.alpha]\nagents = [\"pi\"]\n\n" +
		"[access.beta]\nagents = [\"pi\"]\n\n" +
		"[access.gamma]\nagents = [\"pi\"]\n" +
		policyRoles +
		"\n[preset.flip]\norder = [\"gamma\", \"alpha\"]\n"
	rosterPath := filepath.Join(t.TempDir(), "roster.toml")
	writeRosterFile(t, rosterPath, policy)
	writeRosterFile(t, StatePath(rosterPath), "version = 2\n\npreset = \"flip\"\n")
	workspaceCatalog(t)
	workspace := loadWorkspaceOrFail(t, rosterPath, "")
	if err := workspace.SaveVariant("frozen", false); err != nil {
		t.Fatalf("SaveVariant() error = %v", err)
	}
	loaded, err := LoadPolicy(rosterPath)
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	preset, ok := loaded.Preset("frozen")
	if !ok {
		t.Fatalf("the saved variant is not a preset: %v", loaded.PresetNames())
	}
	if want := []string{"gamma", "alpha", "beta"}; !reflect.DeepEqual(preset.Order, want) {
		t.Errorf("saved order = %v, want %v", preset.Order, want)
	}
}

// TestWorkspaceDeleteVariant: a machine-owned variant is deleted, the one
// in force is cleared with it, and a roster.toml preset is refused.
func TestWorkspaceDeleteVariant(t *testing.T) {
	rosterPath := workspacePolicy(t)
	variantsPath := VariantsPath(rosterPath)
	if err := SaveVariants(variantsPath, []Preset{
		{Name: "machine", Off: []string{"claude"}},
		{Name: "spare", Lean: []string{"zai"}},
	}); err != nil {
		t.Fatal(err)
	}
	writeRosterFile(t, StatePath(rosterPath), "version = 2\n\npreset = \"machine\"\n")
	workspaceCatalog(t)
	workspace := loadWorkspaceOrFail(t, rosterPath, "")

	if err := workspace.DeleteVariant("low"); err == nil || !strings.Contains(err.Error(), rosterPath) || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("DeleteVariant(low) error = %v, want the roster.toml refusal", err)
	}
	if err := workspace.DeleteVariant("nope"); err == nil || !strings.Contains(err.Error(), "unknown variant") {
		t.Fatalf("DeleteVariant(nope) error = %v, want the unknown name", err)
	}
	if err := workspace.DeleteVariant("machine"); err != nil {
		t.Fatalf("DeleteVariant(machine) error = %v", err)
	}
	policy, err := LoadPolicy(rosterPath)
	if err != nil {
		t.Fatalf("LoadPolicy() after delete error = %v", err)
	}
	if _, ok := policy.Preset("machine"); ok {
		t.Error("the deleted variant is still a preset")
	}
	if _, ok := policy.Preset("spare"); !ok {
		t.Error("deleting one variant dropped another")
	}
	state, err := LoadState(StatePath(rosterPath))
	if err != nil {
		t.Fatal(err)
	}
	if state.Preset != "" {
		t.Errorf("state preset = %q, want it cleared with the deleted variant", state.Preset)
	}
}

// TestWorkspaceDeleteProjectVariant: the project file is in the working
// tree, so a live run stops the delete.
func TestWorkspaceDeleteProjectVariant(t *testing.T) {
	rosterPath := workspacePolicy(t)
	root := applyProject(t, applyPlan())
	path := ProjectVariantsPath(root)
	if err := SaveVariants(path, []Preset{{Name: "project-low", Off: []string{"claude"}}}); err != nil {
		t.Fatal(err)
	}
	workspaceCatalog(t)
	workspace := loadWorkspaceOrFail(t, rosterPath, root)

	lock := filepath.Join(gitDir(t, root), "docflow", "run", "run.lock")
	writeRosterFile(t, lock, "123\n")
	err := workspace.DeleteVariant("project-low")
	if err == nil || !strings.Contains(err.Error(), "a run is in progress") {
		t.Fatalf("DeleteVariant() error = %v, want the run lock refusal", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("a refused delete removed the file: %v", statErr)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := workspace.DeleteVariant("project-low"); err != nil {
		t.Fatalf("DeleteVariant() error = %v", err)
	}
	presets, err := workspace.filePresets(path, SourceProject)
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 0 {
		t.Errorf("presets = %+v, want the project variant gone", presets)
	}
}

// TestWorkspaceExplainAndApplyPlan: the model explains and applies a plan
// header through the same code the CLI calls, and a live run stops the
// write.
func TestWorkspaceExplainAndApplyPlan(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	rosterPath := filepath.Join(config, "docflow", "roster.toml")
	data, err := os.ReadFile("testdata/policy-v2.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), `expires = "2026-06-30"`, `expires = "2099-01-01"`, 1)
	writeRosterFile(t, rosterPath, text)
	writeRosterFile(t, StatePath(rosterPath), "version = 2\n\npreset = \"low\"\n")
	workspaceCatalog(t)
	root := applyProject(t, applyPlan())
	before := readApplyPlan(t, root)

	report, err := ExplainPlan(root, "P040")
	if err != nil {
		t.Fatalf("ExplainPlan() error = %v", err)
	}
	if report.Plan != "P040" || report.Path != "docs/plans/P040-roster.md" {
		t.Errorf("report names %q at %q", report.Plan, report.Path)
	}
	changes := make(map[string]ExplainChange, len(report.Changes))
	for _, change := range report.Changes {
		changes[change.Key] = change
	}
	if got := changes["executor"]; got.Old != "" || got.New != "pi | opencode-go/deepseek-v4.1 | high" {
		t.Errorf("executor change = %+v, want the brief chain added", got)
	}
	if got := changes["heavy"]; got.Old == "" || got.New != "" {
		t.Errorf("heavy change = %+v, want the stale key removed", got)
	}
	if _, ok := changes["fallback"]; ok {
		t.Error("fallback changed although neither side has a tail")
	}
	if !contains(report.Warnings, "no fallback for executor") {
		t.Errorf("warnings = %v, want the executor warning", report.Warnings)
	}
	if after := readApplyPlan(t, root); after != before {
		t.Fatal("ExplainPlan rewrote the plan")
	}

	result, applyReport, err := ApplyPlan(root, "P040", true)
	if err != nil {
		t.Fatalf("ApplyPlan(dry run) error = %v", err)
	}
	if result.Code != clierrors.EXIT_OK || !strings.Contains(result.Stdout, "dry-run") {
		t.Fatalf("dry run = (%d, %q), want the dry-run notice", result.Code, result.Stdout)
	}
	if len(applyReport.Changes) != len(report.Changes) {
		t.Errorf("dry run changes = %+v, want the explain changes", applyReport.Changes)
	}
	if after := readApplyPlan(t, root); after != before {
		t.Fatal("the dry run wrote the plan")
	}

	result, report2, err := ApplyPlan(root, "P040", false)
	if err != nil {
		t.Fatalf("ApplyPlan() error = %v", err)
	}
	if result.Code != clierrors.EXIT_OK || !strings.Contains(result.Stdout, "commit: P040: apply roster") {
		t.Fatalf("apply = (%d, %q), want the commit hint", result.Code, result.Stdout)
	}
	if len(report2.Changes) != len(report.Changes) {
		t.Errorf("apply changes = %+v, want the explain changes", report2.Changes)
	}
	written := readApplyPlan(t, root)
	for key, want := range applyV2Header {
		if line := key + ": " + want; !strings.Contains(written, "\n"+line+"\n") {
			t.Errorf("plan after apply has no %q:\n%s", line, written)
		}
	}

	lock := filepath.Join(gitDir(t, root), "docflow", "run", "run.lock")
	writeRosterFile(t, lock, "123\n")
	_, _, err = ApplyPlan(root, "P040", false)
	if err == nil || !strings.Contains(err.Error(), "a run is in progress") {
		t.Fatalf("ApplyPlan() error = %v, want the run lock refusal", err)
	}
	coded, ok := err.(interface{ ExitCode() int })
	if !ok || coded.ExitCode() != clierrors.EXIT_ORDER {
		t.Fatalf("ApplyPlan() error code = %v, want %d", err, clierrors.EXIT_ORDER)
	}
}
