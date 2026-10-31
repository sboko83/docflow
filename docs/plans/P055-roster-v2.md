---
id: P055
title: Model roster v2: access catalog, conditions, resolver
type: feature
status: active
created: 2026-10-31
executor: pi | opencode-go/deepseek-v4.1-flash | high
fallback: pi | zai/glm-5.3-flash | high; pi | zai/glm-5.3 | high
cheap: pi | opencode-go/deepseek-v4.1-flash | low; pi | zai/glm-5.3-flash | low
heavy: pi | opencode-go/deepseek-v4.1-flash | max; pi | zai/glm-5.3 | high
review: codex | gpt-6.1-sol | medium; pi | zai/glm-5.3 | high
acceptance: codex | gpt-6.1-sol | medium; pi | zai/glm-5.3 | high
replan: codex | gpt-6.1-sol | medium; pi | zai/glm-5.3 | medium
permissions: auto
timeout: 7200
gate: go test -timeout 20m ./internal/roster ./cmd/docflow ./tools/bench
---
## Goal

Ростер становится отдельной подсистемой из каталога, стабильной политики и
временных условий. `docflow roster scan` сам собирает модели, контекст,
цену и ризонинг из Pi/OpenCode и известных метаданных Codex/Claude;
`roster brief` по условиям выдаёт цепочки ролей, бюджет юнита и объяснение;
`roster apply` пишет только существующие ключи плана. Пользователь говорит
условия («codex по минимуму», «локальные недоступны», «zai только flash»),
а не расставляет модели. Формат плана остаётся `docflow: 3`, раннер ростер
не читает. Приёмка: сценарий с claude/local off, codex minimal, zai flash и
opencode-go first даёт ожидаемые цепочки, проходит `roster apply` и
`run --dry-run --all`.

## Context

- docs/design/format.md:L154-211
- docs/design/runner.md:L323-374
- docs/usage.md:L569-661
- internal/roster/roster.go:L1-120
- cmd/docflow/main.go:L280-360

## Constraints

- Формат плана не меняется: `docflow: 3`, никаких новых ключей плана.
  `run`, `lint`, `cycle` не читают ростер и не получают новой логики.
- Ростер остаётся отдельным пакетом `internal/roster`; резолвер, каталог и
  политика не импортируют `internal/runner`. `apply` может использовать
  `cycle`/`format`/`lint`, как сейчас.
- Один самодостаточный бинарник, Go 1.24, CGO=0, macOS/Linux/Windows.
  Сторонние библиотеки не добавляются.
- Скан не вызывает модели и не печатает секреты. Кэш — под XDG cache,
  состояние — вне рабочего дерева; значения ключей не попадают в вывод,
  ошибки и коммиты.
- `roster apply` пишет только шапку плана (`executor:`, `fallback:`,
  `replan:` и ключи ролей), не трогает `Executor:` юнитов и не коммитит.
  Во время прогона — отказ по `run.lock`.
- Старый формат ростера читается только миграцией: после cutover v1-файл
  даёт код 2 и подсказку `docflow roster migrate`. `bench/roster.toml`
  переводится на v2; бенчмарк не перезапускается.
- Документация в `docs/` — русский; `README.md` и `README.ru.md` меняются
  вместе; `CHANGELOG.md` — под `## [Unreleased]`.
- План P052 не трогаем. В маршрутах P055 нет claude, local и
  `deepseek-v4-pro`; codex используется для review, acceptance и replan.

## Prerequisites

- `command -v pi >/dev/null && command -v opencode >/dev/null && command -v codex >/dev/null`
- Pi, OpenCode и Codex авторизованы вне репозитория; значения ключей в
  план, кэш и отчёты не попадают. Живой скан и `run --dry-run` не тратят
  квоту моделей.

## Traps

- До P055.4.6 старый и новый ростер сосуществуют: старые `show`/`apply` и
  их тесты должны оставаться зелёными. Не удалять v1-код и фикстуры раньше
  финального cutover.
- Один доступ может иметь несколько агентов (`opencode-go` через `pi` и
  через `opencode`). В цепочке fallback повторяется доступ, а не
  `agent|model`; тест на `pi + opencode-go` и `opencode + go` обязателен.
- `opencode api get /api/model` отвечает `{location, data}`; другие пути
  могут вернуть HTML. Проверять JSON и не считать HTML моделью.
- `~/.pi/agent/models-store.json` — кэш Pi и может менять форму. Читать
  терпимо, фикстуры держать в `internal/roster/testdata`, при поломке
  оставлять частичный каталог с пометкой `unknown`.
- `check = true` и `trusted = true` несовместимы: доступ, который требует
  независимой проверки, не может сам быть финальным судьёй. `minimal` без
  `trusted` не даёт ролей — это ошибка политики, а не тихое отключение.
