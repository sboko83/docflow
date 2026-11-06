package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/roster"
)

// The roster screen tests read a fixture policy, its catalog and the two
// machine-owned variants files. The roster lives under a temporary
// XDG_CONFIG_HOME, so the default path of the model is the fixture and no
// test touches the personal roster.

const testRosterPolicy = `version = 2

[access.zai]
agents = ["pi"]
kind   = "subscription"
budget = "lean"
check  = true
models = ["glm-*"]

[access.codex]
agents  = ["codex"]
kind    = "subscription"
budget  = "normal"
trusted = true
expires = "2099-06-30"

[access.claude]
agents  = ["claude"]
kind    = "subscription"
budget  = "off"
trusted = true

[model."zai/glm-5.3"]
class = "strong"

[model."zai/glm-5.3-flash"]
class = "cheap"

[model."codex/gpt-6.1-sol"]
class = "strong"

[model."codex/gpt-6-luna"]
class = "standard"

[model."claude/claude-opus-5-5"]
class = "strong"

[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"

[preset.low]
lean    = ["zai"]
minimal = ["codex"]
order   = ["zai", "codex", "claude"]
`

const testGlobalVariants = "version = 2\n\n[preset.machine]\nlean = [\"zai\"]\n"

const testProjectVariants = "version = 2\n\n[preset.project]\nminimal = [\"codex\"]\n"

// rosterFixture writes the plan project with a git directory — the project
// variants file is written under the checkout lock — a roster under a
// private config and the catalog cache.
func rosterFixture(t *testing.T) (root, rosterPath string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root = writeTestProject(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "docflow test"},
		{"config", "user.email", "test@example.invalid"},
		{"add", "-A"},
		{"commit", "-q", "-m", "initial"},
	} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rosterPath = filepath.Join(config, "docflow", "roster.toml")
	writeRosterTestFile(t, rosterPath, testRosterPolicy)
	writeRosterTestFile(t, filepath.Join(config, "docflow", "roster.variants.toml"), testGlobalVariants)
	writeRosterTestFile(t, filepath.Join(root, "docs", "roster.variants.toml"), testProjectVariants)
	writeRosterTestCatalog(t)
	return root, rosterPath
}

// writeRosterTestCatalog installs the catalog the workspace loads from the
// default cache path.
func writeRosterTestCatalog(t *testing.T) {
	t.Helper()
	catalog := &roster.Catalog{
		Version: roster.CatalogVersion,
		Sources: []string{"fixture"},
		Accesses: []roster.AccessInfo{
			{Access: "zai", Agents: []string{"pi"}, Kind: "subscription", Auth: "ready"},
			{Access: "codex", Agents: []string{"codex"}, Kind: "subscription", Auth: "ready"},
			{Access: "claude", Agents: []string{"claude"}, Kind: "subscription", Auth: "ready"},
		},
		Models: []roster.ModelInfo{
			{Access: "zai", ID: "glm-5.3-flash", Context: 200000},
			{Access: "zai", ID: "glm-5.3", Context: 200000},
			{Access: "codex", ID: "gpt-6-luna", Context: 400000},
			{Access: "codex", ID: "gpt-6.1-sol", Context: 400000},
			{Access: "claude", ID: "claude-opus-5-5", Context: 200000},
		},
	}
	if err := catalog.Save(""); err != nil {
		t.Fatalf("catalog Save: %v", err)
	}
}

func writeRosterTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRosterTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(data)
}

