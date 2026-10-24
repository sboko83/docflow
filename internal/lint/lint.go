// Package lint validates docflow files, plans, archives, and their references.
package lint

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/cycle"
	"bsv-docflow/internal/format"
)

const DocflowVersion = format.DocflowVersion

var (
	docflowKeys         = []string{"docflow", "read_budget"}
	docflowOptionalKeys = []string{"start_read", "start_budget", "repos", "preflight"}
	docflowSections     = []string{"Queue", "Frozen", "Backlog"}
	docflowOptionalSecs = []string{"Frozen"}
	planKeys            = []string{"id", "title", "type", "status", "created"}
	planOptionalKeys    = []string{"gate", "executor", "permissions", "replan", "fallback", "timeout", "cheap", "heavy", "review", "acceptance"}
	planTypes           = []string{"feature", "bugfix", "perf", "module", "refactor"}
	planStatuses        = []string{"draft", "active", "frozen"}
	planPermissions     = []string{"auto", "full"}
	planSections        = []string{"Goal", "Context", "Constraints", "Prerequisites", "Traps", "Decisions"}
	archiveKeys         = []string{"id", "title", "closed"}
	archiveOptional     = []string{"superseded_by"}
	archiveSections     = []string{"Outcome", "Decisions", "Gotchas", "Follow-ups"}
	requiredFields      = []string{"Read", "Do", "Verify", "Commit"}
	executorAgents      = []string{"claude", "codex", "pi"}
	roleKeys            = []string{"cheap", "heavy", "review", "acceptance"}

	planIDRE       = regexp.MustCompile(`^P\p{Nd}{3,}$`)
	unitIDRE       = regexp.MustCompile(`^P\p{Nd}+\.\p{Nd}+\.\p{Nd}+$`)
	verifyRE       = regexp.MustCompile("^`[^`]+`$")
	modelRE        = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)
	phaseRE        = regexp.MustCompile(`^Phase \p{Nd}+: (.+)$`)
	unitHeadingRE  = regexp.MustCompile(`^### \[(.)\] (P\p{Nd}+\.\p{Nd}+\.\p{Nd}+) (.+?)\s*$`)
	itemRE         = regexp.MustCompile(`^- (.*?)\s*$`)
	continuationRE = regexp.MustCompile(`^[ \t]+\S`)
	prereqRE       = regexp.MustCompile("^(.*):\\s*`([^`]*)`(?: \\(once\\))?$")
)

var unitFields = format.UnitFields
var statuses = []string{" ", "~", "x", "-", "!"}

// Issue is one lint finding.
type Issue struct {
	Path  string
	Line  int
	Level string
	Text  string
}

func (i Issue) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", i.Path, i.Line, i.Level, i.Text)
}

// Report contains lint findings in CLI display order.
type Report struct {
	Issues []Issue
}

func (r *Report) Error(path string, line int, text string) {
	r.Issues = append(r.Issues, Issue{Path: path, Line: line, Level: "error", Text: text})
}

func (r *Report) Warning(path string, line int, text string) {
	r.Issues = append(r.Issues, Issue{Path: path, Line: line, Level: "warning", Text: text})
}

func (r *Report) Errors() []Issue {
	result := make([]Issue, 0)
	for _, issue := range r.Issues {
		if issue.Level == "error" {
			result = append(result, issue)
		}
	}
	return result
}

func versionError(doc *format.DocflowFile) (int, string, bool) {
	version := doc.Meta["docflow"]
	if version == DocflowVersion {
		return 0, "", false
	}
	line := doc.MetaLines["docflow"]
	if line == 0 {
		line = 1
	}
	found := "no version"
	if version != "" {
		found = "version " + version
	}
	return line, fmt.Sprintf("format %s, expected %s; run docflow migrate", found, DocflowVersion), true
}

// CheckVersion returns the format-version error that ordinary CLI commands
// report before doing their work.
func CheckVersion(root string) error {
	return format.CheckVersion(root)
}

func lines(doc *format.Doc) ([]string, []bool) {
	result := make([]string, len(doc.Raw))
	for i, line := range doc.Raw {
		result[i] = strings.TrimRight(line, "\r\n")
	}
	return result, format.FenceMask(result)
}

func checkMeta(rep *Report, doc *format.Doc, keys, optional []string) {
	for _, key := range keys {
		if _, ok := doc.Meta[key]; !ok {
			rep.Error(doc.Path, 1, fmt.Sprintf("missing frontmatter key '%s'", key))
		} else if doc.Meta[key] == "" {
			rep.Error(doc.Path, doc.MetaLines[key], fmt.Sprintf("empty '%s'", key))
		}
	}
	known := makeSet(keys, optional)
	ordered := make([]string, 0, len(doc.MetaLines))
	for key := range doc.MetaLines {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool { return doc.MetaLines[ordered[i]] < doc.MetaLines[ordered[j]] })
	for _, key := range ordered {
		if !known[key] {
			rep.Warning(doc.Path, doc.MetaLines[key], fmt.Sprintf("unknown frontmatter key '%s'", key))
		}
	}
}

