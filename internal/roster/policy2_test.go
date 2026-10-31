package roster

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/clierrors"
)

const (
	policyHeader = "version = 2\n"
	policyZai    = "[access.zai]\nagents = [\"pi\"]\n"
	policyRoles  = `[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"
`
)

func TestPolicyV2Fixture(t *testing.T) {
	policy, err := LoadPolicy(filepath.Join("testdata", "policy-v2.toml"))
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if policy.Version != PolicyVersion {
		t.Fatalf("Version = %d, want %d", policy.Version, PolicyVersion)
	}
	wantNames := []string{"opencode-go", "zai", "codex", "claude", "opencode", "local"}
	if got := policy.AccessNames(); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("AccessNames() = %v, want file order %v", got, wantNames)
	}

	opencodeGo, ok := policy.Access("opencode-go")
	if !ok {
		t.Fatal("Access(opencode-go) not found")
	}
	if want := []string{"pi", "opencode"}; !reflect.DeepEqual(opencodeGo.Agents, want) {
		t.Errorf("opencode-go agents = %v, want %v", opencodeGo.Agents, want)
	}
	if opencodeGo.Kind != "subscription" || opencodeGo.Budget != BudgetPrefer || !opencodeGo.Trusted || opencodeGo.Check {
		t.Errorf("opencode-go = %+v, want subscription/prefer/trusted", opencodeGo)
	}
	if want := []string{"deepseek-*"}; !reflect.DeepEqual(opencodeGo.Models, want) {
		t.Errorf("opencode-go models = %v, want %v", opencodeGo.Models, want)
	}
	if !opencodeGo.Expires.IsZero() {
		t.Errorf("opencode-go expires = %v, want no expiry", opencodeGo.Expires)
	}

	// opencode-go and opencode are separate accesses: the same subscription
	// reached through pi and opencode is one quota, the OpenCode gateway is
	// another.
	opencode, ok := policy.Access("opencode")
	if !ok {
		t.Fatal("Access(opencode) not found")
	}
	if want := []string{"opencode"}; !reflect.DeepEqual(opencode.Agents, want) {
		t.Errorf("opencode agents = %v, want %v", opencode.Agents, want)
	}
	if opencode.Kind != "metered" || opencode.Budget != BudgetOff {
		t.Errorf("opencode = %+v, want metered/off", opencode)
	}

	zai, ok := policy.Access("zai")
	if !ok {
		t.Fatal("Access(zai) not found")
	}
	if zai.Budget != BudgetLean || !zai.Check || zai.Trusted {
		t.Errorf("zai = %+v, want lean/check", zai)
	}
	if want := []string{"glm-5.3*"}; !reflect.DeepEqual(zai.Models, want) {
		t.Errorf("zai models = %v, want %v", zai.Models, want)
	}
	if want := []string{"glm-5.3-thinking"}; !reflect.DeepEqual(zai.Exclude, want) {
		t.Errorf("zai exclude = %v, want %v", zai.Exclude, want)
	}
	if want := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC); !zai.Expires.Equal(want) {
		t.Errorf("zai expires = %v, want %v", zai.Expires, want)
	}

	codex, _ := policy.Access("codex")
	if codex.Budget != BudgetMinimal || !codex.Trusted || codex.Check {
		t.Errorf("codex = %+v, want minimal/trusted", codex)
	}
	claude, _ := policy.Access("claude")
	if claude.Budget != BudgetOff || !claude.Trusted {
		t.Errorf("claude = %+v, want off/trusted", claude)
	}
	local, _ := policy.Access("local")
	if local.Kind != "local" || !local.Check || local.Budget != BudgetOff {
		t.Errorf("local = %+v, want local/check/off", local)
	}

	wantClasses := map[string]string{
		"opencode-go/deepseek-v4.1-flash": "cheap",
		"opencode-go/deepseek-v4.1":       "standard",
		"zai/glm-5.3-flash":               "cheap",
		"zai/glm-5.3":                     "strong",
		"codex/gpt-6-luna":                "cheap",
		"codex/gpt-6.1-sol":               "strong",
	}
	if !reflect.DeepEqual(policy.Models, wantClasses) {
		t.Errorf("Models = %v, want %v", policy.Models, wantClasses)
	}

	wantRoles := map[string]string{
		"cheap":      "cheap",
		"executor":   "standard",
		"heavy":      "strong",
		"review":     "standard",
		"acceptance": "trusted",
		"replan":     "trusted",
	}
	if !reflect.DeepEqual(policy.Roles, wantRoles) {
		t.Errorf("Roles = %v, want %v", policy.Roles, wantRoles)
	}

	wantEfforts := map[string]string{
		"opencode-go/deepseek-v4.1-flash": "high",
		"zai/glm-5.3":                     "max",
		"codex/gpt-6.1-sol":               "medium",
	}
	if !reflect.DeepEqual(policy.Efforts, wantEfforts) {
		t.Errorf("Efforts = %v, want %v", policy.Efforts, wantEfforts)
	}

	if got := policy.PresetNames(); !reflect.DeepEqual(got, []string{"low"}) {
		t.Fatalf("PresetNames() = %v, want [low]", got)
	}
	low, ok := policy.Preset("low")
	if !ok {
		t.Fatal("Preset(low) not found")
	}
	wantLow := Preset{
		Name:    "low",
		Off:     []string{"claude", "local"},
		Lean:    []string{"zai"},
		Minimal: []string{"codex"},
		Prefer:  []string{"opencode-go"},
		Order:   []string{"opencode-go", "zai", "codex"},
		Models:  map[string][]string{"zai": {"*flash*"}},
	}
	if !reflect.DeepEqual(*low, wantLow) {
		t.Errorf("Preset(low) = %+v, want %+v", *low, wantLow)
	}
}

