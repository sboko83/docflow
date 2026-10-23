//go:build darwin || linux

package cycle

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestVerifyCommandRunsDetached(t *testing.T) {
	out, code := runVerifyCommand(t.TempDir(), `test "$(ps -o pgid= -p $$ | tr -d ' ')" = "$$" && echo own-group`)
	if code != 0 || strings.TrimSpace(out) != "own-group" {
		t.Fatalf("runVerifyCommand() = %q, %d; want own-group, 0", out, code)
	}
}

// TestVerifyCommandStopSignalKillsGroup runs runVerifyCommand in a child test
// process, interrupts it and checks that the command's own children are gone.
func TestVerifyCommandStopSignalKillsGroup(t *testing.T) {
	if pidFile := os.Getenv("DOCFLOW_VERIFY_SIGNAL_HELPER"); pidFile != "" {
		runVerifyCommand(filepath.Dir(pidFile), `sleep 30 & echo $! > "`+pidFile+`"; wait`)
		os.Exit(0)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestVerifyCommandStopSignalKillsGroup$")
	cmd.Env = append(os.Environ(), "DOCFLOW_VERIFY_SIGNAL_HELPER="+pidFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := 0
	for deadline := time.Now().Add(10 * time.Second); pid == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
	}
	if pid == 0 {
		_ = cmd.Process.Kill()
		t.Fatal("helper did not start the verify command")
	}
	_ = cmd.Process.Signal(os.Interrupt)
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("helper exit = %v; want death by SIGINT", err)
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
		t.Fatalf("helper exit = %v; want death by SIGINT", err)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("verify command child %d survived the stop signal", pid)
}
