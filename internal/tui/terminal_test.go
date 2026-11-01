//go:build darwin || linux

package tui

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"bsv-docflow/internal/agentio"
	"bsv-docflow/internal/runner"
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
