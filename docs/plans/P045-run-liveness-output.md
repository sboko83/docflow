---
id: P045
title: Agent liveness and run output
type: feature
status: active
created: 2026-10-26
permissions: auto
gate: go test ./...
executor: pi | strata/swift-1.5-iq3_s | high
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
heavy: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
review: codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
acceptance: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
---
## Goal

`docflow run` tells a working agent from a hung one: an agent silent for
the idle limit with no tool running is stopped as stalled, the hard time
cap rises to 2 h and reaching it while the agent is active reads as a
resumed session, not a failure; the spinner shows turns and the age of
the last event, and the agent log is written while the call runs. A unit
block shows its start and end time, the quota sits inside the block, and
the quota display is configurable: used or left, colour thresholds
50/80, reset time shown by default and switchable off.

## Context

- docs/design/runner.md:L297-330

## Constraints

- Go standard library only.
- Plan format stays `docflow: 3`; the plan header key `timeout:` keeps its
  meaning and range (60–86400) and overrides the profile cap.
- The agent call stays one subprocess per attempt; a stopped attempt is
  resumed by the existing retry path (session resume with feedback).
- Text output (pipe, `TERM=dumb`) stays one line per event; frame changes
  apply to the TTY layout only.
- Language: code, comments, `README.md`, `CHANGELOG.md` in English;
  `docs/` in Russian.
- A unit does not commit and does not rewrite history; the runner commits
  with the unit's `Commit:` line.

## Traps

- `go test ./...` takes ~3 min; run a package's tests while working and the
  full gate once at the end.
- `docflow` may be missing from `PATH`; build the checkout binary with
  `go build -o bin/docflow ./cmd/docflow` (`bin/` is gitignored).
- Event shapes differ per stream. Pi emits `tool_execution_start` /
  `tool_execution_end` and streams `thinking_delta`; Codex emits
  `item.started` / `item.completed`; Claude (`stream-json` without partial
  messages) is silent while the model generates and marks a tool with a
  `tool_use` block in an `assistant` event and its `tool_result` in a
  `user` event. Confirm on real logs:
  `grep -o '"type":"[a-z_.]*"' .git/docflow/run/*.out | sort | uniq -c`.

## Decisions

- Liveness comes from the agent's own event stream, which the runner
  already parses line by line — rejected: CPU or network probes of the
  process group (platform-specific, and a slow model API looks the same as
  a hang).
- A tool in flight is never idle; it is bounded only by the hard cap — a
  20-minute test run emits no events — rejected: one idle limit for
  everything.
- Default idle limit 20 min, hard cap 2 h for all built-in profiles —
  Claude and Codex are silent for a whole model turn, and a long thinking
  turn runs over 10 min; a GLM unit in another project was active for the whole 60 min it got
  (128 turns) — rejected: idle 5 min (false stalls), cap 1 h (cuts active
  work).
- The quota display mode lives in `[display]` of the agents config — the
  runner reads that file; the roster is read only while planning —
  rejected: a key in `roster.toml`, a CLI flag per run.
- Codex: ход считается по turn.started, а не по запуску элемента — в реальных логах есть пары turn.started/completed, и один ход может выполнить несколько элементов — отклонено: считать ходом не-молчаливый item.started (это действия, а не ходы)
- Claude: StructuredOutput никогда не считается инструментом, а событие result очищает инструменты — tool_use без парного tool_result держал бы завершившийся вызов «активным» вечно и блокировал бы проверку простоя — отклонено: считать каждый блок tool_use
- LivenessState.Lines = число наблюдаемых строк потока — нужно, чтобы отличить активного агента от молчавшего весь вызов, потому что NewLiveness сеет LastLine временем старта вызова — отклонено: сравнивать только свежесть LastLine (агент 'sleep 5' под капом 1 с читался бы активной работой, и существующие тесты 'timed out after N s' переехали бы в жёлтую линию)
- Кап при активной работе печатается через новый Progress.resume (жёлтый, style "") — отклонено: p.line(..., "yellow"), который внутри блока юнита навешивает '!' и превращает продолженную попытку в проблему
- Инструмент называется в cap-строке только пока инструмент в работе (capActivity) — активность потока переживает инструмент, который её породил (tool_result обновляет событие, Tools=0, activity осталась) — отклонено: называть последнюю активность всегда ('during Read x' для уже завершившегося инструмента)
- Подсказка — набор строк в итоговой сводке прогона (runTotals.noteCap, дедуп по тексту) — отклонено: detail-строка под --verbose (совет человеку, читающему конец прогона) и строка в блоке юнита (совет относится к прогону целиком)
- Юнит P045.1.2 трогает close.go только ради текста зависания и спиннера: строка 'close agent timed out' сохранена без variant '↻ attempt N', потому что текст из Do называет номера попыток, а у close их нумерации в выводе нет — отклонено: обобщать формулировку на close (ревью решит, если это дефект)
- В cap-строке счётчик ходов просклоняён ('1 turn') — отклонено: дословный шаблон плана '(<turns> turns)' (в кодовой базе счётчики уже склоняются через plural)

