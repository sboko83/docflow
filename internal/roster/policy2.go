package roster

import (
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
)

// PolicyVersion is the format marker of a v2 roster: a top-level
// version = 2. The old roster has no version key; migrating it is a
// separate command, and Load never guesses.
const PolicyVersion = 2

// Access budgets, from most to least available: prefer and normal serve
// every role, lean skips heavy, acceptance and replan, minimal serves
// only acceptance and replan, off serves nothing.
const (
	BudgetPrefer  = "prefer"
	BudgetNormal  = "normal"
	BudgetLean    = "lean"
	BudgetMinimal = "minimal"
	BudgetOff     = "off"
)

// DefaultBudget is the budget of an access without a budget key.
const DefaultBudget = BudgetNormal

var (
	// Budgets lists the access budgets from most to least available.
	Budgets = []string{BudgetPrefer, BudgetNormal, BudgetLean, BudgetMinimal, BudgetOff}
	// Kinds lists the access kinds: a subscription is a quota, a metered
	// access is billed per token, a local access runs on this machine.
	Kinds = []string{"subscription", "metered", "local"}
	// PolicyAgents lists the agents an access may be reached through.
	PolicyAgents = []string{"claude", "codex", "pi", "opencode"}
)

// Policy is a validated v2 roster policy (roster.toml): the stable
// configuration of accesses, model classes, role requirements, effort
// overrides and presets. Temporary conditions do not live here: they go
// to roster.state.toml, and the resolver merges them over the policy.
type Policy struct {
	Path     string
	Version  int
	Accesses []Access          // [access.NAME] in file order
	Models   map[string]string // "ACCESS/MODEL" → cheap|standard|strong
	Roles    map[string]string // role → cheap|standard|strong|trusted
	Efforts  map[string]string // "ACCESS/MODEL" → effort level, overrides the class ladder
	Presets  []Preset          // [preset.NAME] in file order

	index map[string]int // access name → position in Accesses
}

// Access is one [access.NAME] section: the agent channels that reach it,
// its kind and budget, the review flags, the model allowlist and the
// expiry date (zero when the access never expires).
type Access struct {
	Name    string
	Agents  []string
	Kind    string
	Budget  string
	Trusted bool
	Check   bool
	Models  []string // model globs the access may serve; empty means all
	Exclude []string // model globs the access never serves
	Expires time.Time
}

// Preset is one [preset.NAME] section: the conditions roster brief and
// roster apply merge over the policy, plus the access order the preset
// sets. Access conditions (off/lean/minimal/prefer) are exclusive: one
// access carries at most one of them per preset. Order lists the accesses
// the preset puts first; the rest keep the policy file order. Models
// narrows an access to the globs it names; an empty list allows none.
type Preset struct {
	Name    string
	Off     []string
	Lean    []string
	Minimal []string
	Prefer  []string
	Order   []string
	Models  map[string][]string // access → model globs the preset allows
}

// Access looks up an access by name.
func (p *Policy) Access(name string) (*Access, bool) {
	position, ok := p.index[name]
	if !ok {
		return nil, false
	}
	return &p.Accesses[position], true
}

// AccessNames returns the access names in policy file order.
func (p *Policy) AccessNames() []string {
	names := make([]string, 0, len(p.Accesses))
	for _, access := range p.Accesses {
		names = append(names, access.Name)
	}
	return names
}

// Preset looks up a preset by name.
func (p *Policy) Preset(name string) (*Preset, bool) {
	for i := range p.Presets {
		if p.Presets[i].Name == name {
			return &p.Presets[i], true
		}
	}
	return nil, false
}

// PresetNames returns the preset names in file order.
func (p *Policy) PresetNames() []string {
	names := make([]string, 0, len(p.Presets))
	for _, preset := range p.Presets {
		names = append(names, preset.Name)
	}
	return names
}

