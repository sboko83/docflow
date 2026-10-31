package roster

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"bsv-docflow/internal/agentio"
)

// The sources a scanner stamps into Catalog.Sources and into the Source
// field of the entries it contributes. A catalog entry keeps the name of
// the last source that filled a field, and ModelInfo.Provenance keeps the
// name per field.
const (
	SourcePiStore  = "pi-store"  // ~/.pi/agent/models-store.json
	SourcePiModels = "pi-models" // ~/.pi/agent/models.json
	SourcePiList   = "pi-list"   // pi --list-models, the store-less fallback
	SourceOpenCode = "opencode"  // opencode api get /api/model
)

// scanTimeout bounds one probe command: a hung CLI stops the scan instead
// of hanging the planner. The probes call no model and spend no quota.
const scanTimeout = 30 * time.Second

// ScanPi reads the Pi model catalogs and returns the model catalog with
// the agent channel "pi" added to every access. Sources are merged in
// fidelity order: the cached store, then the rounded pi --list-models
// fallback when the store is absent, then the exact user config, so the
// config wins over a rounded table and an unknown field stays as the
// store filled it. Every problem — a broken provider entry, a missing CLI
// — is a warning next to a partial catalog, never an error: a plan can be
// composed while one channel of one access is unreadable.
func ScanPi() (*Catalog, []string) {
	catalog := emptyCatalog()
	var problems []string
	home, err := os.UserHomeDir()
	if err != nil {
		catalog.Sort()
		return catalog, []string{fmt.Sprintf("pi: cannot find the home directory: %v", err)}
	}
	dir := filepath.Join(home, ".pi", "agent")
	storePath := filepath.Join(dir, "models-store.json")
	store, err := os.ReadFile(storePath)
	switch {
	case err == nil:
		found, warnings := parsePiStore(storePath, store)
		catalog.Merge(found)
		problems = append(problems, warnings...)
	case os.IsNotExist(err):
		found, warnings := scanPiList()
		catalog.Merge(found)
		problems = append(problems, warnings...)
	default:
		problems = append(problems, fmt.Sprintf("pi: %s: %v", storePath, err))
	}
	configPath := filepath.Join(dir, "models.json")
	config, err := os.ReadFile(configPath)
	if err == nil {
		found, warnings := parsePiModels(configPath, config)
		catalog.Merge(found)
		problems = append(problems, warnings...)
	} else if !os.IsNotExist(err) {
		problems = append(problems, fmt.Sprintf("pi: %s: %v", configPath, err))
	}
	catalog.Sort()
	return catalog, problems
}

// ScanOpenCode asks the local OpenCode server for its model catalog and
// returns it with the agent channel "opencode" added to every access. The
// call goes through the CLI, never the network directly: the server holds
// the credentials. A missing CLI, an HTML answer from a proxy or a broken
// entry is a warning next to a partial catalog, not a panic.
func ScanOpenCode() (*Catalog, []string) {
	run, err := agentio.CallAgent("", []string{"opencode", "api", "get", "/api/model"}, nil, "", agentio.CallOptions{Timeout: scanTimeout})
	if err != nil || run.TimedOut || run.Code != 0 {
		catalog := emptyCatalog()
		return catalog, []string{"opencode: " + commandFailure("opencode", run, err)}
	}
	catalog, problems := parseOpenCode(run.Stdout)
	catalog.Sort()
	return catalog, problems
}

// commandFailure renders a failed probe as one line: the error, the exit
// code and the first stderr line, which never holds a key — the probes
// print statuses, not secrets.
func commandFailure(name string, run agentio.AgentRun, err error) string {
	if err != nil {
		return name + ": " + strings.TrimPrefix(err.Error(), "run: ")
	}
	if run.TimedOut {
		return fmt.Sprintf("%s timed out after %s", name, scanTimeout)
	}
	if detail := firstLine(run.Stderr); detail != "" {
		return fmt.Sprintf("%s exited %d: %s", name, run.Code, detail)
	}
	return fmt.Sprintf("%s exited %d without output", name, run.Code)
}

// firstLine keeps one short line of a tool message: a full stderr dump is
// noise in a warning and can carry a multi-line banner.
func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if runes := []rune(text); len(runes) > 200 {
		text = string(runes[:200]) + "…"
	}
	return text
}