## Phase 1: Liveness

### [x] P045.1.1 Watch agent liveness
Read:
- internal/agentio/process.go:L20-66
- internal/agentio/process.go:L100-230
- internal/agentio/output.go:L245-370
- internal/agents/agents.go:L37-130
- internal/agents/agents.go:L332-390
- internal/runner/helpers.go:L39-50
- internal/agentio/testdata/fake_agent.sh
- internal/agentio/process_test.go:L175-230
Do:
- Add `agentio.Liveness` (mutex-guarded): time of the last stdout line,
  number of turns, the tools in flight with the start time of the oldest.
  The `eventWriter` updates it for every line; the stream (`claude`,
  `codex`, `pi` from the profile's `stream`) decides turns and tool
  start/end, per the event shapes in this plan's Traps.
- `CallOptions` gets `Idle time.Duration` and `Liveness *Liveness`.
  `CallAgent` checks every few seconds: no line for `Idle` and no tool in
  flight → kill the process group as on timeout and set
  `AgentRun.Stalled = true`. `Idle == 0` disables the check.
- Write the `.out` log while the call runs: `$ <argv>` and `--- stdout`
  first, stdout lines appended as they arrive, then `--- stderr`, stderr
  and `--- exit N` with ` (timed out)` or ` (stalled)` at the end. Only
  the `.out` file name is read elsewhere (`internal/runner/helpers.go`), not
  its layout.
- Built-in profiles in `DefaultAgents`: `timeout = 7200` for claude, codex
  and pi; new key `idle_timeout = 1200`. Register `idle_timeout` as an int
  in `AgentKeys`; `checkAgent` accepts 0 (off) or 5–86400.
- `internal/runner/helpers.go`: `agentIdleTimeout(agent)` next to
  `agentCallTimeout`; pass `Idle` and a fresh `Liveness` into every
  `CallAgent` call in `run.go`, `replan.go` and `close.go`.
- Tests with the fake agent: silent past `Idle` → `Stalled` and the group
  killed; silent with a tool in flight → not stalled; the `.out` file holds
  stdout before the agent exits; turns and in-flight tools for one sample
  line of each stream.
Verify: `go test ./internal/agentio/ ./internal/agents/ ./internal/runner/`
Traps: `fake_agent.sh` is shared by many tests; add new modes, do not change
existing ones.
Depends: -
Role: heavy
Commit: P045.1.1: watch agent liveness during calls
Handoff:
- Семантика ходов/инструментов на поток: claude — ход = событие assistant, инструмент = блок tool_use (StructuredOutput не считается) до парного tool_result в user, result очищает всё; codex — ход = turn.started (пары turn.started/completed есть в реальных .out-логах), инструмент = не-молчаливый item.started по item.id до item.completed; pi — ход = message_end с role=assistant, инструмент = tool_execution_start/end по toolCallId. Не-JSON строки тоже обновляют время последнего события
- Спиннер P045.1.2 берёт данные из Liveness.State() (LastLine, Turns, Tools, OldestTool) — объект создаётся свежим рядом с каждым вызовом CallAgent в run.go/replan.go/close.go и живёт, пока идёт вызов; выборка простоя — тикер agentio.LivenessCheck = 2 c, kill может наступить до 2 c после превышения лимита
- Раскладка .out изменилась: $ argv → --- stdout (строки пишутся по мере прихода) → --- stderr → --- exit N в конце с суффиксом (timed out) или (stalled); строка exit переехала в конец файла
- Шаг с доками (runner.md «Резервные маршруты и таймаут», usage.md — ключ idle_timeout, CHANGELOG [Unreleased]) остался юниту P045.1.2 по плану
- Unverified: Поведение на реальных зависших агентах (claude/codex/pi) — формы событий подтверждены на реальных .out-логах репозитория, а kill и подавление инструментом проверены только на fake agent; не хватает наблюдаемого прогона с живым профилем

### [x] P045.1.2 Report stalls and caps in the run
Read:
- internal/runner/run.go:L740-760
- internal/runner/run.go:L915-930
- internal/runner/replan.go:L300-312
- internal/runner/replan.go:L440-450
- internal/runner/close.go:L315-320
- internal/runner/close.go:L395-402
- internal/runner/progress.go:L392-430
- internal/runner/progress.go:L751-812
- docs/usage.md:L426-498
Do:
- A stalled attempt: problem text
  `agent silent for <idle>, no tool running — stopped`; it is retried
  like a timeout (session resume, feedback "the previous attempt
  stalled").
- A timed-out attempt whose last event is under 60 s old: a yellow line
  `↻ attempt N: time cap <cap> reached while active (<turns> turns); resuming the session`
  instead of the `!` problem line; a timed-out attempt with a tool in
  flight names it: `time cap reached during <activity>`.
- Spinner (TTY): `<id> <elapsed> · turn <n> · <age> ago`, and
  `· tool <duration>` while a tool runs; read from the call's `Liveness`.
- Run summary: when an attempt hit the cap while active, add
  `hint: <id> needs more than <cap> on <route>: split the unit or set timeout: in the plan header`.
- Tests for the three messages, the spinner text and the hint.
- Docs: `docs/design/runner.md` section «Резервные маршруты и таймаут» —
  idle limit, cap, the two outcomes; `docs/usage.md` profile keys
  (`timeout`, `idle_timeout`) and how to watch an agent live
  (`tail -f <git-dir>/docflow/run/<unit>.<attempt>.out`); Russian.
  `CHANGELOG.md` `## [Unreleased]` → `### Added` and `### Changed`.
Verify: `go test ./internal/runner/`
Depends: P045.1.1
Commit: P045.1.2: report stalled agents and active caps
Handoff:
- Progress.summary теперь принимает 4-й аргумент hints []string: в TTY строки 'hint: …' печатаются внутри рамки после строк квоты (префикс '│ ', жёлтый, без пустой строки-разделителя), в текстовом режиме — отдельными строками после строки сводки; P045.2.1 перерабатывает рамку сводки (квота внутри кадра) и обязан сохранить строку hint внутри рамки
- Progress.resume(text) — жёлтая линия без '!' внутри блока: lineLocked навешивает '!' на стили red/yellow, поэтому resume красит текст сам (p.paint) и передаёт style=""; любая линия «это не проблема, продолжаем» идёт через неё
- Признак живости для отчёта — agentio.LivenessState.Lines: NewLiveness сеет LastLine временем старта, поэтому activeAt проверяет сначала Lines>0, потом окно activeEventWindow (60 с) из helpers.go; Tools>0 даёт активность независимо от возраста последнего события
- runTotals получил hints + noteCap/capHints/lastActivity: заметки дедуплицируются по полному тексту и собираются из run, replan и cap-отчётов; P045.2.1 трогает сводку — не потерять их
- Тексты отчётов лежат в internal/runner/helpers.go (shortDuration, stallProblem, stalledFeedback, capProblem, capActivity, capReport, capHint, livenessLabel) — run.go, replan.go и close.go вызывают одни и те же функции; счётчик ходов просклоняён через plural() ('1 turn'), лимиты форматирует shortDuration ('2h', '20m', '45s'), а elapsed/age спиннера — agentio.Duration ('2m05s')
- Unverified: спиннер в настоящем PTY-прогоне с живым профилем — приёмочный список P045.3.1 (сценарий 5) покрывает блок юнита и квоту, но не текст спиннера; решение о PTY-сценарии для спиннера — за ревью P045.1.3 или close
- Unverified: Строка зависания и подпись спиннера в вызове close: нет ни одного теста на problem-строки close (ни на 'close agent timed out', ни на новую 'agent silent for …'), Verify не прогоняет close со зависшим агентом
- Unverified: Линия спиннера в настоящем PTY-кадре: обрезка по ширине терминала с суффиксом '· tool <duration>' проверена только как текстовая функция livenessLabel, Verify не запускает PTY-сценарий со спиннером

### [ ] P045.1.3 Review the liveness phase
Read:
- docs/design/runner.md:L297-330
Do:
- Review the diff named in the prompt's `## Phase diff` against `## Goal`,
  `## Constraints`, `## Decisions` and each unit's `Do:`. Check that the
  idle watchdog cannot fire while a tool is in flight and that the kill
  path is the same as for a timeout. Edit nothing.
- A defect — reply `blocked` with `blocker: "plan"`; observations without
  a defect — plain Handoff items.
Verify: `go test ./...`
Role: review
Commit: P045.1.3: review liveness phase

## Phase 2: Output

### [ ] P045.2.1 Frame times and quota display
Read:
- internal/runner/progress.go:L255-300
- internal/runner/progress.go:L327-445
- internal/runner/progress.go:L503-680
- internal/runner/run.go:L55-90
- internal/agents/agents.go:L239-330
- internal/runner/progress_test.go:L197-260
- docs/design/runner.md:L380-420
- docs/usage.md:L426-498
Do:
- Unit and replan blocks: header `┌─ HH:MM · <id>: <title>` with the
  block's start time (dim); the quota goes inside the frame — a `│` line,
  `│  Quota · used|left  <Agent>`, bar rows prefixed `│  `; the last line
  `└─ HH:MM · <duration>` replaces the `Time:` line. Text output:
  `<id> time: <duration> (HH:MM:SS–HH:MM:SS)`.
- `[display]` table in the agents config: `quota = "used" | "left"`
  (default `used`), `resets = true | false` (default `true`); any other
  key or value is a config error. `LoadAgents` accepts the table next to
  `tiers` and `agents`.
- Colours: `used` — under 50% green, 50–79% yellow, 80% and over red.
  `left` shows 100 − used and the mirror: over 50% green, 21–50% yellow,
  20% and under red. The bar and the delta follow the mode (`left` shows
  `(-2)` where `used` shows `(+2)`).
- Reset time inline after the percent and delta, dim: `↻HH:MM` for the 5h
  window, `↻Mon HH:MM` for longer ones; the separate reset lines and their
  `--verbose` gate go; `resets = false` hides them. The start block and the
  summary use the same mode.
- Tests: frame lines, both modes, thresholds at 49/50/79/80 used, inline
  reset on and off, a config error for an unknown value.
- Docs: `docs/design/runner.md` paragraphs about TTY blocks and quota;
  `docs/usage.md` `[display]` keys; Russian. `CHANGELOG.md`.
Verify: `go test ./internal/runner/ ./internal/agents/`
Traps: The start block uses rounded corners (`╭─`/`╰─`) and has no quota
inside; keep it. Narrow terminals (`width() < 60`) shorten the bar to 6
cells; the inline reset must still wrap by the existing row rule.
Depends: P045.1.3
Commit: P045.2.1: show block times and configurable quota

### [ ] P045.2.2 Review the output phase
Read:
- docs/design/runner.md:L380-420
Do:
- Review the diff named in the prompt's `## Phase diff` against `## Goal`,
  `## Constraints`, `## Decisions` and each unit's `Do:`. Edit nothing.
- A defect — reply `blocked` with `blocker: "plan"`; observations without
  a defect — plain Handoff items.
Verify: `go test ./...`
Role: review
Commit: P045.2.2: review output phase

## Phase 3: Acceptance

### [ ] P045.3.1 Accept liveness and run output
Read:
- internal/runner/pty_darwin_test.go
- internal/runner/progress_test.go:L197-260
- tools/parity/README.md:L1-60
Do:
- One scenario per Goal statement through the runner entry point the
  existing tests use, with the fake agent and a config in a temp
  `DOCFLOW_AGENTS` file:
  1. `idle_timeout = 5`, the agent prints one event and sleeps 10 s → the
     attempt stops as stalled, attempt 2 resumes the session.
  2. The agent starts a tool event and sleeps past `idle_timeout` → not
     stalled.
  3. The agent keeps printing events past a short `timeout` → the yellow
     `↻ … reached while active` line and the summary hint.
  4. The `.out` log holds stdout lines while the agent still runs.
  5. Real TTY: the unit block header carries the start time, the quota
     sits inside the frame, the last line is `└─ HH:MM · <duration>`;
     `[display] quota = "left"` prints `Quota · left` and 100 − used;
     `resets = false` hides `↻`.
- Close every `Unverified:` item of earlier units with a test, or give it a
  disposition as the plan skill's Acceptance section requires.
Verify: `go test ./... && go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow`
Traps: A failing scenario is fixed inside this unit when the fix is small;
otherwise reply `blocked` with the reason.
Depends: P045.1.1, P045.1.2, P045.1.3, P045.2.1, P045.2.2
Role: acceptance
Commit: P045.3.1: accept liveness and run output
