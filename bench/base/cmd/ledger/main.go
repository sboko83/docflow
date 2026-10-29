package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

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
		return errors.New("command required: add, list, or balance")
	}
	store := journal.Store{Path: *path}
	switch args[0] {
	case "add":
		return addCommand(store, args[1:], stdout)
	case "list":
		return listCommand(store, args[1:], stdout)
	case "balance":
		return balanceCommand(store, args[1:], stdout)
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

func listCommand(store journal.Store, args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: ledger list")
	}
	entries, err := store.ReadAll()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "DATE\tAMOUNT\tDESCRIPTION"); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := fmt.Fprintf(stdout, "%s\t%d\t%s\n", entry.Date, entry.Amount, entry.Description); err != nil {
			return err
		}
	}
	return nil
}

func balanceCommand(store journal.Store, args []string, stdout io.Writer) error {
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
