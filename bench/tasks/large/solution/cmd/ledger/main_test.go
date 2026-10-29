package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func invoke(t *testing.T, path string, args ...string) (string, error) {
	t.Helper()
	var out, stderr bytes.Buffer
	err := run(append([]string{"--file", path}, args...), &out, &stderr, func(string) (string, bool) { return "", false })
	if err != nil {
		return stderr.String(), err
	}
	return out.String(), nil
}
func TestCurrencyImportExportRecurrenceAndLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	if _, err := invoke(t, path, "currency", "set", "EUR"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, path, "add", "2025-01-02", "100", "salary"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, path, "add", "--currency", "USD", "2025-01-03", "20", "coffee"); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(t.TempDir(), "import.csv")
	if err := os.WriteFile(csvPath, []byte("date,amount,currency,description\n2025-01-02,100,EUR,salary\n2025-01-04,30,EUR,book\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, path, "import", "csv", csvPath); err != nil {
		t.Fatal(err)
	}
	rates := filepath.Join(t.TempDir(), "rates.tsv")
	_ = os.WriteFile(rates, []byte("EUR\tUSD\t2\t1\n"), 0600)
	got, err := invoke(t, path, "balance", "--in", "USD", "--rates", rates)
	if err != nil || got != "Balance: 280 USD\n" {
		t.Fatalf("balance = %q, %v", got, err)
	}
	if _, err := invoke(t, path, "recur", "add", "monthly", "5", "10", "EUR", "rent"); err != nil {
		t.Fatal(err)
	}
	got, err = invoke(t, path, "list", "--through", "2025-02-06")
	if err != nil || !strings.Contains(got, "2025-02-05\t10\tEUR\trent") {
		t.Fatalf("list = %q, %v", got, err)
	}
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, err = invoke(t, path, "add", "2025-01-05", "1", "blocked"); err == nil {
		t.Fatal("write succeeded with existing lock")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("locked write changed journal")
	}
	got, err = invoke(t, path, "export", "json")
	if err != nil || !strings.HasPrefix(got, "[{\"date\":\"2025-01-02\"") {
		t.Fatalf("export = %q, %v", got, err)
	}
}

func TestEveryJournalWriteIsBlockedByExistingLock(t *testing.T) {
	for _, name := range []string{"add", "import", "recurrence"} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "ledger.tsv")
			initial := []byte("# ledger-v3\n")
			if err := os.WriteFile(p, initial, 0600); err != nil {
				t.Fatal(err)
			}
			args := writeArgs(t, name, p)
			if err := os.WriteFile(p+".lock", []byte("locked"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := invoke(t, p, args...); err == nil {
				t.Fatal("write succeeded with existing lock")
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(initial, after) {
				t.Fatalf("journal changed: %q", after)
			}
			if _, err := os.Stat(p + ".recur"); !os.IsNotExist(err) {
				t.Fatalf("recurrence file changed: %v", err)
			}
		})
	}
}
func TestEveryJournalWriteRemovesLockFile(t *testing.T) {
	for _, name := range []string{"add", "import", "recurrence"} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "ledger.tsv")
			if _, err := invoke(t, p, writeArgs(t, name, p)...); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(p + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("lock file remains: %v", err)
			}
		})
	}
}
func writeArgs(t *testing.T, name, path string) []string {
	t.Helper()
	switch name {
	case "add":
		return []string{"add", "2025-01-01", "5", "coffee"}
	case "import":
		file := filepath.Join(filepath.Dir(path), "in.csv")
		if err := os.WriteFile(file, []byte("date,amount,currency,description\n2025-01-01,5,USD,coffee\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return []string{"import", "csv", file}
	case "recurrence":
		return []string{"recur", "add", "monthly", "15", "5", "USD", "rent"}
	default:
		t.Fatalf("unknown operation %q", name)
		return nil
	}
}

func TestWriteFailureRemovesLockFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal-dir")
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, p, "add", "2025-01-01", "5", "coffee"); err == nil {
		t.Fatal("add unexpectedly wrote to a directory")
	}
	if _, err := os.Stat(p + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("lock file remains after write failure: %v", err)
	}
}