// rosterScreenModel loads the plan workspace and then the roster screen of
// the fixture roster.
func rosterScreenModel(t *testing.T, root, rosterPath string) Model {
	t.Helper()
	model := New(root)
	model.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	updated, _ := model.Update(model.Init()())
	model = updated.(Model)
	model.rosterPath = rosterPath
	updated, cmd := model.Update(keyMsg("2"))
	model = updated.(Model)
	if cmd == nil {
		t.Fatalf("opening the roster screen returned no load command")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.rosterPhase != rosterReady {
		t.Fatalf("roster phase = %v, error = %q", model.rosterPhase, model.rosterErr)
	}
	return model
}

// rosterPanelModel focuses one roster panel of the loaded screen.
func rosterPanelModel(t *testing.T, root, rosterPath string, panel rosterPanel) Model {
	t.Helper()
	model := rosterScreenModel(t, root, rosterPath)
	for model.rosterPanel != panel {
		model = send(t, model, keyMsg("tab"))
	}
	return model
}

// TestRosterScreenShowsVariantsAccessesAndChains renders the screen on the
// fixture at both supported layouts: three panels from 100 columns, one
// panel switched by Tab below it.
func TestRosterScreenShowsVariantsAccessesAndChains(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	model := rosterScreenModel(t, root, rosterPath)

	wide := resize(t, model, 120, 40)
	rendered := wide.String()
	assertScreenFits(t, rendered, 120, 40)
	for _, want := range []string{
		"Варианты", "Доступы", "Цепочки",
		"low (roster)", "machine (глоб.)", "project (проект)",
		"zai", "бюджет lean", "check", "модели glm-*",
		"codex", "trusted", "бюджет off", "до 2099-06-30",
		"executor", "heavy", "бюджет ",
		"⚠",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("120x40 roster screen lost %q:\n%s", want, rendered)
		}
	}
	// The selected variant is the first row; no preset is active yet.
	if !strings.Contains(rendered, ">  low (roster)") {
		t.Fatalf("the selected variant is not marked:\n%s", rendered)
	}

	narrow := resize(t, model, 60, 20)
	rendered = narrow.String()
	assertScreenFits(t, rendered, 60, 20)
	if !strings.Contains(rendered, "> Варианты") || strings.Contains(rendered, "Цепочки") {
		t.Fatalf("60x20 shows more than the focused panel:\n%s", rendered)
	}
	narrow = send(t, narrow, keyMsg("tab"))
	if rendered := narrow.String(); !strings.Contains(rendered, "> Доступы") || !strings.Contains(rendered, "zai") {
		t.Fatalf("Tab did not switch the single panel:\n%s", rendered)
	}
	// The single full-width panel has room for the conditions a narrower
	// column truncates: the expiry date comes from the policy.
	if rendered := narrow.String(); !strings.Contains(rendered, "до 2099-06-30") {
		t.Fatalf("60x20 access panel lost the expiry date:\n%s", rendered)
	}
	// The smallest supported terminal draws every panel without overflowing
	// and keeps the help reachable.
	tiny := resize(t, model, 32, 10)
	for _, panel := range []rosterPanel{panelVariants, panelAccesses, panelRoles} {
		tiny.rosterPanel = panel
		rendered := tiny.String()
		assertScreenFits(t, rendered, 32, 10)
		if !strings.Contains(rendered, "? помощь") {
			t.Fatalf("32x10 %v lost the help key:\n%s", panel, rendered)
		}
	}

	// The chains are taller than the smallest screen: the keys scroll them
	// from the first role to the warning line under the last chain.
	roles := resize(t, model, 32, 10)
	roles.rosterPanel = panelRoles
	roles = send(t, roles, keyMsg("G"))
	if rendered := roles.String(); !strings.Contains(rendered, "⚠") {
		t.Fatalf("G did not reach the warning line under the chains:\n%s", rendered)
	}
	roles = send(t, roles, keyMsg("g"))
	if rendered := roles.String(); !strings.Contains(rendered, "executor") {
		t.Fatalf("g did not return to the first chain:\n%s", rendered)
	}
}

