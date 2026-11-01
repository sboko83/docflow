//go:build darwin || linux

package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/runner"
	"bsv-docflow/internal/testshim"
)

// TestActivityTerminalFrames renders the mixed history of all three agent
// CLIs at terminal sizes and checks that agent output reaches the terminal as
// text: no raw escape sequence, no over-wide line and no hidden control byte.
func TestActivityTerminalFrames(t *testing.T) {
	codex, codexLog := parseActivityLines(t, "codex", []string{
		`{"type":"item.started","item":{"id":"c1","type":"command_execution","command":"printf '\u001b[31mred\u001b[0m\n'"}}`,
		`{"type":"item.completed","item":{"id":"c1","type":"command_execution","aggregated_output":"\u001b[32mgreen\u001b[0m","exit_code":0}}`,
	})
	pi, piLog := parseActivityLines(t, "pi", []string{
		`{"type":"message_end","message":{"role":"assistant","usage":{"input":2,"output":1,"cost":{"total":0.001}},"content":[{"type":"thinking","thinking":"plan \u001b]0;title\u0007 thinking"},{"type":"text","text":"answer \u001b[1mbold\u001b[0m"}]}}`,
	})
	claude, claudeLog := parseActivityLines(t, "claude", []string{
		`{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"claude text"}]}}`,
		`not json`,
	})
	unit := "P001.1.2"
	events := []runner.Event{streamEvent(unit, 1, codexLog)}
	for _, a := range codex {
		events = append(events, activityEvent(unit, a))
	}
	events = append(events, streamEvent(unit, 2, piLog))
	for _, a := range pi {
		events = append(events, activityEvent(unit, a))
	}
	events = append(events, streamEvent(unit, 3, claudeLog))
	for _, a := range claude {
		events = append(events, activityEvent(unit, a))
	}
	events = append(events, activityEvent(unit, agentio.Activity{
		Kind: agentio.ActivityKindStderr, Raw: "stderr \u001b[31mred\u001b[0m",
	}))
	model := historyModel(t, events...)
	model = send(t, model, keyMsg("enter"))
	model = send(t, model, keyMsg("end"))

	for _, size := range []struct{ w, h int }{{120, 40}, {80, 24}, {40, 12}} {
		rendered := resize(t, model, size.w, size.h).String()
		lines := strings.Split(rendered, "\n")
		if len(lines) != size.h {
			t.Fatalf("%dx%d rendered %d lines", size.w, size.h, len(lines))
		}
		for _, line := range lines {
			if strings.ContainsAny(line, "\x00\x07\x1b") {
				t.Fatalf("%dx%d frame contains a control sequence: %q", size.w, size.h, line)
			}
		}
		if size.w >= 100 {
			for _, want := range []string{"red", "green", "answer", "claude text", "stderr"} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("%dx%d frame lost %q", size.w, size.h, want)
				}
			}
		} else if size.w >= 80 {
			if !strings.Contains(rendered, "Activity") {
				t.Fatalf("%dx%d frame lost the activity section", size.w, size.h)
			}
		}
	}
}

// TestTerminalDiffAndVerifyKeys drives the real CLI in a PTY and checks the
// diff and verify overlays report their read errors instead of inventing an
// empty change set or a successful check.
func TestTerminalDiffAndVerifyKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("terminal test builds the CLI")
	}
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "docflow")
	build := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
	build.Dir = root
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	project := writeTestProject(t)
	master, slave := openTestPTY(t)
	if err := setTestWinsize(slave, 120, 40); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(bin, "tui")
	child.Dir = project
	child.Env = append(os.Environ(), "TERM=xterm-256color", "NO_COLOR=1")
	child.Stdin = slave
	child.Stdout = slave
	child.Stderr = slave
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var screen bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				mu.Lock()
				screen.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	output := func() string {
		mu.Lock()
		defer mu.Unlock()
		return screen.String()
	}
	waitFor := func(text string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(output(), text) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("terminal output does not contain %q\n%s", text, output())
	}

	waitFor("Queue")
	for i := 0; i < 4; i++ {
		writeAll(t, master, "\x1b[B")
		time.Sleep(30 * time.Millisecond)
	}
	writeAll(t, master, "\r")
	time.Sleep(100 * time.Millisecond)
	writeAll(t, master, "d")
	waitFor("cannot read git diff")
	writeAll(t, master, "\x1b")
	time.Sleep(300 * time.Millisecond)
	writeAll(t, master, "v")
	waitFor("no verify event recorded")
	writeAll(t, master, "\x1b")
	time.Sleep(300 * time.Millisecond)
	writeAll(t, master, "q")

	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("tui exited with %v\n%s", err, output())
		}
	case <-time.After(10 * time.Second):
		_ = child.Process.Kill()
		t.Fatalf("tui did not exit\n%s", output())
	}
	_ = master.Close()
	<-readDone
}

