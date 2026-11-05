package journal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fast = WriteOptions{LockTimeout: 200 * time.Millisecond, LockStale: 30 * time.Second}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func add(t *testing.T, store Store, entry Entry) int64 {
	t.Helper()
	var id int64
	err := store.Update(fast, func(journal *Journal) error {
		var err error
		id, err = journal.Add(entry)
		return err
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return id
}

func TestUpdateCreatesVersion2Journal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "ledger.tsv")
	store := Store{Path: path}
	if id := add(t, store, Entry{Date: "2025-01-02", Amount: 350, Description: "coffee"}); id != 1 {
		t.Fatalf("id = %d", id)
	}
	if got := readFile(t, path); got != "# ledger-v2 next=2\n1\t2025-01-02\t350\tcoffee\n" {
		t.Fatalf("journal = %q", got)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock left behind: %v", err)
	}
}

func TestLegacyJournalReadsWithIDsAndMigratesOnWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	legacy := "2025-01-01\t5\ttea\n2025-01-02\t7\tbus\n"
	writeFile(t, path, legacy)
	store := Store{Path: path}
	journal, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if journal.Version != 1 || journal.Next != 3 || journal.Records[1].ID != 2 {
		t.Fatalf("journal = %#v", journal)
	}
	if got := readFile(t, path); got != legacy {
		t.Fatalf("read changed journal: %q", got)
	}
	add(t, store, Entry{Date: "2025-01-03", Amount: 9, Description: "lunch"})
	want := "# ledger-v2 next=4\n1\t2025-01-01\t5\ttea\n2\t2025-01-02\t7\tbus\n3\t2025-01-03\t9\tlunch\n"
	if got := readFile(t, path); got != want {
		t.Fatalf("journal = %q, want %q", got, want)
	}
}

func TestDeleteKeepsNextAndNeverReusesIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	store := Store{Path: path}
	for _, description := range []string{"a", "b", "c"} {
		add(t, store, Entry{Date: "2025-01-01", Amount: 1, Description: description})
	}
	if err := store.Update(fast, func(journal *Journal) error { return journal.Delete(3) }); err != nil {
		t.Fatal(err)
	}
	if id := add(t, store, Entry{Date: "2025-01-01", Amount: 1, Description: "d"}); id != 4 {
		t.Fatalf("reused id: %d", id)
	}
	before := readFile(t, path)
	if err := store.Update(fast, func(journal *Journal) error { return journal.Delete(3) }); err == nil {
		t.Fatal("deleted a missing ID")
	}
	if got := readFile(t, path); got != before {
		t.Fatalf("failed delete changed journal: %q", got)
	}
}

func TestParseRejectsMalformedJournals(t *testing.T) {
	cases := map[string]string{
		"id not below next": "# ledger-v2 next=2\n2\t2025-01-01\t5\ttea\n",
		"duplicate id":      "# ledger-v2 next=5\n1\t2025-01-01\t5\ttea\n1\t2025-01-01\t5\tbus\n",
		"decreasing id":     "# ledger-v2 next=5\n3\t2025-01-01\t5\ttea\n2\t2025-01-01\t5\tbus\n",
		"unknown header":    "# ledger-v3\n",
		"late header":       "2025-01-01\t5\ttea\n# ledger-v2 next=3\n",
		"bad next":          "# ledger-v2 next=0\n",
		"missing id":        "# ledger-v2 next=2\n2025-01-01\t5\ttea\n",
		"bad legacy record": "not a journal\n",
		"signed id":         "# ledger-v2 next=3\n+1\t2025-01-01\t5\ttea\n",
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(contents)); err == nil {
				t.Fatal("accepted malformed journal")
			}
		})
	}
}

func TestLockTimesOutAndLeavesForeignLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	writeFile(t, path+".lock", "other\n")
	start := time.Now()
	err := Store{Path: path}.Update(fast, func(journal *Journal) error {
		_, err := journal.Add(Entry{Date: "2025-01-01", Amount: 1, Description: "x"})
		return err
	})
	if !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("gave up without waiting")
	}
	if got := readFile(t, path+".lock"); got != "other\n" {
		t.Fatalf("foreign lock changed: %q", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal created: %v", err)
	}
}

func TestStaleLockIsBroken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	writeFile(t, path+".lock", "old\n")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	add(t, Store{Path: path}, Entry{Date: "2025-01-01", Amount: 1, Description: "x"})
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock left behind: %v", err)
	}
}

func TestCrashBeforeRenameLeavesJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	legacy := "2025-01-01\t5\ttea\n"
	writeFile(t, path, legacy)
	crash := errors.New("crash")
	options := fast
	options.BeforeRename = func() { panic(crash) }
	func() {
		defer func() {
			if recovered := recover(); recovered != crash {
				t.Fatalf("recovered %v", recovered)
			}
		}()
		_ = Store{Path: path}.Update(options, func(journal *Journal) error {
			_, err := journal.Add(Entry{Date: "2025-01-02", Amount: 1, Description: "x"})
			return err
		})
	}()
	if got := readFile(t, path); got != legacy {
		t.Fatalf("journal changed by crashed write: %q", got)
	}
}
