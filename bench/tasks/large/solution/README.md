# ledger

`ledger` stores personal expenses in a tab-separated journal. Run it with Go from this directory.

```text
ledger [--file PATH] currency set XXX
ledger [--file PATH] add [--currency XXX] <YYYY-MM-DD> <amount> <description...>
ledger [--file PATH] list [--through YYYY-MM-DD]
ledger [--file PATH] balance --in XXX --rates PATH [--through YYYY-MM-DD]
ledger [--file PATH] import csv|json <file>
ledger [--file PATH] export csv|json
ledger [--file PATH] recur add monthly <day> <amount> <currency> <description...>
```

The journal path comes from `--file` or `LEDGER_FILE`. The default currency is USD and is stored as one code plus newline in `<journal>.settings`; `currency set` changes it. Currency codes are three uppercase ASCII letters. New journals use `# ledger-v3` and tab-separated date, amount, currency, and description fields. Legacy version 1 and version 2 journals are read as USD and migrate to version 3 on append.

Rates are directed rows `FROM<TAB>TO<TAB>NUMERATOR<TAB>DENOMINATOR`; conversion multiplies each amount and rounds down. CSV imports and exports use the header `date,amount,currency,description`. JSON imports and exports use arrays of objects with those four fields. Imports validate all rows before writing and skip exact duplicates.

Monthly recurrence rules are stored in `<journal>.recur`; `list` and `balance` expand them only when `--through` is provided. Expansion is virtual. Writes to the journal, imports, and recurrence changes use `<journal>.lock` and fail if it already exists. Invalid input, malformed files, and unsupported commands exit with code 1 and write a diagnostic to stderr.
