//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func ptyIoctl(fd uintptr, request uintptr, data unsafe.Pointer) bool {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(data))
	return errno == 0
}

// skillsAcceptance drives the built CLI the way a user does: an unpacked
// release, a temporary HOME, stub agents, a local release server.
type skillsAcceptance struct {
	t       *testing.T
	home    string // temporary HOME
	docflow string // ~/.docflow/bin/docflow after the install
	env     []string
	targets []string
}

func newSkillsAcceptance(t *testing.T) (*skillsAcceptance, string) {
	t.Helper()
	root := t.TempDir()
	a := &skillsAcceptance{t: t, home: filepath.Join(root, "home")}
	stubs := filepath.Join(root, "stubs")
	source := filepath.Join(root, "release")
	for _, dir := range []string{a.home, stubs, source} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, agent := range []string{"claude", "codex", "pi"} {
		writeReleaseFile(t, filepath.Join(stubs, agent), "#!/bin/sh\nexit 0\n", 0755)
	}
	a.env = []string{
		"HOME=" + a.home,
		"PATH=" + stubs,
		"SHELL=/bin/zsh",
		"DOCFLOW_NO_UPDATE_CHECK=1",
	}
	// The personal directories the CLI resolves under the temporary HOME.
	a.targets = []string{
		filepath.Join(a.home, ".claude", "skills"),
		filepath.Join(a.home, ".agents", "skills"),
		filepath.Join(a.home, ".pi", "agent", "skills"),
		filepath.Join(a.home, ".zcode", "skills"),
	}
	if err := os.Mkdir(filepath.Join(a.home, ".zcode"), 0755); err != nil {
		t.Fatal(err)
	}

	buildDocflow(t, filepath.Join(source, "docflow"), "1.0.0")
	writeReleaseFile(t, filepath.Join(source, "LICENSE"), "license", 0644)
	writeReleaseFile(t, filepath.Join(source, "skills", "sample", "SKILL.md"), "---\nname: sample\n---\nversion one\n", 0644)
	a.docflow = filepath.Join(a.home, ".docflow", "bin", "docflow")
	return a, source
}

func buildDocflow(t *testing.T, out, version string) {
	t.Helper()
	build := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", out, "./cmd/docflow")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
}

// command builds a child without a controlling terminal: /dev/tty fails to
// open, so any question would fall back silently instead of hanging.
func (a *skillsAcceptance) command(dir, binary string, args ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = a.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, &stdout, &stderr
}