// TestRosterSpaceSwitchesAnAccessOffAndOn: the space key opens the until
// dialog, writes the condition state and reloads the screen; a second space
// lifts the off condition.
func TestRosterSpaceSwitchesAnAccessOffAndOn(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	model := rosterPanelModel(t, root, rosterPath, panelAccesses)
	statePath := roster.StatePath(rosterPath)
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("the fixture wrote a state file before any edit: %v", err)
	}

	model = send(t, model, keyMsg("space"))
	if model.dialog != dialogOff {
		t.Fatalf("space opened dialog %v, want the until dialog", model.dialog)
	}
	model = send(t, model, keyMsg("2099-12-31"))
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogNone {
		t.Fatalf("the date did not close the dialog")
	}
	if !strings.Contains(model.notice, "switched off until 2099-12-31") {
		t.Fatalf("notice = %q", model.notice)
	}
	state, err := roster.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Off) != 1 || state.Off[0] != "zai" || state.Until["zai"].IsZero() {
		t.Fatalf("state off = %v until %v", state.Off, state.Until)
	}
	if rendered := model.String(); !strings.Contains(rendered, "off до 2099-12-31") {
		t.Fatalf("the screen does not show the off date:\n%s", rendered)
	}

	model = send(t, model, keyMsg("space"))
	state, err = roster.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState after on: %v", err)
	}
	if len(state.Off) != 0 || len(state.Until) != 0 {
		t.Fatalf("space did not lift the off condition: %v %v", state.Off, state.Until)
	}
}

// TestRosterBudgetRefusesMinimalOnUntrusted: the refused level shows the
// reason and writes nothing; the next accepted level lands in the file.
func TestRosterBudgetRefusesMinimalOnUntrusted(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	model := rosterPanelModel(t, root, rosterPath, panelAccesses)
	statePath := roster.StatePath(rosterPath)

	// zai is lean and not trusted: the next level is minimal.
	model = send(t, model, keyMsg("right"))
	if !strings.Contains(model.notice, "budget minimal requires trusted = true") {
		t.Fatalf("refusal notice = %q", model.notice)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("a refused budget wrote the state file: %v", err)
	}

	model = send(t, model, keyMsg("left"))
	if !strings.Contains(model.notice, "zai budget normal") {
		t.Fatalf("accepted budget notice = %q", model.notice)
	}
	state, err := roster.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Budget["zai"] != roster.BudgetNormal {
		t.Fatalf("state budget = %v", state.Budget)
	}
	if rendered := model.String(); !strings.Contains(rendered, "бюджет normal") {
		t.Fatalf("the screen kept the old budget:\n%s", rendered)
	}
}

// TestRosterModelListEditsAllowAndDeny: Enter opens the catalog models of
// the access, a and x write the condition globs, Backspace drops them.
func TestRosterModelListEditsAllowAndDeny(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	model := rosterPanelModel(t, root, rosterPath, panelAccesses)
	statePath := roster.StatePath(rosterPath)

	model = send(t, model, keyMsg("enter"))
	if model.overlay != overlayRosterModels || model.rmodels == nil || model.rmodels.access != "zai" {
		t.Fatalf("Enter opened overlay %v for %+v", model.overlay, model.rmodels)
	}
	if rendered := model.String(); !strings.Contains(rendered, "Модели zai") || !strings.Contains(rendered, "glm-5.3") {
		t.Fatalf("the model list is not shown:\n%s", rendered)
	}

	// The catalog keeps glm-5.3 before glm-5.3-flash; the first row is
	// selected.
	model = send(t, model, keyMsg("a"))
	state, err := roster.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Models["zai"]) != 1 || state.Models["zai"][0] != "glm-5.3" {
		t.Fatalf("allow wrote %v", state.Models)
	}

	model = send(t, model, keyMsg("x"))
	state, err = roster.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Exclude["zai"]) != 1 || state.Exclude["zai"][0] != "glm-5.3" {
		t.Fatalf("deny wrote %v", state.Exclude)
	}

	model = send(t, model, keyMsg("backspace"))
	state, err = roster.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Models["zai"]) != 0 || len(state.Exclude["zai"]) != 0 {
		t.Fatalf("backspace left the conditions: %v %v", state.Models, state.Exclude)
	}
	if !strings.Contains(model.notice, "condition cleared") {
		t.Fatalf("notice = %q", model.notice)
	}
	// The condition glob is gone, the policy allowlist stays: the model is
	// still allowed by roster.toml, which the screen never rewrites.
	if access, ok := model.rosterAccess("zai"); !ok || len(access.Allow) != 1 || access.Allow[0] != "glm-*" {
		t.Fatalf("allow after the clear = %+v, want the policy globs back", access.Allow)
	}

	model = send(t, model, keyMsg("esc"))
	if model.overlay != overlayNone || model.rmodels != nil {
		t.Fatalf("Esc did not close the model list")
	}
}

