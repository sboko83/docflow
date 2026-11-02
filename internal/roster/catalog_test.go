package roster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
)

// catalogHome isolates the cache root and returns the XDG value set.
func catalogCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	return dir
}

func TestCatalogDefaultCachePath(t *testing.T) {
	dir := catalogCacheDir(t)
	if got, want := DefaultCachePath(), filepath.Join(dir, "docflow", "roster", "catalog.json"); got != want {
		t.Fatalf("DefaultCachePath() = %q, want %q", got, want)
	}
}

func TestCatalogRelativeCacheHomeIsAbsolute(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", filepath.Join("relative", "cache"))
	got := DefaultCachePath()
	if !filepath.IsAbs(got) {
		t.Fatalf("DefaultCachePath() = %q, want an absolute path", got)
	}
	want := filepath.ToSlash(filepath.Join("relative", "cache", "docflow", "roster", "catalog.json"))
	if !strings.HasSuffix(filepath.ToSlash(got), want) {
		t.Fatalf("DefaultCachePath() = %q, want it under the relative XDG root", got)
	}
}

func TestCatalogCachePathWithoutXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads it on Windows
	if got, want := DefaultCachePath(), filepath.Join(home, ".cache", "docflow", "roster", "catalog.json"); got != want {
		t.Fatalf("DefaultCachePath() = %q, want %q", got, want)
	}
}

func TestCatalogMissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(catalogCacheDir(t), "absent", "catalog.json")
	catalog, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v, want nil", err)
	}
	if catalog.Version != CatalogVersion {
		t.Fatalf("Version = %d, want %d", catalog.Version, CatalogVersion)
	}
	if catalog.Models == nil || catalog.Accesses == nil || catalog.Sources == nil {
		t.Fatalf("empty catalog has nil slices: %+v", catalog)
	}
	if len(catalog.Models) != 0 || len(catalog.Accesses) != 0 || len(catalog.Sources) != 0 {
		t.Fatalf("empty catalog is not empty: %+v", catalog)
	}
}

func TestCatalogSaveCreatesDirectories(t *testing.T) {
	path := DefaultCachePath()
	catalog := emptyCatalog()
	catalog.Models = append(catalog.Models, ModelInfo{Access: "zai", ID: "glm-5.3"})
	if err := catalog.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file: %v", err)
	}
}

func TestCatalogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	model := ModelInfo{
		Access:    "opencode-go",
		ID:        "deepseek-v4.1-flash",
		Provider:  "opencode-go",
		Context:   200000,
		MaxOut:    32000,
		Reasoning: true,
		Efforts:   []string{"low", "high"},
		Cost:      &Cost{Input: 0.5, Output: 2, CacheRead: 0.1},
		Input:     []string{"text"},
		Status:    "known",
		Source:    "pi-store",
	}
	catalog := emptyCatalog()
	catalog.ScannedAt = "2026-10-31T12:00:00Z"
	catalog.Sources = []string{"pi-store", "opencode"}
	catalog.Accesses = append(catalog.Accesses, AccessInfo{
		Access: "opencode-go", Agents: []string{"pi", "opencode"}, Kind: "subscription", Auth: "ready", Status: "known", Source: "pi-store",
	})
	catalog.Models = append(catalog.Models, model)
	if err := catalog.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(text, &fields); err != nil {
		t.Fatalf("cache is not JSON: %v", err)
	}
	for _, key := range []string{"version", "scanned_at", "sources", "accesses", "models"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("cache JSON has no %q: %s", key, text)
		}
	}
	loaded, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	if loaded.ScannedAt != catalog.ScannedAt {
		t.Fatalf("ScannedAt = %q, want %q", loaded.ScannedAt, catalog.ScannedAt)
	}
	if !reflect.DeepEqual(loaded.Sources, catalog.Sources) {
		t.Fatalf("Sources = %v, want %v", loaded.Sources, catalog.Sources)
	}
	if len(loaded.Models) != 1 || !reflect.DeepEqual(loaded.Models[0], model) {
		t.Fatalf("Models = %+v, want %+v", loaded.Models, model)
	}
	if len(loaded.Accesses) != 1 || loaded.Accesses[0].Access != "opencode-go" || len(loaded.Accesses[0].Agents) != 2 {
		t.Fatalf("Accesses = %+v", loaded.Accesses)
	}
}

func TestCatalogSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	first := emptyCatalog()
	first.Models = append(first.Models, ModelInfo{Access: "zai", ID: "old"})
	if err := first.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	second := emptyCatalog()
	second.Models = append(second.Models, ModelInfo{Access: "zai", ID: "new"})
	if err := second.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "catalog.json" {
			t.Fatalf("cache dir has leftover %q after a rename", entry.Name())
		}
	}
	loaded, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	if len(loaded.Models) != 1 || loaded.Models[0].ID != "new" {
		t.Fatalf("Models = %+v, want the rewritten catalog", loaded.Models)
	}
}

func TestCatalogSortOrder(t *testing.T) {
	catalog := emptyCatalog()
	for _, model := range []ModelInfo{
		{Access: "zai", ID: "glm-5.3"},
		{Access: "codex", ID: "gpt-6.1-sol"},
		{Access: "zai", ID: "glm-5.3-flash"},
		{Access: "codex", ID: "gpt-6-luna"},
	} {
		catalog.Models = append(catalog.Models, model)
	}
	for _, access := range []AccessInfo{{Access: "zai"}, {Access: "codex"}} {
		catalog.Accesses = append(catalog.Accesses, access)
	}
	catalog.Sort()
	want := []string{"codex/gpt-6-luna", "codex/gpt-6.1-sol", "zai/glm-5.3", "zai/glm-5.3-flash"}
	var got []string
	for _, model := range catalog.Models {
		got = append(got, model.Key())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("model order = %v, want %v", got, want)
	}
	if catalog.Accesses[0].Access != "codex" || catalog.Accesses[1].Access != "zai" {
		t.Fatalf("access order = %+v, want codex before zai", catalog.Accesses)
	}
}

func TestCatalogMergeLaterSourceWins(t *testing.T) {
	base := emptyCatalog()
	base.Sources = []string{"pi-store"}
	base.Models = append(base.Models, ModelInfo{
		Access: "zai", ID: "glm-5.3", Context: 100000, MaxOut: 8000, Cost: &Cost{Input: 1, Output: 3}, Source: "pi-store",
	})
	later := emptyCatalog()
	later.Sources = []string{"opencode"}
	later.Models = append(later.Models, ModelInfo{
		Access: "zai", ID: "glm-5.3", Context: 200000, MaxOut: 32000, Reasoning: true,
		Efforts: []string{"low", "high"}, Cost: &Cost{Input: 2, Output: 6}, Source: "opencode",
	})
	base.Merge(later)
	if len(base.Models) != 1 {
		t.Fatalf("merge produced %d models, want 1", len(base.Models))
	}
	model := base.Models[0]
	if model.Context != 200000 || model.MaxOut != 32000 {
		t.Fatalf("context/max-out = %d/%d, want the later source", model.Context, model.MaxOut)
	}
	if !model.Reasoning || !reflect.DeepEqual(model.Efforts, []string{"low", "high"}) {
		t.Fatalf("reasoning/efforts = %v/%v, want the later source", model.Reasoning, model.Efforts)
	}
	if model.Cost == nil || model.Cost.Input != 2 || model.Cost.Output != 6 {
		t.Fatalf("Cost = %+v, want the later source", model.Cost)
	}
	if model.Source != "opencode" {
		t.Fatalf("Source = %q, want the last contributing source", model.Source)
	}
	if !reflect.DeepEqual(base.Sources, []string{"pi-store", "opencode"}) {
		t.Fatalf("Sources = %v, want both sources in first-seen order", base.Sources)
	}
}

