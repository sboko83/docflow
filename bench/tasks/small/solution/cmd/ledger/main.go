package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"benchledger/internal/journal"
)

type filters struct {
	since string
	until string
}

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
		return errors.New("command required: add, list, or balance")
	}
	store := journal.Store{Path: *path}
	switch args[0] {
	case "add":
		return addCommand(store, args[1:], stdout)
	case "list":
		return listCommand(store, args[1:], stdout, stderr)
	case "balance":
		return balanceCommand(store, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q: use add, list, or balance", args[0])
	}
}

func addCommand(store journal.Store, args []string, stdout io.Writer) error {
	if len(args) < 3 {
		return errors.New("usage: ledger add <YYYY-MM-DD> <amount> <description...>")
	}
	entry, err := journal.NewEntry(args[0], args[1], args[2:])
	if err != nil {
		return err
	}
	if err := store.Append(entry); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Entry added")
	return err
}

func parseFilters(command string, args []string, stderr io.Writer, allowJSON bool) (filters, bool, error) {
	flags := flag.NewFlagSet("ledger "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	since := flags.String("since", "", "include records on or after date (YYYY-MM-DD)")
	until := flags.String("until", "", "include records on or before date (YYYY-MM-DD)")
	jsonOutput := false
	if allowJSON {
		flags.BoolVar(&jsonOutput, "json", false, "print records as JSON")
	}
	if err := flags.Parse(args); err != nil {
		return filters{}, false, err
	}
	for label, value := range map[string]string{"--since": *since, "--until": *until} {
		if value == "" {
			continue
		}
		date, err := time.Parse("2006-01-02", value)
		if err != nil || date.Format("2006-01-02") != value || date.Year() < 1 {
			return filters{}, false, fmt.Errorf("invalid %s date %q: expected YYYY-MM-DD", label, value)
		}
	}
	return filters{since: *since, until: *until}, jsonOutput, nil
}

func matches(entry journal.Entry, filter filters) bool {
	return (filter.since == "" || entry.Date >= filter.since) &&
		(filter.until == "" || entry.Date <= filter.until)
}

func listCommand(store journal.Store, args []string, stdout, stderr io.Writer) error {
	filter, jsonOutput, err := parseFilters("list", args, stderr, true)
	if err != nil {
		return err
	}
	entries, err := store.ReadAll()
	if err != nil {
		return err
	}
	selected := make([]journal.Entry, 0, len(entries))
	for _, entry := range entries {
		if matches(entry, filter) {
			selected = append(selected, entry)
		}
	}
	if jsonOutput {
		type jsonEntry struct {
			Date        string `json:"date"`
			Amount      int64  `json:"amount"`
			Description string `json:"description"`
		}
		result := make([]jsonEntry, 0, len(selected))
		for _, entry := range selected {
			result = append(result, jsonEntry{entry.Date, entry.Amount, entry.Description})
		}
		return json.NewEncoder(stdout).Encode(result)
	}
	if _, err := fmt.Fprintln(stdout, "DATE\tAMOUNT\tDESCRIPTION"); err != nil {
		return err
	}
	for _, entry := range selected {
		if _, err := fmt.Fprintf(stdout, "%s\t%d\t%s\n", entry.Date, entry.Amount, entry.Description); err != nil {
			return err
		}
	}
	return nil
}

func balanceCommand(store journal.Store, args []string, stdout, stderr io.Writer) error {
	filter, _, err := parseFilters("balance", args, stderr, false)
	if err != nil {
		return err
	}
	entries, err := store.ReadAll()
	if err != nil {
		return err
	}
	selected := make([]journal.Entry, 0, len(entries))
	for _, entry := range entries {
		if matches(entry, filter) {
			selected = append(selected, entry)
		}
	}
	total, err := journal.Total(selected)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Balance: %d\n", total)
	return err
}
