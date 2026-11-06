---
id: P058
title: Turn the TUI into a project workspace
type: feature
status: draft
created: 2026-11-03
permissions: auto
timeout: 3600
gate: go test -timeout 20m ./...
executor: pi | zai/glm-5.3-flash | high
fallback: codex | gpt-6-luna | high
cheap: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | high
heavy: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | medium
acceptance: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | medium
replan: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | medium
---
## Goal

`docflow tui` — рабочее место проекта из четырёх экранов: «Проект»
(очередь, планы, Backlog, параметры), «Ростер» (доступы, условия,
варианты и применение к плану), «Прогон» (живой поток агента с полными
рассуждениями) и «История» (прошлые прогоны с повтором потока). Человек
делает в нём всё, что делал через CLI; CLI остаётся для скриптов и агентов.

## Context

Текущий TUI (P052) — пульт одного прогона: дерево плана, лента событий и
около 25 буквенных клавиш. Ростера в нём нет, кроме редактора `m`, который
пишет третий источник маршрутов `<git-dir>/docflow/tui/configuration.json`
рядом с шапкой плана и `roster apply`. Рассуждения агентов парсер уже
выделяет (`agentio.ActivityKindReasoning` для Claude, Codex и Pi), но
каждая дельта — отдельная запись истории с лимитом 200 на юнит, и текст
читается страницами по 4 KiB. Решения человека 2026-11-03: варианты
ростера общие и проектные; TUI полностью заменяет CLI для человека;
рассуждения показываются полным потоком со сворачиванием; сейчас TUI,
веб — позже, поэтому данные экранов собираются вне рендера.

- docs/design/tui.md
- docs/design/roster.md#политика-rostertoml

## Constraints

- Формат плана остаётся `docflow: 3`, ключи шапки и юнита не меняются.
  Новый необязательный файл проекта — только `docs/roster.variants.toml`;
  `run`, `lint` и `cycle` его и ростер не читают.
- Контракт P052 сохраняется: пауза, остановка после юнита, остановка
  сейчас, восстановление checkpoint, lock, выход по сигналу и запрет
  обхода проверки работают как описано в `docs/design/tui.md`.
- Только клавиатура, без мыши. Каждый экран работает от 32x10; уже 100
  колонок — одна панель с переключением `Tab`.
- Новых Go-зависимостей нет: Bubble Tea v2 и lipgloss v2 уже в `go.mod`.
  CGO=0, macOS, Linux, Windows.
- Данные экранов (ростер, поток, история, параметры) собираются функциями,
  которые не импортируют Bubble Tea и возвращают простые структуры:
  будущий веб-интерфейс берёт их без переписывания.
- Вывод агента всегда проходит `tui.Sanitize`: escape-последовательности
  из потока, рассуждений и логов не исполняются.
- Каждый юнит правит разделы `docs/design/tui.md` и
  `docs/design/roster.md`, которые описывают изменённое поведение, в том же
  коммите. `docs/` по-русски, код, комментарии и коммиты — по-английски.

## Traps

- Тесты `internal/tui` большие (`app_test.go`, `acceptance_test.go`,
  `terminal_test.go`) и сверяют строки экрана. Смена клавиши или раскладки
  требует правки ожиданий, а не удаления сценария: сценарий остаётся и
  проверяет то же поведение на новом месте.
- Полный `go test -timeout 20m ./...` на Windows идёт больше 10 минут
  (интеграционные тесты `internal/runner`). Во время работы прогоняй тесты
  своего пакета или `go test -short`: он пропускает тесты дольше 2 с;
  полный прогон — gate.
- Импорты Bubble Tea — `charm.land/bubbletea/v2` и `charm.land/lipgloss/v2`,
  не v1-пути `github.com/charmbracelet/...`.
- Просмотр не берёт checkout lock. Любая запись (план, `docflow.md`,
  ростер проекта, коммит) перечитывает lock в момент действия и отказывает
  при живом чужом прогоне, как lifecycle-клавиши.
