---
id: P053
title: OpenCode adapter and go subscription routes
type: feature
status: active
created: 2026-10-30
executor: pi | zai/glm-5.3-flash | medium
fallback: codex | gpt-6-luna | high
review: codex | gpt-6.1-sol | medium
acceptance: codex | gpt-6.1-sol | high
replan: codex | gpt-6.1-sol | medium
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

- У живых отказов OpenCode в P053.1.1 наблюдался только код 1; код 0
  не воспроизведён. Классификация опирается на событие `error` и поле
  `is_error`, а не на конкретный код выхода.
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
- После P053.2.2 живой smoke запускается один раз в рамках полного
  `docflow verify P053.2.1`: его сохранённые артефакты служат для сверки
  `Do:`, отдельный предварительный smoke не нужен. Бюджет — два крошечных
  юнита: два вызова `#low` (начало и resume) и один `#high`; лимиты попыток
  соответственно 2 и 1. Отказ несуществующей модели не должен доходить до
  генерации. Не вызывать другие модели, не провоцировать исчерпание квоты
  или сетевой отказ; при неожиданном расходе/повторе остановиться и
  сохранить свидетельства, не перезапускать автоматически.

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
- Сообщение 'stream must be one of' строится из Streams динамически — почему: захардкоженный список уже отставал от codex/pi и с opencode лгал бы дальше — отвергнуто: оставить строку как есть.
- cost копится как число под json:cost, а не таблицей cost.total как у pi — почему: opencode несёт part.cost числом и профиль читает json:cost напрямую — отвергнуто: вкладывать в cost.total (потребовало бы другого пути в профиле без выгоды).
- Декодер error принимает и объект {message}, и строку — почему: живой формат — объект, но строка страхует от дрейфа формы — отвергнуто: только объект (нулевой error при дрейфе).
- error_type копится в OpencodeOutput, а не повторным сканированием stdout в Classify — почему: тип ошибки нужен классификатору, а FailureText несёт только message; повторный разбор строк дублировал бы декодер — отвергнуто: читать error.type из сырого stdout в refusal.go.
- gate: true в плане смоука — это команда shell `true`, не булев ключ: гейт проверяет механизм, не тратя подписку go на `go test ./...` во временном репо — отвергнуто: gate: go test (минуты и смысл только в checkout проекта)
- План смоука без шапочного executor:, тройки только в юнитах — почему: closeRouteLine без --close шапку не читает, dry-run --all проходит — отвергнуто: дублировать тройку в шапке
- Проверка маршрутов по строкам вывода, а не по events.jsonl — почему: Do требует «строки маршрутов», и подмена маршрута видна прямо в строке юнита — отвергнуто: разбор событий (это приёмка P053.2.1)
- Replan P053.1.5: перед повторным ревью добавлен P053.1.6 для трёх
  дефектов из блокировки: признаки отказа OpenCode проверяются по
  декодированной ошибке провайдера, перечень исполнителей в спецификации
  включает opencode, наблюдение кодов выхода ограничено подтверждённым
  кодом 1. Причина: текст ассистента с «quota» подменяет outage на limit,
  а документация расходится с lint и живыми Handoff; отвергнуто: ослабить
  ревью или объявить код 0 наблюдавшимся без живого подтверждения.
- opencodeErrorMessage принимает и строку, и объект {message} — декодер уже терпит обе формы, а классификация выживает при дрейфе — отвергнуто: считать объект пустым сообщением (отказ терялся бы молча).
- Replan P053.2.1: перед повторной приёмкой добавлены P053.2.2 для
  сохранения и машинной сверки артефактов smoke, Handoff и живого resume
  и P053.2.3 для ревью этих проверок после исполнителя с `check`.
  Причина: зелёный прежний smoke удалял свидетельства успеха и не
  доказывал событийные маршруты, запись Handoff и продолжение сессии.
  Resume вызывается одним контролируемым провалом Verify внутри первого
  из прежних двух юнитов, без третьего юнита и без дополнительных моделей;
  подготовка и ревью не тратят подписку. Отвергнуто: ослабить эталонные
  `Do:`/`Verify:` приёмки, считать два независимых юнита resume или
  повторять живой smoke ради ручного сбора артефактов.

## Phase 1: Adapter and integration

