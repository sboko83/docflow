package roster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bsv-docflow/internal/testshim"
)

// scanEnv isolates the Pi home and PATH: no real pi or opencode is
// reachable and nothing is read from the developer's own catalogs.
func scanEnv(t *testing.T) (home, bin string) {
	t.Helper()
	home = t.TempDir()
	bin = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("PATH", bin)
	return home, bin
}

// fixture reads a catalog fixture from testdata.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return text
}

// fixturePath returns the absolute slash path of a fixture, for a fake CLI
// that prints it.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("resolve fixture %s: %v", name, err)
	}
	return filepath.ToSlash(path)
}

// installPiFile writes a Pi catalog into the isolated home.
func installPiFile(t *testing.T, home, name, fixtureName string) {
	t.Helper()
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create pi config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), fixture(t, fixtureName), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// installCommand puts a fake CLI on the isolated PATH.
func installCommand(t *testing.T, bin, name, script string) {
	t.Helper()
	testshim.Script(t, bin, name, script)
}

// findAccess returns the access entry with the name or fails the test.
func findAccess(t *testing.T, catalog *Catalog, name string) AccessInfo {
	t.Helper()
	for _, access := range catalog.Accesses {
		if access.Access == name {
			return access
		}
	}
	t.Fatalf("catalog has no access %q: %+v", name, catalog.Accesses)
	return AccessInfo{}
}

// findModel returns the entry with the access/id key or fails the test.
func findModel(t *testing.T, catalog *Catalog, access, id string) ModelInfo {
	t.Helper()
	for _, model := range catalog.Models {
		if model.Access == access && model.ID == id {
			return model
		}
	}
	t.Fatalf("catalog has no model %s/%s: %+v", access, id, catalog.Models)
	return ModelInfo{}
}

// catalogJSON marshals a catalog to assert what must not leak into it.
func catalogJSON(t *testing.T, catalog *Catalog) string {
	t.Helper()
	text, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	return string(text)
}

func TestScanPiStoreAndConfig(t *testing.T) {
	home, bin := scanEnv(t)
	installPiFile(t, home, "models-store.json", "pi-models-store.json")
	installPiFile(t, home, "models.json", "pi-models.json")
	// The store exists, so the rounded table fallback must not run.
	marker := filepath.ToSlash(filepath.Join(home, "pi-called"))
	installCommand(t, bin, "pi", "printf called > '"+marker+"'\n")

	catalog, problems := ScanPi()
	if len(problems) != 0 {
		t.Fatalf("ScanPi() problems = %v, want none", problems)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("ScanPi() ran pi --list-models though the store exists")
	}
	if !reflect.DeepEqual(catalog.Sources, []string{SourcePiStore, SourcePiModels}) {
		t.Fatalf("Sources = %v, want the store then the config", catalog.Sources)
	}

	access := findAccess(t, catalog, "opencode-go")
	if !reflect.DeepEqual(access.Agents, []string{"pi"}) || access.Source != SourcePiStore {
		t.Fatalf("opencode-go access = %+v, want the pi channel", access)
	}

	kimi := findModel(t, catalog, "opencode-go", "kimi-k3.1-pro")
	want := ModelInfo{
		Access:    "opencode-go",
		ID:        "kimi-k3.1-pro",
		Provider:  "opencode-go",
		Context:   1048576,
		MaxOut:    131072,
		Reasoning: true,
		Efforts:   []string{"low", "medium", "high", "xhigh", "max"},
		Cost:      &Cost{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2},
		Input:     []string{"text", "image"},
		Source:    SourcePiStore,
		Provenance: map[string]string{
			"provider":  SourcePiStore,
			"context":   SourcePiStore,
			"max_out":   SourcePiStore,
			"reasoning": SourcePiStore,
			"efforts":   SourcePiStore,
			"cost":      SourcePiStore,
			"input":     SourcePiStore,
		},
	}
	if !reflect.DeepEqual(kimi, want) {
		t.Fatalf("kimi-k3.1-pro = %+v, want %+v", kimi, want)
	}
	if kimi.Provenance["context"] != SourcePiStore || kimi.Provenance["cost"] != SourcePiStore {
		t.Fatalf("kimi provenance = %v, want the store on every field", kimi.Provenance)
	}

	// The config is read after the store: its exact values win, while the
	// fields it leaves out keep what the store supplied.
	glm := findModel(t, catalog, "zai", "glm-5.3")
	if glm.Context != 262144 || glm.MaxOut != 131072 {
		t.Fatalf("zai/glm-5.3 context/max-out = %d/%d, want the config values", glm.Context, glm.MaxOut)
	}
	if glm.Cost == nil || glm.Cost.Input != 0.6 || glm.Cost.Output != 2.2 {
		t.Fatalf("zai/glm-5.3 cost = %+v, want the store price", glm.Cost)
	}
	if !glm.Reasoning || glm.Source != SourcePiModels {
		t.Fatalf("zai/glm-5.3 = %+v, want reasoning from the store and the config as source", glm)
	}
	if glm.Provenance["context"] != SourcePiModels || glm.Provenance["cost"] != SourcePiStore || glm.Provenance["reasoning"] != SourcePiStore {
		t.Fatalf("zai/glm-5.3 provenance = %v, want per-field sources", glm.Provenance)
	}

	local := findModel(t, catalog, "strata", "swift-1.5-iq3_s")
	if local.Context != 262144 || local.MaxOut != 16384 {
		t.Fatalf("strata model = %+v, want its context and max-out", local)
	}
	if local.Cost == nil || local.Cost.Input != 0 || local.Cost.Output != 0 {
		t.Fatalf("strata model cost = %+v, want the explicit free price", local.Cost)
	}
	if len(catalog.Accesses) != 3 {
		t.Fatalf("accesses = %+v, want opencode-go, strata and zai", catalog.Accesses)
	}

	// models.json carries an apiKey command: it must never reach the cache.
	if text := catalogJSON(t, catalog); strings.Contains(text, "fixture-key-command") {
		t.Fatalf("catalog leaked the models.json apiKey: %s", text)
	}
}