// IsV2 reports whether the roster at path ("" means DefaultPath) is a v2
// policy: a top-level version = 2. Any other file is not v2, including
// the old roster and a version written as a string; a missing or
// unparsable file is an error, because the caller must not fall back to
// the old parser and hide a broken v2 file.
func IsV2(path string) (bool, error) {
	if path == "" {
		path = DefaultPath()
	}
	path = expandUser(path)
	text, err := os.ReadFile(path)
	if err != nil {
		return false, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster file: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	root, err := agents.ParseTOML(string(text))
	if err != nil {
		return false, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", path, err), Code: clierrors.EXIT_FORMAT}
	}
	version, ok := root["version"].(int64)
	return ok && version == PolicyVersion, nil
}

// LoadPolicy reads and validates the v2 policy at path ("" means
// DefaultPath).
func LoadPolicy(path string) (*Policy, error) {
	if path == "" {
		path = DefaultPath()
	}
	path = expandUser(path)
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster file: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return ParsePolicy(path, string(text))
}

// ParsePolicy parses and validates v2 policy text; path only locates the
// errors. Every policy problem is an exit-code-2 FormatError naming file
// and line.
func ParsePolicy(path, text string) (*Policy, error) {
	if name, line, duplicated := repeatedTable(text); duplicated {
		// The TOML parser rejects a repeated table with a bare line
		// number; a located error names the file and the line.
		return nil, clierrors.FormatError{Path: path, Line: line, Text: fmt.Sprintf("table [%s] is defined twice", name)}
	}
	root, err := agents.ParseTOML(text)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", path, err), Code: clierrors.EXIT_FORMAT}
	}
	policy := &Policy{Path: path}
	if err := policy.parse(scanLines(text), root); err != nil {
		return nil, err
	}
	return policy, nil
}

func (p *Policy) parse(index *lineIndex, root map[string]any) error {
	version, ok := root["version"].(int64)
	if !ok || version != PolicyVersion {
		// A roster the previous CLI wrote has no version key but spends and
		// routes; the error must say how to convert it, not only what is wrong.
		if legacyRoster(root) {
			return failAt(p.Path, index, "", "version", "the roster is in the previous format: run `docflow roster migrate` to convert it to version %d", PolicyVersion)
		}
		return failAt(p.Path, index, "", "version", "version must be %d", PolicyVersion)
	}
	p.Version = int(version)
	for _, key := range index.keysByLine("", root) {
		switch key {
		case "version", "access", "model", "role", "effort", "preset":
		default:
			return failAt(p.Path, index, "", key, "unknown key '%s'", key)
		}
	}
	if err := p.parseAccesses(index, root["access"]); err != nil {
		return err
	}
	if err := p.parseModels(index, root["model"]); err != nil {
		return err
	}
	if err := p.parseRoles(index, root["role"]); err != nil {
		return err
	}
	if err := p.parseEfforts(index, root["effort"]); err != nil {
		return err
	}
	return p.parsePresets(index, root["preset"])
}

func (p *Policy) parseAccesses(index *lineIndex, value any) error {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return failAt(p.Path, index, "", "access", "access must be a table of [access.<name>] sections")
	}
	p.index = make(map[string]int, len(table))
	for _, name := range index.keysByLine("access", table) {
		if err := checkAccessName(p.Path, index, "access", name); err != nil {
			return err
		}
		section, ok := table[name].(map[string]any)
		if !ok {
			return failAt(p.Path, index, "access", name, "access %s must be a [access.%s] table", name, name)
		}
		access, err := p.parseAccess(index, name, section)
		if err != nil {
			return err
		}
		p.index[name] = len(p.Accesses)
		p.Accesses = append(p.Accesses, access)
	}
	return nil
}

