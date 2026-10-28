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
- Закрытие плана — живой прогон `docflow run --close P049` по скиллу
  `bsv-docflow-close`: это и есть проверка строки бэклола на реальном
  плане. План закрывается этим прогоном, когда все юниты `[x]`, а не
  ручным `docflow close`.

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
- Ручной опт-ин тест удалён целиком, а не переформулирован — Decision плана отвергает ручной режим у консоли, вместо него ConPTY-покрытие — отвергнуто: сохранить тест с переформулированным skip
- Ответ препечатывается в момент старта хоста без ожидания вопроса — буфер консольного ввода хранит вставленные строки до чтения, гонок нет — отвергнуто: ждать в выводе ребёнка строку вопроса перед записью

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

### [ ] P049.1.2 Сценарий живой проверки условия после unblock
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

## Phase 2: Живое закрытие плана

### [ ] P049.2.1 Сценарий `docflow run --close` живым агентом
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

## Phase 3: Приёмка на Windows

### [ ] P049.3.1 Приёмка полного Windows-набора
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
- прогнать новые ConPTY-тесты и живые сценарии
  `bash tools/smoke-run.sh --unblock` и `bash tools/smoke-run.sh --close`
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
сети.
Depends: P049.1.1, P049.1.2, P049.2.1
Role: acceptance
Commit: P049.3.1: accept Windows checks on codex
