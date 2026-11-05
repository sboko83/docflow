package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"benchledger/internal/journal"
)

const crashExitCode = 99

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
		return errors.New("command required: add, delete, list, or balance")
	}
	store := journal.Store{Path: *path}
	switch args[0] {
	case "add":
		return addCommand(store, args[1:], stdout, lookupEnv)
	case "delete":
		return deleteCommand(store, args[1:], stdout, lookupEnv)
	case "list":
		return listCommand(store, args[1:], stdout)
	case "balance":
		return balanceCommand(store, args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q: use add, delete, list, or balance", args[0])
	}
}

// writeOptions reads the lock and crash variables that writes obey.
func writeOptions(lookupEnv func(string) (string, bool)) (journal.WriteOptions, error) {
	options := journal.WriteOptions{LockTimeout: 5 * time.Second, LockStale: 30 * time.Second}
	for _, item := range []struct {
		name   string
		target *time.Duration
	}{{"LEDGER_LOCK_TIMEOUT", &options.LockTimeout}, {"LEDGER_LOCK_STALE", &options.LockStale}} {
		value, ok := lookupEnv(item.name)
		if !ok || value == "" {
			continue
		}
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return journal.WriteOptions{}, fmt.Errorf("invalid %s %q: use a positive duration such as 5s", item.name, value)
		}
		*item.target = duration
	}
	switch crash, _ := lookupEnv("LEDGER_CRASH"); crash {
	case "":
	case "before-rename":
		options.BeforeRename = func() { os.Exit(crashExitCode) }
	default:
		return journal.WriteOptions{}, fmt.Errorf("invalid LEDGER_CRASH %q: only before-rename is supported", crash)
	}
	return options, nil
}

func addCommand(store journal.Store, args []string, stdout io.Writer, lookupEnv func(string) (string, bool)) error {
	if len(args) < 3 {
		return errors.New("usage: ledger add <YYYY-MM-DD> <amount> <description...>")
	}
	entry, err := journal.NewEntry(args[0], args[1], args[2:])
	if err != nil {
		return err
	}
	options, err := writeOptions(lookupEnv)
	if err != nil {
		return err
	}
	var id int64
	err = store.Update(options, func(current *journal.Journal) error {
		id, err = current.Add(entry)
		return err
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Entry %d added\n", id)
	return err
}

func deleteCommand(store journal.Store, args []string, stdout io.Writer, lookupEnv func(string) (string, bool)) error {
	if len(args) != 1 {
		return errors.New("usage: ledger delete <ID>")
	}
	id, err := journal.ParseID(args[0])
	if err != nil {
		return err
	}
	options, err := writeOptions(lookupEnv)
	if err != nil {
		return err
	}
	// Deleting from an absent journal must not create it.
	if _, statErr := os.Stat(store.Path); errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("no entry with ID %d", id)
	}
	if err := store.Update(options, func(current *journal.Journal) error {
		return current.Delete(id)
	}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Entry %d deleted\n", id)
	return err
}

func listCommand(store journal.Store, args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: ledger list")
	}
	current, err := store.Load()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "ID\tDATE\tAMOUNT\tDESCRIPTION"); err != nil {
		return err
	}
	for _, record := range current.Records {
		if _, err := fmt.Fprintf(stdout, "%d\t%s\t%d\t%s\n", record.ID, record.Date, record.Amount, record.Description); err != nil {
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
