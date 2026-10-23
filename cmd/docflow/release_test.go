package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"bsv-docflow/internal/release"
	docskills "bsv-docflow/internal/skills"
)

type releaseFixture struct {
	homeDir    string
	home       string
	binDir     string
	source     string
	executable string
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	f := &releaseFixture{homeDir: t.TempDir(), binDir: t.TempDir(), source: t.TempDir()}
	t.Setenv("HOME", f.homeDir)
	t.Setenv("PATH", f.binDir)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("DOCFLOW_NO_UPDATE_CHECK", "")
	f.home = filepath.Join(f.homeDir, ".docflow")
	writeReleaseFile(t, filepath.Join(f.source, "docflow"), "binary", 0755)
	writeReleaseFile(t, filepath.Join(f.source, "LICENSE"), "license", 0644)
	writeReleaseFile(t, filepath.Join(f.source, "skills", "sample", "SKILL.md"), "---\nname: sample\n---\nversion one\n", 0644)
	f.executable = filepath.Join(f.source, "docflow")
	oldExecutable, oldTerminal, oldVersion, oldSkills := currentExecutable, stderrIsTerminal, version, currentSkillDirectory
	currentExecutable = func() (string, error) { return f.executable, nil }
	currentSkillDirectory = func() (string, error) { return docskills.SkillDirFromExecutable(f.executable) }
	stderrIsTerminal = func(io.Writer) bool { return false }
	version = "1.0.0"
	t.Cleanup(func() {
		currentExecutable, stderrIsTerminal, version = oldExecutable, oldTerminal, oldVersion
		currentSkillDirectory = oldSkills
	})
	return f
}

func writeReleaseFile(t *testing.T, name, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}

