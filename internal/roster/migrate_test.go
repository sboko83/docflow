package roster

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// minimal is the smallest valid v1 roster: two providers, no flags, one
// route per tier and provider.
const minimal = `spend = ["zai", "codex"]
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.z1]
run = "pi | zai/m | high"
tier = "cheap"
[route.zs]
run = "pi | zai/m2 | high"
tier = "strong"
[route.c1]
run = "codex | c | high"
tier = "cheap"
[route.cs]
run = "codex | c2 | high"
tier = "strong"
`

// migratedPolicy migrates a roster file and returns the parsed v2 policy,
// so the tests read the result the way the resolver will.
func migratedPolicy(t *testing.T, path string) *v3Policy {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text, err := migrateToV3("roster.toml", string(data))
	if err != nil {
		t.Fatalf("migrateToV3(%s) error = %v", path, err)
	}
	policy, err := parseV3Policy(path, text)
	if err != nil {
		t.Fatalf("the migrated %s does not parse: %v\n%s", path, err, text)
	}
	return policy
}

func migrateError(t *testing.T, text string) error {
	t.Helper()
	_, err := migrateToV3("roster.toml", text)
	if err == nil {
		t.Fatalf("migrateToV3() error = nil, want an error")
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != 2 {
		t.Fatalf("migrateToV3() error = %v, want exit code 2", err)
	}
	return err
}

func accessOf(t *testing.T, policy *v3Policy, name string) *v3Access {
	t.Helper()
	access, ok := policy.Access(name)
	if !ok {
		t.Fatalf("access %q is missing; accesses: %v", name, policy.AccessNames())
	}
	return access
}

func TestMigrateDraft(t *testing.T) {
	policy := migratedPolicy(t, "testdata/draft-v1.toml")
	if got, want := policy.AccessNames(), []string{"zai", "codex", "claude"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("accesses = %v, want %v in spend order", got, want)
	}
	zai := accessOf(t, policy, "zai")
	if !zai.Check || zai.Trusted {
		t.Fatalf("zai flags = check %t trusted %t, want check only", zai.Check, zai.Trusted)
	}
	if !reflect.DeepEqual(zai.Agents, []string{"pi"}) {
		t.Fatalf("zai agents = %v, want pi", zai.Agents)
	}
	if want := []string{"glm-5.3-flash", "glm-5.3"}; !reflect.DeepEqual(zai.Models, want) {
		t.Fatalf("zai models = %v, want %v", zai.Models, want)
	}
	claude := accessOf(t, policy, "claude")
	if !claude.Trusted || claude.Check {
		t.Fatalf("claude flags = check %t trusted %t, want trusted only", claude.Check, claude.Trusted)
	}
	if codex := accessOf(t, policy, "codex"); len(codex.Models) != 2 {
		t.Fatalf("codex models = %v, want both models", codex.Models)
	}
	// A tier belongs to the route, not the model: the route carries the
	// strongest tier the model served and that route's effort, "-" stays
	// "no effort" instead of becoming a class-ladder guess.
	routes := map[string]v3Route{
		"zai/glm-5.3-flash":        {Access: "zai", Model: "glm-5.3-flash", Eff: "max", Class: "standard"},
		"zai/glm-5.3":              {Access: "zai", Model: "glm-5.3", Eff: "max", Class: "strong"},
		"codex/gpt-6-luna":         {Access: "codex", Model: "gpt-6-luna", Eff: "max", Class: "standard"},
		"codex/gpt-6.1-sol":        {Access: "codex", Model: "gpt-6.1-sol", Eff: "high", Class: "strong"},
		"claude/claude-haiku-5-5":  {Access: "claude", Model: "claude-haiku-5-5", Eff: "-", Class: "cheap"},
		"claude/claude-sonnet-5-5": {Access: "claude", Model: "claude-sonnet-5-5", Eff: "high", Class: "standard"},
		"claude/claude-opus-5-5":   {Access: "claude", Model: "claude-opus-5-5", Eff: "high", Class: "strong"},
	}
	for key, want := range routes {
		access, id, _ := splitModelKey(key)
		got := policy.routesOf(access, id)
		if len(got) != 1 || got[0] != want {
			t.Errorf("route of %s = %v, want %v", key, got, want)
		}
	}
	wantRoles := map[string]string{"cheap": "cheap", "executor": "standard", "heavy": "strong", "review": "strong", "acceptance": "trusted", "replan": "trusted"}
	if !reflect.DeepEqual(policy.Roles, wantRoles) {
		t.Fatalf("roles = %v, want %v", policy.Roles, wantRoles)
	}
	preset, ok := policy.Preset("codex-first")
	if !ok {
		t.Fatalf("preset codex-first is missing; presets: %v", policy.PresetNames())
	}
	if want := []string{"codex", "zai", "claude"}; !reflect.DeepEqual(preset.Order, want) {
		t.Fatalf("preset order = %v, want %v", preset.Order, want)
	}
	// The profile spend names every access, so nothing is switched off.
	if len(preset.Off) != 0 {
		t.Fatalf("preset off = %v, want none", preset.Off)
	}
}

func TestMigrateLegacyFixture(t *testing.T) {
	policy := migratedPolicy(t, "testdata/legacy-roster.toml")
	if got, want := policy.AccessNames(), []string{"opencode-go", "zai", "codex", "pi", "strata"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("accesses = %v, want %v", got, want)
	}
	// One subscription, two channels: pi and opencode routes meet in one
	// access, because the quota unit is the subscription.
	goAccess := accessOf(t, policy, "opencode-go")
	if want := []string{"pi", "opencode"}; !reflect.DeepEqual(goAccess.Agents, want) {
		t.Fatalf("opencode-go agents = %v, want %v", goAccess.Agents, want)
	}
	if want := []string{"kimi-k3.1-pro"}; !reflect.DeepEqual(goAccess.Models, want) {
		t.Fatalf("opencode-go models = %v, want %v", goAccess.Models, want)
	}
	if route := policy.routesOf("opencode-go", "kimi-k3.1-pro"); len(route) != 1 || route[0] != (v3Route{Access: "opencode-go", Model: "kimi-k3.1-pro", Eff: "high", Class: "strong"}) {
		t.Fatalf("route of opencode-go/kimi-k3.1-pro = %v, want high = strong", route)
	}
	// The local provider stays local.
	if got := accessOf(t, policy, "strata").Kind; got != "local" {
		t.Fatalf("strata kind = %q, want local", got)
	}
	// A pi model without a prefix belongs to the pi access.
	pi := accessOf(t, policy, "pi")
	if want := []string{"some-default"}; !reflect.DeepEqual(pi.Models, want) {
		t.Fatalf("pi models = %v, want %v", pi.Models, want)
	}
	if !pi.Trusted {
		t.Fatalf("pi flags = trusted %t, want trusted", pi.Trusted)
	}
	preset, ok := policy.Preset("save-claude")
	if !ok {
		t.Fatalf("preset save-claude is missing; presets: %v", policy.PresetNames())
	}
	if want := []string{"opencode-go", "zai"}; !reflect.DeepEqual(preset.Order, want) {
		t.Fatalf("preset order = %v, want %v", preset.Order, want)
	}
	// A profile spend replaces the top-level one, so the accesses it does
	// not name are switched off.
	if want := []string{"codex", "pi", "strata"}; !reflect.DeepEqual(preset.Off, want) {
		t.Fatalf("preset off = %v, want %v", preset.Off, want)
	}
	data, err := os.ReadFile("testdata/legacy-roster.toml")
	if err != nil {
		t.Fatal(err)
	}
	text, err := migrateToV3("roster.toml", string(data))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "v1 profile flag not migrated: provider codex") {
		t.Fatalf("the migrated text loses the profile flag override:\n%s", text)
	}
}

