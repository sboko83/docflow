# Benchmark fixtures

`bench/base/` is the shared starter project for benchmark tasks. The task
plans, hidden oracles and reference solutions are kept under `bench/tasks/`.
`roster.toml` fixes the routes used by benchmark runs. Fixture Go modules are
isolated from the `bsv-docflow` module by `go.mod`.

See [the benchmark design](../docs/design/bench.md) for the runner and result
format.
