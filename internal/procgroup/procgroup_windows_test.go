//go:build windows

package procgroup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helperEnv = "DOCFLOW_PROCGROUP_HELPER"

// TestMain turns the test binary into the helper process: "parent" starts a
// "child" grandchild, records its PID in the file named by os.Args[1] and waits.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=child")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Args[1], []byte(strconv.Itoa(child.Process.Pid)), 0o644); err != nil {
			os.Exit(2)
		}
		_ = child.Wait()
		os.Exit(0)
	case "child":
		time.Sleep(5 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestKillStopsGrandchildren(t *testing.T) {
	for _, useWatch := range []bool{true, false} {
		t.Run(fmt.Sprint(useWatch), func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			cmd := exec.Command(os.Args[0], pidFile)
			cmd.Env = append(os.Environ(), helperEnv+"=parent")
			var watch *Watch
			var err error
			if useWatch {
				watch, err = Start(cmd)
			} else {
				err = cmd.Start()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Close()
			if useWatch {
				if watch == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal("Start did not assign a job")
				}
				// Descendants exist before the caller starts waiting or killing.
				// Starting first and assigning the job here leaks this child.
				waitPID(t, pidFile)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			pid := waitPID(t, pidFile)
			t.Cleanup(func() { _ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run() })
			if !processExists(pid) {
				t.Fatalf("grandchild %d does not run before Kill", pid)
			}
			_ = Kill(cmd, watch, done, 5*time.Second)
			waitGone(t, pid)
		})
	}
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

func processExists(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
	return err == nil && strings.Contains(string(out), `"`+strconv.Itoa(pid)+`"`)
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %d still runs", pid)
}
