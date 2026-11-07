package roster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// workspacePolicyWorkspace loads the fixture policy with the scenario
// catalog and a fresh state: the model every policy operation test edits.
func workspacePolicyWorkspace(t *testing.T) *Workspace {
	t.Helper()
	path := workspacePolicy(t)
	workspaceCatalog(t)
	workspace, err := LoadWorkspace(path, "")
	if err != nil {
		t.Fatalf("LoadWorkspace() error = %v", err)
	}
	if workspace.Missing || workspace.Legacy {
		t.Fatalf("LoadWorkspace() = missing %t legacy %t, want a loaded policy", workspace.Missing, workspace.Legacy)
	}
	return workspace
}

// reload reloads the workspace from the files, so a test reads what the
// operation actually wrote.
func reloadWorkspace(t *testing.T, workspace *Workspace) *Workspace {
	t.Helper()
	next, err := LoadWorkspace(workspace.PolicyPath, workspace.Root)
	if err != nil {
		t.Fatalf("LoadWorkspace() error = %v", err)
	}
	return next
}

// TestSavePolicyRoundTrip: the fixture policy renders, parses back and
// renders identically; the accesses, routes and roles survive the trip.
func TestSavePolicyRoundTrip(t *testing.T) {
	workspace := workspacePolicyWorkspace(t)
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := SavePolicy(path, workspace.Policy); err != nil {
		t.Fatalf("SavePolicy() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	parsed, err := ParsePolicy(path, text)
	if err != nil {
		t.Fatalf("the written policy does not parse: %v\n%s", err, text)
	}
	if policyText(parsed) != text {
		t.Fatalf("the written policy does not render back as itself:\n%s", text)
	}
	if got, want := parsed.AccessNames(), workspace.Policy.AccessNames(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("accesses = %v, want %v", got, want)
	}
	if len(parsed.Routes) != len(workspace.Policy.Routes) {
		t.Fatalf("routes = %d, want %d", len(parsed.Routes), len(workspace.Policy.Routes))
	}
	for i, route := range parsed.Routes {
		if route != workspace.Policy.Routes[i] {
			t.Fatalf("route %d = %v, want %v", i, route, workspace.Policy.Routes[i])
		}
	}
}

// TestSavePolicyKeepsVariantsOut: a policy loaded with the variants merged
// saves only the presets of the policy layer — the variants files keep
// theirs.
func TestSavePolicyKeepsVariantsOut(t *testing.T) {
	workspace := workspacePolicyWorkspace(t)
	if err := SaveVariants(VariantsPath(workspace.PolicyPath), []Preset{{Name: "machine", Off: []string{"codex"}}}); err != nil {
		t.Fatal(err)
	}
	loaded := reloadWorkspace(t, workspace)
	if _, ok := loaded.Policy.Preset("machine"); !ok {
		t.Fatal("the merged policy lost the machine preset")
	}
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := SavePolicy(path, loaded.Policy); err != nil {
		t.Fatalf("SavePolicy() error = %v", err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "machine") {
		t.Fatalf("the saved policy carries a variants preset:\n%s", data)
	}
	if !strings.Contains(string(data), "[preset.low]") {
		t.Fatalf("the saved policy lost the policy preset:\n%s", data)
	}
}

// TestPolicyAccessOperations walks the access lifecycle: add from the
// catalog, budget, flags with the mutual exclusion, remove with the
// cleanup behind it.
func TestPolicyAccessOperations(t *testing.T) {
	workspace := workspacePolicyWorkspace(t)

	if err := workspace.AddAccess("extra", "local", []string{"pi"}); err != nil {
		t.Fatalf("AddAccess() error = %v", err)
	}
	if err := workspace.AddAccess("extra", "", []string{"pi"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("AddAccess() twice = %v, want already exists", err)
	}
	if err := workspace.AddAccess("bad kind", "free", []string{"pi"}); err == nil || !strings.Contains(err.Error(), "kind must be") {
		t.Fatalf("AddAccess(bad kind) = %v, want a kind error", err)
	}
	if err := workspace.AddAccess("noagents", "", nil); err == nil || !strings.Contains(err.Error(), "agents is required") {
		t.Fatalf("AddAccess(no agents) = %v, want an agents error", err)
	}

	reloaded := reloadWorkspace(t, workspace)
	access, ok := reloaded.Policy.Access("extra")
	if !ok || access.Kind != "local" || access.Budget != DefaultBudget || len(access.Agents) != 1 {
		t.Fatalf("access extra = %+v, want kind local, default budget, one agent", access)
	}

	if err := workspace.SetAccessBudget("extra", "lean"); err != nil {
		t.Fatalf("SetAccessBudget() error = %v", err)
	}
	if err := workspace.SetAccessBudget("extra", "weekly"); err == nil || !strings.Contains(err.Error(), "budget must be") {
		t.Fatalf("SetAccessBudget(weekly) = %v, want a budget error", err)
	}
	if err := workspace.SetAccessFlags("extra", true, true); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("SetAccessFlags(both) = %v, want the mutual exclusion", err)
	}
	if err := workspace.SetAccessFlags("extra", false, true); err != nil {
		t.Fatalf("SetAccessFlags(check) error = %v", err)
	}

	reloaded = reloadWorkspace(t, workspace)
	access, _ = reloaded.Policy.Access("extra")
	if access.Budget != BudgetLean || !access.Check || access.Trusted {
		t.Fatalf("access extra = %+v, want lean and check only", access)
	}

	// Removing zai drops its routes, its preset entries and the state
	// conditions, and no file keeps the name.
	if err := workspace.SetOff("zai", time.Time{}); err != nil {
		t.Fatalf("SetOff() error = %v", err)
	}
	if err := workspace.RemoveAccess("zai"); err != nil {
		t.Fatalf("RemoveAccess() error = %v", err)
	}
	reloaded = reloadWorkspace(t, workspace)
	if _, ok := reloaded.Policy.Access("zai"); ok {
		t.Fatal("RemoveAccess left the access behind")
	}
	for _, route := range reloaded.Policy.Routes {
		if route.Access == "zai" {
			t.Fatalf("RemoveAccess left a route behind: %v", route)
		}
	}
	if preset, ok := reloaded.Policy.Preset("low"); ok && contains(preset.Off, "zai") {
		t.Fatalf("preset low still names zai: %+v", preset)
	}
	if data, _ := os.ReadFile(StatePath(reloaded.PolicyPath)); strings.Contains(string(data), "zai") {
		t.Fatalf("the state still names zai:\n%s", data)
	}
	if err := workspace.RemoveAccess("zai"); err == nil || !strings.Contains(err.Error(), "unknown access") {
		t.Fatalf("RemoveAccess(again) = %v, want unknown access", err)
	}
}

// TestPolicyModelOperations adds a model with its first route, adds a
// level, edits the route, removes it, and refuses the unknowns.
func TestPolicyModelOperations(t *testing.T) {
	workspace := workspacePolicyWorkspace(t)

	if err := workspace.AddAccessModel("zai", "glm-5.3-thinking"); err == nil || !strings.Contains(err.Error(), "not in the catalog") {
		t.Fatalf("AddAccessModel(outside catalog) = %v, want a catalog error", err)
	}
	if err := workspace.AddAccessModel("nope", "glm-5.3"); err == nil || !strings.Contains(err.Error(), "unknown access") {
		t.Fatalf("AddAccessModel(unknown access) = %v, want unknown access", err)
	}
	// The zai allowlist is the glob glm-5.3*: the exact id is covered
	// already, and the refusal says so.
	if err := workspace.AddAccessModel("zai", "glm-5.3"); err == nil || !strings.Contains(err.Error(), "already serves") {
		t.Fatalf("AddAccessModel(covered by glob) = %v, want already serves", err)
	}
	// The local access has no allowlist and its catalog model carries no
	// routes: adding it creates the first route at "-" and the automatic
	// class.
	if err := workspace.AddAccessModel("local", "strata/swift-1.5-iq3_s"); err != nil {
		t.Fatalf("AddAccessModel() error = %v", err)
	}
	reloaded := reloadWorkspace(t, workspace)
	entry, _ := reloaded.Policy.Access("local")
	if !contains(entry.Models, "strata/swift-1.5-iq3_s") {
		t.Fatalf("local models = %v, want the model added", entry.Models)
	}
	fresh := reloaded.Policy.routesOf("local", "strata/swift-1.5-iq3_s")
	if len(fresh) != 1 || fresh[0] != (Route{Access: "local", Model: "strata/swift-1.5-iq3_s", Eff: "-", Class: familyClass("strata/swift-1.5-iq3_s")}) {
		t.Fatalf("first route = %v, want a dash route at the automatic class", fresh)
	}
	// A model without declared levels has no effort to add a level for.
	if err := workspace.AddRouteLevel("local", "strata/swift-1.5-iq3_s"); err == nil || !strings.Contains(err.Error(), "every declared effort") {
		t.Fatalf("AddRouteLevel(no levels) = %v, want an exhaustion error", err)
	}

	// gpt-6-luna is routed in the fixture but missing from the codex
	// allowlist: the addition joins the allowlist and keeps the routes.
	if err := workspace.AddAccessModel("codex", "gpt-6-luna"); err != nil {
		t.Fatalf("AddAccessModel() error = %v", err)
	}
	reloaded = reloadWorkspace(t, workspace)
	entry, _ = reloaded.Policy.Access("codex")
	if !contains(entry.Models, "gpt-6-luna") {
		t.Fatalf("codex models = %v, want gpt-6-luna added", entry.Models)
	}
	routes := reloaded.Policy.routesOf("codex", "gpt-6-luna")
	if len(routes) != 1 || routes[0].Eff != "low" {
		t.Fatalf("routes of the joined model = %v, want the fixture route kept", routes)
	}

	// The second level lands next to the first; the effort rename refuses
	// a collision; the class change sticks; the removal is by key.
	if err := workspace.AddRouteLevel("codex", "gpt-6-luna"); err != nil {
		t.Fatalf("AddRouteLevel() error = %v", err)
	}
	reloaded = reloadWorkspace(t, workspace)
	routes = reloaded.Policy.routesOf("codex", "gpt-6-luna")
	if len(routes) != 2 {
		t.Fatalf("routes after AddRouteLevel = %v, want two", routes)
	}
	if err := workspace.SetRouteEffort("codex", "gpt-6-luna", routes[1].Eff, routes[0].Eff); err == nil || !strings.Contains(err.Error(), "already has a route") {
		t.Fatalf("SetRouteEffort(collision) = %v, want a collision error", err)
	}
	if err := workspace.SetRouteEffort("codex", "gpt-6-luna", routes[1].Eff, " "); err == nil || !strings.Contains(err.Error(), "single word") {
		t.Fatalf("SetRouteEffort(bad key) = %v, want a key error", err)
	}
	if err := workspace.SetRouteClass("codex", "gpt-6-luna", routes[1].Eff, "medium"); err == nil || !strings.Contains(err.Error(), "class must be") {
		t.Fatalf("SetRouteClass(medium) = %v, want a class error", err)
	}
	if err := workspace.SetRouteClass("codex", "gpt-6-luna", routes[1].Eff, "strong"); err != nil {
		t.Fatalf("SetRouteClass() error = %v", err)
	}
	if err := workspace.RemoveRoute("codex", "gpt-6-luna", "nope"); err == nil || !strings.Contains(err.Error(), "no route at effort") {
		t.Fatalf("RemoveRoute(unknown effort) = %v, want an effort error", err)
	}
	if err := workspace.RemoveRoute("codex", "gpt-6-luna", routes[1].Eff); err != nil {
		t.Fatalf("RemoveRoute() error = %v", err)
	}
	if err := workspace.RemoveAccessModel("codex", "gpt-6-luna"); err != nil {
		t.Fatalf("RemoveAccessModel() error = %v", err)
	}
	reloaded = reloadWorkspace(t, workspace)
	entry, _ = reloaded.Policy.Access("codex")
	if contains(entry.Models, "gpt-6-luna") || len(reloaded.Policy.routesOf("codex", "gpt-6-luna")) != 0 {
		t.Fatalf("the model removal left %v / %v behind", entry.Models, reloaded.Policy.routesOf("codex", "gpt-6-luna"))
	}
}

// TestPolicyRefusalKeepsFiles: a refused operation changes neither the
// file nor the model.
func TestPolicyRefusalKeepsFiles(t *testing.T) {
	workspace := workspacePolicyWorkspace(t)
	before, err := os.ReadFile(workspace.PolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	model := *workspace.Policy
	if err := workspace.SetRole("executor", "trusted-not-a-tier"); err == nil {
		t.Fatal("SetRole(bad requirement) = nil, want an error")
	}
	if err := workspace.SetRole("boss", "cheap"); err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("SetRole(unknown role) = %v, want unknown role", err)
	}
	after, _ := os.ReadFile(workspace.PolicyPath)
	if string(before) != string(after) {
		t.Fatalf("a refused operation rewrote the file:\n%s", after)
	}
	if len(workspace.Policy.Routes) != len(model.Routes) || workspace.Policy.Roles["executor"] != model.Roles["executor"] {
		t.Fatal("a refused operation changed the model")
	}
}

// TestVariantOperations walks the variant lifecycle the editor drives:
// create empty and from a copy, budget, narrowing, order, rename.
func TestVariantOperations(t *testing.T) {
	workspace := workspacePolicyWorkspace(t)

	if err := workspace.CreateVariant("quiet", false, ""); err != nil {
		t.Fatalf("CreateVariant() error = %v", err)
	}
	if err := workspace.CreateVariant("quiet", false, ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("CreateVariant(again) = %v, want already exists", err)
	}
	if err := workspace.CreateVariant("two words", false, ""); err == nil || !strings.Contains(err.Error(), "single word") {
		t.Fatalf("CreateVariant(two words) = %v, want a name error", err)
	}
	if err := workspace.CreateVariant("copied", false, "missing"); err == nil || !strings.Contains(err.Error(), "unknown variant") {
		t.Fatalf("CreateVariant(copy of missing) = %v, want unknown variant", err)
	}
	if err := workspace.CreateVariant("frompolicy", false, "low"); err == nil || !strings.Contains(err.Error(), "hand-edited") {
		t.Fatalf("CreateVariant(copy of policy preset) = %v, want a refusal", err)
	}
	// A fresh variant orders every access, so the editor can move them.
	reloaded := reloadWorkspace(t, workspace)
	preset, ok := reloaded.Policy.Preset("quiet")
	if !ok {
		t.Fatal("the created variant is missing")
	}
	if strings.Join(preset.Order, ",") != strings.Join(reloaded.Policy.AccessNames(), ",") {
		t.Fatalf("fresh order = %v, want every access %v", preset.Order, reloaded.Policy.AccessNames())
	}

	if err := workspace.CreateVariant("copied", false, "quiet"); err != nil {
		t.Fatalf("CreateVariant(copy) error = %v", err)
	}
	if err := workspace.SetVariantBudget("quiet", "codex", BudgetOff); err != nil {
		t.Fatalf("SetVariantBudget() error = %v", err)
	}
	if err := workspace.SetVariantBudget("quiet", "codex", "normal"); err == nil || !strings.Contains(err.Error(), "preset carries") {
		t.Fatalf("SetVariantBudget(normal) = %v, want a refusal", err)
	}
	if err := workspace.SetVariantBudget("quiet", "nope", BudgetOff); err == nil || !strings.Contains(err.Error(), "unknown access") {
		t.Fatalf("SetVariantBudget(unknown access) = %v, want unknown access", err)
	}
	if err := workspace.SetVariantBudget("quiet", "codex", ""); err != nil {
		t.Fatalf("SetVariantBudget(clear) error = %v", err)
	}
	if err := workspace.SetVariantBudget("quiet", "codex", BudgetLean); err != nil {
		t.Fatalf("SetVariantBudget(lean) error = %v", err)
	}

	if err := workspace.ToggleVariantModel("quiet", "codex", "gpt-6.1-sol"); err != nil {
		t.Fatalf("ToggleVariantModel() error = %v", err)
	}
	if err := workspace.ToggleVariantModel("quiet", "nope", "m"); err == nil || !strings.Contains(err.Error(), "unknown access") {
		t.Fatalf("ToggleVariantModel(unknown access) = %v, want unknown access", err)
	}
	reloaded = reloadWorkspace(t, workspace)
	preset, _ = reloaded.Policy.Preset("quiet")
	// The codex access serves gpt-6.1-sol and gpt-6-luna; narrowing to
	// every model except the toggled one leaves only gpt-6-luna.
	if got := preset.Models["codex"]; len(got) != 1 || got[0] != "gpt-6-luna" {
		t.Fatalf("narrowing = %v, want [gpt-6-luna]", got)
	}
	if err := workspace.ToggleVariantModel("quiet", "codex", "gpt-6.1-sol"); err != nil {
		t.Fatalf("ToggleVariantModel(back) error = %v", err)
	}
	reloaded = reloadWorkspace(t, workspace)
	preset, _ = reloaded.Policy.Preset("quiet")
	if _, narrowed := preset.Models["codex"]; narrowed {
		t.Fatalf("narrowing = %v, want it dropped when every model is back", preset.Models["codex"])
	}

	if err := workspace.MoveVariantAccess("quiet", "codex", "up"); err != nil {
		t.Fatalf("MoveVariantAccess() error = %v", err)
	}
	if err := workspace.MoveVariantAccess("quiet", "codex", "sideways"); err == nil || !strings.Contains(err.Error(), "up or down") {
		t.Fatalf("MoveVariantAccess(sideways) = %v, want a direction error", err)
	}
	reloaded = reloadWorkspace(t, workspace)
	preset, _ = reloaded.Policy.Preset("quiet")
	at := -1
	for i, name := range preset.Order {
		if name == "codex" {
			at = i
		}
	}
	if at != 1 {
		t.Fatalf("order = %v, want codex one step up from %d", preset.Order, at)
	}
	// The top access cannot move up; nothing changes and nothing fails.
	before := append([]string(nil), preset.Order...)
	if err := workspace.MoveVariantAccess("quiet", "codex", "up"); err != nil {
		t.Fatalf("MoveVariantAccess(top) error = %v", err)
	}
	if strings.Join(preset.Order, ",") != strings.Join(before, ",") {
		t.Fatalf("order = %v, want it kept at the edge", preset.Order)
	}

	if err := workspace.RenameVariant("missing", "other"); err == nil || !strings.Contains(err.Error(), "unknown variant") {
		t.Fatalf("RenameVariant(missing) = %v, want unknown variant", err)
	}
	if err := workspace.RenameVariant("quiet", "low"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("RenameVariant(into policy preset) = %v, want already exists", err)
	}
	if err := workspace.SetPreset("quiet"); err != nil {
		t.Fatalf("SetPreset() error = %v", err)
	}
	if err := workspace.RenameVariant("quiet", "silent"); err != nil {
		t.Fatalf("RenameVariant() error = %v", err)
	}
	reloaded = reloadWorkspace(t, workspace)
	if reloaded.State == nil || reloaded.State.Preset != "silent" {
		t.Fatalf("active preset = %q, want the rename to carry it", reloaded.Preset)
	}
	if _, ok := reloaded.Policy.Preset("quiet"); ok {
		t.Fatal("the old variant name survived the rename")
	}
	// A policy preset stays readonly for the variant operations.
	if err := workspace.RenameVariant("low", "lower"); err == nil || !strings.Contains(err.Error(), "roster.toml") {
		t.Fatalf("RenameVariant(policy preset) = %v, want a refusal", err)
	}
	if err := workspace.SetVariantBudget("low", "codex", BudgetOff); err == nil || !strings.Contains(err.Error(), "roster.toml") {
		t.Fatalf("SetVariantBudget(policy preset) = %v, want a refusal", err)
	}
}
