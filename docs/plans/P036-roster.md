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
[profile.save-claude.provider.codex]   # флаги провайдеров, выбор --profile NAME
trusted = true                 # claude уходит в запас acceptance и replan
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
- Исключение провайдеров с `check` из `review` действует при любом требовании роли, `trusted` включительно — цель `check` в том, чтобы юниты провайдера проверял другой провайдер, а ступень требования этого не меняет — отвергнуто исключать `check` только в нетрастовой ветке подбора (дефект 2 ревью P036.2.3)
- Дефекты ревью фазы 2 исправляет отдельный юнит P036.2.4, затем повторное ревью P036.2.5 — как в фазе 1 (P036.1.4, P036.1.5) — отвергнута правка без повторного ревью: дефект 1 показал, что зелёный локальный прогон зависел от личной конфигурации
- Локализация не-табличного provider вынесена в parseProviders через разрез пути таблицы по последней точке — ошибка называет строку ключа provider и в профиле, и на верхнем уровне, где она раньше указывала в начало файла — отвергнут предварительный контроль типа только в applyProfile: верхнеуровневый случай остался бы без позиции
- Ошибки `spend` локализуются строкой ключа `spend` в его таблице, отсутствующая позиция в `failAt` — строка 1; дефекты повторного ревью исправляет P036.2.6, затем третье ревью P036.2.7 — как P036.2.4/P036.2.5 — отвергнута позиция элемента массива: сканер строк позиций элементов не знает, а строка ключа уже выполняет требование P036.2.1
- Второй кейс без записи в индексе — инлайн-таблица profile = { x = { spend = 1 } }: ParseTOML разбирает её в карту, а scanLines не видит ключей внутри — покрыто расхождение парсер/сканер, а не только отсутствующий заголовок — отвергнут единственный пример profile = 1
- Провайдер маршрута сверяется с верхним `spend`, профиль может сократить
  `spend` — маршруты выпавшего провайдера пропускаются подбором; пример
  профиля — `save-claude` без `spend`, с `trusted` у codex: claude уходит
  в запас — профиль `spend = ["zai", "codex"]` при `check` у zai не
  собирает review из двух провайдеров ни при какой проверке маршрутов.
  Заменяет решение «профиль в тесте переупорядочивает spend… а не
  сокращает» — отвергнута правка только примера: профиль не мог бы
  исключить провайдера (решение пользователя, дефект ревью P036.2.7).
- Дефект третьего ревью исправляет P036.2.8, затем ревью P036.2.9 — как
  P036.2.4/P036.2.5.
- Неизвестный профиль — ошибка аргумента `--profile`, а не файла: код 2,
  путь ростера и список определённых профилей, без строки; `docs/usage.md`
  исправляется под код, «строка» из `Do:` P036.2.1 к нему не относится —
  в файле нет строки, где ошибка; ближайший заголовок `[profile.*]` или
  строка 1 указывали бы мимо — отвергнута синтетическая строка (дефект
  ревью P036.2.9).
- После P036.2.10 пятого ревью фазы 2 нет — дефект P036.2.9 единственный
  и документный, P036.2.10 выполняет доверенный провайдер без `check`,
  итог проверяет приёмка P036.4.1, а бюджет replan фазы (3) уже
  исчерпан — отвергнут ещё один цикл «исправление → ревью».
- Отказ от авто-replan при ростере сохраняет скилл, а не `roster apply`:
  после каждого apply строка `replan:` удаляется из шапки, если
  пользователь авто-replan не выбрал — apply печатает `replan: … → …`,
  повторное включение видно — отвергнут флаг `--no-replan` у apply: фаза
  3 — только скиллы, новый флаг потребовал бы кода, тестов и
  `docs/usage.md` вне её (дефект 2 ревью P036.3.2).
- Дефекты ревью фазы 3 исправляет P036.3.3, затем повторное ревью
  P036.3.4 — как P036.2.4/P036.2.5; скиллы читает холодный агент, и их
  дефекты не ловит ни один тест приёмки — отвергнута правка без ревью.

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

