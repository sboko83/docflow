---
id: P039
title: e2e and live checks
type: feature
status: active
created: 2026-10-25
permissions: auto
gate: go test ./...
executor: pi | strata/swift-1.5-iq3_s | high
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
cheap: pi | strata/swift-1.5-iq3_s | low; pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | high; claude | claude-haiku-5-5 | -
review: codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
acceptance: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
replan: codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
---
## Goal

The e2e scenarios revived in P040 pass on macOS;
`tools/check-plan-skill.sh` checks that a cold agent plans by
`bsv-docflow-plan` with the roster; the Codex runs recorded in RMG
confirm or refute escalation, the spinner and `--approve-for-me` after
`resume`.

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
- The personal roster marks strata for independent review and uses the
  local worker: `docflow roster show | grep -q 'executor: pi | strata/swift-1.5-iq3_s | high' && docflow roster show | grep -Eq 'check:.*strata'`
- RMG run logs exist: `test -d "$HOME/Developer/RMG/.git/docflow/run"`
- P040 is closed (it revives the e2e scenarios):
  `test -f docs/archive/P040-windows-support.md`

## Traps

- `go test ./...` takes ~3 min; run a package's tests or the harness while
  working and the full gate once at the end.
- `docflow` may be missing from `PATH`; build the checkout binary with
  `go build -o bin/docflow ./cmd/docflow` (`bin/` is gitignored).
- A unit does not commit and does not rewrite history; the runner commits
  with the unit's `Commit:` line.

## Decisions

- Codex runs in RMG (Xcode) stand for Gradle too — the mechanism, a write
  outside the repository through escalation, is the same — a separate
  Gradle run was rejected.
- Локальная модель выполняет обычную и дешёвую работу; Codex проверяет
  фазу и делает replan, Claude проводит финальную приёмку — маршруты и
  обоснование в [P043](../archive/P043-local-worker.md).
- --check-only runs only the checks and skips build, preconditions, fixture and the agent call — the unit's Verify command runs it repeatedly and must not spend a subscription session; rejected: re-calling the agent on --check-only
- The three header keys are reported as three ok/FAIL lines (header-executor/review/acceptance) instead of one — keeps failures diagnosable without rerunning the agent; rejected: a single combined header check
- Считать эскалацию по паре «отказ песочницы → повтор той же команды с exit 0» — в потоке нет события approvals, а повторы и коды выхода в логах есть — отвергнут учёт упоминаний эскалации в текстах агента (самоотчёт без исхода)
- Спиннер проверять реальным декодером: временный Go-пакет внутри модуля, вызывающий agents.LoadAgents + agentio.StreamEvent, затем удалён — отвергнута копия декодера на Python (реплику не отличить от расхождения с боевым путём)
- Лога RMG и их содержимое (пути, команды, сообщения) не переносятся в репозиторий: в JSON и в Handoff только счётчики и классы целей — отвергнуты цитаты из логов как доказательство

## Phase 1: Live agent checks

### [x] P039.1.1 Check cold planning
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
    `claude` on `PATH`; `docflow roster show` lists strata among `check`
    providers and prints `executor: pi | strata/swift-1.5-iq3_s | high`
    and `cheap: pi | strata/swift-1.5-iq3_s | low`.
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
Verify: `bash -n tools/check-plan-skill.sh && bash tools/check-plan-skill.sh --check-only`
Traps: the `claude` call takes 5–15 minutes and writes to `~/.claude`:
in the Codex sandbox request escalation for it, and run it in the
background with output to a file if the shell tool limits command time.
macOS has no `timeout`; kill the call from a background watchdog.
Depends: -
Commit: P039.1.1: add live plan-skill check
Handoff:
- tools/check-plan-skill.sh exists but is intentionally not in the gate or CI: a full run calls claude on the subscription (first live run took minutes, capped at 20 by a background watchdog since macOS has no timeout); --check-only reuses the work dir ${TMPDIR:-/tmp}/docflow-plan-skill-check and re-runs only the checks, so it needs a prior full run
- First live run passed all 10 checks with no skill fix: a cold claude session produced a 2-phase, 3-unit plan (ordinary unit, Role: review closing phase 1, Role: acceptance last) with roster routes, no replan: line, no commits — bsv-docflow-plan rules are unambiguous enough as written
- Fixture repo and agent.log stay in TMPDIR; no run content (code, paths, messages) was copied into this repository
- Unverified: Watchdog kill verified with a 5-second copy of the script (sleep 1200 → 5) against a hanging stub agent; the committed 20-minute value itself was not exercised live

### [x] P039.1.2 Проверить сохранённые прогоны Codex
Read:
- internal/agents/agents.go:L65-95
- internal/agentio/output.go
- docs/design/runner.md:L365-420
- docs/docflow.md
Do:
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
- Сохранить сводку скрипта вне репозитория в
  `${TMPDIR:-/tmp}/docflow-p039-codex-audit.json`: объект с ключами
  `escalation`, `resume`, `spinner`, каждый содержит `confirmed` (bool) и
  `evidence` (непустая строка без приватного содержимого логов).
