package cycle

import (
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"bsv-docflow/internal/clierrors"
	"bsv-docflow/internal/format"
	"bsv-docflow/internal/gitx"
	docskills "bsv-docflow/internal/skills"
	"bsv-docflow/templates"
)

const agentsLine = "Plans: docflow (run docflow status), not the bsv-docs PLAN genre"

var (
	slugRE       = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	idPrefixRE   = regexp.MustCompile(`^P(\p{Nd}+)`)
	migrateOneRE = regexp.MustCompile(`^(docflow:[ \t]*)1`)
)

func renderTemplate(name string, values map[string]string) (string, error) {
	text, err := templates.Files.ReadFile(name)
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	replacements := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		replacements = append(replacements, "{"+key+"}", values[key])
	}
	return strings.NewReplacer(replacements...).Replace(string(text)), nil
}

// Init creates the docflow files in root and optionally copies the checkout's
// skills into the selected project's agent directory.
func Init(root, agent string, withSkills bool, skillsDir string) (Result, error) {
	docflowPath := filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))
	if _, err := os.Stat(docflowPath); err == nil {
		return Result{}, clierrors.DocflowError{Message: format.DocflowFilePath + " already exists", Code: clierrors.EXIT_FORMAT}
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	var skillPlan *docskills.ProjectPlan
	if withSkills {
		plan, err := docskills.PlanProjectSkills(skillsDir, root, agent)
		if err != nil {
			return Result{}, err
		}
		skillPlan = &plan
		if plan.Conflict {
			return Result{Code: clierrors.EXIT_FORMAT, Stdout: plan.Stdout, Stderr: plan.Stderr}, nil
		}
	}
	for _, kind := range []string{"plans", "archive"} {
		dir := filepath.Join(root, "docs", kind)
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return Result{}, err
		}
		file, err := os.OpenFile(filepath.Join(dir, ".gitkeep"), os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			return Result{}, err
		}
		if err := file.Close(); err != nil {
			return Result{}, err
		}
	}
	text, err := renderTemplate("docflow.md", nil)
	if err != nil {
		return Result{}, err
	}
	readmeInfo, readmeErr := os.Stat(filepath.Join(root, "README.md"))
	if os.IsNotExist(readmeErr) || readmeErr == nil && !readmeInfo.Mode().IsRegular() {
		var kept strings.Builder
		for _, line := range strings.SplitAfter(text, "\n") {
			if !strings.HasPrefix(line, "start_read:") {
				kept.WriteString(line)
			}
		}
		text = kept.String()
	} else if readmeErr != nil {
		return Result{}, readmeErr
	}
	if err := os.WriteFile(docflowPath, []byte(text), 0o666); err != nil {
		return Result{}, err
	}
	agentsPath := filepath.Join(root, "AGENTS.md")
	agentsText := ""
	if content, err := os.ReadFile(agentsPath); err == nil {
		agentsText = universalNewlines(string(content))
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	if !containsLine(agentsText, agentsLine) {
		if agentsText != "" && !strings.HasSuffix(agentsText, "\n") {
			agentsText += "\n"
		}
		if agentsText != "" {
			agentsText += "\n"
		}
		agentsText += agentsLine + "\n"
		if err := os.WriteFile(agentsPath, []byte(agentsText), 0o666); err != nil {
			return Result{}, err
		}
	}
	stdout := ""
	if skillPlan != nil {
		stdout += skillPlan.Stdout
	}
	stdout += "created docs/docflow.md, docs/plans/, docs/archive/; AGENTS.md has the docflow line\n"
	if skillPlan != nil {
		skillOutput, err := docskills.WriteProjectSkills(skillPlan.Copies, agent)
		if err != nil {
			return Result{}, err
		}
		stdout += skillOutput
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: stdout}, nil
}

func containsLine(text, value string) bool {
	for _, line := range strings.Split(text, "\n") {
		if line == value {
			return true
		}
	}
	return false
}

