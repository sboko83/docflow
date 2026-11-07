package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/roster"
	"bsv-docflow/internal/roster/web"
)

// liveShapedRoster is the shape of the personal roster at the time of the
// P059 acceptance: a v2 policy migrated from v1, with the strata local
// access, the deepseek subscription and the check/trusted split. The
// acceptance copies it, migrates it to v3 and edits it through the web
// editor the way the browser does.
const liveShapedRoster = `# Roster v2, converted from the v1 format by docflow roster migrate.
# Model context, prices and reasoning levels come from docflow roster scan.
version = 2

[access.strata]
agents = ["pi"]
kind = "local"
check = true
models = ["swift-1.5-iq3_s"]

[access.opencode-go]
agents = ["pi"]
trusted = true
models = ["deepseek-v4.1-flash"]

[access.zai]
agents = ["pi"]
check = true
models = ["glm-5.3-flash", "glm-5.3"]

[access.codex]
agents = ["codex"]
trusted = true
models = ["gpt-6-luna", "gpt-6.1-sol"]

[model."strata/swift-1.5-iq3_s"]
class = "standard"

[model."opencode-go/deepseek-v4.1-flash"]
class = "strong"

[model."zai/glm-5.3-flash"]
class = "standard"

[model."zai/glm-5.3"]
class = "strong"

[model."codex/gpt-6-luna"]
class = "standard"

[model."codex/gpt-6.1-sol"]
class = "strong"

[role]
executor   = "standard"
cheap      = "cheap"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"
`

// TestRosterWebAcceptance walks the plan goal on a copy of the live-shaped
// policy: migrate v2 to v3, add the deepseek routes low→cheap and
// high→standard through the web operations, create a variant, and read
// the changes back through `roster brief`.
func TestRosterWebAcceptance(t *testing.T) {
	home := rosterStateEnv(t)
	path := filepath.Join(home, "docflow", "roster.toml")
	writeLifecycleFile(t, home, "docflow/roster.toml", liveShapedRoster)
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "roster", "testdata", "catalog-scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleFile(t, home, "cache/docflow/roster/catalog.json", string(data))

	// The live policy is v2: migrate it the way every other command
	// tells the user to.
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "migrate", "--write")
	if code != 0 {
		t.Fatalf("roster migrate --write = (%d, %q, %q), want 0", code, stdout, stderr)
	}

	// The editor talks to the handler the server serves; the request
	// path is the same the browser uses.
	server := httptest.NewServer(web.New(path).Handler())
	t.Cleanup(server.Close)
	post := func(body string) map[string]any {
		t.Helper()
		resp, err := http.Post(server.URL+"/api/op", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || out["ok"] != true {
			t.Fatalf("POST /api/op %s = (%d, %v), want ok", body, resp.StatusCode, out["error"])
		}
		return out
	}
	// deepseek-v4.1 joins the allowlist with its first declared effort
	// (low) at the automatic class; the editor then fixes the class and
	// adds the second level under high.
	post(`{"op":"AddAccessModel","access":"opencode-go","id":"deepseek-v4.1"}`)
	post(`{"op":"SetRouteClass","access":"opencode-go","model":"deepseek-v4.1","effort":"low","class":"cheap"}`)
	post(`{"op":"AddRouteLevel","access":"opencode-go","model":"deepseek-v4.1"}`)
	post(`{"op":"SetRouteEffort","access":"opencode-go","model":"deepseek-v4.1","effort":"medium","next":"high"}`)
	post(`{"op":"SetRouteClass","access":"opencode-go","model":"deepseek-v4.1","effort":"high","class":"standard"}`)
	post(`{"op":"CreateVariant","name":"web-accept"}`)

	// The policy file carries both routes and the variants file the
	// preset.
	policy, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"low" = "cheap"`, `"high" = "standard"`, "version = 3"} {
		if !strings.Contains(string(policy), want) {
			t.Fatalf("the policy file lost %q:\n%s", want, policy)
		}
	}
	variants, err := os.ReadFile(roster.VariantsPath(path))
	if err != nil {
		t.Fatalf("the variants file was not created: %v", err)
	}
	if !strings.Contains(string(variants), "[preset.web-accept]") {
		t.Fatalf("the variants file lost the preset:\n%s", variants)
	}

	// `roster brief` reads the edited roster: the cheap role goes through
	// the cheap low route, the executor through high.
	code, stdout, stderr = callLifecycleCLI(t, t.TempDir(), "roster", "brief", "--roster", path)
	if code != 0 {
		t.Fatalf("roster brief = (%d, %q, %q), want 0", code, stdout, stderr)
	}
	chains := briefChains(t, stdout)
	if !strings.Contains(chains["cheap"], "opencode-go/deepseek-v4.1@low") {
		t.Fatalf("cheap chain = %q, want the low→cheap route", chains["cheap"])
	}
	if !strings.Contains(chains["executor"], "opencode-go/deepseek-v4.1@high") {
		t.Fatalf("executor chain = %q, want the high→standard route", chains["executor"])
	}
}
