//go:build windows

package cycle

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func fakeGitRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"bin", "cmd", filepath.Join("mingw64", "bin")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "bash.exe"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGitBashFromGitLocation(t *testing.T) {
	root := fakeGitRoot(t)
	want := filepath.Join(root, "bin", "bash.exe")
	for _, gitDir := range []string{"cmd", "bin", filepath.Join("mingw64", "bin")} {
		git := filepath.Join(root, gitDir, "git.exe")
		got := gitBash(func(string) (string, error) { return git, nil })
		if got != want {
			t.Errorf("git in %s: got %q, want %q", gitDir, got, want)
		}
	}
}

func TestGitBashMissing(t *testing.T) {
	notFound := func(string) (string, error) { return "", errors.New("not found") }
	if got := gitBash(notFound); got != "" {
		t.Errorf("no git: got %q", got)
	}
	git := filepath.Join(t.TempDir(), "cmd", "git.exe")
	if got := gitBash(func(string) (string, error) { return git, nil }); got != "" {
		t.Errorf("git without bash.exe: got %q", got)
	}
}

func TestGitBashSkipsSystemRoot(t *testing.T) {
	root := fakeGitRoot(t)
	t.Setenv("SystemRoot", root)
	git := filepath.Join(root, "cmd", "git.exe")
	if got := gitBash(func(string) (string, error) { return git, nil }); got != "" {
		t.Errorf("bash under SystemRoot: got %q", got)
	}
}

func TestPlanShellRunsPOSIX(t *testing.T) {
	if gitBash(exec.LookPath) == "" {
		t.Skip("Git for Windows not found")
	}
	argv := PlanShell(`test "$(echo ok)" = ok`)
	if filepath.Base(argv[0]) != "bash.exe" || argv[1] != "-c" {
		t.Fatalf("argv = %q", argv)
	}
	cmd := verifyShell(`test "$(echo ok)" = ok`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("POSIX command failed: %v\n%s", err, out)
	}
}