- `models.dev` — необязательный источник; без сети остаются встроенные
  метаданные Codex/Claude. Неизвестный контекст нельзя подменять догадкой:
  резолвер должен сказать `context unknown` и не ставить модель в heavy.
- Бюджет контекста — рекомендация брифа, не новый ключ плана и не проверка
  `lint`. `roster apply` только предупреждает о юните больше бюджета.
- `--profile` — старый флаг v1, `--preset` — новый. До cutover поддержать
  оба, после — оставить только `--preset` и внятную ошибку.
- `roster apply` при ошибке `lint` восстанавливает файл; предупреждения
  должны идти в stderr и не менять код успеха.

## Decisions

- Доступ — единица квоты и независимости, а не провайдер: подписка
  `opencode-go` одна, даже если к ней ходят `pi` и `opencode`; fallback
  не повторяет доступ — отвергнуто правило «провайдер = agent или префикс
  модели», оно тратит две попытки на одну квоту.
- Каталог, политика и условия разделены: `roster.toml` — стабильные
  доступы, allowlist и доверие; `roster.state.toml` — временные
  `off/lean/minimal/prefer`, expiry и ограничения моделей; `catalog.json`
  — найденные модели и метаданные. Условия не переписывают ростер —
  отвергнуто хранение временного состояния в `spend`/профилях.
- Резолвинг — только при планировании; план хранит конкретные маршруты,
  раннер ростер не читает. Временное исключение применяется новым планом
  или явным `roster apply --refresh`, а не скрытой подменой во время
  прогона — отвергнут roster-aware `run`, он ломает воспроизводимость.
- Бюджет доступа: `prefer > normal > lean > minimal > off`. `minimal` —
  только `acceptance` и `replan`; `lean` — без `heavy`, `acceptance`,
  `replan`; `off` — ничего. Это проще доли бюджета и покрывает
  «codex по минимуму» — отвергнут числовой share на первый релиз.
- Класс модели (`cheap|standard|strong`) — автоматика по семейству
  (`flash/mini/lite → cheap`, `pro/sol/opus → strong`, остальное
  `standard`) плюс `[model]` override. Цена и контекст — из каталога,
  качество не измеряется — отвергнут скрытый рейтинг «силы» без данных.
- Контекстный бюджет юнита считается из `context - max_out` с запасом и
  множителем класса; он влияет на нарезку через скилл, но не на формат
  плана — отвергнут новый ключ `read_budget` в плане.
- `check`/`trusted` — свойства доступа: `check` исключает из review,
  acceptance и replan; отсутствие независимого ревьюера — ошибка с
  подсказками, отсутствие fallback — предупреждение. Не деградировать
  молча — отвергнут автоматический self-review.
- Естественный язык разбирает скилл планирования в маленький словарь
  флагов; CLI не парсит русский текст. Это дешевле и детерминированнее —
  отвергнут LLM-разбор внутри docflow.
- Живые квоты в v2 не опрашиваются: планирование использует auth, expiry
  и условия, а квоту по-прежнему проверяет раннер на старте. Это
  осознанная граница первого релиза — отвергнут двойной источник правды
  о квоте.
- Replan этого плана — `codex | gpt-6.1-sol | medium` (не high), запас —
  `pi | zai/glm-5.3 | medium`: правка владельца; `high` для replan
  отвергнут как лишний расход.
- Миграция v1 → v2 — `roster migrate`, затем v1 удаляется; старый формат
  даёт ошибку с путём и подсказкой. Отдельный бинарник не вводится:
  пакет + подкоманда с JSON-контрактом, чтобы вынести позже.
- Кэш с незнакомым version считается отсутствующим (пустой каталог, без ошибки) — кэш расходный, его перезапишет scan — отвергнуто: отказ models/status на кэше, записанном прошлой версией бинарника.
- Reasoning=false не перекрывает true при слиянии — прямое следствие правила «пустое не затирает непустое» — отвергнуто: tri-state указатели на каждое поле, усложняющие все сканеры.
- Agents доступа при слиянии объединяются (opencode-go через pi и opencode) — один доступ = одна единица квоты — отвергнуто: замена списка поздним источником, теряющая второй канал.
- Cost — указатель: nil значит «цена неизвестна», пустая структура не выдаёт модель за бесплатную — отвергнуто: value-структура с нулями, неотличимыми от нулевой цены.

## Phase 1: Catalog and discovery


