package format

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		input string
		want  ReadRef
	}{
		{input: "a/b.kt", want: ReadRef{Path: "a/b.kt"}},
		{input: "a/b.kt:L1-80", want: ReadRef{Path: "a/b.kt", Start: intPointer(1), End: intPointer(80)}},
		{input: "`docs/d.md#x-y`", want: ReadRef{Path: "docs/d.md", Anchor: "x-y"}},
	}
	for _, test := range tests {
		got, err := ParseRef(test.input)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("ParseRef(%q) = %#v, %v; want %#v", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{"[doc](docs/doc.md)", ""} {
		if _, err := ParseRef(input); err == nil {
			t.Errorf("ParseRef(%q) succeeded", input)
		}
	}
	_, err := ParseRef("[doc](docs/doc.md)")
	var refError RefError
	if !errors.As(err, &refError) || !strings.Contains(err.Error(), "markdown link: write a bare path") {
		t.Fatalf("markdown link error = %v", err)
	}
}

func TestHeadingSlug(t *testing.T) {
	tests := map[string]string{
		"Формат: общие правила":    "формат-общие-правила",
		"Скрипт `docflow`":         "скрипт-docflow",
		"Read, Do & Verify (v2.1)": "read-do--verify-v21",
		"  Roman Ⅳ numeral  ":      "roman-ⅳ-numeral",
	}
	for input, want := range tests {
		if got := HeadingSlug(input); got != want {
			t.Errorf("HeadingSlug(%q) = %q; want %q", input, got, want)
		}
	}
}

func TestAnchorRange(t *testing.T) {
	lines := []string{
		"```md",
		"## Hidden",
		"```",
		"## Format: common rules",
		"body",
		"### Detail",
		"more",
		"## Repeat",
		"first repeat",
		"## Repeat",
		"second repeat",
	}
	start, stop, ok := AnchorRange(lines, "format-common-rules")
	if !ok || start != 3 || stop != 7 {
		t.Fatalf("Format range = (%d, %d, %v)", start, stop, ok)
	}
	start, stop, ok = AnchorRange(lines, "repeat-1")
	if !ok || start != 9 || stop != len(lines) {
		t.Fatalf("duplicate range = (%d, %d, %v)", start, stop, ok)
	}
	if _, _, ok := AnchorRange(lines, "hidden"); ok {
		t.Fatal("heading inside a fence was returned")
	}
}

func TestRefBudgetCountsBytesAndRefText(t *testing.T) {
	root := t.TempDir()
	doc := "# Title\n## Раздел\nПривет\n### Child\nТекст\n## Repeat\nok\n## Repeat\nlast"
	writeRefFixture(t, root, "docs/d.md", doc)
	raw := SplitLines(doc)
	byteCount := func(start, stop int) int {
		n := 0
		for _, line := range raw[start:stop] {
			n += len(line)
		}
		return n
	}
	for _, test := range []struct {
		ref  string
		want int
	}{
		{ref: "docs/d.md", want: len(doc)},
		{ref: "docs/d.md:L3-5", want: byteCount(2, 5)},
		{ref: "docs/d.md#раздел", want: byteCount(1, 5)},
		{ref: "docs/d.md#repeat-1", want: byteCount(7, 9)},
		{ref: "docs/d.md#title", want: len(doc)},
		{ref: "docs/d.md:L999-1000", want: 0},
	} {
		got, err := RefSize(root, test.ref)
		if err != nil || got != test.want {
			t.Errorf("RefSize(%q) = %d, %v; want %d", test.ref, got, err, test.want)
		}
	}
	got, err := RefText(root, "docs/d.md:L3-5")
	if err != nil || got != strings.Join(raw[2:5], "") {
		t.Fatalf("RefText line range = %q, %v", got, err)
	}
	got, err = RefText(root, "docs/d.md#раздел")
	if err != nil || got != strings.Join(raw[1:5], "") {
		t.Fatalf("RefText anchor = %q, %v", got, err)
	}
}

func TestRefSizeErrors(t *testing.T) {
	root := t.TempDir()
	writeRefFixture(t, root, "docs/d.md", "## Existing\ncontent\n")
	_, err := RefSize(root, "docs/none.md")
	var missing RefMissing
	if !errors.As(err, &missing) || err.Error() != "docs/none.md: file not found" {
		t.Fatalf("missing file error = %v", err)
	}
	for _, test := range []struct {
		ref  string
		want string
	}{
		{ref: "docs/d.md#nope", want: "docs/d.md: no anchor #nope"},
		{ref: "docs/d.md:L5-3", want: "docs/d.md: bad line range L5-3"},
	} {
		_, err := RefSize(root, test.ref)
		var refError RefError
		if !errors.As(err, &refError) || err.Error() != test.want {
			t.Errorf("RefSize(%q) error = %v; want %q", test.ref, err, test.want)
		}
	}
}

func TestRefPath(t *testing.T) {
	root := t.TempDir()
	got, err := RefPath(root, ReadRef{Path: "x.md"})
	if err != nil || got != filepath.Join(root, "x.md") {
		t.Fatalf("RefPath relative = %q, %v", got, err)
	}
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	got, err = RefPath(root, ReadRef{Path: "~/x.md"})
	if err != nil || got != filepath.Join(home, "x.md") {
		t.Fatalf("RefPath home = %q, %v", got, err)
	}
}

func TestContextRefs(t *testing.T) {
	plan := parseSample(t)
	refs := ContextRefs(plan)
	if len(refs) != 1 || refs[0].Text != "docs/architecture.md#storage" {
		t.Fatalf("ContextRefs = %#v", refs)
	}
	if refs := ContextRefs(&Plan{}); len(refs) != 0 {
		t.Fatalf("ContextRefs without section = %#v", refs)
	}
}

func TestContextRefsChecked(t *testing.T) {
	root := t.TempDir()
	writeRefFixture(t, root, "docs/d.md", "## Existing\ncontent\n")
	plan := &Plan{Doc: Doc{
		Raw:      []string{"## Context\n", "- docs/d.md#missing\n"},
		Sections: []Section{{Name: "Context", Line: 1, End: 2}},
	}}
	refs := ContextRefs(plan)
	if len(refs) != 1 {
		t.Fatalf("ContextRefs = %#v", refs)
	}
	_, err := RefSize(root, refs[0].Text)
	var refError RefError
	if !errors.As(err, &refError) || err.Error() != "docs/d.md: no anchor #missing" {
		t.Fatalf("invalid Context reference error = %v", err)
	}
}

func TestArchiveGotchas(t *testing.T) {
	root := t.TempDir()
	writeRefFixture(t, root, "docs/archive/P001-first.md", "---\nid: P001\ntitle: First\n---\n## Gotchas\n- first trap\n- second trap\n")
	writeRefFixture(t, root, "docs/archive/P002-old.md", "---\nid: P002\ntitle: Old\nsuperseded_by: P003\n---\n## Gotchas\n- stale trap\n")
	writeRefFixture(t, root, "docs/archive/P003-empty.md", "---\nid: P003\ntitle: Empty\n---\n## Outcome\nDone\n")
	writeRefFixture(t, root, "docs/archive/P005-no-id.md", "---\ntitle: Fallback\n---\n## Gotchas\n- fallback trap\n")
	writeRefFixture(t, root, "docs/links.md", "See docs/archive/P001-first.md, archive/P003-empty.md, and archive/P005-no-id.md.\n")
	plan := &Plan{Doc: Doc{
		Raw: []string{
			"## Context\n",
			"- docs/archive/P001-first.md\n",
			"- docs/archive/P004-missing.md\n",
			"## Decisions\n",
			"- ../archive/P002-old.md\n",
			"- archive/P005-no-id.md\n",
		},
		Sections: []Section{
			{Name: "Context", Line: 1, End: 3},
			{Name: "Decisions", Line: 4, End: 6},
		},
	}}
	unit := &Unit{Fields: []Field{{Key: "Read", Items: []Item{{Text: "docs/links.md"}}}}}
	want := []string{"- (P001) first trap", "- (P001) second trap", "- (P005) fallback trap"}
	if got := ArchiveGotchas(root, plan, unit); !reflect.DeepEqual(got, want) {
		t.Fatalf("ArchiveGotchas = %#v; want %#v", got, want)
	}
}

func intPointer(value int) *int { return &value }

func writeRefFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
