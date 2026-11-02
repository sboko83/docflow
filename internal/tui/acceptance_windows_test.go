//go:build windows

package tui

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAcceptanceWindowsConsoleContract exercises the real Windows binary in
// the CI matrix: without a console the workspace refuses before loading plans
// with the documented exit code, and --help stays a plain command. The
// interactive console path has no PTY driver on Windows and is not claimed by
// this test.
func TestAcceptanceWindowsConsoleContract(t *testing.T) {
	if testing.Short() {
		t.Skip("acceptance builds the CLI")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd))
	bin := filepath.Join(t.TempDir(), "docflow.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
	build.Dir = root
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	project := t.TempDir()
	docs := filepath.Join(project, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "docflow.md"), []byte("---\ndocflow: 3\n---\n## Queue\n\n## Frozen\n\n## Backlog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "tui")
	cmd.Dir = project
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	exit := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exit = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("tui without a console: %v", err)
	}
	if exit != 2 {
		t.Fatalf("tui without a console exit = %d, want 2\nstdout: %s\nstderr: %s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "requires an interactive terminal") {
		t.Fatalf("refusal does not name the missing terminal:\n%s", stderr.String())
	}

	help := exec.Command(bin, "tui", "--help")
	var helpOut bytes.Buffer
	help.Stdout, help.Stderr = &helpOut, &helpOut
	if err := help.Run(); err != nil {
		t.Fatalf("tui --help: %v\n%s", err, helpOut.String())
	}
	if !strings.Contains(helpOut.String(), "tui") {
		t.Fatalf("tui --help does not describe the command:\n%s", helpOut.String())
	}
}
