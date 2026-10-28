//go:build darwin || linux

package procgroup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDetachStartsNewSession(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", `ps -o pgid= -p $$; echo $$`)
	Detach(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] != fields[1] {
		t.Fatalf("pgid, pid = %q; want the child to lead its own group", fields)
	}
}

func TestKillStopsGrandchildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	cmd := exec.Command("/bin/sh", "-c", `sleep 30 & echo $! > "$1"; wait`, "sh", pidFile)
	Detach(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	pid := waitPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	_ = Kill(cmd, nil, done, 5*time.Second)
	waitGone(t, pid)
}

func TestKillStopsDetachedDescendants(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "agent.sh")
	data := `#!/bin/sh
(
	perl -e 'use POSIX; setsid(); open(F, ">", $ARGV[0]) or die $!; print F $$; close F; sleep 300' "$1" &
	wait
) &
wait
`
	if err := os.WriteFile(script, []byte(data), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script, pidFile)
	Detach(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	pid := waitPID(t, pidFile)
	_ = Kill(cmd, nil, done, 5*time.Second)
	waitGone(t, pid)
}

func waitPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", path)
	return 0
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still runs", pid)
}
