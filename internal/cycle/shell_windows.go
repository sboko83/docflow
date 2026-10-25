//go:build windows

package cycle

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// planShell prefers Git for Windows bash: plans are written in POSIX sh.
// Without it, commands fall back to cmd.exe.
func planShell() []string {
	if bash := gitBash(exec.LookPath); bash != "" {
		return []string{bash, "-c"}
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	return []string{shell, "/c"}
}

// gitBash finds <git root>\bin\bash.exe from git.exe on PATH. git.exe lives
// in <git root>\cmd or <git root>\bin, and inside Git Bash in
// <git root>\mingw64\bin (or another MSYS2 prefix), so the root is one or
// two levels above its directory. System32\bash.exe (WSL) is never used.
func gitBash(lookPath func(string) (string, error)) string {
	git, err := lookPath("git")
	if err != nil {
		return ""
	}
	git, err = filepath.Abs(git)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(git)
	for _, root := range []string{filepath.Dir(dir), filepath.Dir(filepath.Dir(dir))} {
		bash := filepath.Join(root, "bin", "bash.exe")
		if isSystemDir(bash) {
			continue
		}
		if info, err := os.Stat(bash); err == nil && !info.IsDir() {
			return bash
		}
	}
	return ""
}

func isSystemDir(path string) bool {
	windir := os.Getenv("SystemRoot")
	if windir == "" {
		windir = os.Getenv("WINDIR")
	}
	if windir == "" {
		return false
	}
	rel, err := filepath.Rel(windir, path)
	return err == nil && !strings.HasPrefix(rel, "..")
}
