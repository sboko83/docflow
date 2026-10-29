package main

import (
	"bytes"
	"strings"
	"testing"

	"benchledger/internal/journal"
)

func TestParseFiltersValidatesDates(t *testing.T) {
	var stderr bytes.Buffer
	if _, _, err := parseFilters("list", []string{"--since", "2025-02-29"}, &stderr, true); err == nil {
		t.Fatal("accepted invalid date")
	}
}

func TestListCommandFiltersAndEmitsJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	store := journal.Store{Path: t.TempDir() + "/ledger.tsv"}
	for _, entry := range []journal.Entry{
		{Date: "2025-01-01", Amount: 10, Description: "first"},
		{Date: "2025-01-02", Amount: 20, Description: "second"},
	} {
		if err := store.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := listCommand(store, []string{"--since", "2025-01-02", "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"date":"2025-01-02"`) || strings.Contains(stdout.String(), `"date":"2025-01-01"`) {
		t.Fatalf("JSON output = %s", stdout.String())
	}
}

func TestBalanceCommandFiltersInclusiveRange(t *testing.T) {
	var stdout, stderr bytes.Buffer
	store := journal.Store{Path: t.TempDir() + "/ledger.tsv"}
	for _, entry := range []journal.Entry{
		{Date: "2025-01-01", Amount: 10, Description: "first"},
		{Date: "2025-01-02", Amount: 20, Description: "second"},
		{Date: "2025-01-03", Amount: 30, Description: "third"},
	} {
		if err := store.Append(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := balanceCommand(store, []string{"--since", "2025-01-02", "--until", "2025-01-03"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "Balance: 50\n" {
		t.Fatalf("balance output = %q", stdout.String())
	}
}

func TestParseFiltersRejectsEmptyDatesAndExtraArguments(t *testing.T) {
	for _, args := range [][]string{{"--since", ""}, {"--until", ""}, {"--since", "2025-01-01", "extra"}} {
		var stderr bytes.Buffer
		if _, _, err := parseFilters("list", args, &stderr, true); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}
