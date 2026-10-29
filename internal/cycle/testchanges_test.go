package cycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsTestPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"a_test.go", true},
		{"pkg/sub/a_test.go", true},
		{"pkg\\sub\\a_test.go", true},
		{"a_test.py", true},
		{"test_a.py", true},
		{"tests/test_a.py", true},
		{"a.test.js", true},
		{"a.spec.ts", true},
		{"FooTest.java", true},
		{"src/test/java/com/x/Foo.java", true},
		{"FooTest.kt", true},
		{"FooTests.swift", true},
		{"FooTest.swift", true},
		{"androidTest/src/Foo.java", true},
		{"__tests__/foo.js", true},
		{"spec/models.rb", true},
		{"Tests/Foo.swift", true},
		{"latest.go", false},
		{"latest/data.go", false},
		{"contest/main.go", false},
		{"testing/log.go", false},
		{"main.go", false},
		{"a.go.txt", false},
		{"a_test.go.txt", false},
		{"docs/plans/P001-plan.md", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsTestPath(tc.path); got != tc.want {
			t.Errorf("IsTestPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestHasSkipMarker(t *testing.T) {
	for _, marker := range skipMarkers {
		line := "\t" + marker + " \"why\""
		if !hasSkipMarker([]string{"package a", line}) {
			t.Errorf("hasSkipMarker() = false for %q", marker)
		}
	}
	for _, line := range []string{"t.skip()", "x it(", "it.skipx", "T.Skip!", "Skip()"} {
		if hasSkipMarker([]string{"package a", line}) {
			t.Errorf("hasSkipMarker() = true for %q", line)
		}
	}
}

func TestStagedTestChangesItems(t *testing.T) {
	root := makeCommitProject(t, commitPlan(" "), map[string]string{
		"a.py":      "print(1)\n",
		"a_test.go": "package a\nfunc TestA() {}\nfunc TestB() {}\n",
		"b_test.go": "package b\nfunc TestB() {}\n",
		"c_test.go": "package c\nfunc TestC() {}\n",
	})
	startCommitUnit(t, root)
	writeCycleFile(t, root, "a_test.go", "package a\nfunc TestA() {}\n")
	if err := os.Remove(filepath.Join(root, "b_test.go")); err != nil {
		t.Fatal(err)
	}
	writeCycleFile(t, root, "c_test.go", "package c\nfunc TestC() {}\nfunc TestD() { t.Skip(\"why\") }\n")
	verifyAndDoneCommitUnit(t, root, nil)

	result, err := Commit(root, "P001.1.1", nil)
	if err != nil || result.Code != 0 {
		t.Fatalf("Commit() = %#v, %v", result, err)
	}
	wantLines := []string{
		"- a_test.go: -1 lines",
		"- b_test.go: deleted",
		"- c_test.go: +skip",
	}
	body := gitCycle(t, root, "log", "-1", "--format=%B")
	if !strings.Contains(body, "\n\nTest changes:\n") {
		t.Fatalf("commit body has no Test changes block: %q", body)
	}
	for _, want := range wantLines {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("commit body has no %q: %q", want, body)
		}
		if line := "test change: " + strings.TrimPrefix(want, "- "); !strings.Contains(result.Stdout, line+"\n") {
			t.Errorf("commit output has no %q: %q", line, result.Stdout)
		}
	}
	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	sha := strings.TrimSpace(gitCycle(t, root, "rev-parse", "--short", "HEAD"))
	if !strings.HasPrefix(lines[0], sha+" P001.1.1: unit") {
		t.Fatalf("first output line changed: %q", result.Stdout)
	}
}

// The edge content and paths of the diff parser: CRLF line endings, a path
// git would quote in plain output, a binary test file, and a skip marker
// split across added lines (the detector is line-based by design).
func TestStagedTestChangesEdgeContent(t *testing.T) {
	t.Run("crlf content", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{
			"a_test.go": "package a\r\nfunc TestA() {}\r\nfunc TestB() {}\r\n",
		})
		// No core.autocrlf override: under any one config the baseline and the
		// edit normalize the same way, so the deleted count stays 1.
		writeCycleFile(t, root, "a_test.go", "package a\r\nfunc TestA() {}\r\n")
		gitCycle(t, root, "add", "-A")
		items, err := stagedTestChanges(root)
		if err != nil || strings.Join(items, "|") != "a_test.go: -1 lines" {
			t.Fatalf("stagedTestChanges() = %#v, %v", items, err)
		}
	})

	t.Run("path with a space", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{
			"sp ace/a_test.go": "package a\nfunc TestA() {}\nfunc TestB() {}\n",
		})
		writeCycleFile(t, root, "sp ace/a_test.go", "package a\nfunc TestA() {}\n")
		gitCycle(t, root, "add", "-A")
		items, err := stagedTestChanges(root)
		if err != nil || strings.Join(items, "|") != "sp ace/a_test.go: -1 lines" {
			t.Fatalf("stagedTestChanges() = %#v, %v", items, err)
		}
	})

	// The patch header of a path with a space ends with a tab and a
	// non-ASCII path is octal-quoted: the skip marker must still reach the
	// path the numstat names.
	for name, path := range map[string]string{
		"skip marker in a path with a space": "sp ace/b_test.go",
		"skip marker in a non-ASCII path":    "тесты/b_test.go",
	} {
		t.Run(name, func(t *testing.T) {
			root := makeCommitProject(t, commitPlan(" "), map[string]string{
				path: "package b\nfunc TestB() {}\n",
			})
			writeCycleFile(t, root, path, "package b\nfunc TestB() {}\nfunc TestC() { t.Skip(\"why\") }\n")
			gitCycle(t, root, "add", "-A")
			items, err := stagedTestChanges(root)
			if err != nil || strings.Join(items, "|") != path+": +skip" {
				t.Fatalf("stagedTestChanges() = %#v, %v", items, err)
			}
		})
	}

	t.Run("deleted binary test file", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{
			"tests/blob.bin": "\x00\x01binary\x00\n",
		})
		if err := os.Remove(filepath.Join(root, "tests", "blob.bin")); err != nil {
			t.Fatal(err)
		}
		gitCycle(t, root, "add", "-A")
		items, err := stagedTestChanges(root)
		// numstat counts no lines for a binary file, but the file is gone:
		// the name-status letter still names the deletion.
		if err != nil || strings.Join(items, "|") != "tests/blob.bin: deleted" {
			t.Fatalf("stagedTestChanges() = %#v, %v", items, err)
		}
	})

	t.Run("skip marker split across lines", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{
			"c_test.go": "package c\nfunc TestC() {}\n",
		})
		writeCycleFile(t, root, "c_test.go", "package c\nfunc TestC() {}\nfunc TestD() { t.\nSkip(\"why\") }\n")
		gitCycle(t, root, "add", "-A")
		items, err := stagedTestChanges(root)
		if err != nil || len(items) != 0 {
			t.Fatalf("stagedTestChanges() = %#v, %v; a split marker is not detected by the line-based detector", items, err)
		}
	})
}

