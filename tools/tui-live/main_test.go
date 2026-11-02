//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testBinary string

func TestMain(m *testing.M) {
	root, err := repoRoot()
	if err != nil {
		panic(err)
	}
	dir, err := os.MkdirTemp("", "tui-live-binary-")
	if err != nil {
		panic(err)
	}
	testBinary = filepath.Join(dir, "docflow")
	cmd := exec.Command("go", "build", "-o", testBinary, "./cmd/docflow")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("build docflow: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func testCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.name", "live-test"}, {"config", "user.email", "live-test@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := gitOutput(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("live acceptance checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(dir, "commit", "-qm", "init"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeCLIs puts the streaming fixtures on PATH under the real command names.
func fakeCLIs(t *testing.T) {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, script := range map[string]string{"pi": "fake-pi.sh", "codex": "fake-codex.sh"} {
		source := filepath.Join(root, "tools", "tui-live", "testdata", script)
		if err := os.Symlink(source, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TUI_LIVE_SLEEP", "1")
}

func runSuccess(t *testing.T, checkout string) receipt {
	t.Helper()
	fakeCLIs(t)
	var out bytes.Buffer
	args := []string{"--checkout", checkout, "--binary", testBinary, "--tool-timeout", "20s"}
	if err := runCommand(args, &out); err != nil {
		t.Fatalf("--run: %v\n%s", err, out.String())
	}
	return readReceipt(t, checkout)
}

func readReceipt(t *testing.T, checkout string) receipt {
	t.Helper()
	_, gitDir, _, err := checkoutState(checkout)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(receiptFile)))
	if err != nil {
		t.Fatal(err)
	}
	var r receipt
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func writeTestReceipt(t *testing.T, checkout string, r receipt) {
	t.Helper()
	_, gitDir, _, err := checkoutState(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeReceipt(filepath.Join(gitDir, filepath.FromSlash(receiptFile)), r); err != nil {
		t.Fatal(err)
	}
}

func TestRunWritesReceiptAndCheckAcceptsIt(t *testing.T) {
	checkout := testCheckout(t)
	r := runSuccess(t, checkout)
	if !r.OK || len(r.Scenarios) != 2 {
		t.Fatalf("receipt = %+v", r)
	}
	for _, scenario := range r.Scenarios {
		if len(scenario.Calls) == 0 || !scenario.ToolCall || !scenario.Pause.CheckpointWritten || scenario.Pause.Stage != "agent" {
			t.Fatalf("%s: incomplete scenario receipt: %+v", scenario.Name, scenario)
		}
		if scenario.Pause.LiveProcesses != 0 || !scenario.Pause.LockReleased {
			t.Fatalf("%s: the pause left a process or the lock: %+v", scenario.Name, scenario.Pause)
		}
		if !scenario.Independent.OK || !scenario.TreeClean {
			t.Fatalf("%s: independent check or tree failed: %+v", scenario.Name, scenario)
		}
		for _, c := range scenario.Calls {
			if c.DurationSeconds > defaultCallLimit.Seconds() {
				t.Fatalf("%s: call %d too long: %v", scenario.Name, c.Attempt, c.DurationSeconds)
			}
		}
	}
	var out bytes.Buffer
	if err := checkCommand([]string{"--checkout", checkout, "--binary", testBinary}, &out); err != nil {
		t.Fatalf("--check rejected its own receipt: %v", err)
	}
	if !strings.Contains(out.String(), "2 scenarios") {
		t.Fatalf("--check output = %q", out.String())
	}
}

func TestCheckRejectsAnotherRunLimit(t *testing.T) {
	checkout := testCheckout(t)
	r := runSuccess(t, checkout)
	if r.Scenarios[0].Attempts != 1 || r.Scenarios[0].Replans != 0 {
		t.Fatalf("default receipt limits = %d/%d, want 1/0", r.Scenarios[0].Attempts, r.Scenarios[0].Replans)
	}
	r.Scenarios[0].Attempts = 3
	writeTestReceipt(t, checkout, r)
	if err := checkCommand([]string{"--checkout", checkout, "--binary", testBinary}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "limits") {
		t.Fatalf("--check accepted another run limit: err = %v", err)
	}
}

func TestCheckRejectsAnotherHeadOrBinary(t *testing.T) {
	checkout := testCheckout(t)
	runSuccess(t, checkout)

	other := filepath.Join(t.TempDir(), "docflow")
	data, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, append(data, 'x'), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkCommand([]string{"--checkout", checkout, "--binary", other}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("other binary: err = %v, want a binary mismatch", err)
	}

	if err := os.WriteFile(filepath.Join(checkout, "next.txt"), []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(checkout, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(checkout, "commit", "-qm", "next"); err != nil {
		t.Fatal(err)
	}
	if err := checkCommand([]string{"--checkout", checkout, "--binary", testBinary}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "HEAD") {
		t.Fatalf("other HEAD: err = %v, want a HEAD mismatch", err)
	}
}

func TestCheckRejectsUnprovenPauseOrMissingToolCall(t *testing.T) {
	checkout := testCheckout(t)
	r := runSuccess(t, checkout)
	_, _, head, err := checkoutState(checkout)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := fileHash(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	brokenPause := r
	brokenPause.Scenarios = append([]scenarioReceipt{}, r.Scenarios...)
	brokenPause.Scenarios[0].Pause.CheckpointWritten = false
	if err := validateReceipt(brokenPause, head, hash, 1, 0, defaultMaxCalls, defaultCallLimit, defaultMaxAge, true); err == nil || !strings.Contains(err.Error(), "pause is not proven") {
		t.Fatalf("unproven pause: err = %v", err)
	}
	missingTool := r
	missingTool.Scenarios = append([]scenarioReceipt{}, r.Scenarios...)
	missingTool.Scenarios[0].ToolCall = false
	if err := validateReceipt(missingTool, head, hash, 1, 0, defaultMaxCalls, defaultCallLimit, defaultMaxAge, true); err == nil || !strings.Contains(err.Error(), "no tool call") {
		t.Fatalf("missing tool call: err = %v", err)
	}
}

func TestRunStopsAtCallLimit(t *testing.T) {
	checkout := testCheckout(t)
	fakeCLIs(t)
	var out bytes.Buffer
	err := runCommand([]string{"--checkout", checkout, "--binary", testBinary, "--max-calls", "1", "--tool-timeout", "20s"}, &out)
	if err == nil || !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatalf("--run with one call allowed: err = %v\n%s", err, out.String())
	}
}

func TestRunFailsWithoutToolCall(t *testing.T) {
	checkout := testCheckout(t)
	fakeCLIs(t)
	t.Setenv("TUI_LIVE_TOOL", "0")
	var out bytes.Buffer
	err := runCommand([]string{"--checkout", checkout, "--binary", testBinary, "--tool-timeout", "2s"}, &out)
	if err == nil || !strings.Contains(err.Error(), "no tool call") {
		t.Fatalf("--run without a tool call: err = %v\n%s", err, out.String())
	}
}

func TestRunFailsWhenIndependentCheckFails(t *testing.T) {
	checkout := testCheckout(t)
	fakeCLIs(t)
	t.Setenv("TUI_LIVE_CONTENT", "wrong")
	var out bytes.Buffer
	err := runCommand([]string{"--checkout", checkout, "--binary", testBinary, "--tool-timeout", "20s"}, &out)
	if err == nil || !strings.Contains(err.Error(), "independent check failed") {
		t.Fatalf("--run with a bad result: err = %v\n%s", err, out.String())
	}
	r := readReceipt(t, checkout)
	if r.OK || len(r.Scenarios) == 0 || r.Scenarios[0].Independent.OK {
		t.Fatalf("failed receipt = %+v", r)
	}
}

func TestRunRejectsMissingBinary(t *testing.T) {
	checkout := testCheckout(t)
	missing := filepath.Join(t.TempDir(), "no-docflow")
	var out bytes.Buffer
	err := runCommand([]string{"--checkout", checkout, "--binary", missing}, &out)
	if err == nil {
		t.Fatal("--run accepted a missing binary")
	}
}

func TestReceiptMaxAge(t *testing.T) {
	checkout := testCheckout(t)
	r := runSuccess(t, checkout)
	_, _, head, err := checkoutState(checkout)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := fileHash(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	r.CreatedAt = time.Now().Add(-8 * 24 * time.Hour)
	if err := validateReceipt(r, head, hash, 1, 0, defaultMaxCalls, defaultCallLimit, defaultMaxAge, true); err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("old receipt: err = %v", err)
	}
}

// One line with a trailing line break is a correct answer: every agent CLI
// terminates the file it writes. The live Codex route failed on the old
// byte-exact comparison while its files were right.
func TestIndependentTestAcceptsTrailingLineBreak(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{
		"go.mod":       "module fixture\n\ngo 1.20\n",
		"live_test.go": independentTest,
		"a.txt":        "a\n",
		"b.txt":        "b\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if res := independentCheck(dir); !res.OK {
		t.Fatalf("a one-line file with a trailing break was rejected: %s", res.Output)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if independentCheck(dir).OK {
		t.Fatal("a second, empty line passed the independent check")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("wrong\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if independentCheck(dir).OK {
		t.Fatal("a wrong a.txt passed the independent check")
	}
}
