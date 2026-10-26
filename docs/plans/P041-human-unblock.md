---
id: P041
title: Human unblock
type: feature
status: active
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
- Класс в `status` печатается только когда в Handoff есть пункт `Blocked` — чтобы не менять точный вывод parity-сценария cycle_status_prompt.sh на юните `[!]` без Handoff — rejected: печатать `(needs human)` по умолчанию для любого `[!]`.
- Один парсер `Blocked (<needs>):` (blockNeeds) используется и Unblock, и Status — rejected: разбор по месту в каждой команде, две копии формата.
- В parity-сценарий help.sh добавлен `docflow unblock -h`, хотя Do: этого не требовал — новый help текст покрывается e2e-набором, а не только Go-тестом — rejected: оставить список старых команд.
- Вопрос ставится в Run до подтверждения старта, а не в runLoop — клавиатура watchKeys и noEcho включаются позже; проверено на настоящем PTY — rejected: спрашивать в ветке blocked внутри runLoop.
- cycle.BlockedNeeds принимает *format.Unit, а не (root, unitID) — selection уже содержит юнита, повторное чтение плана лишнее, разбор остаётся в cycle — rejected: BlockedNeeds(root, unitID) и копия разбора в internal/runner.
- Строка подсказки — одна функция unblockHint на все точки остановки — rejected: три литерала, разъезжающиеся при правке формата.
- Ответ `y|yes` разбирает общий answeredYes, его использует и confirmRunStartWithIO — rejected: вторая копия правила.
- The rule is prompt text only — no Go code path, no runner-side re-check — why: the condition of a human block (a login, a device, an access) is not knowable in code, the agent is the one who can run its command — rejected: an automatic re-check in the runner after unblock.
- One sentinel phrase ("check that condition with a command first") in both cycle texts, asserted in both packages' tests — why: it is the only anchor that keeps the CLI and runner texts aligned — rejected: per-text assertions with divergent wording, which drift silently.
- In RunnerCycle the not-met branch is a `blocked` reply with `blocker: "human"`; in cycleText it is `docflow block {id} --needs human --reason "<what is still missing>"` — why: each text names the action its mode allows (under the runner `block` refuses with code 5) — rejected: naming `docflow block` in the runner text.
- Re-pin the RunnerCycle golden hash instead of loosening TestStrictRunnerSchema — why: the pin's point is that prompt drift is a deliberate, reviewable act — rejected: dropping the hash comparison.
- Replan P041.1.4: точным пунктом блока считаются только `Blocked (human): …`, `Blocked (plan): …` и legacy `Blocked: …`; похожий префикс и некорректная форма не заменяют последний настоящий блок — иначе произвольный Handoff может скрыть `plan` и разрешить `unblock`. Rejected: распознавание через один префикс `Blocked`.
- Legacy `Blocked: …` требует той же проверки условия после `Unblocked:`, что и `Blocked (human): …`, во всех инструкциях цикла — Constraints уже определяют legacy как `human`. Rejected: снимать legacy-блок командой, но исключать его из правила повторной проверки.
- P041.1.4 заменён исправляющим P041.1.5 и повторным ревью P041.1.6; приёмка зависит от обоих новых юнитов — ревью не исправляет код, а найденные дефекты должны быть устранены до приёмки. Rejected: повторять ревью без исправления или менять завершённые P041.1.1–P041.1.3.
- Класс блока — точный префикс по фиксированному списку blockedClasses {human, plan}, двойник не считается блоком — why: произвольный класс в тексте некем снимать, а префикс `Blocked` из прошлой версии превращал `Blocked (plan)` без `:` в human-блок и разрешал `unblock` там, где нужен replan — rejected: принимать любой `Blocked (<x>):` и считать неизвестный класс human.
- Ассерты по навыку: нормализация пробелов + счётчик вхождений ≥3 — why: правило обязано быть во всех абзацах про снятый юнит, а проза переносится посреди фразы — rejected: ассерт подстроки по сырому тексту (ломается на переносе) и одиночное вхождение (молча пропускает потерю правила в абзаце).
- Документация: правило точных форм — одной строкой в docs/design/cycle.md, запись `### Fixed` в CHANGELOG, правка существующей записи cycle-текста — why: одно событие одно место; usage.md и README описывают команды, а не формы разбора — rejected: дублировать формы в usage.md.
- Переставить хеш TestStrictRunnerSchema, а не ослаблять тест — why: смысл пина в том, что правка промпта — осознанный и просматриваемый шаг (текст RunnerCycle прочитан перед пином) — rejected: убрать сравнение хеша.
- Replan P041.1.6: отмена контекста прерывает ожидание и ответа `done?`, и заметки `note:`; после отмены нельзя вызывать `unblock`, менять план или запускать агента — PTY-прогон ревью подтвердил зависание и позднее снятие блока после Enter. Rejected: проверять контекст только после блокирующего чтения или только перед первым вопросом.
- На Windows терминал определяется и для настоящего `os.Stdin` (`*os.File`), а перенаправленный ввод остаётся неинтерактивным — иначе вопрос из Goal недостижим на поддерживаемой платформе. Rejected: считать тестовый `IsTerminal()` доказательством работы Windows-консоли; выдавать кросс-компиляцию за консольный прогон.
- P041.1.6 заменён исправляющим P041.1.7 и повторным ревью P041.1.8; приёмка зависит от обоих и проверяет отмену на обеих строках ввода. Rejected: повторять ревью без исправления или менять завершённые юниты.
- Отмена вопроса идёт через контекст прогона и общий эпилог signalStopCode, а не через собственный возврат 130 из askUnblock — why: хендлер ставит stopped до cancel(), поэтому тот же эпилог печатает сигнал ровно один раз и возвращает 128+signalNumber (на Windows тоже 130); rejected: захардкодить 130 в askUnblock (разъезжается с signalNumber) и разбирать сигналы в каждой ветке остановки.
- Отменяемое чтение = горут + select по ctx.Done, без закрытия os.Stdin и без низкоуровневого прерывания — why: закрытие общего stdin процесса ломает все поздние читатели, а консольное чтение на Windows прервать нельзя в принципе; rejected: close(os.Stdin), ioctl-трюки, горут-читатель, переживающий вопрос (сразу за отменой Run завершает процесс).
- ctx.Err() проверяется и в select, и перед вызовом unblockCommand — why: ответ может прийти ровно в момент отмены (select выбирает случайно), а претензия человека после остановки не имеет права менять план; rejected: полагаться только на select.
- runInput (stream + reader) — один владелец stdin для всех промптов старта, передаётся и в askUnblock, и в confirmRunStartWithIO — why: требование 'не оставить ридер, съедающий позднее подтверждение' выполняется совместным ридером и снимается follow-up P041.1.2; rejected: два bufio.Reader над одним fd (как было) и передача *bufio.Reader туда, где нужен терминальный checks по потоку.
- Терминал Windows — syscall.GetConsoleMode на Handle(Fd()) стандартного потока, только stdlib — why: Go отображает os.Stdin/Stdout/Stderr в стандартные консольные handles, GetConsoleMode — документированный признак консоли и отказывает pipe/редиректу/NUL; rejected: golang.org/x/sys/windows (запрещён планом) и проверка GetFileType == FILE_TYPE_CHAR (NUL — char device, ложные срабатывания).
- PTY-приёмка 'отмена без Enter' = выход процесса до любого нажатия + обязательная ошибка позднего ввода; промпт ждём по буферу stdout процесса — why: stdout ребёнка перехвачен в буфер, на PTY попадает лишь эхо; rejected: писать клавиши сразу (flaky-форма P041.1.6) и читать промпты с экрана.
- Документация: отмена и консоль Windows — одной фразой в п.2 цикла docs/design/runner.md, дваFixed в CHANGELOG — why: одно событие одно место, usage.md и README описывают команды, а не поведение сигнала; rejected: новый дизайн-топик про Windows и дублирование в usage.md.
- Replan P041.1.8: общий help обязан перечислять все зарегистрированные команды ровно по одному разу и в порядке таблицы `commands`, включая `unblock` между `block` и `lint`; регрессионный тест сравнивает извлечённые имена с регистрацией — проверка вывода на равенство самому `rootHelp` не обнаруживает пропущенную команду. Rejected: проверить только `unblock -h` или добавить ещё один ручной список ожидаемых команд.
- P041.1.8 заменён исправляющим P041.1.9 и повторным ревью P041.1.10; приёмка зависит от обоих — найденный дефект общего help устраняется до повторной проверки всей фазы, а требования по отмене и Windows остаются в ревью. Rejected: повторить заблокированное ревью без исправления или менять завершённые юниты.
- Регрессивный тест сравнивает извлечённые имена с commands через reflect.DeepEqual — why: один список закрывает «ровно один раз, без лишних, в порядке таблицы» и источником остаётся регистрация — rejected: второй рукописный список ожидаемых команд (разъезжается с таблицей) и подсчёт упоминаний unblock в описаниях (считает переносы и текст описаний).
- Извлечение пункта — регулярка ^ (\S+) в границах секции positional arguments — why: колонка описаний (20 пробелов) сама отсекает перенесённые строки, а options: исключает '-h, --help' — rejected: подсчёт отступов по всем строкам вывода и сравнение вывода с rootHelp (TestParseArgsHelp уже так делает и слеп к пропуску).
- rootHelp остаётся рукописным текстом, а не генерируется из commands — why: Do: требует сохранить现有 вёрстку, а описания в таблице однострочные и генерация переформатировала бы весь блок, который пинит тест Python-контракта — rejected: собирать rootHelp из таблицы (меняет закреплённый посимвольный контракт вывода).
- Docs не правятся: docs/usage.md и docs/design/cli.md уже описывают unblock как команду — дефект был только в тексте общего help, и он записан одной строкой в CHANGELOG '### Fixed' — rejected: дублировать список команд в usage.md.

