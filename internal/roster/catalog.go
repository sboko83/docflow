package roster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"bsv-docflow/internal/clierrors"
)

// CatalogVersion is the on-disk layout of the catalog cache. The cache is
// disposable: a file with another version is treated as absent and the
// next scan rewrites it.
const CatalogVersion = 1

// Catalog is what the scanners found: the accesses (quota units), the
// models of every access, and the sources that contributed in merge
// order. It is a cache, not state: rebuilding it must give the same
// answer without touching the roster.
type Catalog struct {
	Version   int          `json:"version"`
	ScannedAt string       `json:"scanned_at,omitempty"`
	Sources   []string     `json:"sources"`
	Accesses  []AccessInfo `json:"accesses"`
	Models    []ModelInfo  `json:"models"`
}

// AccessInfo is one access of the catalog: the name that the policy and
// the state refer to, the agent channels that reach it, its kind, what
// the auth probes said, and the source that supplied the entry.
type AccessInfo struct {
	Access string   `json:"access"`
	Agents []string `json:"agents,omitempty"`
	Kind   string   `json:"kind,omitempty"`
	Auth   string   `json:"auth,omitempty"`
	Status string   `json:"status,omitempty"`
	Source string   `json:"source,omitempty"`
}

// ModelInfo is one model of one access. A zero Context or MaxOut means
// unknown, never zero tokens: the resolver must say "context unknown"
// instead of guessing. Cost is USD per million tokens.
type ModelInfo struct {
	Access    string   `json:"access"`
	ID        string   `json:"id"`
	Provider  string   `json:"provider,omitempty"`
	Context   int      `json:"context,omitempty"`
	MaxOut    int      `json:"max_out,omitempty"`
	Reasoning bool     `json:"reasoning,omitempty"`
	Efforts   []string `json:"efforts,omitempty"`
	Cost      *Cost    `json:"cost,omitempty"`
	Input     []string `json:"input,omitempty"`
	Status    string   `json:"status,omitempty"`
	Source    string   `json:"source,omitempty"`
}

// Cost is the price of a model in USD per million tokens, the unit Pi and
// OpenCode report.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Key is the catalog key of a model: the access and the model ID.
func (m ModelInfo) Key() string { return m.Access + "/" + m.ID }

// DefaultCachePath returns the catalog cache path:
// $XDG_CACHE_HOME/docflow/roster/catalog.json, ~/.cache otherwise. A
// relative XDG_CACHE_HOME is resolved against the working directory, so
// the cache never lands in the repository.
func DefaultCachePath() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base != "" {
		if absolute, err := filepath.Abs(base); err == nil {
			base = absolute
		}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			base = "~/.cache"
		} else {
			base = filepath.Join(home, ".cache")
		}
	}
	return filepath.Join(base, "docflow", "roster", "catalog.json")
}

// LoadCatalog reads the catalog cache at path ("" means
// DefaultCachePath). A missing file is an empty catalog, not an error;
// unreadable JSON is an exit-code-2 error naming the file. A file with a
// version this binary does not know is treated as absent: the cache is
// rebuilt by the next scan.
func LoadCatalog(path string) (*Catalog, error) {
	if path == "" {
		path = DefaultCachePath()
	}
	path = expandUser(path)
	text, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return emptyCatalog(), nil
	}
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: catalog cache: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	catalog := &Catalog{}
	if err := json.Unmarshal(text, catalog); err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", path, err), Code: clierrors.EXIT_FORMAT}
	}
	if catalog.Version != CatalogVersion {
		return emptyCatalog(), nil
	}
	catalog.Sort()
	return catalog, nil
}

// Save writes the catalog to path ("" means DefaultCachePath)
// atomically: a temporary file in the cache directory is renamed over the
// target, so a reader never sees a half-written catalog. Models and
// accesses are sorted first, which keeps the file stable between runs.
func (c *Catalog) Save(path string) error {
	if path == "" {
		path = DefaultCachePath()
	}
	path = expandUser(path)
	c.Sort()
	content, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: catalog: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: catalog cache: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o644); err != nil {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: catalog cache: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: catalog cache: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return nil
}