func TestCommitWithoutTestChangeItems(t *testing.T) {
	root := makeCommitProject(t, commitPlan(" "), map[string]string{
		"a.py":        "print(1)\n",
		"a_test.go":   "package a\nfunc TestA() {}\n",
		"src/main.go": "package a\n",
	})
	startCommitUnit(t, root)
	writeCycleFile(t, root, "src/main.go", "package a\n\nfunc A() {}\n")
	writeCycleFile(t, root, "d_test.go", "package a\n\nfunc TestD() {}\n")
	verifyAndDoneCommitUnit(t, root, nil)

	result, err := Commit(root, "P001.1.1", nil)
	if err != nil || result.Code != 0 {
		t.Fatalf("Commit() = %#v, %v", result, err)
	}
	if strings.Contains(result.Stdout, "test change:") {
		t.Fatalf("commit output marks a test change: %q", result.Stdout)
	}
	body := gitCycle(t, root, "log", "-1", "--format=%B")
	if strings.Contains(body, "Test changes:") {
		t.Fatalf("commit body marks a test change: %q", body)
	}
}

func TestStagedTestChanges(t *testing.T) {
	t.Run("no head", func(t *testing.T) {
		root := t.TempDir()
		gitCycle(t, root, "init", "-q")
		writeCycleFile(t, root, "a_test.go", "package a\nfunc TestA() {}\n")
		gitCycle(t, root, "add", "-A")
		items, err := stagedTestChanges(root)
		if err != nil || len(items) != 0 {
			t.Fatalf("stagedTestChanges() = %#v, %v", items, err)
		}
	})

	t.Run("pure rename", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{"dir/a_test.go": "package a\nfunc TestA() {}\n"})
		if err := os.MkdirAll(filepath.Join(root, "other"), 0o755); err != nil {
			t.Fatal(err)
		}
		gitCycle(t, root, "mv", "dir/a_test.go", "other/a_test.go")
		items, err := stagedTestChanges(root)
		if err != nil || len(items) != 0 {
			t.Fatalf("stagedTestChanges() = %#v, %v", items, err)
		}
	})

	t.Run("new file and non test file", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{"main.go": "package a\n"})
		writeCycleFile(t, root, "new_test.go", "package a\nfunc TestA() {}\n")
		writeCycleFile(t, root, "main.go", "package a\nfunc A() {}\n")
		gitCycle(t, root, "add", "-A")
		items, err := stagedTestChanges(root)
		if err != nil || len(items) != 0 {
			t.Fatalf("stagedTestChanges() = %#v, %v", items, err)
		}
	})

	t.Run("rename with deleted lines", func(t *testing.T) {
		root := makeCommitProject(t, commitPlan(" "), map[string]string{"dir/a_test.go": "package a\nfunc TestA() {}\nfunc TestB() {}\n"})
		if err := os.MkdirAll(filepath.Join(root, "other"), 0o755); err != nil {
			t.Fatal(err)
		}
		gitCycle(t, root, "mv", "dir/a_test.go", "other/a_test.go")
		writeCycleFile(t, root, "other/a_test.go", "package a\nfunc TestA() {}\n")
		gitCycle(t, root, "add", "-A")
		items, err := stagedTestChanges(root)
		if err != nil || strings.Join(items, "|") != "other/a_test.go: -1 lines" {
			t.Fatalf("stagedTestChanges() = %#v, %v", items, err)
		}
	})
}

