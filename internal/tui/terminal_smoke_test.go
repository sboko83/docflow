//go:build darwin || linux

package tui

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestTerminalSmoke runs the real CLI in a PTY: it opens the workspace,
// presses Enter and Tab, resizes the terminal, and exits. It checks that the
// child leaves the alternate screen, shows the cursor and restores echo.
func TestTerminalSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("terminal smoke builds the CLI")
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
	if err := setTestWinsize(slave, 100, 30); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(bin, "tui")
	child.Dir = project
	child.Env = append(os.Environ(), "TERM=xterm-256color", "NO_COLOR=")
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
	deadlineFor := func(marker, text string) bool {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			out := output()
			if i := strings.Index(out, marker); i >= 0 && strings.Contains(out[i:], text) {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}
	waitFor := func(text string) {
		t.Helper()
		if !deadlineFor("", text) {
			t.Fatalf("terminal output does not contain %q\n%s", text, output())
		}
	}
	waitForAfter := func(marker, text string) {
		t.Helper()
		if !deadlineFor(marker, text) {
			t.Fatalf("terminal output after %q does not contain %q\n%s", marker, text, output())
		}
	}

	waitFor("Queue")
	writeAll(t, master, "\r")
	waitForAfter("Queue", "▸")
	writeAll(t, master, "\t")
	time.Sleep(100 * time.Millisecond)

	if err := setTestWinsize(master, 20, 6); err != nil {
		t.Fatal(err)
	}
	_ = child.Process.Signal(syscall.SIGWINCH)
	waitFor("Terminal too small")
	if err := setTestWinsize(master, 120, 40); err != nil {
		t.Fatal(err)
	}
	_ = child.Process.Signal(syscall.SIGWINCH)
	waitForAfter("Terminal too small", "Queue")
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
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out := output()
		if strings.Contains(out, "\x1b[?1049l") && strings.Contains(out, "\x1b[?25h") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	out := output()
	if !strings.Contains(out, "\x1b[?1049l") {
		t.Fatalf("alternate screen was not left\n%q", out)
	}
	if !strings.Contains(out, "\x1b[?25h") {
		t.Fatalf("cursor was not restored\n%q", out)
	}
	if !testEchoEnabled(t, master) {
		t.Fatal("terminal echo was not restored")
	}
	_ = master.Close()
	<-readDone
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func writeAll(t *testing.T, file *os.File, text string) {
	t.Helper()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}