// piProvider is one provider entry of models-store.json or of the
// providers table of models.json. Only the fields the catalog needs are
// declared: an auth key in models.json is never decoded, so it cannot
// reach the cache, a warning or a commit.
type piProvider struct {
	Models []piModel `json:"models"`
}

// piModel is one model of a Pi catalog. Number fields tolerate both a
// number and a numeric string: the store is a cache Pi may rewrite.
type piModel struct {
	ID               string                     `json:"id"`
	Provider         string                     `json:"provider"`
	Reasoning        bool                       `json:"reasoning"`
	Input            []string                   `json:"input"`
	Cost             *piCost                    `json:"cost"`
	ContextWindow    jsonInt                    `json:"contextWindow"`
	MaxTokens        jsonInt                    `json:"maxTokens"`
	ThinkingLevelMap map[string]json.RawMessage `json:"thinkingLevelMap"`
}

// piCost is the USD-per-million price Pi reports.
type piCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// info turns a Pi model into a catalog entry. An entry without an ID is
// skipped: it cannot be keyed or routed.
func (m piModel) info(access, source string) (ModelInfo, bool) {
	if m.ID == "" {
		return ModelInfo{}, false
	}
	provider := m.Provider
	if provider == "" {
		provider = access
	}
	info := ModelInfo{
		Access:    access,
		ID:        m.ID,
		Provider:  provider,
		Context:   int(m.ContextWindow),
		MaxOut:    int(m.MaxTokens),
		Reasoning: m.Reasoning,
		Efforts:   levelMapEfforts(m.ThinkingLevelMap),
		Input:     cleanList(m.Input),
		Source:    source,
	}
	if m.Cost != nil {
		info.Cost = &Cost{Input: m.Cost.Input, Output: m.Cost.Output, CacheRead: m.Cost.CacheRead, CacheWrite: m.Cost.CacheWrite}
	}
	return info, true
}

// parsePiStore decodes models-store.json: a table of providers keyed by
// the access name. Each provider is decoded on its own, so one provider
// Pi changed the shape of does not cost the rest of the catalog.
func parsePiStore(path string, text []byte) (*Catalog, []string) {
	catalog := emptyCatalog()
	providers, err := decodeTable(text)
	if err != nil {
		return catalog, []string{fmt.Sprintf("pi: %s: %v", path, err)}
	}
	catalog.Sources = []string{SourcePiStore}
	return catalog, fillPiProviders(catalog, path, providers, SourcePiStore)
}

// parsePiModels decodes models.json: the user's own providers, the same
// model shape as the store, nested under "providers".
func parsePiModels(path string, text []byte) (*Catalog, []string) {
	catalog := emptyCatalog()
	var root struct {
		Providers json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(text, &root); err != nil {
		return catalog, []string{fmt.Sprintf("pi: %s: %v", path, err)}
	}
	providers, err := decodeTable(root.Providers)
	if err != nil {
		return catalog, []string{fmt.Sprintf("pi: %s: providers: %v", path, err)}
	}
	catalog.Sources = []string{SourcePiModels}
	return catalog, fillPiProviders(catalog, path, providers, SourcePiModels)
}

// fillPiProviders decodes the provider entries of one Pi file into the
// catalog and returns the problems met on the way.
func fillPiProviders(catalog *Catalog, path string, providers map[string]json.RawMessage, source string) []string {
	var problems []string
	for _, name := range sortedKeys(providers) {
		var provider piProvider
		if err := json.Unmarshal(providers[name], &provider); err != nil {
			problems = append(problems, fmt.Sprintf("pi: %s: provider %q: %v", path, name, err))
			continue
		}
		catalog.Accesses = append(catalog.Accesses, AccessInfo{Access: name, Agents: []string{"pi"}, Source: source})
		for _, model := range provider.Models {
			if info, ok := model.info(name, source); ok {
				catalog.Models = append(catalog.Models, info)
			}
		}
	}
	return problems
}

// decodeTable decodes a JSON object of raw entries, so entries are
// decoded one by one and a single broken one is a warning.
func decodeTable(text []byte) (map[string]json.RawMessage, error) {
	var table map[string]json.RawMessage
	if err := json.Unmarshal(text, &table); err != nil {
		return nil, err
	}
	if table == nil {
		table = map[string]json.RawMessage{}
	}
	return table, nil
}

// scanPiList reads the rounded model table of pi --list-models. It is the
// fallback when the store file is absent: no price, no exact token
// counts, but the model list and the thinking/images flags are enough to
// find a route.
func scanPiList() (*Catalog, []string) {
	run, err := agentio.CallAgent("", []string{"pi", "--list-models"}, nil, "", agentio.CallOptions{Timeout: scanTimeout})
	if err != nil || run.TimedOut || run.Code != 0 {
		return emptyCatalog(), []string{"pi: " + commandFailure("pi", run, err)}
	}
	return parsePiList(run.Stdout), nil
}

// parsePiList parses the pi --list-models table: provider, model,
// context, max-out, thinking, images. Unknown columns and a row without a
// model name are skipped; the token sizes keep the 1024 base of the table
// and its one-decimal rounding.
func parsePiList(text string) *Catalog {
	catalog := emptyCatalog()
	catalog.Sources = []string{SourcePiList}
	accesses := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "provider" || strings.HasPrefix(fields[0], "-") {
			continue
		}
		access, id := fields[0], fields[1]
		info := ModelInfo{
			Access:   access,
			ID:       id,
			Provider: access,
			Input:    []string{"text"},
			Source:   SourcePiList,
		}
		if len(fields) > 2 {
			info.Context = parseSize(fields[2])
		}
		if len(fields) > 3 {
			info.MaxOut = parseSize(fields[3])
		}
		if len(fields) > 4 && isYes(fields[4]) {
			info.Reasoning = true
		}
		if len(fields) > 5 && isYes(fields[5]) {
			info.Input = []string{"text", "image"}
		}
		catalog.Models = append(catalog.Models, info)
		if !accesses[access] {
			accesses[access] = true
			catalog.Accesses = append(catalog.Accesses, AccessInfo{Access: access, Agents: []string{"pi"}, Source: SourcePiList})
		}
	}
	return catalog
}

