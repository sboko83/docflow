---
docflow: 3
id: P001
title: Stable entry IDs with locked, crash-safe journal writes
type: feature
status: active
created: 2026-11-05
permissions: auto
gate: go vet ./... && go test ./...
---
## Goal

Every journal record gets a stable ID that is never reused: `list` shows it and `ledger [--file PATH] delete <ID>` removes one record. Several `ledger` processes may write the same journal at once without losing or duplicating records: each write waits for an exclusive lock file, gives up after a timeout, and breaks a stale lock. A write that is interrupted at any point leaves the journal either as it was or fully updated, never partially written.

## Context

The base journal format and CLI are described in `README.md`.

## Constraints

Use only the Go standard library. Base (version 1) journals stay readable. Amounts are positive integers. All text files are UTF-8 with `\n` line endings.

## Traps

Reads (`list`, `balance`) never create, wait for or remove the lock, and leave a version 1 journal byte-identical; only a write migrates it. A process that times out never removes a lock it did not create. A write interrupted before its rename leaves the journal byte-identical and does not consume an ID. Tests that call `run()` in one process cannot catch races between processes: locking and crash behaviour must be tested by starting the built binary as separate processes.

## Decisions

None.

## Phase 1: Stable IDs

### [ ] P001.1.1 Add entry IDs and delete
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/entry.go
- internal/journal/store.go
Do:
- Version 2 journal: first line exactly `# ledger-v2 next=N`, where `N` is the next ID to assign (a positive decimal integer), then records `ID<TAB>YYYY-MM-DD<TAB>amount<TAB>description`. IDs are positive decimal integers, strictly increasing in file order, and all smaller than `N`. A version 2 journal with no records is just the header line.
- A journal without the header is version 1 (the base format, an empty file included): its records read as IDs 1, 2, … in file order, with `next` one past the last. Any line starting with `#` other than a valid version 2 header on the first line, a non-increasing ID, an ID not smaller than `next`, or a malformed record makes every command exit 1 with a diagnostic on stderr and leave all files unchanged.
- `add` writes a version 2 journal: an absent journal is created as `# ledger-v2 next=2` plus record 1; a version 1 journal is migrated to version 2 with its legacy IDs and the new record appended. The new record gets ID `next`, and `next` grows by one. `add` prints exactly `Entry N added` where `N` is the new ID.
- Add `ledger [--file PATH] delete <ID>`: remove the record with that ID, keep `next` unchanged so IDs are never reused, migrate a version 1 journal the same way, and print exactly `Entry N deleted`. An ID that is not a positive decimal integer or not present exits 1 with a diagnostic on stderr and changes no file; deleting from an absent journal does not create it.
- `list` prints the heading `ID<TAB>DATE<TAB>AMOUNT<TAB>DESCRIPTION`, then one row per record in file order. `balance` keeps its output `Balance: N`.
- Add tests for the version 2 format, migration of version 1 on `add` and `delete`, reads leaving version 1 unchanged, ID reuse after delete, and every malformed-journal case above.
Verify: `go test ./...`
Commit: P001.1.1: add stable entry IDs and delete

### [ ] P001.1.2 Review entry IDs
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/store.go
- cmd/ledger/main_test.go
- internal/journal/store_test.go
Do:
- Review the phase diff against the Goal, Constraints, Traps, Decisions, and P001.1.1 Do; do not edit files.
- Confirm that reads never rewrite a version 1 journal and that `next` never decreases.
Verify: `go vet ./... && go test ./...`
Role: review
Commit: P001.1.2: review entry IDs

## Phase 2: Safe concurrent writes

### [ ] P001.2.1 Lock the journal during writes
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/store.go
Do:
- Every write (`add`, `delete`) first creates the lock file `<journal path>.lock` exclusively (create-if-absent in one call), creating the journal's parent directory if needed, and holds it until the write ends; the writer then removes its own lock, on success and on failure.
- While the lock exists, retry at least every 50 ms until `LEDGER_LOCK_TIMEOUT` has elapsed (default `5s`), then exit 1 with a diagnostic on stderr containing the word `locked`, leaving the journal and the lock file unchanged.
- A lock file whose modification time is more than `LEDGER_LOCK_STALE` old (default `30s`) is stale: remove it and retry acquiring the lock exclusively.
- `LEDGER_LOCK_TIMEOUT` and `LEDGER_LOCK_STALE` are Go durations (`time.ParseDuration`) greater than zero. Writes reject an invalid or nonpositive value with exit 1 and a diagnostic on stderr before touching any file; reads ignore both variables.
- `list` and `balance` never look at the lock.
- Add tests for waiting until a lock disappears, timeout with the lock left in place, stale-lock breaking with both the default and a configured `LEDGER_LOCK_STALE`, reads under a held lock, and invalid variables.
Verify: `go test ./...`
Commit: P001.2.1: lock the journal during writes

### [ ] P001.2.2 Replace the journal atomically
Read:
- README.md
- cmd/ledger/main.go
- internal/journal/store.go
Do:
- Under the lock, a write reads the current journal, writes the complete new contents to `<journal path>.tmp` (truncating any leftover file), syncs and closes it, then renames it over the journal path. Never modify the journal file in place.
- `LEDGER_CRASH=before-rename` simulates a crash for tests: right after the temporary file is synced and closed, the process exits with code 99 without renaming and without removing the lock. Any other non-empty value makes a write exit 1 with a diagnostic on stderr before touching any file; reads ignore the variable.
- After such a crash the journal is byte-identical to before the write, and once the lock goes stale the next write succeeds and assigns the ID the crashed write would have used.
- Add tests for crash before rename on `add`, on `delete`, and on the first write to a version 1 journal, and for recovery after the lock goes stale.
Verify: `go test ./...`
Commit: P001.2.2: replace the journal atomically

### [ ] P001.2.3 Accept concurrent and crash-safe writes
Read:
- README.md
- cmd/ledger/main_test.go
- internal/journal/store_test.go
Do:
- In tests, build `./cmd/ledger` into a temporary directory and drive the binary as separate processes.
- Start 12 `add` processes at once on one journal with `LEDGER_LOCK_TIMEOUT=60s`; confirm every process exits 0, `list` shows exactly 12 records with IDs 1–12 and every description once, and no lock file remains.
- Crash an `add` on a version 1 journal with `LEDGER_CRASH=before-rename`; confirm exit 99, a byte-identical journal, `list` still working under the leftover lock, and a successful migrating `add` after the lock is made stale.
- Add or adjust tests for the scenario without weakening existing tests.
Verify: `go test ./...`
Role: acceptance
Commit: P001.2.3: accept concurrent and crash-safe writes