### [-] P036.2.3 Ревью фазы 2
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
Handoff:
- Blocked (plan): Обнаружены четыре дефекта. 1. [P1] internal/roster/apply_test.go:138, :217, :328 — тесты читают личный ростер вместо фикстуры. Запуск с пустым XDG_CONFIG_HOME и -count=1 воспроизводимо роняет три теста; успешная локальная проверка зависит от пользовательской конфигурации. 2. [P2] internal/roster/roster.go:359 — при review = "trusted" доверенный провайдер с check = true попадает в review. Воспроизведено; правило исключения check должно действовать независимо от требования роли. 3. [P2] internal/roster/lines.go:31 — сканер сохраняет кавычки в именах секций, поэтому маршруты [route."z-first"] и [route."a-second"] теряют позиции и сортируются по алфавиту. Воспроизведён выбор второго маршрута при равных ступенях вместо первого в файле. 4. [P2] internal/roster/roster.go:310 — неверный тип provider в выбранном профиле молча игнорируется. Профиль с provider = "typo" принят с кодом 0 вместо ошибки формата.

### [x] P036.2.4 Дефекты ревью фазы 2
Read:
- internal/roster/apply_test.go:L63-106
- internal/roster/apply_test.go:L136-140
- internal/roster/apply_test.go:L215-226
- internal/roster/apply_test.go:L241-250
- internal/roster/apply_test.go:L317-330
- internal/roster/roster.go:L218-232
- internal/roster/roster.go:L275-328
- internal/roster/roster.go:L355-392
- internal/roster/lines.go:L17-101
Do:
- Тесты `apply` не читают личный ростер: фикстура ростера пишется в
  временный каталог теста и передаётся `rosterPath` (как в
  `TestApplyProfile`), в каждом тесте пакета, вызывающем `Apply` или
  `Load` с путём `""`, — `t.Setenv("XDG_CONFIG_HOME", t.TempDir())` плюс
  фикстура в нём либо явный путь. Проверка: `env XDG_CONFIG_HOME="$(mktemp
  -d)" go test -count=1 ./internal/roster/` зелёный.
- `selectChain`: для роли `review` провайдеры с `check` исключены при
  любом требовании, `trusted` включительно (и в доверенной части
  цепочки, и в запасе). Тест: `review = "trusted"`, доверенный провайдер
  с `check = true` — не в цепочке review; при цепочке короче двух — код 2.
- `scanLines`: имя секции заголовка `[a."b".c]` разбирается по сегментам
  вне кавычек, кавычки и пробелы каждого сегмента снимаются — ключи
  индекса совпадают с ключами карт `agents.ParseTOML`. Тест: маршруты
  `[route."z-first"]` и `[route."a-second"]` одной ступени — цепочка берёт
  первый в файле; ошибка в такой секции указывает её строку.
- `applyProfile`: `provider` профиля любого типа идёт в
  `parseProviders` (тот уже даёт ошибку на не-таблицу). Тест:
  `[profile.x]` с `provider = "typo"` при `--profile x` — код 2 со
  строкой ключа.
Verify: `env XDG_CONFIG_HOME="$(mktemp -d)" go test -count=1 ./internal/roster/ ./cmd/docflow/ && go vet ./internal/roster/`
Traps: исключение `check` — только для `review`; `acceptance` и `replan`
с доверенным провайдером, у которого `check`, ведут себя как раньше
(правило подбора в `## Context`). Таблицу эталонных цепочек и
`TestProfileReplacesSpendAndFlags` не менять.
Depends: P036.2.2
Executor: pi | zai/glm-5.3 | max
Commit: P036.2.4: fix roster review defects
Handoff:
- Ошибка «provider должен быть таблицей» теперь указывает строку самого ключа provider (не-таблица) на обоих вызовах parseProviders: таблица пути режется по последней точке на родителя и ключ, поэтому top-level provider = "typo" тоже локализуется, а не падает в начало файла
- scanLines снимает кавычки с сегментов заголовков: имена секций вроде [route."a-first"] и [provider."x"] получают позиции строк, индекс совпадает с ключами карт ParseTOML; экзотический синтаксис по-прежнему даёт строку 1

