package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointRoundTripAndSecretFreeFields(t *testing.T) {
	f := newRunFixture(t)
	head := f.git("rev-parse", "HEAD")
	plan, err := os.ReadFile(filepath.Join(f.root, planFile))
	if err != nil {
		t.Fatal(err)
	}
	cp := Checkpoint{Plan: "P001", Unit: "P001.1.1", Head: head, PlanHash: PlanFingerprint(plan), Stage: "agent", Agent: "codex", Model: "luna", Effort: "low", Session: "session-1", Calls: 2, Tokens: 123, Logs: []string{".git/docflow/control/run.log"}}
	if err := WriteCheckpoint(f.root, cp); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCheckpoint(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != checkpointVersion || got.Plan != cp.Plan || got.Unit != cp.Unit || got.Head != head || got.PlanHash != cp.PlanHash || got.Session != cp.Session || got.Tokens != 123 {
		t.Fatalf("checkpoint round trip: %#v", got)
	}
	// The persisted schema has no secret-bearing field and only includes the
	// supplied, non-secret routing and recovery metadata.
	path, _ := checkpointPath(f.root)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if contains := string(data); contains == "" || !strings.HasSuffix(filepath.ToSlash(path), "/.git/docflow/control/checkpoint.json") {
		t.Fatalf("checkpoint path/data: %s %s", path, data)
	}
}

func TestCheckpointRejectsUnknownVersionAndMissingRequiredFields(t *testing.T) {
	f := newRunFixture(t)
	path, err := checkpointPath(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":99,"plan":"P001","head":"h","stage":"agent"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCheckpoint(f.root); err == nil {
		t.Fatal("unknown checkpoint version accepted")
	}
	if err := WriteCheckpoint(f.root, Checkpoint{Plan: "P001"}); err == nil {
		t.Fatal("incomplete checkpoint accepted")
	}
}

// The checkpoint's log links are checkout-relative. macOS puts a symlink in
// front of a temporary home and git resolves it, so the runner holds two
// spellings of one root; a plain Rel of the two is a ladder of "..".
func TestCheckpointLogsStayUnderTheCheckout(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".git/docflow/run")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(logs, "P001.1.1.1.out")
	if err := os.WriteFile(log, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{root, link, filepath.Clean(root) + string(filepath.Separator)} {
		got := checkpointLogs(base, []string{log})
		if len(got) != 1 || got[0] != ".git/docflow/run/P001.1.1.1.out" {
			t.Fatalf("checkpointLogs(%q) = %q, want the checkout-relative log", base, got)
		}
	}
	if got := checkpointLogs(root, []string{filepath.Join(filepath.Dir(root), "outside.out")}); len(got) != 1 || !filepath.IsAbs(got[0]) {
		t.Fatalf("a log outside the checkout must keep its path: %q", got)
	}
	if got := checkpointLogs(root, nil); got != nil {
		t.Fatalf("checkpointLogs(root, nil) = %q", got)
	}
}
