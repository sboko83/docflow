---
id: P036
title: Executor roster
type: feature
status: active
created: 2026-10-24
gate: go test ./...
executor: pi | zai/glm-5.3-flash | max
fallback: codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
replan: claude | claude-opus-5-5 | high
permissions: auto
---
## Goal

Исполнителей плана подбирает личный ростер `~/.config/docflow/roster.toml`
по порядку расхода провайдеров и ступеням моделей: `docflow roster show`
печатает цепочки ролей, `docflow roster apply Pxxx` пишет их в шапку
плана. Формат `docflow: 3` хранит цепочку на роль (`Role:` в юните), и
раннер при отказе маршрута идёт по цепочке роли юнита, а не по общему
`fallback:`. У каждой роли есть запасной маршрут другого провайдера:
ростер без него не собирается, `lint` предупреждает о цепочке из одного
маршрута. Скилл планирования спрашивает ростер вместо выбора троек и
ставит юниты ревью после фаз, которые выполняет провайдер с `check`.

## Context

- docs/design/format.md:L107-216
- docs/design/runner.md:L218-226

Формат ростера (черновик пользователя уже лежит в
`~/.config/docflow/roster.toml`; реализация разбирает именно его):

```toml
spend = ["zai", "codex", "claude"]   # порядок расхода: первый тратится первым

[provider.zai]
check = true      # юниты этого провайдера проверяет ревью другим провайдером
[provider.claude]
trusted = true    # допускается к acceptance и replan

[role]            # роль -> минимальная ступень: cheap < standard < strong;
cheap      = "cheap"      # trusted = strong у провайдера с trusted
executor   = "standard"
heavy      = "strong"
review     = "strong"     # провайдеры с check исключены
acceptance = "trusted"
replan     = "trusted"

[route.glm-flash-high]
run  = "pi | zai/glm-5.3-flash | high"
tier = "cheap"
# ... route.<name> на каждый маршрут

[profile.save-claude]          # необязательно: переопределяет spend и
spend = ["zai", "codex"]       # флаги провайдеров, выбор --profile NAME
[profile.save-claude.provider.codex]
trusted = true
```

Провайдер маршрута: для `pi` — префикс модели до `/` (`zai/glm-5.3` →
`zai`, модель без `/` → `pi`), для остальных — имя агента.

Подбор цепочки роли: провайдеры в порядке `spend`; у каждого берётся один
маршрут — с наименьшей ступенью не ниже требуемой (при равенстве — первый
в файле); провайдер без подходящего маршрута пропускается. Квота общая на
подписку, поэтому второй маршрут того же провайдера в цепочке был бы
пустой попыткой. Для `review` провайдеры с `check` исключены. Для
`trusted` — сначала провайдеры с `trusted`, ступень `strong`, затем запас:
`strong` остальных провайдеров без `check` в порядке `spend`. Цепочка
любой роли короче двух маршрутов — ошибка ростера: без запаса лимит
единственного провайдера останавливает прогон до сброса.

Ожидаемые цепочки на черновике пользователя (эталон для тестов):

| Роль | Цепочка |
|---|---|
| cheap | `pi \| zai/glm-5.3-flash \| high; codex \| gpt-6-luna \| high; claude \| claude-haiku-5-5 \| -` |
| executor | `pi \| zai/glm-5.3-flash \| max; codex \| gpt-6-luna \| max; claude \| claude-sonnet-5-5 \| high` |
| heavy | `pi \| zai/glm-5.3 \| max; codex \| gpt-6.1-sol \| high; claude \| claude-opus-5-5 \| high` |
| review | `codex \| gpt-6.1-sol \| high; claude \| claude-opus-5-5 \| high` |
| acceptance | `claude \| claude-opus-5-5 \| high; codex \| gpt-6.1-sol \| high` |
| replan | `claude \| claude-opus-5-5 \| high; codex \| gpt-6.1-sol \| high` |

Шапка плана в формате 3: `executor:` — первый маршрут роли executor,
`fallback:` — её остальные маршруты; `cheap:`, `heavy:`, `review:`,
`acceptance:`, `replan:` — полные цепочки `тройка; тройка; …`. Юнит:
`Role: cheap|heavy|review|acceptance` вместо `Executor:`.

## Constraints

- Go, только стандартная библиотека; TOML — `agents.ParseTOML`.
- Раннер ростер не читает: план остаётся воспроизводимым
  ([P021](../archive/P021-plan-executors.md)). Ростер действует только в
  `roster show`/`roster apply`.
