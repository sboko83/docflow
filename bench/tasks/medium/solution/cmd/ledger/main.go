package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"benchledger/internal/journal"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv); err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) error {
	flags := flag.NewFlagSet("ledger", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("file", "", "journal file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	args = flags.Args()
	if *path == "" {
		*path, _ = lookupEnv("LEDGER_FILE")
	}
	if *path == "" {
		return errors.New("journal path required: use --file or LEDGER_FILE")
	}
	if len(args) == 0 {
		return errors.New("command required: add, list, balance, budget, or report")
	}
	store := journal.Store{Path: *path}
	switch args[0] {
	case "add":
		return addCommand(store, args[1:], stdout, stderr)
	case "list":
		return listCommand(store, args[1:], stdout, stderr)
	case "balance":
		return balanceCommand(store, args[1:], stdout, stderr)
	case "budget":
		return budgetCommand(store, args[1:])
	case "report":
		return reportCommand(store, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q: use add, list, balance, budget, or report", args[0])
	}
}

func addCommand(store journal.Store, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ledger add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	category := flags.String("category", "", "expense category")
	if err := flags.Parse(args); err != nil {
		return err
	}
	args = flags.Args()
	if len(args) < 3 {
		return errors.New("usage: ledger add --category NAME <YYYY-MM-DD> <amount> <description...>")
	}
	if err := journal.ValidateCategory(*category); err != nil {
		return err
	}
	entry, err := journal.NewEntry(args[0], args[1], args[2:], *category)
	if err != nil {
		return err
	}
	if err := store.Append(entry); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Entry added")
	return err
}

func budgetPath(store journal.Store) string { return store.Path + ".budgets" }

func readBudgets(store journal.Store) (map[string]int64, error) {
	file, err := os.Open(budgetPath(store))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open budgets: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read budgets: %w", err)
	}
	budgets := map[string]int64{}
	for lineNo, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			return nil, fmt.Errorf("budget line %d: expected category and amount", lineNo+1)
		}
		if err := journal.ValidateCategory(fields[0]); err != nil {
			return nil, fmt.Errorf("budget line %d: %w", lineNo+1, err)
		}
		amount, err := journal.ParseAmount(fields[1])
		if err != nil {
			return nil, fmt.Errorf("budget line %d: %w", lineNo+1, err)
		}
		budgets[fields[0]] = amount
	}
	return budgets, nil
}

func budgetCommand(store journal.Store, args []string) error {
	if len(args) != 3 || args[0] != "set" {
		return errors.New("usage: ledger budget set <category> <amount>")
	}
	if err := journal.ValidateCategory(args[1]); err != nil {
		return err
	}
	amount, err := journal.ParseAmount(args[2])
	if err != nil {
		return err
	}
	budgets, err := readBudgets(store)
	if err != nil {
		return err
	}
	budgets[args[1]] = amount
	categories := make([]string, 0, len(budgets))
	for category := range budgets {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	var b strings.Builder
	for _, category := range categories {
		fmt.Fprintf(&b, "%s\t%d\n", category, budgets[category])
	}
	if err := os.MkdirAll(filepath.Dir(budgetPath(store)), 0o755); err != nil {
		return fmt.Errorf("create budget directory: %w", err)
	}
	if err := os.WriteFile(budgetPath(store), []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write budgets: %w", err)
	}
	return nil
}

func reportCommand(store journal.Store, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ledger report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	month := flags.String("month", "", "report month (YYYY-MM)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *month == "" {
		return errors.New("usage: ledger report --month YYYY-MM")
	}
	parsed, err := time.Parse("2006-01", *month)
	if err != nil || parsed.Format("2006-01") != *month {
		return errors.New("invalid month: expected YYYY-MM")
	}
	entries, err := store.ReadAll()
	if err != nil {
		return err
	}
	budgets, err := readBudgets(store)
	if err != nil {
		return err
	}
	totals := map[string]int64{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Date, *month) {
			if totals[entry.Category] > int64(^uint64(0)>>1)-entry.Amount {
				return errors.New("category total exceeds the supported integer range")
			}
			totals[entry.Category] += entry.Amount
		}
	}
	categories := map[string]bool{}
	for category := range totals {
		categories[category] = true
	}
	for category := range budgets {
		categories[category] = true
	}
	ordered := make([]string, 0, len(categories))
	for category := range categories {
		ordered = append(ordered, category)
	}
	sort.Strings(ordered)
	if _, err := fmt.Fprintln(stdout, "CATEGORY\tSPENT\tBUDGET\tSTATUS"); err != nil {
		return err
	}
	for _, category := range ordered {
		budget, exists := budgets[category]
		budgetText, status := "-", "OK"
		if exists {
			budgetText = strconv.FormatInt(budget, 10)
			if totals[category] > budget {
				status = "OVER"
			}
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%d\t%s\t%s\n", category, totals[category], budgetText, status); err != nil {
			return err
		}
	}
	return nil
}

func listCommand(store journal.Store, args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: ledger list")
	}
	entries, err := store.ReadAll()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "DATE\tAMOUNT\tCATEGORY\tDESCRIPTION"); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := fmt.Fprintf(stdout, "%s\t%d\t%s\t%s\n", entry.Date, entry.Amount, entry.Category, entry.Description); err != nil {
			return err
		}
	}
	return nil
}

func balanceCommand(store journal.Store, args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: ledger balance")
	}
	entries, err := store.ReadAll()
	if err != nil {
		return err
	}
	total, err := journal.Total(entries)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Balance: %d\n", total)
	return err
}
