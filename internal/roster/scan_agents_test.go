package roster

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

// authCatalog builds the accesses the auth probes work on, with the
// channels a merged Pi/OpenCode/built-in scan would supply.
func authCatalog() *Catalog {
	catalog := emptyCatalog()
	catalog.Sources = []string{SourcePiStore, SourceOpenCode, SourceBuiltin}
	catalog.Merge(&Catalog{Accesses: []AccessInfo{
		{Access: "claude", Agents: []string{"claude"}, Source: SourceBuiltin},
		{Access: "codex", Agents: []string{"codex"}, Source: SourceBuiltin},
		{Access: "opencode", Agents: []string{"opencode"}, Source: SourceOpenCode},
		{Access: "opencode-go", Agents: []string{"pi", "opencode"}, Source: SourcePiStore},
		{Access: "strata", Agents: []string{"pi"}, Source: SourcePiStore},
		{Access: "zai", Agents: []string{"pi"}, Source: SourcePiStore},
	}})
	catalog.Sort()
	return catalog
}

// installAuthCommands puts the three fake CLIs on the isolated PATH and
// records every invocation in one log, so a test can prove which probes
// ran and which never did.
func installAuthCommands(t *testing.T, bin string) (logPath string) {
	t.Helper()
	logPath = filepath.ToSlash(filepath.Join(t.TempDir(), "probe.log"))
	record := "printf '%s\\n' \"$*\" >> '" + logPath + "'\n"
	installCommand(t, bin, "pi", record+
		"case \"$*\" in\n"+
		"  'auth check --provider zai --json') printf '%s\\n' '{\"status\":\"ready\",\"provider\":\"zai\",\"authType\":\"api_key\"}'; exit 0 ;;\n"+
		"  'auth check --provider opencode-go --json') printf '%s\\n' '{\"status\":\"ready\",\"provider\":\"opencode-go\",\"authType\":\"api_key\"}'; exit 0 ;;\n"+
		"  'auth check --provider strata --json') printf '%s\\n' '{\"status\":\"not_ready\",\"provider\":\"strata\",\"reason\":\"credentials_not_configured\"}'; exit 1 ;;\n"+
		"  *) printf 'unexpected pi invocation\\n' >&2; exit 3 ;;\n"+
		"esac\n")
	installCommand(t, bin, "opencode", record+
		"case \"$*\" in\n"+
		"  'api get /api/provider') printf '%s\\n' '{\"location\":{\"directory\":\"/tmp\"},\"data\":[{\"id\":\"opencode-go\",\"integrationID\":\"opencode\",\"activation\":\"enabled\",\"headers\":{\"x-opencode-org-id\":\"wrk_fixture_org_secret\"}},{\"id\":\"opencode\",\"integrationID\":\"opencode\",\"activation\":\"disabled\"}]}'; exit 0 ;;\n"+
		"  *) printf 'unexpected opencode invocation\\n' >&2; exit 3 ;;\n"+
		"esac\n")
	installCommand(t, bin, "codex", record+
		"case \"$*\" in\n"+
		"  'login status') printf 'Logged in using ChatGPT\\n'; exit 0 ;;\n"+
		"  *) printf 'unexpected codex invocation\\n' >&2; exit 3 ;;\n"+
		"esac\n")
	return logPath
}

func TestScanAuthChannels(t *testing.T) {
	home, bin := scanEnv(t)
	logPath := installAuthCommands(t, bin)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write claude config: %v", err)
	}

	catalog := authCatalog()
	problems := ScanAuth(catalog)
	if len(problems) != 0 {
		t.Fatalf("ScanAuth() problems = %v, want none", problems)
	}

	want := map[string]string{
		"claude":      AuthReady,    // config present
		"codex":       AuthReady,    // codex login status exits 0
		"opencode":    AuthNotReady, // activation is disabled
		"opencode-go": AuthReady,    // pi and opencode both ready
		"strata":      AuthNotReady, // pi says the credentials are missing
		"zai":         AuthReady,    // pi says ready
	}
	for name, status := range want {
		access := findAccess(t, catalog, name)
		if access.Auth != status {
			t.Fatalf("%s auth = %q, want %q (%+v)", name, access.Auth, status, access)
		}
		if access.Provenance["auth"] != SourceAuth || access.Source != SourceAuth {
			t.Fatalf("%s provenance/source = %v/%q, want the auth probe", name, access.Provenance, access.Source)
		}
	}
	if !reflect.DeepEqual(catalog.Sources, []string{SourcePiStore, SourceOpenCode, SourceBuiltin, SourceAuth}) {
		t.Fatalf("Sources = %v, want the auth source last", catalog.Sources)
	}

	// The OpenCode answer carries an org ID in its headers: it must not
	// reach the catalog.
	if text := catalogJSON(t, catalog); strings.Contains(text, "wrk_fixture_org_secret") {
		t.Fatalf("catalog leaked the provider headers: %s", text)
	}

	recorded, err := os.ReadFile(filepath.FromSlash(logPath))
	if err != nil {
		t.Fatalf("read probe log: %v", err)
	}
	log := string(recorded)
	if !strings.Contains(log, "auth check --provider zai --json") {
		t.Fatalf("probe log = %q, want the pi auth check", log)
	}
	if strings.Contains(log, "print-api-key") || strings.Contains(log, "print-bearer-token") {
		t.Fatalf("probe log = %q, want no credential-printing command", log)
	}
	if !strings.Contains(log, "api get /api/provider") || !strings.Contains(log, "login status") {
		t.Fatalf("probe log = %q, want the opencode and codex probes", log)
	}
}

