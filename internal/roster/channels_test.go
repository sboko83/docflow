package roster

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestScanAuthPerChannel: the auth of every channel is kept next to the
// folded access auth, and OpenCode's "auto" activation (a stored login)
// is ready — only "disabled" is off.
func TestScanAuthPerChannel(t *testing.T) {
	_, bin := scanEnv(t)
	installCommand(t, bin, "pi", "printf '%s\\n' '{\"status\":\"not_ready\",\"provider\":\"opencode-go\",\"reason\":\"credentials_not_configured\"}'; exit 1\n")
	installCommand(t, bin, "opencode", "printf '%s\\n' '{\"data\":[{\"id\":\"opencode-go\",\"activation\":\"auto\"},{\"id\":\"opencode\",\"activation\":\"disabled\"}]}'\n")

	catalog := emptyCatalog()
	catalog.Merge(&Catalog{Accesses: []AccessInfo{
		{Access: "opencode-go", Agents: []string{"pi", "opencode"}, Source: SourcePiStore},
		{Access: "opencode", Agents: []string{"opencode"}, Source: SourceOpenCode},
	}})
	if problems := ScanAuth(catalog); len(problems) != 0 {
		t.Fatalf("ScanAuth() problems = %v, want none", problems)
	}
	goAccess := findAccess(t, catalog, "opencode-go")
	if goAccess.Auth != AuthReady {
		t.Fatalf("opencode-go auth = %q, want ready: activation auto is a stored login", goAccess.Auth)
	}
	if want := map[string]string{"pi": AuthNotReady, "opencode": AuthReady}; !reflect.DeepEqual(goAccess.Channels, want) {
		t.Fatalf("opencode-go channels = %v, want %v", goAccess.Channels, want)
	}
	if zen := findAccess(t, catalog, "opencode"); zen.Auth != AuthNotReady {
		t.Fatalf("opencode auth = %q, want not_ready for a disabled provider", zen.Auth)
	}
}

// TestScanAuthCodexAPIKey: a Codex login with an API key pays per token,
// and the key prefix of the status line never reaches the catalog.
func TestScanAuthCodexAPIKey(t *testing.T) {
	_, bin := scanEnv(t)
	installCommand(t, bin, "codex", "printf 'Logged in using an API key - sk-fixture***\\n'\n")
	catalog := BuiltinCatalog()
	if problems := ScanAuth(catalog); len(problems) != 0 && !strings.Contains(strings.Join(problems, " "), "claude") {
		t.Fatalf("ScanAuth() problems = %v", problems)
	}
	codex := findAccess(t, catalog, "codex")
	if codex.Kind != KindMetered || codex.Auth != AuthReady {
		t.Fatalf("codex = %+v, want a ready metered access", codex)
	}
	if text := catalogJSON(t, catalog); strings.Contains(text, "sk-fixture") {
		t.Fatalf("catalog leaked the key prefix: %s", text)
	}
}

// TestScanKinds: the scan names the kind of the accesses it recognizes,
// a models.json provider on the LAN is local, and an unknown provider has
// no kind rather than a guess.
func TestScanKinds(t *testing.T) {
	home, _ := scanEnv(t)
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := `{"opencode":{"models":[{"id":"deepseek-v4-flash"}]},"opencode-go":{"models":[{"id":"glm-5.3"}]},"zai":{"models":[{"id":"glm-5.3"}]},"acme":{"models":[{"id":"m1"}]}}`
	config := `{"providers":{"strata":{"baseUrl":"http://192.168.1.100:8081/v1","apiKey":"secret-fixture","models":[{"id":"swift"}]},"cloud":{"baseUrl":"https://api.example.com/v1","models":[{"id":"c1"}]}}}`
	for name, text := range map[string]string{"models-store.json": store, "models.json": config} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	catalog, problems := ScanPi()
	if len(problems) != 0 {
		t.Fatalf("ScanPi() problems = %v", problems)
	}
	want := map[string]string{"opencode": KindMetered, "opencode-go": KindSubscription, "zai": KindSubscription, "acme": "", "strata": KindLocal, "cloud": ""}
	for name, kind := range want {
		if got := findAccess(t, catalog, name).Kind; got != kind {
			t.Fatalf("%s kind = %q, want %q", name, got, kind)
		}
	}
	if model := findModel(t, catalog, "strata", "swift"); !reflect.DeepEqual(model.Agents, []string{"pi"}) {
		t.Fatalf("strata/swift agents = %v, want the pi channel", model.Agents)
	}
	if text := catalogJSON(t, catalog); strings.Contains(text, "192.168.1.100") || strings.Contains(text, "secret-fixture") {
		t.Fatalf("catalog stored the base URL or the key: %s", text)
	}
	if AccessTitle("opencode") != "OpenCode Zen" || AccessTitle("opencode-go") != "OpenCode Go" || AccessTitle("acme") != "" {
		t.Fatalf("titles = %q, %q, %q", AccessTitle("opencode"), AccessTitle("opencode-go"), AccessTitle("acme"))
	}
}

func TestLocalBaseURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://127.0.0.1:8080/v1":    true,
		"http://localhost:11434":      true,
		"http://192.168.1.72:8080":    true,
		"http://10.0.0.5/v1":          true,
		"http://strata:8081/v1":       true,
		"http://box.local:8080":       true,
		"http://[::1]:8080":           true,
		"https://api.z.ai/api/coding": false,
		"https://8.8.8.8/v1":          false,
		"":                            false,
	} {
		if got := localBaseURL(raw); got != want {
			t.Errorf("localBaseURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

// TestRouteAgent: a route runs through the first channel that lists the
// model and has a login; a refused channel never runs it, and an older
// catalog without per-channel answers keeps the first channel.
func TestRouteAgent(t *testing.T) {
	both := []string{"pi", "opencode"}
	piOnly := ModelInfo{ID: "deepseek-v4-flash", Agents: []string{"pi"}}
	everywhere := ModelInfo{ID: "glm-5.3", Agents: []string{"pi", "opencode"}}
	unknown := ModelInfo{ID: "kimi"}
	piRefused := AccessInfo{Auth: AuthReady, Channels: map[string]string{"pi": AuthNotReady, "opencode": AuthReady}}
	for _, tc := range []struct {
		name   string
		agents []string
		info   AccessInfo
		model  ModelInfo
		agent  string
		reason string
	}{
		{"first ready", both, AccessInfo{Channels: map[string]string{"pi": AuthReady, "opencode": AuthReady}}, everywhere, "pi", ""},
		{"skip refused channel", both, piRefused, everywhere, "opencode", ""},
		{"model only on refused channel", both, piRefused, piOnly, "", "no channel with auth (pi not ready)"},
		{"model unknown to the channels", []string{"opencode"}, piRefused, piOnly, "", "listed only by pi"},
		{"older catalog", both, AccessInfo{Auth: AuthReady}, unknown, "pi", ""},
		{"unknown before refused", both, AccessInfo{Channels: map[string]string{"pi": AuthNotReady, "opencode": AuthUnknown}}, everywhere, "opencode", ""},
	} {
		agent, reason := RouteAgent(tc.agents, tc.info, tc.model)
		if agent != tc.agent || (tc.reason == "") != (reason == "") || !strings.Contains(reason, tc.reason) {
			t.Errorf("%s: RouteAgent = (%q, %q), want (%q, …%q…)", tc.name, agent, reason, tc.agent, tc.reason)
		}
	}
}

// TestWorkspaceSetAccessAgent: the chosen channel goes first in the
// policy, an agent that is not a channel of the access is refused.
func TestWorkspaceSetAccessAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roster.toml")
	writeRosterFile(t, path, "version = 4\n\n[access.opencode-go]\nagents = [\"pi\", \"opencode\"]\n\n[access.opencode-go.roles]\nexecutor = \"glm-5.3@high\"\n\n[access.codex]\nagents = [\"codex\"]\n\n[access.codex.roles]\nreview = \"gpt-6.1-sol@medium\"\n")
	workspace := loadWorkspaceOrFail(t, path, t.TempDir())
	if err := workspace.SetAccessAgent("opencode-go", "opencode"); err != nil {
		t.Fatalf("SetAccessAgent() error = %v", err)
	}
	policy, err := LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	access, _ := policy.Access("opencode-go")
	if !reflect.DeepEqual(access.Agents, []string{"opencode", "pi"}) {
		t.Fatalf("agents = %v, want opencode first", access.Agents)
	}
	workspace = loadWorkspaceOrFail(t, path, t.TempDir())
	if err := workspace.SetAccessAgent("opencode-go", "claude"); err == nil || !strings.Contains(err.Error(), "not one of its channels") {
		t.Fatalf("SetAccessAgent(claude) error = %v, want a refusal", err)
	}
}

// TestWorkspaceAddAccessCreatesRoster: on a missing roster the first
// access creates the file (and its directory); a refused first access
// leaves the roster missing.
func TestWorkspaceAddAccessCreatesRoster(t *testing.T) {
	workspaceCatalog(t)
	path := filepath.Join(t.TempDir(), "new", "roster.toml")
	workspace := loadWorkspaceOrFail(t, path, t.TempDir())
	if err := workspace.AddAccess("opencode-go", "", []string{"nope"}); err == nil {
		t.Fatal("AddAccess(unknown agent) error = nil, want a refusal")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) || !workspace.Missing || workspace.Policy != nil {
		t.Fatalf("a refused first access created the roster: stat %v, missing %v", err, workspace.Missing)
	}
	if err := workspace.AddAccess("opencode-go", "", []string{"opencode"}); err != nil {
		t.Fatalf("AddAccess() error = %v", err)
	}
	reloaded := loadWorkspaceOrFail(t, path, t.TempDir())
	if reloaded.Missing || len(reloaded.Accesses) != 1 || reloaded.Accesses[0].Access != "opencode-go" {
		t.Fatalf("reloaded workspace = missing %v, accesses %+v", reloaded.Missing, reloaded.Accesses)
	}
	if err := reloaded.SetCell("opencode-go", "executor", "glm-5.3@high"); err != nil {
		t.Fatalf("SetCell() on the new roster error = %v", err)
	}
}
