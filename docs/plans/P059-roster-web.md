---
id: P059
title: roster web
type: feature
status: active
created: 2026-11-07
permissions: full
gate: go test -short -timeout 20m ./...
executor: pi | zai/glm-5.3-flash | high
fallback: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6-luna | medium
review: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6.1-sol | medium
acceptance: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6.1-sol | medium
replan: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6.1-sol | medium
---
## Goal

Ростер редактируется из браузера: `docflow roster web` поднимает локальный
сервер со встроенным UI (макет — `docs/design/roster-web-mockup.html`),
который правит базовый ростер (`roster.toml`) и его варианты
(`roster.variants.toml`). Класс модели принадлежит паре «модель + ризонинг»:
`deepseek@low = cheap`, `deepseek@high = standard` — один ключ `[route]`
заменяет `[model]` и `[effort]`, версия политики 3. TUI не трогаем.

## Context

- docs/design/roster.md
- docs/design/roster-web-mockup.html
- docs/design/cli.md
- internal/roster/policy2.go
- internal/roster/resolve.go
- internal/roster/resolve_chain.go
- internal/roster/brief.go
- internal/roster/workspace.go
- internal/roster/migrate.go
- internal/roster/variants.go
- internal/roster/state.go
- cmd/docflow/main.go

## Constraints

- Формат плана не меняется: `run`, `lint`, `cycle` ростер не читают,
  `docflow: 3` без новых ключей.
- TUI (`docflow tui`) не трогаем: экран ростера остаётся на `LoadWorkspace`
  с прежней сигнатурой; правки ростера уходят в веб.
- Самодостаточный бинарник: никаких новых Go-зависимостей; фронтенд —
  нативный HTML/JS без сборки и без внешних CDN (иконки из макета на lucide
  CDN заменяются инлайном или текстом).
- `roster scan` остаётся единственным писателем каталога; веб читает кэш
  каталога и не сканирует сам.
- `bench/roster.toml` переводится на v3 вручную; бенчмарк не перезапускается
  (как при переходе v1→v2), изменившиеся цепочки фиксируются в
  `docs/design/bench.md`.

## Traps

- Разбор политики строгий: неизвестный ключ, раздел или дубликат — ошибка с
  файлом и строкой; каждое новое правило разбора получает тест на ошибку
  рядом с `policy2_test.go`.
- Подсказка про `--profile` печатает `PolicyVersion` — после подъёма до 3
  текст подсказки меняется сам, а тесты на текст подсказки надо обновить.
- `roster.state.toml` не меняется вовсе: его `version` — версия формата
  условий, не политики; allowlist-глобы условий идут по id модели и от
  усилий не зависят.
- Ключ `"-"` в `[route]` — «усилие не передавать», строковый ключ TOML.
- Тесты сервера слушают `127.0.0.1:0` и закрывают listener; Windows CI
  гоняет те же тесты.
- `internal/roster` не импортирует `internal/runner`; веб-сервер живёт в
  `internal/roster/web` и импортирует только `internal/roster`.

## Decisions

- `[route."ACCESS/MODEL"]` с парами `"усилие" = "класс"` заменяет `[model]`
  (класс) и `[effort]` (пин усилия) — класс принадлежит паре модель+усилие,
  одна модель может быть cheap на low и standard на high; два старых ключа
  удаляются из разбора. Отвергнуто: оставить `[model]`/`[effort]` рядом с
  `[route]` — три способа выразить одно, freeze-правило требует замены.
- Версия политики 2 → 3; v2-файл любой командой — код 2 с подсказкой
  `docflow roster migrate`; миграция идёт цепочкой v1→v3 с `.bak`
  (`MigrateFile` уже так работает для v1→v2 — расширить на v2→v3).
- Ключ маршрута при миграции: пин усилия из `[effort]` или `"-"`; класс из
  `[model]`, а без записи — автоматический класс по имени модели в
  резолвере (в файле не пишется, `auto` — не значение формата).
- `roster web` — новая подкоманда CLI — обобщает существующие способы правки
  политики: ручное редактирование `roster.toml` и машинную запись вариантов
  из TUI (`SaveVariants`). TUI остаётся читателем (решение пользователя
  2026-11-07: «tui пока не трогаем»).
- `roster.toml` становится машинно-записываемым: `SavePolicy` пишет файл
  целиком (атомарно, через временный файл с перечитыванием парсером — как
  `SaveVariants`); комментарии в файле не сохраняются, ручная правка
  возможна, но следующий Save её перепишет. Отвергнуто: патчевая запись с
  сохранением комментариев — TOML round-trip без зависимостей хрупок.
- Прогон плана: пресет `zai-first` (order `zai → opencode-go → codex`,
  `off = ["strata"]`, добавлен в политику вручную), `permissions: full`.
- v2→v3 migration materializes the class-ladder effort as the route key when a model has a class but no pin; the plan's 'pin or -' covers explicit '-' pins only — '-' for missing pins would silently stop passing effort to reasoning models; rejected: losing the effort on migration

## Phase 1: Формат v3 и ядро

### [x] P059.1.1 Route-таблица в политике версии 3