### [-] P036.2.5 Повторное ревью фазы 2
Read:
- docs/usage.md:L418-520
Do:
- Получить дифф фазы: `git diff "$(git log --format=%H --grep='^P036\.2\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Сверить с `## Goal`, `## Context` (формат, правило подбора, таблица
  цепочек), `## Decisions` и каждым пунктом `Do:` P036.2.1, P036.2.2 и
  P036.2.4; дефекты Handoff P036.2.3 закрыты или сняты решением в
  `## Decisions`.
- Прогнать `env XDG_CONFIG_HOME="$(mktemp -d)" go test -count=1 ./...` и
  `go vet ./...`.
- Код не править. Дефект — ответ `blocked`, `blocker: "plan"`, в
  `reason` каждый дефект с `file:line`. Замечания без дефекта — Handoff
  `Unverified:`.
Verify: `go test ./... && go vet ./...`
Depends: P036.2.4
Executor: codex | gpt-6.1-sol | high
Commit: P036.2.5: re-review roster phase
Handoff:
- Blocked (plan): [P2] internal/roster/roster.go:271 и :310 — ошибки spend привязаны к таблице вместо ключа spend. Воспроизведено: повтор провайдера на строке 3 выдаёт roster.toml:0; повтор в профиле на строке 24 указывает строку заголовка 22. Это нарушает требование P036.2.1 указывать строку ошибки. Кроме того, internal/roster/roster.go:107 передаёт отсутствующую позицию как 0 вместо предусмотренной решением строки 1. Нужны исправление локализации и регрессионные проверки строк.

### [x] P036.2.6 Строки ошибок spend и позиция по умолчанию
Read:
- internal/roster/roster.go:L104-108
- internal/roster/roster.go:L116-120
- internal/roster/roster.go:L256-277
- internal/roster/roster.go:L308-314
- internal/roster/lines.go:L53-108
- internal/roster/roster_test.go:L397-440
- internal/roster/roster_test.go:L506-520
Do:
- `parseSpend` локализует ошибки по ключу `spend` в его таблице:
  верхний уровень — таблица `""`, ключ `spend`; профиль — таблица
  `profile.<name>`, ключ `spend`. Все три ошибки (не массив, неверный
  элемент, повтор провайдера) называют строку ключа `spend`, а не
  заголовок таблицы и не 0.
- `failAt`: строка 0 (в индексе нет записи) превращается в строку 1, как
  записано в `## Decisions`; `index.line` по-прежнему возвращает 0 —
  на нём держится `sortByLine`.
- Тесты строк: повтор провайдера в верхнем `spend` — строка ключа
  `spend`; повтор в `spend` профиля (`--profile`) — строка ключа `spend`
  профиля, а не строка `[profile.x]`; не-массив `spend` — строка ключа;
  ошибка без записи в индексе (например, `profile = 1` без заголовка
  `[profile]` либо экзотический синтаксис) — строка 1, не 0.
Verify: `env XDG_CONFIG_HOME="$(mktemp -d)" go test -count=1 ./internal/roster/ ./cmd/docflow/ && go vet ./internal/roster/`
Traps: многострочный массив `spend` локализуется строкой ключа, не
строкой элемента — сканер позиций элементов не знает. Тексты ошибок,
таблицу эталонных цепочек и `TestProfileReplacesSpendAndFlags` не
менять.
Depends: P036.2.4
Executor: pi | zai/glm-5.3 | max
Commit: P036.2.6: locate roster spend errors
Handoff:
- internal/roster/lines.go неформатирован в HEAD (gofmt -d показывает лишний отступ блока заголовков из P036.2.4); CI gofmt не проверяет, юнит не трогал — кандидат на P036.2.7 или отдельную правку
- Тексты ошибок spend асимметричны: не-массив — «profile x must be an array…» (метка — подлежащее), повтор/элемент — «profile x: duplicate…»; тексты заморожены юнитом, ревью не должно их «выравнивать»
- Top-level ключ profile = 1 в тестах вставляется после spend до первого заголовка таблицы: в конце файла TOML относит его к последней таблице ([route.cs]) и ошибка становится unknown key, а не ошибкой типа

