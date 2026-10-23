package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/format"
	docskills "bsv-docflow/internal/skills"
	"bsv-docflow/templates"
)

func callLifecycleCLI(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeLifecycleFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lifecycleDocflow(queue, frozen []string) string {
	var text strings.Builder
	text.WriteString("---\ndocflow: 2\nread_budget: 40000\n---\n## Queue\n")
	for _, id := range queue {
		fmt.Fprintf(&text, "- %s\n", id)
	}
	text.WriteByte('\n')
	if len(frozen) > 0 {
		text.WriteString("## Frozen\n")
		for _, id := range frozen {
			fmt.Fprintf(&text, "- %s\n", id)
		}
		text.WriteByte('\n')
	}
	text.WriteString("## Backlog\n- some task\n")
	return text.String()
}

func lifecyclePlan(id, status string, unitStatuses ...string) string {
	var text strings.Builder
	fmt.Fprintf(&text, "---\nid: %s\ntitle: Plan %s\ntype: feature\nstatus: %s\ncreated: 2026-10-12\n---\n", id, id, status)
	text.WriteString("## Goal\nGoal.\n\n## Context\n\n## Constraints\n\n## Prerequisites\n\n## Traps\n\n## Decisions\n- lifecycle decision — why — alternative\n\n## Phase 1: First\n\n")
	for index, unitStatus := range unitStatuses {
		unitID := fmt.Sprintf("%s.1.%d", id, index+1)
		fmt.Fprintf(&text, "### [%s] %s Unit %d\nRead:\n- a.py\nDo:\n- edit a.py\nVerify: `true`\nCommit: %s: unit %d\n\n", unitStatus, unitID, index+1, unitID, index+1)
	}
	return text.String()
}

func makeLifecycleProject(t *testing.T, queue, frozen []string, plans map[string]string, archiveDir bool) string {
	t.Helper()
	root := t.TempDir()
	dirs := []string{"docs/plans"}
	if archiveDir {
		dirs = append(dirs, "docs/archive")
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeLifecycleFile(t, root, "docs/docflow.md", lifecycleDocflow(queue, frozen))
	writeLifecycleFile(t, root, "a.py", "x = 1\n")
	for id, content := range plans {
		writeLifecycleFile(t, root, "docs/plans/"+id+"-slug.md", content)
	}
	return root
}

func initLifecycleGit(t *testing.T, root string) {
	t.Helper()
	commands := [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "docflow test"},
		{"config", "user.email", "test@example.invalid"},
		{"add", "-A"},
		{"commit", "-q", "-m", "initial"},
	}
	for _, args := range commands {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
}

func TestInitCreatesProjectAndIsIdempotentOnlyOnce(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := t.TempDir()
	writeLifecycleFile(t, root, "README.md", "# Project\n")
	code, stdout, stderr := callLifecycleCLI(t, root, "init")
	want := "created docs/docflow.md, docs/plans/, docs/archive/; AGENTS.md has the docflow line\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Fatalf("init = (%d, %q, %q)", code, stdout, stderr)
	}
	for _, kind := range []string{"plans", "archive"} {
		if _, err := os.Stat(filepath.Join(root, "docs", kind, ".gitkeep")); err != nil {
			t.Fatalf("missing docs/%s/.gitkeep: %v", kind, err)
		}
	}
	templateText, err := templates.Files.ReadFile("docflow.md")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "docs/docflow.md")); err != nil || string(got) != string(templateText) {
		t.Fatalf("embedded template output = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "AGENTS.md")); err != nil || string(got) != "Plans: docflow (run docflow status), not the bsv-docs PLAN genre\n" {
		t.Fatalf("AGENTS.md = %q, %v", got, err)
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("lint = (%d, %q, %q)", code, stdout, stderr)
	}
	wantStatus := "queue is empty\ncurrent: none\nstart: README.md\nlint: ok\n"
	if code, stdout, stderr := callLifecycleCLI(t, root, "status"); code != 0 || stdout != wantStatus || stderr != "" {
		t.Fatalf("status = (%d, %q, %q)", code, stdout, stderr)
	}
	for _, name := range []string{".claude", ".agents"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s: %v", name, err)
		}
	}
	code, _, stderr = callLifecycleCLI(t, root, "init")
	if code != 2 || !strings.Contains(stderr, "docs/docflow.md already exists") {
		t.Fatalf("second init = (%d, %q)", code, stderr)
	}
}