func checkSections(rep *Report, path string, sections []format.Section, expected, optional []string) {
	seen := make([]string, 0, len(sections))
	seenSet := make(map[string]bool)
	for _, section := range sections {
		if !contains(expected, section.Name) {
			rep.Error(path, section.Line, fmt.Sprintf("unknown section '%s'", section.Name))
		} else if seenSet[section.Name] {
			rep.Error(path, section.Line, fmt.Sprintf("duplicate section '%s'", section.Name))
		} else {
			seen = append(seen, section.Name)
			seenSet[section.Name] = true
		}
	}
	for _, name := range expected {
		if !seenSet[name] && !contains(optional, name) {
			rep.Error(path, 1, fmt.Sprintf("missing section '%s'", name))
		}
	}
	ordered := make([]string, 0, len(seen))
	for _, name := range expected {
		if seenSet[name] {
			ordered = append(ordered, name)
		}
	}
	if !equalStrings(seen, ordered) {
		line := 1
		for _, section := range sections {
			if seenSet[section.Name] {
				line = section.Line
				break
			}
		}
		rep.Error(path, line, "sections out of order, expected: "+strings.Join(expected, ", "))
	}
}

func readBudget(doc *format.DocflowFile) int {
	return positiveDecimal(doc.Meta["read_budget"])
}

func startRefs(doc *format.DocflowFile) []string { return splitComma(doc.Meta["start_read"]) }
func repoGlobs(doc *format.DocflowFile) []string { return splitComma(doc.Meta["repos"]) }