func (a *skillsAcceptance) run(dir, binary string, args ...string) (string, string) {
	a.t.Helper()
	cmd, stdout, stderr := a.command(dir, binary, args...)
	if err := cmd.Run(); err != nil {
		a.t.Fatalf("%s %v: %v\nstdout=%q\nstderr=%q", binary, args, err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func (a *skillsAcceptance) assertNoPersonalLinks(step string) {
	a.t.Helper()
	for _, target := range a.targets {
		if _, err := os.Lstat(filepath.Join(target, "sample")); !os.IsNotExist(err) {
			a.t.Fatalf("%s: personal skill present in %s: %v", step, target, err)
		}
	}
}

func (a *skillsAcceptance) assertMode(step, want string) {
	a.t.Helper()
	data, err := os.ReadFile(filepath.Join(a.home, ".docflow", "skills-mode"))
	if err != nil || strings.TrimSpace(string(data)) != want {
		a.t.Fatalf("%s: skills-mode = %q, %v; want %q", step, data, err, want)
	}
}

// TestAcceptProjectOnlySkillsInstall walks the P046 Goal: the install asks
// once on a real terminal, update follows the answer, project copies stay
// untouched, and switching back to global restores the personal links.
func TestAcceptProjectOnlySkillsInstall(t *testing.T) {
	a, source := newSkillsAcceptance(t)

	// 1. Install under a pseudo-terminal, answer "p". Stdin is a pipe that
	// nobody writes to (the curl | sh case); the question goes to stderr and
	// the answer comes from the controlling terminal.
	master, slave := openPTY(t)
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinRead.Close()
	defer stdinWrite.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	install := exec.CommandContext(ctx, filepath.Join(source, "docflow"), "install")
	install.Env = a.env
	var installOut bytes.Buffer
	install.Stdin, install.Stdout, install.Stderr = stdinRead, &installOut, slave
	install.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 2}
	if err := install.Start(); err != nil {
		t.Fatal(err)
	}
	// Keep draining the master after the question: on macOS the session
	// leader's exit waits for its terminal output to be read.
	prompt := make(chan string, 1)
	go func() {
		var seen bytes.Buffer
		sent := false
		buf := make([]byte, 256)
		for {
			n, err := master.Read(buf)
			seen.Write(buf[:n])
			if !sent && strings.Contains(seen.String(), "[G/p] ") {
				sent = true
				prompt <- seen.String()
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case text := <-prompt:
		if !strings.Contains(text, "link into agent directories (global) or only per project (project)? [G/p] ") {
			t.Fatalf("question text = %q", text)
		}
	case <-time.After(30 * time.Second):
		install.Process.Kill()
		t.Fatal("no skills mode question on the terminal")
	}
	if _, err := master.Write([]byte("p\n")); err != nil {
		t.Fatal(err)
	}
	if err := install.Wait(); err != nil {
		t.Fatalf("install: %v\nstdout=%q", err, installOut.String())
	}
	slave.Close()
	if !strings.Contains(installOut.String(), "skills: project mode") {
		t.Fatalf("install stdout = %q", installOut.String())
	}
	a.assertMode("install", "project")
	a.assertNoPersonalLinks("install")

	// 2. Update to a newer release: no question, still no personal links.
	newer := filepath.Join(t.TempDir(), "docflow")
	buildDocflow(t, newer, "1.1.0")
	binary, err := os.ReadFile(newer)
	if err != nil {
		t.Fatal(err)
	}
	archive := releaseArchiveWithBinary(t, string(binary), "---\nname: sample\n---\nversion two\n")
	archiveName := fmt.Sprintf("docflow_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/download/VERSION":
			fmt.Fprintln(w, "1.1.0")
		case "/download/v1.1.0/checksums.txt":
			fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(archive), archiveName)
		case "/download/v1.1.0/" + archiveName:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a.env = append(a.env, "DOCFLOW_RELEASE_URL="+server.URL)
	outside := t.TempDir()
	stdout, stderr := a.run(outside, a.docflow, "update")
	if !strings.Contains(stdout, "updated: ") || !strings.Contains(stdout, "-> 1.1.0") {
		t.Fatalf("update stdout = %q", stdout)
	}
	if strings.Contains(stderr, "[G/p]") {
		t.Fatalf("update asked the question: %q", stderr)
	}
	a.assertMode("update", "project")
	a.assertNoPersonalLinks("update")

	// 3. A project copy from docflow init --skills survives update --skills
	// project.
	project := t.TempDir()
	a.run(project, a.docflow, "init", "--skills")
	copyPath := filepath.Join(project, ".claude", "skills", "sample", "SKILL.md")
	before, err := os.ReadFile(copyPath)
	if err != nil || !strings.Contains(string(before), "version two") {
		t.Fatalf("project copy = %q, %v", before, err)
	}
	beforeInfo, _ := os.Stat(copyPath)
	stdout, _ = a.run(project, a.docflow, "update", "--skills", "project")
	if !strings.Contains(stdout, "up to date: 1.1.0") {
		t.Fatalf("update --skills project stdout = %q", stdout)
	}
	after, err := os.ReadFile(copyPath)
	afterInfo, _ := os.Stat(copyPath)
	if err != nil || !bytes.Equal(before, after) || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatalf("project copy changed: %q, %v", after, err)
	}
	a.assertNoPersonalLinks("update --skills project")

	// 4. update --skills global brings the personal links back, into the
	// active release.
	a.run(outside, a.docflow, "update", "--skills", "global")
	a.assertMode("update --skills global", "global")
	want := filepath.Join(a.home, ".docflow", "current", "skills", "sample")
	for _, target := range a.targets {
		got, err := os.Readlink(filepath.Join(target, "sample"))
		if err != nil || got != want {
			t.Fatalf("Readlink(%s/sample) = %q, %v; want %q", target, got, err, want)
		}
	}
	if data, err := os.ReadFile(copyPath); err != nil || !bytes.Equal(data, before) {
		t.Fatalf("project copy changed by global: %q, %v", data, err)
	}
}