func TestScanPiListFallback(t *testing.T) {
	home, bin := scanEnv(t)
	args := filepath.ToSlash(filepath.Join(home, "pi-args"))
	installCommand(t, bin, "pi", "printf '%s\\n' \"$*\" > '"+args+"'\n"+
		"printf '%s\\n' \\\n"+
		"  'provider     model                       context  max-out  thinking  images' \\\n"+
		"  'opencode-go  kimi-k3.1-pro               1.0M     131.1K   yes       yes' \\\n"+
		"  'zai          glm-5.3-flash               1M       131.1K   yes       no' \\\n"+
		"  'strata       swift-1.5-iq3_s             256K     16384    yes       no' \\\n"+
		"  'dead         dead-model                  -        -        no        no'\n")

	catalog, problems := ScanPi()
	if len(problems) != 0 {
		t.Fatalf("ScanPi() problems = %v, want none", problems)
	}
	recorded, err := os.ReadFile(filepath.FromSlash(args))
	if err != nil {
		t.Fatalf("read recorded arguments: %v", err)
	}
	if got := strings.TrimSpace(string(recorded)); got != "--list-models" {
		t.Fatalf("pi arguments = %q, want --list-models", got)
	}
	if !reflect.DeepEqual(catalog.Sources, []string{SourcePiList}) {
		t.Fatalf("Sources = %v, want the list fallback", catalog.Sources)
	}

	kimi := findModel(t, catalog, "opencode-go", "kimi-k3.1-pro")
	if kimi.Context != 1048576 || kimi.MaxOut != 134246 {
		t.Fatalf("kimi context/max-out = %d/%d, want the table sizes", kimi.Context, kimi.MaxOut)
	}
	if !kimi.Reasoning || !reflect.DeepEqual(kimi.Input, []string{"text", "image"}) {
		t.Fatalf("kimi = %+v, want thinking and image input", kimi)
	}
	if kimi.Cost != nil || len(kimi.Efforts) != 0 || kimi.Source != SourcePiList {
		t.Fatalf("kimi = %+v, want no price and no efforts from the table", kimi)
	}

	glm := findModel(t, catalog, "zai", "glm-5.3-flash")
	if glm.Context != 1048576 || !glm.Reasoning || !reflect.DeepEqual(glm.Input, []string{"text"}) {
		t.Fatalf("glm-5.3-flash = %+v, want the text-only row", glm)
	}
	dead := findModel(t, catalog, "dead", "dead-model")
	if dead.Context != 0 || dead.MaxOut != 0 || dead.Reasoning {
		t.Fatalf("dead-model = %+v, want unknown sizes and no thinking", dead)
	}
	access := findAccess(t, catalog, "opencode-go")
	if !reflect.DeepEqual(access.Agents, []string{"pi"}) {
		t.Fatalf("opencode-go access = %+v, want the pi channel", access)
	}
}