func outside(pattern string) bool {
	if strings.HasPrefix(pattern, "/") || strings.HasPrefix(pattern, "~") {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(pattern), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// NestedRepos are directories matched by the docflow file's repos: globs
// that contain a .git entry, sorted and relative to root.
func NestedRepos(root string, doc *format.DocflowFile) []string {
	return matchRepos(root, repoGlobs(doc))
}

func matchRepos(root string, patterns []string) []string {
	found := make(map[string]bool)
	for _, pattern := range patterns {
		if outside(pattern) {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			continue
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() || filepath.Clean(path) == filepath.Clean(root) {
				continue
			}
			if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
				rel, err := filepath.Rel(root, path)
				if err == nil {
					found[filepath.ToSlash(rel)] = true
				}
			}
		}
	}
	result := make([]string, 0, len(found))
	for path := range found {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

// ProjectRepos returns the nested repositories declared by docs/docflow.md.
func ProjectRepos(root string) ([]string, error) {
	path := filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return []string{}, nil
	}
	doc, err := format.LoadDocflow(path)
	if err != nil {
		return nil, err
	}
	return NestedRepos(root, doc), nil
}

func lintRepos(rep *Report, root string, doc *format.DocflowFile) {
	line := doc.MetaLines["repos"]
	if line == 0 {
		line = 1
	}
	for _, pattern := range repoGlobs(doc) {
		if outside(pattern) {
			rep.Error(doc.Path, line, fmt.Sprintf("repos: %s is outside the project root", pattern))
		} else if len(matchRepos(root, []string{pattern})) == 0 {
			rep.Warning(doc.Path, line, fmt.Sprintf("repos: %s matches no git repository", pattern))
		}
	}
	covered := makeSet(NestedRepos(root, doc))
	for _, pattern := range []string{"*/.git", "*/*/.git"} {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		sort.Strings(matches)
		for _, gitPath := range matches {
			rel, err := filepath.Rel(root, filepath.Dir(gitPath))
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			first := strings.Split(rel, "/")[0]
			if first == ".git" || covered[rel] {
				continue
			}
			rep.Warning(doc.Path, line, fmt.Sprintf("%s: nested git repository not in repos:", rel))
		}
	}
}

func lintStart(rep *Report, root string, doc *format.DocflowFile) {
	budgetText := doc.Meta["start_budget"]
	limit := 0
	if budgetText != "" {
		limit = positiveDecimal(budgetText)
		if limit == 0 {
			rep.Error(doc.Path, doc.MetaLines["start_budget"], fmt.Sprintf("start_budget '%s' must be a positive integer", budgetText))
		}
	}
	sizes := make([]refSize, 0)
	for _, ref := range startRefs(doc) {
		size, err := format.RefSize(root, ref)
		if err != nil {
			rep.Error(doc.Path, doc.MetaLines["start_read"], err.Error())
			continue
		}
		sizes = append(sizes, refSize{ref: ref, size: size})
	}
	total := 0
	for _, item := range sizes {
		total += item.size
	}
	if limit > 0 && total/4 > limit {
		parts := make([]string, 0, len(sizes))
		for _, item := range sizes {
			parts = append(parts, fmt.Sprintf("%s: ~%d", item.ref, item.size/4))
		}
		rep.Error(doc.Path, doc.MetaLines["start_read"], fmt.Sprintf("start_read: ~%d tokens > start_budget %d (%s)", total/4, limit, strings.Join(parts, ", ")))
	}
}

type refSize struct {
	ref  string
	size int
}

func lintDocflow(rep *Report, root string, doc *format.DocflowFile) {
	checkMeta(rep, &doc.Doc, docflowKeys, docflowOptionalKeys)
	if line, text, invalid := versionError(doc); invalid {
		rep.Error(doc.Path, line, text)
	}
	budget := doc.Meta["read_budget"]
	if budget != "" && readBudget(doc) == 0 {
		rep.Error(doc.Path, doc.MetaLines["read_budget"], fmt.Sprintf("read_budget '%s' must be a positive integer", budget))
	}
	lintStart(rep, root, doc)
	lintRepos(rep, root, doc)
	checkSections(rep, doc.Path, doc.Sections, docflowSections, docflowOptionalSecs)
	seen := make(map[string]bool)
	for _, item := range doc.Queue {
		if !planIDRE.MatchString(item.Text) {
			rep.Error(doc.Path, item.Line, fmt.Sprintf("bad plan id '%s'", item.Text))
		} else if seen[item.Text] {
			rep.Error(doc.Path, item.Line, fmt.Sprintf("duplicate %s in Queue", item.Text))
		}
		seen[item.Text] = true
	}
	frozen := make(map[string]bool)
	for _, item := range doc.Frozen {
		id := frozenID(item)
		if !planIDRE.MatchString(id) {
			rep.Error(doc.Path, item.Line, fmt.Sprintf("bad plan id '%s'", id))
		} else if seen[id] {
			rep.Error(doc.Path, item.Line, fmt.Sprintf("%s in both Queue and Frozen", id))
		} else if frozen[id] {
			rep.Error(doc.Path, item.Line, fmt.Sprintf("duplicate %s in Frozen", id))
		}
		frozen[id] = true
	}
}

func frozenID(item format.Item) string {
	words := strings.Fields(item.Text)
	if len(words) == 0 {
		return ""
	}
	return words[0]
}

func checkUnit(rep *Report, path, planID string, unit *format.Unit) {
	if !contains(statuses, unit.Status) {
		rep.Error(path, unit.Line, fmt.Sprintf("unknown status [%s]", unit.Status))
	}
	if unit.Phase == nil {
		rep.Error(path, unit.Line, fmt.Sprintf("unit %s outside a phase", unit.ID))
	} else if !strings.HasPrefix(unit.ID, fmt.Sprintf("%s.%d.", planID, *unit.Phase)) {
		rep.Error(path, unit.Line, fmt.Sprintf("unit id %s must start with %s.%d.", unit.ID, planID, *unit.Phase))
	}
	keys := make([]string, 0, len(unit.Fields))
	for _, field := range unit.Fields {
		if !contains(unitFields, field.Key) {
			rep.Error(path, field.Line, fmt.Sprintf("unknown field '%s'", field.Key))
			continue
		}
		if contains(keys, field.Key) {
			if field.Key != "Executor" {
				rep.Error(path, field.Line, fmt.Sprintf("duplicate field '%s'", field.Key))
			}
			continue
		}
		if len(keys) > 0 && indexOf(unitFields, field.Key) < indexOf(unitFields, keys[len(keys)-1]) {
			rep.Error(path, field.Line, fmt.Sprintf("field '%s' out of order, expected: %s", field.Key, strings.Join(unitFields, ", ")))
		}
		keys = append(keys, field.Key)
	}
	for _, key := range requiredFields {
		field := unit.Field(key)
		if field == nil {
			rep.Error(path, unit.Line, fmt.Sprintf("unit %s: missing '%s'", unit.ID, key))
		} else if field.Value == "" && len(field.Items) == 0 {
			rep.Error(path, field.Line, fmt.Sprintf("unit %s: empty '%s'", unit.ID, key))
		}
	}
	if field := unit.Field("Verify"); field != nil && (len(field.Items) > 0 || !verifyRE.MatchString(field.Value)) {
		rep.Error(path, field.Line, "Verify must be one command in backticks")
	}
	if field := unit.Field("Commit"); field != nil {
		prefix := unit.ID + ": "
		if !strings.HasPrefix(field.Value, prefix) || strings.TrimSpace(strings.TrimPrefix(field.Value, prefix)) == "" {
			rep.Error(path, field.Line, fmt.Sprintf("Commit must start with \"%s\"", prefix))
		}
	}
	if field := unit.Field("Model"); field != nil && (len(field.Items) > 0 || !modelRE.MatchString(field.Value)) {
		rep.Error(path, field.Line, "Model must be one word: a tier (fast, default, strong) or a model id")
	}
}

// ParseExecutor parses an agent, model, and optional effort triple.
func ParseExecutor(value string) (string, string, *string, error) {
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return "", "", nil, fmt.Errorf("expected \"agent | model | effort\" with 2 separators, found %d", len(parts)-1)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		name := []string{"agent", "model", "effort"}[i]
		if parts[i] == "" {
			return "", "", nil, fmt.Errorf("empty %s", name)
		}
		if !executorPart(parts[i]) {
			return "", "", nil, fmt.Errorf("%s '%s' must be one word", name, parts[i])
		}
	}
	if !contains(executorAgents, parts[0]) {
		return "", "", nil, fmt.Errorf("unknown agent '%s'; known: %s", parts[0], strings.Join(executorAgents, ", "))
	}
	if parts[2] == "-" {
		return parts[0], parts[1], nil, nil
	}
	effort := parts[2]
	return parts[0], parts[1], &effort, nil
}

func executorPart(value string) bool {
	for _, r := range value {
		if unicode.IsSpace(r) || r == '|' {
			return false
		}
	}
	return value != ""
}

func executorMode(plan *format.Plan) bool {
	if _, ok := plan.Meta["executor"]; ok {
		return true
	}
	for _, unit := range plan.Units {
		if unit.Field("Executor") != nil {
			return true
		}
	}
	return false
}

// ExecutorMode reports whether a plan uses explicit executor assignments.
func ExecutorMode(plan *format.Plan) bool { return executorMode(plan) }

// RoleKeys returns the unit role names in canonical header order.
func RoleKeys() []string { return append([]string(nil), roleKeys...) }

type executorIssue struct {
	line int
	text string
}

func executorIssues(plan *format.Plan) []executorIssue {
	issues := fallbackIssues(plan)
	if !executorMode(plan) {
		return issues
	}
	_, head := plan.Meta["executor"]
	if head {
		if _, _, _, err := ParseExecutor(plan.Meta["executor"]); err != nil {
			issues = append(issues, executorIssue{plan.MetaLines["executor"], "executor: " + err.Error()})
		}
	}
	for _, unit := range plan.Units {
		fields := make([]format.Field, 0)
		for _, field := range unit.Fields {
			if field.Key == "Executor" {
				fields = append(fields, field)
			}
		}
		for n, field := range fields {
			if n > 0 {
				issues = append(issues, executorIssue{field.Line, fmt.Sprintf("unit %s: duplicate field 'Executor'", unit.ID)})
			}
			if len(field.Items) > 0 || field.End != field.Line {
				issues = append(issues, executorIssue{field.Line, fmt.Sprintf("unit %s: Executor must be one line", unit.ID)})
			} else if _, _, _, err := ParseExecutor(field.Value); err != nil {
				issues = append(issues, executorIssue{field.Line, fmt.Sprintf("unit %s: Executor: %s", unit.ID, err.Error())})
			}
		}
		if unit.Status == "x" || unit.Status == "-" {
			continue
		}
		if model := unit.Field("Model"); model != nil {
			issues = append(issues, executorIssue{model.Line, fmt.Sprintf("unit %s: Model: conflicts with executors of the plan; use Executor:", unit.ID)})
		}
		if len(fields) == 0 && unit.Field("Role") == nil && !head {
			issues = append(issues, executorIssue{unit.Line, fmt.Sprintf("unit %s: no executor; add Executor: to the unit or executor: to the frontmatter", unit.ID)})
		}
	}
	return issues
}

func fallbackIssues(plan *format.Plan) []executorIssue {
	value, present := plan.Meta["fallback"]
	if !present {
		return nil
	}
	issues := make([]executorIssue, 0)
	line := plan.MetaLines["fallback"]
	if !executorMode(plan) {
		issues = append(issues, executorIssue{line, "fallback needs executor mode"})
	}
	primary := ""
	if value, ok := plan.Meta["executor"]; ok {
		if agent, model, effort, err := ParseExecutor(value); err == nil {
			primary = executorKey(agent, model, effort)
		}
	}
	for _, raw := range strings.Split(value, ";") {
		agent, model, effort, err := ParseExecutor(strings.TrimSpace(raw))
		if err != nil {
			issues = append(issues, executorIssue{line, "fallback: " + err.Error()})
			continue
		}
		if primary != "" && executorKey(agent, model, effort) == primary {
			issues = append(issues, executorIssue{line, "fallback: repeats the executor assignment"})
		}
	}
	return issues
}

func executorKey(agent, model string, effort *string) string {
	effortValue := "-"
	if effort != nil {
		effortValue = *effort
	}
	return strings.Join([]string{agent, model, effortValue}, "\x00")
}

// planIssue is a plan-level finding with a level: executor chains add
// warnings next to errors.
type planIssue struct {
	line  int
	level string
	text  string
}

type routeTriple struct {
	agent  string
	model  string
	effort *string
}

func (t routeTriple) chainKey() string {
	return t.agent + "\x00" + t.model
}

// parseExecutorChain splits a "agent | model | effort; ..." chain into
// triples; every segment must parse, so empty segments are errors.
func parseExecutorChain(value string) ([]routeTriple, []string) {
	var triples []routeTriple
	var errs []string
	for _, raw := range strings.Split(value, ";") {
		agent, model, effort, err := ParseExecutor(strings.TrimSpace(raw))
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		triples = append(triples, routeTriple{agent: agent, model: model, effort: effort})
	}
	return triples, errs
}

// chainIssues validates role chains, the replan chain, Role fields, and
// single-route chains that leave a unit without a fallback.
func chainIssues(plan *format.Plan) []planIssue {
	issues := make([]planIssue, 0)
	mode := executorMode(plan)
	used := make(map[string]bool)
	for _, unit := range plan.Units {
		field := unit.Field("Role")
		if field == nil {
			continue
		}
		if len(field.Items) > 0 || field.End != field.Line {
			issues = append(issues, planIssue{field.Line, "error", fmt.Sprintf("unit %s: Role must be one line", unit.ID)})
			continue
		}
		if !mode {
			issues = append(issues, planIssue{field.Line, "error", fmt.Sprintf("unit %s: Role needs executor mode", unit.ID)})
			continue
		}
		if unit.Field("Executor") != nil {
			issues = append(issues, planIssue{field.Line, "error", fmt.Sprintf("unit %s: Role: conflicts with Executor:", unit.ID)})
		}
		if !contains(roleKeys, field.Value) {
			issues = append(issues, planIssue{field.Line, "error", fmt.Sprintf("unit %s: Role must be one of: %s", unit.ID, strings.Join(roleKeys, ", "))})
			continue
		}
		used[field.Value] = true
		if _, ok := plan.Meta[field.Value]; !ok {
			issues = append(issues, planIssue{field.Line, "error", fmt.Sprintf("unit %s: Role '%s' has no '%s:' chain in the frontmatter", unit.ID, field.Value, field.Value)})
		}
	}
	for _, key := range roleKeys {
		value, present := plan.Meta[key]
		if !present {
			continue
		}
		line := plan.MetaLines[key]
		if !mode {
			issues = append(issues, planIssue{line, "error", key + " needs executor mode"})
			continue
		}
		triples, errs := parseExecutorChain(value)
		for _, err := range errs {
			issues = append(issues, planIssue{line, "error", key + ": " + err})
		}
		seen := make(map[string]bool)
		for _, triple := range triples {
			if seen[triple.chainKey()] {
				issues = append(issues, planIssue{line, "error", fmt.Sprintf("%s: route '%s | %s' repeats in the chain", key, triple.agent, triple.model)})
				continue
			}
			seen[triple.chainKey()] = true
		}
		if len(errs) == 0 {
			if len(triples) == 1 {
				issues = append(issues, planIssue{line, "warning", "no fallback for " + key})
			}
			if !used[key] {
				issues = append(issues, planIssue{line, "warning", "no units with role " + key})
			}
		}
	}
	if value, present := plan.Meta["replan"]; present {
		line := plan.MetaLines["replan"]
		if !mode {
			issues = append(issues, planIssue{line, "error", "replan needs executor mode"})
		} else {
			triples, errs := parseExecutorChain(value)
			for _, err := range errs {
				issues = append(issues, planIssue{line, "error", "replan: " + err})
			}
			seen := make(map[string]bool)
			for _, triple := range triples {
				if seen[triple.chainKey()] {
					issues = append(issues, planIssue{line, "error", fmt.Sprintf("replan: route '%s | %s' repeats in the chain", triple.agent, triple.model)})
					continue
				}
				seen[triple.chainKey()] = true
			}
			if len(errs) == 0 && len(triples) == 1 {
				issues = append(issues, planIssue{line, "warning", "no fallback for replan"})
			}
		}
	}
	if mode && strings.TrimSpace(plan.Meta["fallback"]) == "" {
		if _, ok := plan.Meta["executor"]; ok {
			issues = append(issues, planIssue{plan.MetaLines["executor"], "warning", "no fallback for executor"})
		}
		for _, unit := range plan.Units {
			if field := unit.Field("Executor"); field != nil {
				issues = append(issues, planIssue{field.Line, "warning", fmt.Sprintf("unit %s: no fallback for executor", unit.ID)})
			}
		}
	}
	return issues
}

// ExecutorIssueStrings returns executor assignment findings in CLI form.
func ExecutorIssueStrings(plan *format.Plan) []string {
	issues := executorIssues(plan)
	result := make([]string, len(issues))
	for i, issue := range issues {
		result[i] = fmt.Sprintf("%s:%d: error: %s", plan.Path, issue.line, issue.text)
	}
	return result
}

// UnitExecutor resolves the first route assigned to a unit, replacing the
// full plan value when the unit has an explicit Executor: or Role: field.
func UnitExecutor(plan *format.Plan, unit *format.Unit) (string, string, *string, error) {
	if field := unit.Field("Role"); field != nil {
		chain := strings.Split(plan.Meta[field.Value], ";")
		if len(chain) == 0 {
			return "", "", nil, fmt.Errorf("role %s has no route", field.Value)
		}
		return ParseExecutor(strings.TrimSpace(chain[0]))
	}
	field := unit.Field("Executor")
	if field != nil {
		return ParseExecutor(field.Value)
	}
	value, ok := plan.Meta["executor"]
	if !ok {
		return "", "", nil, fmt.Errorf("no executor assignment")
	}
	return ParseExecutor(value)
}

// PlanPermissions returns auto unless the plan explicitly requests full.
func PlanPermissions(plan *format.Plan) string { return format.PlanPermissions(plan) }

// PlanPrerequisites returns the text and optional command from the section.
func PlanPrerequisites(plan *format.Plan) []format.Prerequisite {
	return format.PlanPrerequisites(plan)
}

func prereqIssues(plan *format.Plan) []executorIssue {
	section := plan.Section("Prerequisites")
	if section == nil {
		return []executorIssue{}
	}
	issues := make([]executorIssue, 0)
	lines, mask := lines(&plan.Doc)
	current := false
	for n := section.Line; n < section.End && n < len(lines); n++ {
		if n < len(mask) && mask[n] {
			current = false
			continue
		}
		match := itemRE.FindStringSubmatch(lines[n])
		if len(match) > 0 {
			current = true
			prereq := prereqRE.FindStringSubmatch(strings.TrimSpace(match[1]))
			if len(prereq) > 0 && strings.TrimSpace(prereq[2]) == "" {
				issues = append(issues, executorIssue{n + 1, "Prerequisites: empty command in backticks"})
			}
			continue
		}
		if current && continuationRE.MatchString(lines[n]) {
			continue
		}
		current = false
		if strings.TrimSpace(lines[n]) != "" {
			issues = append(issues, executorIssue{n + 1, "Prerequisites: line outside a list item"})
		}
	}
	return issues
}

func lintDepends(rep *Report, plan *format.Plan) {
	order := make(map[string]int, len(plan.Units))
	for i, unit := range plan.Units {
		order[unit.ID] = i
	}
	for n, unit := range plan.Units {
		field := unit.Field("Depends")
		if field == nil {
			continue
		}
		if len(field.Items) > 0 {
			rep.Error(plan.Path, field.Line, "Depends must be unit ids separated by commas, or -")
		}
		depends, _ := format.UnitDepends(plan, unit)
		for _, dep := range depends {
			if !unitIDRE.MatchString(dep) {
				rep.Error(plan.Path, field.Line, fmt.Sprintf("bad unit id '%s' in Depends", dep))
			} else if dep == unit.ID {
				rep.Error(plan.Path, field.Line, fmt.Sprintf("unit %s depends on itself", unit.ID))
			} else if _, ok := order[dep]; !ok {
				rep.Error(plan.Path, field.Line, fmt.Sprintf("%s in Depends is not a unit of this plan", dep))
			} else if order[dep] > n {
				rep.Error(plan.Path, field.Line, fmt.Sprintf("%s in Depends comes after %s", dep, unit.ID))
			}
		}
	}
}

const contextHint = " (Context list items are file references; write repro as prose)"

func lintRefs(rep *Report, root string, plan *format.Plan, budget int) {
	size := func(item format.Item, hint string) int {
		value, err := format.RefSize(root, item.Text)
		if err == nil {
			return value
		}
		var missing format.RefMissing
		var invalid format.RefError
		if errors.As(err, &missing) {
			rep.Warning(plan.Path, item.Line, err.Error()+hint)
		} else if errors.As(err, &invalid) {
			rep.Error(plan.Path, item.Line, err.Error()+hint)
		} else {
			rep.Error(plan.Path, item.Line, err.Error()+hint)
		}
		return 0
	}
	context := 0
	for _, item := range format.ContextRefs(plan) {
		context += size(item, contextHint)
	}
	for _, unit := range plan.Units {
		if unit.Status == "x" || unit.Status == "-" {
			continue
		}
		read := unit.Field("Read")
		total := context + len([]byte(cycle.PromptText(root, plan, unit)))
		// prompt_text is transferred in P024.3.1; this unit accounts for
		// the prompt and its Context and Read references together.
		if read != nil {
			for _, item := range read.Items {
				total += size(item, "")
			}
		}
		if budget > 0 && total/4 > budget {
			rep.Warning(plan.Path, unit.Line, fmt.Sprintf("unit %s: ~%d tokens of context > read_budget %d", unit.ID, total/4, budget))
		}
	}
}

// ActivationIssues runs the plan and reference checks used by `activate`,
// without including findings from unrelated plans in the project.
func ActivationIssues(root string, plan *format.Plan) ([]string, bool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, false, err
	}
	text, err := format.ReadText(filepath.Join(root, filepath.FromSlash(format.DocflowFilePath)))
	if err != nil {
		return nil, false, err
	}
	docflow, err := format.ParseDocflow(text, format.DocflowFilePath)
	if err != nil {
		return nil, false, err
	}
	report := &Report{Issues: make([]Issue, 0)}
	lintPlan(report, plan)
	lintRefs(report, root, plan, readBudget(docflow))
	issues := make([]string, 0, len(report.Issues))
	for _, issue := range report.Issues {
		issues = append(issues, issue.String())
	}
	return issues, len(report.Errors()) > 0, nil
}