// isYes reads the yes/no cells of the Pi table.
func isYes(cell string) bool {
	return strings.EqualFold(cell, "yes") || strings.EqualFold(cell, "true")
}

// parseSize reads a token count of the Pi table: 262144, 262.1K, 1M, or
// "-" for unknown. Suffixes are binary, the way the table prints them.
func parseSize(cell string) int {
	cell = strings.TrimSpace(cell)
	if cell == "" || cell == "-" {
		return 0
	}
	multiplier := 1.0
	switch cell[len(cell)-1] {
	case 'K', 'k':
		multiplier, cell = 1024, cell[:len(cell)-1]
	case 'M', 'm':
		multiplier, cell = 1024*1024, cell[:len(cell)-1]
	case 'G', 'g':
		multiplier, cell = 1024*1024*1024, cell[:len(cell)-1]
	}
	value, err := strconv.ParseFloat(cell, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return int(math.Round(value * multiplier))
}

// ocModel is one entry of the OpenCode /api/model response. The headers
// and settings fields are not declared on purpose: they carry an org ID
// and credentials that must not reach the catalog.
type ocModel struct {
	ID            string      `json:"id"`
	ModelID       string      `json:"modelID"`
	ProviderID    string      `json:"providerID"`
	Variants      []ocVariant `json:"variants"`
	Cost          []ocCost    `json:"cost"`
	Status        string      `json:"status"`
	Capabilities  ocCaps      `json:"capabilities"`
	Compatibility ocCompat    `json:"compatibility"`
	Limit         ocLimit     `json:"limit"`
}

type ocVariant struct {
	ID       string                     `json:"id"`
	Settings map[string]json.RawMessage `json:"settings"`
}

type ocCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
	Cache  struct {
		Read  float64 `json:"read"`
		Write float64 `json:"write"`
	} `json:"cache"`
}

type ocCaps struct {
	Input []string `json:"input"`
}

type ocCompat struct {
	ReasoningField string `json:"reasoningField"`
}

type ocLimit struct {
	Context jsonInt `json:"context"`
	Output  jsonInt `json:"output"`
}

// reasoning reports whether the model reasons. OpenCode marks it either
// by a reasoning field mapping or by a variant that sets a reasoning
// setting; a model with neither is treated as non-reasoning, since a
// variant list is what the router actually offers.
func (m ocModel) reasoning() bool {
	if m.Compatibility.ReasoningField != "" {
		return true
	}
	for _, variant := range m.Variants {
		for key := range variant.Settings {
			switch key {
			case "reasoningEffort", "reasoningSummary", "thinking", "thinkingConfig", "effort":
				return true
			}
		}
	}
	return false
}

