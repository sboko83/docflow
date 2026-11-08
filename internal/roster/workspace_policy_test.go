package roster

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// editorWorkspace loads the fixture roster with the scenario catalog.
func editorWorkspace(t *testing.T) (*Workspace, string) {
	t.Helper()
	rosterPath := workspacePolicy(t)
	workspaceCatalog(t)
	t.Chdir(t.TempDir())
	return loadWorkspaceOrFail(t, rosterPath, ""), rosterPath
}

// reload reads the workspace again, the way the editor does after an op.
func reload(t *testing.T, rosterPath string) *Workspace {
	t.Helper()
	return loadWorkspaceOrFail(t, rosterPath, "")
}

func variantRow(t *testing.T, workspace *Workspace, name string) WorkspaceVariant {
	t.Helper()
	for _, row := range workspace.Variants {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("no variant %q in %+v", name, workspace.Variants)
	return WorkspaceVariant{}
}

// TestLoadWorkspaceModel: the rows carry the cells of the policy and the
// effective cells of the preset in force; the variants carry their
// changes; the order and the chains follow the preset.
func TestLoadWorkspaceModel(t *testing.T) {
	workspace, rosterPath := editorWorkspace(t)
	zai := accessRow(t, workspace, "zai")
	if zai.Cells["executor"] != "glm-5.3@max" || zai.Effective["executor"] != "glm-5.3@max" || !zai.Check {
		t.Fatalf("zai row = %+v", zai)
	}
	if got := strings.Join(workspace.Order, ","); got != "opencode-go,zai,codex,claude,opencode,local" {
		t.Fatalf("order = %s", got)
	}
	low := variantRow(t, workspace, "low")
	if low.Source != SourceVariants || !reflect.DeepEqual(low.Off, []string{"claude", "local", "opencode"}) || low.Roles["zai"]["executor"] != "" {
		t.Fatalf("low row = %+v", low)
	}
	if err := workspace.SetPreset("low"); err != nil {
		t.Fatal(err)
	}
	workspace = reload(t, rosterPath)
	zai = accessRow(t, workspace, "zai")
	if _, ok := zai.Effective["executor"]; ok || zai.Cells["executor"] == "" {
		t.Fatalf("zai under low = %+v, want the executor cleared only in effect", zai)
	}
	if claude := accessRow(t, workspace, "claude"); !claude.Off {
		t.Fatalf("claude under low = %+v, want off", claude)
	}
	if workspace.Preset != "low" || workspace.Problem != "" {
		t.Fatalf("preset = %q, problem = %q", workspace.Preset, workspace.Problem)
	}
	var executor RoleBrief
	for _, role := range workspace.Roles {
		if role.Role == "executor" {
			executor = role
		}
	}
	if len(executor.Routes) != 1 || executor.Routes[0].Access != "opencode-go" {
		t.Fatalf("executor chain = %+v", executor.Routes)
	}
}

// TestPolicyTableOperations: a cell, the order and the check flag are
// written to roster.toml; a judge cell on a check access is refused and
// leaves the file as it was.
func TestPolicyTableOperations(t *testing.T) {
	workspace, rosterPath := editorWorkspace(t)
	if err := workspace.SetCell("codex", "heavy", "gpt-6.1-sol@high"); err != nil {
		t.Fatalf("SetCell() error = %v", err)
	}
	if err := workspace.MoveAccess("codex", "up"); err != nil {
		t.Fatalf("MoveAccess() error = %v", err)
	}
	if err := workspace.SetCell("opencode-go", "review", ""); err != nil {
		t.Fatalf("SetCell(clear) error = %v", err)
	}
	policy, err := LoadPolicy(rosterPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(policy.AccessNames(), ","); got != "opencode-go,codex,zai,claude,opencode,local" {
		t.Fatalf("order = %s", got)
	}
	codex, _ := policy.Access("codex")
	if codex.Cell("heavy") != (Cell{Model: "gpt-6.1-sol", Effort: "high"}) {
		t.Fatalf("codex heavy = %+v", codex.Cell("heavy"))
	}
	if og, _ := policy.Access("opencode-go"); !og.Cell("review").Empty() {
		t.Fatalf("opencode-go review = %+v, want cleared", og.Cell("review"))
	}
	before, _ := os.ReadFile(rosterPath)
	if err := workspace.SetCell("zai", "review", "glm-5.3@high"); err == nil || !strings.Contains(err.Error(), "never judges") {
		t.Fatalf("SetCell(check judge) error = %v", err)
	}
	if err := workspace.SetCell("zai", "planner", "glm-5.3"); err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("SetCell(unknown role) error = %v", err)
	}
	if after, _ := os.ReadFile(rosterPath); string(after) != string(before) {
		t.Fatal("a refused cell rewrote roster.toml")
	}
	// Turning check on drops the judge cells of the access.
	if err := workspace.SetAccessCheck("codex", true); err != nil {
		t.Fatalf("SetAccessCheck() error = %v", err)
	}
	policy, _ = LoadPolicy(rosterPath)
	codex, _ = policy.Access("codex")
	if !codex.Check || !codex.Cell("acceptance").Empty() || codex.Cell("heavy").Empty() {
		t.Fatalf("codex after check = %+v", codex)
	}
}

// TestAccessOperations: an access is added at the end and removed with
// everything that names it in the variants and the conditions.
func TestAccessOperations(t *testing.T) {
	workspace, rosterPath := editorWorkspace(t)
	if err := workspace.AddAccess("new", "metered", []string{"opencode"}); err != nil {
		t.Fatalf("AddAccess() error = %v", err)
	}
	if err := workspace.AddAccess("bad/name", "", []string{"pi"}); err == nil {
		t.Fatal("AddAccess() accepted a name with '/'")
	}
	if err := workspace.SetOff("claude", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.RemoveAccess("claude"); err != nil {
		t.Fatalf("RemoveAccess() error = %v", err)
	}
	workspace = reload(t, rosterPath)
	if got := strings.Join(workspace.Policy.AccessNames(), ","); got != "opencode-go,zai,codex,opencode,local,new" {
		t.Fatalf("accesses = %s", got)
	}
	if low := variantRow(t, workspace, "low"); contains(low.Off, "claude") {
		t.Fatalf("low still names claude: %+v", low)
	}
	if contains(workspace.State.Off, "claude") {
		t.Fatalf("state still names claude: %+v", workspace.State)
	}
}

// TestVariantOperations: a policy is created, switches an access off,
// reorders, overrides and clears cells, inherits again, is renamed with the
// state following it and is deleted.
func TestVariantOperations(t *testing.T) {
	workspace, rosterPath := editorWorkspace(t)
	if err := workspace.CreateVariant("zai-first", false, ""); err != nil {
		t.Fatalf("CreateVariant() error = %v", err)
	}
	if err := workspace.CreateVariant("zai-first", false, ""); err == nil {
		t.Fatal("CreateVariant() accepted a taken name")
	}
	steps := []func() error{
		func() error { return workspace.MoveVariantAccess("zai-first", "zai", "up") },
		func() error { return workspace.SetVariantOff("zai-first", "local", true) },
		func() error {
			return workspace.SetVariantCell("zai-first", "codex", "executor", "gpt-6-luna@high", false)
		},
		func() error { return workspace.SetVariantCell("zai-first", "opencode-go", "cheap", "", false) },
		func() error {
			return workspace.SetVariantCell("zai-first", "zai", "cheap", "glm-5.3-flash@high", false)
		},
		func() error { return workspace.SetVariantCell("zai-first", "zai", "cheap", "", true) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d error = %v", i, err)
		}
	}
	if err := workspace.SetVariantCell("zai-first", "zai", "replan", "glm-5.3", false); err == nil {
		t.Fatal("SetVariantCell() accepted a judge cell on a check access")
	}
	workspace = reload(t, rosterPath)
	row := variantRow(t, workspace, "zai-first")
	if !reflect.DeepEqual(row.Order, []string{"zai", "opencode-go", "codex", "claude", "opencode", "local"}) || !reflect.DeepEqual(row.Off, []string{"local"}) {
		t.Fatalf("zai-first = %+v", row)
	}
	want := map[string]map[string]string{"codex": {"executor": "gpt-6-luna@high"}, "opencode-go": {"cheap": ""}}
	if !reflect.DeepEqual(row.Roles, want) {
		t.Fatalf("zai-first roles = %v, want %v", row.Roles, want)
	}
	// Moving back to the policy order drops the order key.
	if err := workspace.MoveVariantAccess("zai-first", "zai", "down"); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SetPreset("zai-first"); err != nil {
		t.Fatal(err)
	}
	if err := workspace.RenameVariant("zai-first", "zai-last"); err != nil {
		t.Fatalf("RenameVariant() error = %v", err)
	}
	workspace = reload(t, rosterPath)
	if row := variantRow(t, workspace, "zai-last"); len(row.Order) != 0 {
		t.Fatalf("zai-last order = %v, want none", row.Order)
	}
	if workspace.State.Preset != "zai-last" {
		t.Fatalf("state preset = %q, want the new name", workspace.State.Preset)
	}
	if err := workspace.DeleteVariant("zai-last"); err != nil {
		t.Fatalf("DeleteVariant() error = %v", err)
	}
	workspace = reload(t, rosterPath)
	if workspace.State.Preset != "" || len(workspace.Variants) != 1 {
		t.Fatalf("after delete: preset %q, variants %+v", workspace.State.Preset, workspace.Variants)
	}
}

// TestWorkspaceConditionOperations: off, on, expire and the preset write
// the state file; an unknown access is refused before anything is written.
func TestWorkspaceConditionOperations(t *testing.T) {
	workspace, rosterPath := editorWorkspace(t)
	day := time.Now().UTC().AddDate(0, 1, 0).Truncate(24 * time.Hour)
	if err := workspace.SetOff("codex", day); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SetExpires("zai", day); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SetOff("nobody", time.Time{}); err == nil || !strings.Contains(err.Error(), "unknown access 'nobody'") {
		t.Fatalf("SetOff(nobody) error = %v", err)
	}
	if err := workspace.SetPreset("nothing"); err == nil {
		t.Fatal("SetPreset() accepted an unknown preset")
	}
	workspace = reload(t, rosterPath)
	codex := accessRow(t, workspace, "codex")
	if !codex.Off || codex.Until != dayText(day) {
		t.Fatalf("codex row = %+v", codex)
	}
	if zai := accessRow(t, workspace, "zai"); zai.Expires != dayText(day) {
		t.Fatalf("zai row = %+v", zai)
	}
	if err := workspace.SetOn("codex"); err != nil {
		t.Fatal(err)
	}
	if codex := accessRow(t, reload(t, rosterPath), "codex"); codex.Off {
		t.Fatalf("codex after on = %+v", codex)
	}
}

// TestWorkspaceSaveVariantGlobal freezes the preset in force plus the off
// switches as a new policy; a dated condition is refused.
func TestWorkspaceSaveVariantGlobal(t *testing.T) {
	workspace, rosterPath := editorWorkspace(t)
	if err := workspace.SetPreset("low"); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SetOff("codex", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveVariant("frozen", false); err != nil {
		t.Fatalf("SaveVariant() error = %v", err)
	}
	row := variantRow(t, reload(t, rosterPath), "frozen")
	if !reflect.DeepEqual(row.Off, []string{"claude", "local", "opencode", "codex"}) || row.Roles["zai"]["executor"] != "" {
		t.Fatalf("frozen = %+v", row)
	}
	if err := workspace.SetOff("zai", time.Now().UTC().AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveVariant("dated", false); err == nil || !strings.Contains(err.Error(), "a preset has no dates") {
		t.Fatalf("SaveVariant(dated) error = %v", err)
	}
}
