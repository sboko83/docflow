package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"bsv-docflow/internal/roster"
	"bsv-docflow/internal/testshim"
)

// The roster catalog tests drive the in-process CLI on an isolated HOME:
// a fake Pi model store, fake pi/opencode/codex CLIs and a temporary
// XDG_CACHE_HOME, so the scan never reads the machine it runs on.

// rosterStore is the fake ~/.pi/agent/models-store.json: the shape the
// real store has, two accesses and three models.
const rosterStore = `{
  "zai": {"models": [
    {"id": "glm-5.3", "provider": "zai", "reasoning": true, "input": ["text"], "contextWindow": 1000000, "maxTokens": 131072, "cost": {"input": 1.4, "output": 4.4, "cacheRead": 0.26}},
    {"id": "glm-5.3-flash", "provider": "zai", "reasoning": true, "input": ["text"], "contextWindow": 1000000, "maxTokens": 131072, "cost": {"input": 0.15, "output": 0.5}}
  ]},
  "opencode-go": {"models": [
    {"id": "glm-5.3-flash", "provider": "opencode-go", "reasoning": true, "input": ["text"], "contextWindow": 1000000, "maxTokens": 131072}
  ]}
}`

// rosterModelAnswer is the {location, data} answer of
// `opencode api get /api/model`; one model has no context window and no
// price, so the unknown cells show up in the table.
const rosterModelAnswer = `{"location":{"directory":"/tmp"},"data":[` +
	`{"id":"m1","modelID":"glm-5.3-flash","providerID":"opencode-go","variants":[{"id":"high","settings":{"reasoningEffort":"high"}}],"cost":[{"input":0.15,"output":0.5,"cache":{"read":0.03,"write":0}}],"status":"active","capabilities":{"input":["text"]},"limit":{"context":1000000,"output":131072}},` +
	`{"id":"m2","modelID":"kimi-k3","providerID":"opencode-go","status":"active","limit":{"context":1048576,"output":131072}},` +
	`{"id":"m3","modelID":"mystery","providerID":"opencode","status":"active","limit":{"context":0,"output":0}}]}`

// rosterProviderAnswer is the answer of `opencode api get /api/provider`:
// the headers carry an org ID the catalog must never keep.
const rosterProviderAnswer = `{"data":[` +
	`{"id":"opencode-go","activation":"enabled","headers":{"x-opencode-org-id":"wrk_fixture_org_secret"}},` +
	`{"id":"opencode","activation":"disabled","headers":{"x-opencode-org-id":"wrk_fixture_org_secret"}}]}`

// rosterSecret is the org ID above: no output, warning or cache may quote
// it.
const rosterSecret = "wrk_fixture_org_secret"

// rosterPolicy is the roster the catalog commands filter by.
const rosterPolicy = "spend = [\"zai\", \"opencode-go\"]\n"

// rosterPolicyMissing also names strata, an access no channel of this
// machine reaches.
const rosterPolicyMissing = "spend = [\"zai\", \"opencode-go\", \"strata\"]\n"

// installRosterCommands puts the three fake CLIs on the isolated PATH.
// They answer the probes and the catalog calls of P055.1.2/P055.1.3 and
// refuse anything else, so an unexpected call fails the scan loudly.
func installRosterCommands(t *testing.T, bin string) {
	t.Helper()
	testshim.Script(t, bin, "pi", "case \"$*\" in\n"+
		"  'auth check --provider zai --json') printf '%s\\n' '{\"status\":\"ready\",\"provider\":\"zai\"}'; exit 0 ;;\n"+
		"  'auth check --provider opencode-go --json') printf '%s\\n' '{\"status\":\"not_ready\",\"provider\":\"opencode-go\",\"reason\":\"credentials_not_configured\"}'; exit 1 ;;\n"+
		"  *) printf 'unexpected pi invocation: %s\\n' \"$*\" >&2; exit 3 ;;\n"+
		"esac\n")
	testshim.Script(t, bin, "opencode", "case \"$*\" in\n"+
		"  'api get /api/provider') printf '%s\\n' '"+rosterProviderAnswer+"'; exit 0 ;;\n"+
		"  'api get /api/model') printf '%s\\n' '"+rosterModelAnswer+"'; exit 0 ;;\n"+
		"  *) printf 'unexpected opencode invocation: %s\\n' \"$*\" >&2; exit 3 ;;\n"+
		"esac\n")
	testshim.Script(t, bin, "codex", "case \"$*\" in\n"+
		"  'login status') printf 'Logged in using ChatGPT\\n'; exit 0 ;;\n"+
		"  *) printf 'unexpected codex invocation: %s\\n' \"$*\" >&2; exit 3 ;;\n"+
		"esac\n")
}