// TestRosterVariantsSetClearSaveAndDelete: Enter makes a variant the active
// preset, Backspace clears it, n saves the conditions and D deletes a
// machine-owned variant after the confirmation.
func TestRosterVariantsSetClearSaveAndDelete(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	model := rosterPanelModel(t, root, rosterPath, panelVariants)
	statePath := roster.StatePath(rosterPath)
	globalVariants := roster.VariantsPath(rosterPath)

	// The second variant is the global one.
	model = send(t, model, keyMsg("down"))
	if variant, _ := model.selectedVariant(); variant.Name != "machine" {
		t.Fatalf("selected variant = %+v", variant)
	}
	model = send(t, model, keyMsg("enter"))
	state, err := roster.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Preset != "machine" {
		t.Fatalf("preset = %q, want machine", state.Preset)
	}
	if rendered := model.String(); !strings.Contains(rendered, "● machine (глоб.)") {
		t.Fatalf("the active variant is not marked:\n%s", rendered)
	}

	model = send(t, model, keyMsg("backspace"))
	state, err = roster.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState after clear: %v", err)
	}
	if state.Preset != "" {
		t.Fatalf("preset = %q after Backspace", state.Preset)
	}

	// n saves the conditions in force into the global variants file.
	model = send(t, model, keyMsg("n"))
	if model.dialog != dialogVariant {
		t.Fatalf("n opened dialog %v", model.dialog)
	}
	model = send(t, model, keyMsg("frozen"))
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogNone {
		t.Fatalf("Enter did not save the variant: %v %q", model.dialog, model.notice)
	}
	if !strings.Contains(readRosterTestFile(t, globalVariants), "[preset.frozen]") {
		t.Fatalf("the saved variant is not in %s:\n%s", globalVariants, readRosterTestFile(t, globalVariants))
	}
	if !strings.Contains(model.String(), "frozen (глоб.)") {
		t.Fatalf("the saved variant is not listed:\n%s", model.String())
	}

	// Tab switches the target to the project file, which lives in the
	// checkout and obeys its lock.
	model = send(t, model, keyMsg("n"))
	model = send(t, model, keyMsg("project-one"))
	model = send(t, model, keyMsg("tab"))
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogNone {
		t.Fatalf("the project variant was not saved: %q", model.notice)
	}
	projectVariants := filepath.Join(root, "docs", "roster.variants.toml")
	if !strings.Contains(readRosterTestFile(t, projectVariants), "[preset.project-one]") {
		t.Fatalf("the project variant is not in %s", projectVariants)
	}

	// D asks first and deletes from the machine file on confirmation. The
	// new machine variant is the last row of the list.
	for {
		variant, ok := model.selectedVariant()
		if !ok || variant.Name == "project-one" {
			break
		}
		model = send(t, model, keyMsg("down"))
	}
	if variant, _ := model.selectedVariant(); variant.Name != "project-one" {
		t.Fatalf("could not select the project variant")
	}
	model = send(t, model, keyMsg("D"))
	if model.dialog != dialogVariantDelete {
		t.Fatalf("D opened dialog %v, want the confirmation", model.dialog)
	}
	if strings.Contains(readRosterTestFile(t, projectVariants), "preset.project-one") == false {
		t.Fatalf("the confirmation deleted the variant before Enter")
	}
	model = send(t, model, keyMsg("enter"))
	if strings.Contains(readRosterTestFile(t, projectVariants), "[preset.project-one]") {
		t.Fatalf("Enter did not delete the variant:\n%s", readRosterTestFile(t, projectVariants))
	}
	for _, variant := range model.roster.Variants {
		if variant.Name == "project-one" {
			t.Fatalf("the deleted variant is still in the model: %+v", model.roster.Variants)
		}
	}
}

