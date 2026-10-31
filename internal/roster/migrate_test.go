package roster

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// migratedPolicy migrates a roster file and returns the parsed v2 policy,
// so the tests read the result the way the resolver will.
func migratedPolicy(t *testing.T, path string) *Policy {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text, err := Migrate(string(data))
	if err != nil {
		t.Fatalf("Migrate(%s) error = %v", path, err)
	}
	policy, err := ParsePolicy(path, text)
	if err != nil {
		t.Fatalf("the migrated %s does not parse: %v\n%s", path, err, text)
	}
	return policy
}

func migrateError(t *testing.T, text string) error {
	t.Helper()
	_, err := Migrate(text)
	if err == nil {
		t.Fatalf("Migrate() error = nil, want an error")
	}
	if coded, ok := err.(interface{ ExitCode() int }); !ok || coded.ExitCode() != 2 {
		t.Fatalf("Migrate() error = %v, want exit code 2", err)
	}
	return err
}

func accessOf(t *testing.T, policy *Policy, name string) *Access {
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
	// A tier belongs to the route, not the model: the class is the
	// strongest tier the model served.
	classes := map[string]string{
		"zai/glm-5.3-flash":       "standard",
		"zai/glm-5.3":             "strong",
		"codex/gpt-6-luna":        "standard",
		"codex/gpt-6.1-sol":       "strong",
		"claude/claude-haiku-5-5": "cheap",
		"claude/claude-opus-5-5":  "strong",
	}
	for key, want := range classes {
		if got := policy.Models[key]; got != want {
			t.Errorf("class of %s = %q, want %q", key, got, want)
		}
	}
	// The effort comes from the strongest route of the model; "-" stays
	// "no effort" instead of becoming a class-ladder guess.
	efforts := map[string]string{
		"zai/glm-5.3-flash":        "max",
		"zai/glm-5.3":              "max",
		"codex/gpt-6-luna":         "max",
		"claude/claude-haiku-5-5":  "-",
		"claude/claude-sonnet-5-5": "high",
	}
	for key, want := range efforts {
		if got := policy.Efforts[key]; got != want {
			t.Errorf("effort of %s = %q, want %q", key, got, want)
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
	if got := policy.Models["opencode-go/kimi-k3.1-pro"]; got != "strong" {
		t.Fatalf("class of opencode-go/kimi-k3.1-pro = %q, want strong", got)
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
	text, err := Migrate(string(data))
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
	first, err := Migrate(string(data))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Migrate(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("two migrations of one roster differ:\n%s\n---\n%s", first, second)
	}
	// The output is not a v1 roster: migrating it again must fail instead
	// of rewriting a v2 file as if it were the old format.
	if _, err := Migrate(first); err == nil || !strings.Contains(err.Error(), "already version 2") {
		t.Fatalf("Migrate(migrated) error = %v, want already version 2", err)
	}
}

func TestMigrateRejectsNonV1(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "already version 2",
			text: "version = 2\n\n[access.zai]\nagents = [\"pi\"]\n\n[role]\ncheap = \"cheap\"\nexecutor = \"standard\"\nheavy = \"strong\"\nreview = \"strong\"\nacceptance = \"trusted\"\nreplan = \"trusted\"\n",
			want: "already version 2",
		},
		{
			name: "unknown version",
			text: "version = 3\nspend = [\"zai\"]\n",
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
				t.Fatalf("Migrate() error = %v, want it to contain %q", err, tc.want)
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
		t.Fatalf("Migrate() error = %v, want the access name", err)
	}
	if !strings.Contains(err.Error(), "route x") {
		t.Fatalf("Migrate() error = %v, want the route location", err)
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
		t.Fatalf("Migrate() error = %v, want the glob problem", err)
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
	policy := func() *Policy {
		migrated, err := Migrate(text)
		if err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		parsed, err := ParsePolicy("roster.toml", migrated)
		if err != nil {
			t.Fatalf("the migrated roster does not parse: %v\n%s", err, migrated)
		}
		return parsed
	}()
	access := accessOf(t, policy, "opencode.go")
	if want := []string{"kimi-k3.1-pro"}; !reflect.DeepEqual(access.Models, want) {
		t.Fatalf("models of opencode.go = %v, want %v", access.Models, want)
	}
	if got := policy.Models["opencode.go/kimi-k3.1-pro"]; got != "standard" {
		t.Fatalf("class of opencode.go/kimi-k3.1-pro = %q, want standard", got)
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
	migrated, err := Migrate(text)
	if err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	policy, err := ParsePolicy("roster.toml", migrated)
	if err != nil {
		t.Fatalf("the migrated roster does not parse: %v\n%s", err, migrated)
	}
	if got := policy.Efforts["zai/glm-5.3"]; got != "high" {
		t.Fatalf("effort of zai/glm-5.3 = %q, want the first route's high", got)
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
	migrated, err := Migrate(text)
	if err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	policy, err := ParsePolicy("roster.toml", migrated)
	if err != nil {
		t.Fatalf("the migrated roster does not parse: %v\n%s", err, migrated)
	}
	if got, want := policy.AccessNames(), []string{"zai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("accesses = %v, want %v: a route-less provider cannot name a channel", got, want)
	}
}

func TestMigrateFile(t *testing.T) {
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
	// --write keeps the v1 text and replaces the roster.
	result, err = MigrateFile(path, true)
	if err != nil {
		t.Fatalf("MigrateFile(write) error = %v", err)
	}
	if result.Backup != path+".bak" {
		t.Fatalf("backup = %q, want %q", result.Backup, path+".bak")
	}
	backup, err := os.ReadFile(result.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != minimal {
		t.Fatalf("backup holds\n%s\nwant the v1 text\n%s", backup, minimal)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != result.Text {
		t.Fatalf("roster.toml does not hold the migrated text")
	}
	if _, err := ParsePolicy(path, string(data)); err != nil {
		t.Fatalf("the written roster does not load: %v", err)
	}
	// A second --write is a refusal, not a rewrite of the v2 file: the
	// roster and its backup stay as they are.
	_, err = MigrateFile(path, true)
	if err == nil || !strings.Contains(err.Error(), "already version 2") {
		t.Fatalf("second MigrateFile() error = %v, want already version 2", err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(data) {
		t.Fatalf("a refused migration rewrote roster.toml")
	}
	if again, _ := os.ReadFile(result.Backup); string(again) != minimal {
		t.Fatalf("a refused migration rewrote the backup")
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
// the v2 loader, and the error tells the user which command converts it.
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
	if v2, err := IsV2(path); err != nil || v2 {
		t.Fatalf("IsV2() = (%t, %v), want a v1 roster to be read as not v2", v2, err)
	}
}
