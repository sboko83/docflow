package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The roster condition tests drive the in-process CLI against the v2
// policy fixture installed as the personal roster in a temporary
// XDG_CONFIG_HOME: the state file must be created next to it, and the
// repository must stay untouched.

// rosterStateEnv installs testdata/policy-v2.toml as the personal roster
// in a temporary XDG_CONFIG_HOME and returns that directory.
func rosterStateEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("DOCFLOW_AGENTS", filepath.Join(home, "missing.toml"))
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	t.Setenv("DOCFLOW_SKILLS_CHECK", "0")
	t.Setenv("DOCFLOW_NOTIFY", "0")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "roster", "testdata", "policy-v2.toml"))
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleFile(t, home, "docflow/roster.toml", string(data))
	return home
}

// rosterNoCacheNote is the warning status prints while the catalog cache
// is empty; the condition tests do not scan, so it is always there.
const rosterNoCacheNote = "docflow: roster: the catalog cache is empty; run docflow roster scan\n"

func rosterStatePath(home string) string {
	return filepath.Join(home, "docflow", "roster.state.toml")
}

func readRosterState(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(rosterStatePath(home))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRosterStateOffOn(t *testing.T) {
	home := rosterStateEnv(t)
	root := t.TempDir()
	before := readRosterFile(t, home, "docflow/roster.toml")

	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "off", "codex")
	if code != 0 || stdout != "codex: off\n" || stderr != "" {
		t.Fatalf("roster off = (%d, %q, %q)", code, stdout, stderr)
	}
	if state := readRosterState(t, home); !strings.Contains(state, "off = [\"codex\"]") {
		t.Fatalf("state after off:\n%s", state)
	}
	if got := readRosterFile(t, home, "docflow/roster.toml"); got != before {
		t.Fatal("roster off rewrote roster.toml")
	}
	code, stdout, stderr = callLifecycleCLI(t, root, "roster", "status")
	if code != 0 || stderr != rosterNoCacheNote {
		t.Fatalf("roster status = (%d, %q, %q)", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "codex") || !strings.Contains(stdout, "off") || !strings.Contains(stdout, "state") {
		t.Fatalf("status does not show the off condition:\n%s", stdout)
	}

	code, stdout, stderr = callLifecycleCLI(t, root, "roster", "off", "claude", "--until", "2099-01-01")
	if code != 0 || stdout != "claude: off until 2099-01-01\n" || stderr != "" {
		t.Fatalf("roster off --until = (%d, %q, %q)", code, stdout, stderr)
	}
	if state := readRosterState(t, home); !strings.Contains(state, "[until]\nclaude = \"2099-01-01\"") {
		t.Fatalf("state after off --until:\n%s", state)
	}
	code, stdout, stderr = callLifecycleCLI(t, root, "roster", "status", "--json")
	if code != 0 || !strings.Contains(stdout, `"value": "until 2099-01-01"`) || !strings.Contains(stdout, `"source": "state"`) {
		t.Fatalf("status --json = (%d, %q, %q)", code, stdout, stderr)
	}

	code, stdout, stderr = callLifecycleCLI(t, root, "roster", "on", "claude")
	if code != 0 || stdout != "claude: on\n" || stderr != "" {
		t.Fatalf("roster on = (%d, %q, %q)", code, stdout, stderr)
	}
	if state := readRosterState(t, home); strings.Contains(state, "claude") {
		t.Fatalf("state after on still holds claude:\n%s", state)
	}
	// An off without a date drops the previous bound.
	if code, stdout, stderr := callLifecycleCLI(t, root, "roster", "off", "codex", "--until", "2099-01-01"); code != 0 {
		t.Fatalf("roster off dated = (%d, %q, %q)", code, stdout, stderr)
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "roster", "off", "codex"); code != 0 || stdout != "codex: off\n" {
		t.Fatalf("roster off open-ended = (%d, %q, %q)", code, stdout, stderr)
	}
	if state := readRosterState(t, home); strings.Contains(state, "until") {
		t.Fatalf("state kept the dropped until date:\n%s", state)
	}
}

func TestRosterStateBudgetExpireAllowDeny(t *testing.T) {
	home := rosterStateEnv(t)
	root := t.TempDir()
	for _, tc := range []struct {
		args   []string
		stdout string
	}{
		{[]string{"roster", "budget", "opencode-go", "normal"}, "opencode-go: budget normal\n"},
		{[]string{"roster", "expire", "zai", "2099-01-01"}, "zai: expires 2099-01-01\n"},
		{[]string{"roster", "allow", "zai", "*flash*", "glm-5*"}, "zai: allow *flash* glm-5*\n"},
		{[]string{"roster", "deny", "zai", "glm-5.3-thinking"}, "zai: deny glm-5.3-thinking\n"},
		{[]string{"roster", "deny", "zai", "glm-5.3-thinking"}, "zai: deny glm-5.3-thinking\n"},
	} {
		code, stdout, stderr := callLifecycleCLI(t, root, tc.args...)
		if code != 0 || stdout != tc.stdout || stderr != "" {
			t.Fatalf("%v = (%d, %q, %q), want %q", tc.args, code, stdout, stderr, tc.stdout)
		}
	}
	state := readRosterState(t, home)
	for _, want := range []string{`opencode-go = "normal"`, `zai = "2099-01-01"`, `zai = ["*flash*", "glm-5*"]`, `zai = ["glm-5.3-thinking"]`} {
		if !strings.Contains(state, want) {
			t.Errorf("state lacks %q:\n%s", want, state)
		}
	}
	if strings.Count(state, "glm-5.3-thinking") != 1 {
		t.Errorf("deny duplicated a pattern:\n%s", state)
	}
	// The state is data, not policy: a broken level, a minimal budget on
	// an untrusted access and a broken glob fail before the write.
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"roster", "budget", "zai", "plenty"}, "level must be"},
		{[]string{"roster", "budget", "zai", "minimal"}, "minimal requires trusted"},
		{[]string{"roster", "deny", "zai", "["}, "bad model pattern"},
		{[]string{"roster", "expire", "zai", "2026-13-01"}, "date must be YYYY-MM-DD"},
		{[]string{"roster", "budget", "zai"}, "LEVEL is required"},
		{[]string{"roster", "allow", "zai"}, "PATTERN is required"},
		{[]string{"roster", "off"}, "ACCESS is required"},
		{[]string{"roster", "off", "nope"}, "unknown access 'nope'"},
	} {
		code, stdout, stderr := callLifecycleCLI(t, root, tc.args...)
		if code != 2 || !strings.Contains(stderr, tc.message) {
			t.Errorf("%v = (%d, %q, %q), want code 2 with %q", tc.args, code, stdout, stderr, tc.message)
		}
	}
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "status")
	if code != 0 || stderr != rosterNoCacheNote {
		t.Fatalf("roster status = (%d, %q, %q)", code, stdout, stderr)
	}
	for _, want := range []string{"zai", "expires", "2099-01-01", "glm-5.3-thinking"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, stdout)
		}
	}
}