func (p *Policy) parseAccess(index *lineIndex, name string, section map[string]any) (Access, error) {
	table := "access." + name
	access := Access{Name: name, Budget: DefaultBudget}
	hasAgents := false
	for _, key := range index.keysByLine(table, section) {
		value := section[key]
		switch key {
		case "agents":
			items, ok := value.([]any)
			if !ok {
				return access, failAt(p.Path, index, table, key, "access %s: agents must be an array of %s", name, orList(PolicyAgents))
			}
			list, err := nameList(items)
			if err != nil {
				return access, failAt(p.Path, index, table, key, "access %s: agents: %v", name, err)
			}
			for _, agent := range list {
				if !contains(PolicyAgents, agent) {
					return access, failAt(p.Path, index, table, key, "access %s: unknown agent '%s' (known: %s)", name, agent, strings.Join(PolicyAgents, ", "))
				}
			}
			access.Agents = list
			hasAgents = true
		case "kind":
			kind, ok := value.(string)
			if !ok || !contains(Kinds, kind) {
				return access, failAt(p.Path, index, table, key, "access %s: kind must be %s", name, orList(Kinds))
			}
			access.Kind = kind
		case "budget":
			budget, ok := value.(string)
			if !ok || !contains(Budgets, budget) {
				return access, failAt(p.Path, index, table, key, "access %s: budget must be %s", name, orList(Budgets))
			}
			access.Budget = budget
		case "trusted", "check":
			flag, ok := value.(bool)
			if !ok {
				return access, failAt(p.Path, index, table, key, "access %s: %s must be a boolean", name, key)
			}
			if key == "trusted" {
				access.Trusted = flag
			} else {
				access.Check = flag
			}
		case "models", "exclude":
			globs, err := globList(value)
			if err != nil {
				return access, failAt(p.Path, index, table, key, "access %s: %s: %v", name, key, err)
			}
			if key == "models" {
				access.Models = globs
			} else {
				access.Exclude = globs
			}
		case "expires":
			text, ok := value.(string)
			if !ok {
				return access, failAt(p.Path, index, table, key, "access %s: expires must be a date YYYY-MM-DD", name)
			}
			day, err := parseDay(text)
			if err != nil {
				return access, failAt(p.Path, index, table, key, "access %s: expires: %v", name, err)
			}
			access.Expires = day
		default:
			return access, failAt(p.Path, index, table, key, "unknown key '%s' in [access.%s]", key, name)
		}
	}
	if !hasAgents {
		return access, failAt(p.Path, index, table, "", "access %s: agents is required", name)
	}
	if len(access.Agents) == 0 {
		return access, failAt(p.Path, index, table, "agents", "access %s: agents must not be empty", name)
	}
	if access.Check && access.Trusted {
		return access, failAt(p.Path, index, table, "trusted", "access %s: check and trusted are mutually exclusive", name)
	}
	if access.Budget == BudgetMinimal && !access.Trusted {
		return access, failAt(p.Path, index, table, "budget", "access %s: budget minimal requires trusted = true, minimal serves only acceptance and replan", name)
	}
	return access, nil
}

func (p *Policy) parseModels(index *lineIndex, value any) error {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return failAt(p.Path, index, "", "model", "model must be a table of [model.\"ACCESS/MODEL\"] sections")
	}
	p.Models = make(map[string]string, len(table))
	for _, key := range index.keysByLine("model", table) {
		section, ok := table[key].(map[string]any)
		if !ok {
			return failAt(p.Path, index, "model", key, "model %s must be a [model.\"%s\"] table", key, key)
		}
		access, _, err := splitModelKey(key)
		if err != nil {
			return failAt(p.Path, index, "model", key, "model %s: %v", key, err)
		}
		if _, ok := p.Access(access); !ok {
			return failAt(p.Path, index, "model", key, "model %s: unknown access '%s'", key, access)
		}
		tablePath := "model." + key
		class := ""
		for _, field := range index.keysByLine(tablePath, section) {
			if field != "class" {
				return failAt(p.Path, index, tablePath, field, "unknown key '%s' in [model.\"%s\"]", field, key)
			}
			value, ok := section[field].(string)
			if !ok || !contains(tiers, value) {
				return failAt(p.Path, index, tablePath, field, "model %s: class must be %s", key, orList(tiers))
			}
			class = value
		}
		if class == "" {
			return failAt(p.Path, index, tablePath, "", "model %s: class is required", key)
		}
		p.Models[key] = class
	}
	return nil
}

func (p *Policy) parseRoles(index *lineIndex, value any) error {
	table, ok := value.(map[string]any)
	if value == nil {
		return failAt(p.Path, index, "role", "", "[role] is required: %s", strings.Join(Roles, ", "))
	}
	if !ok {
		return failAt(p.Path, index, "", "role", "role must be a table of [role] requirements")
	}
	p.Roles = make(map[string]string, len(Roles))
	for _, role := range index.keysByLine("role", table) {
		if !contains(Roles, role) {
			return failAt(p.Path, index, "role", role, "unknown role '%s' (known: %s)", role, strings.Join(Roles, ", "))
		}
		requirement, ok := table[role].(string)
		if !ok || !contains(tierRequirements, requirement) {
			return failAt(p.Path, index, "role", role, "role %s: requirement must be %s", role, orList(tierRequirements))
		}
		p.Roles[role] = requirement
	}
	for _, role := range Roles {
		if _, ok := p.Roles[role]; !ok {
			return failAt(p.Path, index, "role", "", "role %s: no requirement in [role]", role)
		}
	}
	return nil
}