// ReferenceIssues returns only reference and read-budget findings for a plan.
func ReferenceIssues(root string, plan *format.Plan) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	text, err := format.ReadText(filepath.Join(root, filepath.FromSlash(format.DocflowFilePath)))
	if err != nil {
		return nil, err
	}
	docflow, err := format.ParseDocflow(text, format.DocflowFilePath)
	if err != nil {
		return nil, err
	}
	report := &Report{Issues: make([]Issue, 0)}
	lintRefs(report, root, plan, readBudget(docflow))
	issues := make([]string, 0, len(report.Issues))
	for _, issue := range report.Issues {
		issues = append(issues, issue.String())
	}
	return issues, nil
}

// Plan checks the plan text without consulting the working tree.
func Plan(rep *Report, plan *format.Plan) { lintPlan(rep, plan) }

func lintPlan(rep *Report, plan *format.Plan) {
	path := plan.Path
	checkMeta(rep, &plan.Doc, planKeys, planOptionalKeys)
	planID := plan.Meta["id"]
	if planID != "" && !planIDRE.MatchString(planID) {
		rep.Error(path, plan.MetaLines["id"], fmt.Sprintf("bad plan id '%s'", planID))
	}
	for _, item := range []struct {
		key     string
		allowed []string
	}{{"type", planTypes}, {"status", planStatuses}} {
		value := plan.Meta[item.key]
		if value != "" && !contains(item.allowed, value) {
			rep.Error(path, plan.MetaLines[item.key], fmt.Sprintf("%s '%s' not in: %s", item.key, value, strings.Join(item.allowed, ", ")))
		}
	}
	fixed := make([]format.Section, 0, len(plan.Sections))
	phaseSeen := false
	for _, section := range plan.Sections {
		if strings.HasPrefix(section.Name, "Phase") {
			phaseSeen = true
			if !phaseRE.MatchString(section.Name) {
				rep.Error(path, section.Line, fmt.Sprintf("bad phase heading '%s'", section.Name))
			}
		} else if phaseSeen && contains(planSections, section.Name) {
			rep.Error(path, section.Line, fmt.Sprintf("section '%s' after phases", section.Name))
		} else {
			fixed = append(fixed, section)
		}
	}
	checkSections(rep, path, fixed, planSections, []string{"Prerequisites"})
	for n, phase := range plan.Phases {
		if phase.Number != n+1 {
			rep.Error(path, phase.Line, fmt.Sprintf("phase %d, expected %d", phase.Number, n+1))
		}
	}
	allLines, mask := lines(&plan.Doc)
	for _, section := range plan.Sections {
		if !phaseRE.MatchString(section.Name) {
			continue
		}
		for i := section.Line; i < section.End && i < len(allLines); i++ {
			if (i >= len(mask) || !mask[i]) && strings.HasPrefix(allLines[i], "### ") && !unitHeadingRE.MatchString(allLines[i]) {
				rep.Error(path, i+1, `bad unit heading, expected "### [s] Pxxx.N.M title"`)
			}
		}
	}
	seen := make(map[string]bool)
	for _, unit := range plan.Units {
		if seen[unit.ID] {
			rep.Error(path, unit.Line, fmt.Sprintf("duplicate unit id %s", unit.ID))
		}
		seen[unit.ID] = true
		checkUnit(rep, path, planID, unit)
		covered := map[int]bool{unit.Line: true}
		for _, field := range unit.Fields {
			for n := field.Line; n <= field.End; n++ {
				covered[n] = true
			}
		}
		for n := unit.Line + 1; n <= unit.End && n-1 < len(allLines); n++ {
			if !covered[n] && (n-1 >= len(mask) || !mask[n-1]) && strings.TrimSpace(allLines[n-1]) != "" {
				rep.Warning(path, n, fmt.Sprintf("unit %s: text outside fields", unit.ID))
			}
		}
	}
	for _, issue := range executorIssues(plan) {
		rep.Error(path, issue.line, issue.text)
	}
	if value := plan.Meta["permissions"]; value != "" && !contains(planPermissions, value) {
		rep.Error(path, plan.MetaLines["permissions"], fmt.Sprintf("permissions '%s' not in: %s", value, strings.Join(planPermissions, ", ")))
	}
	if value, present := plan.Meta["timeout"]; present {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 60 || seconds > 86400 {
			rep.Error(path, plan.MetaLines["timeout"], "timeout must be an integer from 60 to 86400 seconds")
		}
	}
	for _, issue := range chainIssues(plan) {
		if issue.level == "warning" {
			rep.Warning(path, issue.line, issue.text)
		} else {
			rep.Error(path, issue.line, issue.text)
		}
	}
	for _, issue := range prereqIssues(plan) {
		rep.Error(path, issue.line, issue.text)
	}
	lintDepends(rep, plan)
}

