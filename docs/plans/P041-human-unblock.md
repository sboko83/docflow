---
id: P041
title: Human unblock
type: feature
status: draft
created: 2026-10-25
permissions: auto
gate: go test ./...
executor: pi | strata/swift-1.5-iq3_s | medium
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
cheap: pi | strata/swift-1.5-iq3_s | medium; pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | high; claude | claude-haiku-5-5 | -
heavy: pi | strata/swift-1.5-iq3_s | high; pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
review: codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
acceptance: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
replan: codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
---
## Goal

A unit blocked with `needs human` resumes after the person has done the
step: `docflow unblock <id> --note "…"` sets it `[~]` with an
`Unblocked:` Handoff item, `docflow run` names that command when it stops
on such a block and, in a terminal, asks at the start of the run whether
the step is done; the resumed agent checks the blocked condition before it
goes on.

## Context

- docs/design/cycle.md#цикл-юнита
- docs/design/runner.md:L40-70

## Constraints

- Go standard library only.
- Plan format stays `docflow: 3`: no new unit status, `Unblocked:` is an
  ordinary Handoff item.
- `docflow unblock` lifts only a human block. The latest `Blocked (…)`
  Handoff item of the unit decides the class; a legacy `Blocked: …` item
  without a class counts as `human`. A `plan` block goes to replan, as now.
- `docflow unblock` does not commit, like `docflow block`.
- The run asks about a blocked unit only at the start of a run, only when
  stdin is a terminal and `--yes` is not given. A block in the middle of a
  run stops it, as now.
- Language: code, comments, `README.md`, `CHANGELOG.md` in English;
  `docs/` in Russian; `README.md` and `README.ru.md` change together.
- A unit does not commit and does not rewrite history; the runner commits
  with the unit's `Commit:` line.

## Traps

- `go test ./...` takes ~3 min; run a package's tests while working and
  the full gate once at the end.
- `docflow` may be missing from `PATH`; build the checkout binary with
  `go build -o bin/docflow ./cmd/docflow` (`bin/` is gitignored).
- `docflow block` writes the Handoff item as `Blocked (<needs>): <reason>`
  (`internal/cycle/verify.go`, func `Block`); parse exactly that shape.

## Decisions

- The person confirms with a command, `docflow unblock`, not by answering
  a run that waits — a run goes unattended for hours, a process waiting on
  stdin holds the terminal and has no stdin without a TTY — rejected: run
  waits for Enter after a human block.
- The run asks only at the start of a run in a terminal without `--yes` —
  the person is present exactly when they start `docflow run` — rejected:
  asking in the middle of a run; `--yes` unblocking on its own (an
  unattended run cannot vouch for a human step).
- `unblock` always sets `[~]` — the work of a human-blocked unit stays
  valid and a `[~]` prompt does not check the tree — rejected: `[ ]` on a
  clean tree.
- The resumed agent checks the blocked condition before it continues —
  the note is a claim, not evidence — rejected: trusting the note.

## Phase 1: Unblock

### [ ] P041.1.1 Add docflow unblock
Read:
- internal/cycle/verify.go:L570-606
- internal/cycle/commands.go:L210-320
- cmd/docflow/main.go:L30-60
- cmd/docflow/help_text.go:L130-160
- internal/cycle/done_test.go:L165-243
- docs/design/cli.md:L1-48
- docs/usage.md:L569-580
- README.md:L60-75
- README.ru.md:L58-72
Do:
- Add `func Unblock(root, unitID, note string) (Result, error)` next to
  `Block` in `internal/cycle/verify.go`: the unit must be `[!]`, else exit
  `EXIT_ORDER` with `<id> is [<s>], expected [!]`; find the unit's latest
  Handoff item starting with `Blocked`; class `plan` → `EXIT_ORDER` with
  `<id> is blocked (needs plan): replan it with bsv-docflow-plan`; class
  `human` or no class → set `[~]`, append the Handoff item
  `Unblocked: <note>` (one line; empty note → `Unblocked: the person
  reports the blocked step done`), save, print `<id>: unblocked`.