func TestScanAuthAnyChannelReady(t *testing.T) {
	home, bin := scanEnv(t)
	// pi does not know this subscription; the opencode channel does.
	installCommand(t, bin, "pi", "printf '%s\\n' '{\"status\":\"not_ready\",\"provider\":\"opencode-go\",\"reason\":\"credentials_not_configured\"}'; exit 1\n")
	installCommand(t, bin, "opencode", "printf '%s\\n' '{\"data\":[{\"id\":\"opencode-go\",\"activation\":\"enabled\"}]}'\n")
	_ = home

	catalog := emptyCatalog()
	catalog.Merge(&Catalog{Accesses: []AccessInfo{{Access: "opencode-go", Agents: []string{"pi", "opencode"}, Source: SourcePiStore}}})
	problems := ScanAuth(catalog)
	if len(problems) != 0 {
		t.Fatalf("ScanAuth() problems = %v, want none", problems)
	}
	// One access, two doors: a ready channel makes the access usable.
	if access := findAccess(t, catalog, "opencode-go"); access.Auth != AuthReady {
		t.Fatalf("opencode-go auth = %q, want ready through opencode", access.Auth)
	}
	if !reflect.DeepEqual(findAccess(t, catalog, "opencode-go").Agents, []string{"pi", "opencode"}) {
		t.Fatalf("opencode-go agents = %v, want both channels", findAccess(t, catalog, "opencode-go").Agents)
	}
}

func TestScanAuthUnknown(t *testing.T) {
	scanEnv(t) // no CLIs, no configs

	catalog := emptyCatalog()
	catalog.Merge(&Catalog{Accesses: []AccessInfo{
		{Access: "claude", Agents: []string{"claude"}, Source: SourceBuiltin},
		{Access: "codex", Agents: []string{"codex"}, Source: SourceBuiltin},
		{Access: "zai", Agents: []string{"pi"}, Source: SourcePiStore},
	}})
	problems := ScanAuth(catalog)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want one warning for pi and one for codex", problems)
	}
	if !strings.Contains(problems[0], "pi") || !strings.Contains(problems[1], "codex") {
		t.Fatalf("problems = %v, want a pi and a codex warning", problems)
	}
	for _, name := range []string{"claude", "codex", "zai"} {
		if access := findAccess(t, catalog, name); access.Auth != AuthUnknown {
			t.Fatalf("%s auth = %q, want unknown", name, access.Auth)
		}
	}
	if access := findAccess(t, catalog, "claude"); access.Provenance["auth"] != SourceAuth {
		t.Fatalf("claude provenance = %v, want the auth probe", access.Provenance)
	}
}

func TestScanAuthBrokenPiAnswer(t *testing.T) {
	_, bin := scanEnv(t)
	installCommand(t, bin, "pi", "printf 'no json here\\n'; exit 0\n")

	catalog := emptyCatalog()
	catalog.Merge(&Catalog{Accesses: []AccessInfo{{Access: "zai", Agents: []string{"pi"}, Source: SourcePiStore}}})
	problems := ScanAuth(catalog)
	if len(problems) != 1 || !strings.Contains(problems[0], "pi auth check") {
		t.Fatalf("problems = %v, want one pi auth check warning", problems)
	}
	if access := findAccess(t, catalog, "zai"); access.Auth != AuthUnknown {
		t.Fatalf("zai auth = %q, want unknown", access.Auth)
	}
}

