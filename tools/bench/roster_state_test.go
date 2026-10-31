package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRosterMetadataWithLocalState(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"roster.toml", "catalog.json"} {
		data, err := os.ReadFile(filepath.Join(root, "bench", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "roster.toml")
	before, err := rosterMetadata(path)
	if err != nil || !strings.Contains(strings.Join(before.chains, "\n"), "zai/") {
		t.Fatalf("baseline metadata = %+v, %v", before, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roster.state.toml"), []byte("version = 2\noff = [\"zai\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := rosterMetadata(path)
	if err != nil || strings.Contains(strings.Join(after.chains, "\n"), "zai/") || len(after.chains) == 0 {
		t.Fatalf("conditioned metadata = %+v, %v", after, err)
	}
	if after.sha != before.sha {
		t.Fatal("state changed the policy fingerprint")
	}
}
