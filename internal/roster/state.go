package roster

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bsv-docflow/internal/agents"
	"bsv-docflow/internal/clierrors"
)

// StateVersion is the format marker of roster.state.toml: a top-level
// version = 2. The state is the temporary layer of the roster and never
// replaces the policy file.
const StateVersion = 2

// Condition sources: the stable policy file and the temporary state file.
const (
	SourcePolicy = "roster"
	SourceState  = "state"
)

// State is the condition layer of the roster (roster.state.toml) merged
// over the policy: the accesses switched off by hand and the day their
// off condition ends, the temporary expiry dates and the preset to apply.
// `roster off`, `on`, `expire` and `reset` write only this file; a
// missing state file means no conditions.
type State struct {
	Path    string
	Version int
	Off     []string             // accesses switched off by hand
	Until   map[string]time.Time // access → the last day it stays off
	Expires map[string]time.Time // access → the last day it is usable
	Preset  string               // preset merged over the policy
}

// Condition is one active condition of the roster with its source: an
// expiry from the policy or the state, an off switch, or the preset.
type Condition struct {
	Access string `json:"access"`
	Kind   string `json:"kind"`
	Value  string `json:"value,omitempty"`
	Source string `json:"source"`
}

// ConditionsReport is the JSON of the active conditions: the state file
// the conditions were read from and one row per condition.
type ConditionsReport struct {
	State      string      `json:"state"`
	Conditions []Condition `json:"conditions"`
}

// PolicyPath resolves the roster path: "" means DefaultPath and a leading
// ~ is expanded, so the caller can stat or derive paths from it.
func PolicyPath(rosterPath string) string {
	if rosterPath == "" {
		return DefaultPath()
	}
	return expandUser(rosterPath)
}

// StatePath returns the condition state path of a roster: the same
// directory and name with .toml replaced by .state.toml, so
// ~/.config/docflow/roster.toml has ~/.config/docflow/roster.state.toml
// next to it. "" means the default roster.
func StatePath(rosterPath string) string {
	policy := PolicyPath(rosterPath)
	if base, found := strings.CutSuffix(policy, ".toml"); found {
		return base + ".state.toml"
	}
	return policy + ".state.toml"
}

// DefaultStatePath returns the state path of the default roster.
func DefaultStatePath() string { return StatePath(DefaultPath()) }

// ParseDay reads a date a condition command carries: exactly YYYY-MM-DD
// in UTC. The command line reaches it with its own words, so the error is
// an exit-code-2 message instead of a located format error.
func ParseDay(text string) (time.Time, error) {
	day, err := parseDay(text)
	if err != nil {
		return time.Time{}, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return day, nil
}

// NewState returns an empty state with non-nil maps, so a mutation never
// writes into a nil map.
func NewState(path string) *State {
	return &State{
		Path:    path,
		Version: StateVersion,
		Off:     []string{},
		Until:   map[string]time.Time{},
		Expires: map[string]time.Time{},
	}
}

// LoadState reads the state at path ("" means DefaultStatePath). A
// missing file is an empty state, not an error: it is the normal state of
// a roster without conditions. An off condition whose until date has
// passed is dropped on the way in, so an expired switch never comes back;
// the file keeps it until the next mutation rewrites it.
func LoadState(path string) (*State, error) {
	if path == "" {
		path = DefaultStatePath()
	}
	path = expandUser(path)
	text, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		state := NewState(path)
		state.clean(time.Now().UTC())
		return state, nil
	}
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster state: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	return ParseState(path, string(text))
}

// ParseState parses and validates state text; path only locates the
// errors. A state problem is an exit-code-2 error naming file and line.
func ParseState(path, text string) (*State, error) {
	if name, line, duplicated := repeatedTable(text); duplicated {
		return nil, clierrors.FormatError{Path: path, Line: line, Text: fmt.Sprintf("table [%s] is defined twice", name)}
	}
	root, err := agents.ParseTOML(text)
	if err != nil {
		return nil, clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: %v", path, err), Code: clierrors.EXIT_FORMAT}
	}
	state := NewState(path)
	if err := state.parse(scanLines(text), root); err != nil {
		return nil, err
	}
	state.clean(time.Now().UTC())
	return state, nil
}