// info turns an OpenCode model into a catalog entry. The variant IDs are
// the efforts a route may name: the OpenCode adapter passes the chosen
// one as #variant.
func (m ocModel) info() (ModelInfo, bool) {
	id := m.ModelID
	if id == "" {
		id = m.ID
	}
	if id == "" || m.ProviderID == "" {
		return ModelInfo{}, false
	}
	efforts := map[string]bool{}
	for _, variant := range m.Variants {
		if variant.ID != "" {
			efforts[variant.ID] = true
		}
	}
	info := ModelInfo{
		Access:    m.ProviderID,
		ID:        id,
		Provider:  m.ProviderID,
		Context:   int(m.Limit.Context),
		MaxOut:    int(m.Limit.Output),
		Reasoning: m.reasoning(),
		Efforts:   orderedEfforts(efforts),
		Input:     cleanList(m.Capabilities.Input),
		Status:    m.Status,
		Source:    SourceOpenCode,
	}
	if len(m.Cost) > 0 {
		cost := m.Cost[0]
		info.Cost = &Cost{Input: cost.Input, Output: cost.Output, CacheRead: cost.Cache.Read, CacheWrite: cost.Cache.Write}
	}
	return info, true
}

// parseOpenCode decodes the {location, data} answer of the local OpenCode
// server. Anything else — HTML from a proxy, a truncated body — is a
// source problem, and the raw body is not quoted: it may carry request
// headers.
func parseOpenCode(text string) (*Catalog, []string) {
	catalog := emptyCatalog()
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "<") {
		return catalog, []string{"opencode: /api/model answered HTML, not JSON"}
	}
	var root struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		return catalog, []string{"opencode: /api/model answered no JSON"}
	}
	if root.Data == nil {
		return catalog, []string{"opencode: /api/model answered no data list"}
	}
	catalog.Sources = []string{SourceOpenCode}
	accesses := map[string]bool{}
	var problems []string
	for _, raw := range root.Data {
		var model ocModel
		if err := json.Unmarshal(raw, &model); err != nil {
			problems = append(problems, fmt.Sprintf("opencode: /api/model: %v", err))
			continue
		}
		info, ok := model.info()
		if !ok {
			problems = append(problems, "opencode: /api/model: entry without provider or model id")
			continue
		}
		if !accesses[info.Access] {
			accesses[info.Access] = true
			catalog.Accesses = append(catalog.Accesses, AccessInfo{Access: info.Access, Agents: []string{"opencode"}, Source: SourceOpenCode})
		}
		catalog.Models = append(catalog.Models, info)
	}
	return catalog, problems
}

// jsonInt is a tolerant integer: a JSON number or a numeric string, null
// and garbage as zero. Token counts are unknown when zero; a store that
// switches the type must not fail the whole scan.
type jsonInt int

func (n *jsonInt) UnmarshalJSON(data []byte) error {
	text := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if text == "" || text == "null" {
		*n = 0
		return nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		*n = 0
		return nil
	}
	*n = jsonInt(math.Round(value))
	return nil
}

// sortedKeys orders a decoded table, so a scan of the same files always
// yields the same catalog; the condition state sorts its tables with it.
func sortedKeys[V any](table map[string]V) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// cleanList trims, dedupes and orders a list of modalities.
func cleanList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// effortLadder is the order efforts are listed in: the reasoning levels
// from off to maximum, shared by Pi and OpenCode. Names outside the ladder
// follow, sorted.
var effortLadder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// orderedEfforts renders a set of effort names in ladder order.
func orderedEfforts(efforts map[string]bool) []string {
	if len(efforts) == 0 {
		return nil
	}
	ordered := make([]string, 0, len(efforts))
	for _, name := range effortLadder {
		if efforts[name] {
			ordered = append(ordered, name)
			delete(efforts, name)
		}
	}
	rest := make([]string, 0, len(efforts))
	for name := range efforts {
		rest = append(rest, name)
	}
	sort.Strings(rest)
	ordered = append(ordered, rest...)
	return ordered
}

// levelMapEfforts reads the supported levels of a Pi thinkingLevelMap: a
// null value means the level is not available for this model.
func levelMapEfforts(levels map[string]json.RawMessage) []string {
	efforts := map[string]bool{}
	for name, value := range levels {
		if name == "" || strings.TrimSpace(string(value)) == "null" {
			continue
		}
		efforts[name] = true
	}
	return orderedEfforts(efforts)
}