// TestRosterDeleteRefusesRosterPreset: a preset of the hand-edited policy
// has no dialog and no write; the notice names the file to edit by hand.
func TestRosterDeleteRefusesRosterPreset(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	model := rosterPanelModel(t, root, rosterPath, panelVariants)

	model = send(t, model, keyMsg("D"))
	if model.dialog != dialogNone {
		t.Fatalf("D opened a dialog for a roster.toml preset")
	}
	if !strings.Contains(model.notice, "roster.toml") || !strings.Contains(model.notice, "by hand") {
		t.Fatalf("notice = %q", model.notice)
	}
	if !strings.Contains(readRosterTestFile(t, rosterPath), "[preset.low]") {
		t.Fatalf("the refusal rewrote roster.toml")
	}
}

// TestRosterProjectVariantRefusedUnderRunLock: the project variants file
// lives in the working tree, so a live run stops the save and the notice
// names the lock; the file keeps its presets.
func TestRosterProjectVariantRefusedUnderRunLock(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	model := rosterPanelModel(t, root, rosterPath, panelVariants)
	projectVariants := filepath.Join(root, "docs", "roster.variants.toml")
	before := readRosterTestFile(t, projectVariants)

	output, err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Output()
	if err != nil {
		t.Fatalf("git rev-parse --git-dir: %v", err)
	}
	gitDir := strings.TrimSpace(string(output))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	writeRosterTestFile(t, filepath.Join(gitDir, "docflow", "run", "run.lock"), "123\n")

	model = send(t, model, keyMsg("n"))
	model = send(t, model, keyMsg("locked"))
	model = send(t, model, keyMsg("tab"))
	model = send(t, model, keyMsg("enter"))
	// The name dialog stays open with the reason, so the name can be fixed
	// or the save retried once the run is gone.
	if model.dialog != dialogVariant {
		t.Fatalf("the refused save closed the dialog: %q", model.notice)
	}
	if reason := strings.Join(model.dialogLines, "\n"); !strings.Contains(reason, "a run is in progress") {
		t.Fatalf("dialog body = %q, want the run lock refusal", reason)
	}
	if after := readRosterTestFile(t, projectVariants); after != before {
		t.Fatalf("a refused save rewrote the project variants file:\n%s", after)
	}
}

// TestRosterScreenDefaultsToTheDefaultRosterPath: without --roster the
// screen reads the personal roster of roster.DefaultPath(), and the flag of
// the command replaces it.
func TestRosterScreenDefaultsToTheDefaultRosterPath(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	root := t.TempDir()
	if got, want := New(root).rosterPath, roster.DefaultPath(); got != want {
		t.Fatalf("default roster path = %q, want %q", got, want)
	}
	if got := applyOptions(New(root), options{rosterPath: "/tmp/bench/roster.toml"}).rosterPath; got != "/tmp/bench/roster.toml" {
		t.Fatalf("WithRosterPath left %q", got)
	}
}

