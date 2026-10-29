package benchoracle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var ledger string

func TestMain(m *testing.M) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		os.Exit(1)
	}
	root := filepath.Dir(filepath.Dir(source))
	binDir, err := os.MkdirTemp("", "docflow-small-oracle-")
	if err != nil {
		os.Exit(1)
	}
	ledger = filepath.Join(binDir, "ledger")
	if os.PathSeparator == '\\' {
		ledger += ".exe"
	}
	build := exec.Command("go", "build", "-o", ledger, "./cmd/ledger")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		_, _ = os.Stderr.Write(output)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(binDir)
	os.Exit(code)
}

func journal(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	contents := "2025-01-01\t10\tfirst\n" +
		"2025-01-02\t20\tsecond item\n" +
		"2025-01-03\t30\tthird\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, path string, args ...string) (string, string, int) {
	t.Helper()
	commandArgs := append([]string{"--file", path}, args...)
	command := exec.Command(ledger, commandArgs...)
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return stdout.String(), stderr.String(), exit.ExitCode()
	}
	t.Fatalf("run ledger: %v", err)
	return "", "", -1
}

func TestListSinceIncludesBoundary(t *testing.T) {
	stdout, _, code := run(t, journal(t), "list", "--since", "2025-01-02")
	want := "DATE\tAMOUNT\tDESCRIPTION\n2025-01-02\t20\tsecond item\n2025-01-03\t30\tthird\n"
	if code != 0 || stdout != want {
		t.Fatalf("list output = %q (code %d), want %q", stdout, code, want)
	}
}

func TestListUntilIncludesBoundary(t *testing.T) {
	stdout, _, code := run(t, journal(t), "list", "--until", "2025-01-02")
	want := "DATE\tAMOUNT\tDESCRIPTION\n2025-01-01\t10\tfirst\n2025-01-02\t20\tsecond item\n"
	if code != 0 || stdout != want {
		t.Fatalf("list output = %q (code %d), want %q", stdout, code, want)
	}
}

func TestListDateRangeIncludesBothBoundaries(t *testing.T) {
	stdout, _, code := run(t, journal(t), "list", "--since", "2025-01-02", "--until", "2025-01-03")
	want := "DATE\tAMOUNT\tDESCRIPTION\n2025-01-02\t20\tsecond item\n2025-01-03\t30\tthird\n"
	if code != 0 || stdout != want {
		t.Fatalf("list output = %q (code %d), want %q", stdout, code, want)
	}
}

func TestBalanceSinceIncludesBoundary(t *testing.T) {
	stdout, _, code := run(t, journal(t), "balance", "--since", "2025-01-02")
	if code != 0 || stdout != "Balance: 50\n" {
		t.Fatalf("balance output = %q (code %d)", stdout, code)
	}
}

func TestBalanceUntilIncludesBoundary(t *testing.T) {
	stdout, _, code := run(t, journal(t), "balance", "--until", "2025-01-02")
	if code != 0 || stdout != "Balance: 30\n" {
		t.Fatalf("balance output = %q (code %d)", stdout, code)
	}
}

func TestBalanceDateRangeIncludesBothBoundaries(t *testing.T) {
	stdout, _, code := run(t, journal(t), "balance", "--since", "2025-01-02", "--until", "2025-01-03")
	if code != 0 || stdout != "Balance: 50\n" {
		t.Fatalf("balance output = %q (code %d)", stdout, code)
	}
}

func TestListJSONHasDocumentedFieldsAndValues(t *testing.T) {
	stdout, _, code := run(t, journal(t), "list", "--json")
	if code != 0 {
		t.Fatalf("list exited with %d", code)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout, err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	var date, description string
	var amount int
	if err := json.Unmarshal(entries[1]["date"], &date); err != nil || date != "2025-01-02" {
		t.Fatalf("date = %q, error = %v", date, err)
	}
	if err := json.Unmarshal(entries[1]["amount"], &amount); err != nil || amount != 20 {
		t.Fatalf("amount = %d, error = %v", amount, err)
	}
	if err := json.Unmarshal(entries[1]["description"], &description); err != nil || description != "second item" {
		t.Fatalf("description = %q, error = %v", description, err)
	}
	if len(entries[1]) != 3 {
		t.Fatalf("JSON object fields = %v, want exactly date, amount, description", entries[1])
	}
}

func TestListJSONDateFilterAndEmptyArray(t *testing.T) {
	stdout, _, code := run(t, journal(t), "list", "--since", "2025-01-02", "--until", "2025-01-02", "--json")
	if code != 0 {
		t.Fatalf("filtered JSON exited with %d: %q", code, stdout)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout, err)
	}
	if len(entries) != 1 || len(entries[0]) != 3 {
		t.Fatalf("entries = %q, want one object with exactly date, amount, description", stdout)
	}
	var date, description string
	var amount int
	if err := json.Unmarshal(entries[0]["date"], &date); err != nil || date != "2025-01-02" {
		t.Fatalf("date = %q, error = %v", date, err)
	}
	if err := json.Unmarshal(entries[0]["amount"], &amount); err != nil || amount != 20 {
		t.Fatalf("amount = %d, error = %v", amount, err)
	}
	if err := json.Unmarshal(entries[0]["description"], &description); err != nil || description != "second item" {
		t.Fatalf("description = %q, error = %v", description, err)
	}
	stdout, _, code = run(t, journal(t), "list", "--since", "2025-02-01", "--json")
	if code != 0 || strings.TrimSpace(stdout) != "[]" {
		t.Fatalf("empty JSON = %q (code %d), want []", stdout, code)
	}
}

func TestInvalidSinceDateExitsOne(t *testing.T) {
	stdout, stderr, code := run(t, journal(t), "list", "--since", "2025-02-29")
	if code != 1 || stdout != "" || stderr == "" {
		t.Fatalf("stdout=%q stderr=%q code=%d, want empty stdout, diagnostic and code 1", stdout, stderr, code)
	}
}

func TestInvalidUntilDateExitsOne(t *testing.T) {
	stdout, stderr, code := run(t, journal(t), "balance", "--until", "yesterday")
	if code != 1 || stdout != "" || stderr == "" {
		t.Fatalf("stdout=%q stderr=%q code=%d, want empty stdout, diagnostic and code 1", stdout, stderr, code)
	}
}

func TestBalanceRejectsJSONFlag(t *testing.T) {
	stdout, stderr, code := run(t, journal(t), "balance", "--json")
	if code != 1 || stdout != "" || stderr == "" {
		t.Fatalf("stdout=%q stderr=%q code=%d, want unsupported flag diagnostic and code 1", stdout, stderr, code)
	}
}
