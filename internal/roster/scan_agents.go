package roster

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bsv-docflow/internal/agentio"
)

// The sources of the auth probes, the offline table and the optional
// models.dev enrichment. Like the scanners' names they are stamped into
// Catalog.Sources and into the per-field provenance.
const (
	SourceAuth      = "auth"       // the pi/opencode/codex/claude probes
	SourceBuiltin   = "builtin"    // the offline Codex and Claude table
	SourceModelsDev = "models.dev" // https://models.dev/api.json
)

// AuthReady, AuthNotReady and AuthUnknown are the access states the
// probes reported: ready means the probe answered "usable", not_ready
// that it answered "not configured", and unknown that no probe could
// decide. An access with several channels is ready when any channel is
// ready: one quota unit, one working door.
const (
	AuthReady    = "ready"
	AuthNotReady = "not_ready"
	AuthUnknown  = "unknown"
)

// ScanAuth probes every access of catalog through the agent channels it
// lists and merges the auth status back into catalog, stamping the source
// "auth" on the field it filled. No probe prints or stores a credential:
// the key-printing commands are never called, and a failed probe becomes
// one warning per channel, not one per access. The returned warnings are
// the same shape as the scanners' problems: a partial result is not an
// error.
func ScanAuth(catalog *Catalog) []string {
	if catalog == nil {
		return nil
	}
	channels := map[string]authResult{}
	var problems []string
	probe := func(agent string, result authResult) {
		channels[agent] = result
		if result.problem != "" {
			problems = append(problems, result.problem)
		}
	}
	if names := accessesWithAgent(catalog, "pi"); len(names) > 0 {
		probe("pi", probePiAuth(names))
	}
	if names := accessesWithAgent(catalog, "opencode"); len(names) > 0 {
		probe("opencode", probeOpenCodeAuth())
	}
	if names := accessesWithAgent(catalog, "codex"); len(names) > 0 {
		probe("codex", probeCodexAuth(names))
	}
	if names := accessesWithAgent(catalog, "claude"); len(names) > 0 {
		probe("claude", probeClaudeAuth(names))
	}
	if len(channels) == 0 {
		return problems
	}

	found := emptyCatalog()
	found.Sources = []string{SourceAuth}
	for _, access := range catalog.Accesses {
		var statuses []string
		for _, agent := range access.Agents {
			result, ok := channels[agent]
			if !ok {
				continue
			}
			if status := result.status[access.Access]; status != "" {
				statuses = append(statuses, status)
			}
		}
		status := combineAuth(statuses)
		if status == "" {
			continue
		}
		found.Accesses = append(found.Accesses, AccessInfo{Access: access.Access, Auth: status, Source: SourceAuth})
	}
	catalog.Merge(found)
	catalog.Sort()
	return problems
}

// authResult is one channel's answer: the status per access name and, at
// most, one warning describing why the channel could not answer.
type authResult struct {
	status  map[string]string
	problem string
}

// freshStatus returns an initialized result.
func freshStatus() authResult {
	return authResult{status: map[string]string{}}
}

// combineAuth folds the channel statuses of one access: any ready door
// makes the access usable, otherwise a definite not_ready wins over an
// undecided channel.
func combineAuth(statuses []string) string {
	if len(statuses) == 0 {
		return ""
	}
	for _, status := range statuses {
		if status == AuthReady {
			return AuthReady
		}
	}
	for _, status := range statuses {
		if status == AuthNotReady {
			return AuthNotReady
		}
	}
	return AuthUnknown
}

// accessesWithAgent lists the access names reached through one agent
// channel, in catalog order.
func accessesWithAgent(catalog *Catalog, agent string) []string {
	var names []string
	for _, access := range catalog.Accesses {
		for _, channel := range access.Agents {
			if channel == agent {
				names = append(names, access.Access)
				break
			}
		}
	}
	return names
}