func universalNewlines(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// New creates a draft plan from the embedded template and appends it to Queue.
func New(root, slug, title, planType string) (Result, error) {
	if !slugRE.MatchString(slug) {
		return Result{}, clierrors.DocflowError{Message: fmt.Sprintf("bad slug %s: use lower-case words joined by -", pythonRepr(slug)), Code: clierrors.EXIT_FORMAT}
	}
	id, err := nextPlanID(root)
	if err != nil {
		return Result{}, err
	}
	if title == "" {
		title = strings.ReplaceAll(slug, "-", " ")
	}
	if planType == "" {
		planType = "feature"
	}
	text, err := renderTemplate("plan.md", map[string]string{
		"id": id, "title": title, "type": planType, "date": time.Now().Format("2006-01-02"),
	})
	if err != nil {
		return Result{}, err
	}
	rel := filepath.ToSlash(filepath.Join("docs", "plans", id+"-"+slug+".md"))
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(text), 0o666); err != nil {
		return Result{}, err
	}
	if err := format.CheckVersion(root); err != nil {
		return Result{}, err
	}
	docflow, err := readDocflow(root)
	if err != nil {
		return Result{}, err
	}
	if err := docflow.AppendSectionItems("Queue", []string{id}); err != nil {
		return Result{}, err
	}
	if err := docflow.Save(filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))); err != nil {
		return Result{}, err
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: rel + "\n"}, nil
}

func nextPlanID(root string) (string, error) {
	top := new(big.Int)
	for _, kind := range []string{"plans", "archive"} {
		paths, err := filepath.Glob(filepath.Join(root, "docs", kind, "*.md"))
		if err != nil {
			return "", err
		}
		for _, path := range paths {
			if n, ok := numericIDPrefix(filepath.Base(path)); ok && n.Cmp(top) > 0 {
				top.Set(n)
			}
			content, err := format.ReadText(path)
			if err != nil {
				return "", err
			}
			rawLines := format.SplitLines(content)
			lines := make([]string, len(rawLines))
			for i, line := range rawLines {
				lines[i] = strings.TrimRight(line, "\r\n")
			}
			meta, _, _, err := format.ParseMeta(filepath.ToSlash(path), lines)
			if err != nil {
				continue
			}
			if n, ok := numericIDPrefix(meta["id"]); ok && n.Cmp(top) > 0 {
				top.Set(n)
			}
		}
	}
	top.Add(top, big.NewInt(1))
	digits := top.String()
	if len(digits) < 3 {
		digits = strings.Repeat("0", 3-len(digits)) + digits
	}
	return "P" + digits, nil
}

func numericIDPrefix(value string) (*big.Int, bool) {
	match := idPrefixRE.FindStringSubmatch(value)
	if len(match) != 2 {
		return nil, false
	}
	var digits strings.Builder
	for _, r := range match[1] {
		digit, ok := decimalDigit(r)
		if !ok {
			return nil, false
		}
		digits.WriteByte(byte('0' + digit))
	}
	n, ok := new(big.Int).SetString(digits.String(), 10)
	return n, ok
}