- `docflow: 3` и миграция 2 → 3 в одном юните; формат, `templates/` и
  `lint` меняются вместе.
- Собственный `docs/docflow.md` этого репозитория остаётся `docflow: 2`
  до закрытия плана: прогон идёт установленным CLI формата 2, и маркер 3
  остановит его. Миграция репозитория — после выпуска.
- `Executor:` юнита работает как раньше: своя тройка, затем общий
  `fallback:`. Планы без ролей ведут себя как в формате 2, кроме
  `replan:` (решение ниже).
- `docs/` — по-русски; код, комментарии, CHANGELOG, скиллы — по-английски.

## Prerequisites

- Ключ z.ai известен pi (юниты плана идут на z.ai):
  `pi auth check --provider zai --no-refresh`
- Навыки агентов идут из установленного релиза, а не из рабочего дерева:
  `for l in ~/.claude/skills ~/.agents/skills ~/.pi/agent/skills; do readlink "$l/bsv-docflow-run" | grep -q '/.docflow/current/' || exit 1; done`

## Traps

- Раннер этого прогона — установленный `~/.docflow/current/docflow`
  формата 2, навыки агентов — ссылки на `~/.docflow/current/skills`. Не
  запускать `tools/install-dev.sh` и не подменять установленный CLI во
  время плана; `go test ./...` бинарь не ставит. Навыки из рабочего
  дерева P036.3.1 переписывает под формат 3: связанные с ним агенты и
  replan писали бы `Role:` в этот план формата 2.
- `fallback:` — хвост роли executor, а не общий запас: юнит с `Role:` его
  не использует. Юнит с `Executor:` — использует, как в формате 2.
- Ключ ступени `trusted` в `[role]` — не ступень маршрута: у маршрута
  `tier` только `cheap|standard|strong`.

## Decisions

- Роль юнита записана полем `Role:`, цепочки — ключами ролей в шапке —
  `roster apply` переписывает только шапку, роль видна, а не угадывается
  — отвергнуто поле `Fallback:` в юните: роль пришлось бы выводить по
  правилу, и ручной `Executor:` перезаписывался бы как heavy.
- В формате 3 `replan:` — полная цепочка, `fallback:` к ней не
  добавляется; migrate 2 → 3 дописывает маршруты `fallback:` в `replan:`
  незакрытых планов — replan по правилу ростера только `trusted`, а
  старые планы сохраняют поведение — отвергнуто «replan, затем
  fallback»: при первом лимите replan уходил бы к недоверенному
  провайдеру.
- В цепочке роли по одному маршруту на провайдера — квота общая на
  подписку, второй маршрут того же провайдера после лимита упадёт так же
  — отвергнут перебор всех маршрутов по ступеням.
- У каждой роли не меньше двух маршрутов разных провайдеров; для
  `trusted` запас — `strong` недоверенного провайдера без `check` —
  лимит единственного провайдера иначе останавливает прогон до сброса
  (живой случай: RMG P010 без `fallback:` ждал z.ai до 00:38) —
  отвергнута цепочка только из `trusted`: при одном доверенном
  провайдере она всегда из одного маршрута.
- Профили `[profile.<name>]` переопределяют только `spend` и флаги
  провайдеров — маршруты и требования ролей общие — отвергнуты отдельные
  файлы ростеров.
- Ревью — отдельный юнит после фазы, ревьюер код не правит, дефект —
  `blocked` с `blocker: "plan"` — исправление планирует replan —
  отвергнута правка кода ревьюером.
- Навыки на время плана — релизные, это проверяет Prerequisites —
  P036.3.1 переписывает навыки под формат 3, а репозиторий до закрытия
  остаётся на формате 2 — отвергнут один Trap без проверки: dev-ссылки
  уже однажды остались после P035.
- Dry-run берёт первый маршрут `Role:` и показывает источник `role`, чтобы превью совпадало с маршрутом запуска; наследование `executor:` для такого юнита исключено.
- Исправлена также устаревшая строка раздела авто-replan («replan идёт по цепочке replan: затем fallback:») за пределами названного в Do: раздела резервных маршрутов — она прямо противоречила поведению формата 3, которое описывал этот юнит — отвергнуто оставить как есть: два раздела описывали бы одно поведение по-разному
- Режим исполнителей для `Role:` и ключей ролей — как в формате 2: любое
  назначение, `executor:` в шапке или `Executor:` в юните; так уже
  записано в `docs/design/format.md` — юнит с `Role:` шапку не наследует,
  требование `executor:` в шапке не меняло бы ни одного маршрута —
  отвергнуто требовать `executor:` (скобка в `Do:` P036.1.1, дефект 2
  ревью P036.1.3).
