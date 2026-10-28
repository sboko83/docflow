---
id: P049
title: Windows checks of console and live runner rules
type: feature
status: active
created: 2026-10-28
executor: pi | zai/glm-5.3-flash | high
fallback: pi | strata/swift-1.5-iq3_s | -; codex | gpt-6-luna | high
acceptance: codex | gpt-6-luna | high; pi | zai/glm-5.3-flash | high
permissions: auto
timeout: 7200
gate: go build ./... && go test ./internal/runner/ ./internal/cycle/ ./internal/agents/
---
## Goal

Проверки, которые нельзя воспроизвести без настоящей консоли и живого
агента, становятся воспроизводимыми сценариями: вопрос старта о
human-блоке в настоящей консоли Windows закрыт ConPTY-тестом, правило
повторной проверки условия после `docflow unblock` и закрытие плана
живым агентом — сценариями в `tools/smoke-run.sh`. Строки бэклола
[P041](../archive/P041-human-unblock.md) и
[P038](../archive/P038-runner-backlog-batch.md) закрыты, паритет и
установка на Windows проходят приёмку.

## Context

Бэклол: строки P041 и P038 взяты в работу. Живой прогон `docflow run`
на реальном pi устроен скриптом `tools/smoke-run.sh`, ConPTY-обвязка —
стадии `host` и `child` в тесте `TestWindowsConsoleProgress`. Ниже —
файлы, у которых закрытые Follow-ups.

- docs/docflow.md
- tools/smoke-run.sh
- internal/runner/terminal_console_windows_test.go
- docs/archive/P041-human-unblock.md
- docs/archive/P038-runner-backlog-batch.md
- docs/archive/P045-run-liveness-output.md
- docs/archive/P048-windows-fixtures.md

## Constraints

- Цель — Windows: проверка идёт на этой машине (Go 1.27, Git Bash, pi с
  маршрутами `zai/glm-5.3-flash` и `strata/swift-1.5-iq3_s`, codex по
  ChatGPT); Linux и macOS не затрагиваются, WSL на машине нет.
- Раннер, CLI, формат и скиллы не меняются: правки в тестовых файлах
  пакета `internal/runner` и в `tools/`. Если в живом прогоне всплывёт
  дефект раннера — он правится в этом же юните с регрессией.
- Расход подписки: codex — один вызов (приёмка `P049.3.1`), zai —
  живой сценарий `P049.2.1`, остальное на `strata` (локальная модель,
  подписки не тратит).
- Живые прогоны только во временных репозиториях скриптов `tools/`, в
  этом репозитории живой прогон не запускается.
- Планы временных репозиториев и комментарии в скриптах — на английском.

## Prerequisites

- Codex CLI залогинен по ChatGPT: `codex login status`
- Маршруты pi на этой машине: `pi --list-models | grep -qE 'strata|glm-5.3-flash'`
- Git Bash для команд плана: `bash -c 'command -v bash >/dev/null'`

## Traps

- ConPTY-тест устроен так, что хост живёт в порождённом процессе
  (`DOCFLOW_TEST_CONSOLE_STAGE=host`, re-exec тестового бинаря через
  `-test.run`): ребёнок получает handles консоли, handles хоста менять
  нельзя.
- Вопрос старта о human-блоке читается из общего `runInput` с одним
  `bufio.Reader`: второй ридер над тем же fd съедает набранную заранее
  строку — см. Gotchas в P041.
- Фраза правила повторной проверки в `cycleText` и `RunnerCycle`
  переносится посреди строки: ассерты нормализуют пробелы через
  `strings.Fields`, правка `RunnerCycle` перепинивает хеш в
  `TestStrictRunnerSchema`.
- Точный вывод `docflow run` закреплен в `terminal_accept_test.go` и
  `unblock_acceptance_test.go`: меняется вывод — правятся оба.
- Внутренний прогон под гейтом раннера получает неконсольный stdin,
  поэтому в сценарии он идёт с `--yes` и `--max-units`.
- `tools/smoke-run.sh`: временный репозиторий через `mktemp`,
  `git config core.autocrlf false`, `PATH="$repo/bin:$PATH"`, план
  временного репозитория с `executor:` на strata-маршруте.