// rosterScanEnv isolates HOME, XDG_CONFIG_HOME, XDG_CACHE_HOME and PATH,
// writes the fake Pi store and the roster, and returns the home directory.
func rosterScanEnv(t *testing.T, policy string) (home string) {
	t.Helper()
	rosterEnv(t, policy)
	home = os.Getenv("HOME")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("DOCFLOW_MODELS_DEV_URL", "")
	writeLifecycleFile(t, home, ".pi/agent/models-store.json", rosterStore)
	bin := t.TempDir()
	installRosterCommands(t, bin)
	t.Setenv("PATH", bin)
	return home
}

// rosterCachePath is the catalog cache of the isolated environment.
func rosterCachePath(home string) string {
	return filepath.Join(home, "cache", "docflow", "roster", "catalog.json")
}

// tableRows splits a roster table into header and cells; the columns are
// padded, so the test compares fields instead of raw lines.
func tableRows(text string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, strings.Fields(line))
	}
	return rows
}

// findRow returns the row whose first cell is key.
func findRow(t *testing.T, rows [][]string, key string) []string {
	t.Helper()
	for _, row := range rows {
		if len(row) > 0 && row[0] == key {
			return row
		}
	}
	t.Fatalf("no row %q in %v", key, rows)
	return nil
}

func TestRosterScanSummary(t *testing.T) {
	home := rosterScanEnv(t, rosterPolicy)
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan")
	if code != 0 || stderr != "" {
		t.Fatalf("roster scan = (%d, %q, %q)", code, stdout, stderr)
	}
	rows := tableRows(stdout)
	if strings.Join(rows[0], " ") != "access agent models auth source" {
		t.Fatalf("header = %v", rows[0])
	}
	want := [][]string{
		{"claude", "claude", "6", "unknown", "builtin+auth"},
		{"codex", "codex", "8", "ready", "builtin+auth"},
		{"opencode", "opencode", "1", "not_ready", "opencode+auth"},
		{"opencode-go", "pi,opencode", "2", "ready", "pi-store+opencode+auth"},
		{"zai", "pi", "2", "ready", "pi-store+auth"},
	}
	if len(rows) != len(want)+1 {
		t.Fatalf("rows = %v", rows)
	}
	for _, expected := range want {
		if got := findRow(t, rows, expected[0]); strings.Join(got, " ") != strings.Join(expected, " ") {
			t.Errorf("%s row = %v, want %v", expected[0], got, expected)
		}
	}
	// opencode-go is ready through the opencode channel although pi said
	// not_ready: one access, one quota unit, one working door.
	if row := findRow(t, rows, "opencode-go"); row[3] != roster.AuthReady {
		t.Fatalf("opencode-go auth = %q, want ready", row[3])
	}

	cache, err := roster.LoadCatalog(rosterCachePath(home))
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if cache.Version != roster.CatalogVersion || cache.ScannedAt == "" || len(cache.Models) != 19 {
		t.Fatalf("cache = version %d, scanned %q, %d models", cache.Version, cache.ScannedAt, len(cache.Models))
	}
	if text := stdout + stderr + catalogText(t, cache); strings.Contains(text, rosterSecret) {
		t.Fatalf("the catalog leaked the provider headers: %s", text)
	}
}