func TestInitWithoutReadmePreservesAgentsAndUsesCurrentFormat(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := t.TempDir()
	writeLifecycleFile(t, root, "AGENTS.md", "# Project\nrules")
	if code, _, stderr := callLifecycleCLI(t, root, "init", "--agent", "codex"); code != 0 || stderr != "" {
		t.Fatalf("init = (%d, %q)", code, stderr)
	}
	docflowText, err := os.ReadFile(filepath.Join(root, "docs/docflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(docflowText), "start_read:") || !strings.Contains(string(docflowText), "start_budget:") {
		t.Fatalf("docflow template without README = %q", docflowText)
	}
	docflow, err := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
	if err != nil || docflow.Meta["docflow"] != format.DocflowVersion {
		t.Fatalf("docflow version = %v, %v", docflow, err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "AGENTS.md")); err != nil || string(got) != "# Project\nrules\n\nPlans: docflow (run docflow status), not the bsv-docs PLAN genre\n" {
		t.Fatalf("AGENTS.md = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("unexpected .agents: %v", err)
	}
}

func TestInitDoesNotDuplicateAgentsLineAndUsesCurrentDirectory(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := t.TempDir()
	existing := "# Project\nPlans: docflow (run docflow status), not the bsv-docs PLAN genre\n"
	writeLifecycleFile(t, root, "AGENTS.md", existing)
	if code, _, stderr := callLifecycleCLI(t, root, "init"); code != 0 || stderr != "" {
		t.Fatalf("init = (%d, %q)", code, stderr)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "AGENTS.md")); string(got) != existing {
		t.Fatalf("AGENTS.md duplicated the docflow line: %q", got)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := callLifecycleCLI(t, sub, "init"); code != 0 || stderr != "" {
		t.Fatalf("init in subdirectory = (%d, %q)", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(sub, "docs/docflow.md")); err != nil {
		t.Fatalf("init did not use cwd: %v", err)
	}
}

func TestInitCopiesSkillsIntoProject(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := t.TempDir()
	previousSkillDirectory := currentSkillDirectory
	currentSkillDirectory = func() (string, error) {
		_, sourceFile, _, ok := runtime.Caller(0)
		if !ok {
			return "", fmt.Errorf("cannot locate lifecycle test source")
		}
		return filepath.Abs(filepath.Join(filepath.Dir(sourceFile), "..", "..", "skills"))
	}
	defer func() { currentSkillDirectory = previousSkillDirectory }()
	code, stdout, stderr := callLifecycleCLI(t, root, "init", "--skills")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "created docs/docflow.md,") {
		t.Fatalf("init --skills = (%d, %q, %q)", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/docflow.md")); err != nil {
		t.Fatalf("init did not create docs/docflow.md: %v", err)
	}
	skillDir, err := currentSkillDirectory()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := docskills.PlanProjectSkills(skillDir, root, "claude")
	if err != nil || plan.Conflict || len(plan.Copies) != 0 {
		t.Fatalf("installed project skills do not match source: %#v, %v", plan, err)
	}
}

func newLifecycleProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeLifecycleFile(t, root, "README.md", "# Project\n")
	if code, _, stderr := callLifecycleCLI(t, root, "init"); code != 0 || stderr != "" {
		t.Fatalf("init = (%d, %q)", code, stderr)
	}
	return root
}

func TestNewCreatesDraftAndUsesDefaults(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := newLifecycleProject(t)
	code, stdout, stderr := callLifecycleCLI(t, root, "new", "offline-cache", "--title", "Offline cache", "--type", "perf")
	if code != 0 || stdout != "docs/plans/P001-offline-cache.md\n" || stderr != "" {
		t.Fatalf("new = (%d, %q, %q)", code, stdout, stderr)
	}
	path := filepath.Join(root, "docs/plans/P001-offline-cache.md")
	plan, err := format.LoadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	wantMeta := map[string]string{"id": "P001", "title": "Offline cache", "type": "perf", "status": "draft", "created": time.Now().Format("2006-01-02")}
	for key, value := range wantMeta {
		if plan.Meta[key] != value {
			t.Errorf("plan meta %s = %q, want %q", key, plan.Meta[key], value)
		}
	}
	docflow, err := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
	if err != nil || len(docflow.Queue) != 1 || docflow.Queue[0].Text != "P001" {
		t.Fatalf("queue = %#v, %v", docflow, err)
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("lint = (%d, %q, %q)", code, stdout, stderr)
	}
	if code, _, _ := callLifecycleCLI(t, root, "next"); code != 1 {
		t.Fatalf("next on a draft plan returned %d, want 1", code)
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "new", "fix-login"); code != 0 || stdout != "docs/plans/P002-fix-login.md\n" || stderr != "" {
		t.Fatalf("new defaults = (%d, %q, %q)", code, stdout, stderr)
	}
	defaultPlan, err := format.LoadPlan(filepath.Join(root, "docs/plans/P002-fix-login.md"))
	if err != nil || defaultPlan.Meta["title"] != "fix login" || defaultPlan.Meta["type"] != "feature" {
		t.Fatalf("default plan = %#v, %v", defaultPlan, err)
	}
}

func TestNewFindsLargestIDInPlanNamesAndMetadata(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := newLifecycleProject(t)
	writeLifecycleFile(t, root, "docs/plans/P002-a.md", "---\nid: P002\n---\n")
	writeLifecycleFile(t, root, "docs/archive/P004-b.md", "---\nid: P004\n---\n")
	writeLifecycleFile(t, root, "docs/archive/note.md", "---\nid: P007\n---\n")
	if code, _, stderr := callLifecycleCLI(t, root, "new", "next-one"); code != 0 || stderr != "" {
		t.Fatalf("new = (%d, %q)", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/plans/P008-next-one.md")); err != nil {
		t.Fatalf("expected P008 plan: %v", err)
	}
	docflow, err := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
	if err != nil || len(docflow.Queue) != 1 || docflow.Queue[0].Text != "P008" {
		t.Fatalf("queue = %#v, %v", docflow, err)
	}
}

func TestNewRejectsBadSlugsAndTypes(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := newLifecycleProject(t)
	for _, slug := range []string{"Bad", "a_b", "-x", "x--y"} {
		if code, _, stderr := callLifecycleCLI(t, root, "new", slug); code != 2 {
			t.Errorf("new %q = (%d, %q)", slug, code, stderr)
		}
	}
	if code, _, _ := callLifecycleCLI(t, root, "new", "valid", "--type", "chore"); code != 2 {
		t.Errorf("new with invalid type returned %d, want 2", code)
	}
	plans, err := filepath.Glob(filepath.Join(root, "docs/plans", "*.md"))
	if err != nil || len(plans) != 0 {
		t.Fatalf("invalid new created plans %#v, %v", plans, err)
	}
}

func TestActivateDraftsAndReportsLintFindings(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	t.Run("draft becomes active", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": lifecyclePlan("P001", "draft", " ")}, true)
		if code, _, _ := callLifecycleCLI(t, root, "next"); code != 1 {
			t.Fatalf("next before activation = %d", code)
		}
		code, stdout, stderr := callLifecycleCLI(t, root, "activate", "P001")
		if code != 0 || stdout != "commit: P001: activate plan\n" || stderr != "" {
			t.Fatalf("activate = (%d, %q, %q)", code, stdout, stderr)
		}
		plan, _ := format.LoadPlan(filepath.Join(root, "docs/plans/P001-slug.md"))
		docflow, _ := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
		if plan.Meta["status"] != "active" || len(docflow.Queue) != 1 || docflow.Queue[0].Text != "P001" {
			t.Fatalf("activation state: plan=%#v queue=%#v", plan.Meta, docflow.Queue)
		}
		if code, stdout, _ := callLifecycleCLI(t, root, "next"); code != 0 || stdout != "P001.1.1\n" {
			t.Fatalf("next after activation = (%d, %q)", code, stdout)
		}
	})
	t.Run("adds missing queue item", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{
			"P001": lifecyclePlan("P001", "active", " "),
			"P002": lifecyclePlan("P002", "draft", " "),
		}, true)
		if code, _, stderr := callLifecycleCLI(t, root, "activate", "P002"); code != 0 || stderr != "" {
			t.Fatalf("activate = (%d, %q)", code, stderr)
		}
		docflow, _ := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
		if len(docflow.Queue) != 2 || docflow.Queue[0].Text != "P001" || docflow.Queue[1].Text != "P002" {
			t.Fatalf("queue = %#v", docflow.Queue)
		}
	})
	t.Run("lint errors keep draft", func(t *testing.T) {
		bad := strings.Replace(lifecyclePlan("P001", "draft", " "), "Commit: P001.1.1: unit 1\n", "", 1)
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": bad}, true)
		code, stdout, stderr := callLifecycleCLI(t, root, "activate", "P001")
		plan, _ := format.LoadPlan(filepath.Join(root, "docs/plans/P001-slug.md"))
		if code != 2 || stdout != "" || !strings.Contains(stderr, "error:") || plan.Meta["status"] != "draft" {
			t.Fatalf("activate invalid plan = (%d, %q, %q), status=%q", code, stdout, stderr, plan.Meta["status"])
		}
	})
	t.Run("missing read warns but activates", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": lifecyclePlan("P001", "draft", " ")}, true)
		if err := os.Remove(filepath.Join(root, "a.py")); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := callLifecycleCLI(t, root, "activate", "P001")
		plan, _ := format.LoadPlan(filepath.Join(root, "docs/plans/P001-slug.md"))
		if code != 0 || stdout != "commit: P001: activate plan\n" || !strings.Contains(stderr, "warning:") || plan.Meta["status"] != "active" {
			t.Fatalf("activate with missing Read = (%d, %q, %q), status=%q", code, stdout, stderr, plan.Meta["status"])
		}
	})
	t.Run("refuses active and frozen", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, []string{"P002"}, map[string]string{
			"P001": lifecyclePlan("P001", "active", " "),
			"P002": lifecyclePlan("P002", "frozen", " "),
		}, true)
		code, _, stderr := callLifecycleCLI(t, root, "activate", "P001")
		if code != 2 || !strings.Contains(stderr, "plan is active, not a draft") {
			t.Fatalf("activate active = (%d, %q)", code, stderr)
		}
		code, _, stderr = callLifecycleCLI(t, root, "activate", "P002")
		if code != 2 || !strings.Contains(stderr, "P002 is frozen; run docflow thaw P002") {
			t.Fatalf("activate frozen = (%d, %q)", code, stderr)
		}
	})
	t.Run("unknown plan", func(t *testing.T) {
		root := makeLifecycleProject(t, nil, nil, nil, true)
		code, _, stderr := callLifecycleCLI(t, root, "activate", "P009")
		if code != 2 || !strings.Contains(stderr, "P009: no plan in docs/plans/") {
			t.Fatalf("activate unknown = (%d, %q)", code, stderr)
		}
	})
}

