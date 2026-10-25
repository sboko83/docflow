---
id: P038
title: runner backlog batch
type: feature
status: active
created: 2026-10-25
permissions: auto
gate: go test ./...
executor: codex | gpt-6-luna | max
fallback: pi | zai/glm-5.3-flash | high; claude | claude-sonnet-5-5 | high
cheap: codex | gpt-6-luna | high; pi | zai/glm-5.3-flash | high; claude | claude-haiku-5-5 | -
heavy: codex | gpt-6.1-sol | high; pi | zai/glm-5.3 | high; claude | claude-opus-5-5 | high
acceptance: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
---
## Goal

`install-dev.sh` switches skills over a release install, the gate loses its
60-second test, a route switch and a review unit get the context they now
recompute, the runner writes an event stream, and `docflow run --close` closes a ready plan
through an agent.

## Context

Every unit with a user-visible change adds its entry to `CHANGELOG.md`
under `## [Unreleased]` (bsv-changelog rules). `docs/` is in Russian;
code, comments and `CHANGELOG.md` are in English.

## Constraints

- Go standard library only; no new dependencies.
- Plan format stays `docflow: 3`: no new plan header keys or unit fields.
  New CLI flags are allowed.
- A changed decision is edited in its `docs/design/` topic in the same
  unit; the "why" of a new decision goes to `## Decisions` of this plan.
- Tests never touch the real `~/.docflow`, `~/.claude`, `~/.codex` or
  `~/.pi`: a temporary `HOME` and fake agent binaries, as the existing
  fixtures in `internal/runner/run_test.go` do.

## Traps

- `go test ./...` takes ~170 s before unit 1.1 lands; run a package's
  tests while working and the full gate once at the end.
- The installed CLI is the release `~/.docflow/bin/docflow`; `docflow`
  may be missing from `PATH`. Tests build their own binary
  (`runnerBinary` in `internal/runner/run_test.go`).
- A unit does not commit and does not rewrite history; the runner commits
  with the unit's `Commit:` line.

## Decisions

- `run --close` runs on the header `cheap:` chain, or on `executor:` +
  `fallback:` when the plan has no `cheap:` — closing is documentation
  work — the `acceptance:` chain is rejected as needlessly expensive.
- Перенёс проверку убийства detached grandchild в тест `CallAgent`, а runner acceptance оставил проверку timeout и возобновления: config override запрещён для плана с executor-маршрутом.
- Отбирать коммиты по буквальному префиксу subject и исключать commit review unit по его ID — так соседние номера фаз и replan не попадают в список — вместо `git log --grep`.
- Передавать список `repos:` в общий рендерер prompt — чтобы обычный prompt, runner и read-budget lint строили одинаковый phase diff — вместо отдельных реализаций.

## Phase 1: Tooling

### [x] P038.1.1 Relink release skills and shorten the timeout test
Read:
- internal/skills/skills.go:L416-480
- internal/skills/skills_test.go:L164-240
- tools/install-dev.sh:L120-185
- docs/design/skills.md:L55-80
- internal/runner/helpers.go:L36-50
- internal/runner/acceptance_fallback_test.go
- internal/agentio/process.go
Do:
- In `InstallSkills` link mode, treat a destination that is a symlink
  resolving under `$HOME/.docflow/` as a docflow-managed link: replace it
  with the link to the new source and print
  `<name>: relinked <destination> -> <source> (was <old target>)`. Any
  other existing destination stays a conflict with exit code 2.
- Add a `skills_test.go` case: a temporary `HOME` with
  `~/.docflow/current/skills/<name>` linked into the agent skills
  directory, `install-skills` from another source directory relinks it;
  a foreign symlink in the same run still fails with
  `exists and is not a link`.
- Make `tools/install-dev.sh` print the relinked lines it gets from
  `install-skills` unchanged; add no parallel linking logic to the script.
- Cut `TestAcceptanceTimeoutKillsDetachedGrandchild` below 15 s without a
  plan-format change. Criterion: if the fixture's agent config can set the
  built-in adapter's `timeout` in executor mode (see `agentCallTimeout`),
  drop `timeout: 60` from the fixture plan and set a 3-second profile
  timeout; otherwise move the detached-grandchild kill check into an
  `internal/agentio` test calling `CallAgent` with
  `CallOptions{Timeout: 3 * time.Second}`, and keep in the runner test
  only the resume-after-timeout part with the same short timeout.
- Update `docs/design/skills.md` where it describes the link conflict.
Verify: `/bin/bash -c 's=$(date +%s); go test -count=1 ./internal/skills ./internal/agentio && go test -count=1 -run TestAcceptanceTimeoutKillsDetachedGrandchild ./internal/runner && [ $(( $(date +%s) - s )) -lt 60 ]'`
Traps: `sameResolvedPath` resolves both sides, so a link into
`~/.docflow/current` (itself a symlink to a release directory) must be
matched by its resolved target being under the resolved `~/.docflow`.
Depends: -
Role: cheap
Commit: P038.1.1: relink release skills, shorten timeout test
Handoff:
- `tools/install-dev.sh` уже передаёт stdout команды `install-skills` напрямую, поэтому relink-сообщения проходят без изменений скрипта.