func TestScanPiMissing(t *testing.T) {
	scanEnv(t) // no store, no config, no pi on PATH

	catalog, problems := ScanPi()
	if catalog == nil || len(catalog.Models) != 0 || len(catalog.Accesses) != 0 {
		t.Fatalf("catalog = %+v, want an empty partial catalog", catalog)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "pi") {
		t.Fatalf("problems = %v, want one pi warning", problems)
	}
}

func TestScanPiBrokenStoreKeepsConfig(t *testing.T) {
	home, _ := scanEnv(t)
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create pi config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models-store.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write broken store: %v", err)
	}
	installPiFile(t, home, "models.json", "pi-models.json")

	catalog, problems := ScanPi()
	if len(problems) != 1 || !strings.Contains(problems[0], "models-store.json") {
		t.Fatalf("problems = %v, want one warning naming the store", problems)
	}
	local := findModel(t, catalog, "strata", "swift-1.5-iq3_s")
	if local.Context != 262144 || local.Source != SourcePiModels {
		t.Fatalf("strata model = %+v, want the config entry", local)
	}
}

func TestScanOpenCode(t *testing.T) {
	home, bin := scanEnv(t)
	args := filepath.ToSlash(filepath.Join(home, "opencode-args"))
	installCommand(t, bin, "opencode", "printf '%s\\n' \"$*\" > '"+args+"'\n"+
		"while IFS= read -r line; do printf '%s\\n' \"$line\"; done < '"+fixturePath(t, "opencode-models.json")+"'\n")

	catalog, problems := ScanOpenCode()
	if len(problems) != 0 {
		t.Fatalf("ScanOpenCode() problems = %v, want none", problems)
	}
	recorded, err := os.ReadFile(filepath.FromSlash(args))
	if err != nil {
		t.Fatalf("read recorded arguments: %v", err)
	}
	if got := strings.TrimSpace(string(recorded)); got != "api get /api/model" {
		t.Fatalf("opencode arguments = %q, want the model endpoint", got)
	}
	if !reflect.DeepEqual(catalog.Sources, []string{SourceOpenCode}) {
		t.Fatalf("Sources = %v, want the opencode source", catalog.Sources)
	}

	kimi := findModel(t, catalog, "opencode-go", "kimi-k3.1-pro")
	if kimi.Context != 1048576 || kimi.MaxOut != 131072 || !kimi.Reasoning {
		t.Fatalf("kimi = %+v, want its limit and reasoning", kimi)
	}
	if !reflect.DeepEqual(kimi.Efforts, []string{"low", "high", "max"}) {
		t.Fatalf("kimi efforts = %v, want the variant IDs in ladder order", kimi.Efforts)
	}
	if !reflect.DeepEqual(kimi.Input, []string{"text", "image", "video"}) {
		t.Fatalf("kimi input = %v, want the capabilities", kimi.Input)
	}
	if kimi.Cost == nil || kimi.Cost.Input != 2 || kimi.Cost.Output != 10 || kimi.Cost.CacheRead != 0.2 || kimi.Cost.CacheWrite != 2 {
		t.Fatalf("kimi cost = %+v, want the first price entry", kimi.Cost)
	}
	if kimi.Status != "active" || kimi.Source != SourceOpenCode {
		t.Fatalf("kimi status/source = %q/%q, want the server answer", kimi.Status, kimi.Source)
	}

	// No variants and no reasoning field mapping: the model does not think.
	flash := findModel(t, catalog, "opencode-go", "glm-5.3-flash")
	if flash.Reasoning || len(flash.Efforts) != 0 {
		t.Fatalf("glm-5.3-flash = %+v, want no reasoning", flash)
	}

	access := findAccess(t, catalog, "opencode-go")
	if !reflect.DeepEqual(access.Agents, []string{"opencode"}) || access.Source != SourceOpenCode {
		t.Fatalf("opencode-go access = %+v, want the opencode channel", access)
	}
	if text := catalogJSON(t, catalog); strings.Contains(text, "wrk_fixture_org_secret") {
		t.Fatalf("catalog leaked the response headers: %s", text)
	}
}