- Register the command `unblock` in `cmd/docflow/main.go` (positional
  `id`, optional `--note NOTE`, help `resume a unit blocked for a human`),
  its handler, and its usage text in `cmd/docflow/help_text.go`; add it to
  `runnerOnly` in `main.go`, so an agent under the runner cannot unblock
  its own unit (the runner calls it through `cycle.Self`, like `block`).
- `docflow status`: the `blocked:` line ends with ` (needs human)` or
  ` (needs plan)` from the same latest `Blocked` item; for `human` add the
  next line `unblock: docflow unblock <id> --note "…"`.
- Tests in `internal/cycle/done_test.go` and `internal/cycle/cycle_test.go`:
  human block → unblock → `[~]` and the Handoff item; plan block refused
  with the plan unchanged; legacy `Blocked:` item unblocked; wrong status
  refused; empty note text; `status` lines for both classes. Update the
  command list in `cmd/docflow/main_test.go`.
- Docs: `docs/design/cli.md` command table, `docs/design/cycle.md` (the
  block bullet: a human block is lifted by `unblock`, a plan block by
  replan), `docs/usage.md` section «Блок и replan» and the situations table,
  `README.md`/`README.ru.md` line about `[!]`; `CHANGELOG.md`
  `## [Unreleased]` → `### Added`.
Verify: `go test ./internal/cycle/ ./cmd/docflow/ && go build -o bin/docflow ./cmd/docflow && bin/docflow unblock --help | grep -q -- '--note'`
Traps: `cmd/docflow/main_test.go` pins the exact ordered command list; insert
`unblock` right after `block` in both the table and the test.
Depends: -
Commit: P041.1.1: add docflow unblock

### [ ] P041.1.2 Ask to unblock at run start
Read:
- internal/runner/run.go:L150-280
- internal/runner/run.go:L895-925
- internal/runner/helpers.go:L180-240
- internal/runner/helpers.go:L315-370
- internal/runner/terminal_run_test.go:L51-125
- docs/design/runner.md:L40-70
- docs/usage.md:L400-412
Do:
- When a run stops on a `blocked` result with `needs human`
  (`run.go`, the `result["status"] == "blocked"` branch), print after the
  red stop line: `unblock: docflow unblock <id> --note "…", then docflow run`.
- When the first selection of a run is `blocked` (`sel.Kind == "blocked"`):
  read the class from the unit's latest `Blocked` Handoff item. `plan` →
  as now. `human` with stdin a terminal and no `--yes` → print the reason,
  ask `done? [y/N] `, on `y` ask `note: ` (one line, may be empty), run
  `cycle.Self(root, "unblock", id, "--note="+note)`, then select again and
  go on with the usual start (prerequisites, start confirmation, quota).
  Any other answer, no terminal, or `--yes` → the current stop with code 3
  plus the `unblock:` hint line.
- Ask only for the first selection of the run; a block reached later stops
  the run as now.
- Tests: the hint line after a human block (fake agent); in a real TTY
  (`runInTerminal`) — `y` + note resumes the unit in the same run and the
  plan has `Unblocked: <note>`; `n` → code 3, plan unchanged; no TTY and
  `--yes` → no question, code 3 with the hint; a plan block → no question.
- Docs: `docs/design/runner.md` (step 2 of the cycle: the hint and the
  start question), `docs/usage.md` (code 3 item); `CHANGELOG.md`
  `## [Unreleased]`.
Verify: `go test ./internal/runner/`
Traps: With a terminal the run starts `watchKeys` on stdin and turns echo
off (`noEcho`) before the loop; the question must read its answer without
the key watcher eating it, and the typed note must be visible — follow how
`confirmRunStartWithIO` coexists with them, and test it in a real TTY, not
with a `bytes.Buffer` only.
Role: heavy
Commit: P041.1.2: ask to unblock at run start