- Предупреждение о навыках (`PlanAgents`) охватывает агентов всех
  маршрутов цепочек юнитов и `replan:`, а не только первых — резервный
  маршрут без навыка падает в момент отказа основного — отвергнута
  правка только разбора `replan:`.
- В PlanAgents при сбое UnitRoleRoutes остаётся основной маршрут, а не пусто — предупреждение о навыках best-effort, строгая валидация цепочек уже в lint — отвергнуто молчаливое исключение юнита
- Агенты-маркеры в тесте: codex только вторым маршрутом heavy:, claude только вторым маршрутом replan: — ParseExecutor знает лишь claude/codex/pi, четыре разных агента невозможны, а такая раскладка роняет своего агента при обрыве каждой цепочки — отвергнуты уникальные агенты на маршрут
- Все шесть ролей обязаны иметь явное требование в [role] — roster show печатает каждую цепочку, пропущенное требование — код 2 на строке [role] — отвергнуты необязательные роли с тихим пропуском
- Таблица флагов профиля заменяет весь набор флагов перечисленного провайдера, неперечисленные держат top-level флаги — «заменяет флаги перечисленных провайдеров» из ## Context — отвергнуто слияние по ключам
- Позиции ошибок берутся из отдельного сканера строк рядом с agents.ParseTOML (тот возвращает карты без позиций); при экзотическом синтаксисе — строка 1 — отвергнуто расширение парсера позициями ключей
- run.lock — файл реального замка раннера run/run.lock — намерение «идёт прогон» иначе не срабатывало бы никогда — отвергнут литеральный путь <git-dir>/docflow/run.lock
- новые ключи вставляются в обратном порядке после одного якоря (строка fallback:, иначе конец frontmatter, вычисленный один раз через format.ParseMeta) — повторный InsertMeta(0) пересчитал бы конец после каждой вставки и перевернул канонический порядок — отвергнут пересчёт якоря на каждый ключ
- пост-проверка — lint.ActivationIssues без CheckVersion — репозиторий остаётся docflow: 2 до закрытия P036, apply не должен зависеть от маркера версии — отвергнут полный lint.Lint по корню
- профиль в тесте переупорядочивает spend всех трёх провайдеров (codex, claude, zai), а не сокращает его — проверка P036.2.1 кодом 2 отвергает маршруты провайдера вне эффективного spend

## Phase 1: Формат 3

### [x] P036.1.1 Роли и цепочки в формате, миграция 2 → 3
Read:
- docs/design/format.md:L240-252
- internal/format/doc.go:L15-25
- internal/format/doc.go:L70-80
- internal/lint/lint.go:L25-35
- internal/lint/lint.go:L360-560
- internal/cycle/lifecycle.go:L660-730
- templates/docflow.md
- templates/plan.md
Do:
- `format.DocflowVersion` → `"3"`; `Role` в `UnitFields` (перед
  `Commit`); ключи `cheap`, `heavy`, `review`, `acceptance` в
  `planOptionalKeys`.
- `lint`: значение ключа роли и `replan:` — цепочка из ≥1 тройки через
  `;`, каждая разбирается `ParseExecutor`, повтор `agent|model` в цепочке
  — ошибка; `Role:` — одно из `cheap|heavy|review|acceptance`; `Role:` и
  `Executor:` в одном юните — ошибка; `Role:` без ключа своей роли в
  шапке — ошибка; ключ роли без юнитов с этой ролью — предупреждение;
  в режиме исполнителей цепочка из одного маршрута — предупреждение
  `no fallback for <role>`: `executor:` без `fallback:`, ключ роли и
  `replan:` с одной тройкой; юнит с `Executor:` при пустом `fallback:` —
  то же предупреждение;
  `Role:` и ключи ролей только в режиме исполнителей (есть `executor:`).
- Функция цепочки роли юнита для раннера: `Executor:` — тройка и
  `fallback:`; `Role:` — цепочка ключа роли; иначе `executor:` и
  `fallback:`.
- `migrate`: шаг `"2"` — маркер `docflow: 3` и в каждом плане
  `docs/plans/*.md` (архив не трогать) с `replan:` и `fallback:` —
  `replan: X; <маршруты fallback без X>`; вывод перечисляет изменённые
  планы. Цепочка `"1"` → `"2"` → `"3"` проходит целиком.
- `templates/docflow.md` — `docflow: 3`; комментарий в `templates/plan.md`
  — ключи ролей и `Role:`.