## Phase 1: Unblock

### [x] P041.1.1 Add docflow unblock
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
Handoff:
- Класс блока разбирает одна функция — blockNeeds в internal/cycle/verify.go (последний пункт Handoff `Blocked (<needs>): …`, legacy `Blocked:` = human); Status уже её использует. Раннеру (P041.1.2) она недоступна: неэкспортируемая — экспортируй обёртку в cycle (например BlockedNeeds(root, unitID)), а не дублируй разбор в internal/runner.
- `unblock` добавлена в runnerOnly: под раннером (DOCFLOW_RUNNER_UNIT) команда отвечает код 5 — это проверено вручную. Раннер зовёт её через cycle.Self, как block; для ручной проверки CLI нужен `env -u DOCFLOW_RUNNER_UNIT bin/docflow …`.
- Промпт снятого `[~]` юнита уже содержит его блок целиком, включая `Blocked (human): …` и `Unblocked: …` (проверено на temp-репо: `docflow prompt P001.1.1`); секция `## Handoff from Depends` — только про зависимости. Для P041.1.3 правило повторной проверки есть на что опереться, менять вывод prompt не нужно.
- Юнит `[!]` без пункта `Blocked` в Handoff (заблокирован руками) печатается как раньше — `blocked: <id> <title>` без класса и без подсказки; на этом держится parity-сценарий tools/parity/scenarios/cycle_status_prompt.sh. Не ломай это при правке status.
- Parity-набор: 68 сценариев зелёные (`bash tools/parity/run.sh bin/docflow bin/docflow`); tools/parity/scenarios/help.sh теперь вызывает и `docflow unblock -h` — при новых командах список сценария надо пополнять.
- Unverified: Новые строки status (`(needs human)` + `unblock: …`) не имеют e2e/parity-сценария: проверка — юнит-тесты TestStatusReadinessAndBlocked и ручной прогон bin/docflow status в temp-репо; сценарий в tools/parity/scenarios/ не добавлен.

