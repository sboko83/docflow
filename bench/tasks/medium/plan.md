---
docflow: 3
id: P001
title: Add categories and monthly budgets
type: feature
status: active
created: 2026-10-29
permissions: auto
gate: go vet ./... && go test ./...
---
## Goal

`ledger [--file PATH] add --category NAME <YYYY-MM-DD> <amount>
<description...>` records an expense in a version 2 journal. Version 2 stores
the category with each record and `list` and `balance` continue to read the
base project's version 1 journal, treating its records as `Uncategorized`.
`ledger [--file PATH] budget set <category> <amount>` stores a monthly budget
in `<journal path>.budgets`. `ledger [--file PATH] report --month YYYY-MM`
prints each category's monthly sum, budget, and whether spending exceeded it.

## Context

The base journal format and CLI are described in `README.md`.

## Constraints

Keep the version 1 journal readable. Amounts are positive integers in the
account's smallest unit. Use only the Go standard library.

## Traps

The category flag follows `add`. The `budget` command uses a separate file
beside the selected journal.

## Decisions

None.

## Phase 1: Version 2 journal

### [ ] P001.1.1 Add categorized journal records
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
Do:
- Add the required `--category NAME` option after `add`; accept the documented positional date, amount, and description after the option.
- Write version 2 journals with the first line `# ledger-v2` and records `YYYY-MM-DD<TAB>amount<TAB>category<TAB>description`.
- Read existing version 1 records `YYYY-MM-DD<TAB>amount<TAB>description` and expose their category as `Uncategorized`; when adding to a version 1 journal, migrate its records to version 2 before appending the new record.
- Reject an empty category or one containing tab, carriage return, or newline with exit code 1 and a diagnostic on stderr.
- Add package tests for version 1 reading, version 2 reading and writing, migration, and invalid categories.
Verify: `go test ./...`
Commit: P001.1.1: add categorized journal records

### [ ] P001.1.2 Review journal format
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
- cmd/ledger/main_test.go
- internal/journal/entry_test.go
- internal/journal/store_test.go
Do:
- Review the phase diff against the Goal, Constraints, Decisions, and P001.1.1 Do; do not edit files.
- Confirm version 1 records remain readable and a write migrates them without changing date, amount, or description.
Verify: `go vet ./... && go test ./...`
Role: review
Commit: P001.1.2: review journal format

## Phase 2: Budgets and reports

### [ ] P001.2.1 Add budgets and monthly reports
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
Do:
- Implement `ledger [--file PATH] budget set <category> <amount>`; store one tab-separated `category<TAB>amount` row per category in `<journal path>.budgets`, replacing an existing row when that category is set again.
- Implement `ledger [--file PATH] report --month YYYY-MM`; reject invalid months, sum journal amounts for that month by category, and include categories with either spending or a configured budget.
- Print the exact header `CATEGORY<TAB>SPENT<TAB>BUDGET<TAB>STATUS`, then rows sorted by category; use `-` for a category without a budget and `OVER` only when spent is greater than its budget, otherwise `OK`.
- Add CLI tests for budget persistence and replacement, month filtering, category totals, sorted rows, and over-budget status.
Verify: `go test ./...`
Commit: P001.2.1: add budgets and monthly reports

### [ ] P001.2.2 Accept category reports
Read:
- README.md
- cmd/ledger/main_test.go
- internal/journal/store_test.go
Do:
- Exercise the CLI on a version 1 journal fixture, add categorized expenses, set multiple budgets, and request reports for a month with both under-budget and over-budget categories.
- Confirm the report includes the legacy `Uncategorized` total, excludes records from other months, and prints categories in sorted order.
- Add or adjust end-to-end CLI tests for this scenario without weakening existing tests.
Verify: `go test ./...`
Role: acceptance
Commit: P001.2.2: accept category reports