- `roster.toml` пишет человек: TUI его не переписывает, иначе теряются
  комментарии и порядок. Машинные файлы — `roster.state.toml` и файлы
  вариантов.
- Файлы в рабочем дереве (`docs/roster.variants.toml`, планы,
  `docs/docflow.md`) после правки делают дерево грязным, а `prompt`
  отказывает на грязном дереве: старт прогона после такой правки требует
  коммита.

## Decisions

- Four screens switched by `1`–`4` replace the tree-plus-details workspace —
  roster, live run and project each need the full width and their own few
  keys, and 25 global letters are not learnable. Rejected more panes and
  overlays on the single P052 screen.
- Routes come only from the plan header: the TUI `m` overlay and
  `configuration.json` are removed, and the roster screen writes the header
  through `roster apply` — three sources of routes made the effective one
  unguessable, and the header is visible in git. Rejected keeping the
  per-run overlay next to the roster.
- A roster variant is a preset. Variants created in the TUI live in
  machine-owned files: global `roster.variants.toml` next to `roster.toml`
  and project `docs/roster.variants.toml` (committed with the project);
  presets in `roster.toml` are shown read-only — rewriting a hand-edited
  TOML loses comments and order. Rejected a round-trip editor for
  `roster.toml` and project variants under `<git-dir>` (not shared between
  machines).
- A variant name is unique across `roster.toml`, the global and the project
  variants file; a duplicate is a policy error naming both files —
  `--preset NAME` must mean one thing everywhere. Rejected silent project
  precedence.
- Streaming deltas of one agent message merge into one history entry keyed
  by `Activity.Message`, and the per-unit limit counts entries, not deltas —
  a reasoning stream otherwise evicts the 200-entry history within seconds.
  Rejected raising the limit.
- Reasoning is shown in full, dimmed, and `t` collapses every reasoning
  block to its first line — the human's choice of 2026-11-03. Rejected a
  separate reasoning pane.
- Planning from the TUI suspends the screen and runs the agent's own
  interactive CLI with a prompt that names the Backlog line and the
  `bsv-docflow-plan` skill; the TUI writes no plan body itself — plans are
  written by an agent under the planning skill. Rejected plan-editing forms.

## Phase 1: Workspace shell

### [ ] P058.1.1 Split the workspace into four screens
Read:
- internal/tui/model.go
- internal/tui/view.go
- internal/tui/keys.go
- internal/tui/app.go
- internal/tui/screen_test.go
Do:
- Введи тип экрана (`screenProject`, `screenRoster`, `screenRun`, `screenHistory`) в `Model` и строку вкладок сверху: `1 Проект  2 Ростер  3 Прогон  4 История`, активная выделена, справа — состояние хоста прогона (`run:…`).
- Клавиши `1`–`4` переключают экран из любого места, кроме открытого диалога и текстового ввода; `Tab` остаётся сменой панели внутри экрана.
- Нынешнее дерево и карточка юнита становятся экраном «Проект»; лента активности выбранного юнита — экраном «Прогон» (пока тем же рендером). «Ростер» и «История» показывают заглушку `пока пусто` с подсказкой.
- Нижняя строка показывает клавиши текущего экрана (не больше пяти) и `? помощь`; `?` открывает помощь текущего экрана, общие клавиши (`1`–`4`, `q`, `r`, `p`, `S`, `x`) — отдельным блоком.
- Клавиши управления прогоном `s`, `R`, `p`, `S`, `x` работают на любом экране; запуск прогона с «Проекта» переключает на «Прогон».
- Добавь тесты в `internal/tui/screen_test.go`: переключение `1`–`4`, вкладки на 32x10 и 120x40, `1`–`4` в поиске печатаются как текст, `s` с «Проекта» открывает «Прогон»; поправь ожидания существующих тестов под новую шапку.
- Перепиши раздел «Клавиши» и вступление `docs/design/tui.md` под экраны.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: `handleKey` разбирает клавиши одной цепочкой условий — вставь переключение экранов после диалогов и текстового ввода, иначе `1` в поиске сменит экран. Не удаляй существующие сценарии тестов.
Depends: -
Role: heavy
Commit: P058.1.1: split the TUI into four screens