func TestCatalogMergePartialDataKeepsKnownFields(t *testing.T) {
	base := emptyCatalog()
	base.Models = append(base.Models, ModelInfo{
		Access: "zai", ID: "glm-5.3", Provider: "zai", Context: 200000, MaxOut: 32000,
		Reasoning: true, Efforts: []string{"low"}, Cost: &Cost{Input: 1, Output: 3},
		Input: []string{"text", "image"}, Status: "known", Source: "pi-store",
	})
	partial := emptyCatalog()
	partial.Models = append(partial.Models, ModelInfo{Access: "zai", ID: "glm-5.3", Source: "opencode"})
	base.Merge(partial)
	model := base.Models[0]
	if model.Context != 200000 || model.MaxOut != 32000 || model.Provider != "zai" {
		t.Fatalf("empty fields erased known ones: %+v", model)
	}
	if !model.Reasoning || !reflect.DeepEqual(model.Efforts, []string{"low"}) {
		t.Fatalf("empty fields erased reasoning/efforts: %+v", model)
	}
	if model.Cost == nil || model.Cost.Input != 1 || model.Cost.Output != 3 {
		t.Fatalf("empty cost erased a known cost: %+v", model.Cost)
	}
	if !reflect.DeepEqual(model.Input, []string{"text", "image"}) || model.Status != "known" {
		t.Fatalf("empty fields erased input/status: %+v", model)
	}
	if model.Source != "pi-store" {
		t.Fatalf("Source = %q, want the source that provided the fields", model.Source)
	}
}

func TestCatalogMergeKeysByAccessAndID(t *testing.T) {
	catalog := emptyCatalog()
	channel := func(access, name string) *Catalog {
		other := emptyCatalog()
		other.Accesses = append(other.Accesses, AccessInfo{Access: access, Agents: []string{name}, Source: name})
		other.Models = append(other.Models, ModelInfo{Access: access, ID: "go", Source: name})
		return other
	}
	catalog.Merge(channel("opencode-go", "pi"))
	catalog.Merge(channel("opencode-go", "opencode"))
	catalog.Merge(channel("opencode", "opencode"))
	if len(catalog.Accesses) != 2 {
		t.Fatalf("accesses = %+v, want opencode-go and opencode", catalog.Accesses)
	}
	if got := catalog.Accesses[0].Agents; !reflect.DeepEqual(got, []string{"pi", "opencode"}) {
		t.Fatalf("opencode-go agents = %v, want both channels of one access", got)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("models = %+v, want one per access", catalog.Models)
	}
}

func TestCatalogMergeAccessFlags(t *testing.T) {
	base := emptyCatalog()
	base.Accesses = append(base.Accesses, AccessInfo{Access: "codex", Kind: "subscription", Auth: "unknown", Source: "builtin"})
	probe := emptyCatalog()
	probe.Accesses = append(probe.Accesses, AccessInfo{Access: "codex", Auth: "ready", Source: "auth"})
	base.Merge(probe)
	access := base.Accesses[0]
	if access.Kind != "subscription" {
		t.Fatalf("Kind = %q, want the earlier value kept", access.Kind)
	}
	if access.Auth != "ready" || access.Source != "auth" {
		t.Fatalf("Auth/Source = %q/%q, want the probe result", access.Auth, access.Source)
	}
}

func TestCatalogUnknownVersionIsDiscarded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	content := `{"version": 99, "sources": ["future"], "accesses": [], "models": [{"access": "zai", "id": "glm"}]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	catalog, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v, want nil", err)
	}
	if catalog.Version != CatalogVersion || len(catalog.Models) != 0 || len(catalog.Sources) != 0 {
		t.Fatalf("catalog = %+v, want an empty catalog of the current version", catalog)
	}
}

func TestCatalogBrokenJSONIsFormatError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	_, err := LoadCatalog(path)
	if err == nil {
		t.Fatal("LoadCatalog() error = nil, want a format error")
	}
	coded, ok := err.(interface{ ExitCode() int })
	if !ok || coded.ExitCode() != clierrors.EXIT_FORMAT {
		t.Fatalf("error %v has no exit code 2", err)
	}
}
