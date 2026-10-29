---
docflow: 3
id: P001
title: Add currencies, exchange, import, export and recurring entries
type: feature
status: active
created: 2026-10-29
permissions: auto
gate: go vet ./... && go test ./...
---
## Goal

`ledger [--file PATH] add --currency XXX <YYYY-MM-DD> <amount> <description...>` stores an expense in the given currency or the journal's configured default currency. `balance --in XXX` converts all entries using a rates file; `import csv|json <file>` skips records already present by date, amount, currency and description; `export csv|json` writes all journal entries. `recur add monthly <day> <amount> <currency> <description...>` adds a recurring monthly rule, expanded by reads through an explicit date. Writes hold an exclusive journal file lock.

## Context

The base journal format and CLI are described in `README.md`.

## Constraints

Use only the Go standard library. Preserve the base version 1 journal as readable. Amounts are positive integers in the currency's smallest unit. Currency codes are three uppercase ASCII letters. All new text files are UTF-8.

## Traps

Options follow their subcommand. Rate conversion uses integer arithmetic and rounds down to the smallest unit of the requested currency. Recurring entries are virtual: reads expand them without appending generated rows to the journal. Import deduplication compares all four documented fields.

## Decisions

None.

## Phase 1: Currency-aware journal

### [ ] P001.1.1 Add currency settings and records
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
Do:
- Add `ledger [--file PATH] currency set XXX`; persist the default currency as one three-letter code plus newline in `<journal path>.settings`. If no setting exists, use `USD`.
- Add `--currency XXX` after `add`; when omitted, use the configured default. Write new journals with first line `# ledger-v2`, then records `YYYY-MM-DD<TAB>amount<TAB>currency<TAB>description`.
- Read base version 1 records as `USD`; read version 2 records as `USD`; read version 3 journals whose first line is `# ledger-v3` and records have the four fields above. When adding to a version 1 or 2 journal, migrate it to version 3 without changing existing values except assigning `USD` to legacy records.
- Reject invalid currency codes, nonpositive amounts, malformed settings and malformed journal records with exit code 1 and a diagnostic on stderr.
- Add tests for settings, default currency, explicit currency, version 1 and 2 migration, version 3 read/write, and invalid input.
Verify: `go test ./...`
Commit: P001.1.1: add currency-aware journal

### [ ] P001.1.2 Review currency journal
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
- cmd/ledger/main_test.go
- internal/journal/store_test.go
Do:
- Review the phase diff against the Goal, Constraints, Decisions, and P001.1.1 Do; do not edit files.
- Confirm legacy records remain readable and migration preserves date, amount, and description while assigning `USD`.
Verify: `go vet ./... && go test ./...`
Role: review
Commit: P001.1.2: review currency journal

## Phase 2: Conversion and data exchange

### [ ] P001.2.1 Convert balances with exchange rates
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
Do:
- Add `ledger [--file PATH] balance --in XXX --rates PATH`; rates file rows are `FROM<TAB>TO<TAB>NUMERATOR<TAB>DENOMINATOR`, with one positive integer numerator and denominator per directed conversion. No implicit inverse or multi-hop conversion is used.
- Convert each entry directly from its stored currency to the requested currency using `amount * numerator / denominator`, rounded down; leave same-currency entries unchanged.
- Print exactly `Balance: N XXX` on success. Reject a missing rate, malformed rate, overflow, invalid target currency, or missing option with exit code 1 and a diagnostic on stderr.
- Add CLI tests for mixed currencies, same-currency totals, rounding, missing rates, malformed files, and invalid arguments.
Verify: `go test ./...`
Commit: P001.2.1: convert balances with exchange rates

### [ ] P001.2.2 Import and export journal entries
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
Do:
- Add `ledger [--file PATH] import csv|json <file>`; CSV has the exact header `date,amount,currency,description`, and JSON is an array of objects with exactly string `date`, integer `amount`, string `currency`, and string `description` fields.
- Validate every imported row before writing. If any row is invalid, exit 1 with a diagnostic and leave the journal unchanged.
- Skip an imported entry when an existing or earlier imported entry has the same date, amount, currency, and description; append remaining entries in input order.
- Add `ledger [--file PATH] export csv|json`; export all stored entries in insertion order using the same formats and field names. CSV uses standard quoting and ends with a newline; JSON is a compact array followed by a newline.
- Reject unsupported formats and malformed data with exit code 1 and a diagnostic on stderr. Add tests for both formats, escaping, round trips, duplicates, and atomic validation failure.
Verify: `go test ./...`
Commit: P001.2.2: import and export journal entries

### [ ] P001.2.3 Review exchange and data exchange
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
- cmd/ledger/main_test.go
Do:
- Review the phase diff against the Goal, Constraints, Decisions, and P001.2.1–P001.2.2 Do; do not edit files.
- Confirm conversion requires an explicit directed rate and imports validate the whole input before changing the journal.
Verify: `go vet ./... && go test ./...`
Role: review
Commit: P001.2.3: review exchange and data exchange

## Phase 3: Recurrence and acceptance

### [ ] P001.3.1 Add monthly recurring entries
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
Do:
- Add `ledger [--file PATH] recur add monthly <day> <amount> <currency> <description...>`; accept day 1–28, positive amount, valid currency, and nonempty description. Store rules in `<journal path>.recur` as tab-separated `monthly<TAB>day<TAB>amount<TAB>currency<TAB>description` rows.
- Add optional `--through YYYY-MM-DD` after `list` and `balance`. With it, expand each rule to one virtual entry on that day of every month from the first journal entry's month through the requested month, inclusive, only when the occurrence date is not after the requested date. Without it, do not expand rules.
- Keep virtual entries after stored entries, ordered by occurrence date and then rule insertion order. Do not write virtual entries into the journal.
- Reject invalid rules and dates with exit code 1 and a diagnostic on stderr. Add tests for storage, validation, cutoff, order, and no-mutation-on-read.
Verify: `go test ./...`
Commit: P001.3.1: add monthly recurring entries

### [ ] P001.3.2 Lock journal during writes
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/store.go
- internal/journal/store_test.go
Do:
- Acquire an exclusive lock on the journal path before every journal-changing operation (`add`, `import`, and `recur add`) and hold it through completion of the write.
- Use a lock file at `<journal path>.lock`; create it exclusively and remove it after the write. If the lock already exists, fail with exit code 1 and a diagnostic on stderr without changing the journal or auxiliary files.
- Ensure lock cleanup on success and write failure. Add tests that a pre-existing lock blocks each write operation and that successful operations remove their lock file.
Verify: `go test ./...`
Commit: P001.3.2: lock journal during writes

### [ ] P001.3.3 Accept multi-currency ledger workflows
Read:
- README.md
- cmd/ledger/main_test.go
- internal/journal/store_test.go
Do:
- Exercise an end-to-end CLI scenario that sets a default currency, adds entries in two currencies, converts the balance with a rates file, imports CSV and JSON containing both new and duplicate entries, exports both formats, adds a monthly recurring rule, and reads through a cutoff date.
- Confirm imported duplicates are skipped, recurring entries are virtual, and a pre-existing lock leaves the journal unchanged.
- Add or adjust CLI tests for the full scenario without weakening existing tests.
Verify: `go test ./...`
Role: acceptance
Commit: P001.3.3: accept multi-currency ledger workflows
