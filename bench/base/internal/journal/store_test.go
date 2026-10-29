package journal

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStoreAppendCreatesParentAndPreservesRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "personal", "ledger.tsv")
	store := Store{Path: path}
	entries := []Entry{
		{Date: "2025-01-02", Amount: 350, Description: "coffee"},
		{Date: "2025-01-03", Amount: 1200, Description: "weekly groceries"},
		{Date: "2025-01-04", Amount: 75, Description: "bus"},
	}
	for _, entry := range entries {
		if err := store.Append(entry); err != nil {
			t.Fatalf("Append(%#v) returned error: %v", entry, err)
		}
	}
	got, err := store.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if !reflect.DeepEqual(got, entries) {
		t.Fatalf("ReadAll = %#v, want %#v", got, entries)
	}
	wantContents := "2025-01-02\t350\tcoffee\n" +
		"2025-01-03\t1200\tweekly groceries\n" +
		"2025-01-04\t75\tbus\n"
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(contents) != wantContents {
		t.Fatalf("journal contents = %q, want %q", contents, wantContents)
	}
}

func TestReadAllMissingJournalIsEmpty(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "missing.tsv")}
	entries, err := store.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll returned error for absent journal: %v", err)
	}
	if entries == nil || len(entries) != 0 {
		t.Fatalf("ReadAll = %#v, want empty list", entries)
	}
}

func TestStoreAppendRejectsInvalidEntriesWithoutChangingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	store := Store{Path: path}
	valid := Entry{Date: "2025-01-01", Amount: 5, Description: "tea"}
	if err := store.Append(valid); err != nil {
		t.Fatalf("initial Append returned error: %v", err)
	}
	invalid := Entry{Date: "bad", Amount: 5, Description: "coffee"}
	if err := store.Append(invalid); err == nil {
		t.Fatal("Append accepted invalid entry")
	}
	entries, err := store.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if !reflect.DeepEqual(entries, []Entry{valid}) {
		t.Fatalf("invalid append changed journal: %#v", entries)
	}
}

func TestStoreRejectsEmptyPath(t *testing.T) {
	store := Store{}
	if err := store.Append(Entry{Date: "2025-01-01", Amount: 1, Description: "x"}); err == nil {
		t.Fatal("Append accepted empty path")
	}
	if _, err := store.ReadAll(); err == nil {
		t.Fatal("ReadAll accepted empty path")
	}
}

func TestReadParsesMultipleRecordsAndFinalLineWithoutNewline(t *testing.T) {
	input := "2025-01-01\t125\tcoffee\n2025-01-02\t300\tbook"
	entries, err := Read(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	want := []Entry{
		{Date: "2025-01-01", Amount: 125, Description: "coffee"},
		{Date: "2025-01-02", Amount: 300, Description: "book"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("Read = %#v, want %#v", entries, want)
	}
}

func TestReadRejectsTabsInsideDescription(t *testing.T) {
	if _, err := Read(strings.NewReader("2025-01-01\t5\tfirst\tsecond\n")); err == nil {
		t.Fatal("Read accepted tab in description")
	}
}

func TestReadReturnsLineNumberForMalformedRecord(t *testing.T) {
	input := "2025-01-01\t5\tvalid\nnot a record\n"
	_, err := Read(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "journal line 2:") {
		t.Fatalf("Read error = %v, want line number 2", err)
	}
}

func TestReadRejectsInvalidJournalCases(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "missing date", line: "\t5\tcoffee"},
		{name: "missing amount", line: "2025-01-01\t\tcoffee"},
		{name: "missing description", line: "2025-01-01\t5\t"},
		{name: "too few fields", line: "2025-01-01\t5"},
		{name: "too many fields", line: "2025-01-01\t5\tcoffee\textra"},
		{name: "bad date", line: "2025-02-29\t5\tcoffee"},
		{name: "bad amount", line: "2025-01-01\tfive\tcoffee"},
		{name: "zero amount", line: "2025-01-01\t0\tcoffee"},
		{name: "negative amount", line: "2025-01-01\t-5\tcoffee"},
		{name: "overflow amount", line: "2025-01-01\t9223372036854775808\tcoffee"},
		{name: "empty line", line: "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, err := Read(strings.NewReader(test.line)); err == nil {
				t.Fatalf("Read(%q) = %#v, want error", test.line, got)
			}
		})
	}
}

func TestReadAllowsEmptyInput(t *testing.T) {
	entries, err := Read(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if entries == nil || len(entries) != 0 {
		t.Fatalf("Read = %#v, want empty slice", entries)
	}
}