func (s *State) parse(index *lineIndex, root map[string]any) error {
	version, ok := root["version"].(int64)
	if !ok || version != StateVersion {
		return failAt(s.Path, index, "", "version", "version must be %d", StateVersion)
	}
	s.Version = int(version)
	for _, key := range index.keysByLine("", root) {
		switch key {
		case "version", "off", "preset", "until", "expires":
		case "budget", "models", "exclude":
			return failAt(s.Path, index, "", key, "the '%s' condition was removed with roster version %d: %s, or `docflow roster reset`", key, PolicyVersion, migrateHint)
		default:
			return failAt(s.Path, index, "", key, "unknown key '%s'", key)
		}
	}
	if value, ok := root["preset"]; ok {
		name, ok := value.(string)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return failAt(s.Path, index, "", "preset", "preset must be a single preset name")
		}
		s.Preset = name
	}
	if value, ok := root["off"]; ok {
		items, ok := value.([]any)
		if !ok {
			return failAt(s.Path, index, "", "off", "off must be an array of access names")
		}
		list, err := nameList(items)
		if err != nil {
			return failAt(s.Path, index, "", "off", "off: %v", err)
		}
		s.Off = list
	}
	if err := s.parseDates(index, "until", root["until"], s.Until); err != nil {
		return err
	}
	return s.parseDates(index, "expires", root["expires"], s.Expires)
}

func (s *State) parseDates(index *lineIndex, key string, value any, target map[string]time.Time) error {
	table, ok := value.(map[string]any)
	if value != nil && !ok {
		return failAt(s.Path, index, "", key, "%s must be a table of access = date", key)
	}
	for _, access := range index.keysByLine(key, table) {
		if err := checkAccessName(s.Path, index, key, access); err != nil {
			return err
		}
		text, ok := table[access].(string)
		if !ok {
			return failAt(s.Path, index, key, access, "%s.%s: date must be YYYY-MM-DD", key, access)
		}
		day, err := parseDay(text)
		if err != nil {
			return failAt(s.Path, index, key, access, "%s.%s: %v", key, access, err)
		}
		target[access] = day
	}
	return nil
}

// clean drops the conditions that are over: an off condition past its
// until date and a until date without an access still off. The off
// condition holds through the named day, so the switch is gone the day
// after it.
func (s *State) clean(today time.Time) {
	if s.Off == nil {
		s.Off = []string{}
	}
	kept := make([]string, 0, len(s.Off))
	for _, access := range s.Off {
		until, dated := s.Until[access]
		if dated && today.After(until) {
			delete(s.Until, access)
			continue
		}
		kept = append(kept, access)
	}
	s.Off = kept
	for access := range s.Until {
		if !contains(s.Off, access) {
			delete(s.Until, access)
		}
	}
}

// Validate checks the state against the policy it will be merged over:
// every named access must exist and the preset must be declared. An
// unknown name is an error, not a silently ignored condition: a typo in a
// dead file must fail instead of doing nothing.
func (s *State) Validate(policy *Policy) error {
	for _, access := range s.Off {
		if _, ok := policy.Access(access); !ok {
			return s.unknownAccess(policy, "off", access)
		}
	}
	for _, access := range sortedKeys(s.Until) {
		if _, ok := policy.Access(access); !ok {
			return s.unknownAccess(policy, "until", access)
		}
	}
	for _, access := range sortedKeys(s.Expires) {
		if _, ok := policy.Access(access); !ok {
			return s.unknownAccess(policy, "expires", access)
		}
	}
	if s.Preset != "" {
		if _, ok := policy.Preset(s.Preset); !ok {
			return clierrors.DocflowError{Message: fmt.Sprintf("docflow: %s: preset: unknown preset '%s' (known: %s)", s.Path, s.Preset, orList(policy.PresetNames())), Code: clierrors.EXIT_FORMAT}
		}
	}
	return nil
}