### [x] P041.1.2 Ask to unblock at run start
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
Handoff:
- Вопрос снят не в runLoop, а в Run: сразу после runLock/stop-file и до prerequisites, confirmRunStart и запуска watchKeys/noEcho. Лювый новый промпт внутри runLoop обязан разбираться с клавиатурой watchKeys и выключенным echo — там это ловушка; перенос вопроса в цикл её вернёт.
- Класс и причину блока раннер берёт из cycle.BlockedNeeds(unit *format.Unit) (needs, reason) — сигнатура принимает юнит, а не (root, unitID), как предлагал P041.1.1: selection раннера уже несёт разобранного юнита, чтение плана не нужно. Парсер один: blockedItem + blockedClass в internal/cycle/verify.go, blockNeeds (status) — обёртка.
- Текст подсказки `unblock: docflow unblock <id> --note "…", then docflow run` живёт в одной функции unblockHint (internal/runner/helpers.go) и печатается в трёх точках: блок посреди юнита (runUnit), остановка на выборе (runLoop), отказ от вопроса. Юнит `[!]` без пункта `Blocked` подсказки не даёт — это держат TestRunStopsAndLock/blocked и parity-сценарий cycle_status_prompt.sh.
- На старте прогона по os.Stdin работает два последовательных bufio.Reader (вопрос про unblock, затем confirmRunStartWithIO): заранее нажатую Enter-строку первый может проглотить. Общий читатель на весь старт — незакрытый follow-up, сейчас поведение как в старом коде подтверждения.
- Parity после правки зелёные: 68 сценариев (bash tools/parity/run.sh bin/docflow bin/docflow). Строки раннера в parity нет: набор покрывает CLI-цикл, а не run; подсказка и вопрос покрыты Go-тестами TestRunStopsOnHumanBlock и TestTerminalUnblockAtRunStart.
- Новый хелпер PTY-тестов runInTerminalWithInput (terminal_run_test.go) возвращает код, stdout+stderr и нормализованный экран терминала — на экране видно эхо введённой заметки; следующие TTY-тесты (P041.1.3) могут брать его.
- Unverified: Ветка интерактивного вопроса на консоли Windows (terminal_windows.go, isTerminal/noEcho): PTY-тесты собраны под darwin||linux — нет ни одного прогона вопроса на Windows.
- Unverified: Путь `y` в настоящем терминале с живым агентом: ручной прогон в pty отвечал `n` (не тратить квоту модели), снятие блока живым прогоном проверено только fake-агентом в PTY-тесте.

