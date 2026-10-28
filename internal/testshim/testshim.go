// Package testshim installs shell-script fake agents for tests. The scripts
// are POSIX sh, so on Windows they run through Git Bash — the same shell plan
// commands use there — fronted with a compiled shim exe, because native
// LookPath finds only .exe on PATH and a .cmd front rewrites arguments that
// carry quotes or newlines (an agent schema always does). Used by the agentio
// and runner test suites; no production code imports it.
package testshim

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Bash returns the shell for fake agents and quota probes: /bin/bash on Unix,
// Git Bash on Windows. Skips the test when Git Bash is absent: the POSIX
// fixtures cannot run under cmd.exe.
func Bash(t testing.TB) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "/bin/bash"
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH: cannot find Git Bash")
		return ""
	}
	git, err = filepath.Abs(git)
	if err != nil {
		t.Skip("cannot resolve git: cannot find Git Bash")
		return ""
	}
	dir := filepath.Dir(git)
	for _, root := range []string{filepath.Dir(dir), filepath.Dir(filepath.Dir(dir))} {
		bash := filepath.Join(root, "bin", "bash.exe")
		if info, err := os.Stat(bash); err == nil && !info.IsDir() {
			return bash
		}
	}
	t.Skip("Git Bash not found: the POSIX fixtures cannot run under cmd.exe")
	return ""
}

// Script installs a shell-script command named name in dir: the script itself
// plus, on Windows, a name.exe shim that runs it through Bash, so exec finds
// the command and arguments with quotes and newlines survive. The shebang
// line, if any, is a comment under that shell.
func Script(t testing.TB, dir, name, script string) {
	t.Helper()
	bash := Bash(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.ToSlash(filepath.Join(dir, name))
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	// The shim passes arguments through a NUL-separated file instead of the
	// command line: the native-to-MSYS parse loses arguments that carry
	// quotes (an agent schema always does), whatever quoting is used. The
	// prologue rebuilds "$@" before the script body runs.
	prologue := "#!/bin/bash\n" +
		"if [ -n \"${DOCFLOW_SHIM_ARGS_FILE:-}\" ]; then\n" +
		"	args=()\n" +
		"	while IFS= read -r -d '' arg; do args+=(\"$arg\"); done < \"$DOCFLOW_SHIM_ARGS_FILE\"\n" +
		"	set -- ${args[@]+\"${args[@]}\"}\n" +
		"fi\n"
	if err := os.WriteFile(path, []byte(prologue+strings.TrimPrefix(script, "#!/bin/sh\n")), 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := shimExe()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFile(source, path+".exe"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCFLOW_SHIM_BASH", bash)
}

var (
	shimOnce sync.Once
	shimPath string
	shimErr  error
)

// shimSource is the shim exe: it re-execs the shell script beside itself —
// claude.exe runs the claude script — through the shell from DOCFLOW_SHIM_BASH.
const shimSource = `package main

import (
	"os"
	"os/exec"
	"strings"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(127)
	}
	bash := os.Getenv("DOCFLOW_SHIM_BASH")
	if bash == "" {
		os.Exit(127)
	}
	cmd := exec.Command(bash, strings.TrimSuffix(exe, ".exe"))
	if len(os.Args) > 1 {
		file, err := os.CreateTemp("", "docflow-shim-args-")
		if err != nil {
			os.Exit(127)
		}
		name := file.Name()
		for _, arg := range os.Args[1:] {
			_, _ = file.WriteString(arg)
			_, _ = file.WriteString("\x00")
		}
		_ = file.Close()
		defer os.Remove(name)
		cmd.Env = append(os.Environ(), "DOCFLOW_SHIM_ARGS_FILE="+name)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		code := 1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
		os.Exit(code)
	}
}
`

func shimExe() (string, error) {
	shimOnce.Do(func() {
		dir, err := os.MkdirTemp("", "docflow-shim-")
		if err != nil {
			shimErr = err
			return
		}
		shimPath = filepath.Join(dir, "shim.exe")
		source := filepath.Join(dir, "main.go")
		if err := os.WriteFile(source, []byte(shimSource), 0o644); err != nil {
			shimErr = err
			return
		}
		build := exec.Command("go", "build", "-o", shimPath, source)
		if b, err := build.CombinedOutput(); err != nil {
			shimErr = fmt.Errorf("build shim: %w: %s", err, b)
		}
	})
	return shimPath, shimErr
}

func copyFile(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