func (s *State) unknownAccess(policy *Policy, condition, access string) error {
	return clierrors.DocflowError{
		Message: fmt.Sprintf("docflow: %s: %s: unknown access '%s' (known: %s)", s.Path, condition, access, orList(policy.AccessNames())),
		Code:    clierrors.EXIT_FORMAT,
	}
}

// SetOff switches an access off; a non-zero until bounds the condition to
// the named day, a zero one leaves it open-ended. The empty bound drops a
// previous date, so `off` without --until means "off until further
// notice".
func (s *State) SetOff(access string, until time.Time) {
	s.Off = addUnique(s.Off, access)
	if until.IsZero() {
		delete(s.Until, access)
		return
	}
	s.Until[access] = until
}

// SetOn lifts the conditions that switch an access off: the off entry
// and its until date.
func (s *State) SetOn(access string) {
	s.Off = removeItem(s.Off, access)
	delete(s.Until, access)
}

// SetExpires bounds an access to the named day: it is usable through that
// day and gone after it, until the condition is reset.
func (s *State) SetExpires(access string, day time.Time) {
	s.Expires[access] = day
}

// Reset drops every condition; the caller deletes the file.
func (s *State) Reset() {
	s.Off = []string{}
	s.Until = map[string]time.Time{}
	s.Expires = map[string]time.Time{}
	s.Preset = ""
}

// clone returns a copy of the state: a mutation runs on the copy, so a
// condition refused by Validate never leaves the loaded state half-changed.
func (s *State) clone() *State {
	copied := *s
	copied.Off = append([]string(nil), s.Off...)
	copied.Until = maps.Clone(s.Until)
	copied.Expires = maps.Clone(s.Expires)
	if copied.Off == nil {
		copied.Off = []string{}
	}
	return &copied
}

// Save writes the state to path ("" means the path it was loaded from,
// then DefaultStatePath) atomically: a temporary file next to it is
// renamed over the target, so a reader never sees a half-written state.
// Keys are sorted, which keeps the file stable between mutations.
func (s *State) Save(path string) error {
	if path == "" {
		path = s.Path
	}
	if path == "" {
		path = DefaultStatePath()
	}
	path = expandUser(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster state: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(s.text()), 0o644); err != nil {
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster state: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return clierrors.DocflowError{Message: fmt.Sprintf("docflow: roster state: %v", err), Code: clierrors.EXIT_FORMAT}
	}
	s.Path = path
	return nil
}

// text renders the state as TOML: the version, the preset, the off list
// and one table per condition kind, empty tables dropped.
func (s *State) text() string {
	var text strings.Builder
	text.WriteString("# roster conditions: the temporary state layer of roster.toml.\n")
	text.WriteString("# Written by docflow roster off|on|expire; drop the file to reset it.\n")
	fmt.Fprintf(&text, "version = %d\n", StateVersion)
	if s.Preset != "" {
		fmt.Fprintf(&text, "\npreset = %s\n", quote(s.Preset))
	}
	if len(s.Off) > 0 {
		quoted := make([]string, 0, len(s.Off))
		for _, access := range s.Off {
			quoted = append(quoted, quote(access))
		}
		fmt.Fprintf(&text, "\noff = [%s]\n", strings.Join(quoted, ", "))
	}
	writeDates(&text, "until", s.Until)
	writeDates(&text, "expires", s.Expires)
	return text.String()
}

func writeDates(text *strings.Builder, key string, dates map[string]time.Time) {
	if len(dates) == 0 {
		return
	}
	fmt.Fprintf(text, "\n[%s]\n", key)
	for _, access := range sortedKeys(dates) {
		fmt.Fprintf(text, "%s = %s\n", tomlKey(access), quote(dayText(dates[access])))
	}
}