func TestScanAuthCodexLoggedOut(t *testing.T) {
	_, bin := scanEnv(t)
	installCommand(t, bin, "codex", "printf 'Not logged in\\n'; exit 1\n")

	catalog := emptyCatalog()
	catalog.Merge(&Catalog{Accesses: []AccessInfo{{Access: "codex", Agents: []string{"codex"}, Source: SourceBuiltin}}})
	problems := ScanAuth(catalog)
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none for a logged-out CLI", problems)
	}
	if access := findAccess(t, catalog, "codex"); access.Auth != AuthNotReady {
		t.Fatalf("codex auth = %q, want not_ready", access.Auth)
	}
}

func TestScanAuthClaudeVersion(t *testing.T) {
	_, bin := scanEnv(t)
	installCommand(t, bin, "claude", "case \"$*\" in '--version') printf 'claude 9.9.9\\n'; exit 0 ;; *) exit 3 ;; esac\n")

	catalog := emptyCatalog()
	catalog.Merge(&Catalog{Accesses: []AccessInfo{{Access: "claude", Agents: []string{"claude"}, Source: SourceBuiltin}}})
	problems := ScanAuth(catalog)
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if access := findAccess(t, catalog, "claude"); access.Auth != AuthReady {
		t.Fatalf("claude auth = %q, want ready through --version", access.Auth)
	}
}

func TestScanAuthClaudeBrokenCLI(t *testing.T) {
	_, bin := scanEnv(t)
	installCommand(t, bin, "claude", "printf 'boom\\n' >&2; exit 2\n")

	catalog := emptyCatalog()
	catalog.Merge(&Catalog{Accesses: []AccessInfo{{Access: "claude", Agents: []string{"claude"}, Source: SourceBuiltin}}})
	problems := ScanAuth(catalog)
	if len(problems) != 1 || !strings.Contains(problems[0], "claude --version") {
		t.Fatalf("problems = %v, want one claude warning", problems)
	}
	if access := findAccess(t, catalog, "claude"); access.Auth != AuthUnknown {
		t.Fatalf("claude auth = %q, want unknown", access.Auth)
	}
}

// enrichCatalog is the catalog the enrichment tests start from: one model
// whose fields a scanner already knows, one it does not, one on an access
// models.dev does not publish, and one model models.dev never heard of.
func enrichCatalog() *Catalog {
	catalog := emptyCatalog()
	catalog.Merge(&Catalog{Models: []ModelInfo{
		{Access: "zai", ID: "glm-5.3", Context: 262144, Reasoning: true, Source: SourcePiModels, Provenance: map[string]string{"context": SourcePiModels, "reasoning": SourcePiModels}},
		{Access: "zai", ID: "glm-5.3-flash"},
		{Access: "zai", ID: "glm-9-unknown"},
		{Access: "opencode-go", ID: "deepseek-v4.1-flash"},
		{Access: "strata", ID: "swift-1.5-iq3_s", Context: 262144, Source: SourcePiModels},
	}})
	catalog.Sort()
	return catalog
}

