package benchoracle

import (
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
	binDir, err := os.MkdirTemp("", "docflow-medium-oracle-")
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

func fixture(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.tsv")
	if text != "" {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func run(t *testing.T, path string, args ...string) (string, string, int) {
	t.Helper()
	commandArgs := append([]string{"--file", path}, args...)
	command := exec.Command(ledger, commandArgs...)
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
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

func legacy() string { return "2025-01-01\t10\told lunch\n2025-01-02\t20\told train\n" }

func TestAddWritesVersionTwoRecord(t *testing.T) {
	path := fixture(t, "")
	_, _, code := run(t, path, "add", "--category", "Food", "2025-01-03", "30", "new", "lunch")
	data, err := os.ReadFile(path)
	if err != nil || code != 0 || string(data) != "# ledger-v2\n2025-01-03\t30\tFood\tnew lunch\n" {
		t.Fatalf("journal=%q err=%v code=%d", data, err, code)
	}
}

func TestAddRequiresCategory(t *testing.T) {
	_, stderr, code := run(t, fixture(t, ""), "add", "2025-01-03", "30", "lunch")
	if code != 1 || stderr == "" {
		t.Fatalf("stderr=%q code=%d", stderr, code)
	}
}

func TestAddRejectsEmptyCategory(t *testing.T) {
	_, stderr, code := run(t, fixture(t, ""), "add", "--category", "", "2025-01-03", "30", "lunch")
	if code != 1 || stderr == "" {
		t.Fatalf("stderr=%q code=%d", stderr, code)
	}
}

func TestAddRejectsCategoryWithTab(t *testing.T) {
	_, stderr, code := run(t, fixture(t, ""), "add", "--category", "Food\tOther", "2025-01-03", "30", "lunch")
	if code != 1 || stderr == "" {
		t.Fatalf("stderr=%q code=%d", stderr, code)
	}
}

func TestLegacyListShowsUncategorized(t *testing.T) {
	stdout, _, code := run(t, fixture(t, legacy()), "list")
	want := "DATE\tAMOUNT\tCATEGORY\tDESCRIPTION\n2025-01-01\t10\tUncategorized\told lunch\n2025-01-02\t20\tUncategorized\told train\n"
	if code != 0 || stdout != want {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestLegacyBalanceIncludesAllRecords(t *testing.T) {
	stdout, _, code := run(t, fixture(t, legacy()), "balance")
	if code != 0 || stdout != "Balance: 30\n" {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestAddMigratesLegacyRecords(t *testing.T) {
	path := fixture(t, legacy())
	_, _, code := run(t, path, "add", "--category", "Food", "2025-01-03", "30", "new lunch")
	data, err := os.ReadFile(path)
	want := "# ledger-v2\n2025-01-01\t10\tUncategorized\told lunch\n2025-01-02\t20\tUncategorized\told train\n2025-01-03\t30\tFood\tnew lunch\n"
	if err != nil || code != 0 || string(data) != want {
		t.Fatalf("journal=%q err=%v code=%d", data, err, code)
	}
}

func TestBudgetSetWritesBesideJournal(t *testing.T) {
	path := fixture(t, "")
	_, _, code := run(t, path, "budget", "set", "Food", "100")
	data, err := os.ReadFile(path + ".budgets")
	if err != nil || code != 0 || string(data) != "Food\t100\n" {
		t.Fatalf("budget=%q err=%v code=%d", data, err, code)
	}
}

func TestBudgetSetReplacesCategory(t *testing.T) {
	path := fixture(t, "")
	_, _, _ = run(t, path, "budget", "set", "Food", "100")
	_, _, code := run(t, path, "budget", "set", "Food", "80")
	data, err := os.ReadFile(path + ".budgets")
	if err != nil || code != 0 || string(data) != "Food\t80\n" {
		t.Fatalf("budget=%q err=%v code=%d", data, err, code)
	}
}

func TestBudgetSetRejectsZeroAmount(t *testing.T) {
	_, stderr, code := run(t, fixture(t, ""), "budget", "set", "Food", "0")
	if code != 1 || stderr == "" {
		t.Fatalf("stderr=%q code=%d", stderr, code)
	}
}

func TestReportSumsMonthlyCategories(t *testing.T) {
	path := fixture(t, "# ledger-v2\n2025-01-01\t10\tFood\tcoffee\n2025-01-12\t20\tFood\tlunch\n2025-01-20\t7\tTravel\tbus\n")
	stdout, _, code := run(t, path, "report", "--month", "2025-01")
	want := "CATEGORY\tSPENT\tBUDGET\tSTATUS\nFood\t30\t-\tOK\nTravel\t7\t-\tOK\n"
	if code != 0 || stdout != want {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestReportRowsAreSorted(t *testing.T) {
	path := fixture(t, "# ledger-v2\n2025-01-01\t1\tTravel\tbus\n2025-01-01\t1\tFood\tmeal\n2025-01-01\t1\tBooks\tbook\n")
	stdout, _, code := run(t, path, "report", "--month", "2025-01")
	want := "CATEGORY\tSPENT\tBUDGET\tSTATUS\nBooks\t1\t-\tOK\nFood\t1\t-\tOK\nTravel\t1\t-\tOK\n"
	if code != 0 || stdout != want {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestReportMarksSpendingOverBudget(t *testing.T) {
	path := fixture(t, "# ledger-v2\n2025-01-01\t101\tFood\tmeal\n")
	_, _, _ = run(t, path, "budget", "set", "Food", "100")
	stdout, _, code := run(t, path, "report", "--month", "2025-01")
	if code != 0 || stdout != "CATEGORY\tSPENT\tBUDGET\tSTATUS\nFood\t101\t100\tOVER\n" {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestReportExactBudgetIsNotOver(t *testing.T) {
	path := fixture(t, "# ledger-v2\n2025-01-01\t100\tFood\tmeal\n")
	_, _, _ = run(t, path, "budget", "set", "Food", "100")
	stdout, _, code := run(t, path, "report", "--month", "2025-01")
	if code != 0 || stdout != "CATEGORY\tSPENT\tBUDGET\tSTATUS\nFood\t100\t100\tOK\n" {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestReportIncludesBudgetWithNoSpending(t *testing.T) {
	path := fixture(t, "")
	_, _, _ = run(t, path, "budget", "set", "Food", "100")
	stdout, _, code := run(t, path, "report", "--month", "2025-01")
	if code != 0 || stdout != "CATEGORY\tSPENT\tBUDGET\tSTATUS\nFood\t0\t100\tOK\n" {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestReportShowsDashWithoutBudget(t *testing.T) {
	path := fixture(t, "# ledger-v2\n2025-01-01\t5\tFood\tmeal\n")
	stdout, _, code := run(t, path, "report", "--month", "2025-01")
	if code != 0 || stdout != "CATEGORY\tSPENT\tBUDGET\tSTATUS\nFood\t5\t-\tOK\n" {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestReportExcludesOtherMonths(t *testing.T) {
	path := fixture(t, "# ledger-v2\n2025-01-01\t5\tFood\tjan\n2025-02-01\t9\tFood\tfeb\n")
	stdout, _, code := run(t, path, "report", "--month", "2025-01")
	if code != 0 || stdout != "CATEGORY\tSPENT\tBUDGET\tSTATUS\nFood\t5\t-\tOK\n" {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestReportIncludesLegacyUncategorizedTotal(t *testing.T) {
	path := fixture(t, legacy())
	stdout, _, code := run(t, path, "report", "--month", "2025-01")
	if code != 0 || !strings.Contains(stdout, "Uncategorized\t30\t-\tOK\n") {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}

func TestReportRejectsInvalidMonth(t *testing.T) {
	_, stderr, code := run(t, fixture(t, ""), "report", "--month", "2025-13")
	if code != 1 || stderr == "" {
		t.Fatalf("stderr=%q code=%d", stderr, code)
	}
}

func TestReportRequiresMonth(t *testing.T) {
	_, stderr, code := run(t, fixture(t, ""), "report")
	if code != 1 || stderr == "" {
		t.Fatalf("stderr=%q code=%d", stderr, code)
	}
}

func TestReportHeaderIsExact(t *testing.T) {
	stdout, _, code := run(t, fixture(t, ""), "report", "--month", "2025-01")
	if code != 0 || !strings.HasPrefix(stdout, "CATEGORY\tSPENT\tBUDGET\tSTATUS\n") {
		t.Fatalf("stdout=%q code=%d", stdout, code)
	}
}
