---
id: P060
title: dsh agent in the runner
closed: 2026-11-09
---
## Outcome           <!-- 2–3 строки: что теперь есть -->

`docflow run` ведёт юниты через пятого встроенного агента — dsh (DeepSeek
Harness, `dsh headless --json -`): блок `[agents.dsh]` в `DefaultAgents`,
декодер `stream = "dsh"` в agentio, `dsh` как значение агента в тройке
маршрута. Формат не вырос. Проверено на dsh 0.2.2-rc.1 (Windows 11, Node
24): фикстуры потока, тесты парсера и `TestDshRealUnitCommits` — юнит
тестового плана доходит до коммита на реальном dsh.

## Decisions         <!-- решение — почему — отвергнутая альтернатива -->
- Промпт — через stdin (`-`) — так же у claude/codex/opencode, `CallAgent`
  уже принимает stdin — отвергнут плейсхолдер `{prompt}` в argv
  (экранирование, нестандарт для реестра).
- `result = "json:text"` и fenced JSON-блок в ответе — schema-флага у
  headless dsh нет, а `final.reason.kind = completed` бывает и при
  невыполненной задаче — та же схема, что у codex.
- Авторизация — подписка через маршрут `deepseek-account`: патч профиля
  `$DSH_HOME/profiles/headless/cordis.patch.yml` применяется сам, `cmd`
  без `--patch`, `DEEPSEEK_API_KEY` не нужен; auth-regex —
  `MISSING_CREDENTIAL|Authentication Fails|AUTH:` (коды и текст живых
  отказов спайка). Limit-regex без голого `429`: hex `request_id`
  auth-отказа может содержать `429`, а лимит классифицируется раньше auth
  — отказ входа стал бы получасовым ожиданием лимита.
- `auto` и `full` — оба `--permission-mode danger-full-access
  --approval-policy never`, как `auto = full` у opencode — `workspace-write`
  на Windows запрещает запись вне cwd (кэш go-build → `go test` падает),
  HTTPS и коммит из linked worktree, `--add-dir` ломает каждый
  shell-вызов, а эскалации в headless нет (`ask` отказывает без канала
  подтверждения) — отвергнут `auto = workspace-write` с instructions про
  обход: юниты падали бы на собственном `docflow verify`.
- Модель и усилие маршрута — пометка, не аргумент: флагов модели и усилия
  у headless нет, модель задаёт профиль dsh (`agent-default-model`), в
  событиях имя модели не пишется — отвергнуто выдумывать `model_args`.
- Resume — `--session-id {session}` в конце argv (dsh разбирает флаги и
  после `-`); чужой id даёт событие `error`, exit 1.
- `idle_timeout` — стандартные 1200 с: во время shell-команды поток молчит
  целиком, но тишина ограничена одной командой (pwsh по умолчанию
  обрывает её через 300 с), а открытый `tool_call` держит вызов живым;
  `instructions` просят ставить `timeoutMs` долгим проверкам.
- Токены — сумма `usage` событий `status` `step_end`; стоимости и квоты в
  потоке нет, в профиль они не идут.
- Не-действия агента (`DshSilent`) — `session`, `status`, `thinking`,
  `tool_result`, `final`, `error`; неизвестный тип события в живой строке
  показывается своим именем, в истории — `unknown` с сырой строкой.
- Комментарий над `DefaultAgents` больше не ссылается на Python CLI —
  Python-реализации CLI в репо и истории нет; причина держать байты —
  `--print-config` печатает текст для копирования.

## Gotchas           <!-- ловушки, актуальные после закрытия -->

- dsh — developer preview: декодер держится на фикстурах
  `internal/agentio/testdata/dsh*.jsonl`, снятых на 0.2.2-rc.1; новая
  версия dsh может сменить словарь событий — сначала переснять фикстуры.
- Limit-regex dsh — предположение: живой отказ по лимиту подписки не
  снят.
- `TestDshRealUnitCommits` запускает dsh с `danger-full-access`;
  классификатор авто-режима Claude Code не даёт агенту запустить его сам —
  реальный прогон делает человек. Без dsh на PATH или без логина тест
  пропускается.
- Модель подписки `deepseek-flash` в профиле dsh и
  `opencode-go/deepseek-v4.1-flash` — сравнить нельзя: в событиях имени
  модели нет.

## Follow-ups        <!-- несделанное; в Backlog — всё, кроме `Waits:` и `Accepted:` -->

- Accepted: HTTPS и кэш go-build под `danger-full-access` (Unverified
  спайка) — закрыты реальным прогоном юнита с verify и gate и зелёным
  `TestDshRealUnitCommits`.
- Waits: решение владельца о dsh вне `docflow run` — `roster.PolicyAgents`
  (roster apply не ставит dsh в ячейки), `agents.InteractiveArgv`
  (планирование в TUI через dsh — отказ `unknown agent`),
  `install-skills`/`doctor --agent dsh` (каталог скиллов dsh неизвестен).
