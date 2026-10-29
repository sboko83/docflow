---
docflow: 3
id: P001
title: Add date filters and JSON output
type: feature
status: active
created: 2026-10-29
permissions: auto
gate: go vet ./... && go test ./...
---
## Goal

`ledger [--file PATH] list [--since YYYY-MM-DD] [--until YYYY-MM-DD]
[--json]` and `ledger [--file PATH] balance [--since YYYY-MM-DD]
[--until YYYY-MM-DD]` accept inclusive date filters. `list --json` prints a
JSON array of objects with string `date`, numeric `amount`, and string
`description` fields. An invalid filter date exits with code 1.

## Context

The ledger journal is a tab-separated file described in `README.md`.

## Constraints

Keep the journal format stable. Preserve existing unfiltered output.

## Traps

Filter and JSON flags follow the command name.

## Decisions

None.

## Phase 1: Filter commands

### [ ] P001.1.1 Add date filters and JSON output
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
Do:
- Add inclusive `--since YYYY-MM-DD` and `--until YYYY-MM-DD` filters to `list` and `balance`; a record matches when its date is on or after `--since` and on or before `--until`.
- Accept filters after their subcommand using `ledger [--file PATH] list [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--json]` and `ledger [--file PATH] balance [--since YYYY-MM-DD] [--until YYYY-MM-DD]`.
- Add `--json` to `list`; print one JSON array whose objects have exactly the string fields `date`, `description` and numeric field `amount`, in journal insertion order; print `[]` when no records match.
- Reject a `--since` or `--until` value that is not a real `YYYY-MM-DD` calendar date with exit code 1 and a diagnostic on stderr.
- Preserve the existing tab-separated `list` output and `Balance: N` output when these options are not used.
- Add tests for the command behavior.
Verify: `go test ./...`
Commit: P001.1.1: add date filters and JSON output