// probePiAuth asks pi about one provider at a time. The answer is a JSON
// status, and pi exits nonzero for not_ready, so the status in the body
// decides, not the exit code.
func probePiAuth(names []string) authResult {
	result := freshStatus()
	for _, name := range names {
		run, err := agentio.CallAgent("", []string{"pi", "auth", "check", "--provider", name, "--json"}, nil, "", agentio.CallOptions{Timeout: scanTimeout})
		if err != nil || run.TimedOut {
			result.status[name] = AuthUnknown
			if result.problem == "" {
				result.problem = commandFailure("pi auth check", run, err)
			}
			continue
		}
		var answer struct {
			Status string `json:"status"`
		}
		text := strings.TrimSpace(run.Stdout)
		if json.Unmarshal([]byte(text), &answer) != nil || answer.Status == "" {
			result.status[name] = AuthUnknown
			if result.problem == "" {
				result.problem = commandFailure("pi auth check", run, nil)
			}
			continue
		}
		switch answer.Status {
		case AuthReady:
			result.status[name] = AuthReady
		case AuthNotReady:
			result.status[name] = AuthNotReady
		default:
			result.status[name] = AuthUnknown
		}
	}
	return result
}

// probeOpenCodeAuth reads the provider list of the local OpenCode server:
// the activation field is the auth state, and the headers are not
// decoded — they carry an org ID that must not reach the catalog.
func probeOpenCodeAuth() authResult {
	result := freshStatus()
	run, err := agentio.CallAgent("", []string{"opencode", "api", "get", "/api/provider"}, nil, "", agentio.CallOptions{Timeout: scanTimeout})
	if err != nil || run.TimedOut || run.Code != 0 {
		result.problem = commandFailure("opencode", run, err)
		return result
	}
	trimmed := strings.TrimSpace(run.Stdout)
	if strings.HasPrefix(trimmed, "<") {
		result.problem = "opencode: /api/provider answered HTML, not JSON"
		return result
	}
	var root struct {
		Data []struct {
			ID         string `json:"id"`
			Activation string `json:"activation"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil || root.Data == nil {
		result.problem = "opencode: /api/provider answered no data list"
		return result
	}
	for _, provider := range root.Data {
		if provider.ID == "" {
			continue
		}
		if strings.EqualFold(provider.Activation, "enabled") {
			result.status[provider.ID] = AuthReady
		} else {
			result.status[provider.ID] = AuthNotReady
		}
	}
	return result
}

// probeCodexAuth asks the Codex CLI for its login state. The answer is
// plain text, not JSON: exit 0 means logged in, a nonzero exit is the
// CLI's own "not logged in".
func probeCodexAuth(names []string) authResult {
	result := freshStatus()
	run, err := agentio.CallAgent("", []string{"codex", "login", "status"}, nil, "", agentio.CallOptions{Timeout: scanTimeout})
	if err != nil || run.TimedOut {
		result.problem = commandFailure("codex login status", run, err)
		for _, name := range names {
			result.status[name] = AuthUnknown
		}
		return result
	}
	status := AuthReady
	if run.Code != 0 {
		status = AuthNotReady
	}
	for _, name := range names {
		result.status[name] = status
	}
	return result
}

// probeClaudeAuth decides the Claude access by its config or a live CLI.
// A missing config and a missing CLI mean unknown, not not_ready: Claude
// is optional in the roster, and the scan does not guess about a machine
// it never saw. A CLI that is present and fails is a warning.
func probeClaudeAuth(names []string) authResult {
	result := freshStatus()
	ready := claudeConfigured()
	if !ready {
		run, err := agentio.CallAgent("", []string{"claude", "--version"}, nil, "", agentio.CallOptions{Timeout: scanTimeout})
		switch {
		case err == nil && !run.TimedOut && run.Code == 0:
			ready = true
		case err == nil && !run.TimedOut:
			result.problem = commandFailure("claude --version", run, nil)
		}
	}
	status := AuthUnknown
	if ready {
		status = AuthReady
	}
	for _, name := range names {
		result.status[name] = status
	}
	return result
}

// claudeConfigured reports whether the Claude CLI has a config on this
// machine. Only the presence is checked: the file is never read, so its
// credentials cannot reach the scan.
func claudeConfigured() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, name := range []string{".claude.json", filepath.Join(".claude", ".credentials.json")} {
		if info, err := os.Stat(filepath.Join(home, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// builtinModel is one entry of the offline table: the fields this
// machine cannot scan, because neither Codex nor Claude publishes a
// readable catalog.
type builtinModel struct {
	id      string
	context int
	maxOut  int
	efforts []string
	cost    *Cost
}

// builtinCodexModels are the Codex model slugs this CLI offers. The
// context is the window the CLI itself reports (not the API window), and
// the price is the published API price: it names the model's class, the
// access kind decides whether the resolver may compare it.
var builtinCodexModels = []builtinModel{
	{id: "gpt-6.1-sol", context: 500000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, cost: &Cost{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}},
	{id: "gpt-6.1-terra", context: 500000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, cost: &Cost{Input: 1, Output: 5, CacheRead: 0.05, CacheWrite: 1.25}},
	{id: "gpt-6-sol", context: 272000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, cost: &Cost{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}},
	{id: "gpt-6-luna", context: 272000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max"}, cost: &Cost{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125}},
	{id: "gpt-6-astra", context: 272000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, cost: &Cost{Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5}},
	{id: "gpt-5.6-sol", context: 272000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, cost: &Cost{Input: 4, Output: 20, CacheRead: 0.4, CacheWrite: 5}},
	{id: "gpt-5.6-terra", context: 272000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, cost: &Cost{Input: 2, Output: 12, CacheRead: 0.2, CacheWrite: 2.5}},
	{id: "gpt-5.6-luna", context: 272000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max"}, cost: &Cost{Input: 0.2, Output: 1.2, CacheRead: 0.02, CacheWrite: 0.25}},
}

// builtinClaudeModels are the Claude Code model slugs. Haiku 4.5 is the
// one model without named effort levels: its reasoning is a token budget,
// so the effort list stays empty instead of inventing levels.
var builtinClaudeModels = []builtinModel{
	{id: "claude-opus-5-5", context: 1000000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max"}, cost: &Cost{Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5}},
	{id: "claude-sonnet-5-5", context: 1000000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max"}, cost: &Cost{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}},
	{id: "claude-haiku-5-5", context: 1000000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max"}, cost: &Cost{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125}},
	{id: "claude-opus-5", context: 1000000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max"}, cost: &Cost{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25}},
	{id: "claude-sonnet-5", context: 1000000, maxOut: 128000, efforts: []string{"low", "medium", "high", "xhigh", "max"}, cost: &Cost{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}},
	{id: "claude-haiku-4-5", context: 200000, maxOut: 64000, cost: &Cost{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25}},
}

// BuiltinCatalog returns the offline table of the Codex and Claude
// models as a catalog: one access per CLI, the agent channel that
// reaches it, and the metadata the scanners cannot read anywhere on this
// machine. It merges like any other source, so a value the CLI reports
// later wins over the table.
func BuiltinCatalog() *Catalog {
	catalog := emptyCatalog()
	catalog.Sources = []string{SourceBuiltin}
	addBuiltin := func(access, agent string, models []builtinModel) {
		catalog.Accesses = append(catalog.Accesses, AccessInfo{Access: access, Agents: []string{agent}, Source: SourceBuiltin})
		for _, model := range models {
			info := ModelInfo{
				Access:    access,
				ID:        model.id,
				Provider:  access,
				Context:   model.context,
				MaxOut:    model.maxOut,
				Reasoning: true,
				Efforts:   append([]string{}, model.efforts...),
				Input:     []string{"text", "image"},
				Source:    SourceBuiltin,
			}
			if model.cost != nil {
				cost := *model.cost
				info.Cost = &cost
			}
			if len(info.Efforts) == 0 {
				info.Efforts = nil
			}
			catalog.Models = append(catalog.Models, info.withProvenance())
		}
	}
	addBuiltin("codex", "codex", builtinCodexModels)
	addBuiltin("claude", "claude", builtinClaudeModels)
	catalog.Sort()
	return catalog
}

// models.dev: an optional online source of context windows, prices and
// effort levels. It is fetched only with --online, cached next to the
// catalog cache for a day, and never overrides a value a scanner read
// from the tool itself — it only fills what is unknown.

const (
	// ModelsDevURL is the published aggregate of the model metadata.
	ModelsDevURL = "https://models.dev/api.json"
	// ModelsDevCacheVersion is the layout of the enrichment cache; another
	// version is treated as absent, like the catalog cache.
	ModelsDevCacheVersion = 1
	// modelsDevURLEnv overrides the endpoint for tests and mirrors.
	modelsDevURLEnv = "DOCFLOW_MODELS_DEV_URL"
	// modelsDevTTL is how long a fetched file is reused before a new scan
	// goes online again.
	modelsDevTTL = 24 * time.Hour
	// modelsDevMaxBytes bounds the response: the real file is ~6 MB, and a
	// broken endpoint must not stream forever.
	modelsDevMaxBytes = 32 << 20
)

// modelsDevProviders maps a catalog access to the provider ID it appears
// under in models.dev. The names do not match in general — Codex models
// are published as "openai", Claude as "anthropic" — so the mapping is
// explicit and nothing is matched by prefix.
var modelsDevProviders = map[string]string{
	"codex":       "openai",
	"openai":      "openai",
	"claude":      "anthropic",
	"anthropic":   "anthropic",
	"opencode":    "opencode",
	"opencode-go": "opencode",
	"zai":         "zai",
}

// EnrichOptions configures the optional online enrichment. The zero
// value is offline: EnrichModelsDev does nothing without Online.
type EnrichOptions struct {
	// Online enables the models.dev source.
	Online bool
	// Refresh ignores a cache file that is still fresh.
	Refresh bool
	// CachePath overrides the cache file ("" means
	// DefaultModelsDevCachePath).
	CachePath string
	// Endpoint overrides the URL ("" means $DOCFLOW_MODELS_DEV_URL, then
	// ModelsDevURL).
	Endpoint string
}

// EnrichModelsDev fills the unknown metadata of the catalog models from
// models.dev and returns how many models it touched plus the warnings.
// The online document is cached next to the catalog; a network error
// keeps what the scan and the built-in table already supplied instead of
// failing the scan. Only gaps are filled: a value a scanner read from the
// tool itself is never replaced by the aggregate.
func EnrichModelsDev(catalog *Catalog, options EnrichOptions) (int, []string) {
	if catalog == nil || !options.Online {
		return 0, nil
	}
	path := options.CachePath
	if path == "" {
		path = DefaultModelsDevCachePath()
	}
	path = expandUser(path)
	endpoint := modelsDevEndpoint(options.Endpoint)
	cache, cached := loadModelsDevCache(path)
	if cached && !options.Refresh && cache.fresh(time.Now()) {
		return enrichFromModelsDev(catalog, cache.Data), nil
	}

	data, err := fetchModelsDev(endpoint)
	if err != nil {
		warning := "models.dev: " + err.Error()
		if !cached {
			return 0, []string{warning + "; built-in metadata only"}
		}
		return enrichFromModelsDev(catalog, cache.Data), []string{warning + "; using the cached copy from " + cache.FetchedAt}
	}
	var problems []string
	if err := saveModelsDevCache(path, endpoint, data); err != nil {
		problems = append(problems, "models.dev: cache: "+err.Error())
	}
	return enrichFromModelsDev(catalog, data), problems
}

// enrichFromModelsDev fills the gaps of the catalog models from a decoded
// models.dev document and returns how many models changed.
func enrichFromModelsDev(catalog *Catalog, data []byte) int {
	providers := decodeModelsDev(data)
	if len(providers) == 0 {
		return 0
	}
	filled := 0
	for i := range catalog.Models {
		model := &catalog.Models[i]
		providerID, ok := modelsDevProviders[model.Access]
		if !ok {
			continue
		}
		raw, ok := providers[providerID][model.ID]
		if !ok {
			continue
		}
		var entry modelsDevModel
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		fields := model.fillFromModelsDev(entry)
		if len(fields) == 0 {
			continue
		}
		for _, field := range fields {
			model.Provenance = stamp(model.Provenance, SourceModelsDev, field)
		}
		model.Source = SourceModelsDev
		filled++
	}
	if filled > 0 {
		catalog.Sort()
	}
	return filled
}

// modelsDevModel is the part of a models.dev entry the catalog uses.
type modelsDevModel struct {
	ID               string                     `json:"id"`
	Reasoning        bool                       `json:"reasoning"`
	ReasoningOptions []modelsDevReasoningOption `json:"reasoning_options"`
	Modalities       struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Limit struct {
		Context jsonInt `json:"context"`
		Output  jsonInt `json:"output"`
	} `json:"limit"`
	Cost *modelsDevCost `json:"cost"`
}

type modelsDevReasoningOption struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

type modelsDevCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// efforts reads the named effort levels of an entry: a budget-token or a
// toggle reasoning option names no levels, so the list stays empty.
func (m modelsDevModel) efforts() []string {
	levels := map[string]bool{}
	for _, option := range m.ReasoningOptions {
		if option.Type != "effort" {
			continue
		}
		for _, value := range option.Values {
			if value != "" {
				levels[value] = true
			}
		}
	}
	return orderedEfforts(levels)
}

// fillFromModelsDev sets the fields the model does not know yet and
// returns their names. A known value is never replaced: the aggregate is
// the fallback, not the source of truth.
func (m *ModelInfo) fillFromModelsDev(entry modelsDevModel) []string {
	var fields []string
	if m.Context == 0 && entry.Limit.Context > 0 {
		m.Context = int(entry.Limit.Context)
		fields = append(fields, "context")
	}
	if m.MaxOut == 0 && entry.Limit.Output > 0 {
		m.MaxOut = int(entry.Limit.Output)
		fields = append(fields, "max_out")
	}
	if !m.Reasoning && entry.Reasoning {
		m.Reasoning = true
		fields = append(fields, "reasoning")
	}
	if len(m.Efforts) == 0 {
		if efforts := entry.efforts(); len(efforts) > 0 {
			m.Efforts = efforts
			fields = append(fields, "efforts")
		}
	}
	if m.Cost == nil && entry.Cost != nil {
		m.Cost = &Cost{Input: entry.Cost.Input, Output: entry.Cost.Output, CacheRead: entry.Cost.CacheRead, CacheWrite: entry.Cost.CacheWrite}
		fields = append(fields, "cost")
	}
	if len(m.Input) == 0 {
		if input := cleanList(entry.Modalities.Input); len(input) > 0 {
			m.Input = input
			fields = append(fields, "input")
		}
	}
	return fields
}

// decodeModelsDev decodes the top-level provider table and keeps the raw
// model table of each provider: one provider whose shape changed costs
// only that provider, and a model is decoded only when the catalog needs
// it.
func decodeModelsDev(data []byte) map[string]map[string]json.RawMessage {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	providers := make(map[string]map[string]json.RawMessage, len(raw))
	for id, body := range raw {
		var provider struct {
			Models map[string]json.RawMessage `json:"models"`
		}
		if json.Unmarshal(body, &provider) != nil || len(provider.Models) == 0 {
			continue
		}
		providers[id] = provider.Models
	}
	return providers
}

// modelsDevCache is the cached copy of the online document: the raw JSON
// is kept, so a cached run decodes it exactly like a fresh one.
type modelsDevCache struct {
	Version   int             `json:"version"`
	URL       string          `json:"url,omitempty"`
	FetchedAt string          `json:"fetched_at"`
	Data      json.RawMessage `json:"data"`
}

// fresh reports whether the cache is younger than the TTL.
func (c modelsDevCache) fresh(now time.Time) bool {
	fetched, err := time.Parse(time.RFC3339, c.FetchedAt)
	if err != nil {
		return false
	}
	return now.Sub(fetched) < modelsDevTTL
}

// DefaultModelsDevCachePath returns the enrichment cache path: the
// models.dev file sits next to the catalog cache, so both follow
// $XDG_CACHE_HOME and neither lands in the repository.
func DefaultModelsDevCachePath() string {
	return filepath.Join(filepath.Dir(DefaultCachePath()), "models.dev.json")
}

// loadModelsDevCache reads the cache at path. A missing, unreadable or
// foreign-version file is simply not a cache: it is disposable and the
// next online scan rewrites it.
func loadModelsDevCache(path string) (modelsDevCache, bool) {
	text, err := os.ReadFile(path)
	if err != nil {
		return modelsDevCache{}, false
	}
	var cache modelsDevCache
	if json.Unmarshal(text, &cache) != nil {
		return modelsDevCache{}, false
	}
	if cache.Version != ModelsDevCacheVersion || len(cache.Data) == 0 {
		return modelsDevCache{}, false
	}
	return cache, true
}

// saveModelsDevCache writes the fetched document atomically: a temporary
// file in the cache directory is renamed over the target.
func saveModelsDevCache(path, endpoint string, data []byte) error {
	cache := modelsDevCache{
		Version:   ModelsDevCacheVersion,
		URL:       endpoint,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Data:      json.RawMessage(data),
	}
	content, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// modelsDevEndpoint resolves the URL; the environment override is for
// tests and mirrors.
func modelsDevEndpoint(override string) string {
	if override != "" {
		return override
	}
	if endpoint := os.Getenv(modelsDevURLEnv); endpoint != "" {
		return endpoint
	}
	return ModelsDevURL
}

// fetchModelsDev downloads the aggregate. The body never reaches an
// error: only the status code and the transport error are quoted.
func fetchModelsDev(endpoint string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "docflow")
	client := &http.Client{Timeout: scanTimeout}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("answered HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, modelsDevMaxBytes))
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, errors.New("answered no JSON")
	}
	return body, nil
}
