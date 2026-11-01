package roster

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// statePolicy is the policy the state tests merge over: one access per
// kind of condition the state can carry.
const statePolicy = "version = 2\n\n" +
	"[access.zai]\nagents = [\"pi\"]\nbudget = \"lean\"\nmodels = [\"glm-5.3*\"]\n\n" +
	"[access.codex]\nagents = [\"codex\"]\ntrusted = true\n\n" +
	"[access.claude]\nagents = [\"claude\"]\ntrusted = true\n\n" +
	"[access.local]\nagents = [\"pi\"]\nkind = \"local\"\ncheck = true\n\n" +
	policyRoles +
	"\n[preset.low]\noff = [\"claude\", \"local\"]\nlean = [\"zai\"]\nminimal = [\"codex\"]\norder = [\"codex\", \"zai\"]\n"

func stateTestPolicy(t *testing.T) *Policy {
	t.Helper()
	policy, err := ParsePolicy("roster.toml", statePolicy)
	if err != nil {
		t.Fatalf("ParsePolicy() error = %v", err)
	}
	return policy
}

func TestStatePath(t *testing.T) {
	if got, want := StatePath("roster.toml"), "roster.state.toml"; got != want {
		t.Errorf("StatePath(roster.toml) = %q, want %q", got, want)
	}
	if got, want := StatePath("my.roster"), "my.roster.state.toml"; got != want {
		t.Errorf("StatePath without suffix = %q, want %q", got, want)
	}
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	if got, want := StatePath(""), filepath.Join(config, "docflow", "roster.state.toml"); got != want {
		t.Errorf("StatePath(\"\") = %q, want %q", got, want)
	}
	if got, want := DefaultStatePath(), filepath.Join(config, "docflow", "roster.state.toml"); got != want {
		t.Errorf("DefaultStatePath() = %q, want %q", got, want)
	}
}

func TestStateOffOnUntil(t *testing.T) {
	state := NewState("roster.state.toml")
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	state.SetOff("codex", day)
	if !reflect.DeepEqual(state.Off, []string{"codex"}) {
		t.Fatalf("off = %v, want [codex]", state.Off)
	}
	if got := state.Until["codex"]; !got.Equal(day) {
		t.Fatalf("until = %v, want %v", got, day)
	}
	// An off without a date drops the previous one instead of keeping a
	// stale bound.
	state.SetOff("codex", time.Time{})
	if _, ok := state.Until["codex"]; ok {
		t.Fatalf("until kept after an open-ended off: %v", state.Until)
	}
	state.SetBudget("codex", BudgetOff)
	state.SetOn("codex")
	if len(state.Off) != 0 || len(state.Until) != 0 {
		t.Fatalf("on left off conditions: %v %v", state.Off, state.Until)
	}
	if _, ok := state.Budget["codex"]; ok {
		t.Fatalf("on left the off budget: %v", state.Budget)
	}
}

func TestStateAllowDenyDedup(t *testing.T) {
	state := NewState("roster.state.toml")
	for _, globs := range [][]string{{"*flash*", "glm-5*"}, {"*flash*"}} {
		if err := state.Allow("zai", globs); err != nil {
			t.Fatalf("Allow(%v) error = %v", globs, err)
		}
	}
	if want := []string{"*flash*", "glm-5*"}; !reflect.DeepEqual(state.Models["zai"], want) {
		t.Fatalf("models = %v, want %v", state.Models["zai"], want)
	}
	if err := state.Deny("zai", []string{"glm-5.3-thinking"}); err != nil {
		t.Fatalf("Deny() error = %v", err)
	}
	if err := state.Deny("zai", []string{"glm-5.3-thinking"}); err != nil {
		t.Fatalf("Deny() repeat error = %v", err)
	}
	if want := []string{"glm-5.3-thinking"}; !reflect.DeepEqual(state.Exclude["zai"], want) {
		t.Fatalf("exclude = %v, want %v", state.Exclude["zai"], want)
	}
	if err := state.Allow("zai", []string{"["}); err == nil {
		t.Fatal("Allow() accepted a broken glob")
	}
	if err := state.SetBudget("zai", "plenty"); err == nil {
		t.Fatal("SetBudget() accepted an unknown level")
	}
}

