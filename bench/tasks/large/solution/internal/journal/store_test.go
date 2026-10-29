package journal

import (
	"os"
	"strings"
	"testing"
)

func TestReadLegacyAndVersionedJournal(t *testing.T) {
	cases := []struct{ input, currency string }{{"2025-01-01\t10\tcoffee\n", "USD"}, {"# ledger-v2\n2025-01-01\t10\tUSD\tcoffee\n", "USD"}, {"# ledger-v3\n2025-01-01\t10\tEUR\tcoffee\n", "EUR"}}
	for _, tc := range cases {
		entries, err := Read(strings.NewReader(tc.input))
		if err != nil || len(entries) != 1 || entries[0].Currency != tc.currency {
			t.Errorf("Read(%q) = %#v, %v", tc.input, entries, err)
		}
	}
}
func TestAppendMigratesAndWritesV3(t *testing.T) {
	path := t.TempDir() + "/ledger"
	if err := os.WriteFile(path, []byte("2025-01-01\t10\tcoffee\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := Store{Path: path}
	if err := s.Append(Entry{Date: "2025-01-02", Amount: 20, Currency: "EUR", Description: "tea"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "# ledger-v3\n2025-01-01\t10\tUSD\tcoffee\n2025-01-02\t20\tEUR\ttea\n"
	if string(data) != want {
		t.Fatalf("journal = %q, want %q", data, want)
	}
}
func TestSettingsAndImports(t *testing.T) {
	s := Store{Path: t.TempDir() + "/ledger"}
	currency, err := s.DefaultCurrency()
	if err != nil || currency != "USD" {
		t.Fatalf("DefaultCurrency = %q, %v", currency, err)
	}
	if err = s.SetCurrency("CAD"); err != nil {
		t.Fatal(err)
	}
	currency, err = s.DefaultCurrency()
	if err != nil || currency != "CAD" {
		t.Fatalf("DefaultCurrency = %q, %v", currency, err)
	}
	entries, err := ReadImport("csv", strings.NewReader("date,amount,currency,description\n2025-01-01,2,USD,coffee\n"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadImport = %#v, %v", entries, err)
	}
	if err = s.AddImported(entries); err != nil {
		t.Fatal(err)
	}
	if err = s.AddImported(entries); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadAll()
	if err != nil || len(got) != 1 {
		t.Fatalf("ReadAll = %#v, %v", got, err)
	}
}
func TestRatesConvertWithFloorAndRejectMissing(t *testing.T) {
	rates, err := ReadRates(strings.NewReader("EUR\tUSD\t3\t2\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ConvertTotal([]Entry{{Date: "2025-01-01", Amount: 3, Currency: "EUR", Description: "x"}}, "USD", rates)
	if err != nil || got != 4 {
		t.Fatalf("ConvertTotal = %d, %v", got, err)
	}
	if _, err = ConvertTotal([]Entry{{Date: "2025-01-01", Amount: 1, Currency: "EUR", Description: "x"}}, "CAD", rates); err == nil {
		t.Fatal("missing conversion accepted")
	}
}
