package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectGitCommit(t *testing.T) {
	for _, command := range []string{
		"git commit -m test", "git -C src -c core.foo=bar commit", "git --no-pager commit",
		"go test ./... && git commit", "git status; git 'commit'", "env FOO=1 command git commit",
		`bash -lc 'git commit -m test'`, `& 'C:\Program Files\Git\cmd\git.exe' commit -m test`,
		"echo ok\ngit commit", "echo $(git commit)",
	} {
		if !directGitCommit(command) {
			t.Errorf("missed %q", command)
		}
	}
	for _, command := range []string{
		"git status", "docflow finish P001.1.1 --handoff done", `echo "git commit"`,
		"git log --grep commit", "git -c alias.commit=status status", "# git commit\ngit diff",
		`printf '%s' 'git commit'`, "git commit-tree HEAD^{tree}",
	} {
		if directGitCommit(command) {
			t.Errorf("false positive %q", command)
		}
	}
}

func TestStatusHookGuardsUnfinishedUnits(t *testing.T) {
	root := t.TempDir()
	writeLifecycleFile(t, root, "docs/docflow.md", lifecycleDocflow([]string{"P001"}, nil))
	path := "docs/plans/P001-test.md"
	call := func(tool, command string) string {
		t.Helper()
		event, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "cwd": filepath.Join(root, "src"), "tool_input": map[string]string{"command": command}})
		var output bytes.Buffer
		if code := statusHook(bytes.NewReader(event), &output); code != 0 {
			t.Fatalf("code %d", code)
		}
		var decoded map[string]any
		if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"~", "!"} {
		writeLifecycleFile(t, root, path, lifecyclePlan("P001", "active", status))
		for _, tool := range []string{"Bash", "PowerShell"} {
			if got := call(tool, "git commit -m bypass"); !strings.Contains(got, `"permissionDecision":"deny"`) || !strings.Contains(got, "P001.1.1") {
				t.Fatalf("unguarded %s %s: %s", status, tool, got)
			}
		}
		for _, command := range []string{"git diff", "docflow finish P001.1.1 --handoff done", `echo 'git commit'`} {
			if got := call("Bash", command); strings.TrimSpace(got) != "{}" {
				t.Fatalf("normal flow changed: %s", got)
			}
		}
	}
	writeLifecycleFile(t, root, path, lifecyclePlan("P001", "draft", "~"))
	if got := call("Bash", "git commit"); strings.TrimSpace(got) != "{}" {
		t.Fatal(got)
	}
	writeLifecycleFile(t, root, path, lifecyclePlan("P001", "active", " "))
	if got := call("Bash", "git commit"); strings.TrimSpace(got) != "{}" {
		t.Fatal(got)
	}
}

func TestStatusHookRejectsBrokenInput(t *testing.T) {
	for _, input := range []string{"", "{", "{} {}", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git commit"}}`} {
		var out bytes.Buffer
		statusHook(strings.NewReader(input), &out)
		if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
			t.Fatalf("input %q: %s", input, out.String())
		}
	}
}

func TestStatusHookPendingCommitAndCompletedCycle(t *testing.T) {
	root := makeLifecycleProject(t, []string{"P001"}, nil, map[string]string{"P001": lifecyclePlan("P001", "active", "~")}, true)
	initLifecycleGit(t, root)
	writeLifecycleFile(t, root, "a.py", "x = 2\n")
	for _, args := range [][]string{{"verify", "P001.1.1"}, {"done", "P001.1.1", "--handoff", "Update the fixture value"}} {
		if code, out, errOut := callLifecycleCLI(t, root, args...); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
	}
	event, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": root, "tool_input": map[string]string{"command": "git commit -m bypass"}})
	var out bytes.Buffer
	statusHook(bytes.NewReader(event), &out)
	if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
		t.Fatalf("pending commit bypassed: %s", out.String())
	}
	if code, output, errOut := callLifecycleCLI(t, root, "commit", "P001.1.1"); code != 0 {
		t.Fatalf("commit: %d %s %s", code, output, errOut)
	}
	out.Reset()
	statusHook(bytes.NewReader(event), &out)
	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("completed cycle still guarded: %s", out.String())
	}
}