### [-] P036.2.7 Третье ревью фазы 2
Read:
- docs/usage.md:L418-520
Do:
- Получить дифф фазы: `git diff "$(git log --format=%H --grep='^P036\.2\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Сверить с `## Goal`, `## Context` (формат, правило подбора, таблица
  цепочек), `## Decisions` и каждым пунктом `Do:` P036.2.1, P036.2.2,
  P036.2.4 и P036.2.6; дефекты Handoff P036.2.3 и P036.2.5 закрыты или
  сняты решением в `## Decisions`.
- Каждая ошибка ростера с кодом 2 и `FormatError` называет строку ≥ 1;
  проверить выборочно по проверкам из `Do:` P036.2.1.
- Прогнать `env XDG_CONFIG_HOME="$(mktemp -d)" go test -count=1 ./...` и
  `go vet ./...`.
- Код не править. Дефект — ответ `blocked`, `blocker: "plan"`, в
  `reason` каждый дефект с `file:line`. Замечания без дефекта — Handoff
  `Unverified:`.
Verify: `go test ./... && go vet ./...`
Depends: P036.2.6
Executor: codex | gpt-6.1-sol | high
Commit: P036.2.7: re-review roster phase
Handoff:
- Blocked (plan): [P2] docs/usage.md:522 — пример профиля save-claude исключает claude из spend, сохраняя его маршруты. На эталонном ростере команда roster show --profile save-claude воспроизводимо возвращает код 2: «route haiku: provider claude is not in spend» (internal/roster/roster.go:345). Нужно согласовать пример с принятым решением о проверке маршрутов против эффективного spend.

### [x] P036.2.8 Профиль сокращает spend, валидный пример профиля
Read:
- internal/roster/roster.go:L84-100
- internal/roster/roster.go:L340-349
- internal/roster/roster_test.go:L300-396
- docs/usage.md:L500-528
Do:
- `Load`: `checkRoutes` до `applyProfile` — провайдер маршрута
  сверяется с верхним `spend`; провайдер вне `spend` профиля подбором
  пропускается.
- Тесты: профиль со `spend` без одного провайдера при достаточном запасе
  — код 0, маршрутов выпавшего провайдера нет ни в одной цепочке;
  маршрут провайдера вне верхнего `spend` — код 2 и при `--profile`;
  пример профиля из `## Context` на эталонном черновике — код 0,
  `acceptance` и `replan` начинаются с codex, claude — второй.
- `docs/usage.md` «Ростер»: пример профиля из `## Context`; профиль
  заменяет `spend` (может сократить — маршруты выпавших провайдеров
  пропускаются) и флаги перечисленных провайдеров, цепочка каждой роли
  по-прежнему не короче двух провайдеров.
- `gofmt -w internal/roster/lines.go` (Handoff P036.2.6).
Verify: `env XDG_CONFIG_HOME="$(mktemp -d)" go test -count=1 ./internal/roster/ ./cmd/docflow/ && go vet ./internal/roster/ && test -z "$(gofmt -l internal/roster)"`
Traps: таблицу эталонных цепочек и `TestProfileReplacesSpendAndFlags` не
менять; тексты ошибок не менять.
Depends: P036.2.6
Executor: claude | claude-opus-5-5 | high
Commit: P036.2.8: let roster profiles drop providers
Handoff:
- Маршруты сверяются с верхним spend до применения профиля (roster.go Load): профиль может выкинуть провайдера, его маршруты подбор пропускает; опечатка в провайдере маршрута ловится и при --profile
- Пример save-claude в docs/usage.md и ## Context — без spend, codex trusted; проверен на ~/.config/docflow/roster.toml: roster show --profile save-claude код 0, acceptance/replan: codex, claude
- internal/roster/lines.go отформатирован gofmt; логика не менялась

### [-] P036.2.9 Четвёртое ревью фазы 2
Read:
- docs/usage.md:L418-530
Do:
- Получить дифф фазы: `git diff "$(git log --format=%H --grep='^P036\.2\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Сверить с `## Goal`, `## Context` (формат, правило подбора, таблица
  цепочек), `## Decisions` и каждым пунктом `Do:` P036.2.1, P036.2.2,
  P036.2.4, P036.2.6 и P036.2.8; дефекты Handoff P036.2.3, P036.2.5 и
  P036.2.7 закрыты или сняты решением в `## Decisions`.