## Phase 2: Roster variants and roster data

### [ ] P058.2.1 Load roster variants from global and project files
Read:
- docs/design/roster.md
- docs/design/format.md#раскладка-в-проекте
- internal/roster/policy2.go
- internal/roster/state.go:L1-150
- internal/roster/state.go:L405-560
- internal/roster/roster.go
- cmd/docflow/roster_brief.go
Do:
- Добавь файлы вариантов: глобальный `roster.variants.toml` рядом с политикой (`PolicyPath`) и проектный `docs/roster.variants.toml` от корня checkout. Формат — `version = 2` и только таблицы `[preset.NAME]` той же грамматики, что в `roster.toml`; любой другой ключ — ошибка с файлом и строкой.
- Загрузчик политики сливает пресеты `roster.toml`, глобальных и проектных вариантов; у `Preset` появляется поле источника (`roster`, `variants`, `project`). Одно имя в двух файлах — ошибка политики с обоими путями. Пресет варианта, называющий неизвестный доступ, — ошибка, как в `roster.toml`.
- `--preset`, `preset =` в `roster.state.toml` и `roster brief/explain/apply` видят пресеты всех трёх слоёв; корень проекта для CLI — текущий checkout, вне checkout проектный слой пуст.
- Добавь атомарную запись файла вариантов целиком (`SaveVariants(path, presets)`): файл машинный, комментарии не сохраняются; после записи файл перечитывается парсером, ошибка — откат.
- Тесты в `internal/roster`: слияние трёх слоёв, конфликт имён, неизвестный доступ в варианте, отсутствие файлов, запись и повторное чтение, отказ на лишнем ключе; в `cmd/docflow` — `roster brief --preset` с проектным вариантом.
- Опиши слои вариантов в `docs/design/roster.md` (таблица «Слои», разделы «Политика» и «Команды») и строку `docs/roster.variants.toml` в «Раскладке» `docs/design/format.md`.
Verify: `go test -timeout 20m ./internal/roster/ ./cmd/docflow/`
Traps: `lint` и раннер файл вариантов не читают — не подключай его к `format` или `cycle`. Проверку взаимоисключающих условий пресета (`off/lean/minimal/prefer`) переиспользуй, а не копируй.
Depends: P058.1.1
Role: heavy
Commit: P058.2.1: load roster variants from global and project files

### [ ] P058.2.2 Expose roster state as a workspace model
Read:
- docs/design/roster.md#условия-rosterstatetoml
- internal/roster/state.go:L280-445
- internal/roster/brief.go
- internal/roster/apply.go
- internal/roster/report.go
- internal/roster/catalog.go:L1-130
- cmd/docflow/main.go:L440-700
Do:
- Создай `internal/roster/workspace.go` без импортов Bubble Tea: `LoadWorkspace(rosterPath, root string)` возвращает структуру со списком доступов (агенты, kind, auth, число моделей, бюджет, `check`/`trusted`, условия с датами и источником), вариантами с источником, активным пресетом, цепочками ролей брифа с бюджетом и `why`, предупреждениями и признаком v1-ростера или отсутствующего каталога.
- Добавь операции, которые валидируют условие до записи и пишут через `State.Save`: off/on с датой, бюджет, allow/deny глобов, выбор и сброс пресета, `reset`; сохранение текущих условий как нового варианта в глобальный или проектный файл; удаление варианта из машинного файла (вариант `roster.toml` удалить нельзя — ошибка с указанием файла).
- Добавь `ExplainPlan(root, planID)` — та же разница `ключ: старое → новое`, что у `roster explain`, — и `ApplyPlan` поверх `Apply` с тем же отказом при `run.lock` и откатом после `lint`.
- Переведи `roster status/off/on/budget/...` в `cmd/docflow` на эти операции там, где логика дублируется; вывод и коды CLI не меняются.
- Тесты в `internal/roster`: модель на фикстурной политике и каталоге, каждая операция с отказом на неизвестном доступе и `minimal` без `trusted`, сохранение варианта и его появление в `--preset`, v1-ростер даёт признак миграции, а не панику.
Verify: `go test -timeout 20m ./internal/roster/ ./cmd/docflow/`
Traps: команды чтения не переписывают `roster.state.toml` — `LoadWorkspace` тоже. Истёкший `off` отбрасывается в памяти, файл переписывает только следующая мутация.
Commit: P058.2.2: expose roster state as a workspace model

