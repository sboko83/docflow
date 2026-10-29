package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAddsListsAndTotalsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lookup := func(key string) (string, bool) { return "", false }

	commands := [][]string{
		{"--file", path, "add", "--category", "Food", "2025-01-02", "350", "coffee"},
		{"--file", path, "add", "--category", "Food", "2025-01-03", "1200", "weekly", "groceries"},
	}
	for _, command := range commands {
		if err := run(command, &stdout, &stderr, lookup); err != nil {
			t.Fatalf("run(%q) returned error: %v", command, err)
		}
	}
	if stdout.String() != "Entry added\nEntry added\n" {
		t.Fatalf("add output = %q", stdout.String())
	}
	stdout.Reset()
	if err := run([]string{"--file", path, "list"}, &stdout, &stderr, lookup); err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	wantList := "DATE\tAMOUNT\tCATEGORY\tDESCRIPTION\n" +
		"2025-01-02\t350\tFood\tcoffee\n" +
		"2025-01-03\t1200\tFood\tweekly groceries\n"
	if stdout.String() != wantList {
		t.Fatalf("list output = %q, want %q", stdout.String(), wantList)
	}
	stdout.Reset()
	if err := run([]string{"--file", path, "balance"}, &stdout, &stderr, lookup); err != nil {
		t.Fatalf("balance returned error: %v", err)
	}
	if stdout.String() != "Balance: 1550\n" {
		t.Fatalf("balance output = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunUsesEnvironmentPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env-ledger.tsv")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lookup := func(key string) (string, bool) {
		if key == "LEDGER_FILE" {
			return path, true
		}
		return "", false
	}
	if err := run([]string{"add", "--category", "Food", "2025-04-05", "25", "sandwich"}, &stdout, &stderr, lookup); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if stdout.String() != "Entry added\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(contents) != "# ledger-v2\n2025-04-05\t25\tFood\tsandwich\n" {
		t.Fatalf("journal = %q", contents)
	}
}

func TestRunFlagOverridesEnvironmentPath(t *testing.T) {
	flagPath := filepath.Join(t.TempDir(), "flag.tsv")
	environmentPath := filepath.Join(t.TempDir(), "environment.tsv")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lookup := func(string) (string, bool) { return environmentPath, true }
	if err := run([]string{"--file", flagPath, "add", "--category", "Other", "2025-01-01", "1", "item"}, &stdout, &stderr, lookup); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if _, err := os.Stat(flagPath); err != nil {
		t.Fatalf("flag path not created: %v", err)
	}
	if _, err := os.Stat(environmentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("environment path state = %v, want absent", err)
	}
}

func TestRunListOfMissingFilePrintsHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.tsv")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lookup := func(string) (string, bool) { return "", false }
	if err := run([]string{"--file", path, "list"}, &stdout, &stderr, lookup); err != nil {
		t.Fatalf("list returned error: %v", err)
	}
	if stdout.String() != "DATE\tAMOUNT\tCATEGORY\tDESCRIPTION\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunBalanceOfMissingFileIsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.tsv")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lookup := func(string) (string, bool) { return "", false }
	if err := run([]string{"--file", path, "balance"}, &stdout, &stderr, lookup); err != nil {
		t.Fatalf("balance returned error: %v", err)
	}
	if stdout.String() != "Balance: 0\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunInvalidCommandsReturnErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	lookup := func(string) (string, bool) { return "", false }
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing command", args: []string{"--file", path}},
		{name: "unknown command", args: []string{"--file", path, "remove"}},
		{name: "short add", args: []string{"--file", path, "add", "2025-01-01", "3"}},
		{name: "bad add date", args: []string{"--file", path, "add", "yesterday", "3", "coffee"}},
		{name: "bad add amount", args: []string{"--file", path, "add", "2025-01-01", "0", "coffee"}},
		{name: "empty description", args: []string{"--file", path, "add", "2025-01-01", "3"}},
		{name: "list arguments", args: []string{"--file", path, "list", "extra"}},
		{name: "balance arguments", args: []string{"--file", path, "balance", "extra"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if err := run(test.args, &stdout, &stderr, lookup); err == nil {
				t.Fatal("run returned no error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunRequiresConfiguredPath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lookup := func(string) (string, bool) { return "", false }
	err := run([]string{"balance"}, &stdout, &stderr, lookup)
	if err == nil || !strings.Contains(err.Error(), "--file or LEDGER_FILE") {
		t.Fatalf("run error = %v, want path error", err)
	}
}

func TestRunFlagParsingFailureReturnsError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lookup := func(string) (string, bool) { return "", false }
	err := run([]string{"--unknown"}, &stdout, &stderr, lookup)
	if err == nil {
		t.Fatal("run accepted unknown flag")
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("flag diagnostic = %q", stderr.String())
	}
}

func TestRunReportsMalformedJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.tsv")
	if err := os.WriteFile(path, []byte("not a journal\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	lookup := func(string) (string, bool) { return "", false }
	if err := run([]string{"--file", path, "list"}, &stdout, &stderr, lookup); err == nil {
		t.Fatal("list accepted malformed journal")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
