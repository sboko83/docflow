package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bsv-docflow/internal/roster"
	"bsv-docflow/internal/testshim"
)

// TestRosterV2Flow starts with probes rather than a prebuilt catalog. Every
// fake CLI logs before dispatch, including unexpected model invocations.
func TestRosterV2Flow(t *testing.T) {
	originalPath := os.Getenv("PATH")
	policy := strings.Replace(rosterFixture(t, "policy-v2.toml"), "2026-06-30", "2099-01-01", 1)
	home := rosterScanEnv(t, policy)
	bin := os.Getenv("PATH")
	// Windows testshim needs Git Bash while creating each executable shim.
	// Only the actual scan runs with PATH restricted to the fake commands.
	t.Setenv("PATH", originalPath)
	t.Setenv("ROSTER_CALLS", filepath.Join(home, "calls"))
	logCall := "printf '%s\\n' \"$*\" >> \"$ROSTER_CALLS\"\n"
	store := strings.Replace(rosterStore, `"id": "glm-5.3-flash", "provider": "opencode-go"`, `"id": "deepseek-v4.1-flash", "provider": "opencode-go"`, 1)
	writeLifecycleFile(t, home, ".pi/agent/models-store.json", store)
	answer := strings.NewReplacer("glm-5.3-flash", "deepseek-v4.1-flash", "kimi-k3", "deepseek-v4.1").Replace(rosterModelAnswer)
	answer = strings.Replace(answer, `"id":"m2",`, `"id":"m2","variants":[{"id":"high","settings":{"reasoningEffort":"high"}}],`, 1)
	testshim.Script(t, bin, "opencode", logCall+"case \"$*\" in\n"+
		"'api get /api/model') printf '%s\\n' '"+answer+"';;\n"+
		"'api get /api/provider') printf '%s\\n' '"+rosterProviderAnswer+"';;\n"+
		"*) exit 3;;\nesac\n")
	testshim.Script(t, bin, "pi", logCall+"case \"$*\" in\n"+
		"'auth check --provider zai --json'|'auth check --provider opencode-go --json') printf '%s\\n' '{\"status\":\"ready\"}';;\n"+
		"*) exit 3;;\nesac\n")
	testshim.Script(t, bin, "codex", logCall+"case \"$*\" in\n'login status') printf 'Logged in using ChatGPT\\n';;\n*) exit 3;;\nesac\n")
	t.Setenv("PATH", bin)
	root := t.TempDir()
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "scan", "--refresh")
	if code != 0 || strings.Contains(stderr, "unexpected") {
		t.Fatalf("scan = (%d, %q, %q)", code, stdout, stderr)
	}
	catalog, err := roster.LoadCatalog(rosterCachePath(home))
	if err != nil {
		t.Fatal(err)
	}
	var channels []string
	for _, access := range catalog.Accesses {
		if access.Access == "opencode-go" {
			channels = access.Agents
		}
	}
	if !reflect.DeepEqual(channels, []string{"pi", "opencode"}) {
		t.Fatalf("shared access channels = %v", channels)
	}
	probes := readRosterFile(t, home, "calls")
	for _, call := range strings.Split(strings.TrimSpace(probes), "\n") {
		switch call {
		case "api get /api/model", "api get /api/provider", "auth check --provider zai --json", "auth check --provider opencode-go --json", "login status":
		default:
			t.Fatalf("scan invoked a non-probe command: %q", call)
		}
	}
	// Git is needed to create the project; keep the fake CLIs first.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+originalPath)
	invoke := func(args ...string) (string, string) {
		t.Helper()
		code, out, errout := callLifecycleCLI(t, root, args...)
		if code != 0 {
			t.Fatalf("%v = (%d, %q, %q)", args, code, out, errout)
		}
		return out, errout
	}
	invoke("roster", "off", "claude", "--until", "2099-01-01")
	status, _ := invoke("roster", "status", "--json")
	if !strings.Contains(status, `"value": "until 2099-01-01"`) || !strings.Contains(status, `"source": "state"`) {
		t.Fatalf("status lost dated off: %s", status)
	}
	state := readRosterState(t, home)
	flags := []string{"--off", "local", "--minimal", "codex", "--lean", "zai", "--only", "zai=*flash*", "--prefer", "opencode-go"}
	out, _ := invoke(append([]string{"roster", "brief", "--json"}, flags...)...)
	want := map[string]string{
		"executor": "pi | opencode-go/deepseek-v4.1 | high",
		"cheap":    "pi | opencode-go/deepseek-v4.1-flash | high; pi | zai/glm-5.3-flash | low",
		"heavy":    "", "review": "pi | opencode-go/deepseek-v4.1 | high",
		"acceptance": "codex | gpt-6.1-sol | medium", "replan": "codex | gpt-6.1-sol | medium",
	}
	if got := jsonBriefChains(t, out); !reflect.DeepEqual(got, want) {
		t.Fatalf("chains = %v, want %v", got, want)
	}
	var brief roster.Brief
	if err := json.Unmarshal([]byte(out), &brief); err != nil {
		t.Fatal(err)
	}
	if !contains(brief.Warnings, "no fallback for review") {
		t.Fatalf("brief warnings = %v", brief.Warnings)
	}
	root = rosterProject(t, rosterPlanUnits("", rosterConditionUnits))
	before := readRosterFile(t, root, rosterPlanPath)
	projectFormat := readRosterFile(t, root, "docs/docflow.md")
	revision := gitRevision(t, root)
	_, warnings := invoke(append([]string{"roster", "apply", "P001"}, flags...)...)
	if !strings.Contains(warnings, "no fallback for review") || strings.Contains(warnings, "no routes for heavy") {
		t.Fatalf("apply warnings = %q", warnings)
	}
	after := readRosterFile(t, root, rosterPlanPath)
	_, beforeBody, _ := strings.Cut(before, "\n---\n")
	_, afterBody, _ := strings.Cut(after, "\n---\n")
	if afterBody != beforeBody || readRosterFile(t, root, "docs/docflow.md") != projectFormat || gitRevision(t, root) != revision {
		t.Fatal("apply changed the body, project format or commit")
	}
	// Exact full header comparison catches new keys as well as wrong routes.
	header := "executor: " + want["executor"] + "\ncheap: " + want["cheap"] + "\nreview: " + want["review"] + "\nacceptance: " + want["acceptance"] + "\nreplan: " + want["replan"] + "\n"
	if after != strings.Replace(before, "\n---\n", "\n"+header+"---\n", 1) {
		t.Fatalf("unexpected plan header:\n%s", after)
	}
	commitRosterProject(t, root)
	dryRun, _ := invoke("run", "--dry-run", "--all")
	for _, line := range []string{
		"P001.1.2 [ ] Plain unit: " + want["executor"] + " (plan)",
		"P001.1.3 [ ] Review unit: " + want["review"] + " (role review)",
		"P001.1.4 [ ] Acceptance unit: " + want["acceptance"] + " (role acceptance)",
		"P001.1.5 [ ] Cheap unit: pi | opencode-go/deepseek-v4.1-flash | high (role cheap)",
	} {
		if !strings.Contains(dryRun, line+"\n") {
			t.Errorf("dry-run lacks %q:\n%s", line, dryRun)
		}
	}
	if readRosterState(t, home) != state || readRosterFile(t, home, "calls") != probes {
		t.Fatal("brief/apply/dry-run mutated conditions or invoked an agent")
	}
	if strings.Contains(readRosterFile(t, home, "cache/docflow/roster/catalog.json"), rosterSecret) {
		t.Fatal("scan leaked provider headers")
	}
	invoke("roster", "reset")
	if readRosterState(t, home) != "" {
		t.Fatal("reset kept the conditions")
	}
	writeLifecycleFile(t, home, "docflow/roster.toml", draftV1Roster(t))
	for _, args := range [][]string{{"roster", "scan"}, {"roster", "brief"}, {"roster", "apply", "P001", "--off", "local", "--only", "zai=*flash*", "--expires", "zai=2099-01-01"}} {
		code, _, errout := callLifecycleCLI(t, root, args...)
		if code != 2 || !strings.Contains(errout, "roster migrate") {
			t.Fatalf("v1 %v = (%d, %q)", args, code, errout)
		}
	}
	invoke("roster", "migrate", "--write")
	if readRosterFile(t, home, "docflow/roster.toml.bak") != draftV1Roster(t) {
		t.Fatal("migration lost the original")
	}
	invoke("roster", "brief")
}

func TestRosterV2ExpiredAccessCLI(t *testing.T) {
	home := rosterBriefEnv(t)
	writeLifecycleFile(t, home, "docflow/roster.state.toml", "version = 2\n[expires]\nzai = \"2000-01-01\"\n")
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "brief", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("expired brief = (%d, %q, %q)", code, stdout, stderr)
	}
	if chain := jsonBriefChains(t, stdout)["cheap"]; chain != "pi | opencode-go/deepseek-v4.1-flash | high" {
		t.Fatalf("expired zai remained in cheap: %q", chain)
	}
	if !strings.Contains(stdout, `"value": "2000-01-01"`) {
		t.Fatalf("brief did not show expiry: %s", stdout)
	}
}