## Phase 2: Prompt context

### [x] P038.2.1 Carry work context across a route switch
Read:
- internal/runner/run.go:L440-460
- internal/runner/run.go:L560-760
- internal/agentio/output.go:L238-340
- internal/runner/fallback_test.go
- docs/design/runner.md:L50-100
Do:
- Extend `previousExecutorAttempt` with two blocks after the refusal
  sentence: `### Working tree` — `git diff --stat HEAD` plus untracked
  paths from `git status --porcelain`, at most 40 lines, with a
  `… N more` line when clipped; `### Last messages` — up to the last three
  assistant text messages of the refused call, each clipped with
  `agentio.Clip` to 600 characters, the message carrying the refusal
  excluded.
- Take the messages from the refused call's stdout through
  `StreamEvent`/`AgentText` for the agent's output format; a format with
  no extractable messages omits `### Last messages`. Never include the
  refusal text itself.
- Add a `fallback_test.go` case: the first fake agent writes a file,
  prints an assistant message, then a limit refusal; the second call's
  prompt contains `## Previous attempt`, the file name from the diff stat
  and the message, and does not contain the refusal text.
- Update the `## Previous attempt` paragraph in `docs/design/runner.md`.
Verify: `go test -count=1 ./internal/runner ./internal/agentio`
Depends: -
Commit: P038.2.1: pass diff and last messages on route switch
Handoff:
- Fallback and replan prompts share the same context block; its Git snapshot covers the root repository, not configured nested repositories.
- Package tests and full `docflow verify P038.2.1` passed. The runner can proceed with `done` and `commit`.

### [x] P038.2.2 Put the phase diff base into the review prompt
Read:
- internal/cycle/commands.go:L1-95
- internal/cycle/cycle_test.go:L380-420
- internal/gitx/status.go
- docs/design/cycle.md:L50-80
- skills/bsv-docflow-plan/SKILL.md:L243-262
- skills/bsv-docflow-run/SKILL.md:L120-130
Do:
- In `PromptText`, for a unit with `Role: review`, append a
  `## Phase diff` section: the base is the parent of the oldest commit
  whose subject starts with `<plan id>.<phase>.` (the review unit's own
  id excluded); print `Base: <sha>`, the commit subjects from
  `git log --reverse --format='%h %s' <base>..HEAD` filtered by that
  prefix, and the command `git diff <base> HEAD`.
- No commit with the prefix → the section says
  `no commits with prefix <plan id>.<phase>.` and nothing else.
- With `repos:` in `docs/docflow.md`, compute the section per repository
  of the project, each block headed by the repository path.
- Tests in `cycle_test.go`: a phase with two commits gives the right base
  and both subjects; a phase without commits gives the empty-phase line;
  a non-review unit has no `## Phase diff`.
- Replace the `git log --grep` instruction in the Review template of
  `skills/bsv-docflow-plan/SKILL.md` with "review the diff named in the
  prompt's `## Phase diff`"; mention the section in the `Role: review`
  paragraph of `skills/bsv-docflow-run/SKILL.md`.
- Add `## Phase diff` to the prompt contents list in
  `docs/design/cycle.md`.
Verify: `go test -count=1 ./internal/cycle && grep -q 'Phase diff' skills/bsv-docflow-plan/SKILL.md && grep -q 'Phase diff' docs/design/cycle.md`
Traps: the subject prefix match must be anchored (`P038.2.` must not
match `P038.21.` or a `P038: replan` commit).
Depends: -
Commit: P038.2.2: add phase diff base to review prompt
Handoff:
- Новые прямые вызовы `cycle.PromptText` должны передавать вложенные репозитории из `docs/docflow.md`, чтобы review prompt и оценка read budget учитывали одинаковый контекст.

## Phase 3: Event stream and close

### [ ] P038.3.1 Write the runner event stream
Read:
- internal/runner/run.go:L91-460
- internal/runner/run.go:L451-935
- internal/runner/replan.go
- internal/runner/run_test.go:L27-215
- docs/design/runner.md:L50-140
- docs/design/runner.md:L285-345
Do:
- Add `internal/runner/events.go`: an appender to
  `<git-dir>/docflow/run/<run-id>.events.jsonl`, one JSON object per
  line, written and flushed per event. `<run-id>` is the run start in UTC
  as `20261025T101500Z`, with `-2`, `-3`… when the file exists.
- Every record carries `v: 1`, `ts` (epoch seconds, float), `run`
  (run id) and `event`; `unit` where it applies.