## Phase 3: Roster screen

### [ ] P058.3.1 Show and edit accesses on the roster screen
Read:
- internal/roster/workspace.go
- internal/tui/model.go
- internal/tui/view.go
- internal/tui/keys.go
Do:
- Экран «Ростер»: слева варианты (активный отмечен, источник `roster`/`глоб.`/`проект`), в центре доступы (переключатель вкл/выкл, бюджет, allowlist, `check`/`trusted`, дата `off --until` и `expires`), справа цепочки ролей с бюджетом; предупреждения — строкой под цепочками. Уже 100 колонок — одна панель, `Tab` переключает.
- Клавиши на доступе: `Space` вкл/выкл (с диалогом «до даты, пусто — бессрочно»), `←/→` бюджет `prefer|normal|lean|minimal|off`, `Enter` — список моделей доступа из каталога с правкой allow/deny глобов. После каждой правки модель перечитывается, цепочки обновляются; отказ операции показывается notice, файл не меняется.
- `Enter` на варианте делает его активным пресетом, `Backspace` снимает пресет; `n` сохраняет текущие условия как новый вариант (диалог: имя, глобальный или проектный); `D` удаляет вариант машинного файла после подтверждения.
- Флаги `docflow tui --roster PATH` (как у `roster`), путь по умолчанию — `roster.DefaultPath()`.
- Тесты в `internal/tui`: рендер на фикстурном ростере 120x40 и 60x20, каждое действие меняет фикстурный `roster.state.toml` или файл вариантов, отказ `minimal` на untrusted-доступе показывает причину и не пишет файл.
- Опиши экран «Ростер» в `docs/design/tui.md`.
Verify: `go test -timeout 20m ./internal/tui/ ./cmd/docflow/`
Traps: на проектный файл вариантов действует lock-проверка (он в рабочем дереве); глобальные файлы lock не требуют.
Depends: P058.2.2
Commit: P058.3.1: show and edit accesses on the roster screen

### [ ] P058.3.2 Scan the catalog and migrate the roster from the TUI
Read:
- docs/design/roster.md#каталог
- docs/design/roster.md#миграция
- internal/roster/scan_catalog.go
- internal/roster/migrate.go
- internal/roster/workspace.go
- internal/tui/execute.go:L1-120
Do:
- На экране «Ростер» клавиша `u` запускает `roster scan` в фоне (как `ScanCatalog` CLI, без `--online`), показывает `scanning…` и затем сводку доступов и предупреждения источников; экран не блокируется.
- Отсутствующий каталог — подсказка `u — собрать каталог` вместо цепочек; v1-ростер — экран с объяснением и диалогом `migrate`: предпросмотр v2-текста, `Enter` пишет через `MigrateFile(path, true)` с `.bak`, `Esc` отменяет.
- Отсутствующий `roster.toml` — подсказка, какой файл создать, и пример политики из `docs/design/roster.md` не пишется автоматически.
- Тесты: фоновый скан с фейковым сканером обновляет модель, v1-фикстура предлагает миграцию и после подтверждения читается как v2, отмена ничего не пишет.
- Допиши `docs/design/tui.md` (экран «Ростер»: скан и миграция).
Verify: `go test -timeout 20m ./internal/tui/ ./internal/roster/`
Traps: сканеры вызывают внешние CLI с таймаутом 30 с — в тестах подменяй сканер, не запускай `pi`/`opencode`.
Commit: P058.3.2: scan and migrate the roster from the TUI

