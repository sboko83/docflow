package roster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/clierrors"
)

// The workspace tests read the v4 fixture policy with its low policy and
// the scenario catalog: the same set the brief and apply tests use, so a
// row and a role chain can be compared with the documented brief.

// workspacePolicy writes the fixture with the zai expiry pushed past the
// test clock, and its variants file, as a roster of its own and returns
// its path.
func workspacePolicy(t *testing.T) string {
	t.Helper()
	return workspacePolicyAt(t, filepath.Join(t.TempDir(), "roster.toml"))
}

func workspacePolicyAt(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/policy.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), `expires = "2026-06-30"`, `expires = "2099-01-01"`, 1)
	if text == string(data) {
		t.Fatal("policy.toml no longer names the zai expiry the test patches")
	}
	variants, err := os.ReadFile("testdata/policy.variants.toml")
	if err != nil {
		t.Fatal(err)
	}
	writeRosterFile(t, path, text)
	writeRosterFile(t, VariantsPath(path), string(variants))
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
	state := "version = 2\n\noff = [\"opencode-go\", \"codex\", \"claude\", \"opencode\"]\n"
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
	if err := workspace.SaveVariant("low", true); err == nil || !strings.Contains(err.Error(), "roster.variants.toml") {
		t.Fatalf("SaveVariant(low) error = %v, want the variants file conflict", err)
	}
	if err := workspace.SaveVariant("two words", false); err == nil || !strings.Contains(err.Error(), "single word") {
		t.Fatalf("SaveVariant(two words) error = %v, want the name rule", err)
	}
}

func TestWorkspaceSaveVariantProjectWithoutGit(t *testing.T) {
	root := t.TempDir()
	rosterPath := workspacePolicy(t)
	workspaceCatalog(t)
	workspace := loadWorkspaceOrFail(t, rosterPath, root)
	err := workspace.SaveVariant("outside-git", true)
	if err == nil || !strings.Contains(err.Error(), "git rev-parse") {
		t.Fatalf("SaveVariant outside git = %v, want the checkout error", err)
	}
	if _, err := os.Stat(ProjectVariantsPath(root)); !os.IsNotExist(err) {
		t.Fatalf("refused variant wrote a project file: %v", err)
	}
}

// TestWorkspaceExplainAndApplyPlan: the model explains and applies a plan
// header through the same code the CLI calls, and a live run stops the
// write.
func TestWorkspaceExplainAndApplyPlan(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	rosterPath := workspacePolicyAt(t, filepath.Join(config, "docflow", "roster.toml"))
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

// TestLoadWorkspaceBrokenPolicyIsNotLegacy: a v4 roster whose load fails —
// here a variants preset with a judge cell on a check access — returns the
// real error, not the migration hint of a version 1 roster.
func TestLoadWorkspaceBrokenPolicyIsNotLegacy(t *testing.T) {
	rosterPath := workspacePolicy(t)
	writeRosterFile(t, VariantsPath(rosterPath), "version = 3\n\n[preset.broken.roles.zai]\nreview = \"glm-5.3\"\n")
	_, err := LoadWorkspace(rosterPath, "")
	if err == nil {
		t.Fatal("LoadWorkspace accepted a variants preset the loader refuses")
	}
	if !strings.Contains(err.Error(), "a check access never judges") {
		t.Fatalf("error = %v, want the real parse problem", err)
	}
	if strings.Contains(err.Error(), "migrate") {
		t.Fatalf("error = %v, want no migration hint for a versioned policy", err)
	}
}

// TestCatalogRowsAgentEfforts: a reasoning model whose source declared no
// levels gets the levels its channels accept, so the editor can still
// offer an effort; declared levels and non-reasoning models stay as the
// catalog has them.
func TestCatalogRowsAgentEfforts(t *testing.T) {
	catalog := &Catalog{
		Accesses: []AccessInfo{
			{Access: "strata", Agents: []string{"pi"}},
			{Access: "mixed", Agents: []string{"pi", "codex", "opencode"}},
		},
		Models: []ModelInfo{
			{Access: "strata", ID: "swift", Reasoning: true},
			{Access: "strata", ID: "leveled", Reasoning: true, Efforts: []string{"low", "high"}},
			{Access: "strata", ID: "plain"},
			{Access: "mixed", ID: "any", Reasoning: true},
		},
	}
	workspace := &Workspace{}
	workspace.fillCatalogRows(catalog)
	rows := map[string]WorkspaceModel{}
	for access, models := range workspace.CatalogModels {
		for _, model := range models {
			rows[access+"/"+model.ID] = model
		}
	}
	if got := strings.Join(rows["strata/swift"].AgentEfforts, ","); got != "off,minimal,low,medium,high,xhigh" {
		t.Errorf("swift agent efforts = %s, want the Pi levels", got)
	}
	if got := rows["strata/leveled"]; len(got.AgentEfforts) != 0 || strings.Join(got.Efforts, ",") != "low,high" {
		t.Errorf("leveled = %+v, want only its declared levels", got)
	}
	if got := rows["strata/plain"]; len(got.AgentEfforts) != 0 {
		t.Errorf("plain = %+v, want no levels without reasoning", got)
	}
	if got := strings.Join(rows["mixed/any"].AgentEfforts, ","); got != "low,medium,high,xhigh" {
		t.Errorf("mixed agent efforts = %s, want the levels Pi and Codex share", got)
	}
}