// TestRosterScreenShowsLoadRefusal: an unreadable roster is a notice on the
// screen, not a panic and not a silent empty list.
func TestRosterScreenShowsLoadRefusal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	writeRosterTestFile(t, rosterPath, "version = 2\n\n[access.zai]\nbudget = \"lean\"\n")
	model := New(root)
	model.lockCheck = func(string) (int, bool, error) { return 0, false, nil }
	updated, _ := model.Update(model.Init()())
	model = updated.(Model)
	model.rosterPath = rosterPath
	updated, cmd := model.Update(keyMsg("2"))
	model = updated.(Model)
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	model = resize(t, model, 80, 24)
	if model.rosterPhase != rosterFailed {
		t.Fatalf("phase = %v, want failed", model.rosterPhase)
	}
	if rendered := model.String(); !strings.Contains(rendered, "Не читается ростер") || !strings.Contains(rendered, "agents is required") {
		t.Fatalf("the refusal is not shown:\n%s", rendered)
	}
}

// testLegacyRoster is a version 1 roster: the TUI offers the migration on
// the screen instead of reading it as a broken v2 policy.
const testLegacyRoster = `spend = ["zai", "codex"]

[provider.zai]
check = true

[provider.codex]
trusted = true

[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"

[route.glm-flash-high]
run  = "pi | zai/glm-5.3-flash | high"
tier = "cheap"

[route.glm-flash-max]
run  = "pi | zai/glm-5.3-flash | max"
tier = "standard"

[route.glm-max]
run  = "pi | zai/glm-5.3 | max"
tier = "strong"

[route.luna-high]
run  = "codex | gpt-6-luna | high"
tier = "cheap"

[route.luna-max]
run  = "codex | gpt-6-luna | max"
tier = "standard"

[route.sol-high]
run  = "codex | gpt-6.1-sol | high"
tier = "strong"
`

// flatScreen is the rendered screen with all whitespace removed: a path
// longer than the terminal is wrapped, and a wrapped path is still one name.
func flatScreen(model Model) string {
	return strings.Join(strings.Fields(model.String()), "")
}

// rosterFooter is the last line of the rendered screen.
func rosterFooter(t *testing.T, model Model) string {
	t.Helper()
	lines := strings.Split(model.String(), "\n")
	return strings.TrimRight(lines[len(lines)-1], " ")
}

// TestRosterScanBuildsTheCatalogInTheBackground: an empty catalog replaces
// the chains with the scan hint; u runs the scan in the background, keeps
// the screen usable and finishes with the access summary and the source
// warnings.
func TestRosterScanBuildsTheCatalogInTheBackground(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	if err := os.Remove(roster.DefaultCachePath()); err != nil {
		t.Fatalf("removing the catalog cache: %v", err)
	}
	model := rosterPanelModel(t, root, rosterPath, panelRoles)
	if model.roster == nil || !model.roster.CatalogEmpty {
		t.Fatalf("the fixture catalog is still loaded: %+v", model.roster)
	}
	model = resize(t, model, 120, 40)
	rendered := model.String()
	if !strings.Contains(rendered, "u — собрать каталог") {
		t.Fatalf("an empty catalog does not offer the scan:\n%s", rendered)
	}
	if strings.Contains(rendered, "executor") {
		t.Fatalf("chains were built without a catalog:\n%s", rendered)
	}
	if footer := rosterFooter(t, model); !strings.Contains(footer, "u scan") {
		t.Fatalf("the empty-catalog footer lost the scan: %q", footer)
	}

	model.rosterScan = func(roster.ScanOptions) (*roster.Catalog, []string, error) {
		writeRosterTestCatalog(t)
		catalog, err := roster.LoadCatalog("")
		return catalog, []string{"pi-store: unreadable JSON"}, err
	}
	updated, cmd := model.Update(keyMsg("u"))
	model = updated.(Model)
	if cmd == nil || !model.rosterScanning {
		t.Fatalf("u did not start the scan: scanning=%t cmd=%v", model.rosterScanning, cmd)
	}
	if model.notice != "scanning…" {
		t.Fatalf("notice = %q, want the scanning placeholder", model.notice)
	}
	// The screen keeps its rows and its keys while the scanners run.
	if model.dialog != dialogNone {
		t.Fatalf("the scan opened a dialog")
	}
	if rendered := model.String(); !strings.Contains(rendered, "Доступы") {
		t.Fatalf("the scan took the screen:\n%s", rendered)
	}
	if again, more := model.Update(keyMsg("u")); more != nil {
		t.Fatalf("a second u started another scan")
	} else if !again.(Model).rosterScanning {
		t.Fatalf("the second u stopped the running scan")
	}

	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.rosterScanning || model.roster == nil || model.roster.CatalogEmpty {
		t.Fatalf("the finished scan did not reload the catalog: %+v", model.roster)
	}
	if !strings.Contains(model.notice, "каталог:") || !strings.Contains(model.notice, "предупреждений: 1") {
		t.Fatalf("scan notice = %q", model.notice)
	}
	rendered = model.String()
	if !strings.Contains(rendered, "executor") || !strings.Contains(rendered, "2 мод.") {
		t.Fatalf("the reloaded rows are not shown:\n%s", rendered)
	}
	if !strings.Contains(rendered, "⚠ pi-store: unreadable JSON") {
		t.Fatalf("the source warning is not shown in the chains panel:\n%s", rendered)
	}
	// The warning stays reachable on the smallest screen: G scrolls the
	// chains pane to the end of the block.
	tiny := resize(t, model, 32, 10)
	tiny.rosterPanel = panelRoles
	tiny = send(t, tiny, keyMsg("G"))
	if rendered := tiny.String(); !strings.Contains(rendered, "pi-store") {
		t.Fatalf("32x10 lost the scan warning:\n%s", rendered)
	}
	assertScreenFits(t, tiny.String(), 32, 10)
}