- Тесты `internal/lint`, `internal/cycle`: каждое правило выше,
  миграция 1 → 3 и 2 → 3, план без ролей формата 2 проходит lint без
  изменений.
- `docs/design/format.md`: ключи ролей, `Role:`, цепочка `replan:`,
  версия 3 (ссылку на архивную запись добавит закрытие плана).
- `CHANGELOG.md` `[Unreleased]`: формат 3, роли, migrate.
Verify: `go test ./internal/format/ ./internal/lint/ ./internal/cycle/`
Traps: фикстуры тестов с `docflow: 2` в `cmd/docflow` и `internal/runner`
сломаются от смены версии — перевести их на `format.DocflowVersion` или
на `3`, не меняя смысла тестов; итоговый `go test ./...` — гейт.
Depends: -
Executor: pi | zai/glm-5.3 | max
Commit: P036.1.1: add roles and format 3
Handoff:
- Полный `docflow verify P036.1.1` прошёл: Verify `go test ./internal/format/ ./internal/lint/ ./internal/cycle/` и gate `go test ./...`.
- `docs/docflow.md` оставлен на `docflow: 2`; миграцию репозитория выполнять после закрытия P036, чтобы установленный CLI формата 2 мог продолжить план.
- Изменения оставлены незакоммиченными для `docflow done/commit` раннера.

### [x] P036.1.2 Раннер по цепочке роли
Read:
- docs/design/runner.md:L60-125
- internal/agents/agents.go:L618-700
- internal/runner/chain.go:L1-80
- internal/runner/replan.go:L160-180
- internal/runner/run.go:L979-1060
- internal/runner/fallback_test.go:L1-60
- internal/runner/fallback_test.go:L100-160
- internal/runner/routes_test.go:L131-160
Do:
- `agents.RunRoute`/`RouteSource`/`PlanAgents`: юнит с `Role:` берёт
  первый маршрут цепочки роли; источник маршрута — `role <name>`.
- `unitExecutorChain`: цепочка из функции цепочки роли юнита (P036.1.1)
  вместо «маршрут + `fallback:`»; `replanExecutorChain` — только цепочка
  `replan:`.
- Стартовый замер квоты и пропуск исчерпанных маршрутов охватывают
  цепочки ролей (через `unitExecutorChain`).
- `run --dry-run --all`: у юнита с ролью — `(role heavy)`; после юнитов —
  цепочка каждой роли из шапки и `replan:`.
- Тесты `internal/runner`: отказ `limit` основного маршрута heavy-юнита
  переводит на второй маршрут `heavy:`, а не на `fallback:`; юнит с
  `Executor:` по-прежнему идёт в `fallback:`; replan с цепочкой из двух
  маршрутов не уходит в `fallback:`; dry-run печатает роли и цепочки.
- `docs/design/runner.md`: цепочка роли и `replan:` в разделе резервных
  маршрутов.
- `CHANGELOG.md` `[Unreleased]`: раннер идёт по цепочке роли.
Verify: `go test ./...`
Executor: pi | zai/glm-5.3 | max
Commit: P036.1.2: route units by role chains
Handoff:
- Чужой staged-кусок появился в индексе посреди сессии (00:22:42, не из этого агента; на старте дерево было чистым): internal/runner/progress.go — agentName(zaiQuota) -> "Z.ai", плюс первая запись Changed в [Unreleased]; это законченная работа снятого z.ai backlog-пункта (57f01da). docflow commit делает git add -A, изменение уедет в коммит P036.1.2 — если это нежелательно, человеку нужно закоммитить его отдельно до коммита раннера
- agents.PlanAgents разбирает шапку replan: одиночной тройкой lint.ParseExecutor: для цепочки из нескольких маршрутов формата 3 ParseExecutor падает, и агенты replan молча выпадают из SkillsWarnings; роль-юниты при этом работают (первый маршрут через RunRoute). Не входит в Do: этого юнита — кандидат на P036.1.3/replan
- Выборка плана (cycle.SelectUnit) гоняет полный lint, поэтому защитная ошибка chainLine «<key> needs executor mode» недостижима: план с ключом роли вне режима исполнителей до dry-run не доходит
- Unverified: Вывод односессионного dry-run «from: role <name>» (run.go, печать RouteSource) — проверен только путь --all; отдельной проверки single-unit dry-run нет
- Unverified: Чужое изменение progress.go (Z.ai) гонялось в моих прогонах go test ./..., но отдельного теста на него нет и писалось оно не этим юнитом

