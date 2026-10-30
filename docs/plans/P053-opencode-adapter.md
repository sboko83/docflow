---
id: P053
title: OpenCode adapter and go subscription routes
type: feature
status: draft
created: 2026-10-30
executor: pi | strata/swift-1.5-iq3_s | high
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
review: codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
acceptance: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
gate: go test ./...
---
## Goal

`docflow run` ведёт план встроенным исполнителем `opencode` на подписке
go: маршрут `opencode | opencode-go/deepseek-v4.1-flash | low|high`
проходит цикл юнита целиком — промпт, поток событий, продолжение сессии,
result JSON, живость и классификация отказов. Скиллы ставятся
`docflow install-skills --agent opencode`, README и docs описывают адаптер.
Проверено тестами и живым прогоном двух юнитов на `#low` и `#high`.

## Context

- docs/design/runner.md#result-json
- docs/design/runner.md#конфиг-и-маршруты
- docs/design/runner.md#резервные-маршруты-и-таймаут
- docs/design/cli.md#cli-docflow
- docs/design/skills.md#установка-скиллов
- docs/design/format.md#план

`opencode v2.2.3` с `run --format json` пишет JSON Lines, а не один JSON:
события `step_start`, `text`, `tool_use`, `step_finish`, `error`. В каждой
строке есть `sessionID`; `step_finish.part` несёт `cost` и `tokens.input`,
`tokens.output`, `tokens.reasoning`, `tokens.cache.read`, `tokens.cache.write`;
`tool_use.part.state.input.command` и `state.status` описывают вызов
инструмента. Отказ провайдера приходит событием `error` с полем
`error.message` и **кодом выхода 0**. Промпт без аргумента читается из stdin
(проверено), модель и вариант задаются одной строкой
`--model provider/model#variant`, флаг `--auto` снимает подтверждения.

## Constraints

- Один самодостаточный бинарник: Go 1.24+, CGO=0, без новых зависимостей;
  macOS, Linux и Windows остаются целевыми платформами.
- Встроенные профили `claude`, `codex`, `pi` не меняют поведение; формат
  плана `docflow: 3` и события `v: 1` не меняются.
- `opencode` — внешний CLI, docflow его не устанавливает. Адаптер —
  встроенный профиль: режим исполнителей работает без `agents.toml`,
  `$DOCFLOW_AGENTS` и `--agent/--model/--effort`.
- Фикстуры — только stdout живого вызова; токены, ключи и домашние пути в
  репозиторий не попадают.
- Квоты подписки в этом плане нет: opencode не отдаёт окна подписки
  командой, пригодной для зонда. Это известное ограничение, оно описано в
  docs; отдельный зонд — follow-up, не этот план.
- У opencode нет песочницы: `auto` и `full` оба передают `--auto`, то есть
  авто-подтверждение вместо изоляции. Docs говорят это прямо, `full` не
  выдаётся за песочницу.
- Живые вызовы — только `opencode-go/deepseek-v4.1-flash` с вариантами
  `#low` и `#high`, короткие промпты и два крошечных юнита. Другие модели
  подписки не тратятся.
- Приёмка идёт на этом Mac; Windows-сценарии проверяются тестами и
  сборкой, не живым прогоном.

## Prerequisites

- OpenCode CLI установлен: `opencode --version`
- Подписка go авторизована: `opencode auth list | grep -q 'OpenCode Console'`

## Traps

- `opencode run` возвращает код 0 и на отказе провайдера: успех нельзя
  определять по коду выхода, только по событию `error` и полю `is_error`.
- Вывод — JSON Lines. `ParseJSON(stdout)` вернёт последнюю строку
  (`step_finish`), а не ответ агента; разбор обязан идти по событиям.
- Модель и вариант — один аргумент `--model model#variant`. Раздельные
  `model_args` + `effort_args` дают `#low` отдельным словом и не работают.
- `tool_use` в `--format json` может приходить только с
  `state.status: completed`; тогда «инструмент в работе» по потоку не
  виден. Проверить длинным инструментом и записать вывод в Handoff, а не
  угадывать.
- Промпт идёт в stdin: `opencode run` без сообщения читает stdin. Аргумент
  `{prompt}` не нужен и на Windows рискует упереться в длину командной
  строки.
- OpenCode читает ещё `.claude/skills` и `.agents/skills`. Установка для
  `codex` может случайно сделать скиллы видимыми; `--agent opencode`
  обязан ставить их в `.opencode/skills` и `~/.config/opencode/skills`.