// emptyCatalog returns a catalog with non-nil slices, so the JSON of an
// empty cache has [] instead of null.
func emptyCatalog() *Catalog {
	return &Catalog{Version: CatalogVersion, Sources: []string{}, Accesses: []AccessInfo{}, Models: []ModelInfo{}}
}

// Sort orders accesses by name and models by access, then ID: every
// listing of the catalog is stable without caring about scan order.
func (c *Catalog) Sort() {
	if c.Models == nil {
		c.Models = []ModelInfo{}
	}
	if c.Accesses == nil {
		c.Accesses = []AccessInfo{}
	}
	if c.Sources == nil {
		c.Sources = []string{}
	}
	sort.SliceStable(c.Accesses, func(i, j int) bool { return c.Accesses[i].Access < c.Accesses[j].Access })
	sort.SliceStable(c.Models, func(i, j int) bool {
		a, b := c.Models[i], c.Models[j]
		if a.Access != b.Access {
			return a.Access < b.Access
		}
		return a.ID < b.ID
	})
}

// Merge folds other into c by the access name and the access/id key: a
// later source overrides the fields of an earlier one, but an empty value
// never erases a non-empty one, so a partial scanner cannot wipe what a
// richer one found. Sources keep their first-seen order.
func (c *Catalog) Merge(other *Catalog) {
	if other == nil {
		return
	}
	if c.Version == 0 {
		c.Version = CatalogVersion
	}
	if other.ScannedAt != "" {
		c.ScannedAt = other.ScannedAt
	}
	for _, source := range other.Sources {
		if source != "" && !contains(c.Sources, source) {
			c.Sources = append(c.Sources, source)
		}
	}
	accesses := make(map[string]int, len(c.Accesses))
	for i, access := range c.Accesses {
		accesses[access.Access] = i
	}
	for _, access := range other.Accesses {
		if access.Access == "" {
			continue
		}
		if i, ok := accesses[access.Access]; ok {
			c.Accesses[i].mergeFrom(access)
			continue
		}
		accesses[access.Access] = len(c.Accesses)
		c.Accesses = append(c.Accesses, access)
	}
	models := make(map[string]int, len(c.Models))
	for i, model := range c.Models {
		models[model.Key()] = i
	}
	for _, model := range other.Models {
		if model.Access == "" || model.ID == "" {
			continue
		}
		if i, ok := models[model.Key()]; ok {
			c.Models[i].mergeFrom(model)
			continue
		}
		models[model.Key()] = len(c.Models)
		c.Models = append(c.Models, model)
	}
}

// mergeFrom overrides the fields of a with the non-empty fields of other
// and records other's source when it contributed at least one field.
func (a *AccessInfo) mergeFrom(other AccessInfo) {
	contributed := false
	for _, agent := range other.Agents {
		if agent != "" && !contains(a.Agents, agent) {
			a.Agents = append(a.Agents, agent)
			contributed = true
		}
	}
	if other.Kind != "" {
		a.Kind = other.Kind
		contributed = true
	}
	if other.Auth != "" {
		a.Auth = other.Auth
		contributed = true
	}
	if other.Status != "" {
		a.Status = other.Status
		contributed = true
	}
	if contributed && other.Source != "" {
		a.Source = other.Source
	}
}

// mergeFrom is the field-level counterpart of AccessInfo.mergeFrom: a
// boolean can only be raised, lists are replaced when non-empty, and an
// unknown cost never clears a known one.
func (m *ModelInfo) mergeFrom(other ModelInfo) {
	contributed := false
	if other.Provider != "" {
		m.Provider = other.Provider
		contributed = true
	}
	if other.Context > 0 {
		m.Context = other.Context
		contributed = true
	}
	if other.MaxOut > 0 {
		m.MaxOut = other.MaxOut
		contributed = true
	}
	if other.Reasoning && !m.Reasoning {
		m.Reasoning = true
		contributed = true
	}
	if len(other.Efforts) > 0 {
		m.Efforts = append([]string{}, other.Efforts...)
		contributed = true
	}
	if other.Cost != nil {
		cost := *other.Cost
		m.Cost = &cost
		contributed = true
	}
	if len(other.Input) > 0 {
		m.Input = append([]string{}, other.Input...)
		contributed = true
	}
	if other.Status != "" {
		m.Status = other.Status
		contributed = true
	}
	if contributed && other.Source != "" {
		m.Source = other.Source
	}
}
