package format

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"bsv-docflow/internal/clierrors"
)

var sampleLines = []string{
	"---",
	"id: P007",
	"title: Offline cache for feed",
	"type: feature",
	"status: active",
	"created: 2026-10-12",
	"---",
	"## Goal",
	"Кэш ленты офлайн.",
	"",
	"````markdown",
	"## Goal",
	"### [ ] P999.1.1 Fake unit inside fence",
	"Read:",
	"- nowhere",
	"````",
	"",
	"## Context",
	"- docs/architecture.md#storage",
	"",
	"## Constraints",
	"",
	"## Prerequisites",
	"- Тестовый аккаунт: `cat ~/logins/rmg.txt`",
	"- Симулятор запущен",
	"  и разблокирован",
	"",
	"## Traps",
	"",
	"## Decisions",
	"",
	"## Phase 1: Storage layer",
	"",
	"### [x] P007.1.1 Add SQLDelight schema",
	"Read:",
	"- shared/src/FeedRepository.kt:L1-80",
	"- docs/architecture.md#storage",
	"Do:",
	"- Создать схему `feed`,",
	"  с индексом по `created`.",
	"- Второй пункт",
	"",
	"Свободный текст после пустой строки.",
	"Verify: `./gradlew :shared:allTests`",
	"Traps: одна строка",
	"Depends: -",
	"Executor: codex | gpt-5.5 | high",
	"Model: strong",
	"Role: heavy",
	"Commit: P007.1.1: add feed cache schema",
	"Handoff:",
	"- кэш пустой при первом запуске",
	"",
	"### [~] P007.1.2 Fill cache",
	"Read:",
	"- shared/src/Feed.kt",
	"Not a field line",
	"- orphan item",
	"Do:",
	"- Заполнить кэш",
	"Verify: `./gradlew test`",
	"Commit: P007.1.2: fill feed cache",
	"",
	"## Phase 2: Sync",
	"",
	"### [ ] P007.2.1 Sync on start",
	"Read:",
	"- a.kt",
	"Do:",
	"- b",
	"Verify: `true`",
	"Depends: P007.1.2",
	"Commit: P007.2.1: sync on start",
	"",
	"### [-] P007.2.2 Skipped unit",
	"Read:",
	"- a.kt",
	"Do:",
	"- b",
	"Verify: `true`",
	"Commit: P007.2.2: skipped",
	"",
	"### [!] P007.2.3 Blocked unit",
	"Read:",
	"- a.kt",
	"Do:",
	"- b",
	"Verify: `true`",
	"Commit: P007.2.3: blocked",
}

const samplePrerequisites = "## Prerequisites\n" +
	"- Тестовый аккаунт: `cat ~/logins/rmg.txt`\n" +
	"- Симулятор запущен\n" +
	"  и разблокирован\n\n"

func sampleText() string { return strings.Join(sampleLines, "\n") + "\n" }

func parseSample(t *testing.T) *Plan {
	t.Helper()
	plan, err := ParsePlan(sampleText(), "docs/plans/P007-offline-cache.md")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func renderTemplate(t *testing.T, name string, values map[string]string) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	content, err := os.ReadFile(filepath.Join(root, "templates", name))
	if err != nil {
		t.Fatal(err)
	}
	result := string(content)
	for key, value := range values {
		result = strings.ReplaceAll(result, "{"+key+"}", value)
	}
	return result
}

func TestDocflowTemplate(t *testing.T) {
	doc, err := ParseDocflow(renderTemplate(t, "docflow.md", nil), "docs/docflow.md")
	if err != nil {
		t.Fatal(err)
	}
	wantMeta := map[string]string{"docflow": "3", "read_budget": "40000", "start_read": "README.md", "start_budget": "4000"}
	if !reflect.DeepEqual(doc.Meta, wantMeta) {
		t.Fatalf("meta = %#v, want %#v", doc.Meta, wantMeta)
	}
	if got := sectionNames(doc.Sections); !reflect.DeepEqual(got, []string{"Queue", "Backlog"}) {
		t.Fatalf("sections = %#v", got)
	}
	if len(doc.Queue) != 0 || len(doc.Backlog) != 0 {
		t.Fatalf("template items = queue %#v backlog %#v", doc.Queue, doc.Backlog)
	}
}

