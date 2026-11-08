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
// Provenance keeps that source per field, so a merged catalog still says
// which scanner supplied the kind, the auth status and the status. Auth
// folds the channels (any ready door makes the access usable); Channels
// keeps the answer of every channel, because a route runs through one
// agent, and pi without a key is not ready just because opencode is.
type AccessInfo struct {
	Access     string            `json:"access"`
	Agents     []string          `json:"agents,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Auth       string            `json:"auth,omitempty"`
	Channels   map[string]string `json:"channels,omitempty"`
	Status     string            `json:"status,omitempty"`
	Source     string            `json:"source,omitempty"`
	Provenance map[string]string `json:"provenance,omitempty"`
}

// ModelInfo is one model of one access. A zero Context or MaxOut means
// unknown, never zero tokens: the resolver must say "context unknown"
// instead of guessing. Cost is USD per million tokens. Provenance maps a
// field to the source that supplied its current value: with two channels
// of one access the last contributor of Source hides whether the context
// came from an exact catalog or from a rounded fallback. Agents are the
// channels whose catalog lists the model: Pi's catalog of an access can be
// wider than what OpenCode serves on the same login, and a route must not
// send a model to an agent that does not know it. Empty means unknown (an
// older cache): any channel of the access may serve it.
type ModelInfo struct {
	Access     string            `json:"access"`
	ID         string            `json:"id"`
	Agents     []string          `json:"agents,omitempty"`
	Provider   string            `json:"provider,omitempty"`
	Context    int               `json:"context,omitempty"`
	MaxOut     int               `json:"max_out,omitempty"`
	Reasoning  bool              `json:"reasoning,omitempty"`
	Efforts    []string          `json:"efforts,omitempty"`
	Cost       *Cost             `json:"cost,omitempty"`
	Input      []string          `json:"input,omitempty"`
	Status     string            `json:"status,omitempty"`
	Source     string            `json:"source,omitempty"`
	Provenance map[string]string `json:"provenance,omitempty"`
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
		c.Accesses = append(c.Accesses, access.withProvenance())
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
		c.Models = append(c.Models, model.withProvenance())
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
		a.Provenance = stamp(a.Provenance, other.Source, "kind")
		contributed = true
	}
	if other.Auth != "" {
		a.Auth = other.Auth
		a.Provenance = stamp(a.Provenance, other.Source, "auth")
		contributed = true
	}
	for agent, status := range other.Channels {
		if agent == "" || status == "" {
			continue
		}
		if a.Channels == nil {
			a.Channels = map[string]string{}
		}
		a.Channels[agent] = status
		contributed = true
	}
	if other.Status != "" {
		a.Status = other.Status
		a.Provenance = stamp(a.Provenance, other.Source, "status")
		contributed = true
	}
	if contributed && other.Source != "" {
		a.Source = other.Source
	}
}

// withProvenance stamps the fields an entry already carries with its own
// source, so an entry added by the first source has the same per-field
// view as one that later sources overrode.
func (a AccessInfo) withProvenance() AccessInfo {
	if len(a.Provenance) > 0 {
		return a
	}
	a.Provenance = nil
	if a.Kind != "" {
		a.Provenance = stamp(a.Provenance, a.Source, "kind")
	}
	if a.Auth != "" {
		a.Provenance = stamp(a.Provenance, a.Source, "auth")
	}
	if a.Status != "" {
		a.Provenance = stamp(a.Provenance, a.Source, "status")
	}
	return a
}

// mergeFrom is the field-level counterpart of AccessInfo.mergeFrom: a
// boolean can only be raised, lists are replaced when non-empty, and an
// unknown cost never clears a known one. Every overridden field records
// the source that supplied it.
func (m *ModelInfo) mergeFrom(other ModelInfo) {
	contributed := false
	fields := make([]string, 0, 8)
	for _, agent := range other.Agents {
		if agent != "" && !contains(m.Agents, agent) {
			m.Agents = append(m.Agents, agent)
			contributed = true
		}
	}
	if other.Provider != "" {
		m.Provider = other.Provider
		contributed = true
		fields = append(fields, "provider")
	}
	if other.Context > 0 {
		m.Context = other.Context
		contributed = true
		fields = append(fields, "context")
	}
	if other.MaxOut > 0 {
		m.MaxOut = other.MaxOut
		contributed = true
		fields = append(fields, "max_out")
	}
	if other.Reasoning && !m.Reasoning {
		m.Reasoning = true
		contributed = true
		fields = append(fields, "reasoning")
	}
	if len(other.Efforts) > 0 {
		m.Efforts = append([]string{}, other.Efforts...)
		contributed = true
		fields = append(fields, "efforts")
	}
	if other.Cost != nil {
		cost := *other.Cost
		m.Cost = &cost
		contributed = true
		fields = append(fields, "cost")
	}
	if len(other.Input) > 0 {
		m.Input = append([]string{}, other.Input...)
		contributed = true
		fields = append(fields, "input")
	}
	if other.Status != "" {
		m.Status = other.Status
		contributed = true
		fields = append(fields, "status")
	}
	for _, field := range fields {
		m.Provenance = stamp(m.Provenance, other.Source, field)
	}
	if contributed && other.Source != "" {
		m.Source = other.Source
	}
}

// withProvenance is the ModelInfo counterpart of
// AccessInfo.withProvenance: the source of every field the entry carries.
func (m ModelInfo) withProvenance() ModelInfo {
	if len(m.Provenance) > 0 {
		return m
	}
	m.Provenance = nil
	if m.Provider != "" {
		m.Provenance = stamp(m.Provenance, m.Source, "provider")
	}
	if m.Context > 0 {
		m.Provenance = stamp(m.Provenance, m.Source, "context")
	}
	if m.MaxOut > 0 {
		m.Provenance = stamp(m.Provenance, m.Source, "max_out")
	}
	if m.Reasoning {
		m.Provenance = stamp(m.Provenance, m.Source, "reasoning")
	}
	if len(m.Efforts) > 0 {
		m.Provenance = stamp(m.Provenance, m.Source, "efforts")
	}
	if m.Cost != nil {
		m.Provenance = stamp(m.Provenance, m.Source, "cost")
	}
	if len(m.Input) > 0 {
		m.Provenance = stamp(m.Provenance, m.Source, "input")
	}
	if m.Status != "" {
		m.Provenance = stamp(m.Provenance, m.Source, "status")
	}
	return m
}

// stamp records source as the origin of one field; an empty source (a
// fixture without one) leaves the map alone.
func stamp(provenance map[string]string, source, field string) map[string]string {
	if source == "" {
		return provenance
	}
	if provenance == nil {
		provenance = map[string]string{}
	}
	provenance[field] = source
	return provenance
}