### [x] P055.1.1 Catalog model and cache
Read:
- internal/roster/roster.go:L1-120
- internal/roster/lines.go:L53-117
- docs/design/cycle.md:L96-112
- docs/design/format.md:L270-283
Do:
- Создай `internal/roster/catalog.go` и `catalog_test.go`: типы `Catalog`, `AccessInfo`, `ModelInfo`; поля `Access`, `ID`, `Provider`, `Context`, `MaxOut`, `Reasoning`, `Efforts`, `Cost`, `Input`, `Status`, `Source`.
- Сделай `Catalog` версионированным JSON с `version`, `scanned_at`, `sources`, `accesses`, `models`; сортируй модели по `access`, затем `id`, чтобы вывод был стабильным.
- Добавь путь кэша `$XDG_CACHE_HOME/docflow/roster/catalog.json`, `~/.cache/...` без XDG; чтение отсутствующего файла — пустой каталог без ошибки, запись — атомарная через временный файл и `rename`.
- Добавь слияние источников по ключу `access/id`: более поздний источник перекрывает поля, но не затирает непустые значения пустыми; тест на приоритет и частичные данные.
Verify: `go test ./internal/roster -run 'TestCatalog'`
Traps: не писать в репозиторий; `XDG_CACHE_HOME` может быть относительным — приводить к абсолютному; на Windows путь через `filepath`.
Commit: P055.1.1: add roster catalog model
Handoff:
- Для P055.1.2: слияние источников — Catalog.Merge, каждый сканер собирает свой Catalog и вызывает Merge; ключ модели — ({Access}/{ID}), доступы склеиваются по имени, каналы Agents объединяются, а не заменяются.
- Полевого источника (per-field provenance, «сохрани источник каждого поля» из Do P055.1.2) в ModelInfo/AccessInfo нет: Source — только имя последнего источника, реально давшего хоть одно поле; если 1.2 нужна провенанс по полям, её придётся добавить (например map[string]string).
- Semantics: Context == 0 и MaxOut == 0 означают unknown, а не ноль токенов; Cost — *Cost (nil = цена неизвестна), единица — USD за миллион токенов; Reasoning в слиянии только поднимается, false не сбрасывает true.
- DefaultCachePath намеренно не использует os.UserCacheDir: по Do путь всегда $XDG_CACHE_HOME/... или ~/.cache/..., в том числе на macOS (не ~/Library/Caches); на Windows используется filepath, но поведение там не проверялось.
- Unverified: Атомарная замена файла через os.Rename проверена только на macOS (APFS); на Windows rename поверх существующего файла и поведение каталога с открытым файлом кэша не проверялись.
- Unverified: Fallback без XDG_CACHE_HOME на Windows даёт ~/.cache (по Do), а не %LocalAppData%; этот путь на Windows не запускался.

### [ ] P055.1.2 Scan Pi and OpenCode
Read:
- internal/roster/catalog.go
- internal/roster/roster.go:L55-68
- internal/testshim/testshim.go:L1-90
- docs/usage.md:L546-562
- internal/agentio/process.go:L58-75
- internal/agentio/process.go:L184-220
Do:
- Создай `internal/roster/scan.go`, `scan_test.go` и фикстуры `testdata/pi-models-store.json`, `testdata/pi-models.json`, `testdata/opencode-models.json`.
- `ScanPi`: прочитай `~/.pi/agent/models-store.json` (формат `{provider: {models: [{id, contextWindow, maxTokens, cost, reasoning, thinkingLevelMap, input, ...}]}}`) и `~/.pi/agent/models.json` (формат `{providers: {name: {baseUrl, models: [...]}}}`); если store нет, вызови `pi --list-models` и разбери таблицу `provider model context max-out thinking images` как fallback без цены; добавь канал `pi` к каждому доступ-провайдеру.
- `ScanOpenCode`: вызови `opencode api get /api/model`, разбери `data` (`providerID`, `modelID`, `limit.context`, `limit.output`, `cost`, `variants`, `capabilities`, `status`); добавь канал `opencode`; HTML или не-JSON — ошибка источника, не паника.
- Собери `AccessInfo` из провайдеров, объедини модели по `access/id`; сохрани источник каждого поля; при отсутствии Pi/OpenCode верни частичный каталог и список проблем.
- В тестах подменяй `HOME`/`XDG_CONFIG_HOME` и команду `opencode` через `testshim`; сеть не использовать.
Verify: `go test ./internal/roster -run 'TestScan'`
Traps: `models-store.json` и `models.json` — разные схемы; цена Pi/OpenCode в USD за миллион токенов, не за токен; не читать ключи из auth-файлов.
Commit: P055.1.2: scan pi and opencode catalogs