func lintArchive(rep *Report, doc *format.Archive) {
	checkMeta(rep, &doc.Doc, archiveKeys, archiveOptional)
	docID := doc.Meta["id"]
	if docID != "" && !planIDRE.MatchString(docID) {
		rep.Error(doc.Path, doc.MetaLines["id"], fmt.Sprintf("bad id '%s'", docID))
	}
	if superseded, ok := doc.Meta["superseded_by"]; ok && !planIDRE.MatchString(superseded) {
		rep.Error(doc.Path, doc.MetaLines["superseded_by"], fmt.Sprintf("bad superseded_by '%s'", superseded))
	}
	checkSections(rep, doc.Path, doc.Sections, archiveSections, nil)
	allLines, _ := lines(&doc.Doc)
	for _, section := range doc.Sections {
		nonEmpty := false
		for i := section.Line; i < section.End && i < len(allLines); i++ {
			if strings.TrimSpace(allLines[i]) != "" {
				nonEmpty = true
				break
			}
		}
		if !nonEmpty {
			rep.Error(doc.Path, section.Line, fmt.Sprintf("empty section '%s'", section.Name))
		}
	}
}

func load[T any](rep *Report, root, rel string, parse func(string, string) (T, error)) (T, error) {
	text, err := format.ReadText(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		var zero T
		return zero, err
	}
	doc, err := parse(text, rel)
	if err == nil {
		return doc, nil
	}
	var parseErr *clierrors.FormatError
	if errors.As(err, &parseErr) {
		rep.Error(parseErr.Path, parseErr.Line, parseErr.Text)
		var zero T
		return zero, nil
	}
	var zero T
	return zero, err
}

