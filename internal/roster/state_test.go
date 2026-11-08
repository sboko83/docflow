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
const statePolicy = "version = 4\n\n" +
	"[access.zai]\nagents = [\"pi\"]\nexpires = \"2099-01-01\"\n\n[access.zai.roles]\nexecutor = \"glm-5.3@high\"\n\n" +
	"[access.codex]\nagents = [\"codex\"]\n\n" +
	"[access.claude]\nagents = [\"claude\"]\n\n" +
	"[access.local]\nagents = [\"pi\"]\nkind = \"local\"\ncheck = true\n"

// stateTestPolicy parses the state policy and gives it the low preset: two
// accesses off, codex first and the zai executor cell cleared.
func stateTestPolicy(t *testing.T) *Policy {
	t.Helper()
	policy, err := ParsePolicy("roster.toml", statePolicy)
	if err != nil {
		t.Fatalf("ParsePolicy() error = %v", err)
	}
	policy.Presets = []Preset{{
		Name:   "low",
		Source: SourceVariants,
		Off:    []string{"claude", "local"},
		Order:  []string{"codex", "zai"},
		Roles:  map[string]map[string]Cell{"zai": {"executor": {}}},
	}}
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
	state.SetOn("codex")
	if len(state.Off) != 0 || len(state.Until) != 0 {
		t.Fatalf("on left off conditions: %v %v", state.Off, state.Until)
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
	state.SetExpires("zai", day)
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
		!reflect.DeepEqual(loaded.Expires, state.Expires) ||
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
	merged, err := Merge(policy, state)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if want := []string{"codex", "zai", "claude", "local"}; !reflect.DeepEqual(merged.AccessNames(), want) {
		t.Fatalf("merged order = %v, want the preset order %v", merged.AccessNames(), want)
	}
	zai, _ := merged.Access("zai")
	if !zai.Off || !zai.Cell("executor").Empty() {
		t.Errorf("zai = %+v, want off with the executor cell cleared", zai)
	}
	claude, _ := merged.Access("claude")
	local, _ := merged.Access("local")
	codex, _ := merged.Access("codex")
	if !claude.Off || !local.Off || codex.Off {
		t.Errorf("off = claude %v, local %v, codex %v", claude.Off, local.Off, codex.Off)
	}
	// The policy itself stays untouched.
	if stable, _ := policy.Access("zai"); stable.Off || stable.Cell("executor").Empty() {
		t.Fatalf("Merge() changed the policy: %+v", stable)
	}
}

func TestStateMergeExpiry(t *testing.T) {
	policy := stateTestPolicy(t)
	state := NewState("roster.state.toml")
	today := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	state.SetExpires("zai", today.AddDate(0, 0, 1))
	merged := mergeAt(policy.clone(), state, today)
	zai, _ := merged.Access("zai")
	if !zai.Expires.Equal(today.AddDate(0, 0, 1)) {
		t.Fatalf("zai expires = %v, want the earlier state date", zai.Expires)
	}
	state.SetExpires("zai", time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	merged = mergeAt(policy.clone(), state, today)
	zai, _ = merged.Access("zai")
	if dayText(zai.Expires) != "2099-01-01" {
		t.Fatalf("zai expires = %v, want the policy date: a condition never extends it", zai.Expires)
	}
}

// TestStateRefusesRemovedConditions: the budget, allowlist and denial
// conditions of the v3 roster send the user to the migration.
func TestStateRefusesRemovedConditions(t *testing.T) {
	for _, key := range []string{"budget", "models", "exclude"} {
		_, err := ParseState("roster.state.toml", "version = 2\n\n["+key+"]\nzai = \"lean\"\n")
		if err == nil || !strings.Contains(err.Error(), "docflow roster migrate") {
			t.Errorf("%s: ParseState() error = %v, want the migration hint", key, err)
		}
	}
}

func TestStateConditions(t *testing.T) {
	policy := stateTestPolicy(t)
	state := NewState("roster.state.toml")
	rows := Conditions(policy, state)
	if len(rows) != 1 || rows[0].Access != "zai" || rows[0].Kind != "expires" || rows[0].Value != "2099-01-01" || rows[0].Source != SourcePolicy {
		t.Fatalf("policy conditions = %+v, want the zai expiry", rows)
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
