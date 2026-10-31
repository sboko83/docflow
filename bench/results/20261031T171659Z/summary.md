# Benchmark run 20261031T171659Z

| Task | Arm | Route / roster | Oracle | Cost | Minutes | Units | Attempts | Replans |
|---|---|---|---:|---:|---:|---:|---:|---:|
| small | single | codex \| gpt-6.1-sol \| high | 3/11 | — | 0.0 | 0 (0 done, 0 blocked) | 0 | 0 |
| small | docflow | roster | 11/11 | 0.03 | 4.6 | 1 (1 done, 0 blocked) | 1 | 0 |
| small | docflow-one | codex \| gpt-6.1-sol \| high | 11/11 | — | 2.0 | 1 (1 done, 0 blocked) | 1 | 0 |

### small / single oracle failures

```text
=== RUN   TestListSinceIncludesBoundary
    ledger_test.go:74: list output = "" (code 1), want "DATEworkwork/bench/<run-id>/… itemworkwork/bench/<run-id>/…
--- FAIL: TestListSinceIncludesBoundary (0.22s)
=== RUN   TestListUntilIncludesBoundary
    ledger_test.go:82: list output = "" (code 1), want "DATEworkwork/bench/<run-id>/… itemworkwork/bench/<run-id>/…
--- FAIL: TestListUntilIncludesBoundary (0.01s)
=== RUN   TestListDateRangeIncludesBothBoundaries
    ledger_test.go:90: list output = "" (code 1), want "DATEworkwork/bench/<run-id>/… itemworkwork/bench/<run-id>/…
--- FAIL: TestListDateRangeIncludesBothBoundaries (0.01s)
=== RUN   TestBalanceSinceIncludesBoundary
    ledger_test.go:97: balance output = "" (code 1)
--- FAIL: TestBalanceSinceIncludesBoundary (0.01s)
=== RUN   TestBalanceUntilIncludesBoundary
    ledger_test.go:104: balance output = "" (code 1)
--- FAIL: TestBalanceUntilIncludesBoundary (0.01s)
=== RUN   TestBalanceDateRangeIncludesBothBoundaries
    ledger_test.go:111: balance output = "" (code 1)
--- FAIL: TestBalanceDateRangeIncludesBothBoundaries (0.01s)
=== RUN   TestListJSONHasDocumentedFieldsAndValues
    ledger_test.go:118: list exited with 1
--- FAIL: TestListJSONHasDocumentedFieldsAndValues (0.01s)
=== RUN   TestListJSONDateFilterAndEmptyArray
    ledger_test.go:146: filtered JSON exited with 1: ""
--- FAIL: TestListJSONDateFilterAndEmptyArray (0.01s)
FAIL
FAIL	benchledgerworkwork/bench/<run-id>/…	0.761s
```