func closeTestPlan(id string, emptyDecisions bool) string {
	decision := "- кэш в SQLite — почему — отвергнут файл\n  с продолжением\n- второе\n"
	if emptyDecisions {
		decision = ""
	}
	return fmt.Sprintf("---\nid: %s\ntitle: Offline cache\ntype: feature\nstatus: active\ncreated: 2026-10-12\n---\n## Goal\n\n## Context\n\n## Constraints\n\n## Traps\n- см. docs/plans/%s-offline-cache.md\n\n## Decisions\n%s\n## Phase 1: First\n\n### [x] %s.1.1 Done\nRead:\n- a.py\nDo:\n- b\nVerify: `true`\nCommit: %s.1.1: done\n\n### [-] %s.1.2 Skipped\nRead:\n- a.py\nDo:\n- b\nVerify: `true`\nCommit: %s.1.2: skipped\n", id, id, decision, id, id, id, id)
}

func closeLifecycleProject(t *testing.T, archiveDir bool) string {
	t.Helper()
	root := makeLifecycleProject(t, []string{"P001", "P002"}, nil, map[string]string{
		"P001": closeTestPlan("P001", false),
		"P002": lifecyclePlan("P002", "active", " "),
	}, archiveDir)
	initLifecycleGit(t, root)
	return root
}