### [x] P053.1.1 Agent profile and JSON stream decoder
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
Handoff:
- Отказ маршрута (provider.no-route: несуществующая модель или вариант) приходит событием error и кодом выхода 1, а не 0 — код 0 у провайдерского отказа в живых прогнах не воспроизведён; P053.1.2 Classify должен ключиться на error.type/error.message, не на коде.
- Живая структура потока v2.2.3: события envelope step_start/tool_use/step_finish/text/error, part.type в kebab-case (step-start/step-finish/tool/text); tool_use пришёл только со state.status=completed (shell: state.input.command, state.output, title); cost на подписке go всегда 0; в tokens есть reasoning, но профиль его не суммирует (ключи usage.input/output/cache_read/cache_write).
- [agents.opencode] пока без таблицы permissions: BuildCommand с full падает 'no permissions table', auto не добавляет аргументов; P053.1.2 добавляет auto/full = ["--auto"].
- Сообщение об отказе stream теперь собирается из Streams динамически (pyRepr, join) — старый захардкоженный список '", "claude"' был враньём ещё до юнита; тестов на точный текст нет.
- Unverified: Продолжение сессии: resume_args ['--session', '{session}'] покрыт только модульным тестом BuildCommand — живой вызов с --session не сделан (закрывается smoke-прогоном P053.2.1).
- Unverified: Код выхода при отказе посреди генерации (не резолвинг маршрута): не воспроизведён — оба живых отказа вышли с кодом 1; отсутствующая проверка — живой вызов, обрывающийся провайдерской ошибкой в момент стрима (важно для Classify в P053.1.2).

### [x] P053.1.2 Liveness, refusals, and permissions
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
Handoff:
- Живой поток v2.2.3 не даёт running/pending: даже sleep 15 пришёл одним tool_use со state.status completed (пауза 16.5 c без строк между step_start и tool_use) — ветка running в Liveness мертва на этой версии, инструменты у opencode не считаются, живость держится только на строках потока и idle_timeout; записано в docs/design/runner.md (Поток событий).
- OpencodeOutput теперь кладёт error_type (error.type события error) под json:error_type — Classify ключует provider.no-route по нему; строковая форма error (дрейф) оставляет error_type nil; MessageText/FailureText без изменений.
- Профиль получил [agents.opencode.permissions] auto/full = ["--auto"]; --auto в argv встаёт сразу после cmd (--agent build) и перед --model; закреплённый SHA-256 DefaultAgents в internal/agents/agents_test.go обновлён (1975e077…).
- Unverified: Ветвь running/pending в Liveness покрыта только синтетикой: живой поток этих событий не отдаёт (наблюдение v2.2.3), поэтому поведение на реальном стриме с running не проверено — отсутствует живая версия opencode, которая их шлёт.
- Unverified: Ключевые слова лимита (429/rate limit/usage limit/quota) и простоя (5xx/overloaded/timeout/connection) в Classify проверены синтетикой: живого сообщения отказа opencode с этими словами нет в фикстурах (там только provider.no-route) — отсутствует захват реального limit/outage-сообщения подписки.

### [x] P053.1.3 CLI, lint, skills, and release integration
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
Handoff:
- «Подсказка doctor про PATH» истолкована как проза в help `install` — «agent CLIs in PATH (claude, codex, pi, opencode)»: другой подсказки про PATH со списком агентов нет, у doctor только выбор --agent; ветка `pi --version` в doctor не тронута.
- opencode добавлен и в третий цикл release.go — удаление личных ссылок в `docflow uninstall` (диапазон L445-460 из Read:), иначе uninstall оставлял бы ссылки в ~/.config/opencode/skills; формулировка Do называла только два цикла.
- Порядок значений везде — opencode последним (claude, codex, pi, opencode), как в Streams из internal/agents; docs/design/cli.md и skills.md всё ещё пишут {claude,codex,pi} — их обновляет P053.1.4.
- Unverified: Релизные пути для opencode без отдельного теста: release-тесты подменяют PATH заглушками только claude/codex/pi, поэтому новый элемент releaseSkillOptions/uninstall выполняется лишь через общий цикл LookPath — не хватает проверки (заглушка opencode в release-тесте или живой smoke P053.1.4), что `docflow install` кладёт ссылки в ~/.config/opencode/skills при реальном opencode в PATH.

### [x] P053.1.4 Docs and live smoke script
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
Handoff:
- tools/smoke-opencode.sh: живой режим в этом юните не запускался — он и есть приёмка P053.2.1; без opencode в PATH живой режим падает сразу с понятной ошибкой, --dry-run opencode не требует
- Скрипт проверяет маршруты grep-ом по строкам юнитов текстового вывода ('P001.1.x Write ... -> opencode | opencode-go/deepseek-v4.1-flash | low|high'): тихое переключение на fallback сломает эти grep-ы — это намеренно
- При провале скрипт сохраняет временный репозиторий и печатает его путь, при успехе удаляет через trap; живой режим запускать только вне этого checkout (скрипт всегда работает во временном каталоге)
- Unverified: Живой режим tools/smoke-opencode.sh не прогонялся: путь run --yes --no-notify, проверка коммитов и grep-ов строк маршрутов против реального вывода раннера не исполнен — отсутствующая проверка: сам живой смоук (P053.2.1, тратит квоту go)
- Unverified: Скрипт проверен только на macOS (/bin/bash 3.2, bash -n + dry-run): работа под Git Bash на Windows (суффикс go env GOEXE, mktemp) не запускалась — отсутствующая проверка: запуск скрипта на Windows

