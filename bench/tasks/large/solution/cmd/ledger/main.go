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
		return errors.New("command required: add, list, balance, currency, import, export, or recur")
	}
	store := journal.Store{Path: *path}
	switch args[0] {
	case "add":
		return addCommand(store, args[1:], stdout, stderr)
	case "list":
		return listCommand(store, args[1:], stdout, stderr)
	case "balance":
		return balanceCommand(store, args[1:], stdout, stderr)
	case "currency":
		return currencyCommand(store, args[1:])
	case "import":
		return importCommand(store, args[1:])
	case "export":
		return exportCommand(store, args[1:], stdout)
	case "recur":
		return recurCommand(store, args[1:])
	default:
		return fmt.Errorf("unknown command %q: use add, list, balance, currency, import, export, or recur", args[0])
	}
}
func lock(store journal.Store) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(store.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create journal directory: %w", err)
	}
	p := store.Path + ".lock"
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, errors.New("journal is locked")
		}
		return nil, fmt.Errorf("create journal lock: %w", err)
	}
	_ = f.Close()
	return func() { _ = os.Remove(p) }, nil
}
func addCommand(store journal.Store, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ledger add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	currency := flags.String("currency", "", "currency code")
	if err := flags.Parse(args); err != nil {
		return err
	}
	args = flags.Args()
	if len(args) < 3 {
		return errors.New("usage: ledger add [--currency XXX] <YYYY-MM-DD> <amount> <description...>")
	}
	release, err := lock(store)
	if err != nil {
		return err
	}
	defer release()
	if *currency == "" {
		*currency, err = store.DefaultCurrency()
		if err != nil {
			return err
		}
	}
	entry, err := journal.NewEntry(args[0], args[1], args[2:], *currency)
	if err != nil {
		return err
	}
	if err := store.Append(entry); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Entry added")
	return err
}
func currencyCommand(store journal.Store, args []string) error {
	if len(args) != 2 || args[0] != "set" {
		return errors.New("usage: ledger currency set XXX")
	}
	return store.SetCurrency(args[1])
}
func parseThrough(value string) (time.Time, error) {
	s, err := journal.ParseDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse("2006-01-02", s)
}
func withRecurring(store journal.Store, entries []journal.Entry, through string) ([]journal.Entry, error) {
	if through == "" {
		return entries, nil
	}
	cutoff, err := parseThrough(through)
	if err != nil {
		return nil, err
	}
	rules, err := readRecurrences(store)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 || len(rules) == 0 {
		return entries, nil
	}
	first, _ := time.Parse("2006-01-02", entries[0].Date)
	virtual := []journal.Entry{}
	for month := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC); !month.After(time.Date(cutoff.Year(), cutoff.Month(), 1, 0, 0, 0, 0, time.UTC)); month = month.AddDate(0, 1, 0) {
		for _, r := range rules {
			date := time.Date(month.Year(), month.Month(), r.Day, 0, 0, 0, 0, time.UTC)
			if !date.After(cutoff) {
				virtual = append(virtual, journal.Entry{Date: date.Format("2006-01-02"), Amount: r.Amount, Currency: r.Currency, Description: r.Description})
			}
		}
	}
	sort.SliceStable(virtual, func(i, j int) bool { return virtual[i].Date < virtual[j].Date })
	return append(entries, virtual...), nil
}
func readRecurrences(store journal.Store) ([]journal.Recurrence, error) {
	data, err := os.ReadFile(store.Path + ".recur")
	if errors.Is(err, os.ErrNotExist) {
		return []journal.Recurrence{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read recurrence rules: %w", err)
	}
	out := []journal.Recurrence{}
	if len(data) == 0 {
		return out, nil
	}
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		p := strings.SplitN(line, "\t", 5)
		if len(p) != 5 || p[0] != "monthly" {
			return nil, fmt.Errorf("recurrence line %d: malformed rule", i+1)
		}
		day, e1 := strconv.Atoi(p[1])
		amount, e2 := journal.ParseAmount(p[2])
		if e1 != nil || day < 1 || day > 28 || e2 != nil || journal.ValidateCurrency(p[3]) != nil || strings.TrimSpace(p[4]) == "" || strings.ContainsAny(p[4], "\r\n\t") {
			return nil, fmt.Errorf("recurrence line %d: malformed rule", i+1)
		}
		out = append(out, journal.Recurrence{Day: day, Amount: amount, Currency: p[3], Description: p[4]})
	}
	return out, nil
}
func listCommand(store journal.Store, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ledger list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	through := flags.String("through", "", "expand recurring entries through date")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: ledger list [--through YYYY-MM-DD]")
	}
	entries, err := store.ReadAll()
	if err != nil {
		return err
	}
	entries, err = withRecurring(store, entries, *through)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(stdout, "DATE\tAMOUNT\tCURRENCY\tDESCRIPTION"); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err = fmt.Fprintf(stdout, "%s\t%d\t%s\t%s\n", e.Date, e.Amount, e.Currency, e.Description); err != nil {
			return err
		}
	}
	return nil
}
func balanceCommand(store journal.Store, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ledger balance", flag.ContinueOnError)
	flags.SetOutput(stderr)
	target := flags.String("in", "", "target currency")
	ratesPath := flags.String("rates", "", "rates file")
	through := flags.String("through", "", "expand recurring entries through date")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: ledger balance --in XXX --rates PATH [--through YYYY-MM-DD]")
	}
	if *target == "" || *ratesPath == "" {
		return errors.New("usage: ledger balance --in XXX --rates PATH")
	}
	entries, err := store.ReadAll()
	if err != nil {
		return err
	}
	entries, err = withRecurring(store, entries, *through)
	if err != nil {
		return err
	}
	f, err := os.Open(*ratesPath)
	if err != nil {
		return fmt.Errorf("open rates: %w", err)
	}
	defer f.Close()
	rates, err := journal.ReadRates(f)
	if err != nil {
		return err
	}
	total, err := journal.ConvertTotal(entries, *target, rates)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Balance: %d %s\n", total, *target)
	return err
}
func importCommand(store journal.Store, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: ledger import csv|json <file>")
	}
	f, err := os.Open(args[1])
	if err != nil {
		return fmt.Errorf("open import: %w", err)
	}
	entries, readErr := journal.ReadImport(args[0], f)
	closeErr := f.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	release, err := lock(store)
	if err != nil {
		return err
	}
	defer release()
	return store.AddImported(entries)
}
func exportCommand(store journal.Store, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: ledger export csv|json")
	}
	entries, err := store.ReadAll()
	if err != nil {
		return err
	}
	return journal.WriteExport(args[0], stdout, entries)
}
func recurCommand(store journal.Store, args []string) error {
	if len(args) < 5 || args[0] != "add" || args[1] != "monthly" {
		return errors.New("usage: ledger recur add monthly <day> <amount> <currency> <description...>")
	}
	day, err := strconv.Atoi(args[2])
	if err != nil || day < 1 || day > 28 {
		return errors.New("day must be between 1 and 28")
	}
	amount, err := journal.ParseAmount(args[3])
	if err != nil {
		return err
	}
	if err = journal.ValidateCurrency(args[4]); err != nil {
		return err
	}
	description, err := journal.NormalizeDescription(args[5:])
	if err != nil {
		return err
	}
	release, err := lock(store)
	if err != nil {
		return err
	}
	defer release()
	rules, err := readRecurrences(store)
	if err != nil {
		return err
	}
	rules = append(rules, journal.Recurrence{Day: day, Amount: amount, Currency: args[4], Description: description})
	var b strings.Builder
	for _, r := range rules {
		fmt.Fprintf(&b, "monthly\t%d\t%d\t%s\t%s\n", r.Day, r.Amount, r.Currency, r.Description)
	}
	if err := os.WriteFile(store.Path+".recur", []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write recurrence rules: %w", err)
	}
	return nil
}