func TestCloseArchivesFinishedPlansAndPreservesDecisions(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := closeLifecycleProject(t, true)
	code, stdout, stderr := callLifecycleCLI(t, root, "close", "P001")
	if code != 0 || stdout != "docs/archive/P001-slug.md\n" || stderr != "" {
		t.Fatalf("close = (%d, %q, %q)", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/plans/P001-slug.md")); !os.IsNotExist(err) {
		t.Fatalf("plan still exists: %v", err)
	}
	archive, err := format.LoadArchive(filepath.Join(root, "docs/archive/P001-slug.md"))
	if err != nil {
		t.Fatal(err)
	}
	if archive.Meta["id"] != "P001" || archive.Meta["title"] != "Offline cache" || archive.Meta["closed"] != time.Now().Format("2006-01-02") {
		t.Fatalf("archive metadata = %#v", archive.Meta)
	}
	if !strings.Contains(archive.Text(), "-->\n- кэш в SQLite — почему — отвергнут файл\n  с продолжением\n- второе\n\n## Gotchas") {
		t.Fatalf("archive lost decisions:\n%s", archive.Text())
	}
	docflow, err := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
	if err != nil || len(docflow.Queue) != 1 || docflow.Queue[0].Text != "P002" {
		t.Fatalf("queue after close = %#v, %v", docflow, err)
	}
	code, stdout, _ = callLifecycleCLI(t, root, "lint")
	if code != 2 || !strings.Contains(stdout, "empty section 'Outcome'") || !strings.Contains(stdout, "empty section 'Gotchas'") || !strings.Contains(stdout, "empty section 'Follow-ups'") || strings.Contains(stdout, "empty section 'Decisions'") {
		t.Fatalf("lint after close = (%d, %q)", code, stdout)
	}
}

func TestCloseRejectsOpenUnitsAndExternalReferences(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	t.Run("open units", func(t *testing.T) {
		for _, status := range []string{" ", "~", "!"} {
			plan := strings.Replace(closeTestPlan("P001", false), "[-] P001.1.2", "["+status+"] P001.1.2", 1)
			root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": plan}, true)
			initLifecycleGit(t, root)
			code, _, stderr := callLifecycleCLI(t, root, "close", "P001")
			if code != 5 || !strings.Contains(stderr, "units not [x]/[-]: P001.1.2") {
				t.Errorf("close with unit status %q = (%d, %q)", status, code, stderr)
			}
			if _, err := os.Stat(filepath.Join(root, "docs/plans/P001-slug.md")); err != nil {
				t.Errorf("open plan was removed for status %q: %v", status, err)
			}
		}
	})
	t.Run("references and id prefix", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": closeTestPlan("P001", false)}, true)
		initLifecycleGit(t, root)
		writeLifecycleFile(t, root, "README.md", "intro\nсм. docs/plans/P001-offline-cache.md\n")
		writeLifecycleFile(t, root, "docs/plans/P0012-other.md", "no reference\n")
		code, _, stderr := callLifecycleCLI(t, root, "close", "P001")
		if code != 2 || stderr != "README.md:2: error: references plans/P001\n" {
			t.Fatalf("close with reference = (%d, %q)", code, stderr)
		}
		if _, err := os.Stat(filepath.Join(root, "docs/plans/P001-slug.md")); err != nil {
			t.Fatalf("referenced plan was removed: %v", err)
		}
	})
	t.Run("other numeric prefix is not a reference", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": closeTestPlan("P001", false)}, true)
		initLifecycleGit(t, root)
		writeLifecycleFile(t, root, "README.md", "docs/plans/P0010-x.md\n")
		if code, _, stderr := callLifecycleCLI(t, root, "close", "P001"); code != 0 || stderr != "" {
			t.Fatalf("close beside P0010 = (%d, %q)", code, stderr)
		}
	})
	t.Run("hidden markdown is ignored", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": closeTestPlan("P001", false)}, true)
		initLifecycleGit(t, root)
		writeLifecycleFile(t, root, ".claude/notes.md", "docs/plans/P001-offline-cache.md\n")
		if code, _, stderr := callLifecycleCLI(t, root, "close", "P001"); code != 0 || stderr != "" {
			t.Fatalf("close with hidden reference = (%d, %q)", code, stderr)
		}
	})
	t.Run("empty decisions remain empty", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": closeTestPlan("P001", true)}, true)
		initLifecycleGit(t, root)
		if code, _, stderr := callLifecycleCLI(t, root, "close", "P001"); code != 0 || stderr != "" {
			t.Fatalf("close empty decisions = (%d, %q)", code, stderr)
		}
		code, stdout, _ := callLifecycleCLI(t, root, "lint")
		if code != 2 || !strings.Contains(stdout, "empty section 'Decisions'") {
			t.Fatalf("lint empty decisions = (%d, %q)", code, stdout)
		}
	})
	t.Run("unknown plan", func(t *testing.T) {
		root := makeLifecycleProject(t, nil, nil, nil, true)
		if code, _, stderr := callLifecycleCLI(t, root, "close", "P009"); code != 2 || !strings.Contains(stderr, "P009: no plan in docs/plans/") {
			t.Fatalf("close unknown = (%d, %q)", code, stderr)
		}
	})
}