### [ ] P058.3.3 Apply a roster variant to a plan from the TUI
Read:
- docs/design/roster.md#команды
- internal/roster/workspace.go
- internal/roster/apply.go
- internal/tui/execute.go
- internal/tui/view.go
Do:
- На экранах «Ростер» и «Проект» клавиша `A` для выбранного плана (Queue или Frozen, статус `draft`/`active`/`frozen`) открывает диалог: разница `ключ: старое → новое` из `ExplainPlan`, предупреждения ролей плана, активный вариант. `Enter` пишет шапку через `ApplyPlan`, `Esc` отменяет.
- Отказы показываются причиной в диалоге: открытому юниту не хватает цепочки, `run.lock` или живой прогон, `lint` после записи (файл восстановлен), изменений нет.
- После записи — notice `commit: Pxxx: apply roster` и перечитывание рабочего места.
- Карточка плана на «Проекте» показывает строку `⚠ вариант NAME меняет N ключей`, если активный вариант расходится с шапкой.
- Тесты: diff и запись на фикстурном плане, каждый отказ, предупреждение расхождения в карточке.
- Опиши применение в `docs/design/tui.md`.
Verify: `go test -timeout 20m ./internal/tui/`
Commit: P058.3.3: apply a roster variant to a plan from the TUI

### [ ] P058.3.4 Remove the TUI route overlay
Read:
- docs/design/tui.md#конфигурация-прогона
- docs/design/runner.md#поток-событий
- internal/runner/configuration.go
- internal/runner/chain.go
- internal/runner/start_preview.go
- internal/runner/events.go
- internal/tui/models.go
- internal/tui/recovery.go
Do:
- Удали редактор `m` (`internal/tui/models.go`, `internal/tui/models_test.go`) и чтение/запись `<git-dir>/docflow/tui/configuration.json` в TUI.
- Удали из раннера оверлей `RunConfiguration` (`overrides` в `unitExecutorChain`, `LoadConfiguration`, `SaveConfiguration`, `ValidateConfiguration`); `EffectiveAssignments` и `AssignmentLines` считают назначения только из плана и остаются для диалогов старта и восстановления.
- Событие `config` потока событий сохраняет форму `v: 1`: поле overrides больше не пишется, эффективная таблица остаётся. Старый файл `configuration.json` на диске игнорируется без ошибки.
- Предупреждение «Resume начнёт новую сессию» переносится в диалог восстановления: если шапка плана после паузы дала пойманному юниту другой маршрут.
- Обнови тесты `internal/runner` и `internal/tui`, которые проверяли оверлей: сценарии старта и восстановления остаются, источник назначений — шапка плана.
- Перепиши «Конфигурацию прогона» в `docs/design/tui.md` (назначения только из шапки, лимиты `--attempts/--replans`) и упоминания оверлея в `docs/design/runner.md`.
Verify: `go test -short -timeout 20m ./internal/runner/ ./internal/tui/`
Traps: `internal/runner` целиком идёт больше 10 минут — запускай с `-run` по затронутым тестам, затем `-short`; медленные интеграционные тесты раннера проверяет gate. `grep -rn RunConfiguration internal/` после правки должен остаться пустым.
Role: heavy
Commit: P058.3.4: remove the TUI route overlay

## Phase 4: Run screen