### [ ] P055.1.3 Auth and metadata enrichment
Read:
- internal/roster/catalog.go
- internal/roster/scan.go
- internal/agentio/process.go:L58-75
- internal/agentio/process.go:L184-220
- internal/agents/agents.go:L60-125
- internal/agentio/zai.go:L80-130
Do:
- Создай `internal/roster/scan_agents.go`, `scan_agents_test.go` и `testdata/models-dev.json`.
- Добавь проверку auth по доступам: `pi auth check --provider NAME --json` (статус `ready`), `opencode api get /api/provider` (`activation`), `codex login status`; для Claude — наличие конфига/успешный `claude --version`, иначе `unknown`. Никогда не вызывай `pi auth print-api-key` и не печатай значения.
- Добавь встроенную таблицу известных Codex/Claude моделей (контекст, max-out, reasoning, усилия, цена) и необязательное обогащение `https://models.dev/api.json` по флагу `--online`; кэшируй результат, при ошибке сети оставляй встроенные данные.
- В тестах используй фейковые `pi`/`opencode`/`codex` через `testshim` и фикстуру models.dev; сеть не использовать.
Verify: `go test ./internal/roster -run 'TestScanAuth|TestEnrich'`
Traps: `opencode api get /api/provider` возвращает `data` массивом; `codex login status` печатает текст, не JSON; `models.dev` ID провайдеров не совпадают с именами доступов — сопоставлять явно.
Commit: P055.1.3: scan auth and enrich model metadata

### [ ] P055.1.4 CLI scan, models, status
Read:
- cmd/docflow/main.go:L45-60
- cmd/docflow/main.go:L280-360
- cmd/docflow/help_text.go:L280-320
- internal/roster/scan.go
- internal/roster/catalog.go
- cmd/docflow/roster_acceptance_test.go:L1-60
Do:
- Добавь в `handleRoster` подкоманды `scan`, `models`, `status`; в таблицу команд и `help_text.go` — те же имена и флаги.
- `scan [--json] [--refresh] [--online] [--roster PATH]`: собирает каталог, пишет кэш, печатает сводку `access agent models auth source`; `--refresh` игнорирует свежий кэш.
- `models [ACCESS] [--json] [--all]`: таблица `access model context max-out reasoning cost source`; по умолчанию только модели доступов из ростера v1/v2, `--all` — весь каталог.
- `status [--json]`: таблица доступов с каналами, auth, количеством моделей и источником; состояния условий добавит P055.2.2.
- Старые `show`/`apply` не трогай. Тесты — на временном XDG и фейковых CLI.
Verify: `go test ./cmd/docflow -run 'TestRosterScan|TestRosterModels'`
Traps: не ломать старые тесты `roster show`; `--roster` по умолчанию `~/.config/docflow/roster.toml`; ошибки скана — код 2, частичный результат — код 0 с предупреждениями в stderr.
Commit: P055.1.4: add roster scan and models commands

## Phase 2: Policy and conditions


### [ ] P055.2.1 Policy v2 parser
Read:
- internal/roster/roster.go:L116-351
- internal/roster/lines.go:L53-117
- internal/roster/catalog.go
- docs/design/format.md:L154-173
- internal/roster/testdata/draft.toml
Do:
- Создай `internal/roster/policy2.go`, `policy2_test.go`, `testdata/policy-v2.toml`.
- Формат: `version = 2`; `[access.NAME]` с `agents` (массив `claude|codex|pi|opencode`), `kind` (`subscription|metered|local`), `budget` (`prefer|normal|lean|minimal|off`), `trusted`, `check`, `models`, `exclude`, `expires`; `[model."ACCESS/MODEL"]` с `class` (`cheap|standard|strong`); `[role]` с требованиями; `[effort]` с уровнями; `[preset.NAME]` с `off`, `lean`, `minimal`, `prefer`, `order`, `models`.
- Валидируй: неизвестные ключи и агенты, пустой `agents`, `check && trusted`, `minimal` без `trusted`, дату `YYYY-MM-DD`, глобы моделей, повтор доступа, обязательные роли, `class` и `budget` вне enum. Ошибки — `FormatError` с путём и строкой через `lineIndex`.
- Добавь `IsV2(path)`; старый парсер `Load` не меняй. Тесты: валидный файл, каждая ошибка, позиция строки.
Verify: `go test ./internal/roster -run 'TestPolicy'`
Traps: `[access.opencode-go]` и `[access.opencode]` — разные доступы; `agents` одного доступа не должны повторяться; `version` без `2` — не v2.
Role: heavy
Commit: P055.2.1: add roster policy v2 parser


