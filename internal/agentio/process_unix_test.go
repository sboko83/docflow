//go:build darwin || linux

package agentio

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

// orphanScript starts a double-forked grandchild in a new session that keeps
// the agent's stdout and stderr open, waits until it is running, then runs
// tail (the rest of the agent script).
func orphanScript(tail string) string {
	return `perl -e 'use POSIX; exit if fork; setsid(); exit if fork; open(F, ">", "orphan.pid") or die $!; print F $$; close F; sleep 120' &
while [ ! -s orphan.pid ]; do sleep 0.01; done
printf '%s\n' '{"type":"done"}'
` + tail
}

func killOrphan(t *testing.T, root string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "orphan.pid"))
	if err != nil {
		return
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 1 {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func TestCallAgentReturnsDespiteOrphanHoldingPipes(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl is required for setsid")
	}
	cases := []struct {
		name    string
		tail    string
		timeout time.Duration
		code    int
	}{
		{name: "agent exits", tail: "exit 0\n", code: 0},
		{name: "timeout kills agent", tail: "sleep 120\n", timeout: 2 * time.Second, code: -int(syscall.SIGTERM)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, argv := fakeScenario(t, orphanScript(tc.tail))
			t.Cleanup(func() { killOrphan(t, root) })
			start := time.Now()
			type outcome struct {
				run AgentRun
				err error
			}
			result := make(chan outcome, 1)
			go func() {
				run, err := CallAgent(root, argv, nil, filepath.Join(root, "log"), CallOptions{Timeout: tc.timeout})
				result <- outcome{run, err}
			}()
			limit := tc.timeout + PipeGrace + 10*time.Second
			select {
			case got := <-result:
				if got.err != nil {
					t.Fatal(got.err)
				}
				run := got.run
				equal(t, run.Code, tc.code)
				equal(t, run.TimedOut, tc.timeout > 0)
				if !strings.Contains(run.Stdout, `"done"`) {
					t.Fatalf("stdout lost: %q", run.Stdout)
				}
				t.Logf("returned after %s", time.Since(start).Round(100*time.Millisecond))
			case <-time.After(limit):
				killOrphan(t, root)
				<-result
				t.Fatalf("CallAgent still waiting after %s: an orphan holding the agent's pipes blocks cmd.Wait", limit)
			}
		})
	}
}