### [ ] P058.4.1 Merge streaming deltas into message blocks
Read:
- internal/tui/activity.go:L1-330
- internal/agentio/activity.go:L1-240
- internal/tui/activity_test.go:L1-200
Do:
- Вынеси историю активности в функции без Bubble Tea (тот же пакет или `internal/runner`, на выбор по импортам): дельта (`Activity.Delta`) с тем же `Activity.Message`, что у последней открытой записи юнита, дописывает её текст, а не создаёт запись; финальное сообщение закрывает блок и меняет состояние на итоговое.
- Текст блока в памяти ограничь 64 KiB: дальше блок помечается `truncated`, полный текст читается из лога по `Ref` первой дельты.
- Лимит истории на юнит считает блоки; инструмент, verify и события раннера остаются отдельными записями в порядке прихода.
- Тесты: 5000 дельт рассуждения дают одну запись и не вытесняют вызовы инструментов; чередование reasoning и assistant с разными ключами даёт два блока; усечение на 64 KiB; финал без дельт — одна запись.
Verify: `go test -timeout 20m ./internal/tui/ ./internal/agentio/`
Traps: парсер уже вычитает отправленные дельты из финального текста (`messageState.emitted`) — не дублируй текст при закрытии блока.
Depends: P058.1.1
Commit: P058.4.1: merge streaming deltas into message blocks

### [ ] P058.4.2 Render the live run screen
Read:
- internal/tui/activity.go
- internal/tui/view.go
- internal/tui/execute.go:L170-340
- internal/runner/recovery.go:L100-110
Do:
- Шапка экрана «Прогон»: план, время прогона, юнит `n/m`, попытка `k/N`, сумма токенов по usage (неизвестное — `?`, не ноль), текущий маршрут.
- Слева — юниты плана: статус, длительность, маршрут; под ними полоса этапов текущего юнита `prompt · work · verify · done · commit` по событиям раннера.
- Справа — поток выбранного юнита: рассуждения полным текстом, приглушённо, с префиксом `◇`; сообщения агента полным текстом; вызов инструмента — одна строка (`⚙`, имя, аргумент, состояние, длительность), `Enter` раскрывает результат существующими страницами лога; изменения файлов — `✎ путь`; verify, смены маршрута, квота и replan — строки раннера.
- `t` сворачивает все блоки рассуждений до первой строки и обратно; follow (`f`, `G`) и ручная прокрутка работают как раньше.
- Тесты: снимки экрана на фикстурном потоке с reasoning, tool и verify на 120x40 и 60x20, `t`, follow не двигает читателя старой истории, escape-последовательность в рассуждении выводится обезвреженной.
- Опиши экран «Прогон» в `docs/design/tui.md` (раздел «История и просмотр»).
Verify: `go test -timeout 20m ./internal/tui/`
Traps: страница живого результата читает лог от `ref.Offset` до текущего конца через `agentio.ReadLiveActivityPage` — сохрани это поведение для раскрытого инструмента.
Role: heavy
Commit: P058.4.2: render the live run screen

## Phase 5: Project and history

### [ ] P058.5.1 Manage plans and commits on the project screen
Read:
- docs/design/tui.md#lock-и-lifecycle
- docs/design/cli.md
- internal/tui/execute.go:L630-766
- internal/tui/view.go
- internal/format/doc.go:L1-240
Do:
- Экран «Проект»: слева Queue (прогресс `x/N` и полоса), Frozen, строка Backlog со счётчиком; справа карточка плана: Goal, маршруты шапки, предупреждение расхождения с вариантом, последний прогон (`lastRunText`).
- Клавиши: `Enter` — дерево фаз и юнитов плана (нынешний вид), `a/F/T/C` как раньше, `N` — новый план через `docflow new` (диалог slug), `e` — открыть план в `$VISUAL`/`$EDITOR` (на Windows без переменных — `notepad`) с приостановкой экрана и перечитыванием после выхода.
- `c` коммитит только служебные файлы docflow (`docs/docflow.md`, `docs/plans/`, `docs/archive/`, `docs/roster.variants.toml`) сообщением из последней подсказки `commit:`; диалог показывает файлы и сообщение. Посторонние изменения в коммит не попадают; живой прогон и чужой lock — отказ.
- Тесты: карточка и прогресс на фикстуре, `N` создаёт план и строку Queue, `c` коммитит только служебные пути во временном git-репозитории, отказ при живом прогоне, `e` с подменённым редактором.
- Опиши экран «Проект» в `docs/design/tui.md`.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: `tea.ExecProcess` приостанавливает рендер — после возврата перечитай рабочее место и размер терминала. Коммит — `git commit -- <пути>`, не `git add -A`.
Depends: P058.3.3
Commit: P058.5.1: manage plans and commits on the project screen