Read:
- internal/roster/policy2.go:L1-120
- internal/roster/policy2_test.go:L1-80
- internal/roster/migrate.go
- internal/roster/migrate_test.go
- docs/design/roster.md#политика-rostertoml
- bench/roster.toml

Do:
- Поднять `PolicyVersion` до 3; v2-файл — код 2 с подсказкой
  `docflow roster migrate` (парсер уже так отвечает v1 — обобщить проверку).
- Ввести `[route."ACCESS/MODEL"]`: таблица `усилие → класс`, класс
  `cheap | standard | strong`; дубли усилия — ошибка; модель без уровней
  ризонинга использует ключ `"-"`.
- Убрать из разбора `[model]` и `[effort]`: оба ключа — код 2 с подсказкой
  `roster migrate`; типы класса модели и пина усилия заменить структурой
  маршрута (`Access/Model/Eff/Class`).
- Расширить `MigrateFile` до v2→v3 (цепочка v1→v3): класс `[model]` и пин
  `[effort]` дают один маршрут; сгенерированный текст перечитывается
  парсером v3 до записи.
- Перевести `bench/roster.toml` на v3 вручную; прогнать
  `go run ./tools/bench validate`.
- Обновить секцию политики в docs/design/roster.md: пример v3, что заменил
  `[model]`/`[effort]`, миграция.

Verify: `go test ./internal/roster/`

Traps: `[preset]`-глобы `models`/`exclude` и `roster.state.toml` не зависят
от усилий — их разбор не трогать; `--profile`-подсказка печатает
`PolicyVersion`, тесты на её текст обновить.

Commit: P059.1.1: replace model class and effort pins with route table v3
Handoff:
- servingRoute picks among a model's routes by the minimal class that meets the role minimum; a class tie keeps file order — if P059.1.2 wants a lower-effort preference on ties, that is its change
- routeEffort snaps a route key to the declared levels like the v2 pin did; P059.1.2 may pass unknown keys verbatim instead
- context-audit start-reads row fails on the clean tree too (pre-existing, README size at 200 plans) — owned by P059.2.4, not this unit
- Unverified: TUI model list now prints 'effort→class' route summaries (was a single class) — no test asserts the new format string

### [ ] P059.1.2 Резолвер и бриф по маршрутам

Read:
- internal/roster/resolve.go
- internal/roster/resolve_chain.go
- internal/roster/brief.go:L1-80
- internal/roster/resolve_test.go:L1-60
- docs/design/roster.md#резолвер

Do:
- Кандидат роли — маршрут из `[route]`: доступ отдаёт по одному кандидату на
  модель, внутри модели выбор — минимальный подходящий класс, затем цена
  `input + output`, затем больший контекст, затем id.
- Усилие кандидата — ключ маршрута (`"-"` → усилие не передаётся); строка
  брифа и `why:` печатают `access/model@effort` вместо `access/model`.
- Автоматический класс по имени модели остаётся оценкой, когда маршрут не
  закрепил класс.
- Бюджет контекста юнита считается от выбранного маршрута; метаданные
  (контекст, цена, уровни) по-прежнему из каталога.
- Обновить секцию резолвера в docs/design/roster.md и примеры вывода.

Verify: `go test ./internal/roster/`

Traps: порядок кандидатов внутри цепочки не меняется (`prefer` → `normal` →
`lean` → `minimal`); `heavy` требует известный контекст; судейские фильтры
`check`/`trusted` без изменений.

Commit: P059.1.2: resolve role candidates through the route table

### [ ] P059.1.3 Операции рабочего места и SavePolicy

Read:
- internal/roster/workspace.go:L100-180
- internal/roster/workspace_test.go:L1-60
- internal/roster/variants.go:L200-260
- internal/roster/state.go:L1-60

Do:
- `SavePolicy(path, policy)`: атомарная полная запись `roster.toml` v3
  (временный файл, перечитать `ParsePolicy`, заменить цель) по образцу
  `SaveVariants`; порядок разделов: `[access]`, `[route]`, `[role]`,
  `[preset]` из политики.
- Операции рабочего места над моделью политики: доступы (добавить из
  каталога / удалить / `trusted`/`check` взаимоисключающе / базовый бюджет),
  модели (добавить из каталога с первым маршрутом / удалить), маршруты
  (добавить уровень / удалить / сменить класс), роли, варианты (создать с
  копией / переименовать / удалить / бюджеты / порядок / сужение моделей).
- Каждая операция валидирует политику до записи; отказ не меняет ни файл,
  ни модель.
- `LoadWorkspace` расширяется каталогом моделей доступов (id, контекст,
  цена, уровни) для пикера веба; читающие сигнатуры не ломать — TUI их
  использует.

Verify: `go test ./internal/roster/`

Traps: операция, называющая неизвестный доступ/модель/вариант, — ошибка, не
молчание; `check`-доступ не может быть `trusted`; имена вариантов уникальны
по всем трём слоям.

Commit: P059.1.3: add workspace write operations and atomic policy save

### [ ] P059.1.4 Ревью фазы 1