func decimalDigit(r rune) (int, bool) {
	if !unicode.Is(unicode.Nd, r) {
		return 0, false
	}
	for _, block := range unicode.Nd.R16 {
		if uint32(r) >= uint32(block.Lo) && uint32(r) <= uint32(block.Hi) && (uint16(r)-block.Lo)%block.Stride == 0 {
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

type planReference struct {
	path string
	line int
}

func planReferences(root, planID, planPath string) ([]planReference, error) {
	pattern, err := regexp.Compile(`plans/` + regexp.QuoteMeta(planID) + `([^\p{Nd}]|$)`)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(root, filepath.FromSlash(planPath))
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	if absolute, err := filepath.Abs(target); err == nil {
		target = filepath.Clean(absolute)
	}
	found := make([]planReference, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel != "." && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".md" {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		full := path
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			full = resolved
		}
		if absolute, err := filepath.Abs(full); err == nil && filepath.Clean(absolute) == target {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for index, line := range strings.Split(universalNewlines(string(content)), "\n") {
			if pattern.MatchString(line) {
				found = append(found, planReference{path: filepath.ToSlash(rel), line: index + 1})
			}
		}
		return nil
	})
	return found, err
}

func newArchive(root, planID, slug, title string) (string, *format.Archive, error) {
	rel := filepath.ToSlash(filepath.Join("docs", "archive", planID+"-"+slug+".md"))
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		return "", nil, clierrors.DocflowError{Message: rel + " already exists", Code: clierrors.EXIT_FORMAT}
	} else if !os.IsNotExist(err) {
		return "", nil, err
	}
	text, err := renderTemplate("archive.md", map[string]string{
		"id": planID, "title": title, "date": time.Now().Format("2006-01-02"),
	})
	if err != nil {
		return "", nil, err
	}
	archive, err := format.ParseArchive(text, rel)
	if err != nil {
		return "", nil, err
	}
	return rel, archive, nil
}

// Close archives a plan whose units have all been completed or skipped.
func Close(root, requestedID string) (Result, error) {
	plan, err := findPlan(root, requestedID)
	if err != nil {
		return Result{}, err
	}
	planID := plan.Meta["id"]
	if planID == "" {
		planID = requestedID
	}
	open := make([]string, 0)
	for _, unit := range plan.Units {
		pending, err := commitPending(root, plan, unit)
		if err != nil {
			return Result{}, err
		}
		if !DoneStatuses[unit.Status] || pending {
			open = append(open, unit.ID)
		}
	}
	if len(open) > 0 {
		return Result{Code: clierrors.EXIT_ORDER, Stderr: fmt.Sprintf("%s: units not [x]/[-]: %s\n", planID, strings.Join(open, ", "))}, nil
	}
	refs, err := planReferences(root, planID, plan.Path)
	if err != nil {
		return Result{}, err
	}
	if len(refs) > 0 {
		var stderr strings.Builder
		for _, ref := range refs {
			fmt.Fprintf(&stderr, "%s:%d: error: references plans/%s\n", ref.path, ref.line, planID)
		}
		return Result{Code: clierrors.EXIT_FORMAT, Stderr: stderr.String()}, nil
	}
	base := strings.TrimSuffix(filepath.Base(plan.Path), filepath.Ext(plan.Path))
	slug := ""
	if len(base) > len(planID) {
		slug = base[len(planID)+1:]
	}
	if slug == "" {
		slug = strings.ToLower(planID)
	}
	rel, archive, err := newArchive(root, planID, slug, valueOr(plan.Meta["title"], slug))
	if err != nil {
		return Result{}, err
	}
	if decisions := plan.Section("Decisions"); decisions != nil {
		body := make([]string, 0, decisions.End-decisions.Line)
		for _, line := range plan.Raw[decisions.Line:decisions.End] {
			body = append(body, universalNewlines(line))
		}
		for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
			body = body[:len(body)-1]
		}
		for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
			body = body[1:]
		}
		if len(body) > 0 {
			section := archive.Section("Decisions")
			if section == nil {
				return Result{}, clierrors.DocflowError{Message: fmt.Sprintf("%s: no section 'Decisions'", rel), Code: clierrors.EXIT_FORMAT}
			}
			at := section.Line
			archive.Raw = append(archive.Raw[:at], append(body, archive.Raw[at:]...)...)
		}
	}
	archivePath := filepath.Join(root, filepath.FromSlash(rel))
	if err := format.CheckVersion(root); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o777); err != nil {
		return Result{}, err
	}
	if err := archive.Save(archivePath); err != nil {
		return Result{}, err
	}
	docflow, err := readDocflow(root)
	if err != nil {
		return Result{}, err
	}
	for _, item := range append(append([]format.Item{}, docflow.Queue...), docflow.Frozen...) {
		if planID == item.Text || planID == frozenIDText(item) {
			if err := docflow.RemoveLine(item.Line); err != nil {
				return Result{}, err
			}
			break
		}
	}
	if err := docflow.Save(filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))); err != nil {
		return Result{}, err
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(plan.Path))); err != nil {
		return Result{}, err
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: rel + "\n"}, nil
}