func TestEnrichFillsGaps(t *testing.T) {
	catalog := enrichCatalog()
	before := len(catalog.Models)

	filled := enrichFromModelsDev(catalog, fixture(t, "models-dev.json"))
	if filled != 3 {
		t.Fatalf("filled = %d, want the three models models.dev knows", filled)
	}
	if len(catalog.Models) != before {
		t.Fatalf("models = %d, want the enrichment to add no model", len(catalog.Models))
	}

	// A known context is never replaced by the aggregate; the price,
	// efforts and modalities it supplies fill the gaps.
	glm := findModel(t, catalog, "zai", "glm-5.3")
	if glm.Context != 262144 || glm.MaxOut != 131072 {
		t.Fatalf("glm-5.3 context/max-out = %d/%d, want the scanner context and the models.dev output", glm.Context, glm.MaxOut)
	}
	if glm.Cost == nil || glm.Cost.Input != 1.4 || glm.Cost.Output != 4.4 || glm.Cost.CacheRead != 0.26 {
		t.Fatalf("glm-5.3 cost = %+v, want the zai price, not the zhipuai one", glm.Cost)
	}
	if !reflect.DeepEqual(glm.Efforts, []string{"low", "high", "max"}) || !reflect.DeepEqual(glm.Input, []string{"text"}) {
		t.Fatalf("glm-5.3 efforts/input = %v/%v, want the models.dev values", glm.Efforts, glm.Input)
	}
	if glm.Source != SourceModelsDev {
		t.Fatalf("glm-5.3 source = %q, want the last contributor", glm.Source)
	}
	if glm.Provenance["context"] != SourcePiModels || glm.Provenance["cost"] != SourceModelsDev || glm.Provenance["efforts"] != SourceModelsDev {
		t.Fatalf("glm-5.3 provenance = %v, want per-field sources", glm.Provenance)
	}

	// The provider names differ: opencode-go is published under opencode,
	// and only the explicit mapping finds it.
	flash := findModel(t, catalog, "opencode-go", "deepseek-v4.1-flash")
	if flash.Context != 1000000 || flash.MaxOut != 384000 || !flash.Reasoning {
		t.Fatalf("deepseek-v4.1-flash = %+v, want the opencode metadata", flash)
	}
	if flash.Cost == nil || flash.Cost.Input != 0.3 || flash.Cost.Output != 1.2 || flash.Cost.CacheRead != 0.006 || flash.Cost.CacheWrite != 0 {
		t.Fatalf("deepseek-v4.1-flash cost = %+v, want the opencode price", flash.Cost)
	}
	if !reflect.DeepEqual(flash.Input, []string{"text", "image"}) || !reflect.DeepEqual(flash.Efforts, []string{"low", "high", "max"}) {
		t.Fatalf("deepseek-v4.1-flash input/efforts = %v/%v, want the models.dev values", flash.Input, flash.Efforts)
	}

	// A model with an unknown value everywhere keeps its state and its
	// source: "context unknown" is the resolver's answer, not a guess.
	local := findModel(t, catalog, "strata", "swift-1.5-iq3_s")
	if local.Source != SourcePiModels || local.Cost != nil || local.Provenance["context"] != SourcePiModels {
		t.Fatalf("strata model = %+v, want it untouched", local)
	}
	unknown := findModel(t, catalog, "zai", "glm-9-unknown")
	if unknown.Source != "" || unknown.Context != 0 || unknown.Cost != nil {
		t.Fatalf("unknown model = %+v, want it untouched", unknown)
	}
}

func TestEnrichModelsDevCache(t *testing.T) {
	fixtureBytes := fixture(t, "models-dev.json")
	var requests int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requests, 1)
		_, _ = w.Write(fixtureBytes)
	}))
	defer server.Close()
	cachePath := filepath.Join(t.TempDir(), "models.dev.json")
	options := EnrichOptions{Online: true, CachePath: cachePath, Endpoint: server.URL}

	filled, problems := EnrichModelsDev(enrichCatalog(), options)
	if filled != 3 || len(problems) != 0 {
		t.Fatalf("first run: filled=%d problems=%v, want 3 and none", filled, problems)
	}
	if got := atomic.LoadInt64(&requests); got != 1 {
		t.Fatalf("requests = %d, want one fetch", got)
	}
	cached, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if !strings.Contains(string(cached), `"version":1`) || !strings.Contains(string(cached), `"fetched_at"`) {
		t.Fatalf("cache = %s, want a versioned document with a fetch time", cached)
	}

	// A fresh cache answers the next scan without the network.
	filled, problems = EnrichModelsDev(enrichCatalog(), options)
	if filled != 3 || len(problems) != 0 {
		t.Fatalf("cached run: filled=%d problems=%v, want 3 and none", filled, problems)
	}
	if got := atomic.LoadInt64(&requests); got != 1 {
		t.Fatalf("requests = %d, want the fresh cache to be reused", got)
	}

	// --refresh goes online again; when the endpoint fails the stale copy
	// still fills the catalog, with a warning.
	server.Close()
	options.Refresh = true
	filled, problems = EnrichModelsDev(enrichCatalog(), options)
	if filled != 3 {
		t.Fatalf("refreshed run: filled=%d, want the stale cache", filled)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "using the cached copy from") {
		t.Fatalf("problems = %v, want one stale-cache warning", problems)
	}
}