// Lint checks docs/docflow.md, all plans, archives, and plan references.
func Lint(root string) (*Report, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rep := &Report{Issues: make([]Issue, 0)}
	docflow, err := load(rep, root, format.DocflowFilePath, format.ParseDocflow)
	if err != nil {
		return nil, err
	}
	var budget int
	if docflow != nil {
		lintDocflow(rep, root, docflow)
		budget = readBudget(docflow)
	}
	ids := make(map[string]string)
	type planRecord struct {
		id, path string
		line     int
		status   string
	}
	plans := make([]planRecord, 0)
	for _, kind := range []string{"plans", "archive"} {
		pattern := filepath.Join(root, "docs", kind, "*.md")
		paths, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		sort.Strings(paths)
		for _, full := range paths {
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return nil, err
			}
			rel = filepath.ToSlash(rel)
			if kind == "plans" {
				plan, err := load(rep, root, rel, format.ParsePlan)
				if err != nil {
					return nil, err
				}
				if plan == nil {
					continue
				}
				lintPlan(rep, plan)
				if plan.Meta["status"] != "frozen" {
					lintRefs(rep, root, plan, budget)
				}
				id := plan.Meta["id"]
				if id == "" {
					continue
				}
				line := plan.MetaLines["id"]
				if previous, ok := ids[id]; ok {
					rep.Error(plan.Path, line, fmt.Sprintf("id %s already used in %s", id, previous))
				} else {
					ids[id] = plan.Path
				}
				plans = append(plans, planRecord{id: id, path: plan.Path, line: line, status: plan.Meta["status"]})
			} else {
				doc, err := load(rep, root, rel, format.ParseArchive)
				if err != nil {
					return nil, err
				}
				if doc == nil {
					continue
				}
				lintArchive(rep, doc)
				id := doc.Meta["id"]
				if id == "" {
					continue
				}
				line := doc.MetaLines["id"]
				if previous, ok := ids[id]; ok {
					rep.Error(doc.Path, line, fmt.Sprintf("id %s already used in %s", id, previous))
				} else {
					ids[id] = doc.Path
				}
			}
		}
	}
	if docflow != nil {
		planIDs := makeSet(nil)
		for _, plan := range plans {
			planIDs[plan.id] = true
		}
		queue := makeSet(nil)
		for _, item := range docflow.Queue {
			queue[item.Text] = true
		}
		frozen := makeSet(nil)
		for _, item := range docflow.Frozen {
			frozen[frozenID(item)] = true
		}
		sections := []struct {
			name  string
			items []format.Item
			id    func(format.Item) string
		}{{"Queue", docflow.Queue, func(i format.Item) string { return i.Text }}, {"Frozen", docflow.Frozen, frozenID}}
		for _, section := range sections {
			for _, item := range section.items {
				id := section.id(item)
				if planIDRE.MatchString(id) && !planIDs[id] {
					rep.Error(docflow.Path, item.Line, fmt.Sprintf("%s in %s has no plan in docs/plans/", id, section.name))
				}
			}
		}
		for _, plan := range plans {
			if plan.status == "frozen" {
				if queue[plan.id] {
					rep.Error(plan.path, plan.line, fmt.Sprintf("plan %s is frozen but in Queue", plan.id))
				} else if !frozen[plan.id] {
					rep.Error(plan.path, plan.line, fmt.Sprintf("plan %s is frozen but not in Frozen", plan.id))
				}
			} else if frozen[plan.id] {
				rep.Error(plan.path, plan.line, fmt.Sprintf("plan %s is in Frozen but its status is not frozen", plan.id))
			} else if !queue[plan.id] {
				rep.Error(plan.path, plan.line, fmt.Sprintf("plan %s is not in Queue", plan.id))
			}
		}
	}
	return rep, nil
}

