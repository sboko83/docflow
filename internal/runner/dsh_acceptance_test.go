package runner

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dshReady returns the DSH_HOME of a dsh that is on PATH and answers a task
// through its login, or skips: CI has neither dsh nor a DeepSeek account.
// The fixture moves HOME, so the real DSH_HOME is resolved before that.
func dshReady(t *testing.T) string {
	t.Helper()
	dsh, err := exec.LookPath("dsh")
	if err != nil {
		t.Skip("dsh is not on PATH")
	}
	home := os.Getenv("DSH_HOME")
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory for DSH_HOME")
		}
		home = filepath.Join(user, ".dsh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	probe := exec.CommandContext(ctx, dsh, "headless", "--json", "-")
	probe.Dir = t.TempDir()
	probe.Env = append(os.Environ(), "DSH_HOME="+home)
	probe.Stdin = strings.NewReader("Reply with the single word: ok")
	var out bytes.Buffer
	probe.Stdout = &out
	if err := probe.Run(); err != nil || !strings.Contains(out.String(), `"kind":"completed"`) {
		t.Skipf("dsh login check failed: %v %s", err, strings.TrimSpace(out.String()))
	}
	return home
}

// Acceptance of P060: one unit of a plan routed to dsh runs through the real
// dsh CLI, the built-in profile and the dsh decoder up to the unit commit.
func TestDshRealUnitCommits(t *testing.T) {
	skipSlow(t)
	home := dshReady(t)
	f := newRunFixture(t)
	t.Setenv("DSH_HOME", home)
	f.replace(planFile, "status: active", "status: active\nexecutor: dsh | deepseek-flash | -")
	r := f.cli("run", "--no-wait", "--max-units", "1", "--yes")
	f.check(r, 0, "P001.1.1 done:")
	if subject := f.git("log", "-1", "--format=%s"); !strings.HasPrefix(subject, "P001.1.1:") {
		t.Fatalf("last commit = %q, want the unit commit\n%s%s", subject, r.out, r.err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "a.txt")); err != nil {
		t.Fatalf("the unit's file is missing: %v\n%s%s", err, r.out, r.err)
	}
	if !strings.Contains(f.read(planFile), "[x] P001.1.1") {
		t.Fatalf("the unit is not done in the plan\n%s%s", r.out, r.err)
	}
}