### [-] P036.1.3 Ревью фазы 1
Read:
- docs/design/format.md:L107-252
- docs/design/runner.md:L218-240
Do:
- Получить дифф фазы: `git diff "$(git log --format=%H --grep='^P036\.1\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Сверить с `## Goal`, `## Constraints`, `## Decisions` и каждым пунктом
  `Do:` P036.1.1 и P036.1.2: правила lint, миграция `replan:`, юнит с
  `Executor:` и план без ролей ведут себя как в формате 2, тест на каждый
  пункт `Do:`.
- Прогнать `go test ./...` и `go vet ./...`.
- Код не править. Дефект — расхождение с Goal/Constraints/Decisions, нет
  теста на пункт `Do:` — ответ `blocked`, `blocker: "plan"`, в `reason`
  каждый дефект с `file:line`. Замечания без дефекта — Handoff
  `Unverified:`.
Verify: `go test ./... && go vet ./...`
Executor: codex | gpt-6.1-sol | high
Commit: P036.1.3: review format 3 phase
Handoff:
- Blocked (plan): 1. internal/agents/agents.go:638 — PlanAgents разбирает replan: как одиночную тройку; цепочка исключает replan-агентов из SkillsWarnings. Воспроизведено: отсутствует pi. 2. internal/lint/lint.go:579 — проверка использует executorMode, допускающий Executor: другого юнита. Поэтому Role: и ключ роли принимаются без executor: в шапке вопреки P036.1.1. Воспроизведено: lint без ошибок. 3. internal/agents/agents_test.go:457 — нет теста PlanAgents с Role:. internal/cycle/cycle_test.go:706 — миграционные тесты не проверяют сохранность архива и плана с replan: без fallback:. Требуемое покрытие Do: неполное.

### [x] P036.1.4 Агенты цепочек и покрытие тестами
Read:
- internal/agents/agents.go:L625-648
- internal/agents/agents_test.go:L430-460
- internal/runner/chain.go:L22-130
- internal/cycle/lifecycle.go:L674-730
- internal/cycle/cycle_test.go:L706-760
Do:
- `agents.PlanAgents`: агенты всех маршрутов цепочки каждого открытого
  юнита (`Role:` — цепочка ключа роли; `Executor:` и наследование —
  маршрут и `fallback:`) и всех маршрутов `replan:`. Разбор цепочки —
  один на `agents` и `runner`: перенести `chainRoutes`/`unitRoleRoutes`
  из `internal/runner/chain.go` в `internal/agents` (экспортировать),
  `runner` вызывает их; поведение раннера не меняется.
- Тест `internal/agents`: `PlanAgents` на плане с `Role:` и `replan:` из
  двух маршрутов разных агентов возвращает агентов всех цепочек,
  включая второй маршрут `replan:`.
- Тесты `internal/cycle` миграции 2 → 3: план в `docs/archive/` с
  `replan:` и `fallback:` не меняется; план в `docs/plans/` с `replan:`
  без `fallback:` не меняется и не попадает в вывод.
- `CHANGELOG.md` `[Unreleased]`: предупреждение о навыках охватывает
  агентов резервных маршрутов и цепочки `replan:`.
Verify: `go test ./internal/agents/ ./internal/runner/ ./internal/cycle/`
Traps: `internal/agents` не может импортировать `internal/runner`
(обратная зависимость уже есть) — общий разбор живёт в `agents`. Правило
lint «`Role:` вне режима исполнителей» не менять (см. `## Decisions`).
Executor: pi | zai/glm-5.3 | max
Commit: P036.1.4: cover chain agents and migration
Handoff:
- ParseExecutor допускает только известных агентов (claude, codex, pi) — в фикстурах планов нельзя использовать других агентов-маркеров (gemini/mistral дают ошибку lint)
- Разбор цепочек теперь экспортирован из internal/agents: ChainRoutes/UnitRoleRoutes; будущие части плана (roster) должны использовать их, а не плодить свой разбор
- PlanAgents при ошибке разбора полной цепочки юнита оставляет только основной маршрут (RunRoute) — предупреждение best-effort, невалидную цепочку ловит lint до запуска

### [x] P036.1.5 Повторное ревью фазы 1
Read:
- docs/design/format.md:L107-260
- docs/design/runner.md:L218-240
Do:
- Получить дифф фазы: `git diff "$(git log --format=%H --grep='^P036\.1\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Сверить с `## Goal`, `## Constraints`, `## Decisions` и каждым пунктом
  `Do:` P036.1.1, P036.1.2 и P036.1.4; дефекты Handoff P036.1.3 закрыты
  или сняты решением в `## Decisions`.