- Events: `run_start` (plan, unit count), `unit_start` (unit, route),
  `attempt` (unit, n, route, log path), `quota` (name, windows),
  `route_switch` (unit, from, to, refusal kind), `replan` (unit, route,
  result), `verify` (unit, exit code), `commit` (unit, sha), `unit_end`
  (unit, status, seconds, cost when known), `run_end` (reason, exit
  code).
- A write failure prints one warning line and disables the stream for
  the rest of the run; it never changes the run's exit code.
- `run --dry-run` writes no stream.
- Tests: a run with one successful unit and one route switch produces the
  expected event names in order and valid JSON lines; an unwritable run
  directory warns once and the run still succeeds.
- Document the file, the record schema and the `v` promise in
  `docs/design/runner.md`.
Verify: `go test -count=1 ./internal/runner`
Traps: the stream sits next to the call logs `<id>.<n>.out` in the same
directory; do not change their naming.
Depends: -
Role: heavy
Commit: P038.3.1: write runner event stream

### [ ] P038.3.2 Close a ready plan with run --close
Read:
- cmd/docflow/main.go:L125-145
- cmd/docflow/main.go:L295-310
- internal/runner/run.go:L91-460
- internal/runner/chain.go
- internal/runner/replan.go
- internal/cycle/lifecycle.go:L339-420
- skills/bsv-docflow-close/SKILL.md
- internal/runner/events.go
- docs/design/runner.md:L125-140
- docs/usage.md:L581-598
Do:
- Add the `run` flag `--close`: when the plan is `ready to close` — at
  the end of the run or already at its start — the runner calls an agent
  with a prompt to close the plan by the `bsv-docflow-close` skill: plan
  id, the instruction to run `docflow close <id>` and fill the archive
  entry, and the same result-JSON contract as units.
- Route: the header `cheap:` chain when present, otherwise `executor:` +
  `fallback:`; refusals and fallback behave as for units. The call log is
  `<id>.close<N>.out`; the event stream gets a `close` event.
- After the call the runner checks: the plan file is gone from
  `docs/plans/`, `docs/archive/<id>-*.md` exists, `docflow lint` is clean,
  and changed paths are within `docs/`, `CHANGELOG.md`, `README.md`,
  `README.ru.md`. All hold → commit `<id>: close plan`. Any fails → exit
  code 2 with the failed checks listed; the tree is left as is, nothing
  is rolled back.
- Without `--close` the run ends at `ready to close` as today.
  `run --dry-run --close` prints the close route as one more line.
- Tests: a fake agent that runs the close and writes the archive sections
  → the runner commits `P001: close plan`; a fake agent that edits a
  source file → exit 2 and the file stays modified; a plan without
  `cheap:` uses the executor route.
- Update `docs/design/runner.md` (the "ready to close" end of the run),
  `docs/usage.md#закрытие`, and `## Decisions` of this plan if the check
  list changes.
Verify: `go test -count=1 ./internal/runner ./cmd/docflow`
Traps: the close agent needs `docflow` on `PATH`: check the environment
`agentio.CallAgent` passes to agents, and when the running binary's
directory is not on its `PATH`, prepend it for the close call. The
runner never runs `docflow close` itself — the agent does, by the skill.
Depends: P038.3.1
Role: heavy
Commit: P038.3.2: add run --close

## Phase 4: Acceptance

### [ ] P038.4.1 Accept the batch end to end
Read:
- internal/runner/run_test.go:L27-215
- internal/runner/acceptance_fallback_test.go
- internal/runner/role_acceptance_test.go
- tools/test-install.sh
Do:
- Add `internal/runner/batch_acceptance_test.go` with one CLI scenario
  per Goal statement, on the fake-agent fixture and the plan format the
  runner reads:
  - a route switch: the fallback agent's prompt carries the diff stat and
    the last message of the refused agent;
  - a review unit's prompt from `docflow prompt` carries `## Phase diff`
    with the base before the phase's first commit;
  - the run writes `<run-id>.events.jsonl` with `run_start` … `run_end`
    and a `route_switch` between them;
  - `run --close` on a plan with every unit done ends with the plan in
    `docs/archive/` and the last commit `P001: close plan`.
- Extend `tools/test-install.sh`: over the local release layout it
  already installs, run `tools/install-dev.sh` with the same temporary
  `HOME` and check the skill links now point into the checkout.
- Check the gate time: `TestAcceptanceTimeoutKillsDetachedGrandchild`
  under 15 s in `go test -v ./internal/runner` output.
- Close every `Unverified:` item from the Handoffs of earlier units with
  a test, or keep it as `Unverified:` in this Handoff.
Verify: `go test -count=1 ./... && bash tools/test-install.sh`
Traps: a failing scenario is fixed inside this unit when the fix is
small; otherwise block with the reason.
Depends: P038.1.1, P038.2.1, P038.2.2, P038.3.1, P038.3.2
Role: acceptance
Commit: P038.4.1: accept runner backlog batch