- На Windows `python3` — заглушка из Microsoft Store, в скриптах нужен
  `python`; parity требует собранного `bin/docflow`.
- Закрытие плана — живой прогон `docflow run --close` по скиллу
  `bsv-docflow-close`: это и есть проверка строки бэклола на реальном
  плане. План закрывается этим прогоном, когда все юниты `[x]`, а не
  ручным `docflow close`.
- Архивы P038, P041, P045 и P048 не переписывать: выполнение их
  Follow-ups фиксируется в новой записи P049. P041, P045 и P048
  восстановлены к содержимому до приёмки P049.3.1.

## Decisions

- Живые проверки — флагами в `tools/smoke-run.sh`, а не отдельным
  скриптом: сценарии рядом с существующим живым прогоном и тратят
  квоту только по явному флагу — отвергнут новый скрипт `tools/live-check-*`.
- ConPTY-тест вместо ручной проверки с `DOCFLOW_WINDOWS_CONSOLE_ASK=1`:
  консольный тест не требует человека и входит в `go test ./...` —
  отвергнут ручной режим у консоли.
- Закрытие P049 через `docflow run --close` живым агентом: реальным
  планом проверяется и сам закрывающий прогон — отвергнут ручной
  `docflow close`.
- `smoke-run.sh` снимает `DOCFLOW_RUNNER_UNIT` для всех своих вызовов:
  сценарий работает и из гейта раннера — отвергнута очистка окружения
  только в команде Verify одного плана.
- Приёмка использует успешный живой прогон закрытия из P049.2.1,
  если проверенный код не менялся: повтор требует дополнительных вызовов
  zai сверх ограничения плана — отвергнут обязательный повтор `--close`
  в P049.3.1 без расширения бюджета.

## Phase 1: Windows-консоль и живая проверка unblock

### [x] P049.1.1 ConPTY-тест вопроса старта о human-блоке
Read:
- internal/runner/terminal_console_windows_test.go
- internal/runner/terminal_windows_test.go:L95-127
- internal/runner/helpers.go:L540-620
- internal/runner/run.go:L185-215
- docs/archive/P041-human-unblock.md:L60-95
Do:
- добавить `TestWindowsConsoleUnblockAsk` по образцу
  `TestWindowsConsoleProgress`: стадия `host` собирает ConPTY handles и
  пишет ввод ребёнка, стадия `child` запускает `docflow run` во
  временном репозитории с планом, у которого юнит в состоянии
  `Blocked (human):`
- проверить ветку ответа `y`: вопрос старта поднимает `docflow unblock`
  с заметкой, юнит становится `[~]` с пунктом `Unblocked:` в Handoff
- проверить ветку ответа `n`: прогон печатает причину и команду
  `docflow unblock <id> --note "..."` и не снимает блок
- закрыть ручной путь `TestAskUnblockWindowsConsoleTyped`: заменить
  `t.Skip` на ConPTY-покрытие, из формулировки skip убрать фразу
  `recorded as Unverified`
Verify: `go test ./internal/runner/ -run 'TestWindowsConsole' -count=1`
Traps: хост-процесс получает `DOCFLOW_TEST_CONSOLE_STAGE=host` и
`DOCFLOW_TEST_CONSOLE_WIDTH`; при `go test` в pipe stdin не консоль —
обычные консольные тесты скипаются, а ConPTY-тест обязан пройти.
Depends: -
Commit: P049.1.1: cover human-block start prompt on Windows console
Handoff:
- ConPTY-хост принимает DOCFLOW_TEST_CONSOLE_INPUT и препечатывает ответ сразу после CreateProcess: typed-ahead безопасен, потому что вопрос о human-блоке — первое чтение stdin (стартовый confirm возвращается раньше для планов без permissions/prerequisites)
- TestAskUnblockWindowsConsoleTyped и DOCFLOW_WINDOWS_CONSOLE_ASK удалены; покрытие — TestWindowsConsoleUnblockAsk/{yes,no}; оставшийся skip с фразой 'recorded as Unverified' в TestIsTerminalWindowsConsoleHandle относится к другому тесту и сознательно не тронут