// TestRosterLegacyRosterPreviewsAndWritesTheMigration: the version 1
// screen explains itself and Enter opens the preview; Enter writes the v2
// policy with its .bak, Esc leaves the file untouched.
func TestRosterLegacyRosterPreviewsAndWritesTheMigration(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	writeRosterTestFile(t, rosterPath, testLegacyRoster)
	model := rosterScreenModel(t, root, rosterPath)
	if model.roster == nil || !model.roster.Legacy {
		t.Fatalf("the v1 roster is not flagged for migration: %+v", model.roster)
	}
	model = resize(t, model, 120, 40)
	rendered := model.String()
	for _, want := range []string{"Ростер версии 1", "Enter — миграция", "u — собрать каталог"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the v1 screen lost %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(flatScreen(model), rosterPath) {
		t.Fatalf("the v1 screen does not name %s:\n%s", rosterPath, rendered)
	}
	if footer := rosterFooter(t, model); !strings.Contains(footer, "Enter migrate") {
		t.Fatalf("the v1 footer lost the migration: %q", footer)
	}

	// Enter previews the v2 text and writes nothing.
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogMigrate {
		t.Fatalf("Enter opened dialog %v, want the migration preview", model.dialog)
	}
	if got := readRosterTestFile(t, rosterPath); got != testLegacyRoster {
		t.Fatalf("the preview rewrote the roster:\n%s", got)
	}
	if _, err := os.Stat(rosterPath + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("the preview wrote a backup: %v", err)
	}
	rendered = model.String()
	for _, want := range []string{"version = 2", "roster.toml.bak", "Enter — записать"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the preview lost %q:\n%s", want, rendered)
		}
	}

	// Esc cancels: the file and its backup stay as they were.
	model = send(t, model, keyMsg("esc"))
	if model.dialog != dialogNone || !strings.Contains(model.notice, "отменена") {
		t.Fatalf("Esc did not cancel the migration: dialog=%v notice=%q", model.dialog, model.notice)
	}
	if got := readRosterTestFile(t, rosterPath); got != testLegacyRoster {
		t.Fatalf("a cancelled migration rewrote the roster:\n%s", got)
	}
	if _, err := os.Stat(rosterPath + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("a cancelled migration wrote a backup: %v", err)
	}

	// A live foreign run refuses the write and keeps the preview open.
	model.lockCheck = func(string) (int, bool, error) { return 4242, true, nil }
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogMigrate || !strings.Contains(model.notice, "another runner") {
		t.Fatalf("a live run did not refuse the migration: dialog=%v notice=%q", model.dialog, model.notice)
	}
	if got := readRosterTestFile(t, rosterPath); got != testLegacyRoster {
		t.Fatalf("a refused migration rewrote the roster:\n%s", got)
	}
	model.lockCheck = func(string) (int, bool, error) { return 0, false, nil }

	// Enter writes the v2 policy the preview shows: the v1 text moves to the
	// backup and the screen reads the migrated roster.
	model = send(t, model, keyMsg("enter"))
	if model.dialog != dialogNone {
		t.Fatalf("the confirmed migration kept the dialog open")
	}
	if model.roster == nil || model.roster.Legacy || model.rosterPhase != rosterReady {
		t.Fatalf("the migrated roster is not loaded: phase=%v roster=%+v", model.rosterPhase, model.roster)
	}
	if migrated := readRosterTestFile(t, rosterPath); !strings.Contains(migrated, "version = 2") {
		t.Fatalf("the roster was not migrated:\n%s", migrated)
	}
	if backup := readRosterTestFile(t, rosterPath+".bak"); backup != testLegacyRoster {
		t.Fatalf("the backup does not keep the v1 text:\n%s", backup)
	}
	if !strings.Contains(model.notice, "мигрирован") || !strings.Contains(model.notice, "бэкап") {
		t.Fatalf("migration notice = %q", model.notice)
	}
	rendered = model.String()
	for _, want := range []string{"Доступы", "zai", "codex"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the migrated screen lost %q:\n%s", want, rendered)
		}
	}
}