### [ ] P058.5.2 Edit the Backlog and plan a line with an agent
Read:
- docs/design/format.md#docflowmd
- internal/format/doc.go
- internal/agents/agents.go:L1-200
- internal/roster/workspace.go
- skills/bsv-docflow-plan/SKILL.md:L1-60
Do:
- На «Проекте» `b` открывает Backlog: список строк, `+` добавить, `Enter` изменить (многострочный ввод), `D` удалить с подтверждением; запись — через `format.Doc` с сохранением прочих разделов и переводов строк.
- `P` на строке Backlog запускает планирование: агент и модель — первый маршрут цепочки `heavy` активного брифа ростера (нет цепочки — `executor:`); экран приостанавливается и запускается интерактивный CLI агента с промптом «возьми строку Backlog в план по скиллу `bsv-docflow-plan`: <текст строки>». Таблица интерактивного запуска (`claude`, `codex`, `pi`, `opencode`: argv с моделью и промптом) живёт в `internal/agents` рядом с профилями.
- Агент без интерактивного запуска или отсутствующий CLI — отказ с промптом, который можно скопировать.
- После возврата рабочее место перечитывается; новый план появляется в Queue.
- Тесты: добавление, правка и удаление строк Backlog сохраняют остальной файл байт в байт; argv интерактивного запуска для каждого агента; подменённый запуск получает промпт с текстом строки.
- Опиши Backlog и планирование в `docs/design/tui.md`, таблицу интерактивного запуска — в `docs/design/runner.md#профили-агентов`.
Verify: `go test -timeout 20m ./internal/tui/ ./internal/agents/ ./internal/format/`
Traps: Backlog-строка бывает многострочной (продолжение с отступом) — правка заменяет весь пункт, а не первую строку.
Commit: P058.5.2: edit the Backlog and plan a line with an agent

### [ ] P058.5.3 Show past runs and replay their stream
Read:
- docs/design/runner.md#поток-событий
- internal/runner/recovery.go
- internal/runner/events.go
- internal/agentio/activity.go:L120-210
- internal/tui/activity.go:L1-260
Do:
- Функция без Bubble Tea читает `<git-dir>/docflow/run/*.events.jsonl` и даёт сводку прогона: id, план, начало, длительность, юниты (done/failed/blocked), попытки, смены маршрута, replan, токены по usage (неизвестное — `?`), причина и код завершения.
- Экран «История»: список прогонов, новейший сверху, и карточка выбранного; `Enter` открывает прогон на экране «Прогон» в режиме просмотра: поток восстанавливается повторным разбором логов вызовов (`NewActivityParser`, `Feed`, `Finalize`) по ссылкам `stream`/`attempt` журнала; управляющие клавиши прогона в этом режиме отказывают с подсказкой.
- Отсутствующий лог вызова — блок `лог недоступен`, а не ошибка всего прогона; битая строка журнала пропускается со счётчиком.
- Тесты: сводка фикстурного журнала с retry и route_switch, повтор потока даёт те же блоки, что живой прогон на тех же байтах, отсутствующий лог.
- Опиши экран «История» в `docs/design/tui.md`.
Verify: `go test -timeout 20m ./internal/tui/ ./internal/runner/ -run 'History|Replay|Summary'`
Traps: пути логов в журнале относительны корню checkout — разрешай их тем же помощником, что страница активности (`resolveLogPath`).
Depends: P058.4.2
Commit: P058.5.3: show past runs and replay their stream