### [x] P049.1.2 Сценарий живой проверки условия после unblock
Read:
- tools/smoke-run.sh
- docs/design/cycle.md:L44-60
- docs/archive/P041-human-unblock.md:L60-95
- internal/cycle/commands.go:L15-40
- internal/agents/agents.go:L150-175
Do:
- добавить флаг `--unblock` в `tools/smoke-run.sh`: план из двух юнитов,
  у первого юнита в `Do:` шаг с условием `test -f unblock.flag` и
  действием `docflow block --needs human` при его отсутствии
- проверить первую ветку: условие не выполнено, агент блокирует юнит,
  прогон останавливается с кодом 3, у юнита `Blocked (human):`
- снять блок командой `docflow unblock <id> --note "flag"` при
  невыполненном условии и проверить вторую ветку: агент сначала
  выполняет условие командой, условие не выполнено, юнит снова `[!]` с
  `Blocked (human):`, прогон — код 3
- создать `unblock.flag`, снять блок вторично и проверить третью
  ветку: агент проверяет условие командой, условие выполнено, юнит
  доходит до `[x]`, прогон — код 0
- маршрут временного плана `pi | strata/swift-1.5-iq3_s | -`, чтобы не
  тратить подписку; скрипт печатает `smoke-run: ok unblock` при успехе
Verify: `bash tools/smoke-run.sh --unblock`
Traps: внутренний прогон идёт с `--yes` (вопроса о блоке в неконсоли
нет) и `--max-units 1`; у агента stdin не консоль; `unblock.flag`
создаётся в `$PWD` временного репозитория, а не в `$work`.
Depends: -
Commit: P049.1.2: add live unblock re-check scenario
Handoff:
- smoke-run.sh unsets DOCFLOW_RUNNER_UNIT at the top: launched from an agent session inside `docflow run`, hand `docflow block/unblock/finish/done/commit` calls are refused with exit 5 — any new scenario flag in tools/ must keep that unset
- unblock-branch asserts speak plan text (`^### [!] P001.1.1`, `^- Blocked (human): ` count >= 2) and exit codes 3/3/0; the agent's reason wording is free text and is never asserted — new live branches should follow the same shape
- the unblock plan asserts the second unit stays `### [ ] P001.1.2`: --max-units 1 plus a trailing unit is what proves a blocked run does not skip ahead
- Unverified: the plain live smoke path (zai/codex) after the if/else plan restructure is only dry-run-checked; a live run spends zai quota the plan reserves for P049.2.1 — missing check: `bash tools/smoke-run.sh` without flags

## Phase 2: Живое закрытие плана

### [x] P049.2.1 Сценарий `docflow run --close` живым агентом
Read:
- tools/smoke-run.sh
- docs/design/runner.md:L155-180
- internal/runner/close.go:L1-70
- skills/bsv-docflow-close/SKILL.md
- docs/archive/P038-runner-backlog-batch.md
Do:
- добавить флаг `--close` в `tools/smoke-run.sh`: план из одного юнита
  доводится до `ready to close` живым прогоном, затем запускается
  `docflow run --close` на живом агенте по маршруту
  `pi | zai/glm-5.3-flash | high`
- проверить результат закрытия: `docs/archive/P001-*.md` с Outcome,
  Decisions, Gotchas, Follow-ups, файл плана удалён, коммит
  `P001: close plan`
- проверить событие `close` в `<run-id>.events.jsonl` с `plan`,
  `route`, `result`, `exit_code` и что у закрывающего вызова есть
  `close: true`
- проверить `--dry-run --close`: строка маршрута `close:` печатается и
  вызова агента нет
- печать `smoke-run: ok close` при успехе, `smoke-run: kept <dir>` при
  провале
Verify: `bash tools/smoke-run.sh --close`
Traps: закрывающий агент сам вызывает `docflow close` и коммитит: чужие
коммиты в прогоне разбираются по P012; логи закрытия —
`<run-id>.close<N>.out`, нумерация продолжается со второго запуска.
Depends: -
Executor: pi | zai/glm-5.3-flash | high
Commit: P049.2.1: add live close-run scenario
Handoff:
- smoke-run.sh --close spends two zai calls per run (unit + closing agent); the closing call passed on the first attempt, retry path and second-run log numbering (P001.close2.out) stay unexercised
- the closing agent gets a PowerShell-syntax CLI command from closeCLICommand on Windows while pi agents drive Git Bash; the zai agent adapted on its own — if a close run stalls on `& '...'`, that mismatch is the place to look
- Unverified: the live close scenario ran once; the agent-driven close procedure varies between runs, so flakes on refusal/retry paths are possible — missing check: a repeat `bash tools/smoke-run.sh --close` (each run spends two zai calls)

