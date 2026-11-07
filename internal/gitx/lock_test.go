package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lockRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "lock"}, {"config", "user.email", "lock@example.invalid"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestGitWaitsForAHeldIndexLock: an editor's git status holds index.lock
// for a moment; git add repeats instead of failing the unit's commit.
func TestGitWaitsForAHeldIndexLock(t *testing.T) {
	root := lockRepo(t)
	lock := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.Remove(lock)
	}()
	if _, err := Git(root, "add", "-A"); err != nil {
		t.Fatalf("git add did not wait for the lock: %v", err)
	}
	staged, err := Git(root, "diff", "--cached", "--name-only")
	if err != nil || strings.TrimSpace(string(staged)) != "a.txt" {
		t.Fatalf("staged = %q, %v", staged, err)
	}
}

// TestGitFailsOnAStaleIndexLock: a lock nobody releases still fails, with
// Git's own message, once the waits are spent.
func TestGitFailsOnAStaleIndexLock(t *testing.T) {
	saved := IndexLockWaits
	IndexLockWaits = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { IndexLockWaits = saved })
	root := lockRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".git", "index.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	RetryIndexLock(func() ([]byte, bool) {
		attempts++
		_, err := exec.Command("git", "-C", root, "add", "-A").Output()
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.Stderr, true
		}
		return nil, err != nil
	})
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if _, err := Git(root, "add", "-A"); err == nil || !strings.Contains(err.Error(), "index.lock") {
		t.Fatalf("stale lock error = %v", err)
	}
}