func TestMigrateDeterministic(t *testing.T) {
	data, err := os.ReadFile("testdata/legacy-roster.toml")
	if err != nil {
		t.Fatal(err)
	}
	first, err := migrateToV3("roster.toml", string(data))
	if err != nil {
		t.Fatal(err)
	}
	second, err := migrateToV3("roster.toml", string(data))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("two migrations of one roster differ:\n%s\n---\n%s", first, second)
	}
	// The v3 text goes on to version 4; a v4 text is not migrated again.
	v4, err := Migrate(first)
	if err != nil {
		t.Fatalf("migrateToV3(v3) error = %v", err)
	}
	if _, err := Migrate(v4); err == nil || !strings.Contains(err.Error(), "already version 4") {
		t.Fatalf("Migrate(v4) error = %v, want already version 4", err)
	}
}

func TestMigrateRejectsNonV1(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "unknown version",
			text: "version = 9\nspend = [\"zai\"]\n",
			want: "is not the v1 roster format",
		},
		{
			name: "broken TOML",
			text: "spend = [\"zai\"\n",
			want: "docflow: roster.toml:",
		},
		{
			name: "no spend",
			text: "[role]\ncheap = \"cheap\"\n",
			want: "spend must be an array",
		},
		{
			name: "unknown profile key",
			text: minimal + "\n[profile.x]\nspend = [\"zai\"]\nmodel = \"m\"\n",
			want: "unknown key 'model'",
		},
		{
			name: "route outside spend",
			text: `spend = ["zai"]
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.x]
run = "codex | c | high"
tier = "strong"
`,
			want: "provider codex is not in spend",
		},
		{
			name: "check and trusted meet in one access",
			text: `spend = ["go", "opencode"]
[provider.go]
check = true
[provider.opencode]
trusted = true
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.a]
run = "pi | go/m | high"
tier = "standard"
[route.b]
run = "opencode | go/m | high"
tier = "strong"
`,
			want: "access go: provider go sets check and provider opencode sets trusted",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := migrateError(t, tc.text)
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("migrateToV3() error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestMigrateBadAccessName: a v1 provider name the CLI flags cannot
// address must fail the migration, not produce a v2 file that will not
// load.
func TestMigrateBadAccessName(t *testing.T) {
	text := `spend = ["a,b"]
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.x]
run = "pi | a,b/m | high"
tier = "standard"
`
	err := migrateError(t, text)
	if !strings.Contains(err.Error(), "access name 'a,b'") {
		t.Fatalf("migrateToV3() error = %v, want the access name", err)
	}
	if !strings.Contains(err.Error(), "route x") {
		t.Fatalf("migrateToV3() error = %v, want the route location", err)
	}
}

// TestMigrateBadGlob: a model id that is not a valid glob would silently
// match nothing in the models allowlist.
func TestMigrateBadGlob(t *testing.T) {
	text := `spend = ["zai"]
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.x]
run = "pi | zai/m[x | high"
tier = "standard"
`
	err := migrateError(t, text)
	if !strings.Contains(err.Error(), "cannot be a model glob") {
		t.Fatalf("migrateToV3() error = %v, want the glob problem", err)
	}
}

// TestMigrateQuotedNames: a v1 provider or profile name with a dot is
// legal in v1 but must be quoted in v2, or the header would become a
// nested table the next load cannot match.
func TestMigrateQuotedNames(t *testing.T) {
	text := `spend = ["opencode.go"]
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.x]
run = "pi | opencode.go/kimi-k3.1-pro | high"
tier = "standard"
[profile."lean.start"]
spend = ["opencode.go"]
`
	policy := func() *v3Policy {
		migrated, err := migrateToV3("roster.toml", text)
		if err != nil {
			t.Fatalf("migrateToV3() error = %v", err)
		}
		parsed, err := parseV3Policy("roster.toml", migrated)
		if err != nil {
			t.Fatalf("the migrated roster does not parse: %v\n%s", err, migrated)
		}
		return parsed
	}()
	access := accessOf(t, policy, "opencode.go")
	if want := []string{"kimi-k3.1-pro"}; !reflect.DeepEqual(access.Models, want) {
		t.Fatalf("models of opencode.go = %v, want %v", access.Models, want)
	}
	if route := policy.routesOf("opencode.go", "kimi-k3.1-pro"); len(route) != 1 || route[0].Class != "standard" {
		t.Fatalf("route of opencode.go/kimi-k3.1-pro = %v, want class standard", route)
	}
	if _, ok := policy.Preset("lean.start"); !ok {
		t.Fatalf("preset lean.start is missing; presets: %v", policy.PresetNames())
	}
}

// TestMigrateRouteAccess pins the access of a v1 route: a pi or opencode
// model prefix is the access, a codex or claude model stays whole.
func TestMigrateRouteAccess(t *testing.T) {
	cases := []struct {
		agent, model, access, id string
	}{
		{agent: "pi", model: "zai/glm-5.3", access: "zai", id: "glm-5.3"},
		{agent: "pi", model: "some-default", access: "pi", id: "some-default"},
		{agent: "opencode", model: "opencode-go/kimi-k3.1-pro", access: "opencode-go", id: "kimi-k3.1-pro"},
		{agent: "codex", model: "gpt-6.1-sol", access: "codex", id: "gpt-6.1-sol"},
		{agent: "claude", model: "vendor/sonnet", access: "claude", id: "vendor/sonnet"},
		{agent: "pi", model: "trailing/", access: "pi", id: "trailing/"},
	}
	for _, tc := range cases {
		access, id := routeAccess(tc.agent, tc.model)
		if access != tc.access || id != tc.id {
			t.Errorf("routeAccess(%s, %s) = (%s, %s), want (%s, %s)", tc.agent, tc.model, access, id, tc.access, tc.id)
		}
	}
}

// TestMigrateEffortTie: two routes of one model at the same tier — the
// first in file order supplies the class and the effort, so the output
// does not depend on which route the loop met last.
func TestMigrateEffortTie(t *testing.T) {
	text := `spend = ["zai"]
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.a]
run = "pi | zai/glm-5.3 | high"
tier = "strong"
[route.b]
run = "pi | zai/glm-5.3 | max"
tier = "strong"
`
	migrated, err := migrateToV3("roster.toml", text)
	if err != nil {
		t.Fatalf("migrateToV3() error = %v", err)
	}
	policy, err := parseV3Policy("roster.toml", migrated)
	if err != nil {
		t.Fatalf("the migrated roster does not parse: %v\n%s", err, migrated)
	}
	if route := policy.routesOf("zai", "glm-5.3"); len(route) != 1 || route[0].Eff != "high" {
		t.Fatalf("route of zai/glm-5.3 = %v, want the first route's high", route)
	}
}

// TestMigrateProviderWithoutRoutes: an access is a channel plus its
// models, so a v1 provider no route reaches has nothing to convert.
func TestMigrateProviderWithoutRoutes(t *testing.T) {
	text := `spend = ["zai", "claude"]
[provider.claude]
trusted = true
[role]
cheap = "cheap"
executor = "standard"
heavy = "strong"
review = "strong"
acceptance = "trusted"
replan = "trusted"
[route.x]
run = "pi | zai/glm-5.3 | high"
tier = "standard"
`
	migrated, err := migrateToV3("roster.toml", text)
	if err != nil {
		t.Fatalf("migrateToV3() error = %v", err)
	}
	policy, err := parseV3Policy("roster.toml", migrated)
	if err != nil {
		t.Fatalf("the migrated roster does not parse: %v\n%s", err, migrated)
	}
	if got, want := policy.AccessNames(), []string{"zai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("accesses = %v, want %v: a route-less provider cannot name a channel", got, want)
	}
}

func TestMigrateFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "roster.toml")
	if err := os.WriteFile(path, []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}
	// A dry run reads, prints and changes nothing.
	result, err := MigrateFile(path, false)
	if err != nil {
		t.Fatalf("MigrateFile() error = %v", err)
	}
	if result.Text == "" || result.Backup != "" {
		t.Fatalf("MigrateFile() = %+v, want text and no backup", result)
	}
	if data, _ := os.ReadFile(path); string(data) != minimal {
		t.Fatalf("a read-only migration rewrote the roster")
	}
	// --write keeps the v1 text and replaces the roster with version 4.
	result, err = MigrateFile(path, true)
	if err != nil {
		t.Fatalf("MigrateFile(write) error = %v", err)
	}
	if result.Backup != path+".bak" {
		t.Fatalf("backup = %q, want %q", result.Backup, path+".bak")
	}
	if backup, _ := os.ReadFile(result.Backup); string(backup) != minimal {
		t.Fatalf("backup holds\n%s\nwant the v1 text", backup)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != result.Text {
		t.Fatalf("roster.toml does not hold the migrated text")
	}
	policy, err := ParsePolicy(path, string(data))
	if err != nil {
		t.Fatalf("the written roster does not load: %v", err)
	}
	// Without a catalog the cells come from the models the policy names:
	// the strong routes review, the cheap ones take the cheap role.
	zai, _ := policy.Access("zai")
	if got := zai.Cell("cheap"); got != (Cell{Model: "m", Effort: "high"}) {
		t.Fatalf("zai cheap = %+v\n%s", got, data)
	}
	if got := zai.Cell("review"); got != (Cell{Model: "m2", Effort: "high"}) {
		t.Fatalf("zai review = %+v\n%s", got, data)
	}
	if !zai.Cell("acceptance").Empty() {
		t.Fatalf("zai acceptance = %+v: an untrusted v1 provider never judged", zai.Cell("acceptance"))
	}
	// A second --write is a refusal, not a rewrite of the v4 file.
	_, err = MigrateFile(path, true)
	if err == nil || !strings.Contains(err.Error(), "already version 4") {
		t.Fatalf("second MigrateFile() error = %v, want already version 4", err)
	}
	if again, _ := os.ReadFile(result.Backup); string(again) != minimal {
		t.Fatalf("a refused migration rewrote the backup")
	}
}

// TestMigrateV3Scenario converts the v3 acceptance fixture with the
// scenario catalog: the base budgets become the order and the cells, an
// access at budget off becomes an off condition, and the low preset
// becomes a policy of the variants file.
func TestMigrateV3Scenario(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	catalog, err := os.ReadFile("testdata/catalog-scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cache, "docflow", "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "docflow", "roster", "catalog.json"), catalog, 0o644); err != nil {
		t.Fatal(err)
	}
	v3, err := os.ReadFile("testdata/policy-v3.toml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(path, v3, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := MigrateFile(path, true)
	if err != nil {
		t.Fatalf("MigrateFile() error = %v", err)
	}
	policy, err := LoadPolicy(path)
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if got := strings.Join(policy.AccessNames(), ","); got != "opencode-go,zai,codex,claude,opencode,local" {
		t.Fatalf("order = %s: prefer, then lean, minimal, then the accesses that were off", got)
	}
	want, err := LoadPolicy("testdata/policy.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range want.AccessNames() {
		got, _ := policy.Access(name)
		expected, _ := want.Access(name)
		if !reflect.DeepEqual(got.Roles, expected.Roles) {
			t.Errorf("%s cells = %v, want %v as in testdata/policy.toml", name, got.Roles, expected.Roles)
		}
	}
	low, ok := policy.Preset("low")
	if !ok || strings.Join(low.Off, ",") != "claude,local" || len(low.Order) != 0 {
		t.Fatalf("low = %+v", low)
	}
	if cell, ok := low.Roles["zai"]["executor"]; !ok || !cell.Empty() {
		t.Fatalf("low zai executor = %+v, %v; want cleared: the flash-only zai served no executor", cell, ok)
	}
	state, err := LoadState(StatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(state.Off, ",") != "claude,opencode,local" {
		t.Fatalf("state off = %v", state.Off)
	}
	if len(result.Notes) != 3 || len(result.Files) != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestMigrateFileMissing(t *testing.T) {
	_, err := MigrateFile(filepath.Join(t.TempDir(), "roster.toml"), false)
	if err == nil {
		t.Fatal("MigrateFile() error = nil, want a missing-file error")
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != 2 {
		t.Fatalf("MigrateFile() error = %v, want exit code 2", err)
	}
}

// TestV2LoadHintsMigration: after the cutover a v1 roster is not read by
// the v3 loader, and the error tells the user which command converts it.
func TestV2LoadHintsMigration(t *testing.T) {
	data, err := os.ReadFile("testdata/draft-v1.toml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParsePolicy("roster.toml", string(data))
	if err == nil || !strings.Contains(err.Error(), "docflow roster migrate") {
		t.Fatalf("ParsePolicy() error = %v, want the migrate hint", err)
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != 2 {
		t.Fatalf("ParsePolicy() error = %v, want exit code 2", err)
	}
	path := filepath.Join(t.TempDir(), "roster.toml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadPolicy(path)
	if err == nil || !strings.Contains(err.Error(), "docflow roster migrate") {
		t.Fatalf("LoadPolicy() error = %v, want the migrate hint", err)
	}
}

// v2Policy is a v2 policy that exercises the v2→v3 conversion: classes
// with and without pins, a "-" pin, a comment and a preset the output
// must keep as they are.
const v2Policy = `# A v2 policy the user hand-tuned.
version = 2

[access.zai]
agents = ["pi"]
budget = "lean"
check  = true

[access.codex]
agents  = ["codex"]
trusted = true

[model."zai/glm-5.3"]
class = "strong"

[model."zai/glm-5.3-flash"]
class = "cheap"

[model."codex/gpt-6-luna"]
class = "standard"

[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"

[effort]
"zai/glm-5.3"       = "max"
"zai/glm-5.3-flash" = "-"
"codex/gpt-6-luna"  = "high"

[preset.zai-first]
order = ["zai"]
`

// TestMigrateV2: a v2 policy converts in place — the class and the pin of
// a model become one route, everything else is kept as it is, and the
// result loads as version 3.
func TestMigrateV2(t *testing.T) {
	policy := func() *v3Policy {
		migrated, err := migrateToV3("roster.toml", v2Policy)
		if err != nil {
			t.Fatalf("migrateToV3() error = %v", err)
		}
		parsed, err := parseV3Policy("roster.toml", migrated)
		if err != nil {
			t.Fatalf("the migrated roster does not parse: %v\n%s", err, migrated)
		}
		return parsed
	}()
	// The pin moves into the route key; the class follows it. A comment
	// inside the removed tables dies with them (the file is rewritten
	// by the next save anyway); the rest of the file stays as it is.
	routes := map[string]v3Route{
		"zai/glm-5.3":       {Access: "zai", Model: "glm-5.3", Eff: "max", Class: "strong"},
		"zai/glm-5.3-flash": {Access: "zai", Model: "glm-5.3-flash", Eff: "-", Class: "cheap"},
		// A pin without a class takes the automatic class by the name.
		"codex/gpt-6-luna": {Access: "codex", Model: "gpt-6-luna", Eff: "high", Class: "standard"},
	}
	for key, want := range routes {
		access, id, _ := splitModelKey(key)
		got := policy.routesOf(access, id)
		if len(got) != 1 || got[0] != want {
			t.Errorf("route of %s = %v, want %v", key, got, want)
		}
	}
	if _, ok := policy.Preset("zai-first"); !ok {
		t.Fatal("preset zai-first is missing: the conversion lost the preset")
	}
	migrated, err := migrateToV3("roster.toml", v2Policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"# A v2 policy the user hand-tuned.", "[preset.zai-first]", `budget = "lean"`} {
		if !strings.Contains(migrated, keep) {
			t.Errorf("the migrated text lost %q:\n%s", keep, migrated)
		}
	}
	if !strings.Contains(migrated, "version = 3") || strings.Contains(migrated, "version = 2") {
		t.Errorf("the migrated text keeps the wrong version:\n%s", migrated)
	}
}

// TestMigrateV2ClassWithoutPin: a class without a pin takes the class
// ladder as the route key, so the model keeps the effort v2 gave it.
func TestMigrateV2ClassWithoutPin(t *testing.T) {
	text := `version = 2

[access.zai]
agents = ["pi"]

[model."zai/glm-flash"]
class = "cheap"

[model."zai/glm-pro"]
class = "strong"

[role]
cheap      = "cheap"
executor   = "standard"
heavy      = "strong"
review     = "strong"
acceptance = "trusted"
replan     = "trusted"
`
	policy, err := migrateToV3("roster.toml", text)
	if err != nil {
		t.Fatalf("migrateToV3() error = %v", err)
	}
	parsed, err := parseV3Policy("roster.toml", policy)
	if err != nil {
		t.Fatalf("the migrated roster does not parse: %v\n%s", err, policy)
	}
	if route := parsed.routesOf("zai", "glm-flash"); len(route) != 1 || route[0] != (v3Route{Access: "zai", Model: "glm-flash", Eff: "low", Class: "cheap"}) {
		t.Fatalf("route of zai/glm-flash = %v, want low = cheap", route)
	}
	if route := parsed.routesOf("zai", "glm-pro"); len(route) != 1 || route[0] != (v3Route{Access: "zai", Model: "glm-pro", Eff: "high", Class: "strong"}) {
		t.Fatalf("route of zai/glm-pro = %v, want high = strong", route)
	}
}

// TestMigrateV2WithoutModelTables: a v2 policy that named no class at all
// only moves its version line.
func TestMigrateV2WithoutModelTables(t *testing.T) {
	text := "version = 2\n\n[access.zai]\nagents = [\"pi\"]\n\n[role]\ncheap      = \"cheap\"\nexecutor   = \"standard\"\nheavy      = \"strong\"\nreview     = \"strong\"\nacceptance = \"trusted\"\nreplan     = \"trusted\"\n"
	migrated, err := migrateToV3("roster.toml", text)
	if err != nil {
		t.Fatalf("migrateToV3() error = %v", err)
	}
	if !strings.HasPrefix(migrated, "version = 3\n") || !strings.Contains(migrated, "[access.zai]") {
		t.Fatalf("migrateToV3() = %q, want the version bumped and the rest kept", migrated)
	}
}

// TestMigrateV2RejectsBrokenKeys: a model key the v3 format cannot
// address fails the conversion instead of writing an unloadable file.
func TestMigrateV2RejectsBrokenKeys(t *testing.T) {
	text := "version = 2\n\n[access.zai]\nagents = [\"pi\"]\n\n[role]\ncheap      = \"cheap\"\nexecutor   = \"standard\"\nheavy      = \"strong\"\nreview     = \"strong\"\nacceptance = \"trusted\"\nreplan     = \"trusted\"\n\n[effort]\n\"glm-5.3\" = \"high\"\n"
	err := migrateError(t, text)
	if !strings.Contains(err.Error(), "model key") {
		t.Fatalf("migrateToV3() error = %v, want the model key problem", err)
	}
}

// The v1 and v2 migrations end at a version 3 policy; these accessors read
// it the way the tests of that step always did.
func (p *v3Policy) AccessNames() []string {
	names := make([]string, 0, len(p.Accesses))
	for _, access := range p.Accesses {
		names = append(names, access.Name)
	}
	return names
}

func (p *v3Policy) Access(name string) (*v3Access, bool) {
	access := p.access(name)
	return access, access != nil
}

func (p *v3Policy) routesOf(access, model string) []v3Route {
	var routes []v3Route
	for _, route := range p.Routes {
		if route.Access == access && route.Model == model {
			routes = append(routes, route)
		}
	}
	return routes
}

func (p *v3Policy) Preset(name string) (*v3Preset, bool) {
	for i := range p.Presets {
		if p.Presets[i].Name == name {
			return &p.Presets[i], true
		}
	}
	return nil, false
}

func (p *v3Policy) PresetNames() []string {
	names := make([]string, 0, len(p.Presets))
	for _, preset := range p.Presets {
		names = append(names, preset.Name)
	}
	return names
}
