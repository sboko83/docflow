package benchoracle

import (
	"encoding/csv"
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
	binDir, err := os.MkdirTemp("", "docflow-large-oracle-")
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

func fixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func run(t *testing.T, path string, args ...string) (string, string, int) {
	t.Helper()
	args = append([]string{"--file", path}, args...)
	cmd := exec.Command(ledger, args...)
	var out, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if err == nil {
		return out.String(), stderr.String(), 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return out.String(), stderr.String(), exit.ExitCode()
	}
	t.Fatalf("run ledger: %v", err)
	return "", "", -1
}

func write(t *testing.T, path, name, contents string) string {
	t.Helper()
	file := filepath.Join(filepath.Dir(path), name)
	if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func requireError(t *testing.T, stdout, stderr string, code int) {
	t.Helper()
	if code != 1 || stderr == "" {
		t.Fatalf("stdout=%q stderr=%q code=%d; want diagnostic and code 1", stdout, stderr, code)
	}
}

func TestCurrencySetPersistsSetting(t *testing.T) {
	p := fixture(t, "")
	_, _, c := run(t, p, "currency", "set", "EUR")
	b, e := os.ReadFile(p + ".settings")
	if c != 0 || e != nil || string(b) != "EUR\n" {
		t.Fatalf("setting=%q err=%v code=%d", b, e, c)
	}
}
func TestAddUsesDefaultCurrency(t *testing.T) {
	p := fixture(t, "")
	_, _, _ = run(t, p, "currency", "set", "EUR")
	_, _, c := run(t, p, "add", "2025-01-01", "5", "coffee")
	b, _ := os.ReadFile(p)
	if c != 0 || string(b) != "# ledger-v3\n2025-01-01\t5\tEUR\tcoffee\n" {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestAddExplicitCurrencyOverridesDefault(t *testing.T) {
	p := fixture(t, "")
	_, _, _ = run(t, p, "currency", "set", "EUR")
	_, _, c := run(t, p, "add", "--currency", "JPY", "2025-01-01", "5", "coffee")
	b, _ := os.ReadFile(p)
	if c != 0 || !strings.Contains(string(b), "\tJPY\tcoffee\n") {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestAddDefaultsToUSDWithoutSetting(t *testing.T) {
	p := fixture(t, "")
	_, _, c := run(t, p, "add", "2025-01-01", "5", "coffee")
	b, _ := os.ReadFile(p)
	if c != 0 || !strings.Contains(string(b), "\tUSD\tcoffee\n") {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestCurrencyRejectsLowercaseCode(t *testing.T) {
	p := fixture(t, "")
	o, e, c := run(t, p, "currency", "set", "eur")
	requireError(t, o, e, c)
}
func TestCurrencyRejectsWrongLength(t *testing.T) {
	p := fixture(t, "")
	o, e, c := run(t, p, "currency", "set", "EURO")
	requireError(t, o, e, c)
}
func TestAddRejectsInvalidCurrency(t *testing.T) {
	p := fixture(t, "")
	o, e, c := run(t, p, "add", "--currency", "12D", "2025-01-01", "5", "coffee")
	requireError(t, o, e, c)
}
func TestAddMigratesVersionOneAsUSD(t *testing.T) {
	p := fixture(t, "2025-01-01\t5\tcoffee\n")
	_, _, c := run(t, p, "add", "2025-01-02", "7", "tea")
	b, _ := os.ReadFile(p)
	want := "# ledger-v3\n2025-01-01\t5\tUSD\tcoffee\n2025-01-02\t7\tUSD\ttea\n"
	if c != 0 || string(b) != want {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestAddMigratesVersionTwoAsUSD(t *testing.T) {
	p := fixture(t, "# ledger-v2\n2025-01-01\t5\tcoffee\n")
	_, _, c := run(t, p, "add", "2025-01-02", "7", "tea")
	b, _ := os.ReadFile(p)
	if c != 0 || !strings.Contains(string(b), "2025-01-01\t5\tUSD\tcoffee\n") {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestListReadsVersionThreeCurrencies(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tEUR\tcoffee\n")
	o, _, c := run(t, p, "list")
	if c != 0 || !strings.Contains(o, "DATE\tAMOUNT\tCURRENCY\tDESCRIPTION\n2025-01-01\t5\tEUR\tcoffee\n") {
		t.Fatalf("output=%q code=%d", o, c)
	}
}
func TestBalanceSameCurrency(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tEUR\ta\n2025-01-02\t7\tEUR\tb\n")
	r := write(t, p, "rates.tsv", "")
	o, _, c := run(t, p, "balance", "--in", "EUR", "--rates", r)
	if c != 0 || o != "Balance: 12 EUR\n" {
		t.Fatalf("output=%q code=%d", o, c)
	}
}
func TestBalanceConvertsMixedCurrencies(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tEUR\ta\n2025-01-02\t7\tUSD\tb\n")
	r := write(t, p, "rates.tsv", "USD\tEUR\t2\t1\n")
	o, _, c := run(t, p, "balance", "--in", "EUR", "--rates", r)
	if c != 0 || o != "Balance: 19 EUR\n" {
		t.Fatalf("output=%q code=%d", o, c)
	}
}
func TestBalanceRoundsDown(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tUSD	a\n")
	r := write(t, p, "rates.tsv", "USD\tEUR\t1\t2\n")
	o, _, c := run(t, p, "balance", "--in", "EUR", "--rates", r)
	if c != 0 || o != "Balance: 2 EUR\n" {
		t.Fatalf("output=%q code=%d", o, c)
	}
}
func TestBalanceRequiresDirectedRate(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tUSD\ta\n")
	r := write(t, p, "rates.tsv", "EUR\tUSD\t2\t1\n")
	o, e, c := run(t, p, "balance", "--in", "EUR", "--rates", r)
	requireError(t, o, e, c)
}
func TestBalanceRejectsMalformedRate(t *testing.T) {
	p := fixture(t, "# ledger-v3\n")
	r := write(t, p, "rates.tsv", "USD\tEUR\t0\t1\n")
	o, e, c := run(t, p, "balance", "--in", "EUR", "--rates", r)
	requireError(t, o, e, c)
}
func TestBalanceRejectsInvalidTargetCurrency(t *testing.T) {
	p := fixture(t, "")
	r := write(t, p, "rates.tsv", "")
	o, e, c := run(t, p, "balance", "--in", "EURO", "--rates", r)
	requireError(t, o, e, c)
}
func TestBalanceRequiresRatesOption(t *testing.T) {
	p := fixture(t, "")
	o, e, c := run(t, p, "balance", "--in", "USD")
	requireError(t, o, e, c)
}
func TestImportCSVAddsRows(t *testing.T) {
	p := fixture(t, "")
	f := write(t, p, "in.csv", "date,amount,currency,description\n2025-01-01,5,EUR,coffee\n")
	_, _, c := run(t, p, "import", "csv", f)
	b, _ := os.ReadFile(p)
	if c != 0 || !strings.Contains(string(b), "2025-01-01\t5\tEUR\tcoffee\n") {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestImportJSONAddsRows(t *testing.T) {
	p := fixture(t, "")
	f := write(t, p, "in.json", `[{"date":"2025-01-01","amount":5,"currency":"EUR","description":"coffee"}]`)
	_, _, c := run(t, p, "import", "json", f)
	b, _ := os.ReadFile(p)
	if c != 0 || !strings.Contains(string(b), "2025-01-01\t5\tEUR\tcoffee\n") {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestImportCSVSkipsExactDuplicate(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tEUR\tcoffee\n")
	f := write(t, p, "in.csv", "date,amount,currency,description\n2025-01-01,5,EUR,coffee\n")
	_, _, c := run(t, p, "import", "csv", f)
	b, _ := os.ReadFile(p)
	if c != 0 || strings.Count(string(b), "coffee") != 1 {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestImportKeepsDifferentCurrency(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tEUR\tcoffee\n")
	f := write(t, p, "in.csv", "date,amount,currency,description\n2025-01-01,5,USD,coffee\n")
	_, _, c := run(t, p, "import", "csv", f)
	b, _ := os.ReadFile(p)
	if c != 0 || strings.Count(string(b), "coffee") != 2 {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestImportSkipsDuplicatesWithinInput(t *testing.T) {
	p := fixture(t, "")
	f := write(t, p, "in.csv", "date,amount,currency,description\n2025-01-01,5,EUR,coffee\n2025-01-01,5,EUR,coffee\n")
	_, _, c := run(t, p, "import", "csv", f)
	b, _ := os.ReadFile(p)
	if c != 0 || strings.Count(string(b), "coffee") != 1 {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestImportCSVUsesStandardEscaping(t *testing.T) {
	p := fixture(t, "")
	f := write(t, p, "in.csv", "date,amount,currency,description\n2025-01-01,5,EUR,\"coffee, tea\"\n")
	_, _, c := run(t, p, "import", "csv", f)
	b, _ := os.ReadFile(p)
	if c != 0 || !strings.Contains(string(b), "coffee, tea") {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestImportIsAtomicOnInvalidRow(t *testing.T) {
	initial := "# ledger-v3\n2025-01-01\t5\tEUR\told\n"
	p := fixture(t, initial)
	f := write(t, p, "in.csv", "date,amount,currency,description\n2025-01-02,6,USD,new\ninvalid,0,xx,bad\n")
	o, e, c := run(t, p, "import", "csv", f)
	b, _ := os.ReadFile(p)
	requireError(t, o, e, c)
	if string(b) != initial {
		t.Fatalf("journal mutated: %q", b)
	}
}
func TestImportRejectsUnsupportedFormat(t *testing.T) {
	p := fixture(t, "")
	o, e, c := run(t, p, "import", "xml", "x")
	requireError(t, o, e, c)
}
func TestExportCSVHeaderAndFields(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tEUR\tcoffee\n")
	o, _, c := run(t, p, "export", "csv")
	rows, err := csv.NewReader(strings.NewReader(o)).ReadAll()
	if c != 0 || err != nil || len(rows) != 2 || strings.Join(rows[0], ",") != "date,amount,currency,description" || strings.Join(rows[1], ",") != "2025-01-01,5,EUR,coffee" {
		t.Fatalf("csv=%q rows=%v err=%v code=%d", o, rows, err, c)
	}
}
func TestExportJSONFields(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tEUR\tcoffee\n")
	o, _, c := run(t, p, "export", "json")
	var rows []map[string]any
	err := json.Unmarshal([]byte(o), &rows)
	if c != 0 || err != nil || len(rows) != 1 || len(rows[0]) != 4 || rows[0]["currency"] != "EUR" {
		t.Fatalf("json=%q rows=%v err=%v code=%d", o, rows, err, c)
	}
}
func TestExportCSVQuotesDescription(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t5\tEUR\tcoffee, tea\n")
	o, _, c := run(t, p, "export", "csv")
	rows, err := csv.NewReader(strings.NewReader(o)).ReadAll()
	if c != 0 || err != nil || rows[1][3] != "coffee, tea" {
		t.Fatalf("csv=%q err=%v code=%d", o, err, c)
	}
}
func TestExportJSONEmptyArray(t *testing.T) {
	p := fixture(t, "")
	o, _, c := run(t, p, "export", "json")
	if c != 0 || strings.TrimSpace(o) != "[]" {
		t.Fatalf("output=%q code=%d", o, c)
	}
}
func TestExportRejectsUnsupportedFormat(t *testing.T) {
	p := fixture(t, "")
	o, e, c := run(t, p, "export", "xml")
	requireError(t, o, e, c)
}
func TestRecurAddStoresMonthlyRule(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t1\tUSD\tseed\n")
	_, _, c := run(t, p, "recur", "add", "monthly", "15", "20", "EUR", "rent")
	b, e := os.ReadFile(p + ".recur")
	if c != 0 || e != nil || string(b) != "monthly\t15\t20\tEUR\trent\n" {
		t.Fatalf("rules=%q err=%v code=%d", b, e, c)
	}
}
func TestRecurExpandsThroughRequestedDate(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t1\tUSD\tseed\n")
	_, _, _ = run(t, p, "recur", "add", "monthly", "15", "20", "EUR", "rent")
	o, _, c := run(t, p, "list", "--through", "2025-03-10")
	if c != 0 || strings.Contains(o, "2025-03-15") || !strings.Contains(o, "2025-02-15\t20\tEUR\trent") {
		t.Fatalf("output=%q code=%d", o, c)
	}
}
func TestRecurDoesNotExpandWithoutThrough(t *testing.T) {
	p := fixture(t, "# ledger-v3\n2025-01-01\t1\tUSD\tseed\n")
	_, _, _ = run(t, p, "recur", "add", "monthly", "15", "20", "EUR", "rent")
	o, _, c := run(t, p, "list")
	if c != 0 || strings.Contains(o, "rent") {
		t.Fatalf("output=%q code=%d", o, c)
	}
}
func TestRecurDoesNotMutateJournalWhenRead(t *testing.T) {
	initial := "# ledger-v3\n2025-01-01\t1\tUSD\tseed\n"
	p := fixture(t, initial)
	_, _, _ = run(t, p, "recur", "add", "monthly", "15", "20", "EUR", "rent")
	_, _, c := run(t, p, "list", "--through", "2025-03-10")
	b, _ := os.ReadFile(p)
	if c != 0 || string(b) != initial {
		t.Fatalf("journal=%q code=%d", b, c)
	}
}
func TestRecurRejectsDayAbove28(t *testing.T) {
	p := fixture(t, "")
	o, e, c := run(t, p, "recur", "add", "monthly", "29", "20", "EUR", "rent")
	requireError(t, o, e, c)
}
func TestRecurRejectsInvalidCurrency(t *testing.T) {
	p := fixture(t, "")
	o, e, c := run(t, p, "recur", "add", "monthly", "15", "20", "eur", "rent")
	requireError(t, o, e, c)
}
func TestRecurRejectsInvalidThroughDate(t *testing.T) {
	p := fixture(t, "")
	o, e, c := run(t, p, "list", "--through", "2025-02-30")
	requireError(t, o, e, c)
}
func TestWriteBlockedByExistingLock(t *testing.T) {
	p := fixture(t, "# ledger-v3\n")
	initial, _ := os.ReadFile(p)
	_ = write(t, p, "ledger.tsv.lock", "locked")
	o, e, c := run(t, p, "add", "2025-01-01", "5", "coffee")
	b, _ := os.ReadFile(p)
	requireError(t, o, e, c)
	if string(b) != string(initial) {
		t.Fatalf("journal changed: %q", b)
	}
}
func TestSuccessfulWriteRemovesLockFile(t *testing.T) {
	p := fixture(t, "")
	_, _, c := run(t, p, "add", "2025-01-01", "5", "coffee")
	_, e := os.Stat(p + ".lock")
	if c != 0 || !os.IsNotExist(e) {
		t.Fatalf("lock stat err=%v code=%d", e, c)
	}
}