func TestCloseAndNoteCreateMissingArchiveDirectory(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	t.Run("close", func(t *testing.T) {
		root := closeLifecycleProject(t, false)
		if code, stdout, stderr := callLifecycleCLI(t, root, "close", "P001"); code != 0 || stdout != "docs/archive/P001-slug.md\n" || stderr != "" {
			t.Fatalf("close = (%d, %q, %q)", code, stdout, stderr)
		}
		if _, err := os.Stat(filepath.Join(root, "docs/archive/P001-slug.md")); err != nil {
			t.Fatalf("archive not created: %v", err)
		}
	})
	t.Run("note", func(t *testing.T) {
		root := closeLifecycleProject(t, false)
		if code, stdout, stderr := callLifecycleCLI(t, root, "note", "drop-cache"); code != 0 || stdout != "docs/archive/P003-drop-cache.md\n" || stderr != "" {
			t.Fatalf("note = (%d, %q, %q)", code, stdout, stderr)
		}
		if _, err := os.Stat(filepath.Join(root, "docs/archive/P003-drop-cache.md")); err != nil {
			t.Fatalf("archive not created: %v", err)
		}
	})
}

func TestNoteCreatesArchiveAndValidatesSlug(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := closeLifecycleProject(t, true)
	writeLifecycleFile(t, root, "docs/archive/P004-old.md", "---\nid: P004\n---\n")
	code, stdout, stderr := callLifecycleCLI(t, root, "note", "drop-cache", "--title", "Drop cache")
	if code != 0 || stdout != "docs/archive/P005-drop-cache.md\n" || stderr != "" {
		t.Fatalf("note = (%d, %q, %q)", code, stdout, stderr)
	}
	archive, err := format.LoadArchive(filepath.Join(root, "docs/archive/P005-drop-cache.md"))
	if err != nil || archive.Meta["title"] != "Drop cache" || archive.Meta["closed"] != time.Now().Format("2006-01-02") {
		t.Fatalf("archive = %#v, %v", archive, err)
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "note", "drop-cache"); code != 0 || stdout != "docs/archive/P006-drop-cache.md\n" || stderr != "" {
		t.Fatalf("note default title = (%d, %q, %q)", code, stdout, stderr)
	}
	defaultArchive, err := format.LoadArchive(filepath.Join(root, "docs/archive/P006-drop-cache.md"))
	if err != nil || defaultArchive.Meta["title"] != "drop cache" {
		t.Fatalf("default archive = %#v, %v", defaultArchive, err)
	}
	if code, _, stderr := callLifecycleCLI(t, root, "note", "Bad Slug"); code != 2 || !strings.Contains(stderr, "bad slug") {
		t.Fatalf("bad slug = (%d, %q)", code, stderr)
	}
}

func TestNoteWorksWithoutInitializedProject(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := t.TempDir()
	code, stdout, stderr := callLifecycleCLI(t, root, "note", "early-decision")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "docs/docflow.md not found in ") || !strings.Contains(stderr, "or above; run docflow init") {
		t.Fatalf("note without docflow = (%d, %q, %q)", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/archive/P001-early-decision.md")); !os.IsNotExist(err) {
		t.Fatalf("note created an archive without a project: %v", err)
	}
}