### [ ] P055.2.2 Conditions state and commands
Read:
- internal/roster/policy2.go
- internal/roster/catalog.go
- internal/roster/roster.go:L55-68
- cmd/docflow/main.go:L280-360
- cmd/docflow/help_text.go:L280-320
- cmd/docflow/roster_acceptance_test.go:L1-60
Do:
- Создай `internal/roster/state.go` и `state_test.go`: файл `<roster без .toml>.state.toml` рядом с ростером (по умолчанию `~/.config/docflow/roster.state.toml`), поля `off`, `until`, `budget`, `expires`, `models`, `exclude`, `preset`; загрузка, слияние поверх политики, атомарная запись.
- Добавь подкоманды `off ACCESS [--until DATE]`, `on ACCESS`, `budget ACCESS LEVEL`, `expire ACCESS DATE`, `allow ACCESS PATTERN...`, `deny ACCESS PATTERN...`, `reset`; они пишут только state, не `roster.toml`; обнови таблицу команд в `cmd/docflow/main.go` и `help_text.go`.
- `status` показывает активные условия и их источник; `off` с прошедшей датой автоматически снимается при загрузке.
- Тесты: XDG-изоляция, `off`/`on`, `until`, `budget`, `allow/deny`, `reset`, ошибка неизвестного доступа.
Verify: `go test ./cmd/docflow -run 'TestRosterOff|TestRosterState'`
Traps: state — не политика; не переписывать ростер; даты в UTC; `allow` добавляет глоб в `models`, `deny` — в `exclude`, не дублировать.
Commit: P055.2.2: add roster conditions state and commands

### [ ] P055.2.3 Migrate v1 roster
Read:
- internal/roster/roster.go:L116-351
- internal/roster/apply.go:L1-50
- bench/roster.toml
- internal/roster/testdata/draft.toml
- internal/roster/policy2.go
Do:
- Создай `internal/roster/migrate.go`, `migrate_test.go` и `testdata/legacy-roster.toml`.
- `Migrate(text) (string, error)`: `spend` + `[provider]` + `[route]` + `[role]` + `[profile]` → v2. Провайдер `pi` с моделью `X/Y` становится доступом `X` с каналом `pi`; `codex`/`claude` — доступом с агентом; `strata` — `kind = "local"`.
- Тиры маршрутов → `class` модели; все модели провайдера попадают в `models`; `[model]` получает `class`; старые `check/trusted` переносятся; `[profile]` с `spend` → `[preset]` с `order`, без потери флагов.
- Добавь `roster migrate [--write] [--roster PATH]`: без `--write` печатает результат, с `--write` пишет `roster.toml` атомарно и оставляет `roster.toml.bak`; новая v2-загрузка (не старый `Load` до cutover) на v1-файле даёт код 2 с подсказкой.
- Тесты: `draft.toml`, `bench/roster.toml`, идемпотентность, ошибка на не-v1.
Verify: `go test ./internal/roster -run 'TestMigrate'`
Traps: `pi` без префикса — доступ `pi`; один провайдер с несколькими маршрутами одной модели и разными усилиями — не терять усилия, сохранить их в `[effort]` или `[model]`; `spend` профиля — порядок, не allowlist.
Role: heavy
Commit: P055.2.3: migrate v1 roster to v2

## Phase 3: Resolver and brief


### [ ] P055.3.1 Resolver candidates
Read:
- internal/roster/roster.go:L353-438
- internal/roster/policy2.go
- internal/roster/state.go
- internal/roster/catalog.go
- docs/design/format.md:L154-173
Do:
- Создай `internal/roster/resolve.go` и `resolve_test.go`: типы `Candidate`, `Resolution`; собери кандидатов по ролям с фильтрами `off`, `expires`, auth, `budget`, `models`/`exclude`, `class`, `context`, `reasoning`, `check/trusted`.
- Для доступа выбери один канал из `agents` (первый доступный), одну модель — минимальный класс не ниже требования роли, затем минимальную цену/вес; усилие — из `[effort]`, иначе по классу (`cheap→low`, `standard→high`, `strong→high`), с приведением к поддерживаемым уровням модели.
- `prefer` идёт раньше `normal`, `lean` — только cheap/executor/review, `minimal` — только acceptance/replan; `check` не попадает в trusted-роли; `minimal` без trusted — ошибка политики.
- Один доступ — один кандидат; не собирай цепочки в этом юните. Тесты на сценарий: claude off, local off, codex minimal, zai lean/flash, opencode-go prefer.
Verify: `go test ./internal/roster -run 'TestResolveCandidates'`
Traps: контекст неизвестен — не ставить в heavy; `check` исключает review/acceptance/replan; цена подписки не ноль, а квота — не сравнивать с meter.
Role: heavy
Commit: P055.3.1: resolve roster candidates per role