- Прогнать `go test ./...` и `go vet ./...`.
- Код не править. Дефект — расхождение с Goal/Constraints/Decisions, нет
  теста на пункт `Do:` — ответ `blocked`, `blocker: "plan"`, в `reason`
  каждый дефект с `file:line`. Замечания без дефекта — Handoff
  `Unverified:`.
Verify: `go test ./... && go vet ./...`
Depends: P036.1.4
Executor: codex | gpt-6.1-sol | high
Commit: P036.1.5: re-review format 3 phase
Handoff:
- Повторное ревью фазы 1: новых дефектов не обнаружено; замечания P036.1.3 закрыты тестами либо решением плана.
- На macOS arm64 полный docflow verify P036.1.5 прошёл: go test ./..., go vet ./... и gate. После ограничения ps в песочнице проверка выполнена с расширенными разрешениями. Код и план не изменялись.
- Unverified: Отдельного теста single-unit run --dry-run для Role: нет; обязательный сценарий --all покрыт internal/runner/routes_test.go:166.

## Phase 2: Ростер

### [x] P036.2.1 Ростер и docflow roster show
Read:
- internal/agents/toml.go:L1-20
- internal/agents/agents.go:L180-197
- cmd/docflow/main.go:L30-60
- cmd/docflow/main.go:L265-290
- docs/design/cli.md:L15-46
- docs/usage.md:L418-490
Do:
- Пакет `internal/roster`: путь `$XDG_CONFIG_HOME/docflow/roster.toml`
  (по умолчанию `~/.config/docflow/roster.toml`, как `agents.ConfigPath`),
  флаг `--roster PATH`; разбор формата из `## Context`.
- Проверки с кодом 2 и строкой, где ошибка: неизвестный ключ, `run` не
  разбирается `lint.ParseExecutor`, `tier` вне `cheap|standard|strong`,
  требование роли вне `cheap|standard|strong|trusted`, неизвестная роль,
  провайдер маршрута не в `spend`, повтор в `spend`, неизвестный профиль,
  цепочка роли короче двух маршрутов (с именем роли). Нет файла — код 2 и
  путь.
- Подбор цепочек по правилу из `## Context`; профиль заменяет `spend` и
  флаги перечисленных провайдеров.
- `docflow roster show [--profile NAME] [--roster PATH]`: строка
  `spend: zai, codex, claude · check: zai · trusted: claude`, затем строки
  шапки `executor:`, `fallback:`, `cheap:`, `heavy:`, `review:`,
  `acceptance:`, `replan:` в синтаксисе плана; help-текст команды.
- Тесты: черновик из `## Context` даёт таблицу цепочек из `## Context`;
  каждая проверка выше; профиль меняет порядок и флаги; провайдер `pi` у
  модели без `/`.
- `docs/design/cli.md` — строка команды; `docs/usage.md` — раздел
  «Ростер» после «Конфиг агентов»: формат, подбор, профили.
- `CHANGELOG.md` `[Unreleased]`: `docflow roster show`.
Verify: `go test ./internal/roster/ ./cmd/docflow/`
Depends: P036.1.5
Commit: P036.2.1: add roster and roster show
Handoff:
- internal/roster API для P036.2.2: roster.Load(path, profile) — путь "" → DefaultPath() (~/.config/docflow/roster.toml, XDG_CONFIG_HOME, ~ раскрывается); возвращает *Roster{Path, Profile, Spend, RoleTiers, Chains (map[роль][]agents.Route, роли по Roles), Routes (в порядке файла)}; Roster.Show() рендерит ровно тот блок шапки, который apply должен писать (executor:, fallback:, затем Roles[1:]) — переиспользовать Chains/Show, не переразбирать
- Каждая ошибка ростера — код 2: clierrors.FormatError с файлом и строкой (строки даёт индекс строк lines.go, не ParseTOML; экзотический TOML — строка 1) либо clierrors.DocflowError (нет файла, синтаксис, неизвестный профиль)
- CLI: команда roster уже объявлена с позиционным subcommand и флагами --profile/--roster; для apply нужен второй позиционный id и ветка в handleRoster — сейчас apply отклоняется защитой «unknown roster subcommand 'apply'» с кодом 2 (тест это фиксирует)
- Load валидирует всё сразу при каждом вызове: провайдеры маршрутов — против эффективного (после профиля) spend, цепочка каждой роли ≥ 2 маршрутов; флаги провайдера из [profile.X.provider.Y] заменяют его top-level флаги целиком (codex теряет check) — залочено тестом TestProfileReplacesSpendAndFlags