func TestPlanTemplate(t *testing.T) {
	text := renderTemplate(t, "plan.md", map[string]string{"id": "P007", "title": "Offline cache for feed", "type": "feature", "date": "2026-10-12"})
	plan, err := ParsePlan(text, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	wantMeta := map[string]string{"id": "P007", "title": "Offline cache for feed", "type": "feature", "status": "draft", "created": "2026-10-12"}
	if !reflect.DeepEqual(plan.Meta, wantMeta) {
		t.Fatalf("meta = %#v, want %#v", plan.Meta, wantMeta)
	}
	if got := sectionNames(plan.Sections); !reflect.DeepEqual(got, []string{"Goal", "Context", "Constraints", "Traps", "Decisions"}) {
		t.Fatalf("sections = %#v", got)
	}
	if len(plan.Units) != 0 {
		t.Fatalf("template units = %#v", plan.Units)
	}
}

func TestArchiveTemplate(t *testing.T) {
	text := renderTemplate(t, "archive.md", map[string]string{"id": "P007", "title": "T", "date": "2026-11-02"})
	doc, err := ParseArchive(text, "a.md")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Meta["closed"] != "2026-11-02" {
		t.Fatalf("closed = %q", doc.Meta["closed"])
	}
	want := []string{"Outcome", "Decisions", "Gotchas", "Follow-ups"}
	if got := sectionNames(doc.Sections); !reflect.DeepEqual(got, want) {
		t.Fatalf("sections = %#v, want %#v", got, want)
	}
}

func TestRepoDocsParse(t *testing.T) {
	root := filepath.Clean(filepath.Join(filepath.Dir(mustSource(t)), "../.."))
	docPath := filepath.Join(root, "docs", "docflow.md")
	doc, err := LoadDocflow(docPath)
	if err != nil {
		t.Fatal(err)
	}
	original, err := ReadText(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Text() != original {
		t.Fatal("docflow text changed while parsing")
	}
	plans, err := filepath.Glob(filepath.Join(root, "docs", "plans", "P*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range plans {
		plan, err := LoadPlan(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(plan.Units) == 0 {
			t.Errorf("%s has no units", path)
		}
		content, err := ReadText(path)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Text() != content {
			t.Errorf("%s changed while parsing", path)
		}
	}
}

func TestMissing(t *testing.T) {
	_, err := ParsePlan("## Goal\n", "docs/plans/P007-offline-cache.md")
	format, ok := err.(*clierrors.FormatError)
	if err == nil || !ok || format.Line != 1 {
		t.Fatalf("error = %#v, want format error on line 1", err)
	}
}

func TestUnterminated(t *testing.T) {
	if _, err := ParsePlan("---\nid: P007\n## Goal\n", "p.md"); err == nil {
		t.Fatal("ParsePlan() error = nil")
	}
}

func TestBadLine(t *testing.T) {
	_, err := ParsePlan("---\nid: P007\n- list\n---\n", "docs/plans/P007-offline-cache.md")
	if err == nil || !strings.Contains(err.Error(), "docs/plans/P007-offline-cache.md:3: error:") {
		t.Fatalf("error = %v", err)
	}
}

func TestMetaLines(t *testing.T) {
	plan := parseSample(t)
	if plan.Meta["status"] != "active" || plan.MetaLines["status"] != 5 {
		t.Fatalf("status meta = %q at %d", plan.Meta["status"], plan.MetaLines["status"])
	}
}

func TestInsertMeta(t *testing.T) {
	plan := parseSample(t)
	if err := plan.InsertMeta(0, "executor", "pi | zai/m | high"); err != nil {
		t.Fatalf("InsertMeta(0) error = %v", err)
	}
	if plan.Meta["executor"] != "pi | zai/m | high" || plan.MetaLines["executor"] != 7 {
		t.Fatalf("executor meta = %q at %d, want line 7", plan.Meta["executor"], plan.MetaLines["executor"])
	}
	if err := plan.InsertMeta(plan.MetaLines["executor"], "fallback", "codex | c | high"); err != nil {
		t.Fatalf("InsertMeta(after executor) error = %v", err)
	}
	if plan.Meta["fallback"] != "codex | c | high" || plan.MetaLines["fallback"] != 8 {
		t.Fatalf("fallback meta = %q at %d, want line 8", plan.Meta["fallback"], plan.MetaLines["fallback"])
	}
	if plan.MetaLines["created"] != 6 || plan.MetaLines["status"] != 5 {
		t.Fatalf("created at %d, status at %d, want 6 and 5", plan.MetaLines["created"], plan.MetaLines["status"])
	}
	body := strings.Join(plan.Raw[9:], "")
	if !strings.HasPrefix(body, "## Goal") {
		t.Fatalf("body after frontmatter = %q", body)
	}
}

func TestSectionsSkipFencedCode(t *testing.T) {
	plan := parseSample(t)
	want := []string{"Goal", "Context", "Constraints", "Prerequisites", "Traps", "Decisions", "Phase 1: Storage layer", "Phase 2: Sync"}
	if got := sectionNames(plan.Sections); !reflect.DeepEqual(got, want) {
		t.Fatalf("sections = %#v", got)
	}
}

func TestSectionLines(t *testing.T) {
	plan := parseSample(t)
	goal, context := plan.Sections[0], plan.Sections[1]
	if goal.Line != 8 || goal.End != context.Line-1 || plan.Sections[len(plan.Sections)-1].End != len(plan.Raw) {
		t.Fatalf("section lines = %#v / %#v", goal, context)
	}
}

func TestPhasesAndUnits(t *testing.T) {
	plan := parseSample(t)
	if len(plan.Phases) != 2 || plan.Phases[0].Number != 1 || plan.Phases[0].Title != "Storage layer" || plan.Phases[1].Number != 2 || plan.Phases[1].Title != "Sync" {
		t.Fatalf("phases = %#v", plan.Phases)
	}
	ids := []string{plan.Phases[0].Units[0].ID, plan.Phases[0].Units[1].ID}
	if !reflect.DeepEqual(ids, []string{"P007.1.1", "P007.1.2"}) || plan.Unit("P999.1.1") != nil {
		t.Fatalf("phase units = %#v", ids)
	}
	unit := plan.Unit("P007.1.1")
	if unit.Title != "Add SQLDelight schema" || unit.Phase == nil || *unit.Phase != 1 || strings.TrimSpace(plan.Raw[unit.Line-1]) != "### [x] P007.1.1 Add SQLDelight schema" {
		t.Fatalf("unit = %#v", unit)
	}
	if strings.TrimSpace(plan.Raw[unit.End-1]) != "- кэш пустой при первом запуске" {
		t.Fatalf("unit end line = %q", plan.Raw[unit.End-1])
	}
}

func TestStatuses(t *testing.T) {
	plan := parseSample(t)
	got := make([]string, len(plan.Units))
	for i, unit := range plan.Units {
		got[i] = unit.Status
	}
	if !reflect.DeepEqual(got, []string{"x", "~", " ", "-", "!"}) {
		t.Fatalf("statuses = %#v", got)
	}
	if len(Statuses) != 5 {
		t.Fatalf("Statuses = %#v", Statuses)
	}
	for _, status := range []string{" ", "~", "x", "-", "!"} {
		if _, ok := Statuses[status]; !ok {
			t.Errorf("missing status %q", status)
		}
	}
}

func TestPrerequisites(t *testing.T) {
	plan := parseSample(t)
	got := PlanPrerequisites(plan)
	if len(got) != 2 || got[0].Text != "Тестовый аккаунт" || got[0].Command == nil || *got[0].Command != "cat ~/logins/rmg.txt" || got[1].Text != "Симулятор запущен и разблокирован" || got[1].Command != nil {
		t.Fatalf("prerequisites = %#v", got)
	}
}

func TestPrerequisitesOnce(t *testing.T) {
	text := strings.Replace(sampleText(), "- Тестовый аккаунт: `cat ~/logins/rmg.txt`", "- Тестовый аккаунт: `cat ~/logins/rmg.txt` (once)", 1)
	text = strings.Replace(text, "  и разблокирован", "  и разблокирован (once)", 1)
	plan, err := ParsePlan(text, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	got := PlanPrerequisites(plan)
	if len(got) != 2 || got[0].Text != "Тестовый аккаунт" || got[0].Command == nil || *got[0].Command != "cat ~/logins/rmg.txt" || !got[0].Once || got[1].Text != "Симулятор запущен и разблокирован" || got[1].Command != nil || !got[1].Once {
		t.Fatalf("prerequisites = %#v", got)
	}
}

func TestPlanStarted(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{{" ", false}, {"-", false}, {"~", true}, {"!", true}, {"x", true}} {
		t.Run(Statuses[tc.status], func(t *testing.T) {
			plan := parseSample(t)
			ids := make([]string, len(plan.Units))
			for i, unit := range plan.Units {
				ids[i] = unit.ID
			}
			for _, id := range ids {
				if err := plan.SetStatus(id, " "); err != nil {
					t.Fatal(err)
				}
			}
			if len(ids) == 0 {
				t.Fatal("sample plan has no units")
			}
			if err := plan.SetStatus(ids[0], tc.status); err != nil {
				t.Fatal(err)
			}
			if got := PlanStarted(plan); got != tc.want {
				t.Fatalf("PlanStarted() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestPrerequisitesAbsent(t *testing.T) {
	text := strings.Replace(sampleText(), samplePrerequisites, "", 1)
	plan, err := ParsePlan(text, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := PlanPrerequisites(plan); len(got) != 0 {
		t.Fatalf("prerequisites = %#v", got)
	}
}

func TestPermissionsDefault(t *testing.T) {
	plan := parseSample(t)
	if got := PlanPermissions(plan); got != "auto" {
		t.Fatalf("permissions = %q", got)
	}
	text := strings.Replace(sampleText(), "status: active", "status: active\npermissions: full", 1)
	plan, err := ParsePlan(text, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := PlanPermissions(plan); got != "full" {
		t.Fatalf("permissions = %q", got)
	}
}

func TestOrderAndValues(t *testing.T) {
	unit := parseSample(t).Unit("P007.1.1")
	keys := make([]string, len(unit.Fields))
	for i, field := range unit.Fields {
		keys[i] = field.Key
	}
	if !reflect.DeepEqual(keys, UnitFields) {
		t.Fatalf("keys = %#v, want %#v", keys, UnitFields)
	}
	checks := map[string]string{"Verify": "`./gradlew :shared:allTests`", "Commit": "P007.1.1: add feed cache schema", "Traps": "одна строка", "Model": "strong", "Executor": "codex | gpt-5.5 | high"}
	for key, want := range checks {
		if unit.Field(key) == nil || unit.Field(key).Value != want {
			t.Errorf("%s = %#v, want %q", key, unit.Field(key), want)
		}
	}
	if unit.Field("Traps").Items == nil || unit.Field("Read").Value != "" || unit.Field("Missing") != nil {
		t.Fatal("empty items, empty value, or missing field mismatch")
	}
}

func TestItemsWithLines(t *testing.T) {
	read := parseSample(t).Unit("P007.1.1").Field("Read")
	if !reflect.DeepEqual(itemTexts(read.Items), []string{"shared/src/FeedRepository.kt:L1-80", "docs/architecture.md#storage"}) || read.Items[0].Line != read.Line+1 || read.End != read.Line+2 {
		t.Fatalf("Read field = %#v", read)
	}
}

func TestContinuationAndBlankLineEnd(t *testing.T) {
	do := parseSample(t).Unit("P007.1.1").Field("Do")
	want := []string{"Создать схему `feed`, с индексом по `created`.", "Второй пункт"}
	if !reflect.DeepEqual(itemTexts(do.Items), want) || do.End != do.Line+3 {
		t.Fatalf("Do field = %#v", do)
	}
}

func TestInlineValueWrapsWithoutIndent(t *testing.T) {
	text := strings.Replace(sampleText(), "Traps: одна строка\n", "Traps: одна строка\nи вторая\n", 1)
	plan, err := ParsePlan(text, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	traps := plan.Unit("P007.1.1").Field("Traps")
	if traps.Value != "одна строка и вторая" || traps.End != traps.Line+1 || plan.Unit("P007.1.1").Field("Depends").Value != "-" {
		t.Fatalf("Traps = %#v", traps)
	}
}

func TestNonItemLineEndsField(t *testing.T) {
	read := parseSample(t).Unit("P007.1.2").Field("Read")
	if !reflect.DeepEqual(itemTexts(read.Items), []string{"shared/src/Feed.kt"}) || read.End != read.Line+1 {
		t.Fatalf("Read = %#v", read)
	}
}

func TestItemWrappedOverSeveralLines(t *testing.T) {
	text := strings.Replace(sampleText(), "  с индексом по `created`.\n", "  с индексом по `created`;\n  плейсхолдеры `{id}`, `{title}`\n  в шаблоне.\n", 1)
	plan, err := ParsePlan(text, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	do := plan.Unit("P007.1.1").Field("Do")
	want := []string{"Создать схему `feed`, с индексом по `created`; плейсхолдеры `{id}`, `{title}` в шаблоне.", "Второй пункт"}
	if !reflect.DeepEqual(itemTexts(do.Items), want) {
		t.Fatalf("Do items = %#v", itemTexts(do.Items))
	}
}

func TestRoundTrip(t *testing.T) {
	text := sampleText()
	for _, input := range []string{text, strings.ReplaceAll(text, "\n", "\r\n"), strings.TrimSuffix(text, "\n")} {
		plan, err := ParsePlan(input, "p.md")
		if err != nil {
			t.Fatal(err)
		}
		if plan.Text() != input {
			t.Fatal("round trip changed source bytes")
		}
	}
}

func TestSetStatus(t *testing.T) {
	plan := parseSample(t)
	before := plan.Text()
	line := plan.Unit("P007.2.1").Line
	if err := plan.SetStatus("P007.2.1", "~"); err != nil {
		t.Fatal(err)
	}
	if plan.Unit("P007.2.1").Status != "~" {
		t.Fatal("status was not updated")
	}
	if got := changedLines(before, plan.Text()); !reflect.DeepEqual(got, []int{line}) {
		t.Fatalf("changed lines = %#v", got)
	}
	if err := plan.SetStatus("P007.2.1", "?"); err == nil {
		t.Fatal("unknown status was accepted")
	}
	if err := plan.SetStatus("P007.9.9", "x"); err == nil {
		t.Fatal("unknown unit was accepted")
	}
}

func TestSetStatusCRLF(t *testing.T) {
	text := strings.ReplaceAll(sampleText(), "\n", "\r\n")
	plan, err := ParsePlan(text, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.SetStatus("P007.1.2", "x"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(text, "### [~] P007.1.2", "### [x] P007.1.2", 1)
	if plan.Text() != want {
		t.Fatal("CRLF status update changed other bytes")
	}
}

func TestInsertFieldInOrder(t *testing.T) {
	plan := parseSample(t)
	if err := plan.SetField("P007.1.2", "Handoff", "", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	unit := plan.Unit("P007.1.2")
	if unit.Fields[len(unit.Fields)-2].Key != "Commit" || unit.Fields[len(unit.Fields)-1].Key != "Handoff" {
		t.Fatalf("field order = %#v", unit.Fields)
	}
	want := strings.Replace(sampleText(), "Commit: P007.1.2: fill feed cache\n", "Commit: P007.1.2: fill feed cache\nHandoff:\n- a\n- b\n", 1)
	if plan.Text() != want {
		t.Fatal("inserted Handoff differs")
	}
}

func TestInsertMiddleField(t *testing.T) {
	plan := parseSample(t)
	if err := plan.SetField("P007.2.2", "Depends", "-", nil); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sampleText(), "Commit: P007.2.2: skipped\n", "Depends: -\nCommit: P007.2.2: skipped\n", 1)
	if plan.Text() != want {
		t.Fatal("inserted Depends differs")
	}
}

func TestInsertAtEOFWithoutNewline(t *testing.T) {
	text := strings.TrimSuffix(sampleText(), "\n")
	plan, err := ParsePlan(text, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.SetField("P007.2.3", "Handoff", "", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	want := text + "\nHandoff:\n- x\n"
	if plan.Text() != want {
		t.Fatal("EOF insertion differs")
	}
}

func TestReplaceMultilineField(t *testing.T) {
	plan := parseSample(t)
	if err := plan.SetField("P007.1.1", "Do", "", []string{"Один пункт"}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sampleText(), "Do:\n- Создать схему `feed`,\n  с индексом по `created`.\n- Второй пункт\n", "Do:\n- Один пункт\n", 1)
	if plan.Text() != want || plan.Unit("P007.1.1").Field("Verify").Value != "`./gradlew :shared:allTests`" {
		t.Fatal("multiline field replacement differs")
	}
}

func TestSaveFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "plans", "P007-offline-cache.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(sampleText(), "\n", "\r\n")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := LoadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Save(""); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != text {
		t.Fatalf("saved bytes differ: err=%v", err)
	}
	if err := plan.SetStatus("P007.2.1", "x"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Save(""); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPlan(path)
	if err != nil || loaded.Unit("P007.2.1").Status != "x" {
		t.Fatalf("saved status = %#v, err=%v", loaded, err)
	}
}

func TestFindRootFromSubdir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(DocflowFilePath)), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(root, "src", "deep")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindRoot(subdir)
	want, _ := filepath.EvalSymlinks(root)
	if err != nil || got != want {
		t.Fatalf("FindRoot() = %q, %v; want %q", got, err, want)
	}
}

func TestNoRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := FindRoot(root); err == nil || !strings.Contains(err.Error(), "docs/docflow.md not found") {
		t.Fatalf("FindRoot() error = %v", err)
	}
}

func TestUnitDepends(t *testing.T) {
	plan := parseSample(t)
	for _, test := range []struct {
		id   string
		want []string
	}{
		{id: "P007.1.1", want: []string{}},
		{id: "P007.1.2", want: []string{"P007.1.1"}},
		{id: "P007.2.1", want: []string{"P007.1.2"}},
	} {
		unit := plan.Unit(test.id)
		got, err := UnitDepends(plan, unit)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("UnitDepends(%s) = %#v, %v; want %#v", test.id, got, err, test.want)
		}
	}
}

func sectionNames(sections []Section) []string {
	names := make([]string, len(sections))
	for i, section := range sections {
		names[i] = section.Name
	}
	return names
}

func itemTexts(items []Item) []string {
	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = item.Text
	}
	return texts
}

func changedLines(before, after string) []int {
	a, b := SplitLines(before), SplitLines(after)
	changed := make([]int, 0)
	for i := range a {
		if i >= len(b) || a[i] != b[i] {
			changed = append(changed, i+1)
		}
	}
	for i := len(a); i < len(b); i++ {
		changed = append(changed, i+1)
	}
	return changed
}

func mustSource(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return source
}