// TestTerminalRunPauseResumeQuit drives the real CLI in a PTY with the shell
// fake agent: the start dialog is accepted, the live call is paused at a
// checkpoint, resumed, and the workspace exits only after the runner stopped.
func TestTerminalRunPauseResumeQuit(t *testing.T) {
	if testing.Short() {
		t.Skip("terminal test builds the CLI")
	}
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "docflow")
	build := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
	build.Dir = root
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	project, config, scenario := writeRunnableProject(t)
	master, slave := openTestPTY(t)
	if err := setTestWinsize(slave, 120, 40); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(bin, "tui")
	child.Dir = project
	child.Env = append(os.Environ(),
		"TERM=xterm-256color", "NO_COLOR=1",
		"DOCFLOW_AGENTS="+config,
		"FAKE_AGENT_SCENARIO="+scenario,
		"DOCFLOW_NOTIFY=0", "DOCFLOW_SKILLS_CHECK=0",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	)
	child.Stdin, child.Stdout, child.Stderr = slave, slave, slave
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var screen bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				mu.Lock()
				screen.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	output := func() string {
		mu.Lock()
		defer mu.Unlock()
		return screen.String()
	}
	waitFor := func(text string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(output(), text) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("terminal output does not contain %q\n%s", text, output())
	}
	sendKey := func(key string) {
		writeAll(t, master, key)
		time.Sleep(100 * time.Millisecond)
	}

	waitFor("Queue")
	sendKey("s")
	waitFor("Start the current Queue run?")
	sendKey("\r")

	// The status line is redrawn as a diff, so the test follows the files the
	// runner writes: the call counter, the checkpoint and the checkout lock.
	scenarioLog := scenario + ".n"
	waitFile := func(path string, present bool, timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			_, err := os.Stat(path)
			if present == (err == nil) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("file %s present=%v not reached\n%s", path, present, output())
	}
	lock := filepath.Join(project, ".git", "docflow", "run", "run.lock")
	waitFile(scenarioLog, true, 20*time.Second)
	sendKey("p")

	checkpoint := filepath.Join(project, ".git", "docflow", "tui", "checkpoint.json")
	waitFile(checkpoint, true, 20*time.Second)
	waitFile(lock, false, 20*time.Second)
	time.Sleep(200 * time.Millisecond)
	sendKey("R")
	waitFile(lock, true, 20*time.Second)

	waitFile(filepath.Join(project, "a.txt"), true, 30*time.Second)
	waitFile(lock, false, 30*time.Second)
	planText, err := os.ReadFile(filepath.Join(project, "docs", "plans", "P001-tui.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(planText), "[x] P001.1.1") {
		t.Fatalf("resumed run did not complete the unit:\n%s", planText)
	}
	// The final notice can be replaced by the tree reload before the test
	// reads it, so ask to quit and confirm the exit dialog if the workspace
	// still believes the run is live.
	sendKey("q")
	time.Sleep(200 * time.Millisecond)
	_, _ = master.WriteString("\r")

	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("tui exited with %v\n%s", err, output())
		}
	case <-time.After(20 * time.Second):
		_ = child.Process.Kill()
		t.Fatalf("tui did not exit after the run finished\n%s", output())
	}
	_ = master.Close()
	<-readDone
	if _, err := os.Stat(filepath.Join(project, ".git", "docflow", "run", "run.lock")); !os.IsNotExist(err) {
		t.Fatalf("checkout lock left after exit: %v", err)
	}
}