func (p *Policy) parseEfforts(index *lineIndex, value any) error {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return failAt(p.Path, index, "", "effort", "effort must be a table of [effort.\"ACCESS/MODEL\"] levels")
	}
	p.Efforts = make(map[string]string, len(table))
	for _, key := range index.keysByLine("effort", table) {
		access, _, err := splitModelKey(key)
		if err != nil {
			return failAt(p.Path, index, "effort", key, "effort %s: %v", key, err)
		}
		if _, ok := p.Access(access); !ok {
			return failAt(p.Path, index, "effort", key, "effort %s: unknown access '%s'", key, access)
		}
		level, ok := table[key].(string)
		if !ok || level == "" || strings.ContainsAny(level, " \t|") {
			return failAt(p.Path, index, "effort", key, "effort %s: level must be a single word", key)
		}
		p.Efforts[key] = level
	}
	return nil
}

func (p *Policy) parsePresets(index *lineIndex, value any) error {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return failAt(p.Path, index, "", "preset", "preset must be a table of [preset.<name>] sections")
	}
	for _, name := range index.keysByLine("preset", table) {
		section, ok := table[name].(map[string]any)
		if !ok {
			return failAt(p.Path, index, "preset", name, "preset %s must be a [preset.%s] table", name, name)
		}
		preset, err := p.parsePreset(index, name, section)
		if err != nil {
			return err
		}
		p.Presets = append(p.Presets, preset)
	}
	return nil
}

func (p *Policy) parsePreset(index *lineIndex, name string, section map[string]any) (Preset, error) {
	tablePath := "preset." + name
	preset := Preset{Name: name}
	conditions := make(map[string]string, len(p.Accesses))
	for _, key := range index.keysByLine(tablePath, section) {
		value := section[key]
		switch key {
		case "off", "lean", "minimal", "prefer", "order":
			items, ok := value.([]any)
			if !ok {
				return preset, failAt(p.Path, index, tablePath, key, "preset %s: %s must be an array of access names", name, key)
			}
			list, err := nameList(items)
			if err != nil {
				return preset, failAt(p.Path, index, tablePath, key, "preset %s: %s: %v", name, key, err)
			}
			for _, access := range list {
				if _, ok := p.Access(access); !ok {
					return preset, failAt(p.Path, index, tablePath, key, "preset %s: %s: unknown access '%s' (known: %s)", name, key, access, strings.Join(p.AccessNames(), ", "))
				}
			}
			switch key {
			case "off":
				preset.Off = list
			case "lean":
				preset.Lean = list
			case "minimal":
				preset.Minimal = list
			case "prefer":
				preset.Prefer = list
			case "order":
				preset.Order = list
			}
			if key == "order" {
				continue
			}
			for _, access := range list {
				if previous, repeated := conditions[access]; repeated {
					return preset, failAt(p.Path, index, tablePath, key, "preset %s: access '%s' is listed in both %s and %s", name, access, previous, key)
				}
				conditions[access] = key
			}
			if key == "minimal" {
				for _, access := range list {
					if known, ok := p.Access(access); ok && !known.Trusted {
						return preset, failAt(p.Path, index, tablePath, key, "preset %s: minimal access '%s' is not trusted", name, access)
					}
				}
			}
		case "models":
			models, ok := value.(map[string]any)
			if !ok {
				return preset, failAt(p.Path, index, tablePath, key, "preset %s: models must be a table of access = [model globs]", name)
			}
			modelsPath := tablePath + ".models"
			preset.Models = make(map[string][]string, len(models))
			for _, access := range index.keysByLine(modelsPath, models) {
				line := index.line(modelsPath, access)
				if line == 0 {
					line = index.line(tablePath, "models")
				}
				if _, ok := p.Access(access); !ok {
					return preset, failAtLine(p.Path, line, "preset %s: models: unknown access '%s'", name, access)
				}
				globs, err := globList(models[access])
				if err != nil {
					return preset, failAtLine(p.Path, line, "preset %s: models.%s: %v", name, access, err)
				}
				preset.Models[access] = globs
			}
		default:
			return preset, failAt(p.Path, index, tablePath, key, "unknown key '%s' in [preset.%s]", key, name)
		}
	}
	return preset, nil
}

