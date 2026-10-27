---
id: P047
title: Context growth audit
type: perf
status: draft
created: 2026-10-26
permissions: auto
gate: go test ./...
executor: pi | strata/swift-1.5-iq3_s | high
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
heavy: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
review: claude | claude-sonnet-5-5 | high; codex | gpt-6.1-sol | high
acceptance: claude | claude-sonnet-5-5 | high; codex | gpt-6.1-sol | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
---
## Goal

The context of a docflow session does not grow with the number of closed
plans: a measurement tool reports every input docflow assembles or tells an
agent to read, with its size now and projected at 200 closed plans, and
fails on a threshold; a report shows what agents actually read beyond a
unit's `Read:`; `lint` warns when a `Read:` item pulls a whole large file,
whose archive Gotchas grow with every plan linked from it.

## Context

- docs/design/cycle.md:L77-126

## Constraints

- Tools in `tools/` are `bash` 3.2 or `python3` stdlib only; Go code uses
  the standard library.
- Plan format stays `docflow: 3`; a new optional `docs/docflow.md` key
  needs no version bump, but the format spec, `templates/` and `lint`
  change together.
- `docs/design/runner.md` keeps its content; restructuring it into
  sections must not change any decision it states.
- Language: code, comments, `README.md`, `CHANGELOG.md` in English;
  `docs/` in Russian.
- A unit does not commit and does not rewrite history; the runner commits
  with the unit's `Commit:` line.

## Traps

- `go test ./...` takes ~3 min; run a package's tests while working and the
  full gate once at the end.
- `docflow` may be missing from `PATH`; build the checkout binary with
  `go build -o bin/docflow ./cmd/docflow` (`bin/` is gitignored).
- `docflow prompt` appends `## Archive gotchas`: the Gotchas of every
  archive entry linked from the plan's Context/Decisions and from the text
  each `Read:` item references (`ArchiveGotchas` in
  `internal/format/ref.go`). A whole-file reference pulls the Gotchas of
  every entry the file links; a range or `#anchor` pulls only those linked
  inside it. Measured at planning time: whole `docs/design/runner.md` is
  ~12k tokens plus ~4.5k tokens of Gotchas from 20 entries.
- `docs/design/runner.md` has a single H2 heading, so `#anchor`
  references into it are useless until it gets sections.

## Decisions

- Bound the per-unit cost by narrowing references, not by capping
  Gotchas — a trap from a linked entry is relevant to that area, and the
  range a unit reads decides which entries are linked — rejected: a fixed
  byte cap on `## Archive gotchas` (drops traps at random).
- A whole-file `Read:`/Context item over `whole_read_limit` tokens (new
  optional `docs/docflow.md` key, default 4000) is a `lint` warning, not an
  error — existing archived references and small projects stay valid —
  rejected: an error (breaks running plans on upgrade).

## Phase 1: Measure and bound