### [x] P053.1.6 Fix refusal classification and specification
Read:
- internal/agentio/refusal.go
- internal/agentio/refusal_test.go
- internal/agentio/output.go:L133-192
- docs/design/format.md#план
- docs/design/runner.md#резервные-маршруты-и-таймаут
Do:
- В ветке OpenCode функции Classify проверять признаки limit и outage
  по декодированному сообщению ошибки провайдера (`json:error`), а не
  по всему FailureText; provider.no-route по error_type остаётся outage.
  Reset лимита также извлекать из сообщения ошибки. Сохранить текст
  диагностики и запасной regex `limit` профиля.
- Добавить регрессионный тест потока с текстом ассистента
  «Updated quota documentation.» и событием error с сообщением
  «connection reset by peer»: результат outage, а не limit.
  Сохранить существующие проверки limit, outage и provider.no-route.
- В docs/design/format.md включить opencode в допустимые значения
  agent тройки исполнителя, согласовав спецификацию с текущим lint
  без изменения версии формата.
- В docs/design/runner.md убрать утверждение «наблюдались и 0, и 1»:
  по Handoff P053.1.1 живые отказы дали только код 1, код 0 не
  воспроизведён; сохранить независимость классификатора от кода выхода.
Verify: `go test ./internal/agentio && docflow lint`
Traps: FailureText включает текст ассистента и не подходит для признаков
OpenCode; синтетический код выхода не является живым наблюдением;
завершённые юниты и их Handoff не менять.
Depends: P053.1.1, P053.1.2, P053.1.3, P053.1.4
Commit: P053.1.6: fix opencode refusal classification
Handoff:
- Classify (opencode) сканирует только декодированное сообщение ошибки провайдера: json:error как строку, а при дрейфе — поле message объекта; FailureText (текст ассистента, stderr) потоковым regex больше не отдаётся. Сообщение, не совпавшее ни с одним regex, даёт отказ-NULL: у профиля opencode нет ключа limit, запасной LimitText для него no-op (остался для claude/codex/pi и legacy).
- usageResetInText для opencode идёт по сообщению ошибки: метка сброса, процитированная в тексте ассистента, больше не заполняет Refusal.Reset.
- Unverified: Живой сообщения отказа подписки go с формулировкой limit/outage по-прежнему нет: новый тест — синтетический поток, и если реальный limit-отказ несёт слова лимита не в error.message (а, например, в заголовке или тексте ассистента), классификация его пропустит — отсутствующая проверка: захват реального limit/outage-сообщения живым стримом (смоук P053.2.1 тратит квоту go).

### [x] P053.1.5 Review the phase
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
Depends: P053.1.1, P053.1.2, P053.1.3, P053.1.4, P053.1.6
Role: review
Commit: P053.1.5: review opencode adapter
Handoff:
- Blocked (plan): Нужен исправляющий юнит. 1) internal/agentio/refusal.go:91–95 проверяет весь FailureText вместо ошибки провайдера. Воспроизведено: текст ассистента «Updated quota documentation.» и error.message «connection reset by peer» дают limit вместо outage. Проверять признаки OpenCode по декодированной ошибке, добавить регрессионный тест. 2) docs/design/format.md:159 перечисляет только claude/codex/pi, хотя lint теперь принимает opencode; обновить спецификацию. 3) docs/design/runner.md:413 утверждает «наблюдались и 0, и 1», хотя Handoff фиксирует только код 1; убрать неподтверждённое наблюдение.
- Ревью диффа 5ef9797..HEAD завершено: новых дефектов нет; три замечания предыдущего ревью исправлены в P053.1.6.
- Все пять пунктов Test changes оправданы изменениями фазы; ослабления проверок нет.
- На этом Mac полный docflow verify P053.1.5 прошёл: Verify и gate — go test ./..., оба exit 0. Для записи лога и fingerprint потребовались расширенные права. Код и план ревьюер не менял; живая приёмка остаётся в P053.2.1.

## Phase 2: Acceptance

