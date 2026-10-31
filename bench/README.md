# Benchmark fixtures

`bench/base/` is the shared starter project for benchmark tasks. The task
plans, hidden oracles and reference solutions are kept under `bench/tasks/`.
`roster.toml` fixes the routes used by benchmark runs and `catalog.json` is the
model catalog it resolves against: a benchmark must run the same routes on
every machine, so the arms read this fixture instead of the cache of the
person running it. Fixture Go modules are isolated from the `bsv-docflow`
module by `go.mod`.

See [the benchmark design](../docs/design/bench.md) for the runner and result
format.