func TestFreezeThawRoundTripAndValidation(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := makeLifecycleProject(t, []string{"P001", "P002", "P003"}, nil, map[string]string{
		"P001": lifecyclePlan("P001", "active", " "),
		"P002": lifecyclePlan("P002", "active", " "),
		"P003": lifecyclePlan("P003", "active", " "),
	}, true)
	code, stdout, stderr := callLifecycleCLI(t, root, "freeze", "P001", "--reason", "waiting for API")
	if code != 0 || stdout != "docs/plans/P001-slug.md\ncommit: P001: freeze plan\n" || stderr != "" {
		t.Fatalf("freeze = (%d, %q, %q)", code, stdout, stderr)
	}
	docflow, _ := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
	if len(docflow.Queue) != 2 || docflow.Queue[0].Text != "P002" || len(docflow.Frozen) != 1 || docflow.Frozen[0].Text != "P001 — waiting for API" {
		t.Fatalf("after freeze queue=%#v frozen=%#v", docflow.Queue, docflow.Frozen)
	}
	plan, _ := format.LoadPlan(filepath.Join(root, "docs/plans/P001-slug.md"))
	if plan.Meta["status"] != "frozen" {
		t.Fatalf("plan status after freeze = %q", plan.Meta["status"])
	}
	text, _ := os.ReadFile(filepath.Join(root, "docs/docflow.md"))
	if strings.Index(string(text), "## Queue") > strings.Index(string(text), "## Frozen") || strings.Index(string(text), "## Frozen") > strings.Index(string(text), "## Backlog") || !strings.Contains(string(text), "- some task") {
		t.Fatalf("section order or backlog changed: %s", text)
	}
	if code, _, stderr := callLifecycleCLI(t, root, "freeze", "P003"); code != 0 || stderr != "" {
		t.Fatalf("freeze without reason = (%d, %q)", code, stderr)
	}
	code, stdout, stderr = callLifecycleCLI(t, root, "thaw", "P001")
	if code != 0 || stdout != "commit: P001: thaw plan\n" || stderr != "" {
		t.Fatalf("thaw = (%d, %q, %q)", code, stdout, stderr)
	}
	docflow, _ = format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
	if len(docflow.Queue) != 2 || docflow.Queue[0].Text != "P002" || docflow.Queue[1].Text != "P001" || len(docflow.Frozen) != 1 || docflow.Frozen[0].Text != "P003" {
		t.Fatalf("after thaw queue=%#v frozen=%#v", docflow.Queue, docflow.Frozen)
	}
	plan, _ = format.LoadPlan(filepath.Join(root, "docs/plans/P001-slug.md"))
	if plan.Meta["status"] != "active" {
		t.Fatalf("plan status after thaw = %q", plan.Meta["status"])
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("lint = (%d, %q, %q)", code, stdout, stderr)
	}
}

func TestFreezeRejectsBadStateAndProtectsInProgressWork(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	t.Run("draft and absent IDs", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": lifecyclePlan("P001", "draft", " ")}, true)
		if code, _, stderr := callLifecycleCLI(t, root, "freeze", "P001"); code != 2 || !strings.Contains(stderr, "plan is a draft") {
			t.Errorf("freeze draft = (%d, %q)", code, stderr)
		}
		if code, _, stderr := callLifecycleCLI(t, root, "freeze", "P009"); code != 2 || !strings.Contains(stderr, "P009 is not in Queue") {
			t.Errorf("freeze absent = (%d, %q)", code, stderr)
		}
		if code, _, stderr := callLifecycleCLI(t, root, "thaw", "P001"); code != 2 || !strings.Contains(stderr, "P001 is not in Frozen") {
			t.Errorf("thaw absent = (%d, %q)", code, stderr)
		}
	})
	t.Run("clean in-progress plan is reset", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": lifecyclePlan("P001", "active", "~")}, true)
		initLifecycleGit(t, root)
		if code, _, stderr := callLifecycleCLI(t, root, "freeze", "P001"); code != 0 || stderr != "" {
			t.Fatalf("freeze clean in-progress = (%d, %q)", code, stderr)
		}
		plan, _ := format.LoadPlan(filepath.Join(root, "docs/plans/P001-slug.md"))
		if plan.Unit("P001.1.1").Status != " " {
			t.Fatalf("unit status after freeze = %q", plan.Unit("P001.1.1").Status)
		}
	})
	t.Run("dirty in-progress plan remains active", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": lifecyclePlan("P001", "active", "~")}, true)
		initLifecycleGit(t, root)
		writeLifecycleFile(t, root, "a.py", "x = 2\n")
		code, _, stderr := callLifecycleCLI(t, root, "freeze", "P001")
		plan, _ := format.LoadPlan(filepath.Join(root, "docs/plans/P001-slug.md"))
		docflow, _ := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
		if code != 5 || !strings.Contains(stderr, "P001.1.1 is in progress with uncommitted work") || !strings.Contains(stderr, "a.py") || plan.Unit("P001.1.1").Status != "~" || plan.Meta["status"] != "active" || len(docflow.Queue) != 1 || len(docflow.Frozen) != 0 {
			t.Fatalf("freeze dirty = (%d, %q), plan=%#v queue=%#v frozen=%#v", code, stderr, plan, docflow.Queue, docflow.Frozen)
		}
	})
}

func TestThawPrintsReferenceWarnings(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := makeLifecycleProject(t, []string{"P002"}, []string{"P001"}, map[string]string{
		"P001": lifecyclePlan("P001", "frozen", " "),
		"P002": lifecyclePlan("P002", "active", " "),
	}, true)
	if err := os.Remove(filepath.Join(root, "a.py")); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := callLifecycleCLI(t, root, "thaw", "P001")
	if code != 0 || stdout != "commit: P001: thaw plan\n" || !strings.Contains(stderr, "P001-slug.md") || !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "a.py") {
		t.Fatalf("thaw missing read = (%d, %q, %q)", code, stdout, stderr)
	}
}