func runRelease(t *testing.T, wantCode int, args ...string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != wantCode {
		t.Fatalf("run(%q) = %d, want %d; stdout=%q stderr=%q", args, code, wantCode, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func releaseLink(t *testing.T, name, want string) {
	t.Helper()
	if got, err := os.Readlink(name); err != nil || got != want {
		t.Fatalf("Readlink(%s) = %q, %v; want %q", name, got, err, want)
	}
}

func TestReleaseCommandsRefuseDevelopmentCheckout(t *testing.T) {
	f := newReleaseFixture(t)
	if err := os.Mkdir(filepath.Join(f.source, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"update", "uninstall"} {
		_, stderr := runRelease(t, 1, command)
		if !strings.Contains(stderr, "development checkout; use git pull && bash tools/install-dev.sh") {
			t.Fatalf("%s: %q", command, stderr)
		}
	}
	if _, err := os.Stat(f.home); !os.IsNotExist(err) {
		t.Fatalf("development commands touched installation: %v", err)
	}
}

func TestInstallRequiresReleasePayload(t *testing.T) {
	for _, missing := range []string{"LICENSE", "skills"} {
		t.Run(missing, func(t *testing.T) {
			f := newReleaseFixture(t)
			if err := os.RemoveAll(filepath.Join(f.source, missing)); err != nil {
				t.Fatal(err)
			}
			_, stderr := runRelease(t, 1, "install")
			if !strings.Contains(stderr, "incomplete release") {
				t.Fatalf("stderr = %q", stderr)
			}
			if _, err := os.Stat(f.home); !os.IsNotExist(err) {
				t.Fatalf("incomplete release touched installation: %v", err)
			}
		})
	}
}

func TestInstallWithoutAgentsAndUnknownShell(t *testing.T) {
	f := newReleaseFixture(t)
	t.Setenv("SHELL", "/bin/fish")
	stdout, _ := runRelease(t, 0, "install")
	if !strings.Contains(stdout, "Add "+filepath.Join(f.home, "bin")+" to your shell's PATH.") {
		t.Fatalf("stdout = %q", stdout)
	}
	for _, dir := range []string{".claude", ".agents", ".pi", ".zcode"} {
		if _, err := os.Stat(filepath.Join(f.homeDir, dir)); !os.IsNotExist(err) {
			t.Fatalf("unexpected agent directory %s: %v", dir, err)
		}
	}
}

func TestInstallPreservesForeignSkills(t *testing.T) {
	f := newReleaseFixture(t)
	writeReleaseFile(t, filepath.Join(f.binDir, "codex"), "#!/bin/sh\nexit 0\n", 0755)
	foreign := filepath.Join(f.homeDir, ".agents", "skills", "sample", "SKILL.md")
	writeReleaseFile(t, foreign, "Ruleblend-owned", 0644)
	_, stderr := runRelease(t, 2, "install")
	if !strings.Contains(stderr, "exists and is not a link") {
		t.Fatalf("stderr = %q", stderr)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "Ruleblend-owned" {
		t.Fatalf("foreign skill changed: %q, %v", got, err)
	}
}

func testReleaseArchive(t *testing.T, text string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for _, entry := range []struct {
		name, body string
		mode       int64
	}{
		{"docflow", "new binary", 0755},
		{"LICENSE", "license", 0644},
		{"skills/sample/SKILL.md", text, 0644},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestReleaseInstallUpdateUninstall(t *testing.T) {
	f := newReleaseFixture(t)
	var targets []string
	for _, agent := range []string{"claude", "codex", "pi"} {
		writeReleaseFile(t, filepath.Join(f.binDir, agent), "#!/bin/sh\nexit 0\n", 0755)
		target, err := docskills.AgentSkillsDir(agent, "")
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	if err := os.Mkdir(filepath.Join(f.homeDir, ".zcode"), 0755); err != nil {
		t.Fatal(err)
	}
	targets = append(targets, filepath.Join(f.homeDir, ".zcode", "skills"))
	profile := filepath.Join(f.homeDir, ".zshrc")
	writeReleaseFile(t, profile, "# personal configuration\n", 0644)
	stdout, stderr := runRelease(t, 0, "install")
	if !strings.Contains(stdout, "installed: 1.0.0") || !strings.Contains(stdout, filepath.Join(f.home, "bin", "docflow")) || !strings.Contains(stdout, "export PATH=") || stderr != "" {
		t.Fatalf("install stdout=%q stderr=%q", stdout, stderr)
	}
	releaseLink(t, filepath.Join(f.home, "bin", "docflow"), "../current/docflow")
	releaseLink(t, filepath.Join(f.home, "current"), "versions/1.0.0")
	for _, target := range targets {
		releaseLink(t, filepath.Join(target, "sample"), filepath.Join(f.home, "current", "skills", "sample"))
	}
	runRelease(t, 0, "install") // Repeat from the unpacked archive.
	f.executable = filepath.Join(f.home, "bin", "docflow")
	runRelease(t, 0, "install") // Repeat from the installed symlink.
	if data, err := os.ReadFile(profile); err != nil || strings.Count(string(data), "# docflow\n") != 1 {
		t.Fatalf("PATH duplicated: %q, %v", data, err)
	}
	if got, err := docskills.SkillDirFromExecutable(f.executable); err != nil || got != filepath.Join(f.home, "current", "skills") {
		t.Fatalf("skill source = %q, %v", got, err)
	}
	// A user's home can itself be under a checkout: release mode takes priority.
	if err := os.Mkdir(filepath.Join(f.homeDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, ok := repositoryForExecutable(f.executable); ok || versionString(f.executable) != version {
		t.Fatal("release executable detected as a development checkout")
	}
	latest := "1.1.0"
	archive := testReleaseArchive(t, "---\nname: sample\n---\nupdated\n")
	archiveName := fmt.Sprintf("docflow_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/latest/download/VERSION":
			fmt.Fprintln(w, latest)
		case "/download/v" + latest + "/checksums.txt":
			fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(archive), archiveName)
		case "/download/v" + latest + "/" + archiveName:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_RELEASE_URL", server.URL)
	stdout, _ = runRelease(t, 0, "update", "--check")
	if stdout != "update available: 1.0.0 -> 1.1.0\n" || len(requests) != 1 {
		t.Fatalf("check stdout=%q requests=%v", stdout, requests)
	}
	releaseLink(t, filepath.Join(f.home, "current"), "versions/1.0.0")
	for _, next := range []string{"1.1.0", "1.2.0"} {
		latest = next
		stdout, _ = runRelease(t, 0, "update")
		if !strings.Contains(stdout, "updated: "+version+" -> "+latest) || !strings.Contains(stdout, "docflow install-skills --project") {
			t.Fatalf("update stdout=%q", stdout)
		}
		releaseLink(t, filepath.Join(f.home, "current"), "versions/"+latest)
		for _, target := range targets {
			releaseLink(t, filepath.Join(target, "sample"), filepath.Join(f.home, "current", "skills", "sample"))
			if got, err := os.ReadFile(filepath.Join(target, "sample", "SKILL.md")); err != nil || !strings.Contains(string(got), "updated") {
				t.Fatalf("skill did not survive update: %q, %v", got, err)
			}
		}
		version = next // Simulate the next invocation of the installed binary.
	}
	entries, err := os.ReadDir(filepath.Join(f.home, "versions"))
	if err != nil || len(entries) != 2 || entries[0].Name() != "1.1.0" || entries[1].Name() != "1.2.0" {
		t.Fatalf("retained versions = %v, %v", entries, err)
	}
	for _, remote := range []string{"1.2.0", "1.1.0"} {
		latest = remote
		for _, args := range [][]string{{"update"}, {"update", "--check"}} {
			stdout, _ = runRelease(t, 0, args...)
			if stdout != "up to date: 1.2.0\n" {
				t.Fatalf("up-to-date stdout=%q", stdout)
			}
		}
	}
	// Uninstall removes own links, including relative and dangling ones, while
	// preserving foreign symlinks, directories, project copies and profiles.
	foreign := filepath.Join(f.homeDir, ".docflow-other", "skill")
	writeReleaseFile(t, filepath.Join(foreign, "SKILL.md"), "foreign", 0644)
	for _, target := range targets {
		if err := os.Symlink(foreign, filepath.Join(target, "foreign")); err != nil {
			t.Fatal(err)
		}
		writeReleaseFile(t, filepath.Join(target, "managed", "SKILL.md"), "managed", 0644)
		dangling, err := filepath.Rel(target, filepath.Join(f.home, "current", "skills", "gone"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dangling, filepath.Join(target, "gone")); err != nil {
			t.Fatal(err)
		}
	}
	runRelease(t, 0, "uninstall")
	if _, err := os.Stat(f.home); !os.IsNotExist(err) {
		t.Fatalf("installation remains: %v", err)
	}
	for _, target := range targets {
		for _, name := range []string{"sample", "gone"} {
			if _, err := os.Lstat(filepath.Join(target, name)); !os.IsNotExist(err) {
				t.Fatalf("own link remains: %s: %v", name, err)
			}
		}
		releaseLink(t, filepath.Join(target, "foreign"), foreign)
		if _, err := os.Stat(filepath.Join(target, "managed", "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
	if data, err := os.ReadFile(profile); err != nil || string(data) != "# personal configuration\n" {
		t.Fatalf("profile damaged: %q, %v", data, err)
	}
}

func TestDailyReleaseCheck(t *testing.T) {
	f := newReleaseFixture(t)
	runRelease(t, 0, "install")
	f.executable = filepath.Join(f.home, "bin", "docflow")
	stderrIsTerminal = func(io.Writer) bool { return true }
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/latest/download/VERSION" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		fmt.Fprintln(w, "1.1.0")
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_RELEASE_URL", server.URL)
	// A normal command exercises the hook in run, independent of project state.
	_, stderr := runRelease(t, 0, "install-skills", "--target", filepath.Join(f.homeDir, "test-skills"))
	if requests != 1 || !strings.Contains(stderr, "docflow 1.1.0 available (current 1.0.0): docflow update\n") {
		t.Fatalf("first check: requests=%d stderr=%q", requests, stderr)
	}
	_, stderr = runRelease(t, 0, "install-skills", "--target", filepath.Join(f.homeDir, "test-skills"))
	if requests != 1 || stderr != "" {
		t.Fatalf("repeated check: requests=%d stderr=%q", requests, stderr)
	}
	if err := release.RecordCheck(f.home, time.Now().Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, stderr = runRelease(t, 0, "install-skills", "--target", filepath.Join(f.homeDir, "test-skills"))
	if requests != 2 || stderr == "" {
		t.Fatalf("next-day check: requests=%d stderr=%q", requests, stderr)
	}
}

func TestDailyReleaseCheckGuards(t *testing.T) {
	for _, guard := range []string{"pipe", "disabled", "development", "install", "update", "uninstall"} {
		t.Run(guard, func(t *testing.T) {
			f := newReleaseFixture(t)
			runRelease(t, 0, "install")
			f.executable = filepath.Join(f.home, "bin", "docflow")
			stderrIsTerminal = func(io.Writer) bool { return true }
			command := "status"
			switch guard {
			case "pipe":
				stderrIsTerminal = func(io.Writer) bool { return false }
			case "disabled":
				t.Setenv("DOCFLOW_NO_UPDATE_CHECK", "1")
			case "development":
				f.executable = filepath.Join(f.source, "docflow")
			default:
				command = guard
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("guard did not suppress network request")
				fmt.Fprintln(w, "9.0.0")
			}))
			defer server.Close()
			t.Setenv("DOCFLOW_RELEASE_URL", server.URL)
			var stderr bytes.Buffer
			checkReleaseUpdate(command, &stderr)
			if stderr.Len() != 0 {
				t.Fatalf("guard wrote stderr: %q", stderr.String())
			}
			if _, err := os.Stat(filepath.Join(f.home, "last-update-check")); !os.IsNotExist(err) {
				t.Fatalf("guard recorded check: %v", err)
			}
		})
	}
}

func TestDailyReleaseCheckSilencesErrors(t *testing.T) {
	f := newReleaseFixture(t)
	runRelease(t, 0, "install")
	f.executable = filepath.Join(f.home, "bin", "docflow")
	stderrIsTerminal = func(io.Writer) bool { return true }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_RELEASE_URL", server.URL)
	var stderr bytes.Buffer
	checkReleaseUpdate("status", &stderr)
	if stderr.Len() != 0 || release.CheckDue(f.home, time.Now()) {
		t.Fatalf("failed check was not silenced/recorded: stderr=%q", stderr.String())
	}
}

func TestDailyReleaseCheckTimeout(t *testing.T) {
	f := newReleaseFixture(t)
	runRelease(t, 0, "install")
	f.executable = filepath.Join(f.home, "bin", "docflow")
	stderrIsTerminal = func(io.Writer) bool { return true }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
			http.Error(w, "slow server", http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_RELEASE_URL", server.URL)
	var stderr bytes.Buffer
	start := time.Now()
	checkReleaseUpdate("status", &stderr)
	if elapsed := time.Since(start); elapsed < time.Second || elapsed > 4*time.Second {
		t.Fatalf("2-second timeout took %s", elapsed)
	}
	if stderr.Len() != 0 || release.CheckDue(f.home, time.Now()) {
		t.Fatalf("timeout was not silenced/recorded: stderr=%q", stderr.String())
	}
}

func TestReleaseUpdateChecksumFailurePreservesCurrent(t *testing.T) {
	f := newReleaseFixture(t)
	runRelease(t, 0, "install")
	f.executable = filepath.Join(f.home, "bin", "docflow")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/download/VERSION":
			fmt.Fprintln(w, "1.1.0")
		case "/download/v1.1.0/checksums.txt":
			fmt.Fprintf(w, "%s  docflow_%s_%s.tar.gz\n", strings.Repeat("0", 64), runtime.GOOS, runtime.GOARCH)
		default:
			io.WriteString(w, "corrupted archive")
		}
	}))
	defer server.Close()
	t.Setenv("DOCFLOW_RELEASE_URL", server.URL)
	_, stderr := runRelease(t, 1, "update")
	if !strings.Contains(stderr, "sha256 mismatch") {
		t.Fatalf("stderr = %q", stderr)
	}
	releaseLink(t, filepath.Join(f.home, "current"), "versions/1.0.0")
	if _, err := os.Stat(filepath.Join(f.home, "versions", "1.1.0")); !os.IsNotExist(err) {
		t.Fatalf("failed update left version: %v", err)
	}
}

func TestReleaseModeUsesResolvedPath(t *testing.T) {
	f := newReleaseFixture(t)
	runRelease(t, 0, "install")
	outside := filepath.Join(f.home, "versions-other", "docflow")
	writeReleaseFile(t, outside, "outside", 0755)
	escape := filepath.Join(f.home, "versions", "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{f.executable, outside, escape, ""} {
		if release.InstalledExecutable(name) {
			t.Fatalf("not an installed executable: %q", name)
		}
	}
	// Resolve the home and executable consistently when HOME is a symlink.
	homeLink := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(f.homeDir, homeLink); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeLink)
	if !release.InstalledExecutable(filepath.Join(f.home, "bin", "docflow")) {
		t.Fatal("installed executable missed with symlinked HOME")
	}
}

func TestReleaseTerminalRejectsNonTerminals(t *testing.T) {
	if releaseTerminal(&bytes.Buffer{}) {
		t.Fatal("buffer detected as terminal")
	}
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if releaseTerminal(file) {
		t.Fatal("null character device detected as terminal")
	}
}
