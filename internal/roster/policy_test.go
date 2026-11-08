package roster

import (
	"errors"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
)

// TestPolicyFixture reads the v4 scenario fixture: the accesses in file
// order are the default priority, every access carries its row of cells,
// and the low policy of the variants file next to it merges in.
func TestPolicyFixture(t *testing.T) {
	policy, err := LoadPolicy("testdata/policy.toml")
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if got := strings.Join(policy.AccessNames(), ","); got != "opencode-go,zai,codex,claude,opencode,local" {
		t.Fatalf("access order = %s", got)
	}
	zai, _ := policy.Access("zai")
	if !zai.Check || zai.Kind != "subscription" || dayText(zai.Expires) != "2026-06-30" {
		t.Fatalf("zai = %+v", zai)
	}
	if got := zai.Cell("cheap"); got != (Cell{Model: "glm-5.3-flash", Effort: "low"}) {
		t.Fatalf("zai cheap = %+v", got)
	}
	if !zai.Cell("review").Empty() {
		t.Fatalf("zai review = %+v, want empty", zai.Cell("review"))
	}
	local, _ := policy.Access("local")
	if got := local.Cell("executor"); got != (Cell{Model: "strata/swift-1.5-iq3_s"}) {
		t.Fatalf("local executor = %+v: a bare model passes no effort", got)
	}
	low, ok := policy.Preset("low")
	if !ok || low.Source != SourceVariants {
		t.Fatalf("preset low = %+v, %v", low, ok)
	}
	if strings.Join(low.Off, ",") != "claude,local,opencode" {
		t.Fatalf("low off = %v", low.Off)
	}
	if cell, ok := low.Roles["zai"]["executor"]; !ok || !cell.Empty() {
		t.Fatalf("low zai executor = %+v, %v; want a clearing empty cell", cell, ok)
	}
}

// TestParseCell reads MODEL@EFFORT: the effort follows the last '@', a
// bare model passes none, and a broken text is refused.
func TestParseCell(t *testing.T) {
	for text, want := range map[string]Cell{
		"":                  {},
		"glm-5.3":           {Model: "glm-5.3"},
		"glm-5.3@high":      {Model: "glm-5.3", Effort: "high"},
		"vendor@x/model@lo": {Model: "vendor@x/model", Effort: "lo"},
	} {
		got, err := ParseCell(text)
		if err != nil || got != want {
			t.Errorf("ParseCell(%q) = %+v, %v; want %+v", text, got, err, want)
		}
		if got.String() != text {
			t.Errorf("Cell(%q).String() = %q", text, got.String())
		}
	}
	for _, text := range []string{"@high", "glm@", "glm 5", "glm|5"} {
		if _, err := ParseCell(text); err == nil {
			t.Errorf("ParseCell(%q) = nil error", text)
		}
	}
}

// TestSavePolicyRendersTable writes a policy and reads it back unchanged.
func TestSavePolicyRendersTable(t *testing.T) {
	policy, err := ParsePolicy("roster.toml", policyHeader+policyZai+policyCodex)
	if err != nil {
		t.Fatalf("ParsePolicy() error = %v", err)
	}
	text := policyText(policy)
	again, err := ParsePolicy("roster.toml", text)
	if err != nil {
		t.Fatalf("rendered policy does not parse: %v\n%s", err, text)
	}
	if policyText(again) != text {
		t.Fatalf("render is not stable:\n%s\n---\n%s", text, policyText(again))
	}
	for _, line := range []string{"[access.zai.roles]", `executor   = "glm-5.3@high"`, "check = true", `acceptance = "gpt-6.1-sol@medium"`} {
		if !strings.Contains(text, line) {
			t.Errorf("rendered policy lacks %q:\n%s", line, text)
		}
	}
}

const policyHeader = "version = 4\n"

const policyZai = `
[access.zai]
agents = ["pi"]
check  = true

[access.zai.roles]
executor = "glm-5.3@high"
cheap    = "glm-5.3-flash@low"
`

const policyCodex = `
[access.codex]
agents = ["codex"]

[access.codex.roles]
acceptance = "gpt-6.1-sol@medium"
`