### [x] P036.2.2 docflow roster apply
Read:
- internal/format/doc.go:L70-80
- internal/format/doc.go:L138-160
- internal/cycle/lifecycle.go:L470-505
- internal/runner/helpers.go:L130-160
Do:
- Прочитать `internal/roster/roster.go` (создаёт P036.2.1).
- `docflow roster apply <id> [--profile NAME] [--roster PATH]`: план из
  `docs/plans/` со статусом `draft`, `active` или `frozen`, иначе код 2.
- Пишет в шапку `executor:`, `fallback:` (нет хвоста — ключ удалить),
  `replan:` и ключи ролей, на которые ссылается `Role:` хотя бы одного
  юнита; ключи ролей без юнитов удаляет; прочие ключи и их порядок не
  трогает; новые ключи — после `executor:`/`fallback:`.
- Существует `<git-dir>/docflow/run.lock` (идёт прогон) — код 5, план не
  меняется.
- После записи — lint плана; ошибки — план восстановлен, код 2.
- Вывод: `key: old → new` по изменённым ключам и подсказка
  `commit: Pxxx: apply roster`; изменений нет — код 1. Не коммитит.
- Тесты: запись и удаление ключей, порядок прочих ключей, статус
  `done`/архив — код 2, `run.lock` — код 5, повторный apply — код 1,
  профиль.
- `docs/design/cli.md` — строка команды; `docs/usage.md` «Ростер» —
  apply; `CHANGELOG.md` `[Unreleased]`.
Verify: `go test ./internal/roster/ ./cmd/docflow/`
Commit: P036.2.2: add roster apply
Handoff:
- run.lock проверяется по реальному пути раннера <git-dir>/docflow/run/run.lock (helpers.go runLock), а не литеральному <git-dir>/docflow/run.lock из Do:
- format.Doc получил экспортированный InsertMeta(after, key, value): вставка ключа frontmatter после строки (0 = перед закрывающим ---) с перепарсингом — apply вставляет новые ключи в обратном порядке после одного якоря, вычисленного один раз
- lint после записи — lint.ActivationIssues (lintPlan + ссылки, без CheckVersion): apply работает, пока репозиторий docflow: 2
- коды выхода apply: 0 записано, 1 нет изменений, 2 (нет плана/статус/ошибка ростера/ошибка lint с восстановлением), 5 run.lock

### [ ] P036.2.3 Ревью фазы 2
Read:
- docs/usage.md:L418-520
Do:
- Получить дифф фазы: `git diff "$(git log --format=%H --grep='^P036\.2\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Сверить с `## Goal`, `## Context` (формат, правило подбора, таблица
  цепочек), `## Decisions` и каждым пунктом `Do:` P036.2.1 и P036.2.2.
- Прогнать `go test ./...` и `go vet ./...`.
- Код не править. Дефект — ответ `blocked`, `blocker: "plan"`, в
  `reason` каждый дефект с `file:line`. Замечания без дефекта — Handoff
  `Unverified:`.
Verify: `go test ./... && go vet ./...`
Executor: codex | gpt-6.1-sol | high
Commit: P036.2.3: review roster phase

## Phase 3: Скиллы

### [ ] P036.3.1 Скиллы и README под ростер
Read:
- skills/bsv-docflow-plan/SKILL.md:L28-107
- skills/bsv-docflow-plan/SKILL.md:L164-225
- skills/bsv-docflow-run/SKILL.md:L61-128
- docs/usage.md:L251-275
- README.md:L20-40
- README.ru.md:L20-40
Do:
- `bsv-docflow-plan`, опрос: сначала `docflow roster show`; работает —
  спросить «ростер (показать цепочки) / профиль / вручную», ручной выбор
  троек — только при «вручную» или ошибке ростера; при ручном выборе
  спрашивать и запасной маршрут каждой роли, план без запаса — только по
  явному отказу пользователя.
- `bsv-docflow-plan`, юнит: `Role:` по критериям — `cheap` (механика,
  доки, переименования, конфиг), `heavy` (дизайн на несколько модулей,
  конкурентность, формат и состояние), `acceptance`, `review`; обычный
  юнит без поля; `Executor:` — только для явного выбора пользователя.
- `bsv-docflow-plan`, новый раздел «Review»: юнит ревью закрывает каждую
  фазу, основной маршрут юнитов которой у провайдера с `check` (`roster
  show`); шаблон `Do:` — дифф фазы по префиксу коммитов, сверка с Goal,
  Constraints, Decisions и `Do:`, без правок, дефект — `blocked` с
  `blocker: "plan"`, прочее — `Unverified:`; `Verify:` — гейт.