- Каждый пример TOML в «Ростер» `docs/usage.md` проходит `roster show`
  (с `--profile` для профиля) на эталонном черновике.
- Прогнать `env XDG_CONFIG_HOME="$(mktemp -d)" go test -count=1 ./...` и
  `go vet ./...`.
- Код не править. Дефект — ответ `blocked`, `blocker: "plan"`, в
  `reason` каждый дефект с `file:line`. Замечания без дефекта — Handoff
  `Unverified:`.
Verify: `go test ./... && go vet ./...`
Depends: P036.2.8
Executor: codex | gpt-6.1-sol | high
Commit: P036.2.9: re-review roster phase
Handoff:
- Blocked (plan): [P2] internal/roster/roster.go:298 — неизвестный профиль возвращает код 2 и путь без номера строки. Воспроизведено командой roster show --profile missing на эталонном ростере. Это противоречит Do P036.2.1 и docs/usage.md:551. Требуется исправление диагностики либо явное решение об исключении для неизвестного профиля.

### [x] P036.2.10 Неизвестный профиль — ошибка аргумента
Read:
- internal/roster/roster.go:L288-303
- docs/usage.md:L545-552
Do:
- `docs/usage.md` «Ростер»: убрать «неизвестный профиль» из перечня
  ошибок «код 2 с файлом и строкой»; отдельной фразой — неизвестный
  `--profile NAME` даёт код 2 с путём ростера и списком определённых
  профилей, без строки (решение в `## Decisions`).
- Тест неизвестного профиля (найти: `grep -n 'unknown profile'
  internal/roster/*_test.go cmd/docflow/*_test.go`; нет — добавить в
  `internal/roster`): ростер с двумя профилями, `Load` с `missing` — код
  2, сообщение содержит путь ростера и `(defined: <оба профиля в порядке
  файла>)`, позиции `путь:N:` нет.
Verify: `env XDG_CONFIG_HOME="$(mktemp -d)" go test -count=1 ./internal/roster/ ./cmd/docflow/ && ! grep -q 'неизвестный профиль — код 2 с файлом и строкой' docs/usage.md`
Traps: код `applyProfile` и тексты ошибок не менять — исправляется
документ, тест закрепляет текущее поведение. Таблицу эталонных цепочек и
`TestProfileReplacesSpendAndFlags` не менять.
Depends: P036.2.8
Executor: claude | claude-opus-5-5 | high
Commit: P036.2.10: document unknown roster profile
Handoff:
- Unknown --profile is documented as an argument error (code 2, roster path, defined profiles, no line) in docs/usage.md «Ростер»; TestUnknownProfileIsArgumentError in internal/roster/roster_test.go pins the current applyProfile message
- The older unknown-profile assertion inside TestProfileReplacesSpendAndFlags is left untouched per Traps; the new test is separate
- Unverified: Unit Verify (go test ./internal/roster/ ./cmd/docflow/ and the grep on docs/usage.md) was not run: every Bash call failed with an auto-mode classifier error, so the new test has not been compiled or executed
- Unverified: docflow verify P036.2.10 (preflight and gate) was not run for the same reason; the runner's own verify is the first real check

## Phase 3: Скиллы

### [x] P036.3.1 Скиллы и README под ростер
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
Depends: P036.2.10
Commit: P036.3.1: plan with the roster
Handoff:
- Skills grew: plan skill +26 lines (Procedure renumbered to 11 steps, new Review section after Acceptance), run skill +11 lines — the plan's own Read: ranges L28-107/L164-225/L61-128 now point at shifted content; P036.3.2 and P036.4.1 read whole files, so no stale links
- docs/usage.md «Ростер» is now linked via the Cyrillic anchor #ростер from README.md, README.ru.md and «Планирование» — same pattern as the existing #автозапуск links