### [ ] P055.3.2 Chains, review matrix, warnings
Read:
- internal/roster/resolve.go
- internal/roster/policy2.go
- docs/design/format.md:L167-173
- internal/roster/roster.go:L353-438
Do:
- Создай `internal/roster/resolve_chain.go` и тесты: построй цепочки ролей из кандидатов, порядок `prefer > normal > lean`, без повтора доступа; `review` — только не-`check` доступы, `acceptance`/`replan` — только `trusted` и не `check`.
- Построй матрицу `check-доступ → доступные ревьюеры`; пустой ревьюер — ошибка с подсказками (`set check=false`, `allow` доступ, `off` другого); цепочка из одного маршрута — предупреждение `no fallback for <role>`, не ошибка.
- Тесты: happy path, дубль доступа, нет ревьюера, нет fallback, `minimal` не попадает в review, `lean` не попадает в heavy.
Verify: `go test ./internal/roster -run 'TestResolveChain|TestReview'`
Traps: независимость — по доступу, не по агенту; не выбирать same-access reviewer молча; ошибка должна называть условие и доступ.
Role: heavy
Commit: P055.3.2: build role chains and review matrix


### [ ] P055.3.3 Context budget and brief
Read:
- docs/design/cycle.md:L96-112
- internal/roster/catalog.go
- internal/roster/resolve_chain.go
- internal/roster/policy2.go
- docs/usage.md:L569-661
Do:
- Создай `internal/roster/brief.go`, `context.go` и тесты: для основного маршрута роли посчитай `usable = context - max_out - reserve`, `target = clamp(usable * share * class_factor, 20k, 250k)`; `share` — cheap 0.25, executor 0.35, heavy 0.45; `reserve` — 10% контекста, минимум 32k.
- Сделай текстовый и JSON-бриф: условия, цепочки, бюджет юнита, `why`, предупреждения, матрица review; не печатай весь каталог.
- Тесты: golden-вывод для сценария, неизвестный контекст (`context unknown`), отсутствие fallback, несколько доступов; стабильный порядок.
Verify: `go test ./internal/roster -run 'TestBrief|TestContext'`
Traps: не путать токены и байты; не делать бюджет жёсткой проверкой; `review/acceptance` не получают read-бюджет, только diff/test scope.
Commit: P055.3.3: render roster brief and unit budgets

### [ ] P055.3.4 Brief and explain CLI
Read:
- cmd/docflow/main.go:L280-360
- cmd/docflow/help_text.go:L280-320
- internal/roster/brief.go
- internal/roster/policy2.go
- internal/roster/state.go
- cmd/docflow/roster_acceptance_test.go:L1-60
Do:
- Добавь `brief [--json] [--preset NAME] [--off A,B] [--minimal A,B] [--lean A,B] [--prefer A,B] [--expires A=DATE] [--only A=GLOB] [--roster PATH]`; разовые флаги не пишут state.
- `explain PLAN [--json]`: загрузи план из `docs/plans`, сравни шапку с текущим брифом, напечатай `key: old → new` и предупреждения; не меняй файл.
- `show` для v2-файла — алиас `brief`; для v1 — старый вывод. `--profile` пока поддержать для v1.
- Тесты на v2-фикстуре, флаги, JSON, `explain` с расхождением.
Verify: `go test ./cmd/docflow -run 'TestRosterBrief|TestRosterExplain'`
Traps: флаги условий не должны мутировать state; `--only` — глоб по ID модели; explain не пишет и не коммитит.
Commit: P055.3.4: add roster brief and explain commands

## Phase 4: Plan integration and docs


### [ ] P055.4.1 Apply v2
Read:
- internal/roster/apply.go:L1-221
- internal/roster/apply_test.go:L1-80
- internal/roster/resolve_chain.go
- internal/roster/brief.go
- cmd/docflow/main.go:L330-360
Do:
- Создай `internal/roster/apply2.go` и тесты; в `handleRoster` для v2-файла вызывай v2, для v1 — старый `Apply`, чтобы старые тесты остались зелёными.
- v2 `apply` пишет `executor:`, `fallback:`, `replan:` и ключи ролей, которые реально используются; порядок и чужие ключи не трогает; `--preset`, `--dry-run`, `--json`.
- Добавь предупреждения: юнит с `Executor:` вне текущих условий; открытый юнит, чей `Read:`-прокси больше бюджета роли; отсутствие fallback. Предупреждения — stderr, код успеха не меняют.
- Сохрани `run.lock`-отказ, `lint`-проверку и восстановление файла; не коммить.
Verify: `go test ./internal/roster -run 'TestApplyV2|TestApply'`
Traps: не перезаписывать `Executor:` юнита; `--dry-run` не пишет файл; роль без юнитов удаляется; v1-путь не ломать.
Role: heavy
Commit: P055.4.1: apply v2 roster to plan headers


