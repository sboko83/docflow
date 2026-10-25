package release

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLockedReleaseHelper(t *testing.T) {
	if os.Getenv("DOCFLOW_TEST_LOCKED_RELEASE") != "1" {
		return
	}
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestActivateKeepsLockedOldVersion(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".docflow")
	if err := Activate(home, sourceTree(t, "1.0.0"), "1.0.0"); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(home, "versions", "1.0.0", BinaryName())
	if err := os.Remove(locked); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(locked, binary, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(locked, "-test.run=^TestLockedReleaseHelper$")
	cmd.Env = append(os.Environ(), "DOCFLOW_TEST_LOCKED_RELEASE=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Scan()
		ready <- scanner.Text()
	}()
	select {
	case line := <-ready:
		if line != "ready" {
			t.Fatalf("child did not lock executable: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not start")
	}
	// Windows refuses to delete a running executable; this is the real lock,
	// rather than an injected cleanup error or a read-only file.
	if err := os.Remove(locked); err == nil {
		t.Fatal("running executable was not locked")
	}
	stderr, err := os.CreateTemp(t.TempDir(), "warnings")
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	oldStderr := os.Stderr
	os.Stderr = stderr
	defer func() { os.Stderr = oldStderr }()
	for _, version := range []string{"1.1.0", "1.2.0"} {
		if err := Activate(home, sourceTree(t, version), version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stderr.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var warnings bytes.Buffer
	if _, err := io.Copy(&warnings, stderr); err != nil {
		t.Fatal(err)
	}
	if text := warnings.String(); strings.Count(text, "\n") != 1 || !strings.Contains(text, "warning: keeping release 1.0.0 still in use:") {
		t.Fatalf("cleanup warnings = %q", text)
	}
	assertFile(t, ExecutablePath(home), "1.2.0", 0755)
	if _, err := os.Stat(locked); err != nil {
		t.Fatalf("running version removed: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := Activate(home, sourceTree(t, "1.3.0"), "1.3.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(locked)); !os.IsNotExist(err) {
		t.Fatalf("unlocked old version was not cleaned up: %v", err)
	}
}