func TestScanOpenCodeHTML(t *testing.T) {
	_, bin := scanEnv(t)
	installCommand(t, bin, "opencode", "printf '<html><body>proxy</body></html>\\n'\n")

	catalog, problems := ScanOpenCode()
	if catalog == nil || len(catalog.Models) != 0 {
		t.Fatalf("catalog = %+v, want an empty partial catalog", catalog)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "HTML") {
		t.Fatalf("problems = %v, want one HTML warning", problems)
	}
}

func TestScanOpenCodeMissing(t *testing.T) {
	scanEnv(t)

	catalog, problems := ScanOpenCode()
	if catalog == nil || len(catalog.Models) != 0 {
		t.Fatalf("catalog = %+v, want an empty partial catalog", catalog)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "opencode") {
		t.Fatalf("problems = %v, want one opencode warning", problems)
	}
}

func TestScanMergeAccessChannels(t *testing.T) {
	home, bin := scanEnv(t)
	installPiFile(t, home, "models-store.json", "pi-models-store.json")
	installPiFile(t, home, "models.json", "pi-models.json")
	installCommand(t, bin, "opencode", "while IFS= read -r line; do printf '%s\\n' \"$line\"; done < '"+fixturePath(t, "opencode-models.json")+"'\n")

	merged := emptyCatalog()
	pi, problems := ScanPi()
	if len(problems) != 0 {
		t.Fatalf("ScanPi() problems = %v, want none", problems)
	}
	merged.Merge(pi)
	open, problems := ScanOpenCode()
	if len(problems) != 0 {
		t.Fatalf("ScanOpenCode() problems = %v, want none", problems)
	}
	merged.Merge(open)
	merged.Sort()

	if !reflect.DeepEqual(merged.Sources, []string{SourcePiStore, SourcePiModels, SourceOpenCode}) {
		t.Fatalf("Sources = %v, want all three sources", merged.Sources)
	}
	// One subscription, two channels: pi and opencode reach opencode-go,
	// so the access keeps both and its models are keyed by access/id.
	access := findAccess(t, merged, "opencode-go")
	if !reflect.DeepEqual(access.Agents, []string{"pi", "opencode"}) {
		t.Fatalf("opencode-go agents = %v, want both channels", access.Agents)
	}
	if got := findAccess(t, merged, "opencode"); !reflect.DeepEqual(got.Agents, []string{"opencode"}) {
		t.Fatalf("opencode access = %+v, want the opencode channel", got)
	}
	if len(merged.Models) != 5 {
		t.Fatalf("models = %d, want 5 merged entries: %+v", len(merged.Models), merged.Models)
	}
	kimi := findModel(t, merged, "opencode-go", "kimi-k3.1-pro")
	if kimi.Source != SourceOpenCode {
		t.Fatalf("kimi source = %q, want the last contributor", kimi.Source)
	}
	if kimi.Provenance["context"] != SourceOpenCode || kimi.Provenance["cost"] != SourceOpenCode {
		t.Fatalf("kimi provenance = %v, want opencode on the overridden fields", kimi.Provenance)
	}
	// The store supplied modalities and efforts first; the later answer
	// replaces the lists it has.
	if !reflect.DeepEqual(kimi.Input, []string{"text", "image", "video"}) || !reflect.DeepEqual(kimi.Efforts, []string{"low", "high", "max"}) {
		t.Fatalf("kimi input/efforts = %v/%v, want the opencode answer", kimi.Input, kimi.Efforts)
	}
	// A model only the config knows keeps its source and price.
	local := findModel(t, merged, "strata", "swift-1.5-iq3_s")
	if local.Source != SourcePiModels || local.Cost == nil || local.Context != 262144 {
		t.Fatalf("strata model = %+v, want the config entry", local)
	}
}