## Phase 3: Приёмка на Windows

### [x] P049.3.1 Приёмка полного Windows-набора
Read:
- tools/parity/run.sh:L28-70
- tools/test-install-ps1.sh
- tools/parity/scenarios/test_accept_p020__CheckoutAcceptanceTest__test_claude_new_user_path.sh
- docs/archive/P048-windows-fixtures.md:L74-85
- CHANGELOG.md:L1-40
Do:
- собрать `bin/docflow` и прогнать полный набор: `go test ./...`,
  `bash tools/parity/run.sh bin/docflow bin/docflow`,
  `bash tools/test-install.sh`, `bash tools/test-install-ps1.sh`
- прогнать новые ConPTY-тесты и живой сценарий
  `bash tools/smoke-run.sh --unblock`
- принять живое закрытие по успешному Verify P049.2.1 из
  `.git/docflow/run/P049.2.1.verify.log`: проверить код 0 у сценария
  `bash tools/smoke-run.sh --close`, строку `smoke-run: ok close` и
  отсутствие изменений проверенного кода после коммита P049.2.1;
  записать `Accepted:` с причиной использования результата без повтора;
  если свидетельства нет или код изменился, блокировать для перепланирования
- снять с собой каждый пункт `Unverified:` из Handoff первых юнитов:
  закрыть тестом, принять `Accepted:` с причиной, либо оставить
  `Unverified:` со сценарием, который его воспроизводит
- закрыть Follow-ups закрытых планов: вопрос старта в настоящей консоли
  Windows (P041), кадры и спиннер в настоящей консоли Windows (P045),
  четыре паритетных сценария на Windows (P048), ConPTY-обвязка (P048)
- если приёмка вскрыла дефект — чинить в этом же юните с регрессией
- запись в `CHANGELOG.md` появляется, только если изменение задело
  пользовательский путь: тесты и скрипты его не задевают
Verify: `bash tools/parity/run.sh bin/docflow bin/docflow && go test ./...`
Traps: parity считает все 70 сценариев без `PARITY_SKIP`; на Windows в
CI нужен `python` вместо `python3`; `tools/test-install.sh` и
`tools/test-install-ps1.sh` работают с локальной раскладкой релиза без
сети. Повтор `smoke-run.sh --close` в приёмке не запускается: два
вызова zai уже выполнены в P049.2.1; повторяемость отказов и retry
остаётся ограничением этого результата, её нельзя объявлять проверенной.
Depends: P049.1.1, P049.1.2, P049.2.1
Role: acceptance
Commit: P049.3.1: accept Windows checks on codex
Handoff:
- Accepted: живое закрытие временного плана подтверждено в `.git/docflow/run/P049.2.1.verify.log`; `tools/smoke-run.sh` и код раннера после этого не менялись. Повтор сценария на Z.ai не требуется по согласованному бюджету.
- Accepted: повторяемость ответов живой модели не проверяется повторным расходом квоты; состояния отказа, fallback и продолжения нумерации логов закреплены `TestRunCloseFallback`, `TestRunCloseRefusalExhausted`, `TestRunCloseRetryKeepsLogs`.
- Обычный smoke-сценарий проверен живым агентом Strata в отдельной копии скрипта с заменой только маршрута плана; производственный маршрут Z.ai сохранён. Повторная живая проверка `--unblock` тоже завершена.
- `tools/test-install.sh` больше не принуждает `global` на Windows: перенаправленный stderr позволяет проверить fallback без `setsid`, как и на Unix. `tools/test-smoke-run.sh` проверяет успех закрытия, отказ при оставшемся плане в Queue, обычный путь и dry-run без живой модели; запускается в CI.
- После чистого коммита проверки запустить `bin/docflow.exe run --yes --close`: в Queue только P049. Результат закрывает Follow-up P038, но записывается только в новый архив P049; старый архив не редактируется.
- Linux/macOS из Follow-ups P048 ждут CI на этих ОС; цель P049 ограничена Windows. Новая задача в Backlog для этого не нужна.