func frozenIDText(item format.Item) string {
	words := strings.Fields(item.Text)
	if len(words) == 0 {
		return ""
	}
	return words[0]
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// Activate promotes a draft after its own lint checks pass.
func Activate(root, planID string, check func(string, *format.Plan) ([]string, bool, error)) (Result, error) {
	if err := format.CheckVersion(root); err != nil {
		return Result{}, err
	}
	docflow, err := readDocflow(root)
	if err != nil {
		return Result{}, err
	}
	for _, item := range docflow.Frozen {
		if frozenIDText(item) == planID {
			return Result{}, clierrors.DocflowError{Message: fmt.Sprintf("%s is frozen; run docflow thaw %s", planID, planID), Code: clierrors.EXIT_FORMAT}
		}
	}
	plan, err := findPlan(root, planID)
	if err != nil {
		return Result{}, err
	}
	status := plan.Meta["status"]
	if status != "draft" {
		if status == "" {
			status = "without status"
		}
		return Result{}, clierrors.DocflowError{Message: fmt.Sprintf("%s: plan is %s, not a draft", plan.Path, status), Code: clierrors.EXIT_FORMAT}
	}
	issues, hasErrors, err := check(root, plan)
	if err != nil {
		return Result{}, err
	}
	var stderr strings.Builder
	for _, issue := range issues {
		stderr.WriteString(issue)
		stderr.WriteByte('\n')
	}
	if hasErrors {
		return Result{Code: clierrors.EXIT_FORMAT, Stderr: stderr.String()}, nil
	}
	if err := plan.SetMeta("status", "active"); err != nil {
		return Result{}, err
	}
	if err := plan.Save(filepath.Join(root, filepath.FromSlash(plan.Path))); err != nil {
		return Result{}, err
	}
	if !containsItem(docflow.Queue, planID) {
		if err := docflow.AppendSectionItems("Queue", []string{planID}); err != nil {
			return Result{}, err
		}
		if err := docflow.Save(filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))); err != nil {
			return Result{}, err
		}
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: fmt.Sprintf("commit: %s: activate plan\n", planID), Stderr: stderr.String()}, nil
}

func containsItem(items []format.Item, text string) bool {
	for _, item := range items {
		if item.Text == text {
			return true
		}
	}
	return false
}

// Freeze moves a queued plan to Frozen, requiring a clean tree if a unit is in
// progress.
func Freeze(root, planID, reason string, listRepos func(string) ([]string, error)) (Result, error) {
	if err := format.CheckVersion(root); err != nil {
		return Result{}, err
	}
	docflow, err := readDocflow(root)
	if err != nil {
		return Result{}, err
	}
	itemIndex := -1
	for index, item := range docflow.Queue {
		if item.Text == planID {
			itemIndex = index
			break
		}
	}
	if itemIndex < 0 {
		return Result{}, clierrors.DocflowError{Message: fmt.Sprintf("%s is not in Queue", planID), Code: clierrors.EXIT_FORMAT}
	}
	item := docflow.Queue[itemIndex]
	plan, err := findPlan(root, planID)
	if err != nil {
		return Result{}, err
	}
	if plan.Meta["status"] == "draft" {
		return Result{}, clierrors.DocflowError{Message: fmt.Sprintf("%s: plan is a draft", plan.Path), Code: clierrors.EXIT_FORMAT}
	}
	var doing *format.Unit
	for _, unit := range plan.Units {
		if unit.Status == "~" {
			doing = unit
			break
		}
	}
	if doing != nil {
		var repos []string
		if listRepos != nil {
			repos, err = listRepos(root)
			if err != nil {
				return Result{}, err
			}
		}
		dirty, err := gitx.DirtyFiles(root, plan.Path, repos)
		if err != nil {
			return Result{}, err
		}
		if len(dirty) > 0 {
			var stderr strings.Builder
			fmt.Fprintf(&stderr, "%s is in progress with uncommitted work:", doing.ID)
			for _, line := range dirty {
				fmt.Fprintf(&stderr, "\n  %s", line)
			}
			stderr.WriteByte('\n')
			return Result{Code: clierrors.EXIT_ORDER, Stderr: stderr.String()}, nil
		}
		if err := plan.SetStatus(doing.ID, " "); err != nil {
			return Result{}, err
		}
	}
	if err := plan.SetMeta("status", "frozen"); err != nil {
		return Result{}, err
	}
	if err := plan.Save(filepath.Join(root, filepath.FromSlash(plan.Path))); err != nil {
		return Result{}, err
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if err := docflow.RemoveLine(item.Line); err != nil {
		return Result{}, err
	}
	frozenItem := planID
	if reason != "" {
		frozenItem += " — " + reason
	}
	if err := docflow.AddFrozen(frozenItem); err != nil {
		return Result{}, err
	}
	if err := docflow.Save(filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))); err != nil {
		return Result{}, err
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: plan.Path + "\n" + fmt.Sprintf("commit: %s: freeze plan\n", planID)}, nil
}