func writeRunnableProject(t *testing.T) (project, config, scenario string) {
	t.Helper()
	project = t.TempDir()
	write := func(path, text string) {
		t.Helper()
		full := filepath.Join(project, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/docflow.md", "---\ndocflow: 3\n---\n## Queue\n- P001\n\n## Frozen\n\n## Backlog\n")
	write("docs/plans/P001-tui.md", `---
id: P001
title: TUI run
status: active
created: 2026-11-01
---
## Goal

Exercise the interactive runner.

## Phase 1: Run

### [ ] P001.1.1 Live control
Do:
- write a.txt
Verify: `+"`test -f a.txt`"+`
Commit: P001.1.1: live control
`)
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.name", "t"}, {"config", "user.email", "t@example.invalid"},
		{"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "init"},
	} {
		command := exec.Command("git", args...)
		command.Dir = project
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	scenario = filepath.Join(t.TempDir(), "steps")
	if err := os.MkdirAll(scenario, 0o755); err != nil {
		t.Fatal(err)
	}
	result := `{"type":"result","is_error":false,"result":"ok","session_id":"s1","structured_output":{"status":"done","handoff":["done"]}}`
	if err := os.WriteFile(filepath.Join(scenario, "0.sh"), []byte("sleep 5\nprintf a > a.txt\nprintf '%s\\n' '"+result+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scenario, "1.sh"), []byte("printf a > a.txt\nprintf '%s\\n' '"+result+"'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake, err := filepath.Abs("../agentio/testdata/fake_agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	config = filepath.Join(t.TempDir(), "agents.toml")
	body := "[tiers]\ndefault = {agent = \"fake\", model = \"\"}\n[agents.fake]\ncmd = [" +
		strconv.Quote(testshim.Bash(t)) + ", " + strconv.Quote(filepath.ToSlash(fake)) + "]\n" +
		"result = \"json:structured_output\"\ntext = \"json:result\"\nsession = \"json:session_id\"\n"
	if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return project, config, scenario
}

// TestTerminalQuitWithLiveRunStopsAgent leaves the workspace while the fake
// agent is still sleeping, then waits past the sleep: a surviving child would
// write a.txt, while a stopped process group cannot.
func TestTerminalQuitWithLiveRunStopsAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("terminal test builds the CLI")
	}
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "docflow")
	build := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
	build.Dir = root
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	project, config, scenario := writeRunnableProject(t)
	master, slave := openTestPTY(t)
	if err := setTestWinsize(slave, 120, 40); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(bin, "tui")
	child.Dir = project
	child.Env = append(os.Environ(),
		"TERM=xterm-256color", "NO_COLOR=1",
		"DOCFLOW_AGENTS="+config,
		"FAKE_AGENT_SCENARIO="+scenario,
		"DOCFLOW_NOTIFY=0", "DOCFLOW_SKILLS_CHECK=0",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	)
	child.Stdin, child.Stdout, child.Stderr = slave, slave, slave
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var screen bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				mu.Lock()
				screen.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	output := func() string {
		mu.Lock()
		defer mu.Unlock()
		return screen.String()
	}
	waitFor := func(text string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(output(), text) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("terminal output does not contain %q\n%s", text, output())
	}
	sendKey := func(key string) {
		writeAll(t, master, key)
		time.Sleep(100 * time.Millisecond)
	}
	waitFile := func(path string, present bool, timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			_, err := os.Stat(path)
			if present == (err == nil) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("file %s present=%v not reached\n%s", path, present, output())
	}

	waitFor("Queue")
	sendKey("s")
	waitFor("Start the current Queue run?")
	sendKey("\r")
	waitFile(scenario+".n", true, 20*time.Second)
	sendKey("q")
	waitFor("A run is still active")
	sendKey("\r")

	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("tui exited with %v\n%s", err, output())
		}
	case <-time.After(20 * time.Second):
		_ = child.Process.Kill()
		t.Fatalf("tui did not exit with a live run\n%s", output())
	}
	_ = master.Close()
	<-readDone

	if _, err := os.Stat(filepath.Join(project, ".git", "docflow", "tui", "checkpoint.json")); err != nil {
		t.Fatalf("exit with a live run did not write a checkpoint: %v", err)
	}
	time.Sleep(6 * time.Second)
	if _, err := os.Stat(filepath.Join(project, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("a background agent survived the exit and kept working: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".git", "docflow", "run", "run.lock")); !os.IsNotExist(err) {
		t.Fatalf("checkout lock left after exit: %v", err)
	}
}

// TestTerminalSigtermStopsLiveRun sends SIGTERM while the fake agent is still
// sleeping: the workspace must pause at a checkpoint, release the checkout
// lock and leave the terminal usable instead of dying with the agent alive.
func TestTerminalSigtermStopsLiveRun(t *testing.T) {
	testTerminalSignalStopsRun(t, syscall.SIGTERM)
}

// TestTerminalSighupStopsLiveRun covers the signal Bubble Tea does not handle
// itself: a hangup must take the same controlled stop path as SIGTERM.
func TestTerminalSighupStopsLiveRun(t *testing.T) {
	testTerminalSignalStopsRun(t, syscall.SIGHUP)
}

func testTerminalSignalStopsRun(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if testing.Short() {
		t.Skip("terminal test builds the CLI")
	}
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "docflow")
	build := exec.Command("go", "build", "-o", bin, "./cmd/docflow")
	build.Dir = root
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	project, config, scenario := writeRunnableProject(t)
	master, slave := openTestPTY(t)
	if err := setTestWinsize(slave, 120, 40); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(bin, "tui")
	child.Dir = project
	child.Env = append(os.Environ(),
		"TERM=xterm-256color", "NO_COLOR=1",
		"DOCFLOW_AGENTS="+config,
		"FAKE_AGENT_SCENARIO="+scenario,
		"DOCFLOW_NOTIFY=0", "DOCFLOW_SKILLS_CHECK=0",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
	)
	child.Stdin, child.Stdout, child.Stderr = slave, slave, slave
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var screen bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				mu.Lock()
				screen.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	output := func() string {
		mu.Lock()
		defer mu.Unlock()
		return screen.String()
	}
	waitFor := func(text string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(output(), text) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("terminal output does not contain %q\n%s", text, output())
	}
	sendKey := func(key string) {
		writeAll(t, master, key)
		time.Sleep(100 * time.Millisecond)
	}
	waitFile := func(path string, present bool, timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			_, err := os.Stat(path)
			if present == (err == nil) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("file %s present=%v not reached\n%s", path, present, output())
	}

	waitFor("Queue")
	sendKey("s")
	waitFor("Start the current Queue run?")
	sendKey("\r")
	waitFile(scenario+".n", true, 20*time.Second)

	// The agent is in its five-second sleep now. A signal that does not stop
	// the run lets it wake up and write a.txt.
	if err := child.Process.Signal(sig); err != nil {
		t.Fatalf("send %s: %v", sig, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("tui exited with %v after %s\n%s", err, sig, output())
		}
	case <-time.After(30 * time.Second):
		_ = child.Process.Kill()
		t.Fatalf("tui did not exit after %s\n%s", sig, output())
	}

	checkpoint := filepath.Join(project, ".git", "docflow", "tui", "checkpoint.json")
	data, err := os.ReadFile(checkpoint)
	if err != nil {
		t.Fatalf("%s left no checkpoint: %v\n%s", sig, err, output())
	}
	var state struct {
		Stage string `json:"stage"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("checkpoint after %s is not JSON: %v", sig, err)
	}
	if state.Stage == "" {
		t.Fatalf("checkpoint after %s names no stage: %s", sig, data)
	}
	time.Sleep(6 * time.Second)
	if _, err := os.Stat(filepath.Join(project, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("the agent survived %s and kept working: %v", sig, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".git", "docflow", "run", "run.lock")); !os.IsNotExist(err) {
		t.Fatalf("checkout lock left after %s: %v", sig, err)
	}

	out := output()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out = output()
		if strings.Contains(out, "\x1b[?1049l") && strings.Contains(out, "\x1b[?25h") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out, "\x1b[?1049l") {
		t.Fatalf("alternate screen was not left after %s\n%q", sig, out)
	}
	if !strings.Contains(out, "\x1b[?25h") {
		t.Fatalf("cursor was not restored after %s\n%q", sig, out)
	}
	if !testEchoEnabled(t, master) {
		t.Fatalf("terminal echo was not restored after %s", sig)
	}
	_ = master.Close()
	<-readDone
}