// legacyRoster reports whether the parsed document has a top-level key
// only the v1 roster has: spend, provider, route or profile. The v1 file
// has no version key, so its absence alone does not tell a broken v2 file
// from an old roster; these tables do.
func legacyRoster(root map[string]any) bool {
	for _, key := range []string{"spend", "provider", "route", "profile"} {
		if _, ok := root[key]; ok {
			return true
		}
	}
	return false
}

// failAtLine is failAt with a known line; a line of 0 becomes 1, the
// file head, for syntax the line scan cannot see (inline tables).
func failAtLine(path string, line int, format string, args ...any) error {
	if line == 0 {
		line = 1
	}
	return clierrors.FormatError{Path: path, Line: line, Text: fmt.Sprintf(format, args...)}
}

// repeatedTable finds a named table header that appears twice. The TOML
// parser rejects it with a bare line number, while a policy error must
// name the file and the line; only keyed tables are checked, [role] and
// [effort] have a single header each.
func repeatedTable(text string) (string, int, bool) {
	seen := make(map[string]bool)
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "[") || strings.HasPrefix(line, "[[") {
			continue
		}
		end := strings.Index(line, "]")
		if end < 0 {
			continue
		}
		segments := headerSegments(line[1:end])
		if len(segments) < 2 {
			continue
		}
		switch segments[0] {
		case "access", "model", "preset":
		default:
			continue
		}
		name := strings.Join(segments, ".")
		if seen[name] {
			return name, n + 1, true
		}
		seen[name] = true
	}
	return "", 0, false
}

// splitModelKey splits the catalog-style key "ACCESS/MODEL": the access
// comes before the first slash, the model may itself contain slashes.
func splitModelKey(key string) (string, string, error) {
	access, model, found := strings.Cut(key, "/")
	switch {
	case !found || access == "" || model == "":
		return "", "", fmt.Errorf("key must be ACCESS/MODEL")
	case strings.ContainsAny(key, " \t|"):
		return "", "", fmt.Errorf("key must not contain whitespace or '|'")
	}
	return access, model, nil
}

// nameList reads an array of names: non-empty, without whitespace, and
// without repeats, so an access never appears twice in one list.
func nameList(items []any) ([]string, error) {
	list := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for n, item := range items {
		name, ok := item.(string)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("item %d must be a name", n+1)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate '%s'", name)
		}
		seen[name] = true
		list = append(list, name)
	}
	return list, nil
}

// globList reads an array of model globs and checks their syntax, so a
// broken pattern fails at load time instead of silently matching nothing.
func globList(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of model globs")
	}
	globs := make([]string, 0, len(items))
	for n, item := range items {
		glob, ok := item.(string)
		if !ok || glob == "" {
			return nil, fmt.Errorf("item %d must be a model glob", n+1)
		}
		if _, err := path.Match(glob, ""); err != nil {
			return nil, fmt.Errorf("bad glob '%s': %v", glob, err)
		}
		globs = append(globs, glob)
	}
	return globs, nil
}

// parseDay reads a policy date: exactly YYYY-MM-DD in UTC. The shape
// check keeps the format exact; time.Parse then rejects out-of-range days
// like 2026-02-30.
func parseDay(text string) (time.Time, error) {
	invalid := fmt.Errorf("date must be YYYY-MM-DD")
	if len(text) != len("2006-01-02") || text[4] != '-' || text[7] != '-' {
		return time.Time{}, invalid
	}
	for _, i := range []int{0, 1, 2, 3, 5, 6, 8, 9} {
		if text[i] < '0' || text[i] > '9' {
			return time.Time{}, invalid
		}
	}
	day, err := time.Parse("2006-01-02", text)
	if err != nil {
		return time.Time{}, invalid
	}
	return day, nil
}

// orList renders an enumeration for an error message: "a, b or c".
func orList(values []string) string {
	switch len(values) {
	case 0:
		return "-"
	case 1:
		return values[0]
	}
	return strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
}