### [ ] P041.1.3 Re-check the condition after unblock
Read:
- internal/cycle/commands.go:L17-50
- internal/agents/agents.go:L135-176
- internal/runner/start_test.go:L15-50
- skills/bsv-docflow-run/SKILL.md:L40-62
- skills/bsv-docflow-run/SKILL.md:L104-130
- skills/bsv-docflow-plan/SKILL.md
Do:
- Add to both cycle texts — `cycleText` in `internal/cycle/commands.go`
  and `RunnerCycle` in `internal/agents/agents.go` — one rule: when the
  unit's Handoff has `Unblocked:` after its latest `Blocked (human)`,
  first check the blocked condition with a command (a file, a login, a
  device state); met → go on with `Do:`; not met → block again with
  `human`, naming what is still missing, without doing the rest of `Do:`.
- Same rule in `skills/bsv-docflow-run/SKILL.md` («When to block» or the
  runner-mode paragraph); there also say that a human block is lifted by
  `docflow unblock <id> --note`, a plan block by replan.
- `skills/bsv-docflow-plan/SKILL.md`, section «Ask before units» or
  «Unit»: a step only a person can do goes to `## Prerequisites`; when it
  must happen mid-plan, it opens its unit, before the unit's long work, so
  the block comes before the cost.
- Tests: `internal/runner/start_test.go` `TestRunnerPromptInstructions`
  and the `prompt` test in `internal/cycle/cycle_test.go` assert the new
  rule text.
- `docs/design/cycle.md`: one line on the re-check;
  `docs/design/skills.md` only if it lists the run skill's rules.
Verify: `go test ./internal/cycle/ ./internal/runner/ && grep -q 'docflow unblock' skills/bsv-docflow-run/SKILL.md && grep -q 'Unblocked:' internal/agents/agents.go`
Traps: Skill sources live only in `skills/`; never edit the installed
copies under `~/.claude/skills/` or other agent directories.
Role: cheap
Commit: P041.1.3: re-check blocked condition after unblock

### [ ] P041.1.4 Review the unblock phase
Read:
- docs/design/cycle.md#цикл-юнита
- docs/design/runner.md:L40-70
Do:
- Review the diff named in the prompt's `## Phase diff` against `## Goal`,
  `## Constraints`, `## Decisions` and each unit's `Do:`. Edit nothing.
- A defect — reply `blocked` with `blocker: "plan"`; anything else worth
  noting — an `Unverified:` item.
Verify: `go test ./...`
Role: review
Commit: P041.1.4: review unblock phase

## Phase 2: Acceptance

### [ ] P041.2.1 Accept the human unblock
Read:
- internal/runner/terminal_run_test.go:L20-125
- internal/runner/run_test.go:L440-450
Do:
- One scenario per Goal statement, on a fixture plan with the fake agent,
  through the built CLI or the runner entry point the existing tests use:
  1. The agent replies `blocked`, `blocker: "human"` → run exits 3 and
     prints the `unblock:` hint; `docflow status` shows
     `(needs human)` and the hint; `docflow unblock <id> --note x` → `[~]`
     with `Unblocked: x`; the next `docflow run` prompts the same unit, the
     prompt contains `Unblocked: x` and the re-check rule, the agent
     replies `done` → `[x]` and a commit.
  2. Real TTY: run starts on that blocked unit, answer `y` and a note →
     the unit is resumed in the same run.
  3. A `plan` block: `docflow unblock` refuses, the plan file is unchanged.
- Close every `Unverified:` item of earlier units with a test, or keep it
  as `Unverified:` in this Handoff.
Verify: `go test ./...`
Traps: A failing scenario is fixed inside this unit when the fix is small;
otherwise reply `blocked` with the reason.
Depends: P041.1.1, P041.1.2, P041.1.3, P041.1.4
Role: acceptance
Commit: P041.2.1: accept human unblock