func TestEnrichOfflineAndFailure(t *testing.T) {
	catalog := enrichCatalog()
	before := findModel(t, catalog, "zai", "glm-5.3")

	if filled, problems := EnrichModelsDev(catalog, EnrichOptions{}); filled != 0 || problems != nil {
		t.Fatalf("offline run: filled=%d problems=%v, want nothing", filled, problems)
	}
	if after := findModel(t, catalog, "zai", "glm-5.3"); !reflect.DeepEqual(before, after) {
		t.Fatalf("offline run changed the catalog: %+v", after)
	}

	// No cache and a dead endpoint: the built-in table stays, the scan
	// only warns.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	options := EnrichOptions{Online: true, CachePath: filepath.Join(t.TempDir(), "models.dev.json"), Endpoint: deadURL}
	filled, problems := EnrichModelsDev(catalog, options)
	if filled != 0 || len(problems) != 1 {
		t.Fatalf("dead endpoint: filled=%d problems=%v, want one warning", filled, problems)
	}
	if !strings.Contains(problems[0], "models.dev") || !strings.Contains(problems[0], "built-in metadata only") {
		t.Fatalf("problems = %v, want a models.dev warning about the offline table", problems)
	}
	if after := findModel(t, catalog, "zai", "glm-5.3"); !reflect.DeepEqual(before, after) {
		t.Fatalf("failed run changed the catalog: %+v", after)
	}

	// An HTML answer is not a catalog.
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>proxy</body></html>"))
	}))
	defer html.Close()
	options.Endpoint = html.URL
	filled, problems = EnrichModelsDev(enrichCatalog(), options)
	if filled != 0 || len(problems) != 1 || !strings.Contains(problems[0], "no JSON") {
		t.Fatalf("html answer: filled=%d problems=%v, want one no-JSON warning", filled, problems)
	}
}

func TestEnrichBuiltinTable(t *testing.T) {
	catalog := BuiltinCatalog()
	if !reflect.DeepEqual(catalog.Sources, []string{SourceBuiltin}) {
		t.Fatalf("Sources = %v, want the built-in table", catalog.Sources)
	}
	if !sort.SliceIsSorted(catalog.Models, func(i, j int) bool {
		a, b := catalog.Models[i], catalog.Models[j]
		if a.Access != b.Access {
			return a.Access < b.Access
		}
		return a.ID < b.ID
	}) {
		t.Fatalf("models are not sorted: %+v", catalog.Models)
	}
	for _, name := range []string{"claude", "codex"} {
		access := findAccess(t, catalog, name)
		if !reflect.DeepEqual(access.Agents, []string{name}) || access.Source != SourceBuiltin {
			t.Fatalf("%s access = %+v, want the built-in channel", name, access)
		}
	}

	sol := findModel(t, catalog, "codex", "gpt-6.1-sol")
	if sol.Context != 500000 || sol.MaxOut != 128000 || !sol.Reasoning {
		t.Fatalf("gpt-6.1-sol = %+v, want the Codex window", sol)
	}
	if sol.Cost == nil || sol.Cost.Input != 2 || sol.Cost.Output != 10 {
		t.Fatalf("gpt-6.1-sol cost = %+v, want the published price", sol.Cost)
	}
	if !reflect.DeepEqual(sol.Efforts, []string{"low", "medium", "high", "xhigh", "max", "ultra"}) {
		t.Fatalf("gpt-6.1-sol efforts = %v, want the Codex levels", sol.Efforts)
	}
	if sol.Provenance["context"] != SourceBuiltin || sol.Provenance["cost"] != SourceBuiltin {
		t.Fatalf("gpt-6.1-sol provenance = %v, want the built-in source", sol.Provenance)
	}

	// Haiku 4.5 reasons by token budget, so it names no effort levels.
	haiku := findModel(t, catalog, "claude", "claude-haiku-4-5")
	if haiku.Context != 200000 || haiku.MaxOut != 64000 || len(haiku.Efforts) != 0 {
		t.Fatalf("claude-haiku-4-5 = %+v, want the 200k window and no effort names", haiku)
	}
	if haiku.Cost == nil || haiku.Cost.Input != 1 || haiku.Cost.Output != 5 {
		t.Fatalf("claude-haiku-4-5 cost = %+v, want the published price", haiku.Cost)
	}

	// The built-in table is a source like any other: the aggregate fills
	// only what it leaves unknown.
	merged := emptyCatalog()
	merged.Merge(catalog)
	if filled := enrichFromModelsDev(merged, fixture(t, "models-dev.json")); filled != 0 {
		t.Fatalf("filled = %d, want no gap in the built-in models", filled)
	}
	if got := findModel(t, merged, "claude", "claude-opus-5-5"); got.Source != SourceBuiltin || got.Context != 1000000 {
		t.Fatalf("claude-opus-5-5 = %+v, want the built-in row", got)
	}
}
