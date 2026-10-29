# ledger

`ledger` stores personal expenses in a tab-separated text journal. Build or
run it with Go from this directory.

## Commands

```text
ledger [--file PATH] add <YYYY-MM-DD> <amount> <description...>
ledger [--file PATH] list
ledger [--file PATH] balance
```

`--file PATH` selects the journal. If omitted, `LEDGER_FILE` is used. If both
are omitted, the command reports an error. The file is created by `add` when
needed. `amount` is a positive integer in the smallest unit used by the
account; the journal stores it without rounding or currency assumptions.

Each journal record is one UTF-8 line:

```text
YYYY-MM-DD<TAB>amount<TAB>description
```

Descriptions are stored as entered. `list` prints the heading
`DATE\tAMOUNT\tDESCRIPTION`, followed by the records in insertion order.
`balance` prints `Balance: N`, where `N` is the sum of recorded amounts.
Invalid input, unreadable files, malformed journal records and unsupported
commands exit with code 1 and write a diagnostic to stderr.
