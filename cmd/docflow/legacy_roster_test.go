package main

import (
	"strings"
	"testing"
)

// The legacy roster tests keep the v1 CLI contract alive until the cutover
// of P055.4.6 removes the v1 path: `roster show` prints the chain table,
// `roster apply --profile` reorders the chains, and a roster with a role of
// one provider is refused. The fixture is the v1 text of the P036 draft,
// internal/roster/testdata/draft-v1.toml; the v2 acceptance tests read the
// migrated draft.toml.

// draftV1Roster is the v1 draft roster of P036 ## Context.
func draftV1Roster(t *testing.T) string {
	t.Helper()
	return rosterFixture(t, "draft-v1.toml")
}

// legacyTable is the chain table the v1 roster gives for the same draft.
var legacyTable = map[string]string{
	"cheap":      "pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | high; claude | claude-haiku-5-5 | -",
	"executor":   "pi | zai/glm-5.3-flash | max; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high",
	"heavy":      "pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high",
	"review":     "codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high",
	"acceptance": "claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high",
	"replan":     "claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high",
}

// TestLegacyRosterShowMatchesTable: the v1 `roster show` still prints the
// chain table of the draft with its spend summary.
func TestLegacyRosterShowMatchesTable(t *testing.T) {
	rosterEnv(t, draftV1Roster(t))
	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "show")
	if code != 0 || stderr != "" {
		t.Fatalf("roster show = (%d, %q, %q)", code, stdout, stderr)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n")[1:] {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("unexpected line %q in:\n%s", line, stdout)
		}
		got[key] = value
	}
	got["executor"] += "; " + got["fallback"]
	delete(got, "fallback")
	if len(got) != len(legacyTable) {
		t.Fatalf("roles = %v, want %d roles:\n%s", got, len(legacyTable), stdout)
	}
	for role, want := range legacyTable {
		if got[role] != want {
			t.Errorf("%s chain = %q, want %q", role, got[role], want)
		}
	}
	if first, _, _ := strings.Cut(stdout, "\n"); first != "spend: zai, codex, claude · check: zai · trusted: claude" {
		t.Errorf("summary line = %q", first)
	}
}

func TestLegacyRosterApplyLintDryRun(t *testing.T) {
	rosterEnv(t, draftV1Roster(t))
	root := rosterProject(t, rosterPlan(""))
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001")
	if code != 0 || !strings.Contains(stdout, "commit: P001: apply roster") {
		t.Fatalf("roster apply = (%d, %q, %q)", code, stdout, stderr)
	}
	executor, fallback, _ := strings.Cut(legacyTable["executor"], "; ")
	// No unit has Role: cheap, so apply writes no cheap: key.
	want := []string{"executor: " + executor, "fallback: " + fallback}
	for _, role := range []string{"heavy", "review", "acceptance", "replan"} {
		want = append(want, role+": "+legacyTable[role])
	}
	if got := headerLines(readRosterFile(t, root, rosterPlanPath)); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("header =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("lint after apply = (%d, %q, %q)", code, stdout, stderr)
	}
	commitRosterProject(t, root)

	code, stdout, stderr = callLifecycleCLI(t, root, "run", "--dry-run", "--all")
	if code != 0 {
		t.Fatalf("dry-run --all = (%d, %q, %q)", code, stdout, stderr)
	}
	first := func(chain string) string { return strings.SplitN(chain, "; ", 2)[0] }
	spaced := func(chain string) string { return strings.ReplaceAll(chain, " | ", " ") }
	for _, line := range []string{
		"P001.1.1 [ ] Heavy unit: " + first(legacyTable["heavy"]) + " (role heavy)",
		"P001.1.2 [ ] Plain unit: " + executor + " (plan)",
		"P001.1.3 [ ] Review unit: " + first(legacyTable["review"]) + " (role review)",
		"P001.1.4 [ ] Acceptance unit: " + first(legacyTable["acceptance"]) + " (role acceptance)",
		"fallback: " + spaced(fallback),
		"heavy: " + spaced(legacyTable["heavy"]),
		"review: " + spaced(legacyTable["review"]),
		"acceptance: " + spaced(legacyTable["acceptance"]),
		"replan: " + spaced(legacyTable["replan"]),
	} {
		if !strings.Contains(stdout, line+"\n") {
			t.Errorf("dry-run --all lacks %q:\n%s", line, stdout)
		}
	}

	// Single-unit dry-run of the next unit (a Role unit) names the role.
	code, stdout, stderr = callLifecycleCLI(t, root, "run", "--dry-run")
	if code != 0 || !strings.Contains(stdout, "unit: P001.1.1 Heavy unit\nagent: pi\nmodel: zai/glm-5.3\neffort: max\nfrom: role heavy\n") {
		t.Fatalf("single dry-run = (%d, %q, %q)", code, stdout, stderr)
	}

	before := readRosterFile(t, root, rosterPlanPath)
	code, stdout, stderr = callLifecycleCLI(t, root, "roster", "apply", "P001")
	if code != 1 || !strings.Contains(stdout, "header already matches the roster") {
		t.Fatalf("repeat apply = (%d, %q, %q)", code, stdout, stderr)
	}
	if readRosterFile(t, root, rosterPlanPath) != before {
		t.Fatal("repeat apply changed the plan")
	}
}