- `bsv-docflow-plan`, порядок: после юнитов — `docflow roster apply
  Pxxx`, затем lint и `run --dry-run --all`.
- `bsv-docflow-run`: юнит с `Role: review` не правит файлы, дефект —
  `docflow block --needs plan`.
- `bsv-docflow-run`, раздел «Executor»: режим раннера не менять (агент
  назначение не проверяет). В ручной сессии агент подходит, если он есть
  в цепочке юнита: цепочка его `Role:`, иначе `Executor:` или `executor:`
  шапки вместе с `fallback:`; модель и усилие по-прежнему не сверяются.
- `README.md` и `README.ru.md` — ростер одной строкой рядом с
  `fallback`; `docs/usage.md` «Планирование» — ссылка на «Ростер».
- `CHANGELOG.md` `[Unreleased]`: скиллы под ростер.
Verify: `go test ./... && grep -q 'roster apply' skills/bsv-docflow-plan/SKILL.md && grep -q 'Role: review' skills/bsv-docflow-run/SKILL.md`
Depends: P036.2.3
Commit: P036.3.1: plan with the roster

### [ ] P036.3.2 Ревью фазы 3
Read:
- skills/bsv-docflow-plan/SKILL.md
Do:
- Получить дифф фазы: `git diff "$(git log --format=%H --grep='^P036\.3\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Сверить скиллы с `## Decisions`, форматом из P036.1.1 (`docs/design/format.md`)
  и командами из фазы 2 (`docs/usage.md`): имена ключей, ролей, команд и
  флагов совпадают; холодный агент по скиллу составит план с ролями и
  ревью.
- Код и тексты не править. Дефект — `blocked`, `blocker: "plan"`, в
  `reason` каждый с `file:line`. Прочее — Handoff `Unverified:`.
Verify: `go test ./...`
Executor: codex | gpt-6.1-sol | high
Commit: P036.3.2: review skills phase

## Phase 4: Приёмка

### [ ] P036.4.1 Приёмка ростера и формата 3
Read:
- internal/runner/fallback_test.go:L1-60
- internal/runner/fallback_test.go:L100-160
- cmd/docflow/lifecycle_test.go:L1-60
Do:
- Прочитать `internal/roster/roster.go` (создаёт P036.2.1).
- Создать `cmd/docflow/roster_acceptance_test.go` и
  `internal/runner/role_acceptance_test.go`: настоящий CLI на
  временном проекте `docflow: 3` и ростере из `## Context` во временном
  `XDG_CONFIG_HOME`.
- Сценарий 1: `roster show` печатает цепочки ровно как таблица из
  `## Context`.
- Сценарий 2: план с юнитами без роли, `Role: heavy`, `Role: review`,
  `Role: acceptance` → `roster apply` пишет ключи, `lint` чистый,
  `run --dry-run --all` показывает роль и цепочку каждого юнита;
  повторный apply — код 1.
- Сценарий 3: `roster apply --profile` с другим `spend` меняет порядок
  цепочек.
- Сценарий 4: `docflow run` — лимит основного маршрута heavy-юнита
  переводит на второй маршрут `heavy:`; юнит с `Executor:` уходит в
  `fallback:`.
- Сценарий 5: проект `docflow: 2` с планом, где есть `replan:` и
  `fallback:` → `migrate` даёт `docflow: 3` и цепочку `replan:` с
  маршрутами fallback; архив не изменён; CLI на немигрированном проекте —
  код 2 с подсказкой `docflow migrate`.
- Сценарий 6: ростер, где у роли остаётся один провайдер (например,
  `spend = ["claude"]`), — `roster show` код 2 с именем роли; план
  формата 3 без `fallback:` — `lint` предупреждает `no fallback for
  executor`.
- Сценарий 7: идущий прогон (`run.lock`) — `roster apply` код 5, план не
  изменён.
- Закрыть тестом каждый `Unverified:` из Handoff юнитов P036 или оставить
  его в своём Handoff.
Verify: `go test ./...`
Traps: упавший сценарий чинится в этом юните, если правка мала; иначе
`blocked` с причиной.
Depends: P036.1.1, P036.1.2, P036.1.4, P036.1.5, P036.2.1, P036.2.2, P036.2.3, P036.3.1, P036.3.2
Executor: claude | claude-opus-5-5 | high
Commit: P036.4.1: accept roster and format 3