### [ ] P055.4.2 Planning skill
Read:
- skills/bsv-docflow-plan/SKILL.md:L30-122
- skills/bsv-docflow-plan/SKILL.md:L124-223
- skills/bsv-docflow-plan/SKILL.md:L263-287
- docs/usage.md:L569-661
- internal/roster/brief.go
Do:
- Замени в скилле `roster show`/`--profile` на `roster scan` + `roster brief`; survey спрашивает условия и бюджет, не модели; модель/усилие не назначаются вручную без прямой просьбы.
- Опиши перевод фраз («codex по минимуму», «локальные недоступны», «zai кончается», «zai только flash») в `--minimal/--off/--lean/--expires/--only`; покажи разобранные условия до юнитов.
- Добавь бюджет юнита из брифа в нарезку и `roster apply` с предупреждениями; удали старые инструкции про `spend`/профили; сохрани размер скилла.
- В Review оставь проверку `check`-доступов, но источник — политика v2, не `roster show`.
Verify: `grep -q 'roster brief' skills/bsv-docflow-plan/SKILL.md && grep -q 'roster scan' skills/bsv-docflow-plan/SKILL.md && grep -q 'unit budget' skills/bsv-docflow-plan/SKILL.md`
Traps: не раздувать скилл; не обещать, что CLI парсит русский; не убирать ручной `Executor:` как явное исключение пользователя.
Role: cheap
Commit: P055.4.2: plan with roster brief and conditions


### [ ] P055.4.3 Design, usage, CLI docs
Read:
- docs/design/README.md:L1-21
- docs/design/cli.md:L20-50
- docs/usage.md:L569-661
- docs/archive/P036-roster.md:L1-98
- docs/archive/P043-local-worker.md:L1-50
- docs/design/format.md:L154-211
Do:
- Создай `docs/design/roster.md`: слои каталог/политика/условия/резолвер, форматы `roster.toml` и `roster.state.toml`, правила `prefer/normal/lean/minimal/off`, `check/trusted`, бюджет контекста, команды, миграция, «раннер не читает».
- Обнови индекс `docs/design/README.md`, таблицу команд `docs/design/cli.md` и раздел «Ростер» в `docs/usage.md`; убери `--profile`/старый `show` из основного описания, оставь миграцию.
- Свяжи решения с P036/P043 и подчеркни: формат плана не меняется, `run`/`lint` ростер не читают.
- Примеры: сценарий claude/local off, codex minimal, zai flash, opencode-go first.
Verify: `docflow lint && grep -q 'roster.md' docs/design/README.md`
Traps: docs на русском; README/usage не должны обещать несуществующие команды; не дублировать весь P036.
Role: cheap
Commit: P055.4.3: document roster v2 architecture


### [ ] P055.4.4 README and changelog
Read:
- README.md:L28-36
- README.ru.md:L28-36
- CHANGELOG.md:L1-20
- docs/design/roster.md
Do:
- Обнови feature bullet в `README.md` и `README.ru.md`: скан моделей, условия, `roster brief`, планирование без ручной расстановки моделей.
- Добавь в `## [Unreleased]` секции `Added`/`Changed`: `roster scan/models/status/brief/explain`, условия, v2-политика, миграция, удаление v1.
- Не обещай живой опрос квот и не упоминай личные модели/доступы.
Verify: `grep -q 'roster brief' README.md && grep -q 'roster brief' README.ru.md && grep -q 'roster v2' CHANGELOG.md`
Traps: README/RU меняются вместе; changelog — пользовательские изменения, не внутренние тесты.
Role: cheap
Commit: P055.4.4: describe roster v2 in readme and changelog


### [ ] P055.4.5 Migrate tests, fixtures, bench
Read:
- internal/roster/roster_test.go:L1-60
- internal/roster/apply_test.go:L1-80
- cmd/docflow/roster_acceptance_test.go:L1-80
- bench/roster.toml
- tools/bench/run.go:L180-200
- tools/bench/run.go:L680-697
- internal/roster/migrate.go
Do:
- Переведи `internal/roster/testdata/draft.toml`, `bench/roster.toml` и тестовые фикстуры на v2; обнови ожидаемые цепочки `roster_acceptance_test.go` под новые правила (доступы, бюджет, `check/trusted`). Для `bench/roster.toml` подбери `budget`/`order`/`effort` так, чтобы `chains` совпали с текущими результатами; если совпадение невозможно, зафиксируй изменение в `docs/design/bench.md` и не перезапускай старые результаты.
- Обнови `tools/bench/run.go`: `rosterMetadata` читает v2, `chains` берёт из брифа/резолюции, `roster_sha` остаётся хешем файла; `roster apply P001 --roster ...` работает с v2.
- Тесты v1-миграции и v2-применения оставь; старые v1-тесты, которые больше не отражают контракт, перенеси в `legacy_test.go` или удали вместе с фикстурой.
- Проверь `go run ./tools/bench run --task small --dry-run` без вызова моделей.
Verify: `go test ./internal/roster ./cmd/docflow ./tools/bench`
Traps: не перезапускать бенчмарк и не менять `bench/results`; `chains` в результате должны быть стабильны; v1-миграция должна остаться.
Commit: P055.4.5: migrate roster fixtures and bench to v2