### [ ] P058.5.4 Edit project and plan parameters
Read:
- docs/design/format.md#docflowmd
- docs/design/format.md#план
- internal/format/doc.go:L80-200
- internal/tui/app.go
- cmd/docflow/main.go:L240-330
Do:
- На «Проекте» `o` открывает параметры: проекта (`read_budget`, `start_read`, `start_budget` из `docs/docflow.md`), выбранного плана (`permissions`, `timeout`, `gate`) и сессии (лимиты `attempts`, `replans`, по умолчанию из флагов `--attempts/--replans`).
- Правка файла — через `format.Doc.SetMeta`, затем `lint` плана или проекта; ошибка `lint` откатывает файл и показывает причину. `permissions: full` требует подтверждения с объяснением, что снимается песочница.
- Лимиты сессии применяются к следующему старту и не пишутся на диск.
- Lock-проверка на запись, notice `commit:` после правки.
- Тесты: каждый ключ пишется и проходит lint, неверный `timeout` откатывается, лимиты сессии доходят до `runner.Args` следующего старта.
- Опиши параметры в `docs/design/tui.md`.
Verify: `go test -timeout 20m ./internal/tui/ ./cmd/docflow/`
Depends: P058.5.1
Commit: P058.5.4: edit project and plan parameters

## Phase 6: Documentation and acceptance

### [ ] P058.6.1 Document the workspace for users
Read:
- docs/usage.md#терминальное-приложение
- docs/usage.md#ростер
- README.md
- README.ru.md
- CHANGELOG.md:L1-40
- cmd/docflow/help_text.go:L400-430
Do:
- Перепиши раздел «Терминальное приложение» `docs/usage.md`: четыре экрана, клавиши каждого, варианты ростера и применение, просмотр рассуждений, история, Backlog и планирование, параметры; в «Ростере» — файлы вариантов.
- Обнови пункт про TUI в `README.md` и `README.ru.md` одинаково по смыслу; строку `tui` в таблице `docs/design/cli.md` и `docflow tui --help` (флаг `--roster`).
- Добавь в `CHANGELOG.md` под `## [Unreleased]` пользовательские изменения: экраны, ростер в TUI, варианты, удаление `m` и `configuration.json`, поток рассуждений, история, Backlog.
Verify: `go test -timeout 20m ./cmd/docflow/ && grep -q 'roster.variants.toml' docs/usage.md && grep -q 'Unreleased' CHANGELOG.md`
Depends: P058.5.4
Role: cheap
Commit: P058.6.1: document the TUI workspace

### [ ] P058.6.2 Accept the workspace end to end
Read:
- internal/tui/acceptance_test.go:L1-200
- internal/tui/terminal_test.go:L1-200
- internal/tui/terminal_fixture_test.go
Do:
- Сценарий «ростер → план»: на фикстурном ростере выключить доступ, сохранить проектный вариант, применить к плану, проверить шапку плана и `docs/roster.variants.toml`, закоммитить служебные файлы через `c`.
- Сценарий «прогон»: запустить план с фейковым агентом, который стримит рассуждение дельтами, вызов инструмента и сообщение; на «Прогоне» видны полный текст рассуждения, строка инструмента и verify; `t` сворачивает; `p` ставит паузу, `R` продолжает.
- Сценарий «история»: после прогона «История» показывает его сводку, `Enter` повторяет поток с тем же текстом рассуждения.
- Сценарий «Backlog»: добавить строку, `P` с подменённым интерактивным агентом получает промпт с её текстом.
- Каждый `Unverified:` из Handoff юнитов плана закрой тестом, `Accepted: <пункт> — <почему не риск>` или оставь `Unverified:` с конкретным сценарием отказа.
- Сценарии проходят через настоящий терминальный слой (`terminal_test.go`, PTY на Linux/macOS, ConPTY на Windows), как существующие приёмочные тесты P052.
Verify: `go test -timeout 30m ./...`
Traps: падающий сценарий чини в этом же юните, если правка маленькая; иначе `blocked` с причиной.
Depends: P058.1.1, P058.2.1, P058.2.2, P058.3.1, P058.3.2, P058.3.3, P058.3.4, P058.4.1, P058.4.2, P058.5.1, P058.5.2, P058.5.3, P058.5.4, P058.6.1
Role: acceptance
Commit: P058.6.2: accept the TUI workspace end to end