- Без `--agent build` OpenCode может выбрать другой основной агент, в том
  числе без права записи. Профиль фиксирует инструментальный агент.
- Живой smoke-скрипт запускает `docflow run`; запускать его только во
  временном репозитории, иначе он перезапишет `docs/docflow.md` этого
  checkout.

## Decisions

- Плейсхолдер `{model_variant}` = `model#effort` (или просто `model` без
  усилия) — почему: OpenCode принимает модель и вариант одним флагом, а
  docflow хранит их отдельными полями маршрута; отвергнуто: писать `#` в
  профиле жёстко (ломает модели без варианта) и требовать `model#low` в
  тройке плана (дублирует `effort` и расходится с ролями ростера).
- Собственный декодер `stream = "opencode"` — почему: форматы `claude`,
  `codex` и `pi` не знают событий OpenCode; отвергнуто: переиспользовать
  `pi` или `codex` и терять сессию, стоимость и ошибки.
- `auto` и `full` оба дают `--auto` — почему: у OpenCode нет песочницы, а
  `--auto` снимает подтверждения, не отменяя явные `deny` пользователя;
  отвергнуто: объявить `full` неподдерживаемым (пользователь останется без
  режима) и выдавать `auto` за изоляцию.
- Квоты нет — почему: подписка go не отдаёт окна командой, а локальная
  статистика OpenCode не является окном подписки; отвергнуто: парсить
  внутреннюю базу OpenCode.
- Фикстуры — реальный stdout v2.2.3 в `internal/agentio/testdata` — почему:
  контрактные тесты ловят дрейф формата; отвергнуто: синтетические события,
  придуманные по документации.

## Phase 1: Adapter and integration

### [ ] P053.1.1 Agent profile and JSON stream decoder
Read:
- internal/agents/agents.go:L35-150
- internal/agents/agents.go:L894-950
- internal/agentio/output.go:L1-172
- internal/agentio/output.go:L375-458
- internal/agents/agents_test.go:L1-120
- internal/agentio/output_test.go:L1-120
Do:
- Снять живые фикстуры: в временном каталоге выполнить
  `opencode run --format json --model 'opencode-go/deepseek-v4.1-flash#low' --agent build --auto`
  с промптом, который просит один вызов shell и ответ, заканчивающийся
  блоком ```json с ключами `status`, `handoff`, `decisions`, `unverified`,
  `reason`, `blocker`; сохранить stdout в
  `internal/agentio/testdata/opencode-result.jsonl`.
- Снять вторую фикстуру с несуществующей моделью
  `opencode-go/no-such-model`: событие `error` и код 0 — в
  `internal/agentio/testdata/opencode-error.jsonl`.
- Добавить в `DefaultAgents` профиль `[agents.opencode]`: `cmd` =
  `["opencode", "run", "--format", "json", "--agent", "build"]`,
  `model_args` = `["--model", "{model_variant}"]`, `resume_args` =
  `["--session", "{session}"]`, `result` и `text` = `json:text`,
  `session` = `json:session`, `error` = `json:is_error`, `cost` =
  `json:cost`, `output_tokens` = `json:usage.output`, `tokens` =
  `json:usage.input`, `json:usage.cache_read`, `json:usage.cache_write`,
  `stream` = `"opencode"`, `timeout` = 7200, `idle_timeout` = 1200,
  `limit_wait` = 1800.
- Добавить `opencode` в `Streams` и `{model_variant}` в `PlaceholderRE`;
  в `BuildCommand` вычислять `model_variant` как `model`, а при непустом
  `effort` — `model + "#" + effort`.
- Реализовать `OpencodeOutput`: текст последнего сообщения ассистента
  (группировать `text` по `part.messageID`, новое сообщение начинает текст
  заново), `session` из последнего `sessionID`, сумма `part.cost` и
  `part.tokens.*` по `step_finish`, `is_error` и `error` из события
  `error`; подключить в `AgentOutput` до общего `ParseJSON`.
- Тесты: разбор обеих фикстур (текст содержит result JSON, сессия,
  токены и стоимость, `is_error`), `BuildCommand` для `low`, `high` и
  маршрута без усилия (`#` не появляется), неизвестный `stream` по-прежнему
  отвергается.