// TestPolicyErrors pins the located errors of the v4 parser, including the
// hints that send an older roster to the migration.
func TestPolicyErrors(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		needle string
		line   int
		want   string
	}{
		{name: "no version", text: policyZai, line: 1, want: "version must be 4"},
		{name: "version three", text: "version = 3\n" + policyZai, needle: "version = 3", want: "run `docflow roster migrate`"},
		{name: "role table of v3", text: policyHeader + policyZai + "\n[role]\ncheap = \"cheap\"\n", needle: "[role]", want: "[role] was replaced"},
		{name: "route table of v3", text: policyHeader + policyZai + "\n[route.\"zai/glm\"]\nhigh = \"strong\"\n", needle: "[route", want: "[route] was replaced"},
		{name: "preset in roster.toml", text: policyHeader + policyZai + "\n[preset.low]\noff = [\"zai\"]\n", needle: "[preset.low]", want: "variants file"},
		{name: "budget key", text: policyHeader + "[access.zai]\nagents = [\"pi\"]\nbudget = \"lean\"\n", needle: "budget", want: "'budget' was removed"},
		{name: "trusted key", text: policyHeader + "[access.zai]\nagents = [\"pi\"]\ntrusted = true\n", needle: "trusted", want: "'trusted' was removed"},
		{name: "unknown key", text: policyHeader + "spend = [\"zai\"]\n", needle: "spend", want: "unknown key 'spend'"},
		{name: "no agents", text: policyHeader + "[access.zai]\nkind = \"local\"\n", needle: "[access.zai]", want: "agents is required"},
		{name: "unknown agent", text: policyHeader + "[access.zai]\nagents = [\"gemini\"]\n", needle: "agents", want: "unknown agent 'gemini'"},
		{name: "unknown role", text: policyHeader + "[access.zai]\nagents = [\"pi\"]\n[access.zai.roles]\nplanner = \"glm\"\n", needle: "planner", want: "unknown role 'planner'"},
		{name: "empty cell", text: policyHeader + "[access.zai]\nagents = [\"pi\"]\n[access.zai.roles]\ncheap = \"\"\n", needle: "cheap", want: "leave the role out"},
		{name: "broken cell", text: policyHeader + "[access.zai]\nagents = [\"pi\"]\n[access.zai.roles]\ncheap = \"glm@\"\n", needle: "cheap", want: "effort after '@' is empty"},
		{name: "check judges", text: policyHeader + "[access.zai]\nagents = [\"pi\"]\ncheck = true\n[access.zai.roles]\nreview = \"glm-5.3\"\n", needle: "review", want: "a check access never judges"},
		{name: "repeated access", text: policyHeader + "[access.zai]\nagents = [\"pi\"]\n\n[access.zai]\nagents = [\"pi\"]\n", line: 5, want: "table [access.zai] is defined twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePolicy("roster.toml", tc.text)
			if err == nil {
				t.Fatalf("ParsePolicy() = nil error, want %q", tc.want)
			}
			var format clierrors.FormatError
			if !errors.As(err, &format) {
				t.Fatalf("ParsePolicy() error = %v (%T), want a FormatError", err, err)
			}
			line := tc.line
			if line == 0 {
				line = lineOf(tc.text, tc.needle)
			}
			if format.Line != line {
				t.Errorf("error = %s, want line %d", format.Error(), line)
			}
			if !strings.Contains(format.Text, tc.want) {
				t.Errorf("error text = %q, want %q", format.Text, tc.want)
			}
		})
	}
}

// TestPresetErrors pins the v4 preset grammar of a variants file.
func TestPresetErrors(t *testing.T) {
	policy, err := ParsePolicy("roster.toml", policyHeader+policyZai+policyCodex)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ text, want string }{
		{"version = 2\n[preset.low]\noff = [\"zai\"]\n", "previous format"},
		{"version = 3\n[preset.low]\nlean = [\"zai\"]\n", "'lean' was removed"},
		{"version = 3\n[preset.low]\noff = [\"claude\"]\n", "unknown access 'claude'"},
		{"version = 3\n[preset.low.roles.zai]\nreview = \"glm-5.3\"\n", "a check access never judges"},
		{"version = 3\n[preset.low.roles.nobody]\ncheap = \"x\"\n", "unknown access 'nobody'"},
	} {
		_, err := parseVariantPresets(policy, "roster.variants.toml", tc.text, SourceVariants, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("parseVariantPresets(%q) error = %v, want %q", tc.text, err, tc.want)
		}
	}
	presets, err := parseVariantPresets(policy, "roster.variants.toml", "version = 3\n[preset.low]\noff = [\"codex\"]\norder = [\"codex\"]\n[preset.low.roles.zai]\nexecutor = \"\"\ncheap = \"glm-5.3@max\"\n", SourceVariants, nil)
	if err != nil || len(presets) != 1 {
		t.Fatalf("parseVariantPresets() = %+v, %v", presets, err)
	}
	if got := variantsText(presets); !strings.Contains(got, "[preset.low.roles.zai]\nexecutor   = \"\"\ncheap      = \"glm-5.3@max\"\n") {
		t.Fatalf("variantsText() =\n%s", got)
	}
}

// lineOf returns the 1-based line of the first line containing needle.
func lineOf(text, needle string) int {
	for n, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return n + 1
		}
	}
	return 0
}

// policyRoles is the [role] table of the older formats (v1 to v3), which
// the migration tests feed in.
const policyRoles = `
[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"
`
