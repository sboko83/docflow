package cycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bsv-docflow/internal/format"
)

// activateProject commits a draft plan outside the Queue, then applies the
// writes `docflow activate` would leave uncommitted: the status flip and the
// Queue append.
func activateProject(t *testing.T, extra func(root string)) string {
	t.Helper()
	draft := strings.Replace(promptPlan, "status: active", "status: draft", 1)
	unqueued := strings.Replace(promptDocflow, "\n- P001\n", "\n", 1)
	root := promptProject(t, draft, nil)
	writeCycleFile(t, root, "docs/docflow.md", unqueued)
	gitCycle(t, root, "add", "-A")
	gitCycle(t, root, "commit", "-q", "-m", "draft plan")
	writeCycleFile(t, root, "docs/plans/P001-plan.md", promptPlan)
	writeCycleFile(t, root, "docs/docflow.md", promptDocflow)
	if extra != nil {
		extra(root)
	}
	return root
}

func TestPromptCommitsActivationTraces(t *testing.T) {
	root := activateProject(t, nil)
	result, err := runPrompt(t, root, "P001.1.3")
	if err != nil || result.Code != 0 {
		t.Fatalf("Prompt() = %#v, %v", result, err)
	}
	if !strings.Contains(result.Stderr, "P001: activate plan committed") {
		t.Fatalf("prompt stderr = %q", result.Stderr)
	}
	log := gitCycle(t, root, "log", "--format=%s")
	if !strings.Contains(log, "P001: activate plan") {
		t.Fatalf("activation not committed:\n%s", log)
	}
	// The activation commit is separate: after it only the prompted unit's
	// status change stays in the tree.
	status := strings.TrimRight(gitCycle(t, root, "status", "--porcelain"), "\n")
	if status != " M docs/plans/P001-plan.md" {
		t.Fatalf("working tree after activation commit:\n%s", status)
	}
}

func TestPromptKeepsDirtyRefusalBeyondActivation(t *testing.T) {
	t.Run("untracked file", func(t *testing.T) {
		root := activateProject(t, func(root string) {
			writeCycleFile(t, root, "new.txt", "x")
		})
		result, err := runPrompt(t, root, "P001.1.3")
		if err != nil || result.Code != 5 || !strings.Contains(result.Stderr, "working tree is dirty") || !strings.Contains(result.Stderr, "new.txt") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		if strings.Contains(gitCycle(t, root, "log", "--format=%s"), "activate plan") {
			t.Fatal("activation committed on a dirty tree")
		}
	})
	t.Run("docflow edit beyond the queue line", func(t *testing.T) {
		root := activateProject(t, func(root string) {
			text, err := os.ReadFile(filepath.Join(root, "docs", "docflow.md"))
			if err != nil {
				t.Fatal(err)
			}
			writeCycleFile(t, root, "docs/docflow.md", string(text)+"- другая задача\n")
		})
		result, err := runPrompt(t, root, "P001.1.3")
		if err != nil || result.Code != 5 || !strings.Contains(result.Stderr, "working tree is dirty") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		if strings.Contains(gitCycle(t, root, "log", "--format=%s"), "activate plan") {
			t.Fatal("activation committed on a dirty tree")
		}
	})
	t.Run("plan edit beyond the status flip", func(t *testing.T) {
		root := activateProject(t, func(root string) {
			writeCycleFile(t, root, "docs/plans/P001-plan.md", promptPlan+"\nпозже дописано.\n")
		})
		result, err := runPrompt(t, root, "P001.1.3")
		if err != nil || result.Code != 5 || !strings.Contains(result.Stderr, "working tree is dirty") {
			t.Fatalf("Prompt() = %#v, %v", result, err)
		}
		if strings.Contains(gitCycle(t, root, "log", "--format=%s"), "activate plan") {
			t.Fatal("activation committed on a dirty tree")
		}
	})
}

func TestCommitActivationSkipsQueuedPlan(t *testing.T) {
	// A plan already in the Queue: activation dirties only the plan file. The
	// fixture commits the draft itself.
	root := promptProject(t, strings.Replace(promptPlan, "status: active", "status: draft", 1), nil)
	writeCycleFile(t, root, "docs/plans/P001-plan.md", promptPlan)
	committed, err := CommitActivation(root, mustPlan(t, root))
	if err != nil || !committed {
		t.Fatalf("CommitActivation() = %v, %v", committed, err)
	}
	if subject := gitCycle(t, root, "log", "--format=%s"); !strings.Contains(subject, "P001: activate plan") {
		t.Fatalf("commit subjects:\n%s", subject)
	}
}

func mustPlan(t *testing.T, root string) *format.Plan {
	t.Helper()
	plan, err := findPlan(root, "P001")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