func TestFrozenPlansAreSkippedAndLinted(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	t.Run("status and next skip frozen plan", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P002"}, []string{"P001 — later"}, map[string]string{
			"P001": lifecyclePlan("P001", "frozen", " "),
			"P002": lifecyclePlan("P002", "active", " "),
		}, true)
		code, stdout, stderr := callLifecycleCLI(t, root, "status")
		lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
		if code != 0 || stderr != "" || len(lines) != 4 || !strings.HasPrefix(lines[0], "P002 ") || lines[1] != "frozen: P001" || !strings.HasPrefix(lines[2], "current: P002.1.1") || !strings.Contains(lines[3], "lint: ok") || strings.Contains(stdout, "Plan P001") {
			t.Fatalf("status = (%d, %q, %q)", code, stdout, stderr)
		}
		if code, stdout, stderr := callLifecycleCLI(t, root, "next"); code != 0 || stdout != "P002.1.1\n" || stderr != "" {
			t.Fatalf("next = (%d, %q, %q)", code, stdout, stderr)
		}
	})
	t.Run("explicit commands reject frozen unit", func(t *testing.T) {
		root := makeLifecycleProject(t, []string{"P002"}, []string{"P001 — later"}, map[string]string{
			"P001": lifecyclePlan("P001", "frozen", " "),
			"P002": lifecyclePlan("P002", "active", " "),
		}, true)
		for _, args := range [][]string{{"prompt", "P001.1.1"}, {"verify", "P001.1.1"}, {"done", "P001.1.1", "--handoff", "x"}} {
			code, _, stderr := callLifecycleCLI(t, root, args...)
			if code != 2 || !strings.Contains(stderr, "P001 is frozen; run docflow thaw P001") {
				t.Errorf("%v = (%d, %q)", args, code, stderr)
			}
		}
		plan, _ := format.LoadPlan(filepath.Join(root, "docs/plans/P001-slug.md"))
		if plan.Unit("P001.1.1").Status != " " {
			t.Fatalf("frozen unit changed status to %q", plan.Unit("P001.1.1").Status)
		}
	})
	t.Run("lint reports membership inconsistencies", func(t *testing.T) {
		cases := []struct {
			name   string
			queue  []string
			frozen []string
			plans  map[string]string
			want   []string
		}{
			{name: "frozen in queue", queue: []string{"P001"}, plans: map[string]string{"P001": lifecyclePlan("P001", "frozen", " ")}, want: []string{"plan P001 is frozen but in Queue"}},
			{name: "active in frozen", frozen: []string{"P001"}, plans: map[string]string{"P001": lifecyclePlan("P001", "active", " ")}, want: []string{"plan P001 is in Frozen but its status is not frozen"}},
			{name: "both sections", queue: []string{"P001"}, frozen: []string{"P001 — x"}, plans: map[string]string{"P001": lifecyclePlan("P001", "frozen", " ")}, want: []string{"P001 in both Queue and Frozen"}},
			{name: "bad and unknown IDs", frozen: []string{"X1 — bad", "P007"}, want: []string{"bad plan id 'X1'", "P007 in Frozen has no plan in docs/plans/"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				root := makeLifecycleProject(t, tc.queue, tc.frozen, tc.plans, true)
				code, stdout, _ := callLifecycleCLI(t, root, "lint")
				if code != 2 {
					t.Fatalf("lint returned %d, output %q", code, stdout)
				}
				for _, want := range tc.want {
					if !strings.Contains(stdout, want) {
						t.Errorf("lint output %q does not contain %q", stdout, want)
					}
				}
			})
		}
	})
	t.Run("frozen section order", func(t *testing.T) {
		root := t.TempDir()
		writeLifecycleFile(t, root, "docs/docflow.md", "---\ndocflow: 2\nread_budget: 40000\n---\n## Frozen\n\n## Queue\n\n## Backlog\n")
		code, stdout, _ := callLifecycleCLI(t, root, "lint")
		if code != 2 || !strings.Contains(stdout, "out of order") {
			t.Fatalf("lint section order = (%d, %q)", code, stdout)
		}
	})
	t.Run("frozen plans do not report missing reads", func(t *testing.T) {
		root := makeLifecycleProject(t, nil, []string{"P001"}, map[string]string{"P001": lifecyclePlan("P001", "frozen", " ")}, true)
		if err := os.Remove(filepath.Join(root, "a.py")); err != nil {
			t.Fatal(err)
		}
		if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("lint frozen missing Read = (%d, %q, %q)", code, stdout, stderr)
		}
	})
}