// Thaw moves a frozen plan to the end of Queue and reports reference warnings.
func Thaw(root, planID string, check func(string, *format.Plan) ([]string, error)) (Result, error) {
	if err := format.CheckVersion(root); err != nil {
		return Result{}, err
	}
	docflow, err := readDocflow(root)
	if err != nil {
		return Result{}, err
	}
	var frozen *format.Item
	for index := range docflow.Frozen {
		if frozenIDText(docflow.Frozen[index]) == planID {
			frozen = &docflow.Frozen[index]
			break
		}
	}
	if frozen == nil {
		return Result{}, clierrors.DocflowError{Message: fmt.Sprintf("%s is not in Frozen", planID), Code: clierrors.EXIT_FORMAT}
	}
	plan, err := findPlan(root, planID)
	if err != nil {
		return Result{}, err
	}
	if err := plan.SetMeta("status", "active"); err != nil {
		return Result{}, err
	}
	if err := plan.Save(filepath.Join(root, filepath.FromSlash(plan.Path))); err != nil {
		return Result{}, err
	}
	if err := docflow.RemoveLine(frozen.Line); err != nil {
		return Result{}, err
	}
	if err := docflow.AppendSectionItems("Queue", []string{planID}); err != nil {
		return Result{}, err
	}
	if err := docflow.Save(filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))); err != nil {
		return Result{}, err
	}
	issues, err := check(root, plan)
	if err != nil {
		return Result{}, err
	}
	var stderr strings.Builder
	for _, issue := range issues {
		stderr.WriteString(issue)
		stderr.WriteByte('\n')
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: fmt.Sprintf("commit: %s: thaw plan\n", planID), Stderr: stderr.String()}, nil
}

// Note creates an archive entry without a corresponding plan.
func Note(root, slug, title string) (Result, error) {
	if !slugRE.MatchString(slug) {
		return Result{}, clierrors.DocflowError{Message: fmt.Sprintf("bad slug %s: use lower-case words joined by -", pythonRepr(slug)), Code: clierrors.EXIT_FORMAT}
	}
	if title == "" {
		title = strings.ReplaceAll(slug, "-", " ")
	}
	id, err := nextPlanID(root)
	if err != nil {
		return Result{}, err
	}
	rel, archive, err := newArchive(root, id, slug, title)
	if err != nil {
		return Result{}, err
	}
	archivePath := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o777); err != nil {
		return Result{}, err
	}
	if err := archive.Save(archivePath); err != nil {
		return Result{}, err
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: rel + "\n"}, nil
}

type migration func(string) error

var migrations = map[string]migration{"1": migrateOne}

func migrateOne(root string) error {
	path := filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))
	text, err := format.ReadText(path)
	if err != nil {
		return err
	}
	doc, err := format.ParseDocflow(text, format.DocflowFilePath)
	if err != nil {
		return err
	}
	line := doc.MetaLines["docflow"]
	if line == 0 {
		line = 1
	}
	index := line - 1
	if index >= 0 && index < len(doc.Raw) {
		doc.Raw[index] = migrateOneRE.ReplaceAllString(doc.Raw[index], "${1}2")
	}
	return doc.Save(path)
}

// Migrate advances supported project formats and preserves all other content.
func Migrate(root string) (Result, error) {
	path := filepath.Join(root, filepath.FromSlash(format.DocflowFilePath))
	text, err := format.ReadText(path)
	if err != nil {
		return Result{}, err
	}
	docflow, err := format.ParseDocflow(text, format.DocflowFilePath)
	if err != nil {
		return Result{}, err
	}
	version := docflow.Meta["docflow"]
	if version == format.DocflowVersion {
		return Result{Code: clierrors.EXIT_NOTHING, Stderr: "nothing to migrate\n"}, nil
	}
	var stdout strings.Builder
	for version != format.DocflowVersion {
		step := migrations[version]
		if step == nil {
			line := docflow.MetaLines["docflow"]
			if line == 0 {
				line = 1
			}
			return Result{}, &clierrors.FormatError{Path: format.DocflowFilePath, Line: line, Text: fmt.Sprintf("unknown format version %s", pythonRepr(version))}
		}
		if err := step(root); err != nil {
			return Result{}, err
		}
		text, err = format.ReadText(path)
		if err != nil {
			return Result{}, err
		}
		docflow, err = format.ParseDocflow(text, format.DocflowFilePath)
		if err != nil {
			return Result{}, err
		}
		next := docflow.Meta["docflow"]
		fmt.Fprintf(&stdout, "migrated %s -> %s\n", version, next)
		version = next
	}
	return Result{Code: clierrors.EXIT_OK, Stdout: stdout.String()}, nil
}

func pythonRepr(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}