func TestLegacyRosterProfileReordersChains(t *testing.T) {
	rosterEnv(t, draftV1Roster(t))
	root := rosterProject(t, rosterPlan(""))
	if code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001"); code != 0 {
		t.Fatalf("roster apply = (%d, %q, %q)", code, stdout, stderr)
	}
	code, stdout, stderr := callLifecycleCLI(t, root, "roster", "apply", "P001", "--profile", "codex-first")
	if code != 0 || !strings.Contains(stdout, "executor: pi | zai/glm-5.3-flash | max → codex | gpt-6-luna | max") {
		t.Fatalf("roster apply --profile = (%d, %q, %q)", code, stdout, stderr)
	}
	want := []string{
		"executor: codex | gpt-6-luna | max",
		"fallback: pi | zai/glm-5.3-flash | max; claude | claude-sonnet-5-5 | high",
		"heavy: codex | gpt-6.1-sol | high; pi | zai/glm-5.3 | max; claude | claude-opus-5-5 | high",
		"review: " + legacyTable["review"],
		"acceptance: " + legacyTable["acceptance"],
		"replan: " + legacyTable["replan"],
	}
	if got := headerLines(readRosterFile(t, root, rosterPlanPath)); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("header =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("lint after profile apply = (%d, %q, %q)", code, stdout, stderr)
	}
}

func TestLegacyRosterSingleProvider(t *testing.T) {
	routes := "[route.sonnet]\nrun = \"claude | claude-sonnet-5-5 | high\"\ntier = \"standard\"\n\n[route.opus]\nrun = \"claude | claude-opus-5-5 | high\"\ntier = \"strong\"\n"
	roles := "[role]\ncheap = \"standard\"\nexecutor = \"standard\"\nheavy = \"strong\"\nreview = \"strong\"\nacceptance = \"trusted\"\nreplan = \"trusted\"\n\n"
	glm := "[route.glm]\nrun = \"pi | zai/glm-5.3 | max\"\ntier = \"strong\"\n\n"
	for _, tc := range []struct{ name, roster, role string }{
		{"claude only", "spend = [\"claude\"]\n\n[provider.claude]\ntrusted = true\n\n" + roles + routes, "role executor:"},
		{"review left with one provider", "spend = [\"zai\", \"claude\"]\n\n[provider.zai]\ncheck = true\n\n[provider.claude]\ntrusted = true\n\n" + roles + glm + routes, "role review:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rosterEnv(t, tc.roster)
			code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "show")
			if code != 2 || stdout != "" || !strings.Contains(stderr, tc.role) || !strings.Contains(stderr, "need at least two providers") || !strings.Contains(stderr, "roster.toml:") {
				t.Fatalf("roster show = (%d, %q, %q)", code, stdout, stderr)
			}
		})
	}

	t.Run("plan without fallback", func(t *testing.T) {
		rosterEnv(t, draftV1Roster(t))
		root := rosterProject(t, rosterPlan("executor: claude | claude-opus-5-5 | high\nheavy: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high\nreview: "+legacyTable["review"]+"\nacceptance: "+legacyTable["acceptance"]+"\n"))
		code, stdout, stderr := callLifecycleCLI(t, root, "lint")
		if code != 0 || !strings.Contains(stdout+stderr, "no fallback for executor") {
			t.Fatalf("lint without fallback = (%d, %q, %q)", code, stdout, stderr)
		}
	})
}