func TestPolicyV2Minimal(t *testing.T) {
	policy, err := ParsePolicy("roster.toml", policyHeader+policyZai+policyRoles)
	if err != nil {
		t.Fatalf("ParsePolicy() error = %v", err)
	}
	access, ok := policy.Access("zai")
	if !ok {
		t.Fatal("Access(zai) not found")
	}
	if access.Budget != DefaultBudget {
		t.Errorf("budget = %q, want the default %q", access.Budget, DefaultBudget)
	}
	if access.Kind != "" || access.Trusted || access.Check || len(access.Models) != 0 || len(access.Exclude) != 0 || !access.Expires.IsZero() {
		t.Errorf("access = %+v, want everything but agents defaulted", access)
	}
	if len(policy.Models) != 0 || len(policy.Presets) != 0 {
		t.Errorf("Models/Presets = %v/%v, want empty", policy.Models, policy.Presets)
	}
	if _, ok := policy.Preset("low"); ok {
		t.Error("Preset(low) found in a policy without presets")
	}
	if _, ok := policy.Access("opencode-go"); ok {
		t.Error("Access(opencode-go) found in a policy with one access")
	}
}

func TestPolicyV2Errors(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		needle string // line lookup; ignored when line is set
		line   int    // 0 means the line of needle
		want   string
	}{
		{
			name: "no version",
			text: policyZai + policyRoles,
			line: 1,
			want: "version must be 2",
		},
		{
			name:   "version one",
			text:   "version = 1\n" + policyZai + policyRoles,
			needle: "version = 1",
			want:   "version must be 2",
		},
		{
			name:   "version as a string",
			text:   "version = \"2\"\n" + policyZai + policyRoles,
			needle: "version = \"2\"",
			want:   "version must be 2",
		},
		{
			name:   "unknown top-level key",
			text:   policyHeader + "spend = [\"zai\"]\n",
			needle: "spend",
			want:   "unknown key 'spend'",
		},
		{
			name:   "access not a table",
			text:   policyHeader + "access = \"zai\"\n",
			needle: "access = \"zai\"",
			want:   "access must be a table",
		},
		{
			name:   "access section not a table",
			text:   policyHeader + "[access]\nzai = \"pi\"\n",
			needle: "zai = \"pi\"",
			want:   "access zai must be a [access.zai] table",
		},
		{
			name:   "unknown access key",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\"]\nbogus = true\n",
			needle: "bogus",
			want:   "unknown key 'bogus' in [access.zai]",
		},
		{
			name:   "empty agents",
			text:   policyHeader + "[access.zai]\nagents = []\n",
			needle: "agents = []",
			want:   "agents must not be empty",
		},
		{
			name:   "missing agents",
			text:   policyHeader + "[access.zai]\nbudget = \"lean\"\n",
			needle: "[access.zai]",
			want:   "agents is required",
		},
		{
			name:   "agents not an array",
			text:   policyHeader + "[access.zai]\nagents = \"pi\"\n",
			needle: "agents = \"pi\"",
			want:   "agents must be an array",
		},
		{
			name:   "unknown agent",
			text:   policyHeader + "[access.zai]\nagents = [\"gemini\"]\n",
			needle: "agents",
			want:   "unknown agent 'gemini'",
		},
		{
			name:   "repeated agent",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\", \"pi\"]\n",
			needle: "agents",
			want:   "duplicate 'pi'",
		},
		{
			name:   "check and trusted",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\"]\ncheck = true\ntrusted = true\n",
			needle: "trusted = true",
			want:   "check and trusted are mutually exclusive",
		},
		{
			name:   "minimal without trusted",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\"]\nbudget = \"minimal\"\n",
			needle: "budget = \"minimal\"",
			want:   "minimal requires trusted",
		},
		{
			name:   "unknown kind",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\"]\nkind = \"free\"\n",
			needle: "kind = \"free\"",
			want:   "kind must be subscription, metered or local",
		},
		{
			name:   "unknown budget",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\"]\nbudget = \"cheap\"\n",
			needle: "budget = \"cheap\"",
			want:   "budget must be prefer, normal, lean, minimal or off",
		},
		{
			name:   "bad expires date",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\"]\nexpires = \"30.06.2026\"\n",
			needle: "expires =",
			want:   "date must be YYYY-MM-DD",
		},
		{
			name:   "impossible expires date",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\"]\nexpires = \"2026-02-30\"\n",
			needle: "expires =",
			want:   "date must be YYYY-MM-DD",
		},
		{
			name:   "bad model glob",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\"]\nmodels = [\"glm-[\"]\n",
			needle: "models = [",
			want:   "bad glob",
		},
		{
			name:   "model glob not a string",
			text:   policyHeader + "[access.zai]\nagents = [\"pi\"]\nmodels = [5]\n",
			needle: "models = [",
			want:   "item 1 must be a model glob",
		},
		{
			name:   "access name with slash",
			text:   policyHeader + "[access.\"zai/go\"]\nagents = [\"pi\"]\n",
			needle: "[access.\"zai/go\"]",
			want:   "access name 'zai/go'",
		},
		{
			name: "repeated access table",
			text: policyHeader + "[access.zai]\nagents = [\"pi\"]\n\n[access.zai]\nagents = [\"opencode\"]\n",
			line: 5,
			want: "table [access.zai] is defined twice",
		},
		{
			name:   "model unknown access",
			text:   policyHeader + "[model.\"nope/x\"]\nclass = \"cheap\"\n",
			needle: "[model.\"nope/x\"]",
			want:   "unknown access 'nope'",
		},
		{
			name:   "model key without access",
			text:   policyHeader + policyZai + "[model.\"glm-5.3\"]\nclass = \"cheap\"\n",
			needle: "[model.\"glm-5.3\"]",
			want:   "key must be ACCESS/MODEL",
		},
		{
			name:   "model unknown key",
			text:   policyHeader + policyZai + "[model.\"zai/glm-5.3\"]\nclass = \"strong\"\nreasoning = true\n",
			needle: "reasoning",
			want:   "unknown key 'reasoning'",
		},
		{
			name:   "model bad class",
			text:   policyHeader + policyZai + "[model.\"zai/glm-5.3\"]\nclass = \"medium\"\n",
			needle: "class = \"medium\"",
			want:   "class must be cheap, standard or strong",
		},
		{
			name:   "model missing class",
			text:   policyHeader + policyZai + "[model.\"zai/glm-5.3\"]\n",
			needle: "[model.\"zai/glm-5.3\"]",
			want:   "class is required",
		},
		{
			name: "missing role table",
			text: policyHeader + policyZai,
			line: 1,
			want: "[role] is required",
		},
		{
			name:   "unknown role",
			text:   policyHeader + policyZai + "[role]\nbogus = \"cheap\"\n",
			needle: "bogus",
			want:   "unknown role 'bogus'",
		},
		{
			name:   "bad role requirement",
			text:   policyHeader + policyZai + "[role]\ncheap = \"weak\"\n",
			needle: "cheap = \"weak\"",
			want:   "requirement must be cheap, standard, strong or trusted",
		},
		{
			name:   "missing role requirement",
			text:   policyHeader + policyZai + strings.Replace(policyRoles, "executor   = \"standard\"\n", "", 1),
			needle: "[role]",
			want:   "role executor: no requirement",
		},
		{
			name:   "effort unknown access",
			text:   policyHeader + policyZai + policyRoles + "[effort]\n\"nope/x\" = \"high\"\n",
			needle: "\"nope/x\"",
			want:   "unknown access 'nope'",
		},
		{
			name:   "effort key without model",
			text:   policyHeader + policyZai + policyRoles + "[effort]\n\"glm\" = \"high\"\n",
			needle: "\"glm\"",
			want:   "key must be ACCESS/MODEL",
		},
		{
			name:   "effort bad level",
			text:   policyHeader + policyZai + policyRoles + "[effort]\n\"zai/glm-5.3\" = \"very high\"\n",
			needle: "very high",
			want:   "level must be a single word",
		},
		{
			name:   "preset not a table",
			text:   policyHeader + policyZai + policyRoles + "[preset]\nlow = \"x\"\n",
			needle: "low = \"x\"",
			want:   "preset low must be a [preset.low] table",
		},
		{
			name:   "unknown preset key",
			text:   policyHeader + policyZai + policyRoles + "[preset.low]\nbogus = []\n",
			needle: "bogus",
			want:   "unknown key 'bogus' in [preset.low]",
		},
		{
			name:   "preset unknown access",
			text:   policyHeader + policyZai + policyRoles + "[preset.low]\noff = [\"nope\"]\n",
			needle: "off = [",
			want:   "unknown access 'nope'",
		},
		{
			name:   "preset repeated access in one list",
			text:   policyHeader + policyZai + policyRoles + "[preset.low]\nlean = [\"zai\", \"zai\"]\n",
			needle: "lean = [",
			want:   "duplicate 'zai'",
		},
		{
			name:   "preset access in two conditions",
			text:   policyHeader + policyZai + policyRoles + "[preset.low]\noff = [\"zai\"]\nlean = [\"zai\"]\n",
			needle: "lean = [",
			want:   "access 'zai' is listed in both off and lean",
		},
		{
			name:   "preset minimal untrusted",
			text:   policyHeader + policyZai + policyRoles + "[preset.low]\nminimal = [\"zai\"]\n",
			needle: "minimal = [",
			want:   "minimal access 'zai' is not trusted",
		},
		{
			name:   "preset order repeated",
			text:   policyHeader + policyZai + policyRoles + "[preset.low]\norder = [\"zai\", \"zai\"]\n",
			needle: "order = [",
			want:   "duplicate 'zai'",
		},
		{
			name:   "preset models unknown access",
			text:   policyHeader + policyZai + policyRoles + "[preset.low]\nmodels = { nope = [\"*\"] }\n",
			needle: "models = {",
			want:   "unknown access 'nope'",
		},
		{
			name:   "preset models bad glob",
			text:   policyHeader + policyZai + policyRoles + "[preset.low]\nmodels = { zai = [\"glm-[\"] }\n",
			needle: "models = {",
			want:   "bad glob",
		},
		{
			name: "repeated preset table",
			text: policyHeader + policyZai + policyRoles + "[preset.low]\noff = [\"zai\"]\n\n[preset.low]\nlean = [\"zai\"]\n",
			line: 14,
			want: "table [preset.low] is defined twice",
		},
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
			if line == 0 {
				t.Fatalf("test bug: needle %q not found in the text", tc.needle)
			}
			if format.Path != "roster.toml" || format.Line != line {
				t.Errorf("error = %s, want roster.toml:%d", format.Error(), line)
			}
			if !strings.Contains(format.Text, tc.want) {
				t.Errorf("error text = %q, want %q", format.Text, tc.want)
			}
			coded, ok := err.(interface{ ExitCode() int })
			if !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
				t.Errorf("exit code of %v is not %d", err, clierrors.EXIT_FORMAT)
			}
		})
	}
}

func TestPolicyV2IsV2(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"v2", "version = 2\n", true},
		{"v1", `spend = ["zai"]` + "\n", false},
		{"no version", "[access.zai]\nagents = [\"pi\"]\n", false},
		{"version three", "version = 3\n", false},
		{"version string", "version = \"2\"\n", false},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".toml")
			if err := os.WriteFile(path, []byte(tc.text), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := IsV2(path)
			if err != nil {
				t.Fatalf("IsV2() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("IsV2() = %v, want %v", got, tc.want)
			}
		})
	}

	if got, err := IsV2(filepath.Join("testdata", "policy-v2.toml")); err != nil || !got {
		t.Errorf("IsV2(policy-v2.toml) = %v, %v, want true, nil", got, err)
	}

	missing := filepath.Join(dir, "missing.toml")
	if _, err := IsV2(missing); err == nil {
		t.Error("IsV2(missing) = nil error, want a read error")
	} else if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
		t.Errorf("exit code of %v is not %d", err, clierrors.EXIT_FORMAT)
	}

	broken := filepath.Join(dir, "broken.toml")
	if err := os.WriteFile(broken, []byte("version = 2\n[access\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := IsV2(broken); err == nil {
		t.Error("IsV2(broken) = nil error, want a parse error")
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