### [ ] P053.2.2 Preserve smoke evidence and exercise session resume
Read:
- tools/smoke-opencode.sh
- internal/runner/events.go
- internal/runner/events_test.go
- internal/runner/liveness_acceptance_test.go
- internal/runner/acceptance_fallback_test.go
- internal/agentio/testdata/opencode-result.jsonl
- docs/design/runner.md#result-json
- docs/design/runner.md#резервные-маршруты-и-таймаут
Do:
- Доработать `tools/smoke-opencode.sh`, не меняя адаптер и формат событий:
  сохранять вне checkout уникальный каталог свидетельств как при успехе,
  так и при отказе; напечатать абсолютный путь. Сохранить временный
  репозиторий с итоговым планом и коммитами, stdout/stderr, сырые потоки
  попыток, events.jsonl, коды выхода и сводку времени/стоимости каждого
  юнита. Отсутствие обязательного артефакта завершает smoke ошибкой.
- Добавить машинную сверку всех маршрутов попыток в events.jsonl со
  строками вывода: первый юнит только opencode-go/deepseek-v4.1-flash
  low, второй только той же модели high, агент opencode, без fallback.
  Проверить два done, ровно два коммита юнитов и непустой Handoff каждого
  в итоговом плане, соответствующий разобранному result JSON его успешной
  попытки; не принимать сам заголовок Handoff за свидетельство.
- В первом из двух smoke-юнитов организовать один контролируемый провал
  Verify после валидного result JSON. Управлять одноразовым условием
  со стороны harness вне файлов, порученных агенту; следующая проверка
  проходит. Через прозрачную обёртку OpenCode, делегирующую реальному CLI,
  записать argv и stdout попыток: второй вызов обязан содержать
  `--session` с sessionID первого потока, а поток продолжения — тот же
  sessionID. Отсутствие сессии, новый запуск вместо resume или лишняя
  попытка — ошибка smoke. Первый юнит ограничить двумя попытками, второй
  одной; оба остаются крошечными и используют только low/high.
- Добавить в smoke изолированный сценарий opencode-go/no-such-model без
  fallback с одной попыткой и без ожидания: код 7, диагностический отказ,
  нет done и коммита юнита; сохранить его артефакты отдельно от двух
  успешных юнитов. Не воспроизводить реальный limit/outage тратой квоты.
- Добавить `tools/test-smoke-opencode.sh` с подставным CLI без сетевых
  вызовов: положительный цикл с resume и отрицательные случаи подмены
  маршрута в событиях, отсутствующего/несовпавшего Handoff, новой сессии
  вместо продолжения и пропавших артефактов. Каждый отрицательный случай
  должен приводить к ненулевому коду проверки. Реальные вызовы подписки
  оставить только P053.2.1, тестовые потоки не выдавать за живые фикстуры.
Verify: `bash -n tools/smoke-opencode.sh tools/test-smoke-opencode.sh && bash tools/test-smoke-opencode.sh && bash tools/smoke-opencode.sh --dry-run && go test ./...`
Traps: прежний EXIT trap удаляет успешный репозиторий; `$work.out` лежит
рядом, а не внутри него. Маршрут из dry-run и наличие resume_args в
профиле не доказывают маршрут попытки и живой resume. Не запускать живой
smoke в этом юните; не менять эталонную приёмку.
Depends: P053.1.1, P053.1.2, P053.1.3, P053.1.4, P053.1.5, P053.1.6
Commit: P053.2.2: preserve smoke evidence and verify resume

### [ ] P053.2.3 Review smoke evidence checks
Read:
- tools/smoke-opencode.sh
- docs/design/runner.md#result-json
Do:
- Разобрать дифф P053.2.2 против его Do, Goal, Constraints и Decisions:
  артефакты сохраняются, проверки событий и Handoff не подменены grep
  консольного вывода, resume подтверждается argv и одинаковым sessionID,
  число живых вызовов ограничено и отрицательные случаи действительно
  отвергаются. Новый тестовый скрипт прочитать из диффа P053.2.2.
- Разобрать каждый пункт Test changes; ослабление проверки или дефект —
  blocked с blocker plan. Код не менять, подписку не расходовать.
Verify: `go test ./...`
Depends: P053.2.2
Role: review
Commit: P053.2.3: review smoke evidence checks

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
Depends: P053.1.1, P053.1.2, P053.1.3, P053.1.4, P053.1.5, P053.1.6, P053.2.2, P053.2.3
Role: acceptance
Commit: P053.2.1: accept opencode on go subscription
Handoff:
- Blocked (plan): Verify зелёный, но обязательная сверка маршрутов по событиям, проверка записанного Handoff и живой resume не выполнены. Нужен шаг сохранения артефактов smoke и явной проверки resume, затем повторная живая приёмка с учётом ограничения расхода подписки.