### [x] P041.1.3 Re-check the condition after unblock
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
Handoff:
- internal/agents/agents_test.go:555 (TestStrictRunnerSchema) pins sha256 of the whole RunnerCycle text; any edit to the runner prompt fails that test until the hash is re-pasted (go test ./internal/agents prints the new value). P041.1.3 set it to b25fcc9fd39619e8d746f4121a03e48a3eaaa4d200ac0a2eb722b2fa8ebedcf1. The pin is documented nowhere in docs/design — grep 'SHA-256' --include='*_test.go'.
- The two cycle texts share the sentinel phrase "check that condition with a command first", asserted in TestPromptTextAndReadBudget (cycle_test.go:476 parts list) and TestRunnerPromptInstructions (start_test.go:32). Assertions match the rendered text, so a word wrapped across a source line breaks the assertion: the first run failed on `blocker: "human" naming what is still missing` for exactly that reason — keep asserted phrases on one source line.
- Installed skills on this machine are symlinks to the release tree (~/.pi/agent/skills/* and ~/.claude/skills/* → ~/.docflow/current → versions/0.13.0/skills), not the working tree: grep 'docflow unblock' in the installed bsv-docflow-run/SKILL.md returns 0. The AGENTS.md promise that working-tree skill edits are live at once holds only for the tools/install-dev.sh layout; agent CLIs read the new rule after a release. I did not run the installer.
- docs/design/cycle.md and docs/usage.md already state the re-check (added by P041.1.1): cycle.md lines 51-56, usage.md line 604. The doc line in Do: is satisfied — nothing added. docs/design/skills.md describes skills in one line each and lists no rules, so it stayed untouched, as Do: allowed.
- Prompt grew ~5 lines per unit prompt (cycle proxy: bytes/4, TestPromptTextAndReadBudget recomputes tokens from its own output, so it tracks automatically). No parity scenario pins the cycle text: grep '## Cycle' in tools/parity/ hits only repo-fixture names.
- Full verify (unit Verify + plan gate go test ./...) is green; parity was not run — this unit's Verify does not include it and no scenario covers the cycle text (P041.1.2 reported 68 scenarios green).
- Unverified: Behaviour of a real agent on a resumed unblocked unit: no check drives an agent against the new rule. TestTerminalUnblockAtRunStart drives the unblock flow with a fake agent only; the re-check is unmeasurable in the current suite (no live run through unblock + re-check).
- Unverified: Skill text is not active for installed agent CLIs (they read the 0.13.0 release tree) — nothing checks that an agent CLI ever sees the new rule; the unit's Verify only greps the source files in skills/.
- Unverified: Windows console path: the change is platform-neutral prompt text, no Windows run performed; the PTY tests of P041.1.2 remain darwin/linux only.

### [-] P041.1.4 Review the unblock phase
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
Handoff:
- Blocked (plan): Нужен исправляющий юнит для точного распознавания Blocked и повторной проверки условия после снятия legacy-блока.

### [x] P041.1.5 Fix block recognition and legacy re-check
Read:
- internal/cycle/verify.go:L597-675
- internal/cycle/cycle_test.go:L254-325
- internal/cycle/cycle_test.go:L457-492
- internal/cycle/done_test.go:L237-325
- internal/cycle/commands.go:L17-36
- internal/agents/agents.go:L137-155
- internal/agents/agents_test.go:L545-575
- internal/runner/start_test.go:L15-50
- skills/bsv-docflow-run/SKILL.md
Do:
- Tighten the shared block parser: recognize only `Blocked (human): …`,
  `Blocked (plan): …` and legacy `Blocked: …` (class `human`). Ignore
  unrelated or malformed Handoff items, including `Blockedness: …`,
  `Blocked by …` and `Blocked (plan)` without `:`. Select the latest
  valid block, so a later lookalike cannot hide an earlier `plan` block.
  Keep Unblock, Status and runner on the same parser.
- Add regression tests with a real plan block followed by lookalikes:
  `BlockedNeeds` still reports `plan` and its reason, `status` still shows
  `(needs plan)` without a human hint, and `Unblock` refuses without
  changing the plan. Cover lookalikes without any valid block, both
  orders of valid human/plan/legacy items, and `Unblocked:` after them.
- Extend the re-check rule in both `cycleText` and `RunnerCycle` and in
  every corresponding passage of `skills/bsv-docflow-run/SKILL.md`:
  `Unblocked:` after the latest human block includes legacy `Blocked:`.
  Check the condition with a command before `Do:`; when it is unmet,
  block again as `human` using the action allowed by that cycle mode.
- Update the prompt assertions to require explicit legacy coverage in
  both cycle texts; render a resumed legacy-block fixture with its
  `Blocked:` and `Unblocked:` history and the re-check instruction.
  Add a source-skill assertion for the same legacy rule. Re-pin the
  RunnerCycle hash after reviewing the new text.
Verify: `go test ./internal/cycle/ ./internal/runner/ ./internal/agents/`
Traps: A unit `[!]` with no valid Blocked item keeps the existing status
output without a class or hint; do not infer `human` from a lookalike.
Keep the sentinel phrase "check that condition with a command first".
The RunnerCycle hash is pinned by TestStrictRunnerSchema. Edit skill
sources only; do not install them or change installed copies.
Depends: P041.1.1, P041.1.2, P041.1.3, P041.1.4
Commit: P041.1.5: fix block recognition and legacy re-check
Handoff:
- Точное распознавание: blockedClass принимает только `Blocked (human):`, `Blocked (plan):` (классы из blockedClasses = значения `--needs`) и legacy `Blocked:`; для двойника возвращает needs="", reason="". blockedItem выбирает последний ВАЛИДНЫЙ пункт — хвостовой `Blocked (plan)` без `:` не прячет предыдущий `Blocked (plan): …`.
- Снято/сохранено без изменений: `Unblock` юнита `[!]` вообще без валидного пункта блока (ручной блок, в том числе с одними двойниками) по-прежнему снимает его — код 0; класса при этом нет, и `status` не печатает `(needs …)` и строку `unblock:`. Это держат parity-сценарий cycle_status_prompt.sh, TestStatusReadinessAndBlocked и новый TestUnblockIgnoresBlockedLookalikes/«lookalikes alone keep the block classless».
- Общие фразы двух cycle-текстов: сентинел `check that condition with a command first` и новая `a legacy `Blocked:` item counts as human` — печатаются на одной строке источника (перенос строки ломает ассерт) и проверяются в TestPromptTextAndReadBudget, TestBlockedItemRecognition, TestRunnerPromptInstructions и TestRunSkillUnblockRecheckCoversLegacyBlock.
- Хеш RunnerCycle переставлен: b25fcc9f… → 151eb2c681b82e74b1f245d4e63c12505f88a646415325b58b29a9ef46c4bcdd (TestStrictRunnerSchema, internal/agents/agents_test.go:555).
- Навыковый ассерт нормализует пробелы (strings.Fields) и считает вхождения ≥3: проза SKILL.md переносится посреди фразы, а правило живёт в трёх абзацах (Procedure, runner-mode reply, Who lifts each block). При правке навыка держи все три абзаца с этой формулировкой.
- Тестовые хелперы: blockedLookalikes в internal/cycle/cycle_test.go (пакет cycle, виден и в done_test.go), таблица классов/порядков в TestBlockedItemRecognition; многострочные пункты Handoff в фикстуры не добавляй — формат их не парсит как список, а Block пишет причину в одну строку.
- Проверки финального дерева: `bin/docflow verify P041.1.5` — verify+gate passed; `bash tools/parity/run.sh bin/docflow bin/docflow` — 68 passed, 0 skipped; `docflow lint` rc 0; ручной CLI-прогон в temp-репо (`env -u DOCFLOW_RUNNER_UNIT`): `(needs plan)` без подсказки, unblock rc 5 и план без изменений, «только двойники» → `blocked: <id> <title>`.
- P041.1.6 (повторное ревью): дефекты, если найдёт, правятся до приёмки P041.1; приёмка зависит от P041.1.5 и P041.1.6.
- Unverified: Поведение живого агента на снятом юните: правило повторной проверки — только текст промпта, ни один тест не гоняет агента через unblock → re-check (нет такого стенда); проверено лишь присутствие формулировок в рендере промпта.
- Unverified: Установленные копии навыков на этой машине — дерево релиза 0.13.0 (symlinks), новый текст навыка активен для agent CLI только после релиза; установщик не запускался (по Traps юнита), проверка — только grep исходника в skills/.
- Unverified: Консоль Windows: PTY-тесты раннера собраны под darwin||linux, прогона вопроса/снятия блока на Windows нет (правки юнита — платформенно-нейтральный текст, но прогона не было).

### [-] P041.1.6 Review the corrected unblock phase
Read:
- docs/design/cycle.md#цикл-юнита
- docs/design/runner.md:L40-70
Do:
- Review the complete phase diff named in the prompt's `## Phase diff`
  against `## Goal`, `## Constraints`, `## Decisions` and each unit's
  `Do:`, including the fixes required by P041.1.5. Edit nothing.
- Confirm that lookalike Handoff items cannot hide a plan block and that
  the CLI cycle, runner cycle and run skill explicitly require the same
  condition check after a legacy human block is lifted.
- A defect — reply `blocked` with `blocker: "plan"`; anything else worth
  noting — an `Unverified:` item.
Verify: `go test ./...`
Role: review
Commit: P041.1.6: review corrected unblock phase
Handoff:
- Blocked (plan): Нужен исправляющий юнит для двух дефектов. [P2] internal/runner/helpers.go:405,416: чтение stdin не реагирует на отмену контекста; Ctrl-C оставляет процесс ждать, а последующий Enter на note: вызывает unblock уже после отмены. Подтверждено PTY-прогоном. [P2] internal/runner/helpers.go:393: вопрос недостижим на Windows — передаётся os.Stdin (*os.File), для которого internal/runner/terminal_windows.go:11 всегда возвращает false.

### [x] P041.1.7 Fix cancellable unblock input and Windows detection
Read:
- internal/runner/helpers.go:L315-435
- internal/runner/run.go:L125-235
- internal/runner/terminal_unix.go
- internal/runner/terminal_windows.go
- internal/runner/signals_windows.go
- internal/runner/terminal_run_test.go:L84-178
- internal/runner/terminal_run_test.go:L220-300
- docs/design/runner.md:L40-70
Do:
- Pass the run context into the unblock question and make both line reads
  cancellable. On cancellation while waiting for `done?` or `note:`, stop
  promptly without another newline; return the runner's signal exit code
  (Ctrl-C: 130). Do not unblock, reselect, run prerequisites or launch an
  agent after cancellation. Check cancellation before the unblock call
  as well, so an answer delivered after cancellation cannot change state.
- Keep ownership of stdin explicit: cancel or release pending reads when
  the question ends, without closing the process's shared `os.Stdin` or
  leaving a reader to consume the later start confirmation/watchKeys.
  Preserve visible note echo, empty-note behavior and existing guards
  (`--yes`, redirected stdin, a plan block, a block reached mid-run).
- Implement Windows terminal detection for real `*os.File` console
  handles with the standard library. Keep the `IsTerminal()` test hook;
  redirected files and pipes must return false. Do not add dependencies
  or change unrelated Windows console behavior.
- Add deterministic regression coverage for cancellation before input,
  during each read, and before the unblock side effect. Extend real PTY
  coverage: wait for each prompt, send SIGINT, require exit 130 within
  a bounded timeout without Enter; also deliver a late answer/note after
  cancellation and prove the plan is byte-for-byte unchanged, the unit
  stays `[!]`, no `Unblocked:` item appears and no agent is called.
- Add Windows-specific tests using actual `*os.File` handles for console
  detection and redirected input; exercise the unblock question through
  native console stdin when available. Keep platform-neutral guard tests.
  Record missing Windows runtime coverage as `Unverified`, separately
  from successful cross-compilation; do not silently count skips as pass.
- Update the existing runner decision topic for cancellation behavior
  and add the user-visible fixes under `CHANGELOG.md` `[Unreleased]`.
Verify: `go test ./internal/runner/ && GOOS=windows GOARCH=amd64 go test -c -o /tmp/docflow-p041-runner-windows.test.exe ./internal/runner/`
Traps: The signal handler already cancels the run context before the
question, but the usual signal result handling is later in Run. A detached
goroutine around ReadString alone can leave a blocked reader behind;
checking ctx.Err only after ReadString still waits for Enter. The PTY
helper writes all keys immediately: extend it to synchronize on prompts
and signal delivery. Existing PTY tests are darwin/linux only; fake
IsTerminal streams and cross-compilation do not prove Windows-console
reachability. Keep the question before watchKeys/noEcho starts.
Depends: P041.1.2, P041.1.5, P041.1.6
Role: heavy
Commit: P041.1.7: fix unblock input cancellation
Handoff:
- askUnblock теперь возвращает (unblocked, cancelled); точка остановки — общий signalStopCode(p, totals, &signalMu, &stopped, state), его последний аргумент называет состояние юнита: на старте 'P001.1.1 stays [!]' (prompt ещё не было, '[~]' было бы ложью), в цикле 'the current unit stays [~]'. Любая новая точка остановки обязана передать своё состояние, а не литерал.
- readLineContext оставляет заблокированный читатель после отмены (fd нельзя прервать, не закрывая общий os.Stdin) — это легально только потому, что Run сразу возвращает код сигнала. Кто продолжит прогон после отменённого чтения, получит украденный ридер: после cancelled=true в Run нельзя делать ничего, кроме возврата.
- Шов unblockCommand = cycle.Self: в тестовом бинаре cycle.Self перезапускает сам тест-бинарь с args 'unblock …' (то есть гоняет тесты). Любой тест, способный дойти до снятия блока, обязан взять recordUnblock(t). В пакете нет t.Parallel, поэтому шов гонке не подлежит.
- PTY-тесты нового поколения: ttySession (startTerminalRun/typeKeys/signal/awaitExit/screenText) в terminal_run_test.go. typeKeys возвращает ошибку записи: после выхода процесса PTY отвечает EIO — тест требует именно этой ошибки как доказательства, что читателя не осталось. Промпты ждутся по буферу stdout процесса (syncBuffer.waitFor): stdout ребёнка — буфер, а не PTY, на экране только эхо клавиш.
- Проверка мутацией: под формой P041.1.6 (чтение без ctx + без ctx.Err() перед снятием блока) unit-таблица TestAskUnblockCancels падает на стадиях done?/note:/before-the-side-effect, а TestTerminalUnblockSignalCancels — на 'the run did not exit within 10s'. Правка, убирающая только ctx-чувствительность чтения, ловится стадией 'at done?' + unit-стадией 'before the unblock side effect' (страж перед unblockCommand критичен: select при готовности обеих сторон выбирает случайно).
- Windows: isTerminal(*os.File) = syscall.GetConsoleMode(Handle(Fd())). os.Stdin.Fd() — уже console handle (GOROOT: os/file.go Stdin = NewFile(uintptr(syscall.Stdin)), syscall/syscall_windows.go:584 Stdin = getStdHandle(STD_INPUT_HANDLE)); GetConsoleMode отказывает для файла, pipe и NUL. Не 'чинить' через GetStdHandle-маппинг fd 0/1/2 и не через GetFileType(FILE_TYPE_CHAR) — NUL является char device.
- Windows-тесты: TestIsTerminalWindowsConsoleHandle пропускается, когда stdin не консоль, TestAskUnblockWindowsConsoleTyped требует DOCFLOW_WINDOWS_CONSOLE_ASK=1 (нужен человек за клавиатурой). Пропуск ≠ приёмка: рантайм-покрытие Windows вынесено в Unverified этого юнита отдельно от успешной кросс-компиляции.
- runInput чинит follow-up P041.1.2 между вопросом про unblock и confirmRunStart (TestRunInputKeepsBufferedLines: вставленная одной строкой 'y\nзаметка\ny\n' доходит до подтверждения). watchKeys по-прежнему строит свой bufio.Reader поверх того же fd — строка, съеденная подтверждением старта, к 's'-наблюдателю не попадает; этот follow-up остаётся открытым.
- Проверки финального дерева: docflow verify P041.1.7 (verify + gate passed), bash tools/parity/run.sh bin/docflow bin/docflow → 68 passed, 0 skipped, docflow lint rc 0, GOOS=windows GOARCH=amd64 go vet ./... rc 0, GOOS=windows go test -c ./internal/runner → PE32+ (console). Gate плана — go test ./...; parity и test-install — шаги CI, не gate.
- Unverified: Windows runtime: TestIsTerminalWindowsRealHandles, TestIsTerminalWindowsStandardStreams, TestIsTerminalWindowsConsoleHandle, TestAskUnblockWindowsConsoleTyped — только скомпилированы под windows/amd64 (PE32+ console), ни одного прогона: нет Windows-машины и консоли. Кросс-компиляция — не прогон; skips в этих тестах не считаются приёмкой.
- Unverified: Ветка интерактивного вопроса в настоящей консоли Windows (прохождение стража isTerminal(os.Stdin), эхо набранной заметки) — нет ни одного PTY/консольного прогона на Windows: PTY-хелперы собраны под darwin||linux.
- Unverified: Эхо заметки на Windows: disableEcho в terminal_windows.go остаётся no-op (правка вне рамок юнита) — набранная заметка видна консоли, но прогона не было; отсутствие теста и прогона.
- Unverified: watchKeys: строка, набранная на подтверждении старта, не доходит до 's'-наблюдателя (у него свой bufio.Reader над тем же fd) — нет теста на эту связку; общий runInput покрывает только пару 'вопрос → подтверждение'.
- Unverified: Поведение живого агента на юните, снятом прогоном: правило повторной проверки — только текст промпта, ни один тест не гоняет агента через unblock → re-check (унаследовано от P041.1.5).
- Unverified: Установленные копии навыков на этой машине — дерево релиза 0.13.0; новый текст навыка активен для agent CLI только после релиза, установщик не запускался (по Traps плана), проверка — только grep исходника в skills/.

### [-] P041.1.8 Review cancellation and Windows unblock fixes
Read:
- docs/design/cycle.md#цикл-юнита
- docs/design/runner.md:L40-70
Do:
- Review the complete phase diff named in the prompt's `## Phase diff`
  against `## Goal`, `## Constraints`, `## Decisions` and each unit's
  `Do:`, including P041.1.5 and P041.1.7. Edit nothing.
- Confirm that cancellation interrupts both pending reads without Enter,
  late input cannot lift a cancelled block, signal exit handling remains
  correct, and no pending reader steals input from later startup steps.
- Confirm Windows detection accepts actual console `*os.File` stdin and
  rejects redirected input; distinguish native runtime evidence from
  fake-stream tests, skipped tests and cross-compilation.
- Retain the checks that lookalikes cannot hide a plan block and that
  both cycle texts and the run skill require the legacy human re-check.
- A defect — reply `blocked` with `blocker: "plan"`; anything else worth
  noting, including unavailable Windows runtime evidence — `Unverified:`.
Verify: `go test ./...`
Depends: P041.1.7
Role: review
Commit: P041.1.8: review unblock input fixes
Handoff:
- Blocked (plan): [P3] cmd/docflow/main.go:178: команда unblock зарегистрирована, но отсутствует в отдельном списке rootHelp. Воспроизведено собранным CLI: bin/docflow -h пропускает unblock между block и lint, хотя bin/docflow unblock -h работает. Нужен исправляющий юнит: дополнить общий help и проверить соответствие списка зарегистрированным командам.

### [x] P041.1.9 Fix root help command coverage
Read:
- cmd/docflow/main.go:L30-60
- cmd/docflow/main.go:L161-202
- cmd/docflow/main_test.go:L77-90
- cmd/docflow/main_test.go:L316-326
- CHANGELOG.md
Do:
- Add `unblock` to `rootHelp` between `block` and `lint`, with the
  registered description `resume a unit blocked for a human`. Preserve
  the existing help layout and command-specific help.
- Add a regression test for both root `-h` and `--help`: extract command
  entry names from the command section of the rendered output and
  compare them with the ordered names in `commands`. Require every
  registered command exactly once, with no extra names; ignore wrapped
  description lines and the options section. Demonstrate that the test
  fails on the missing `unblock` entry before the fix and passes after it.
- Build the CLI and check that root help lists `unblock` and that
  `unblock -h` still exposes `--note`.
- Add the user-visible help correction under `CHANGELOG.md`
  `## [Unreleased]` / `### Fixed`.
Verify: `go test ./cmd/docflow/ && go build -o bin/docflow ./cmd/docflow && bin/docflow -h | grep -q '^    unblock[[:space:]]' && bin/docflow unblock -h | grep -q -- '--note'`
Traps: TestParseArgsHelp compares output with rootHelp itself and cannot
detect an omitted command. TestCommandsMatchDesign covers registration,
not root help. Do not create a second expected command list or count
mentions inside descriptions as command entries.
Depends: P041.1.7, P041.1.8
Commit: P041.1.9: fix root help command coverage
Handoff:
- rootHelp — рукописный список команд, отдельный от таблицы commands: новый TestRootHelpListsRegisteredCommands сравнивает извлечённые из вывода имена с упорядоченными именами commands, поэтому правка команды в main.go обязана принести строку help в том же изменении, иначе gate (go test ./...) падает.
- Шов извлечения зафиксирован в rootHelpCommands: пункт = ^ (\S+) внутри диапазона 'positional arguments:' … 'options:'; перенесённые строки описания стоят в колонке 20 пробелов и матча не дают. Правка вёрстки help (колонка, ширина переноса) обязана обновить регулярку — тест тогда сообщит о разъезде имён, а не о формате.
- Доказательство мутацией для P041.1.10: до правки теста-на-главную-ветку тест падает на '-h' и '--help' с пропущенным unblock; мутация rootHelp (дубликат строки block, main.go восстановлен из /tmp/main.go.bak, git diff = 1 вставка) даёт падение с лишним именем — тест чувствителен и к пропуску, и к дубликату.
- Проверки финального дерева: go test ./cmd/docflow/ (ok, 4.607s), docflow verify P041.1.9 (verify + gate 'go test ./...'), bin/docflow -h содержит '^ unblock', bin/docflow unblock -h содержит --note, gofmt/vet чистые, docflow lint rc 0, tools/parity/run.sh bin/docflow bin/docflow → 68 passed 0 skipped (шаг CI, не gate).
- docflow в PATH — установленный релиз 0.13.0 (~/.docflow/bin -> ../current), именно он вёл verify/done/commit этого плана; bin/docflow — сборка чекаута. Help-правка живёт только в сборке чекаута.
- Unverified: Help установленного релиза: docflow -h у ~/.docflow/current (0.13.0) не содержит ни одного упоминания unblock — проверка на установленной копии не выполнена, она требует релиза; установщик не запускался (Traps плана).
- Unverified: Parity help.sh: прогон был self-comparison (bin/docflow vs bin/docflow), поэтому изменённый общий help не сверялся с выводом опубликованного релиза — расхождение транскрипта относительно 0.13.0 не проверено.
- Unverified: Собственные help-тексты команд (docflow <cmd> -h) не сверяются с регистрацией — покрытие тестом только у общего help; команда, пропущенная в собственном help-пути, этим юнитом не проверяется.
- Unverified: tools/test-install.sh (шаг CI) не запускался — путь установки/обновления не затронут.

### [x] P041.1.10 Review complete unblock phase and help
Read:
- docs/design/cycle.md#цикл-юнита
- docs/design/runner.md:L40-70
- cmd/docflow/main.go:L30-60
- cmd/docflow/main.go:L161-202
- cmd/docflow/main_test.go
Do:
- Review the complete phase diff named in the prompt's `## Phase diff`
  against `## Goal`, `## Constraints`, `## Decisions` and each unit's
  `Do:`, including P041.1.5, P041.1.7 and P041.1.9. Edit nothing.
- Confirm that cancellation interrupts both pending reads without Enter,
  late input cannot lift a cancelled block, signal exit handling remains
  correct, and no pending reader steals input from later startup steps.
- Confirm Windows detection accepts actual console `*os.File` stdin and
  rejects redirected input; distinguish native runtime evidence from
  fake-stream tests, skipped tests and cross-compilation.
- Retain the checks that lookalikes cannot hide a plan block and that
  both cycle texts and the run skill require the legacy human re-check.
- Confirm that built CLI root help lists `unblock` between `block` and
  `lint`, command-specific help still works, and the regression test
  checks all root help command entries against registration rather than
  comparing the output with itself or another manual list.
- A defect — reply `blocked` with `blocker: "plan"`; anything else worth
  noting, including unavailable Windows runtime evidence — `Unverified:`.
Verify: `go test ./...`
Depends: P041.1.9
Role: review
Commit: P041.1.10: review unblock phase and help
Handoff:
- Ревью diff 098ac4c..HEAD завершено: дефектов не найдено, файлы не изменялись. Полный docflow verify P041.1.10: Verify и gate прошли.
- На macOS arm64 PTY подтверждены отмена обоих вопросов без Enter, выход 130, неизменность плана и отсутствие запуска агента. Поздний ввод отклоняется; общий читатель сохраняет ввод следующего подтверждения.
- Checkout-бинарник: оба варианта root help перечисляют все 26 команд по регистрации, unblock стоит между block и lint; справка всех команд работает. Защита plan-блока от похожих строк и legacy re-check сохранены.
- Signal/PTY-проверки выполнены вне sandbox с учётом известных ограничений среды.
- Unverified: Нативный Windows runtime не проверен: выполнена только успешная кросс-компиляция Windows-тестов. Реальная консоль stdin, перенаправление и интерактивный unblock требуют Windows; fake-stream тесты этого не доказывают.

## Phase 2: Acceptance

### [ ] P041.2.1 Accept the human unblock
Read:
- internal/runner/terminal_run_test.go:L20-125
- internal/runner/run_test.go:L440-450
- internal/runner/terminal_windows.go
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
  4. A legacy `Blocked: …` human block: unblock resumes the same unit;
     its prompt includes the original reason, `Unblocked:` and the
     explicit legacy re-check rule. A fake agent exercises both outcomes
     using a fixture condition: unmet → human block without doing the
     remaining work; met → completion. Keep real-agent compliance as
     `Unverified` unless it is independently demonstrated.
  5. A `plan` block followed by unrelated `Blocked…` Handoff text:
     `status` still reports `needs plan`, `unblock` refuses without
     changing the plan, and `run` offers no human-unblock question.
  6. Real TTY: on both `done?` and `note:`, Ctrl-C exits promptly with
     code 130 without Enter; late input cannot change the plan, add
     `Unblocked:` or launch an agent. The blocked unit stays `[!]`.
  7. Native Windows console: real `os.Stdin` reaches the question and
     `y` plus a note resumes the unit; redirected stdin, `--yes` and a
     plan block never ask. If Windows is unavailable, retain this runtime
     check as `Unverified`; cross-compilation does not close it.
  8. Built CLI: root `-h` and `--help` list `unblock` between `block`
     and `lint`; `unblock -h` still describes the command and `--note`.
- Close every `Unverified:` item of earlier units with a test, or keep it
  as `Unverified:` in this Handoff.
Verify: `go test ./...`
Traps: A failing scenario is fixed inside this unit when the fix is small;
otherwise reply `blocked` with the reason.
Depends: P041.1.1, P041.1.2, P041.1.3, P041.1.4, P041.1.5, P041.1.6, P041.1.7, P041.1.8, P041.1.9, P041.1.10
Role: acceptance
Commit: P041.2.1: accept human unblock
