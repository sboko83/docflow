package cycle

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// commitWithTestChanges makes an empty commit carrying the `Test changes:`
// items in its body: the shape of a unit commit made by docflow commit.
func commitWithTestChanges(t *testing.T, root, subject string, items ...string) {
	t.Helper()
	message := testChangesBody(subject+"\n\n- handoff\n", items)
	command := exec.Command("git", "commit", "-q", "--allow-empty", "-F", "-")
	command.Dir = root
	command.Stdin = strings.NewReader(message)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git commit %q: %v: %s: %s", subject, err, output, stderr.String())
	}
}

func TestPromptTestChangesReview(t *testing.T) {
	reviewPlan := strings.Replace(promptPlan,
		"### [ ] P001.1.3 Third\nRead:",
		"### [ ] P001.1.3 Third\nRole: review\nRead:", 1)
	t.Run("phase commit with a weakened test", func(t *testing.T) {
		root := promptProject(t, reviewPlan, nil)
		commitWithTestChanges(t, root, "P001.1.1: first", "a_test.go: -1 lines")
		commitWithTestChanges(t, root, "P001.1.2: second")
		commitWithTestChanges(t, root, "P001.21.1: other phase", "other_test.py: deleted")
		commitWithTestChanges(t, root, "P001.1.3: review unit", "late_test.go: +skip")
		result, err := Prompt(root, "P001.1.3", nil)
		if err != nil || result.Code != 0 {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		text := result.Stdout
		if !strings.Contains(text, "## Test changes\n\n- P001.1.1: first: a_test.go: -1 lines\n") {
			t.Errorf("review prompt has no item of the phase commit:\n%s", text)
		}
		for _, excluded := range []string{"other_test.py: deleted", "late_test.go: +skip"} {
			if strings.Contains(text, excluded) {
				t.Errorf("review prompt contains %q:\n%s", excluded, text)
			}
		}
		phase := strings.Index(text, "## Phase diff")
		section := strings.Index(text, "## Test changes")
		cycle := strings.Index(text, "## Cycle")
		if !(phase >= 0 && section >= 0 && cycle >= 0 && phase < section && section < cycle) {
			t.Errorf("placement: phase diff %d, test changes %d, cycle %d", phase, section, cycle)
		}
	})
	t.Run("phase without items", func(t *testing.T) {
		root := promptProject(t, reviewPlan, nil)
		commitWithTestChanges(t, root, "P001.1.1: first")
		result, err := Prompt(root, "P001.1.3", nil)
		if err != nil || result.Code != 0 {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		if !strings.Contains(result.Stdout, "## Test changes\n\nnone\n") {
			t.Errorf("no items do not print none:\n%s", result.Stdout)
		}
	})
	t.Run("nested repositories", func(t *testing.T) {
		docflow := strings.Replace(promptDocflow, "read_budget: 40000\n", "read_budget: 40000\nrepos: deps/*\n", 1)
		root := makeCycleProject(t, docflow, map[string]string{"P001": reviewPlan}, map[string]string{
			".gitignore":  "deps/module\n",
			"a.py":        "print(1)\n",
			"docs/ctx.md": "# Ctx\n",
		})
		nested := filepath.Join(root, "deps", "module")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		gitCycleDir(t, nested, "init", "-q")
		gitCycleDir(t, nested, "config", "user.name", "docflow test")
		gitCycleDir(t, nested, "config", "user.email", "test@example.invalid")
		writeCycleFile(t, nested, "tracked.txt", "initial\n")
		gitCycleDir(t, nested, "add", "-A")
		gitCycleDir(t, nested, "commit", "-q", "-m", "initial nested")
		commitWithTestChanges(t, nested, "P001.1.1: nested phase commit", "inner_test.go: -1 lines")
		result, err := Prompt(root, "P001.1.3", func(string) ([]string, error) {
			return []string{"deps/module"}, nil
		})
		if err != nil || result.Code != 0 {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		text := result.Stdout
		for _, want := range []string{
			"## Test changes\n\n### .\n\nnone\n\n\n### deps/module\n\n- P001.1.1: nested phase commit: inner_test.go: -1 lines\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("nested test changes do not contain %q:\n%s", want, text)
			}
		}
	})
}

func TestPromptTestChangesAcceptance(t *testing.T) {
	acceptance := strings.Replace(cycleUnit("P001.2.1", " "),
		"Read:\n- a.py\nDo:", "Role: acceptance\nDepends: P001.1.1\nRead:\n- a.py\nDo:", 1)
	plan := promptPlan + "\n## Phase 2: Second\n\n" + acceptance
	t.Run("items of every phase", func(t *testing.T) {
		root := promptProject(t, plan, nil)
		commitWithTestChanges(t, root, "P001.1.1: first", "a_test.go: -1 lines")
		commitWithTestChanges(t, root, "P001.2.2: second phase", "b_test.py: deleted", "c_test.py: +skip")
		commitWithTestChanges(t, root, "P001: replan", "replanned_test.go: -1 lines")
		commitWithTestChanges(t, root, "P001.2.1: unit", "late_test.go: +skip")
		result, err := Prompt(root, "P001.2.1", nil)
		if err != nil || result.Code != 0 {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		text := result.Stdout
		for _, want := range []string{
			"- P001.1.1: first: a_test.go: -1 lines\n",
			"- P001.2.2: second phase: b_test.py: deleted\n- P001.2.2: second phase: c_test.py: +skip\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("acceptance prompt has no item %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "replanned_test.go") || strings.Contains(text, "late_test.go") {
			t.Errorf("acceptance prompt contains a replan or own unit commit:\n%s", text)
		}
		if strings.Contains(text, "## Phase diff") {
			t.Errorf("acceptance prompt contains a phase diff:\n%s", text)
		}
		section := strings.Index(text, "## Test changes")
		cycle := strings.Index(text, "## Cycle")
		if !(section >= 0 && cycle >= 0 && section < cycle) {
			t.Errorf("placement: test changes %d, cycle %d", section, cycle)
		}
	})
	t.Run("plan without items", func(t *testing.T) {
		root := promptProject(t, plan, nil)
		result, err := Prompt(root, "P001.2.1", nil)
		if err != nil || result.Code != 0 {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		if !strings.Contains(result.Stdout, "## Test changes\n\nnone\n") {
			t.Errorf("no items do not print none:\n%s", result.Stdout)
		}
	})
	t.Run("repository without commits", func(t *testing.T) {
		// An in-progress unit is not gated on a clean tree, so its prompt
		// runs before the first commit.
		started := strings.Replace(plan, "### [ ] P001.2.1", "### [~] P001.2.1", 1)
		root := promptProject(t, started, nil)
		if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
			t.Fatal(err)
		}
		gitCycle(t, root, "init", "-q")
		result, err := Prompt(root, "P001.2.1", nil)
		if err != nil || result.Code != 0 {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		if !strings.Contains(result.Stdout, "## Test changes\n\nnone\n") {
			t.Errorf("a repository without HEAD does not print none:\n%s", result.Stdout)
		}
	})
}

func TestCommitTestChangesBody(t *testing.T) {
	t.Run("block of a unit commit", func(t *testing.T) {
		message := testChangesBody("P001.1.1: first\n\n- от первого\n", []string{"a_test.go: -1 lines", "b_test.go: +skip"})
		items := commitTestChanges(strings.SplitN(message, "\n\n", 2)[1])
		if strings.Join(items, "|") != "a_test.go: -1 lines|b_test.go: +skip" {
			t.Fatalf("items = %#v", items)
		}
	})
	t.Run("no block", func(t *testing.T) {
		if items := commitTestChanges("- от первого\n"); len(items) > 0 {
			t.Fatalf("items of a body without the block = %#v", items)
		}
	})
	t.Run("text after the block", func(t *testing.T) {
		body := "Test changes:\n- a_test.go: -1 lines\n\nCo-authored-by: X\n- not an item\n"
		if items := commitTestChanges(body); strings.Join(items, "|") != "a_test.go: -1 lines" {
			t.Fatalf("items = %#v", items)
		}
	})
}