func TestCommitNestedRepositoryTestChanges(t *testing.T) {
	root := makeCommitProject(t, commitPlan(" "), map[string]string{
		".gitignore": "deps/module/\n",
		"a.py":       "print(1)\n",
		"main.go":    "package a\n",
	})
	nested := filepath.Join(root, "deps", "module")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCycle(t, nested, "init", "-q")
	gitCycle(t, nested, "config", "user.name", "docflow test")
	gitCycle(t, nested, "config", "user.email", "test@example.invalid")
	writeCycleFile(t, root, "deps/module/a_test.go", "package a\nfunc TestA() {}\nfunc TestB() {}\n")
	gitCycle(t, nested, "add", "-A")
	gitCycle(t, nested, "commit", "-q", "-m", "initial nested")
	repos := func(string) ([]string, error) { return []string{"deps/module"}, nil }
	startCommitUnit(t, root)
	writeCycleFile(t, root, "main.go", "package a\nfunc A() {}\n")
	writeCycleFile(t, root, "deps/module/a_test.go", "package a\nfunc TestA() {}\n")
	verifyAndDoneCommitUnit(t, root, repos)

	result, err := Commit(root, "P001.1.1", repos)
	if err != nil || result.Code != 0 {
		t.Fatalf("Commit() = %#v, %v", result, err)
	}
	if !strings.Contains(result.Stdout, "test change: deps/module/a_test.go: -1 lines\n") {
		t.Fatalf("commit output = %q", result.Stdout)
	}
	message := gitCycle(t, nested, "log", "-1", "--format=%B")
	if !strings.Contains(message, "Test changes:\n- a_test.go: -1 lines\n") {
		t.Fatalf("nested commit body = %q", message)
	}
}

func TestCommitPendingRecoveryKeepsTestChangeItems(t *testing.T) {
	root := makeCommitProject(t, commitPlan(" "), map[string]string{
		".gitignore": "deps/module/\n",
		"a.py":       "print(1)\n",
		"main.go":    "package a\n",
	})
	nested := filepath.Join(root, "deps", "module")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCycle(t, nested, "init", "-q")
	gitCycle(t, nested, "config", "user.name", "docflow test")
	gitCycle(t, nested, "config", "user.email", "test@example.invalid")
	writeCycleFile(t, root, "deps/module/a_test.go", "package a\nfunc TestA() {}\nfunc TestB() {}\n")
	gitCycle(t, nested, "add", "-A")
	gitCycle(t, nested, "commit", "-q", "-m", "initial nested")
	repos := func(string) ([]string, error) { return []string{"deps/module"}, nil }
	startCommitUnit(t, root)
	writeCycleFile(t, root, "deps/module/a_test.go", "package a\nfunc TestA() {}\n")
	verifyAndDoneCommitUnit(t, root, repos)
	_, unit, err := findUnit(root, "P001.1.1")
	if err != nil {
		t.Fatal(err)
	}
	// The nested commit is made the way docflow makes it, the journal is not
	// updated, and only the fingerprint is re-saved: this is the state a crash
	// between the nested commit and the journal write leaves.
	if _, _, _, err := commitRepo(nested, CommitMessage(unit)); err != nil {
		t.Fatalf("commitRepo() = %v", err)
	}
	plan, _, err := findUnit(root, "P001.1.1")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := fingerprint(root, plan.Path, repos)
	if err != nil {
		t.Fatal(err)
	}
	reason, err := skipReason(root, "P001.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := saveVerified(root, "P001.1.1", digest, reason); err != nil {
		t.Fatal(err)
	}

	result, err := Commit(root, "P001.1.1", repos)
	if err != nil || result.Code != 0 {
		t.Fatalf("Commit() = %#v, %v", result, err)
	}
	if !strings.Contains(result.Stdout, "test change: deps/module/a_test.go: -1 lines\n") {
		t.Fatalf("recovered commit output = %q", result.Stdout)
	}
}