func TestStateSaveLoadRoundTrip(t *testing.T) {
	// A fixed future day turns into a past one as the calendar advances and
	// LoadState then drops the expired conditions. Keep the fixture relative
	// to now and cut it to a date so it round-trips through the day format.
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 7)
	state := NewState(filepath.Join(t.TempDir(), "roster.state.toml"))
	state.Preset = "low"
	state.SetOff("claude", day)
	state.SetOff("local", time.Time{})
	if err := state.SetBudget("codex", BudgetMinimal); err != nil {
		t.Fatal(err)
	}
	state.SetExpires("zai", day)
	if err := state.Allow("zai", []string{"*flash*"}); err != nil {
		t.Fatal(err)
	}
	if err := state.Deny("zai", []string{"glm-5.3-thinking"}); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(""); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(state.Path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("Save() left a temporary file: %v", err)
	}
	loaded, err := LoadState(state.Path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if !reflect.DeepEqual(loaded.Off, state.Off) ||
		!reflect.DeepEqual(loaded.Until, state.Until) ||
		!reflect.DeepEqual(loaded.Budget, state.Budget) ||
		!reflect.DeepEqual(loaded.Expires, state.Expires) ||
		!reflect.DeepEqual(loaded.Models, state.Models) ||
		!reflect.DeepEqual(loaded.Exclude, state.Exclude) ||
		loaded.Preset != state.Preset {
		t.Fatalf("round trip = %+v, want %+v", loaded, state)
	}
	// Sorted keys keep the file stable: a second save writes the same text.
	first, err := os.ReadFile(state.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Save(""); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(state.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("save is not stable:\n%s\n---\n%s", first, second)
	}
}

func TestStateLoadDropsExpiredOff(t *testing.T) {
	config := t.TempDir()
	path := filepath.Join(config, "roster.state.toml")
	past := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	future := time.Now().UTC().AddDate(0, 7, 0).Format("2006-01-02")
	text := "version = 2\n\noff = [\"claude\", \"local\"]\n\n[until]\nclaude = \"" + past + "\"\nlocal = \"" + future + "\"\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if !reflect.DeepEqual(state.Off, []string{"local"}) {
		t.Fatalf("off = %v, want [local]: a passed until is dropped", state.Off)
	}
	if _, ok := state.Until["claude"]; ok {
		t.Fatalf("until kept for a dropped off: %v", state.Until)
	}
	// The state file itself is only rewritten by a mutation.
	if data, err := os.ReadFile(path); err != nil || string(data) != text {
		t.Fatalf("LoadState() rewrote the file: %q, %v", data, err)
	}
}

func TestStateValidateUnknownAccess(t *testing.T) {
	policy := stateTestPolicy(t)
	state := NewState("roster.state.toml")
	state.Preset = "low"
	state.SetOff("nope", time.Time{})
	if err := state.Validate(policy); err == nil || !strings.Contains(err.Error(), "unknown access 'nope'") || !strings.Contains(err.Error(), "zai") {
		t.Fatalf("Validate() error = %v, want an unknown access naming the known ones", err)
	}
	state.SetOn("nope")
	state.Preset = "gone"
	if err := state.Validate(policy); err == nil || !strings.Contains(err.Error(), "unknown preset 'gone'") {
		t.Fatalf("Validate() error = %v, want an unknown preset", err)
	}
	state.Preset = ""
	state.SetBudget("zai", BudgetMinimal)
	if err := state.Validate(policy); err == nil || !strings.Contains(err.Error(), "minimal requires trusted") {
		t.Fatalf("Validate() error = %v, want minimal to require trusted", err)
	}
	if err := state.SetBudget("zai", BudgetLean); err != nil {
		t.Fatal(err)
	}
	state.SetBudget("codex", BudgetMinimal)
	state.SetExpires("claude", time.Now().UTC().AddDate(0, 1, 0))
	if err := state.Validate(policy); err != nil {
		t.Fatalf("Validate() error = %v, want a valid state", err)
	}
}