// tomlKey renders an access name as a table key: bare when the TOML
// bare-key alphabet allows it, quoted otherwise. A dot is never bare: it
// would turn the key into a nested table path the next load cannot match.
func tomlKey(name string) string {
	if name == "" {
		return quote(name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return quote(name)
		}
	}
	return name
}

// quote renders a TOML basic string. Access names and globs come from the
// command line, so escaping keeps a name with a quote or a backslash from
// breaking the file the next load cannot parse.
func quote(value string) string {
	var text strings.Builder
	text.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			text.WriteString(`\"`)
		case '\\':
			text.WriteString(`\\`)
		case '\n':
			text.WriteString(`\n`)
		case '\t':
			text.WriteString(`\t`)
		default:
			text.WriteRune(r)
		}
	}
	text.WriteByte('"')
	return text.String()
}

// Merge returns a copy of the policy with the state conditions applied
// over it: the preset first, then the expiries and the off switches. The
// policy itself is never modified, so a screen can compare the stable
// layer with what the conditions do to it.
func Merge(policy *Policy, state *State) (*Policy, error) {
	if state == nil {
		return policy.clone(), nil
	}
	if err := state.Validate(policy); err != nil {
		return nil, err
	}
	return mergeAt(policy.clone(), state, time.Now().UTC()), nil
}

// mergeAt applies the state to an already copied policy at the given day,
// so a test can pin the clock the expiry conditions read.
func mergeAt(merged *Policy, state *State, today time.Time) *Policy {
	if state.Preset != "" {
		if preset, ok := merged.Preset(state.Preset); ok {
			applyPreset(merged, preset)
		}
	}
	for access, day := range state.Expires {
		known, ok := merged.Access(access)
		if !ok {
			continue
		}
		if known.Expires.IsZero() || day.Before(known.Expires) {
			known.Expires = day
		}
	}
	for _, access := range state.Off {
		if until, dated := state.Until[access]; dated && today.After(until) {
			continue
		}
		if known, ok := merged.Access(access); ok {
			known.Off = true
		}
	}
	return merged
}

// applyPreset applies a preset to a policy: its off list switches the
// accesses off, its cells replace the base cells (the empty cell clears
// one), and its order puts the listed accesses first.
func applyPreset(policy *Policy, preset *Preset) {
	for _, name := range preset.Off {
		if known, ok := policy.Access(name); ok {
			known.Off = true
		}
	}
	for name, cells := range preset.Roles {
		known, ok := policy.Access(name)
		if !ok {
			continue
		}
		for role, cell := range cells {
			if cell.Empty() {
				delete(known.Roles, role)
				continue
			}
			if known.Roles == nil {
				known.Roles = map[string]Cell{}
			}
			known.Roles[role] = cell
		}
	}
	if len(preset.Order) > 0 {
		policy.reorder(preset.Order)
	}
}

// reorder puts the named accesses first in the given order and keeps the
// rest in policy file order, then rebuilds the name index.
func (p *Policy) reorder(order []string) {
	ordered := make([]Access, 0, len(p.Accesses))
	placed := make(map[string]bool, len(order))
	for _, name := range order {
		access, ok := p.Access(name)
		if !ok || placed[name] {
			continue
		}
		placed[name] = true
		ordered = append(ordered, *access)
	}
	for _, access := range p.Accesses {
		if !placed[access.Name] {
			ordered = append(ordered, access)
		}
	}
	p.Accesses = ordered
	p.reindex()
}

// clone returns a deep copy of the policy: the resolver mutates the
// accesses of the merged copy, and the loaded policy stays untouched.
func (p *Policy) clone() *Policy {
	copied := &Policy{Path: p.Path, Version: p.Version}
	for _, preset := range p.Presets {
		copied.Presets = append(copied.Presets, preset.clone())
	}
	for _, access := range p.Accesses {
		copied.Accesses = append(copied.Accesses, access.clone())
	}
	copied.reindex()
	return copied
}

