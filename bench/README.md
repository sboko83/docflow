# Benchmark fixtures

`bench/base/` is the shared starter project for benchmark tasks. The task
plans, hidden oracles and reference solutions are kept under `bench/tasks/`.
`roster.toml` fixes the routes used by benchmark runs and `catalog.json` is the
model catalog it resolves against: a benchmark must run the same routes on
every machine, so the arms read this fixture instead of the cache of the
person running it. Fixture Go modules are isolated from the `bsv-docflow`
module by `go.mod`.

See [the benchmark design](../docs/design/bench.md) for the runner and result
format. Run history lives in `bench/results/`: `runs.jsonl` plus a generated
README summary; every run appends its rows and commits them itself.

## Live-run findings

The three tasks were run through the full "model × cycle" scheme on
2026-10-31 and 2026-11-01. Every arm passed the oracles, so the corpus
separates arms by cost and time, not quality: the docflow cycle on the
cheap stack (flash executor, flash review/acceptance) completes the
whole corpus for ≈$0.45, vs $1.49 with trusted reviewers and $3.15 for
a one-shot `glm-5.3` run. Details, caveats (n=1) and the next-step
requirement for a hard corpus live in [the design doc](../docs/design/bench.md),
section «Живые прогоны».
