package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRosterMigrateCommand drives `roster migrate` through the CLI on an
// isolated XDG_CONFIG_HOME and the v1 draft fixture: the dry run prints the v2 text, --write keeps
// the v1 roster as roster.toml.bak, and a second --write is refused with
// exit code 2 instead of rewriting the migrated file.
func TestRosterMigrateCommand(t *testing.T) {
	rosterEnv(t, draftV1Roster(t))
	config := os.Getenv("XDG_CONFIG_HOME")
	path := filepath.Join(config, "docflow", "roster.toml")

	code, stdout, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "migrate")
	if code != 0 {
		t.Fatalf("roster migrate = (%d, %q, %q), want 0", code, stdout, stderr)
	}
	if !strings.HasPrefix(stdout, "# Roster v2") || !strings.Contains(stdout, "version = 2") {
		t.Fatalf("roster migrate stdout =\n%s\nwant the v2 text", stdout)
	}
	if data, _ := os.ReadFile(path); string(data) != draftV1Roster(t) {
		t.Fatalf("a dry run rewrote the roster")
	}

	code, stdout, stderr = callLifecycleCLI(t, t.TempDir(), "roster", "migrate", "--write")
	if code != 0 {
		t.Fatalf("roster migrate --write = (%d, %q, %q), want 0", code, stdout, stderr)
	}
	if !strings.Contains(stdout, path) || !strings.Contains(stdout, path+".bak") {
		t.Fatalf("roster migrate --write stdout =\n%s\nwant the roster and backup paths", stdout)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != draftV1Roster(t) {
		t.Fatalf("the backup does not hold the v1 roster")
	}
	migrated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(migrated), "# Roster v2") {
		t.Fatalf("the roster was not migrated:\n%s", migrated)
	}

	code, _, stderr = callLifecycleCLI(t, t.TempDir(), "roster", "migrate", "--write")
	if code != 2 || !strings.Contains(stderr, "already version 2") {
		t.Fatalf("second roster migrate --write = (%d, %q), want 2 and the refusal", code, stderr)
	}
	if again, _ := os.ReadFile(path); string(again) != string(migrated) {
		t.Fatalf("the refused migration rewrote the roster")
	}
}

// TestRosterMigrateArgumentError: migrate takes no positional arguments.
func TestRosterMigrateArgumentError(t *testing.T) {
	rosterEnv(t, draftV1Roster(t))
	code, _, stderr := callLifecycleCLI(t, t.TempDir(), "roster", "migrate", "extra")
	if code != 2 || !strings.Contains(stderr, "unrecognized arguments: extra") {
		t.Fatalf("roster migrate extra = (%d, %q), want 2 and the stray argument", code, stderr)
	}
}