// TestRosterMissingFileIsNamedNotCreated: without a roster.toml the screen
// names the file to create and the example to copy; it never writes a
// policy of its own, and the scan still works.
func TestRosterMissingFileIsNamedNotCreated(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	root, rosterPath := rosterFixture(t)
	if err := os.Remove(rosterPath); err != nil {
		t.Fatalf("removing the roster: %v", err)
	}
	model := rosterScreenModel(t, root, rosterPath)
	if model.roster == nil || !model.roster.Missing {
		t.Fatalf("the missing roster is not flagged: %+v", model.roster)
	}
	model = resize(t, model, 120, 40)
	rendered := model.String()
	for _, want := range []string{"Нет ростера:", "docs/design/roster.md", "u — собрать каталог"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the missing-roster screen lost %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(flatScreen(model), rosterPath) {
		t.Fatalf("the missing-roster screen does not name %s:\n%s", rosterPath, rendered)
	}
	if footer := rosterFooter(t, model); !strings.Contains(footer, "u scan") {
		t.Fatalf("the missing-roster footer lost the scan: %q", footer)
	}
	if _, err := os.Stat(rosterPath); !os.IsNotExist(err) {
		t.Fatalf("the screen created the policy: %v", err)
	}
	// n saves a variant from the conditions; a missing policy has none.
	model = send(t, model, keyMsg("n"))
	if model.dialog != dialogNone {
		t.Fatalf("n opened the variant dialog without a policy")
	}
	if _, err := os.Stat(rosterPath); !os.IsNotExist(err) {
		t.Fatalf("the screen wrote a roster after n: %v", err)
	}

	model.rosterScan = func(roster.ScanOptions) (*roster.Catalog, []string, error) {
		catalog, err := roster.LoadCatalog("")
		return catalog, nil, err
	}
	updated, cmd := model.Update(keyMsg("u"))
	model = updated.(Model)
	if cmd == nil || !model.rosterScanning {
		t.Fatalf("u did not scan without a policy")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if !strings.Contains(model.notice, "каталог:") {
		t.Fatalf("scan notice without a policy = %q", model.notice)
	}
	if _, err := os.Stat(rosterPath); !os.IsNotExist(err) {
		t.Fatalf("the scan created the policy: %v", err)
	}
}