func TestCloseRemovesFrozenPlanAndMembership(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := makeLifecycleProject(t, nil, []string{"P001 — done elsewhere"}, map[string]string{"P001": lifecyclePlan("P001", "frozen", "x")}, true)
	initLifecycleGit(t, root)
	if code, stdout, stderr := callLifecycleCLI(t, root, "close", "P001"); code != 0 || stdout != "docs/archive/P001-slug.md\n" || stderr != "" {
		t.Fatalf("close frozen = (%d, %q, %q)", code, stdout, stderr)
	}
	docflow, err := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
	if err != nil || len(docflow.Queue) != 0 || len(docflow.Frozen) != 0 {
		t.Fatalf("membership after close = %#v, %v", docflow, err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/plans/P001-slug.md")); !os.IsNotExist(err) {
		t.Fatalf("plan still exists: %v", err)
	}
}

func TestMigrateOneToTwoChangesOnlyVersionMarker(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	root := t.TempDir()
	docflowV1 := strings.Replace(lifecycleDocflow([]string{"P001"}, nil), "docflow: 2", "docflow: 1", 1)
	plan := lifecyclePlan("P001", "draft", " ")
	writeLifecycleFile(t, root, "docs/docflow.md", docflowV1)
	writeLifecycleFile(t, root, "docs/plans/P001-slug.md", plan)
	if code, _, stderr := callLifecycleCLI(t, root, "lint"); code != 2 || !strings.Contains(stderr, "format version 1, expected 2; run docflow migrate") {
		t.Fatalf("lint legacy = (%d, %q)", code, stderr)
	}
	code, stdout, stderr := callLifecycleCLI(t, root, "migrate")
	if code != 0 || stdout != "migrated 1 -> 2\n" || stderr != "" {
		t.Fatalf("migrate = (%d, %q, %q)", code, stdout, stderr)
	}
	want := strings.Replace(docflowV1, "docflow: 1", "docflow: 2", 1)
	if got, _ := os.ReadFile(filepath.Join(root, "docs/docflow.md")); string(got) != want {
		t.Fatalf("migrated docflow differs beyond marker:\n%s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "docs/plans/P001-slug.md")); string(got) != plan {
		t.Fatal("migration changed the plan")
	}
	if code, stdout, stderr := callLifecycleCLI(t, root, "migrate"); code != 1 || stdout != "" || stderr != "nothing to migrate\n" {
		t.Fatalf("repeat migrate = (%d, %q, %q)", code, stdout, stderr)
	}
}

func TestInitAndNewWriteCurrentFormatAndExecutorLint(t *testing.T) {
	t.Setenv("DOCFLOW_RUNNER_UNIT", "")
	t.Run("init current format", func(t *testing.T) {
		root := t.TempDir()
		writeLifecycleFile(t, root, "README.md", "# Project\n")
		if code, _, stderr := callLifecycleCLI(t, root, "init"); code != 0 || stderr != "" {
			t.Fatalf("init = (%d, %q)", code, stderr)
		}
		docflow, err := format.LoadDocflow(filepath.Join(root, "docs/docflow.md"))
		if err != nil || docflow.Meta["docflow"] != format.DocflowVersion || format.DocflowVersion != "2" {
			t.Fatalf("format version = %v, %v", docflow, err)
		}
	})
	t.Run("new skeleton and executor assignments", func(t *testing.T) {
		root := newLifecycleProject(t)
		if code, _, stderr := callLifecycleCLI(t, root, "new", "cache"); code != 0 || stderr != "" {
			t.Fatalf("new = (%d, %q)", code, stderr)
		}
		writeLifecycleFile(t, root, "a.txt", "a\n")
		if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("lint skeleton = (%d, %q, %q)", code, stdout, stderr)
		}
		path := filepath.Join(root, "docs/plans/P001-cache.md")
		units := "## Phase 1: First\n\n" +
			"### [ ] P001.1.1 Done unit\nRead:\n- a.txt\nDo:\n- write a.txt\nVerify: `true`\nModel: strong\nCommit: P001.1.1: done unit\n\n" +
			"### [ ] P001.1.2 Inherits\nRead:\n- a.txt\nDo:\n- write b.txt\nVerify: `true`\nCommit: P001.1.2: inherits\n\n" +
			"### [ ] P001.1.3 Overrides\nRead:\n- a.txt\nDo:\n- write c.txt\nVerify: `true`\nExecutor: codex | gpt-5.5 | -\nCommit: P001.1.3: overrides\n"
		original, _ := os.ReadFile(path)
		if err := os.WriteFile(path, append(append([]byte(nil), original...), []byte("\n"+units)...), 0o644); err != nil {
			t.Fatal(err)
		}
		code, stdout, _ := callLifecycleCLI(t, root, "lint")
		if code != 2 || !strings.Contains(stdout, "unit P001.1.1: Model: conflicts") || !strings.Contains(stdout, "unit P001.1.2: no executor") {
			t.Fatalf("lint executor omissions = (%d, %q)", code, stdout)
		}
		plan, _ := os.ReadFile(path)
		text := strings.Replace(string(plan), "created: "+time.Now().Format("2006-01-02")+"\n---", "created: "+time.Now().Format("2006-01-02")+"\nexecutor: claude | claude-opus-5-5 | high\n---", 1)
		text = strings.Replace(text, "Model: strong\n", "", 1)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, stdout, stderr := callLifecycleCLI(t, root, "lint"); code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("lint complete executor assignments = (%d, %q, %q)", code, stdout, stderr)
		}
	})
}