// catalogText marshals a catalog for the leak checks.
func catalogText(t *testing.T, catalog *roster.Catalog) string {
	t.Helper()
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	return string(data)
}

func TestRosterScanMissingAccess(t *testing.T) {
	rosterScanEnv(t, rosterPolicyMissing)
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan")
	if code != 0 {
		t.Fatalf("roster scan = (%d, %q, %q)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "access 'strata' is not in the catalog") {
		t.Fatalf("stderr = %q, want the missing-access warning", stderr)
	}
	// A warning next to a partial result keeps the success code and the
	// table on stdout.
	if !strings.Contains(stdout, "codex") || !strings.Contains(stdout, "zai") {
		t.Fatalf("stdout = %q, want the access table", stdout)
	}
}

func TestRosterScanPartialCatalog(t *testing.T) {
	rosterEnv(t, rosterPolicy)
	home := os.Getenv("HOME")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("DOCFLOW_MODELS_DEV_URL", "")
	bin := t.TempDir()
	// No Pi store: the rounded table fallback fails too, and OpenCode
	// answers HTML the way a proxy does.
	testshim.Script(t, bin, "pi", "printf 'pi is broken\\n' >&2; exit 3\n")
	testshim.Script(t, bin, "opencode", "printf '<html>proxy</html>\\n'; exit 0\n")
	testshim.Script(t, bin, "codex", "printf 'Logged in using ChatGPT\\n'; exit 0\n")
	t.Setenv("PATH", bin)

	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan")
	if code != 0 {
		t.Fatalf("roster scan = (%d, %q, %q), want a partial catalog", code, stdout, stderr)
	}
	for _, want := range []string{
		"docflow: roster: pi: pi exited 3: pi is broken",
		"docflow: roster: opencode: /api/model answered HTML, not JSON",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "<html>") {
		t.Fatalf("stderr quoted the response body: %q", stderr)
	}
	// The built-in table and the codex probe still fill their access.
	rows := tableRows(stdout)
	if row := findRow(t, rows, "codex"); row[2] != "8" || row[3] != "ready" {
		t.Fatalf("codex row = %v", row)
	}
	if row := findRow(t, rows, "claude"); row[3] != "unknown" {
		t.Fatalf("claude row = %v", row)
	}
	if _, err := os.Stat(rosterCachePath(home)); err != nil {
		t.Fatalf("partial scan wrote no cache: %v", err)
	}
}

func TestRosterScanJSON(t *testing.T) {
	rosterScanEnv(t, rosterPolicy)
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan", "--json")
	if code != 0 {
		t.Fatalf("roster scan --json = (%d, %q, %q)", code, stdout, stderr)
	}
	var catalog roster.Catalog
	if err := json.Unmarshal([]byte(stdout), &catalog); err != nil {
		t.Fatalf("scan --json is not a catalog: %v\n%s", err, stdout)
	}
	if catalog.Version != roster.CatalogVersion || len(catalog.Accesses) != 5 {
		t.Fatalf("catalog = version %d, %d accesses", catalog.Version, len(catalog.Accesses))
	}
	if !strings.Contains(stdout, "\"scanned_at\"") {
		t.Fatalf("scan --json lacks the scan time:\n%s", stdout)
	}
}

func TestRosterScanOnlineRefresh(t *testing.T) {
	home := rosterScanEnv(t, rosterPolicy)
	var hits int32
	document := `{"opencode":{"models":{"mystery":{"id":"mystery","reasoning":true,"limit":{"context":65536,"output":8192},"cost":{"input":0.3,"output":1.2}}}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(document))
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_MODELS_DEV_URL", server.URL)

	// A first online scan fetches models.dev and fills the unknown
	// metadata of the opencode models.
	if code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan", "--online", "--refresh"); code != 0 {
		t.Fatalf("roster scan --online = (%d, %q, %q)", code, stdout, stderr)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("models.dev requests = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(rosterCachePath(home)), "models.dev.json")); err != nil {
		t.Fatalf("models.dev cache: %v", err)
	}
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "models", "opencode")
	if code != 0 {
		t.Fatalf("roster models opencode = (%d, %q, %q)", code, stdout, stderr)
	}
	row := findRow(t, tableRows(stdout), "opencode")
	if strings.Join(row, " ") != "opencode mystery 65536 8192 yes 0.3/1.2 models.dev" {
		t.Fatalf("enriched row = %v", row)
	}

	// A fresh cache is reused; --refresh ignores it.
	if code, _, _ := callLifecycleCLI(t, t.TempDir(), "roster", "scan", "--online"); code != 0 {
		t.Fatalf("second online scan failed")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("models.dev requests = %d, want the fresh cache to be reused", got)
	}
	if code, _, _ := callLifecycleCLI(t, t.TempDir(), "roster", "scan", "--online", "--refresh"); code != 0 {
		t.Fatalf("refresh scan failed")
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("models.dev requests = %d, want --refresh to fetch again", got)
	}

	// An unreachable endpoint keeps the previous copy: a warning, not a
	// failed scan.
	server.Close()
	if code, _, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan", "--online", "--refresh"); code != 0 {
		t.Fatalf("scan with an unreachable models.dev = (%d, %q)", code, stderr)
	} else if !strings.Contains(stderr, "using the cached copy from") {
		t.Fatalf("stderr = %q, want the stale-cache note", stderr)
	}
}

func TestRosterModelsFilter(t *testing.T) {
	rosterScanEnv(t, rosterPolicy)
	if code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan"); code != 0 {
		t.Fatalf("roster scan = (%d, %q, %q)", code, stdout, stderr)
	}

	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "models")
	if code != 0 || stderr != "" {
		t.Fatalf("roster models = (%d, %q, %q)", code, stdout, stderr)
	}
	rows := tableRows(stdout)
	if strings.Join(rows[0], " ") != "access model context max-out reasoning cost source" {
		t.Fatalf("header = %v", rows[0])
	}
	// The roster names zai, opencode-go and strata: codex and claude stay
	// out although the catalog holds them.
	accesses := map[string]bool{}
	for _, row := range rows[1:] {
		accesses[row[0]] = true
	}
	if len(accesses) != 2 || !accesses["zai"] || !accesses["opencode-go"] {
		t.Fatalf("accesses = %v, want only the roster accesses", accesses)
	}
	if row := findRow(t, rows, "zai"); strings.Join(row, " ") != "zai glm-5.3 1000000 131072 yes 1.4/4.4 pi-store" {
		t.Fatalf("zai glm-5.3 row = %v", row)
	}

	// --all adds the accesses the roster does not name.
	code, stdout, stderr = callLifecycleCLI(t, t.TempDir(), "roster", "models", "--all")
	if code != 0 || stderr != "" {
		t.Fatalf("roster models --all = (%d, %q, %q)", code, stdout, stderr)
	}
	for _, access := range []string{"claude", "codex", "opencode", "opencode-go", "zai"} {
		findRow(t, tableRows(stdout), access)
	}

	// The ACCESS argument narrows the table to one access and shows the
	// unknown cells as unknown, not as zero.
	code, stdout, stderr = callLifecycleCLI(t, t.TempDir(), "roster", "models", "opencode")
	if code != 0 || stderr != "" {
		t.Fatalf("roster models opencode = (%d, %q, %q)", code, stdout, stderr)
	}
	rows = tableRows(stdout)
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want one model", rows)
	}
	if strings.Join(rows[1], " ") != "opencode mystery unknown unknown no unknown opencode" {
		t.Fatalf("unknown cells = %v", rows[1])
	}

	// An access the catalog does not know is an error, not an empty table.
	code, stdout, stderr = callLifecycleCLI(t, t.TempDir(), "roster", "models", "nonsense")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "unknown access 'nonsense'") || !strings.Contains(stderr, "known: claude, codex, opencode, opencode-go, zai") {
		t.Fatalf("roster models nonsense = (%d, %q, %q)", code, stdout, stderr)
	}
}

func TestRosterModelsJSON(t *testing.T) {
	rosterScanEnv(t, rosterPolicy)
	if code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan"); code != 0 {
		t.Fatalf("roster scan = (%d, %q, %q)", code, stdout, stderr)
	}
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "models", "zai", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("roster models --json = (%d, %q, %q)", code, stdout, stderr)
	}
	var report roster.ModelReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("models --json: %v\n%s", err, stdout)
	}
	if len(report.Models) != 2 || report.Models[0].Access != "zai" || report.Models[0].Cost == nil {
		t.Fatalf("report = %+v", report)
	}
}

func TestRosterModelsWithoutCache(t *testing.T) {
	rosterScanEnv(t, rosterPolicy)
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "models")
	if code != 0 {
		t.Fatalf("roster models without a cache = (%d, %q, %q)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "the catalog cache is empty; run docflow roster scan") {
		t.Fatalf("stderr = %q, want the empty-cache hint", stderr)
	}
	if got := tableRows(stdout); len(got) != 1 || got[0][0] != "access" {
		t.Fatalf("stdout = %q, want the header only", stdout)
	}
}

func TestRosterModelsWithoutRoster(t *testing.T) {
	rosterScanEnv(t, "")
	if err := os.Remove(filepath.Join(os.Getenv("HOME"), "docflow", "roster.toml")); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan"); code != 0 {
		t.Fatalf("roster scan = (%d, %q, %q)", code, stdout, stderr)
	}
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "models")
	if code != 0 {
		t.Fatalf("roster models = (%d, %q, %q)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "lists no accesses; showing the whole catalog") {
		t.Fatalf("stderr = %q, want the fallback note", stderr)
	}
	findRow(t, tableRows(stdout), "codex")
}

func TestRosterStatusTable(t *testing.T) {
	rosterScanEnv(t, rosterPolicy)
	if code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan"); code != 0 {
		t.Fatalf("roster scan = (%d, %q, %q)", code, stdout, stderr)
	}
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "status")
	if code != 0 || stderr != "" {
		t.Fatalf("roster status = (%d, %q, %q)", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "catalog: ") || !strings.Contains(lines[0], "sources: pi-store, opencode, builtin, auth") {
		t.Fatalf("status header = %q", lines[0])
	}
	rows := tableRows(strings.Join(lines[1:], "\n"))
	if row := findRow(t, rows, "opencode-go"); strings.Join(row, " ") != "opencode-go pi,opencode 2 ready pi-store+opencode+auth" {
		t.Fatalf("opencode-go row = %v", row)
	}
}

func TestRosterStatusJSON(t *testing.T) {
	rosterScanEnv(t, rosterPolicy)
	if code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan"); code != 0 {
		t.Fatalf("roster scan = (%d, %q, %q)", code, stdout, stderr)
	}
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "status", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("roster status --json = (%d, %q, %q)", code, stdout, stderr)
	}
	var report roster.StatusReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("status --json: %v\n%s", err, stdout)
	}
	if report.ScannedAt == "" || len(report.Accesses) != 5 {
		t.Fatalf("report = %+v", report)
	}
	if report.Accesses[0].Access != "claude" || report.Accesses[0].Models != 6 {
		t.Fatalf("first access = %+v", report.Accesses[0])
	}
}

func TestRosterStatusWithoutCache(t *testing.T) {
	rosterScanEnv(t, rosterPolicy)
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "status")
	if code != 0 {
		t.Fatalf("roster status without a cache = (%d, %q, %q)", code, stdout, stderr)
	}
	if !strings.HasPrefix(stdout, "catalog: not scanned yet\n") {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "the catalog cache is empty; run docflow roster scan") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRosterScanBrokenRoster(t *testing.T) {
	rosterScanEnv(t, "spend = [\"zai\"\n")
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "scan")
	if code != 2 || stdout != "" {
		t.Fatalf("roster scan with a broken roster = (%d, %q, %q)", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "roster.toml") {
		t.Fatalf("stderr = %q, want the roster path", stderr)
	}
}
