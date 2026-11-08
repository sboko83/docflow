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

// PolicyVersion is the format marker of a roster policy: a top-level
// version = 4. Version 4 replaced the indirection of version 3 — model
// classes in [route], class requirements in [role], access budgets and
// the trusted flag — with an explicit table: every access names the model
// and effort it serves each role with. Older rosters are converted by
// `docflow roster migrate`, and Load never guesses.
const PolicyVersion = 4

var (
	// Kinds lists the access kinds: a subscription is a quota, a metered
	// access is billed per token, a local access runs on this machine.
	Kinds = []string{"subscription", "metered", "local"}
	// PolicyAgents lists the agents an access may be reached through.
	PolicyAgents = []string{"claude", "codex", "pi", "opencode"}
)

// Cell is one cell of the access × role table: the model an access serves
// a role with and the effort passed to the agent. An empty Effort passes
// none. The zero Cell is an empty cell: in a preset it clears the cell of
// the base table.
type Cell struct {
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

// String renders the cell as the policy writes it: MODEL@EFFORT, or the
// bare model without an effort.
func (c Cell) String() string {
	if c.Effort == "" {
		return c.Model
	}
	return c.Model + "@" + c.Effort
}

// Empty reports whether the cell names no model.
func (c Cell) Empty() bool { return c.Model == "" }

// ParseCell reads MODEL@EFFORT or a bare MODEL; the effort follows the last
// '@'. The empty text is the empty cell.
func ParseCell(text string) (Cell, error) {
	if text == "" {
		return Cell{}, nil
	}
	if strings.ContainsAny(text, " \t|") {
		return Cell{}, fmt.Errorf("'%s' must be MODEL or MODEL@EFFORT without whitespace or '|'", text)
	}
	model, effort := text, ""
	if at := strings.LastIndex(text, "@"); at >= 0 {
		model, effort = text[:at], text[at+1:]
		if effort == "" {
			return Cell{}, fmt.Errorf("'%s': the effort after '@' is empty", text)
		}
	}
	if model == "" {
		return Cell{}, fmt.Errorf("'%s': the model is empty", text)
	}
	return Cell{Model: model, Effort: effort}, nil
}

// Policy is a validated v4 roster policy (roster.toml): the accesses in
// priority order, each with its row of the role table, plus the presets
// the variants files merge in. Temporary conditions live in
// roster.state.toml and the resolver merges them over the policy.
type Policy struct {
	Path     string
	Version  int
	Accesses []Access // [access.NAME] in file order: the default priority
	Presets  []Preset // the presets of the variants layers

	index map[string]int // access name → position in Accesses
}

// Access is one [access.NAME] section: the agent channels that reach it,
// its kind, the check flag, the expiry date (zero when it never expires)
// and its row of the role table. Off is never read from the file: the
// merge sets it from a preset or a condition.
type Access struct {
	Name    string
	Agents  []string
	Kind    string
	Check   bool
	Expires time.Time
	Roles   map[string]Cell // role → cell; an absent role is not served
	Off     bool
}

// Cell returns the cell of a role, or the empty cell.
func (a *Access) Cell(role string) Cell { return a.Roles[role] }

// Preset is one [preset.NAME] section of a variants file: a named policy
// over the default table. Off switches accesses off, Order puts the named
// accesses first (the rest keep the policy order), and Roles overrides
// single cells: access → role → cell, where the empty cell clears the
// base cell. Source names the variants layer that defines it.
type Preset struct {
	Name   string
	Source string
	Off    []string
	Order  []string
	Roles  map[string]map[string]Cell
}

// Access looks up an access by name.
func (p *Policy) Access(name string) (*Access, bool) {
	position, ok := p.index[name]
	if !ok {
		return nil, false
	}
	return &p.Accesses[position], true
}

// AccessNames returns the access names in priority order.
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

// PresetNames returns the preset names in layer order.
func (p *Policy) PresetNames() []string {
	names := make([]string, 0, len(p.Presets))
	for _, preset := range p.Presets {
		names = append(names, preset.Name)
	}
	return names
}

// reindex rebuilds the name index after the access list changed.
func (p *Policy) reindex() {
	p.index = make(map[string]int, len(p.Accesses))
	for i, access := range p.Accesses {
		p.index[access.Name] = i
	}
}

// LoadPolicy reads and validates the v4 policy at path ("" means
// DefaultPath) and merges the preset variants over it: the global file
// next to the policy and the project file of the current checkout.
func LoadPolicy(path string) (*Policy, error) { return loadPolicyAt(path, ProjectRoot()) }

// loadPolicyAt is LoadPolicy with the project variants layer of an
// explicit checkout root. An empty root has no project layer.
func loadPolicyAt(path, root string) (*Policy, error) {
	if path == "" {
		path = DefaultPath()
	}
	path = expandUser(path)
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster file: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	policy, err := ParsePolicy(path, string(text))
	if err != nil {
		return nil, err
	}
	return mergeVariants(policy, root)
}

// ParsePolicy parses and validates v4 policy text; path only locates the
// errors. Every policy problem is an exit-code-2 FormatError naming file
// and line.
func ParsePolicy(path, text string) (*Policy, error) {
	if name, line, duplicated := repeatedTable(text); duplicated {
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

// migrateHint is the advice of every error about a key an older format
// had: the file is converted by a command, not hand-edited.
const migrateHint = "run `docflow roster migrate` to convert the roster"

func (p *Policy) parse(index *lineIndex, root map[string]any) error {
	version, ok := root["version"].(int64)
	if !ok || version != PolicyVersion {
		if isPreviousRoster(root, version, ok) {
			return failAt(p.Path, index, "", "version", "the roster is in a previous format: %s to version %d", migrateHint, PolicyVersion)
		}
		return failAt(p.Path, index, "", "version", "version must be %d", PolicyVersion)
	}
	p.Version = int(version)
	for _, key := range index.keysByLine("", root) {
		switch key {
		case "version", "access":
		case "preset":
			return failAtLine(p.Path, index.line(key, ""), "[preset] tables live in the variants file next to roster.toml since version %d: %s", PolicyVersion, migrateHint)
		case "route", "role", "model", "effort":
			return failAtLine(p.Path, index.line(key, ""), "[%s] was replaced by the [access.NAME.roles] table in version %d: %s", key, PolicyVersion, migrateHint)
		default:
			return failAt(p.Path, index, "", key, "unknown key '%s'", key)
		}
	}
	return p.parseAccesses(index, root["access"])
}

// isPreviousRoster reports whether the document is an older roster: a v1
// roster (its own tables, usually no version key) or a v2/v3 policy.
func isPreviousRoster(root map[string]any, version int64, hasVersion bool) bool {
	if hasLegacyTables(root) {
		return true
	}
	for _, key := range []string{"model", "effort", "role"} {
		if _, ok := root[key]; ok {
			return true
		}
	}
	return hasVersion && version > 0 && version < PolicyVersion
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
	access := Access{Name: name}
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
		case "check":
			flag, ok := value.(bool)
			if !ok {
				return access, failAt(p.Path, index, table, key, "access %s: check must be a boolean", name)
			}
			access.Check = flag
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
		case "roles":
			cells, err := parseRoleCells(p.Path, index, table+".roles", value, false)
			if err != nil {
				return access, err
			}
			access.Roles = cells
		case "budget", "trusted", "models", "exclude":
			return access, failAt(p.Path, index, table, key, "access %s: '%s' was removed in version %d, the [access.%s.roles] table names the models: %s", name, key, PolicyVersion, name, migrateHint)
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
	if err := checkJudgeCells(p.Path, index, table+".roles", name, access.Check, access.Roles); err != nil {
		return access, err
	}
	return access, nil
}

// parseRoleCells reads a roles table: role = "MODEL@EFFORT". A preset may
// write the empty string to clear a cell (clear = true); the base table
// leaves a role out instead.
func parseRoleCells(path string, index *lineIndex, table string, value any, clear bool) (map[string]Cell, error) {
	section, ok := value.(map[string]any)
	if !ok {
		return nil, failAt(path, index, table, "", "[%s] must be a table of role = \"MODEL@EFFORT\"", table)
	}
	cells := make(map[string]Cell, len(section))
	for _, role := range index.keysByLine(table, section) {
		if !contains(Roles, role) {
			return nil, failAt(path, index, table, role, "unknown role '%s' (known: %s)", role, strings.Join(Roles, ", "))
		}
		text, ok := section[role].(string)
		if !ok {
			return nil, failAt(path, index, table, role, "%s.%s must be a string MODEL@EFFORT", table, role)
		}
		if text == "" && !clear {
			return nil, failAt(path, index, table, role, "%s.%s is empty: leave the role out instead", table, role)
		}
		cell, err := ParseCell(text)
		if err != nil {
			return nil, failAt(path, index, table, role, "%s.%s: %v", table, role, err)
		}
		cells[role] = cell
	}
	return cells, nil
}

// checkJudgeCells refuses a judge cell on a check access: an access that
// needs an independent review never reviews, accepts or replans itself.
func checkJudgeCells(path string, index *lineIndex, table, name string, check bool, cells map[string]Cell) error {
	if !check {
		return nil
	}
	for _, role := range Roles {
		if cell, ok := cells[role]; ok && !cell.Empty() && judgeRole(role) {
			return failAt(path, index, table, role, "access %s: a check access never judges, so it serves no %s role", name, role)
		}
	}
	return nil
}

// judgeRole reports whether a role reviews, accepts or replans: the three
// roles a check access is excluded from.
func judgeRole(role string) bool {
	switch role {
	case "review", "acceptance", "replan":
		return true
	}
	return false
}

// parsePresetSection reads one [preset.NAME] section of a variants file.
// A nil policy parses the grammar alone, without resolving access names:
// SaveVariants re-reads a file it wrote and has no policy to resolve
// against.
func parsePresetSection(policy *Policy, path string, index *lineIndex, name string, section map[string]any) (Preset, error) {
	tablePath := "preset." + name
	preset := Preset{Name: name}
	known := func(key, access string) error {
		if policy == nil {
			return nil
		}
		if _, ok := policy.Access(access); !ok {
			return failAt(path, index, tablePath, key, "preset %s: %s: unknown access '%s' (known: %s)", name, key, access, strings.Join(policy.AccessNames(), ", "))
		}
		return nil
	}
	for _, key := range index.keysByLine(tablePath, section) {
		value := section[key]
		switch key {
		case "off", "order":
			items, ok := value.([]any)
			if !ok {
				return preset, failAt(path, index, tablePath, key, "preset %s: %s must be an array of access names", name, key)
			}
			list, err := nameList(items)
			if err != nil {
				return preset, failAt(path, index, tablePath, key, "preset %s: %s: %v", name, key, err)
			}
			for _, access := range list {
				if err := known(key, access); err != nil {
					return preset, err
				}
			}
			if key == "off" {
				preset.Off = list
			} else {
				preset.Order = list
			}
		case "roles":
			accesses, ok := value.(map[string]any)
			if !ok {
				return preset, failAt(path, index, tablePath, key, "preset %s: roles must hold [preset.%s.roles.ACCESS] tables", name, name)
			}
			preset.Roles = make(map[string]map[string]Cell, len(accesses))
			for _, access := range index.keysByLine(tablePath+".roles", accesses) {
				if err := known("roles", access); err != nil {
					return preset, err
				}
				cells, err := parseRoleCells(path, index, tablePath+".roles."+access, accesses[access], true)
				if err != nil {
					return preset, err
				}
				if policy != nil {
					base, _ := policy.Access(access)
					if err := checkJudgeCells(path, index, tablePath+".roles."+access, access, base.Check, cells); err != nil {
						return preset, err
					}
				}
				preset.Roles[access] = cells
			}
		case "lean", "minimal", "prefer", "models":
			return preset, failAt(path, index, tablePath, key, "preset %s: '%s' was removed in version %d: a preset sets off, order and [preset.%s.roles.ACCESS] cells; %s", name, key, PolicyVersion, name, migrateHint)
		default:
			return preset, failAt(path, index, tablePath, key, "unknown key '%s' in [preset.%s]", key, name)
		}
	}
	return preset, nil
}

// hasLegacyTables reports whether the parsed document has a top-level key
// only the v1 roster has: spend, provider, route or profile. A v3 policy
// shares [route] with v1; legacyRosterFile excludes a declared version.
func hasLegacyTables(root map[string]any) bool {
	for _, key := range []string{"spend", "provider", "route", "profile"} {
		if _, ok := root[key]; ok {
			return true
		}
	}
	return false
}

// failAtLine is failAt with a known line; a line of 0 becomes 1.
func failAtLine(path string, line int, format string, args ...any) error {
	if line == 0 {
		line = 1
	}
	return clierrors.FormatError{Path: path, Line: line, Text: fmt.Sprintf(format, args...)}
}

// repeatedTable finds a named table header that appears twice. The TOML
// parser rejects it with a bare line number, while a policy error must
// name the file and the line.
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
		case "access", "route", "preset":
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

// globList reads an array of model globs and checks their syntax.
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

// parseDay reads a policy date: exactly YYYY-MM-DD in UTC.
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
