---
id: P039
title: e2e and live checks
type: feature
status: draft
created: 2026-10-25
permissions: auto
gate: go test ./...
executor: codex | gpt-6-luna | max
fallback: pi | zai/glm-5.3-flash | high; claude | claude-sonnet-5-5 | high
acceptance: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
---
## Goal

The 68 end-to-end scenarios in `tools/parity/` pass against the current
binary and run in CI on Linux and macOS; `tools/check-plan-skill.sh` checks
that a cold agent plans by `bsv-docflow-plan` with the roster; the Codex
runs recorded in RMG confirm or refute escalation, the spinner and
`--approve-for-me` after `resume`.

## Context

## Constraints

- Go standard library only; shell scripts target macOS `/bin/bash` 3.2
  (no `declare -A`, `mapfile`, `${var,,}`), checked with `bash -n`.
- Plan format stays `docflow: 3`; no Go behavior change to make a scenario
  pass, unless `docs/design/` says the scenario's expectation is right.
- Nothing from RMG logs (code, paths, messages) goes into the repository:
  only counts and conclusions, in the Handoff.
- `tools/check-plan-skill.sh` is not part of the gate or CI: it calls a
  live agent on the user's subscription.

## Prerequisites

- `claude` is installed and logged in on the subscription; one planning
  session per full run of `tools/check-plan-skill.sh`: `command -v claude`
- The personal roster has the `check` provider zai and pi/zai as the
  default executor: `"$HOME/.docflow/bin/docflow" roster show | grep -q 'check: zai'`
- RMG run logs exist: `test -d "$HOME/Developer/RMG/.git/docflow/run"`

## Traps

- `go test ./...` takes ~3 min; run a package's tests or the harness while
  working and the full gate once at the end.
- `docflow` may be missing from `PATH`; build the checkout binary with
  `go build -o bin/docflow ./cmd/docflow` (`bin/` is gitignored).
- A unit does not commit and does not rewrite history; the runner commits
  with the unit's `Commit:` line.

## Decisions

- The harness keeps its name and layout and runs one binary against
  itself — the Python version it compared against is gone, the scenarios
  remain the only end-to-end runs of the CLI — deleting it was rejected.
- Codex runs in RMG (Xcode) stand for Gradle too — the mechanism, a write
  outside the repository through escalation, is the same — a separate
  Gradle run was rejected.

## Phase 1: End-to-end scenarios

### [ ] P039.1.1 Revive the e2e scenarios and run them in CI
Read:
- tools/parity/README.md
- tools/parity/run.sh
- tools/parity/driver.sh
- tools/parity/e2e.sh
- tools/parity/fixtures/repos-docflow.md
- tools/parity/scenarios/commit_after_other_binary_verify.sh
- docs/design/format.md:L1-120
- docs/design/format.md:L257-300
- .github/workflows/ci.yml
- AGENTS.md
Do:
- Make `tools/parity/run.sh` run every scenario instead of stopping at the
  first failure: print `parity: FAIL <name> (<side> scenario)` per failure,
  a final `parity: N of M failed` line, and exit 1 when any failed; exit
  codes 0 and 2 keep their meaning.
- Build `bin/docflow` and run
  `bash tools/parity/run.sh bin/docflow bin/docflow`; 29 of 68 scenarios
  failed before this unit, mostly plans and fixtures written in
  `docflow: 2`.
- Fix each failing scenario and fixture to the current format and the
  behavior described in `docs/design/`: `docflow: 3` headers, unit fields
  and command output as the current binary documents them.
- A scenario whose subject is the legacy format or migration (names with
  `migrate` or `legacy`) keeps its `docflow: 2` input and checks the
  current migration behavior instead.
- A failure where the binary contradicts `docs/design/` is a bug, not a
  scenario fix: block with `blocker: "plan"` naming the scenario and the
  design line.
- Rewrite the purpose paragraph of `tools/parity/README.md`: the harness
  runs end-to-end scenarios of one binary (`run.sh bin/docflow
  bin/docflow`) or compares two builds; drop mentions of the Python
  version.
- In `.github/workflows/ci.yml` add to the `test` job, after `go test`,
  the steps `go build -o bin/docflow ./cmd/docflow` and
  `bash tools/parity/run.sh bin/docflow bin/docflow`, for both matrix OS.