// clone returns a deep copy of an access.
func (a Access) clone() Access {
	copied := a
	copied.Agents = append([]string(nil), a.Agents...)
	if a.Roles != nil {
		copied.Roles = maps.Clone(a.Roles)
	}
	return copied
}

// clone returns a deep copy of a preset, so editing a variant never writes
// through to the layer it was copied from.
func (p Preset) clone() Preset {
	copied := p
	copied.Off = append([]string(nil), p.Off...)
	copied.Order = append([]string(nil), p.Order...)
	if p.Roles != nil {
		copied.Roles = make(map[string]map[string]Cell, len(p.Roles))
		for access, cells := range p.Roles {
			copied.Roles[access] = maps.Clone(cells)
		}
	}
	return copied
}

// Conditions lists the active conditions with their source: the policy
// conditions in access file order, then the state conditions sorted by
// access, so the listing is stable between runs.
func Conditions(policy *Policy, state *State) []Condition {
	rows := make([]Condition, 0, len(policy.Accesses))
	for _, access := range policy.Accesses {
		if !access.Expires.IsZero() {
			rows = append(rows, Condition{Access: access.Name, Kind: "expires", Value: dayText(access.Expires), Source: SourcePolicy})
		}
	}
	if state == nil {
		return rows
	}
	if state.Preset != "" {
		rows = append(rows, Condition{Access: "-", Kind: "preset", Value: state.Preset, Source: SourceState})
	}
	for _, access := range state.Off {
		value := ""
		if until, dated := state.Until[access]; dated {
			value = "until " + dayText(until)
		}
		rows = append(rows, Condition{Access: access, Kind: "off", Value: value, Source: SourceState})
	}
	for _, access := range sortedKeys(state.Expires) {
		rows = append(rows, Condition{Access: access, Kind: "expires", Value: dayText(state.Expires[access]), Source: SourceState})
	}
	return rows
}

// ConditionTable renders the conditions as a table; no conditions render
// as the single line "conditions: none".
func ConditionTable(conditions []Condition) string {
	if len(conditions) == 0 {
		return "conditions: none\n"
	}
	rows := [][]string{{"access", "condition", "value", "source"}}
	for _, condition := range conditions {
		rows = append(rows, []string{condition.Access, condition.Kind, orDashText(condition.Value), condition.Source})
	}
	return tableText(rows)
}

// NewConditionsReport collects the JSON of the active conditions.
func NewConditionsReport(policy *Policy, state *State) ConditionsReport {
	report := ConditionsReport{Conditions: Conditions(policy, state)}
	if state != nil {
		report.State = state.Path
	}
	return report
}

// dayText renders a policy or state date: exactly the day in UTC.
func dayText(day time.Time) string { return day.UTC().Format("2006-01-02") }

// checkAccessName rejects a name the CLI flags and the ACCESS/MODEL keys
// cannot address.
func checkAccessName(path string, index *lineIndex, table, name string) error {
	if problem := accessNameProblem(name); problem != "" {
		return failAt(path, index, table, name, "access name '%s' %s", name, problem)
	}
	return nil
}

// accessNameProblem says why a name cannot address an access, or returns
// an empty string. The migration shares the rule with the policy and the
// state: a name derived from a v1 route must load as a v2 access.
func accessNameProblem(name string) string {
	switch {
	case name == "":
		return "must not be empty"
	case strings.ContainsAny(name, " \t/|,="):
		return "must not contain whitespace, '/', '|', ',' or '='"
	}
	return ""
}

func addUnique[V comparable](list []V, value V) []V {
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}

func removeItem(list []string, value string) []string {
	kept := make([]string, 0, len(list))
	for _, item := range list {
		if item != value {
			kept = append(kept, item)
		}
	}
	return kept
}