### [-] P036.3.2 Ревью фазы 3
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
Handoff:
- Blocked (plan): 1. skills/bsv-docflow-plan/SKILL.md:217-219: раздел Acceptance требует Executor: вместо Role: acceptance. Это противоречит новым критериям ролей и обходит цепочку acceptance ростера. 2. skills/bsv-docflow-plan/SKILL.md:65-67: обязательный roster apply безусловно записывает replan:, хотя строки 94-98 разрешают выключить авто-replan и запрещают включать его без выбора пользователя. Инструкция не сохраняет отказ после apply (docs/usage.md:557-559). 3. skills/bsv-docflow-run/SKILL.md:75-76: для ручного Executor: указан только его маршрут, а fallback: присоединён лишь к executor: шапки. По реализации internal/agents/agents.go:750 общий fallback добавляется в обоих случаях; текущая инструкция позволяет ошибочно блокировать подходящего запасного агента.

### [ ] P036.3.3 Дефекты ревью фазы 3
Read:
- skills/bsv-docflow-plan/SKILL.md:L55-100
- skills/bsv-docflow-plan/SKILL.md:L150-225
- skills/bsv-docflow-run/SKILL.md:L60-80
- docs/usage.md:L545-566
- internal/agents/agents.go:L746-770
Do:
- `bsv-docflow-plan`, «Acceptance»: юнит приёмки — `Role: acceptance`
  (цепочка `acceptance:` из шапки, её пишет `roster apply` или ручной
  выбор); `Executor:` — только по явному выбору пользователя, как в
  «Executor» (`Role:`/`Executor:`). Без `executor:`/`Executor:` в плане —
  по-прежнему `Model: strong`.
- `bsv-docflow-plan`, шаг 9 Procedure: после `docflow roster apply` —
  если пользователь не выбрал авто-replan в опросе, удалить строку
  `replan:` из шапки; так после каждого повторного apply (решение в
  `## Decisions`). Пункт «Auto-replan» опроса — та же оговорка одной
  фразой: ростер пишет `replan:`, отказ снимает его.
- `bsv-docflow-run`, «Executor», ручная сессия: юнит без `Role:` — его
  `Executor:` или `executor:` шапки, в обоих случаях вместе с
  `fallback:` (как `UnitRoleRoutes` в `internal/agents/agents.go`).
- Других правок нет: `CHANGELOG.md` `[Unreleased]` уже содержит запись
  P036.3.1 о скиллах под ростер.
Verify: `go test ./... && grep -q 'Role: acceptance' skills/bsv-docflow-plan/SKILL.md`
Traps: код и `docs/usage.md` не править — apply пишет `replan:` всегда,
это поведение остаётся. Строки 9 и опроса не противоречат: «Off unless
the user opts in» сохраняется.
Depends: P036.3.1
Executor: claude | claude-opus-5-5 | high
Commit: P036.3.3: fix skills review defects

### [ ] P036.3.4 Повторное ревью фазы 3
Read:
- skills/bsv-docflow-plan/SKILL.md
- skills/bsv-docflow-run/SKILL.md
Do:
- Получить дифф фазы: `git diff "$(git log --format=%H --grep='^P036\.3\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Сверить скиллы с `## Decisions`, форматом из P036.1.1 (`docs/design/format.md`)
  и командами из фазы 2 (`docs/usage.md`): имена ключей, ролей, команд и
  флагов совпадают; холодный агент по скиллу составит план с ролями и
  ревью; три дефекта Handoff P036.3.2 закрыты.
- Код и тексты не править. Дефект — `blocked`, `blocker: "plan"`, в
  `reason` каждый с `file:line`. Прочее — Handoff `Unverified:`.
Verify: `go test ./...`
Depends: P036.3.3
Executor: codex | gpt-6.1-sol | high
Commit: P036.3.4: re-review skills phase

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
Depends: P036.1.1, P036.1.2, P036.1.4, P036.1.5, P036.2.1, P036.2.2, P036.2.3, P036.2.4, P036.2.5, P036.2.6, P036.2.7, P036.2.8, P036.2.9, P036.2.10, P036.3.1, P036.3.2, P036.3.3, P036.3.4
Executor: claude | claude-opus-5-5 | high
Commit: P036.4.1: accept roster and format 3