Verify: `python3 -c 'import json,os; from pathlib import Path; d=json.loads((Path(os.environ.get("TMPDIR") or "/tmp")/"docflow-p039-codex-audit.json").read_text()); assert all(type(d[k]["confirmed"]) is bool and d[k]["evidence"] for k in ("escalation","resume","spinner"))' && git diff --check`
Traps: не вызывать модель и не запускать RMG заново; отсутствие события
в логе не доказывает отказ механизма, непроверенное уходит в Backlog.
Depends: P039.1.1
Role: cheap
Commit: P039.1.2: audit recorded Codex runs
Handoff:
- Codex JSON-поток не содержит событий approval/escalation: элемент command_execution несёт только id/command/aggregated_output/exit_code/status, потому эскалация измерима лишь как «отказ песочницы → повтор той же команды»; для живой проверки нужен новый прогон, а не эти логи
- Подтверждено только 5/5 успешных эскалаций для защищённого песочницей пути внутри workspace (каталог git); для записей вне корня репозитория (DerivedData, кэши) в логах нет ни одного отказа и ни одного подтверждения успеха, 10 отказов CoreSimulator эскалацией не сняты — это строка Backlog в docs/docflow.md
- resume-вызовы (4) сохраняют оба auto-аргумента (--approve-for-me и -c sandbox_workspace_write.network_access=true) в позиции BuildCommand и завершаются exit 0; отказов песочницы в них нет, так что P039.2.1 не может опираться на эти логи в вопросе эскалации после resume
- Спиннер: 42/44 лога дают activity через реальный декодер (agents.LoadAgents + agentio.StreamEvent); 2 лога с нулём — вызовы без item.started (один agent_message, ноль инструментов), то есть пустой спиннер у уunit-блока = отсутствие инструментов у агента, а не потеря декодера
- Логи RMG порождены старым Python-docflow (PermissionError из pathlib в .verify.log): числа описывают тот раннер, механизм (песочница + --approve-for-me + resume) общий с Go-версией; вывод о механизме переносим, счётчики — нет
- Артефакты аудита живут в TMPDIR и исчезнут: скрипт ${TMPDIR}docflow-p039-audit/audit.py, сводка ${TMPDIR}docflow-p039-codex-audit.json; Go-зонд декодера приходилось держать внутри модуля (internal/* импортируется только из него) и удалить — для повтора нужен такой же временный пакет
- Unverified: Эскалация при записях вне корня репозитория (DerivedData, кэши) — в сохранённых логах нет ни одного такого отказа; проверка отсутствует: нужен живой прогон Gradle/Xcode-сборки с наблюдаемыми отказами песочницы (ушла в Backlog)
- Unverified: Успешная эскалация внутри resumed-вызова — в 4 resumed-вызовах отказов песочницы не было; ни одна проверка из этих логов не воспроизводима (ушла в Backlog)
- Unverified: Запуск verify, чей результат прочитан из файла с меткой «escalated» (юнит P008.2.2), не записан ни в одном вызове агента: нельзя подтвердить, что эскалацию запросил агент, — сверки нет и получить её из логов нельзя
- Unverified: CI-допы за пределами гейта P039 (tools/test-install.sh и e2e parity) не запускались: gate плана — только go test ./...

### [ ] P039.1.3 Проверить работу локального исполнителя
Read:
- tools/check-plan-skill.sh
- docs/design/skills.md:L55-95
- docs/docflow.md
Do:
- Проверить `## Phase diff`: корректность скрипта, независимые review и
  acceptance в созданном плане, low/high локальных маршрутов, выводы
  аудита по Handoff и отсутствие приватных данных в diff.
- Ничего не редактировать. Дефект — блок `needs plan` с конкретным
  исправлением для Codex replan; без дефектов — передать результат дальше.
Verify: `go test ./...`
Traps: ревью не заменяет финальную приёмку и не закрывает непроверенное
утверждением из Handoff.
Depends: P039.1.1, P039.1.2
Role: review
Commit: P039.1.3: review local worker changes

## Phase 2: Acceptance

### [ ] P039.2.1 Accept the e2e and live checks
Read:
- tools/parity/README.md
- tools/check-plan-skill.sh
- .github/workflows/ci.yml
- docs/docflow.md
Do:
- Run `bash tools/parity/run.sh bin/docflow bin/docflow` on a fresh
  `go build -o bin/docflow ./cmd/docflow`: all 68 scenarios pass.
- Check that the e2e step in `.github/workflows/ci.yml` runs on every
  matrix OS (no `if:` excluding one).
- Run `bash tools/check-plan-skill.sh` in full once more (a fresh cold
  session) when unit P039.1.1 changed `skills/bsv-docflow-plan/SKILL.md`
  or left an `Unverified:` item; otherwise run `--check-only`.
- Close every `Unverified:` item from earlier Handoffs with a check, or
  keep it as `Unverified:` in this Handoff.
Verify: `go test ./... && go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow && bash tools/check-plan-skill.sh --check-only`
Traps: a failing check is fixed inside this unit when the fix is small;
otherwise block with the reason.
Depends: P039.1.1, P039.1.2, P039.1.3
Role: acceptance
Commit: P039.2.1: accept e2e and live checks