- In `AGENTS.md` extend the sentence "CI also runs `bash
  tools/test-install.sh`" with the e2e run command.
Verify: `go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow && grep -q 'tools/parity/run.sh bin/docflow bin/docflow' .github/workflows/ci.yml`
Traps: scenarios run with `set -euo pipefail` and system/user git config
disabled; a scenario that expects a non-zero exit code writes
`cmd || [ "$?" -eq N ]`, as `README.md` shows — keep that form. Run one
scenario alone with `PARITY_SKIP` listing all the others.
Commit: P039.1.1: revive e2e scenarios in CI

## Phase 2: Live agent checks

### [ ] P039.2.1 Check cold planning and Codex runs
Read:
- skills/bsv-docflow-plan/SKILL.md
- internal/agents/agents.go:L40-110
- docs/design/runner.md:L445-470
- docs/design/skills.md:L55-90
- tools/test-install.sh:L1-40
Do:
- Add `tools/check-plan-skill.sh [--check-only]`:
  - Work directory `${TMPDIR:-/tmp}/docflow-plan-skill-check`, removed and
    recreated on a full run; `--check-only` reuses it and skips steps up
    to the agent call.
  - Build the checkout binary into `<work>/bin/docflow` and put
    `<work>/bin` first in `PATH`. Preconditions, exit 2 when unmet:
    `claude` on `PATH`; `docflow roster show` prints `check: zai` and an
    `executor: pi | zai/` line.
  - Fixture `<work>/repo`: a git repository with a tiny Go module
    (`go.mod`, `main.go`, `main_test.go`), `README.md`,
    `docs/design/README.md`, and `docs/docflow.md` in `docflow: 3` with an
    empty Queue and one Backlog line: add a `greet` subcommand with
    `--name`, `--shout` and `--format json`, a README usage section and a
    test per flag. One initial commit.
  - Call `claude -p --model claude-sonnet-5-5 --permission-mode auto
    --permission-prompts none` in the fixture with a prompt: read and
    follow `<checkout>/skills/bsv-docflow-plan/SKILL.md` (absolute path)
    and plan the Backlog line; the survey is answered — routes from the
    roster's default profile, `permissions: auto`, auto-replan off, no
    prerequisites; ask nothing, do not activate, do not commit. Output to
    `<work>/agent.log`; kill the call after 20 minutes.
  - Checks, one `ok <name>` or `FAIL <name>` line each, exit 1 when any
    failed: exactly one file in `docs/plans/`; `docflow lint` exits 0;
    `status: draft`; the header has `executor:`, `review:` and
    `acceptance:`; no `replan:` line; `docflow run --dry-run --plan <id>
    --all` exits 0; the last unit has `Role: acceptance`; every phase
    holding a unit without `Role: review` or `Role: acceptance` ends with
    a `Role: review` unit.
- Run the script in full. A FAIL the agent caused by following an
  ambiguous or missing rule → fix `skills/bsv-docflow-plan/SKILL.md` and
  rerun; at most two reruns, a FAIL left after them → `Unverified:` naming
  the check and the plan line. A FAIL caused by the script → fix the
  script.
- Add one line to `docs/design/skills.md` next to the skill installation:
  `tools/check-plan-skill.sh` is the live check of the planning skill,
  outside CI because it spends a subscription session.
- Codex in RMG, with a throwaway script outside the repository over
  `$HOME/Developer/RMG/.git/docflow/run/*.out` (Codex logs hold a
  `"thread.started"` line):
  - escalation: count commands that requested escalated permissions and
    their outcomes, separately for writes outside the repository
    (DerivedData, caches);
  - resume: find units with Codex calls numbered above 1, check from
    `internal/agents/agents.go` that `resume_args` keep the `auto`
    arguments (`--approve-for-me`), and whether a resumed call escalated
    successfully;
  - spinner: feed each Codex log through the stream decoder the runner's
    progress line uses and count logs that yield at least one activity
    line.
- Each of the three confirmed → nothing to add. Anything not confirmed →
  add one Backlog line to `docs/docflow.md` starting with `Codex:` that
  names only what stayed unconfirmed, ending with
  `([P015](archive/P015-multi-agent-runner.md))`.
Verify: `bash -n tools/check-plan-skill.sh && bash tools/check-plan-skill.sh --check-only`
Traps: the `claude` call takes 5–15 minutes and writes to `~/.claude`:
in the Codex sandbox request escalation for it, and run it in the
background with output to a file if the shell tool limits command time.
macOS has no `timeout`; kill the call from a background watchdog.
Depends: -
Commit: P039.2.1: add live plan-skill check

## Phase 3: Acceptance

### [ ] P039.3.1 Accept the e2e and live checks
Read:
- tools/parity/README.md
- tools/check-plan-skill.sh
- .github/workflows/ci.yml
- docs/docflow.md
Do:
- Run `bash tools/parity/run.sh bin/docflow bin/docflow` on a fresh
  `go build -o bin/docflow ./cmd/docflow`: all 68 scenarios pass.
- Check that the e2e step in `.github/workflows/ci.yml` runs on both
  matrix OS (no `if:` excluding one).
- Run `bash tools/check-plan-skill.sh` in full once more (a fresh cold
  session) when unit P039.2.1 changed `skills/bsv-docflow-plan/SKILL.md`
  or left an `Unverified:` item; otherwise run `--check-only`.
- Close every `Unverified:` item from earlier Handoffs with a check, or
  keep it as `Unverified:` in this Handoff.
Verify: `go test ./... && go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow && bash tools/check-plan-skill.sh --check-only`
Traps: a failing check is fixed inside this unit when the fix is small;
otherwise block with the reason.
Depends: P039.1.1, P039.2.1
Role: acceptance
Commit: P039.3.1: accept e2e and live checks