### [ ] P055.4.6 Remove v1 roster path
Read:
- internal/roster/roster.go:L1-502
- internal/roster/apply.go:L1-221
- internal/roster/roster_test.go:L1-120
- internal/roster/apply_test.go:L1-80
- cmd/docflow/main.go:L280-360
- cmd/docflow/help_text.go:L280-320
- docs/design/roster.md
- docs/usage.md:L569-661
Do:
- Сделай `Load` v2-единственным; удали старый разбор `spend`/`[provider]`/`[route]`/`[profile]`, старый `selectChain`/`Show` и старый `Apply`; оставь `DefaultPath`, общие типы и `migrate`.
- `roster show` — алиас `brief`; `apply` — только v2; `--profile` удали, `--preset` остаётся. v1-файл — код 2 с путём и подсказкой `docflow roster migrate`.
- Обнови тесты, help, usage и фикстуры; удали неиспользуемые legacy-тесты. Убедись, что `internal/roster` больше не содержит двух парсеров.
Verify: `go test ./internal/roster ./cmd/docflow ./tools/bench`
Traps: не удалить `migrate`; ошибка v1 должна быть actionable; `roster apply` по-прежнему не трогает `Executor:` юнитов.
Role: heavy
Commit: P055.4.6: remove v1 roster path


### [ ] P055.4.7 Review roster v2 diff
Read:
- docs/design/roster.md
- internal/roster/brief.go
- internal/roster/resolve.go
- internal/roster/resolve_chain.go
- cmd/docflow/roster_acceptance_test.go
Do:
- Проверь дифф фазы против `Goal`, `Constraints`, `## Decisions` и `Do:` каждого юнита; ревьюер ничего не правит.
- Разбери `## Test changes`: ослабленный тест без основания — дефект, `blocked` с `blocker: "plan"`; наблюдения без дефекта — пункт Handoff, не `Unverified:`.
- Проверь, что новые тесты действительно ловят: повтор доступа, отсутствие ревьюера, `minimal` вне финальных ролей, миграцию v1, отсутствие новых ключей плана.
Verify: `go test -timeout 20m ./internal/roster ./cmd/docflow ./tools/bench`
Role: review
Commit: P055.4.7: review roster v2 diff

## Phase 5: Acceptance


### [ ] P055.5.1 Accept roster v2 flow
Read:
- cmd/docflow/roster_acceptance_test.go:L1-140
- internal/roster/testdata/policy-v2.toml
- docs/design/roster.md
- internal/roster/brief.go
- README.md:L28-36
Do:
- Добавь end-to-end тест с временным XDG и фейковыми `pi`/`opencode`/`codex`: `scan --refresh` → `brief --off local --minimal codex --lean zai --only zai='*flash*' --prefer opencode-go` → план с ролями → `apply` → `run --dry-run --all`; проверь цепочки, предупреждение об отсутствии fallback и отсутствие новых ключей.
- Проверь `off claude --until`, `status`, `reset`, `roster migrate` старой фикстуры и ошибку v1 после cutover.
- Собери `bin/docflow` и прогони `bash tools/parity/run.sh bin/docflow bin/docflow`; живой смоук на этом Mac: `docflow roster scan --refresh` и `docflow roster brief --off local --minimal codex --lean zai --only zai='*flash*' --prefer opencode-go` без вызовов моделей.
- Закрой каждый `Unverified:` из Handoff юнитов: тест, `Accepted:` или оставь как риск Goal.
Verify: `go test -timeout 30m ./...`
Traps: тесты не должны читать личный ростер; живой скан не коммитить; не тратить квоту моделей на смоук; `run --dry-run` не запускает агентов.
Depends: P055.1.1, P055.1.2, P055.1.3, P055.1.4, P055.2.1, P055.2.2, P055.2.3, P055.3.1, P055.3.2, P055.3.3, P055.3.4, P055.4.1, P055.4.2, P055.4.3, P055.4.4, P055.4.5, P055.4.6, P055.4.7
Role: acceptance
Commit: P055.5.1: accept roster v2 flow