// CmdLint prints a report and returns the CLI exit code.
func CmdLint(root string, out io.Writer) (int, error) {
	rep, err := Lint(root)
	if err != nil {
		return clierrors.EXIT_FORMAT, err
	}
	for _, issue := range rep.Issues {
		fmt.Fprintln(out, issue.String())
	}
	if len(rep.Errors()) > 0 {
		return clierrors.EXIT_FORMAT, nil
	}
	return clierrors.EXIT_OK, nil
}

func splitComma(value string) []string {
	result := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func positiveDecimal(value string) int {
	if value == "" {
		return 0
	}
	var normalized strings.Builder
	for _, r := range value {
		digit, ok := decimalDigit(r)
		if !ok {
			return 0
		}
		normalized.WriteByte(byte('0' + digit))
	}
	n, err := strconv.Atoi(normalized.String())
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func decimalDigit(r rune) (int, bool) {
	for _, block := range unicode.Nd.R16 {
		if r >= rune(block.Lo) && r <= rune(block.Hi) && (uint16(r)-block.Lo)%block.Stride == 0 {
			return int((uint16(r)-block.Lo)/block.Stride) % 10, true
		}
	}
	for _, block := range unicode.Nd.R32 {
		if uint32(r) >= block.Lo && uint32(r) <= block.Hi && (uint32(r)-block.Lo)%block.Stride == 0 {
			return int((uint32(r)-block.Lo)/block.Stride) % 10, true
		}
	}
	return 0, false
}

func makeSet(values []string, more ...[]string) map[string]bool {
	result := make(map[string]bool)
	for _, value := range values {
		result[value] = true
	}
	for _, group := range more {
		for _, value := range group {
			result[value] = true
		}
	}
	return result
}

func contains(values []string, value string) bool { return indexOf(values, value) >= 0 }

func indexOf(values []string, value string) int {
	for i, candidate := range values {
		if candidate == value {
			return i
		}
	}
	return -1
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