Read:
- docs/plans/P059-roster-web.md
- docs/design/roster.md

Do:
- Ревью `## Phase diff` против Goal, Constraints и Decisions: формат v3 без
  остатков `[model]`/`[effort]`, строгий разбор, миграция v1→v3, резолвер
  печатает `model@effort`, `LoadWorkspace` совместим с TUI. Правок кода нет.

Verify: `go test -short -timeout 20m ./...`

Role: review

Commit: P059.1.4: review phase 1 route format and resolver

## Phase 2: Веб-редактор

### [ ] P059.2.1 Сервер `roster web` с JSON API

Read:
- cmd/docflow/main.go:L50-70
- cmd/docflow/main.go:L324-440
- internal/roster/workspace.go:L1-110
- docs/design/cli.md

Do:
- Пакет `internal/roster/web`: `net/http`-сервер, `go:embed static`;
  `GET /api/workspace` — JSON рабочего места (политика, варианты, каталог
  моделей доступов, активные условия), `POST /api/op` — одна операция
  рабочего места из P059.1.3 с телом `{op, …}` и ответом
  `{ok, workspace, error}`; ошибки — код 4xx с сообщением валидации.
- Сервер биндится на `127.0.0.1` (флаг `--addr`, по умолчанию случайный
  порт), печатает URL и работает до SIGINT/SIGTERM; флаг `--roster PATH`
  как у остальных подкоманд.
- Зарегистрировать подкоманду `web` в `cmd/docflow/main.go` (справка,
  разбор флагов).
- Тесты через `httptest`: рабочее место читается, операция меняет файл в
  temp-каталоге, невалидная операция возвращает 4xx и не трогает файл.

Verify: `go test ./internal/roster/web/ ./cmd/docflow/`

Traps: `internal/roster/web` импортирует только `internal/roster` и stdlib;
пишущие запросы сериализуются одним мьютексом, чтобы два POST не писали
`roster.toml` одновременно.

Commit: P059.2.1: serve roster workspace and operations over localhost HTTP

### [ ] P059.2.2 Фронтенд редактора из макета

Read:
- docs/design/roster-web-mockup.html
- internal/roster/web/server.go
- internal/roster/web/server_test.go

Do:
- Перенести макет в `internal/roster/web/static/` (index.html, app.js,
  style.css): переключатель common/варианты, карточки доступов с маршрутами
  «усилие → класс», матрица ролей, пикеры моделей и доступов, панель файла,
  валидация и тосты — всё поведение макета.
- Заменить источники данных: состояние грузится `GET /api/workspace`,
  каждое действие — `POST /api/op`, ошибки сервера — в тосты и красную
  строку валидации; перерисовка после ответа.
- Убрать внешние зависимости макета (lucide CDN, `window.zcode.*`): иконки
  инлайном или текстом, всё офлайн.
- Тест: `GET /` отдаёт страницу; сценарий «добавить маршрут low → cheap,
  создать вариант» проходит end-to-end на temp-политике.

Verify: `go test ./internal/roster/web/`

Traps: файлы статики — `//go:embed static/*` в `server.go`; имена операций
те же, что в P059.1.3, без синонимов.

Commit: P059.2.2: port the roster editor mockup to the embedded web UI

### [ ] P059.2.3 Ревью фазы 2

Read:
- docs/plans/P059-roster-web.md
- internal/roster/web/server.go

Do:
- Ревью `## Phase diff` против Goal и Decisions: самодостаточный бинарник
  (без CDN и новых зависимостей), операции валидируют до записи, UI
  повторяет макет, TUI не тронут. Правок кода нет.

Verify: `go test -short -timeout 20m ./...`

Role: review

Commit: P059.2.3: review phase 2 web server and UI

### [ ] P059.2.4 Приёмка: веб-редактор на живой политике

Read:
- docs/plans/P059-roster-web.md
- CHANGELOG.md:L1-40
- README.md
- README.ru.md
- docs/design/roster.md#команды
- docs/usage.md#ростер

Do:
- Сценарий Goal на копии живой политики в temp-каталоге: `roster web
  --roster tmp/roster.toml`, открыть страницу, добавить opencode-доступ с
  deepseek и маршрутами low→cheap/high→standard, создать вариант, проверить,
  что `roster brief --roster tmp/roster.toml` видит изменения; сценарий
  оформить тестом или скриптом `tools/`, не ручными шагами.
- CHANGELOG `[Unreleased]`: формат v3 с `[route]`, `roster web`, миграция.
- README.md и README.ru.md вместе: команда `roster web` в списке фич;
  docs/design/roster.md — секция «Веб-редактор» (что пишет, что остаётся
  CLI, ссылка на макет), docs/usage.md — кратко о команде.
- Проверить `python tools/context-audit.py --check` не краснеет от новых
  строк README.

Verify: `go test -timeout 20m ./... && bash tools/parity/run.sh bin/docflow bin/docflow`

Depends: P059.1.1, P059.1.2, P059.1.3, P059.1.4, P059.2.1, P059.2.2, P059.2.3

Role: acceptance

Commit: P059.2.4: accept roster web editor end to end