func TestStateMerge(t *testing.T) {
	policy := stateTestPolicy(t)
	state := NewState("roster.state.toml")
	state.Preset = "low"
	state.SetOff("zai", time.Now().UTC().AddDate(0, 7, 0))
	if err := state.SetBudget("codex", BudgetMinimal); err != nil {
		t.Fatal(err)
	}
	state.SetExpires("local", time.Now().UTC().AddDate(0, 0, -1))
	if err := state.Allow("zai", []string{"*flash*"}); err != nil {
		t.Fatal(err)
	}
	if err := state.Deny("zai", []string{"glm-5.3-thinking"}); err != nil {
		t.Fatal(err)
	}
	merged, err := Merge(policy, state)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if want := []string{"codex", "zai", "claude", "local"}; !reflect.DeepEqual(merged.AccessNames(), want) {
		t.Fatalf("merged order = %v, want the preset order %v", merged.AccessNames(), want)
	}
	zai, _ := merged.Access("zai")
	if zai.Budget != BudgetOff {
		t.Errorf("zai budget = %q, want off: an off switch beats lean", zai.Budget)
	}
	if want := []string{"*flash*"}; !reflect.DeepEqual(zai.Models, want) {
		t.Errorf("zai models = %v, want the state allowlist %v", zai.Models, want)
	}
	if want := []string{"glm-5.3-thinking"}; !reflect.DeepEqual(zai.Exclude, want) {
		t.Errorf("zai exclude = %v, want %v", zai.Exclude, want)
	}
	codex, _ := merged.Access("codex")
	if codex.Budget != BudgetMinimal {
		t.Errorf("codex budget = %q, want minimal", codex.Budget)
	}
	claude, _ := merged.Access("claude")
	local, _ := merged.Access("local")
	if claude.Budget != BudgetOff || local.Budget != BudgetOff {
		t.Errorf("preset off = claude %q, local %q, want both off", claude.Budget, local.Budget)
	}
	// The policy itself stays untouched: the resolver compares the stable
	// layer with the conditions.
	if stable, _ := policy.Access("zai"); stable.Budget != BudgetLean || len(stable.Exclude) != 0 {
		t.Fatalf("Merge() changed the policy: %+v", stable)
	}
}

func TestStateMergeExpiredAccess(t *testing.T) {
	policy := stateTestPolicy(t)
	state := NewState("roster.state.toml")
	today := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	state.SetExpires("codex", today.AddDate(0, 0, -1))
	merged := mergeAt(policy.clone(), state, today)
	codex, _ := merged.Access("codex")
	if codex.Budget != BudgetOff {
		t.Fatalf("codex budget = %q, want off the day after its expiry", codex.Budget)
	}
	// The expiry day itself is still usable.
	state.SetExpires("codex", today.AddDate(0, 0, 1))
	merged = mergeAt(policy.clone(), state, today)
	codex, _ = merged.Access("codex")
	if codex.Budget == BudgetOff || !codex.Expires.Equal(today.AddDate(0, 0, 1)) {
		t.Fatalf("codex = %+v, want a live access expiring tomorrow", codex)
	}
}

func TestStateConditions(t *testing.T) {
	policy := stateTestPolicy(t)
	state := NewState("roster.state.toml")
	rows := Conditions(policy, state)
	if len(rows) != 2 || rows[0].Access != "zai" || rows[0].Kind != "budget" || rows[0].Value != BudgetLean || rows[0].Source != SourcePolicy {
		t.Fatalf("policy conditions = %+v, want the zai budget and allowlist", rows)
	}
	state.SetOff("claude", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	rows = Conditions(policy, state)
	last := rows[len(rows)-1]
	if last.Access != "claude" || last.Kind != "off" || last.Value != "until 2026-11-01" || last.Source != SourceState {
		t.Fatalf("state condition = %+v, want the dated claude off", last)
	}
	if got := ConditionTable(nil); got != "conditions: none\n" {
		t.Fatalf("ConditionTable(nil) = %q", got)
	}
	report := NewConditionsReport(policy, state)
	if report.State != "roster.state.toml" || len(report.Conditions) != len(rows) {
		t.Fatalf("report = %+v", report)
	}
}