### [ ] P047.1.1 Add the context audit tool
Read:
- internal/format/ref.go:L180-340
- internal/cycle/commands.go:L20-90
- internal/lint/lint.go:L270-330
- docs/design/format.md:L55-80
Do:
- Write `tools/context-audit.py` (python3 stdlib), run from the repository
  root, measuring proxy tokens (bytes / 4) of:
  1. Start reads: `docflow status` output plus every `start_read` file.
  2. Skills: the `description:` of every `skills/*/SKILL.md` (always in
     an agent's context when installed) and each skill body.
  3. Every `docs/design/*.md` topic and `docs/usage.md`: size, distinct
     archive entries it links, bytes of their `## Gotchas`.
  4. `docs/docflow.md`: total and the `## Backlog` section.
  5. A `docflow prompt` of a regular and of an acceptance unit of a
     fixture plan in a temporary copy of the repository (both `Read:` a
     whole `docs/design/runner.md` and one line range of it).
- Projection to 200 closed plans: from `git log` of `docs/design/` and
  `docs/archive/`, the average growth per archived plan of each topic's
  size and of its linked Gotchas; print now / per plan / at 200 per row.
- `--check`: exit 1 when, at 200 plans, start reads exceed `start_budget`,
  all skill descriptions together exceed 1500 tokens, or the regular-unit
  prompt with only ranged reads exceeds half of `read_budget`; print the
  failing rows.
- `--json` prints the same rows as JSON.
Verify: `go build -o bin/docflow ./cmd/docflow && PATH="$PWD/bin:$PATH" python3 tools/context-audit.py --json | python3 -c 'import json,sys; rows=json.load(sys.stdin); assert any(r["name"].endswith("runner.md") for r in rows)'`
Traps: `git log` history before the public release is squashed: every
archive entry up to P034 lands in one commit; compute growth per plan
from P035 on.
Depends: -
Commit: P047.1.1: add context audit tool

### [ ] P047.1.2 Report reads beyond a unit's Read list
Read:
- internal/stats/stats.go:L1-80
- docs/design/cycle.md:L94-126
Do:
- Write `tools/read-audit.py` (python3 stdlib). For each unit that
  `docflow stats` lists with source `transcript` or a pi `.out` log in
  `<git-dir>/docflow/run/`: take the unit's `Read:` list from the plan file
  as of the unit's commit (`git log` of `docs/plans/<plan>-*.md`), collect
  the files the agent read (Claude `Read` tool calls and `cat`/`sed`/
  `head`/`grep` file arguments in Bash calls; pi `read` and `bash` tool
  calls), and drop the files the unit's commit changed.
- Print per unit: files and approximate tokens read beyond `Read:`; then
  the top 15 paths by total extra tokens across units, and the share of
  extra reads in all reads.
- Explain the jump of `base_ctx` in `docflow stats` from ~33k (P035) to
  ~44k (P036, P041): compare, at the commits of those units, the sizes of
  `AGENTS.md`, `CLAUDE.md`, the skill descriptions and the start reads;
  print which of them changed by more than 2k tokens, or
  `base_ctx: not explained by docflow inputs`.
- Record the numbers in the Handoff (top paths, share, the base_ctx
  verdict); no doc file holds measurement output.
Verify: `python3 tools/read-audit.py --limit 5 | grep -q '^top '`
Traps: Claude transcripts live in `~/.claude/projects/<cwd with / → ->/`;
the transcript of a unit is found the way `internal/stats` finds it. Do
not print file contents, only paths and sizes.
Depends: P047.1.1
Commit: P047.1.2: report reads beyond unit read lists

### [ ] P047.1.3 Warn on whole reads of large files
Read:
- internal/lint/lint.go:L270-330
- internal/format/ref.go:L180-260
- docs/design/format.md:L55-80
- templates/docflow.md
- docs/design/runner.md:L1-40
Do:
- New optional `docs/docflow.md` key `whole_read_limit` (positive integer,
  tokens, default 4000). `lint` warns for every `Read:` item and every
  `## Context` item of a `[ ]`/`[~]`/`[!]` unit's plan that references a
  whole file over the limit:
  `<plan>:<line>: warning: <path> is ~N tokens whole (+M tokens of archive gotchas); reference a range or #anchor`.
  M is what `ArchiveGotchas` would pull for that file.
- Split `docs/design/runner.md` into H2 sections by its existing topics
  (cycle, result JSON, fallback and timeout, output, events and stats, …)
  without changing any statement; keep every existing anchor that other
  files reference working, or update the references.
- Update this repository's `docs/docflow.md` only if its own plans need a
  different limit; document the key in `docs/design/format.md` and the
  templates comment; `CHANGELOG.md` `### Added`.
- Rewrite whole-file references to large topics in the `bsv-docflow-plan`
  skill's sample and rules, if any, to ranges or anchors.
- Tests in `internal/lint`: warning above the limit, none for a range or
  an anchor, the key's validation, the gotchas count in the message.
Verify: `go test ./internal/lint/ ./internal/format/ && go build -o bin/docflow ./cmd/docflow && bin/docflow lint && PATH="$PWD/bin:$PATH" python3 tools/context-audit.py --check`
Traps: `--check` may fail on a row unrelated to whole reads (skill
descriptions, start reads); fix it in this unit only when the fix is a
size cut of the file named in the row; otherwise reply `blocked` with
`blocker: "plan"` and the failing rows.
Depends: P047.1.2
Role: heavy
Commit: P047.1.3: warn on whole reads of large files

### [ ] P047.1.4 Review the context audit
Read:
- docs/design/format.md:L55-80
Do:
- Review the diff named in the prompt's `## Phase diff` against `## Goal`,
  `## Constraints`, `## Decisions` and each unit's `Do:`. Check that the
  restructured `docs/design/runner.md` states the same decisions as
  before. Edit nothing.
- A defect — reply `blocked` with `blocker: "plan"`; observations without
  a defect — plain Handoff items.
Verify: `go test ./...`
Role: review
Commit: P047.1.4: review context audit

## Phase 2: Acceptance

### [ ] P047.2.1 Accept the context bounds
Read:
- tools/parity/README.md:L1-60
- tools/parity/scenarios/lint.sh
Do:
- One scenario per Goal statement through the built CLI:
  1. `python3 tools/context-audit.py --check` passes on this repository
     and prints the projection to 200 plans for every row.
  2. A parity scenario `tools/parity/scenarios/lint_whole_read.sh`: a
     plan unit reading a whole 20 KB file linked to two archive entries
     with Gotchas → `docflow lint` prints the warning with both numbers;
     the same unit with a line range → no warning; `whole_read_limit`
     raised above the file size → no warning.
  3. `python3 tools/read-audit.py --limit 5` prints per-unit rows and the
     `top` list.
- Close every `Unverified:` item of earlier units with a test, or give it a
  disposition as the plan skill's Acceptance section requires.
Verify: `go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow && PATH="$PWD/bin:$PATH" python3 tools/context-audit.py --check`
Traps: A failing scenario is fixed inside this unit when the fix is small;
otherwise reply `blocked` with the reason.
Depends: P047.1.1, P047.1.2, P047.1.3, P047.1.4
Role: acceptance
Commit: P047.2.1: accept context bounds