func TestRosterStateReset(t *testing.T) {
	home := rosterStateEnv(t)
	root := t.TempDir()
	if code, stdout, stderr := callLifecycleCLI(t, root, "roster", "off", "codex"); code != 0 {
		t.Fatalf("roster off = (%d, %q, %q)", code, stdout, stderr)
	}
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "reset")
	if code != 0 || stdout != "conditions: reset\n" || stderr != "" {
		t.Fatalf("roster reset = (%d, %q, %q)", code, stdout, stderr)
	}
	if state := readRosterState(t, home); state != "" {
		t.Fatalf("reset left the state file:\n%s", state)
	}
	// Reset without a state file is not an error: there is nothing to drop.
	if code, stdout, stderr := callLifecycleCLI(t, root, "roster", "reset"); code != 0 || stdout != "conditions: reset\n" {
		t.Fatalf("second reset = (%d, %q, %q)", code, stdout, stderr)
	}
}

func TestRosterStateDropsExpiredOff(t *testing.T) {
	home := rosterStateEnv(t)
	root := t.TempDir()
	writeLifecycleFile(t, home, "docflow/roster.state.toml",
		"version = 2\n\noff = [\"claude\", \"local\"]\n\n[until]\nclaude = \"2020-01-01\"\nlocal = \"2099-01-01\"\n")
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "status")
	if code != 0 || stderr != rosterNoCacheNote {
		t.Fatalf("roster status = (%d, %q, %q)", code, stdout, stderr)
	}
	if strings.Contains(stdout, "2020-01-01") || !strings.Contains(stdout, "until 2099-01-01") {
		t.Fatalf("status shows the wrong off conditions:\n%s", stdout)
	}
	// The next mutation rewrites the file without the expired condition.
	if code, stdout, stderr := callLifecycleCLI(t, root, "roster", "on", "local"); code != 0 {
		t.Fatalf("roster on = (%d, %q, %q)", code, stdout, stderr)
	}
	if state := readRosterState(t, home); strings.Contains(state, "claude") || strings.Contains(state, "2020-01-01") {
		t.Fatalf("state kept the expired condition:\n%s", state)
	}
}

func TestRosterStateNeedsV2Roster(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	writeLifecycleFile(t, home, "docflow/roster.toml", "[access.zai]\nagents = [\"pi\"]\n")
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "off", "zai")
	if code != 2 || !strings.Contains(stderr, "version must be 2") {
		t.Fatalf("roster off on a v1 roster = (%d, %q, %q)", code, stdout, stderr)
	}
	if state := readRosterState(t, home); state != "" {
		t.Fatalf("a failed command wrote the state:\n%s", state)
	}
	// status stays usable: the catalog section is what a v1 setup shows.
	if code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "status"); code != 0 || strings.Contains(stdout, "conditions") {
		t.Fatalf("roster status on a v1 roster = (%d, %q, %q)", code, stdout, stderr)
	}
}