Verify: `go test ./internal/agents ./internal/agentio`
Traps: код 0 у отказа; `ParseJSON` на JSON Lines; не срезать блок ```json
из текста; не переименовывать ключи `usage` — их читают `tokens` и
`output_tokens`.
Commit: P053.1.1: add opencode agent profile and stream decoder

### [ ] P053.1.2 Liveness, refusals, and permissions
Read:
- internal/agentio/liveness.go
- internal/agentio/output.go:L247-373
- internal/agentio/refusal.go
- internal/agentio/usage.go:L1-60
- internal/agentio/liveness_test.go
- internal/runner/helpers.go:L90-170
Do:
- Проверить длинный инструмент: `opencode run --format json` с промптом
  «выполни `sleep 15`, затем ответь ok» и `--auto`; посмотреть, приходит ли
  `tool_use` со `state.status` `running` до завершения. Записать наблюдение
  в Handoff.
- Расширить `Liveness` веткой `opencode`: `step_start` — ход; `tool_use` со
  статусом `running`/`pending` открывает инструмент по `part.id`, со
  статусом `completed`/`error` закрывает; `step_finish` закрывает
  оставшиеся инструменты шага. Если поток не даёт `running`, инструменты
  не считаются и это отражено в Handoff и docs.
- Расширить `StreamEvent` для `opencode`: `text` — `assistant_text`;
  `tool_use` — `activity` вида `"<tool> <title или command>"`, обрезанный
  `Clip`; `step_start` и `step_finish` — без события.
- Добавить в `Classify` ветку `opencode`: читать `error.message` и
  `error.type`; `provider.no-route` — `outage` (маршрут непригоден,
  срабатывает fallback), HTTP 429 / `rate limit` / `usage limit` / `quota`
  — `limit`, 5xx / `overloaded` / `timeout` / `connection` / `ECONNREFUSED`
  — `outage`; regex профиля `limit` остаётся запасным путём.
- Профиль `[agents.opencode.permissions]`: `auto = ["--auto"]`,
  `full = ["--auto"]`; в `instructions` ничего не добавлять.
- Тесты: живость на фикстуре результата и на синтетическом `running`
  инструменте, `StreamEvent` (текст и activity), `Classify` на
  `opencode-error.jsonl`, `BuildCommand` включает `--auto` в обоих режимах.
Verify: `go test ./internal/agentio ./internal/runner`
Traps: не закрывать инструмент по `step_finish` до добавления события
`running`, иначе счётчик всегда 0; `error.type` — не HTTP-статус; не
подменять общий regex `limit` профиля.
Commit: P053.1.2: track opencode liveness and refusals

### [ ] P053.1.3 CLI, lint, skills, and release integration
Read:
- internal/lint/lint.go:L30-45
- internal/lint/lint.go:L415-445
- internal/skills/skills.go:L24-45
- internal/skills/skills.go:L120-150
- cmd/docflow/main.go:L28-55
- cmd/docflow/help_text.go:L1-70
- cmd/docflow/help_text.go:L250-310
- cmd/docflow/release.go:L175-200
- cmd/docflow/release.go:L280-300
- cmd/docflow/release.go:L445-460
- tools/install-dev.sh:L130-160
Do:
- Добавить `opencode` в `executorAgents` (`internal/lint`), чтобы
  `executor: opencode | …` и `Role:` проходили `lint`.
- Добавить каталоги скиллов: проект — `.opencode/skills`, личный —
  `.config/opencode/skills` (`AgentSkillDirs` и `AgentUserSkillDirs`).
- Добавить `opencode` в списки выбора `--agent` у `init`, `install-skills`,
  `doctor` (`main.go` и `help_text.go`); в подсказку `doctor` про PATH.
- Добавить `opencode` в циклы `release.go` (автоустановка скиллов и
  подсказки устаревших копий) и в цикл агентов `tools/install-dev.sh`.
- Обновить ожидаемые строки в тестах: `internal/lint/lint_test.go`
  (список известных агентов), `internal/skills/skills_test.go` (каталоги и
  подсказки), `cmd/docflow/main_test.go` (значения `--agent`).
Verify: `go test ./internal/lint ./internal/skills ./cmd/docflow`
Traps: help-тексты сверяются тестами посимвольно; порядок значений в
`--agent` виден в usage; `doctor` ветку `pi --version` не трогать; скрипт
устанавливает скиллы только для CLI, найденных в PATH.
Commit: P053.1.3: expose opencode in CLI and skills

### [ ] P053.1.4 Docs and live smoke script
Read:
- docs/usage.md#автозапуск
- docs/usage.md#конфиг-агентов
- docs/design/runner.md#result-json
- docs/design/runner.md#конфиг-и-маршруты
- docs/design/cli.md#cli-docflow
- docs/design/skills.md#установка-скиллов
- README.md:L28-40
- README.md:L128-142
- README.ru.md:L118-140
- skills/bsv-changelog/SKILL.md
- CHANGELOG.md:L1-20
Do:
- В `docs/usage.md` добавить профиль `opencode`: команда, промпт в stdin,
  `--model provider/model#variant`, `--session`, `--auto`, отсутствие
  квоты, пример тройки
  `opencode | opencode-go/deepseek-v4.1-flash | high`; в таблицу ключей —
  `stream = "opencode"` и плейсхолдер `{model_variant}` в списке.
- В `docs/design/runner.md` добавить `opencode` в перечень декодеров
  потока и в раздел живости (какие события считаются ходом и
  инструментом); в отказы — `provider.no-route` как `outage` и код 0 у
  события `error`.
- В `docs/design/cli.md` и `docs/design/skills.md` добавить `opencode` в
  `--agent` и каталоги `.opencode/skills` / `~/.config/opencode/skills`.
- В `README.md` перечислить OpenCode среди встроенных исполнителей и
  показать `docflow install-skills --agent opencode`; то же в
  `README.ru.md` (файлы меняются вместе).
- Добавить запись в `## [Unreleased]` → `### Added` по `bsv-changelog`:
  поддержка OpenCode и подписки go, маршруты с вариантом.
- Создать `tools/smoke-opencode.sh`: создаёт временный git-репозиторий,
  `docflow init`, план из двух юнитов (`Executor:` с `#low` и `#high`),
  `gate: true`; режим `--dry-run` собирает маршруты через
  `run --dry-run --all` без вызова модели, обычный режим запускает
  `run --yes --no-notify` и проверяет два коммита и строки маршрутов;
  каталог удаляется через trap; бинарник — `bin/docflow` или `--binary`.
  Скрипт не трогает текущий checkout.
Verify: `bash tools/smoke-opencode.sh --dry-run && grep -q 'model_variant' docs/usage.md`
Traps: README и README.ru.md — вместе; `docs/` по-русски, README и
CHANGELOG по-английски; в smoke-скрипте `docflow init` обязателен, иначе
`run` не найдёт `docs/docflow.md`; не запускать живой режим из этого
checkout.
Commit: P053.1.4: document opencode and add live smoke

### [ ] P053.1.5 Review the phase
Read:
- docs/design/runner.md#result-json
Do:
- Разобрать диф из `## Phase diff` против Goal, Constraints, `## Decisions`
  и `Do:` каждого юнита фазы.
- Каждый пункт `## Test changes` разобрать: оправдан диффом — пропустить;
  тест ослаблен ради зелёного `Verify:` — `blocked` с `blocker: "plan"`.
- Ничего не править; наблюдения без дефекта — в Handoff, не в `Unverified`.
Verify: `go test ./...`
Traps: ревьюер не меняет код; дефект — только `blocked`, не молчаливая
правка.
Depends: P053.1.1, P053.1.2, P053.1.3, P053.1.4
Role: review
Commit: P053.1.5: review opencode adapter

## Phase 2: Acceptance

### [ ] P053.2.1 Accept opencode on the go subscription
Read:
- tools/smoke-opencode.sh
- internal/agentio/testdata/opencode-result.jsonl
- internal/agentio/testdata/opencode-error.jsonl
- docs/design/runner.md#result-json
Do:
- Собрать `bin/docflow` и запустить `bash tools/smoke-opencode.sh` на живых
  `#low` и `#high`: оба юнита `done`, два коммита, маршруты в выводе и в
  `<git-dir>/docflow/run/*.events.jsonl` совпадают, result JSON разобран
  (Handoff записан), переключения на fallback нет.
- Проверить отказ: во временном репозитории запустить план с
  `opencode-go/no-such-model` без fallback; ожидается остановка с кодом 7
  и строкой отказа, а не «успешный» юнит.
- Прогнать `go test ./...`; каждый `Unverified:` пункт Handoffs фазы 1
  закрыть тестом, строкой `Accepted: <пункт> — <почему не риск>` или
  оставить `Unverified:` только при разрыве с Goal.
- Записать в Handoff: пути логов и событий, время и стоимость обоих
  юнитов, наблюдение о `running`-инструменте из P053.1.2.
Verify: `bash tools/smoke-opencode.sh && go test ./...`
Traps: живой прогон тратит квоту go — только две крошечные единицы;
smoke-скрипт обязан работать во временном каталоге; тихое переключение на
fallback маскирует поломку — сверять маршрут в событиях.
Depends: P053.1.1, P053.1.2, P053.1.3, P053.1.4, P053.1.5
Role: acceptance
Commit: P053.2.1: accept opencode on go subscription
