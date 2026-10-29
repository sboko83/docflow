---
id: P052
title: Interactive terminal control for plan execution
type: feature
status: draft
created: 2026-10-29
executor: codex | gpt-6-luna | high
fallback: pi | zai/glm-5.3 | high
review: pi | zai/glm-5.3 | medium; codex | gpt-6.1-sol | medium
acceptance: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high
permissions: auto
timeout: 7200
gate: go test -timeout 20m ./...
---
## Goal

`docflow tui` открывает полноэкранное приложение в текущем терминале:
дерево фаз и юнитов, раскрываемое задание и живая история сообщений,
инструментов, изменений файлов и проверок. Перед запуском пользователь
выбирает модели и исполнителей, reasoning и запасные цепочки для ролей
и отдельных юнитов; фактически применяемые назначения видны до старта.

Пользователь запускает план, приостанавливает текущего агента,
продолжает, останавливается после юнита или завершает работу приложения.
После повторного открытия он видит сохранённое состояние и может
продолжить незавершённую работу без потери файлов и выполненных юнитов.
Блокировки и ошибки объясняют, что делать дальше. Результат проверен
автоматическими сценариями и живым прогоном Codex и Pi/GLM на этом Mac.

## Context

Запрос владельца: терминальное приложение по образцу CLI агентов, в
котором видно план и можно раскрыть задачу, понять действия LLM,
поменять исполнителей перед запуском и управлять паузой и продолжением.
Строка Backlog про интерактивный режим взята в этот план. P051 выполняется
отдельно; его бенчмарк и корпус не меняются.

- docs/design/runner.md#цикл-на-юнит
- docs/design/cycle.md#ветки-и-параллельность
- docs/design/cli.md#cli-docflow

## Constraints

- Одна команда `docflow tui` в существующем самодостаточном бинарнике.
  Go 1.24 и CGO=0 сохраняются. macOS, Linux и Windows остаются целевыми
  платформами; обязательная живая приёмка — текущий Mac.
- Формат плана остаётся docflow: 3, события — v: 1 с совместимыми
  дополнениями. Существующие команды и обычный `docflow run` сохраняют
  проверки порядка, коды выхода, вывод, блокировки и работу без TTY.
- Приложение использует общий раннер и цикл prompt → verify → done →
  commit; собственного механизма проверки или коммита юнита нет.
  Пропуск проверки, ручное выставление [x] и обход lock запрещены.
- Назначения этого плана скопированы из P051: Codex Luna с запасом
  Pi/GLM; review и acceptance — цепочки из P051. Strata и Claude в
  исполнителях этого плана отсутствуют. Права auto, авто-replan выключен.
  Поддержка отображения и адаптера Claude в самом продукте сохраняется.
- Настройки моделей в TUI — явная конфигурация конкретного прогона,
  сохранённая вне рабочего дерева; план и личный ростер от неё не
  переписываются. Запуск показывает все эффективные назначения.
- Один управляемый прогон на checkout; без фонового сервиса и detached
  агента после закрытия TUI. Чужой живой раннер не перехватывать.
- UI и сообщения CLI — English, документация в docs/ — русский.
  Проектные инструкции, установленные скиллы и личные настройки не менять.

## Prerequisites

- Для обязательной живой приёмки доступен текущий Mac с CLI Codex и Pi:
  `test "$(uname -s)" = Darwin && command -v codex >/dev/null && command -v pi >/dev/null`
- Владелец обеспечивает действующую авторизацию Codex и Pi для
  zai/glm-5.3 в настройках этих CLI вне репозитория. Разрешён расход
  квоты на маленьком временном проекте: до четырёх вызовов каждого
  агента, не более 300 секунд на вызов, без автоматического расширения
  лимита. Значения секретов в план и отчёты не попадают.

## Traps

- Текущий `run` сам владеет stdin, сигналами и выводом Progress. Два
  читателя клавиатуры или параллельная ANSI-печать разрушат экран;
  терминалом владеет только UI, раннер получает типизированные события.
- `s + Enter` и `run --stop` завершают прогон после юнита; это ещё не
  пауза текущего агента. SIGSTOP не является переносимым решением и
  оставляет дочерние инструменты работающими.
- Пауза — отдельная причина отмены, не ошибка провайдера и не новая
  попытка. Уже потраченные токены учитываются; время паузы не расходует
  active/idle timeout. Paused виден только после остановки дочерних
  процессов и записи состояния.
- verify/gate и транзакцию done/commit довести до устойчивой границы
  перед подтверждением паузы. Пока это происходит, показывать Pausing
  и текущий этап. Pending commit восстанавливает существующий цикл.
- `run --plan` сейчас допустим только при dry-run. Просмотр любого
  draft/frozen-плана не даёт права его запустить: запуск — только
  текущего active-плана Queue, активация и thaw требуют явного действия.
- Сессия агента возобновляется только на том же agent/model/effort.
  При смене исполнителя или недоступной сессии стартует новая с полным
  промптом, текущим деревом и сводкой прерывания; UI это объясняет.
- Логи могут быть большими, частично записанными и содержать управляющие
  последовательности. Не исполнять escape-последовательности из вывода
  агента, не держать весь прогон в памяти и не терять исходный лог.
- Будущие файлы в Read создаются явно названными предыдущими юнитами.
  Во время планирования lint может предупреждать об их отсутствии;
  к prompt соответствующего юнита файлы обязаны существовать.

## Decisions

- TUI входит в основной бинарник как `docflow tui` — единая установка и
  общий раннер — отвергнуты веб-интерфейс и отдельный docflow-tui.
- Bubble Tea v1.3.10 и поставляемый с ним Lip Gloss v1.1.0 — обработка
  клавиш, resize и отрисовка не пишутся вручную; версия сохраняет Go
  1.24 — отвергнут автоматический переход на Bubble Tea v2.1.1,
  требующий Go 1.26. Источники:
  [v1 go.mod](https://github.com/charmbracelet/bubbletea/blob/v1.3.10/go.mod),
  [v2 go.mod](https://github.com/charmbracelet/bubbletea/blob/v2.1.1/go.mod).
- Для интерактивного запуска вводится видимый снимок конфигурации
  прогона в `<git-dir>/docflow/tui/`: назначения из плана плюс явно
  выбранные изменения — смена моделей не делает дерево грязным и
  воспроизводится после открытия TUI. Это расширяет правило раннера
  «исполнитель только из плана» для интерактивного API; обычные флаги
  `run --model/--agent/--effort` в executor-режиме остаются запрещены.
  Отвергнуты скрытая подмена модели и коммит настроек поверх незавершённой
  работы агента.
- Действия доступны с клавиатуры; Enter раскрывает, Tab меняет панель,
  стрелки перемещают выбор, / ищет в истории, ? показывает помощь.
  Базовая компоновка — дерево слева и детали справа от 100 колонок,
  на узком терминале — одна панель с переключением.
- В первой версии Pause прерывает текущий вызов агента и сохраняет
  возможность продолжения; Stop after unit — отдельное действие.
  Выход при живом прогоне предлагает остаться или остановить и выйти.
  Отвергнута незаметная работа агента после закрытия приложения.
- Интерфейс показывает сообщения и действия, которые выдаёт CLI агента,
  и исходный поток для неизвестных событий. Отсутствующие usage, quota
  или результаты инструментов обозначаются как неизвестные, не нулевые
  и не успешные.
- Живой прогон обязателен, выполняется только в последнем юните на
  временном проекте вне репозитория. Предыдущие юниты расходуют только
  локальные ресурсы и используют фиктивные CLI.

## Phase 1: Controllable runner and activity stream

### [ ] P052.1.1 Expose an interactive runner host
Read:
- internal/runner/run.go:L132-292
- internal/runner/helpers.go:L436-598
- internal/runner/events.go
- internal/runner/start_test.go
- internal/runner/terminal_run_test.go:L348-456
Do:
- Выдели в internal/runner/control.go API интерактивного хозяина прогона: контекст, команды управления, запросы подтверждения/снятия human-блока и наблюдение за состоянием; опиши состояния Idle, Starting, Running, Pausing, Paused, Stopping, Blocked, Finished, Failed.
- Раздели общую механику Run и терминальный адаптер: старый Run сохраняет stdin, сигналы и Progress; интерактивный вызов не читает os.Stdin и не пишет ANSI в обход хозяина.
- Публикуй типизированные события из существующих точек раннера, не создавая второй цикл исполнения; медленный наблюдатель не должен блокировать сбор stdout, дедлайны или завершение процесса.
- Создай internal/runner/control_test.go: взаимоисключение команд, отмена стартового подтверждения, повторный Start и lock занятого checkout; проверь прежние run/TTY-сценарии.
Verify: `go test -timeout 20m ./internal/runner/ ./cmd/docflow/`
Depends: -
Commit: P052.1.1: expose interactive runner host

### [ ] P052.1.2 Pause and resume active work safely
Read:
- internal/runner/control.go
- internal/runner/run.go:L299-435
- internal/runner/helpers.go:L246-337
- internal/agentio/process.go
- internal/procgroup/procgroup.go
- internal/runner/verify_guard_acceptance_test.go:L330-420
Do:
- Реализуй Pause/Resume и Stop after unit через общий контроллер; Pause отменяет текущий вызов, ожидает завершения группы процессов, затем подтверждает Paused; ожидание квоты прерывается сразу, verify/gate и done/commit доходят до устойчивой границы.
- Создай internal/runner/checkpoint.go: атомарный версионированный checkpoint под <git-dir>/docflow/tui/ с plan/unit, HEAD, fingerprint плана, этапом, agent/model/effort, session id, счётчиками и ссылками на логи; не сохраняй секреты.
- Продолжай ту же сессию при совместимом назначении; если session id отсутствует или resume отвергнут, один раз начни новую с полным промптом, текущим деревом и причиной восстановления. Не считай Pause отказом, не запускай fallback и не сбрасывай лимиты попыток или остаток активного timeout; классифицируй эту отмену до обработки отсутствующего result JSON.
- Останови всю группу инструментов при отмене; после Paused новые агенты не запускаются. Не возвращай готовую проверку после изменения дерева и не повторяй завершённый commit.
- Создай internal/runner/pause_test.go и checkpoint_test.go: пауза на tool-call, quota wait, verify, commit boundary, повторные команды, resume без сессии, исключение времени паузы из таймаута и отсутствие сирот.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/agentio/ ./internal/procgroup/`
Commit: P052.1.2: pause and checkpoint active work

### [ ] P052.1.3 Preserve inspectable agent activity
Read:
- internal/agentio/output.go:L205-374
- internal/agentio/process.go:L51-173
- internal/agentio/testdata/codex.jsonl
- internal/agentio/testdata/claude.jsonl
- internal/agentio/testdata/pi.jsonl
- internal/runner/events.go
Do:
- Создай internal/agentio/activity.go и activity_test.go: нормализуй доступные сообщения, tool start/end, аргументы и результат, команды, file changes и usage для Codex, Claude и Pi; исходный фрагмент сохраняй для неизвестного типа.
- Добавь подписку на stdout/stderr и результаты verify/gate без обрезки исходного файла; большие payload отдавай через смещения и постраничное чтение. Не меняй краткий StreamEvent/Progress обычного run.
- Свяжи activity с run/unit/attempt/agent и последовательностью событий; отличай running, succeeded, failed, cancelled и неизвестный результат, не дублируй delta и завершённое сообщение.
- Дополняй JSONL v: 1 совместимыми событиями состояния/конфигурации и ссылками на потоки; прежние имена и поля сохраняй. Снимки конфигурации не включают секреты и тела сообщений.
- Расширь events_test.go и тесты парсера случаями частичной строки, stderr до завершения, неизвестного события, отменённого инструмента и медленного читателя.
Verify: `go test -timeout 20m ./internal/agentio/ ./internal/runner/`
Commit: P052.1.3: expose inspectable agent activity

### [ ] P052.1.4 Review runner control and recovery
Read:
- internal/runner/control.go
- internal/runner/checkpoint.go
- internal/agentio/activity.go
- internal/runner/pause_test.go
Do:
- Проверь Phase diff по Goal, Constraints, Decisions и Do юнитов фазы; особенно конкуренцию stdin, полноту остановки процессов, паузу в commit boundary, сохранение лимитов и совместимость обычного run.
- Разбери каждый пункт Test changes: обоснованное изменение допускается, ослабленная ради зелёного Verify проверка — дефект.
- Ничего не редактируй; при дефекте верни blocked с blocker plan и конкретным сценарием, иначе зафиксируй вывод в Handoff.
Verify: `go test -timeout 20m ./...`
Depends: P052.1.1, P052.1.2, P052.1.3
Role: review
Commit: P052.1.4: review runner control

## Phase 2: Terminal workspace and live inspection

### [ ] P052.2.1 Build the terminal plan workspace
Read:
- cmd/docflow/main.go:L1-110
- cmd/docflow/main.go:L209-321
- internal/format/doc.go:L1-180
- internal/cycle/cycle.go:L20-165
- internal/term/term_test.go
- internal/runner/pty_darwin_test.go
Do:
- Добавь закреплённые зависимости из Decisions без смены минимальной Go; создай internal/tui/model.go, view.go, keys.go, app.go и app_test.go, подключи команду tui в cmd/docflow.
- Отобрази Queue, планы, фазы и юниты со статусами; поддержи раскрытие, перемещение, фокус, прокрутку и карточку Goal/Do/Verify/Depends/Handoff выбранного юнита. Читай Markdown существующим парсером, не дублируй формат.
- Покажи loading, empty, error и content с доступным действием, помощь клавиш и текстовый статус кроме цвета. Сохраняй выбор при обновлении плана.
- От 100 колонок используй две панели, ниже — переключаемую одну; проверь 120x40, 80x24, 40x12, кириллицу, длинные пути, resize и NO_COLOR. При недостаточном размере покажи понятное сообщение, не панику.
- Добавь model/view-тесты и реальный терминальный smoke на доступной платформе: открытие, Enter, Tab, resize и выход восстанавливают echo, курсор и экран. Без TTY команда отказывает с подсказкой обычных команд.
Verify: `go test -timeout 20m ./internal/tui/ ./cmd/docflow/`
Depends: P052.1.4
Commit: P052.2.1: add terminal plan workspace

### [ ] P052.2.2 Inspect live messages tools and changes
Read:
- internal/tui/model.go
- internal/tui/view.go
- internal/agentio/activity.go
- internal/runner/events.go
- internal/runner/terminal_accept_test.go:L1-80
Do:
- Создай internal/tui/activity.go, activity_test.go и terminal_test.go: история выбранного юнита с раскрываемыми сообщениями, tool-call, командами, результатами, проверками и переключениями моделей.
- Реализуй follow текущей задачи, паузу автопрокрутки при чтении старого, поиск и переход к последнему событию; навигация по плану не останавливает прогон.
- Покажи фактического исполнителя, reasoning, время, квоту/стоимость когда доступны, причины ожидания и fallback; неизвестные значения показывай явно.
- Отдавай большие результаты постранично из логов с ограниченной памятью; нейтрализуй управляющие последовательности, сохраняй исходный текст для просмотра и различай начало инструмента и подтверждённое завершение.
- Добавь просмотр фактического git diff по запросу и результатов проверок, с явной ошибкой чтения; не выдумывай изменения из сообщения агента. Покрой смешанный поток всех трёх CLI, stderr, невалидный JSON, большой вывод и терминальные escape.
Verify: `go test -timeout 20m ./internal/tui/ ./internal/agentio/`
Commit: P052.2.2: inspect live agent actions

### [ ] P052.2.3 Connect execution and lifecycle controls
Read:
- internal/tui/app.go
- internal/tui/model.go
- internal/runner/control.go
- internal/runner/checkpoint.go
- internal/cycle/lifecycle.go:L165-290
- docs/usage.md#выполнение-юнита
Do:
- Подключи Start, Pause, Resume, Stop after unit и Stop now к общему хозяину раннера; покажи Pausing до подтверждения, запрети повторный Start и недоступные действия.
- Отобрази стартовые предусловия и подтверждение full отдельным диалогом; declining/cancel ничего не запускает. Для needs human покажи причину и действие Continue с заметкой через существующий unblock.
- Добавь явные действия Activate, Freeze/Thaw и Close через существующие lifecycle-команды и их проверки, только без живого прогона; просмотр draft не активирует его, выбор другого плана не меняет Queue скрыто. Close не выдаёт незаполненный архив за готовый: если нужен текст человека, покажи это и путь файла.
- При выходе с живым прогоном предложи остаться либо остановить и выйти; заверши процессы, запиши checkpoint и восстанови терминал. При занятом чужим раннером checkout разреши просмотр, но откажи в запуске и изменении его состояния.
- Расширь app_test.go и terminal_test.go проверками клавиш и реальных переходов на фиктивном агенте: pause/resume, human block, отказ активации с lint-ошибкой и отсутствие фонового агента после выхода.
Verify: `go test -timeout 20m ./internal/tui/ ./internal/runner/ ./cmd/docflow/`
Commit: P052.2.3: connect terminal execution controls

### [ ] P052.2.4 Review terminal behavior and controls
Read:
- internal/tui/app.go
- internal/tui/view.go
- internal/tui/activity.go
- internal/tui/terminal_test.go
Do:
- Проверь Phase diff по Goal, Constraints, Decisions и Do фазы: навигацию при живом выводе, точность статусов, различение Pause и Stop after unit, подтверждения и восстановление терминала.
- Разбери Test changes; проверь, что тесты нажимают клавиши и проверяют результат процесса, а не только строку View.
- Ничего не редактируй; дефект оформляй blocked с blocker plan и воспроизводимым сценарием.
Verify: `go test -timeout 20m ./...`
Depends: P052.2.1, P052.2.2, P052.2.3
Role: review
Commit: P052.2.4: review terminal controls

## Phase 3: Model setup recovery and delivery

### [ ] P052.3.1 Configure models for the run
Read:
- internal/agents/agents.go:L548-605
- internal/agents/agents.go:L701-745
- internal/roster/roster.go:L1-160
- internal/tui/model.go
- internal/runner/control.go
- docs/usage.md#ростер
Do:
- Создай internal/tui/models.go и models_test.go: экран Models and executors с agent/model/effort, порядком запасных исполнителей, назначениями ролей и переопределением отдельных незавершённых юнитов; предложи текущие значения плана и доступный личный ростер.
- Реализуй конфигурацию прогона и её проверку в internal/runner/configuration.go и configuration_test.go: поверх назначений плана применяй только явно подтверждённые изменения, валидируй встроенными проверками агентов и сохраняй эффективный снимок под <git-dir>/docflow/tui/.
- Перед стартом покажи развёрнутую таблицу назначений всех открытых юнитов, включая review/acceptance и fallback; объясни отличие назначенного и фактического исполнителя после лимита. Отмена редактора не меняет снимок.
- Разреши изменение назначений в Idle и Paused, не трогай [x]/[-]; для текущего приостановленного юнита смена тройки требует новой сессии с явным сообщением. Во время Running редактирование запрещено.
- Пиши изменения конфигурации в журнал до следующего вызова агента; не меняй план, ростер, права и авто-replan скрыто. Сохрани прежний отказ обычного run на --model в executor-режиме.
Verify: `go test -timeout 20m ./internal/tui/ ./internal/runner/ ./internal/agents/`
Depends: P052.2.4
Commit: P052.3.1: configure interactive run models

### [ ] P052.3.2 Restore interrupted terminal sessions
Read:
- internal/runner/checkpoint.go
- internal/runner/configuration.go
- internal/tui/app.go
- internal/tui/models.go
- internal/cycle/commit.go:L1-120
- internal/runner/verify_guard_acceptance_test.go:L330-420
Do:
- Добавь восстановление TUI по checkpoint и сохранённым журналам: покажи исход/этап последнего запуска, незавершённый юнит, эффективные назначения и выбор Continue или Start with plan settings.
- Сверь checkout, plan/unit, HEAD и fingerprint плана; при несовместимом изменении объясни причину и потребуй явного нового запуска. Не удаляй рабочие файлы и не переиспользуй устаревший Verify.
- Восстанавливай interrupted verify и pending commit существующим циклом, не повторяй завершённые юниты; запись checkpoint после done/commit не должна оставлять состояние «продолжить уже выполненный юнит».
- Согласуй lock со всеми процессами в общем контроллере: два TUI не стартуют одновременно, новый экземпляр не удаляет lock живого процесса, устаревший lock не блокирует восстановление навсегда.
- Покрой в pause_test.go и terminal_test.go реальное завершение TUI посреди вызова, повторное открытие, смену модели, недоступную сессию, изменённый план, pending commit и два конкурирующих запуска.
Verify: `go test -timeout 20m ./internal/tui/ ./internal/runner/ ./internal/cycle/`
Commit: P052.3.2: restore interrupted terminal sessions

### [ ] P052.3.3 Document and package terminal operation
Read:
- README.md#how-it-works
- README.ru.md
- docs/design/runner.md#старт-прогона
- docs/usage.md#автозапуск
- tools/test-install.sh:L1-100
- tools/smoke-run.sh:L1-100
Do:
- Создай docs/design/tui.md с действующим контрактом состояния, паузы, конфигурации и восстановления; добавь его в индекс docs/design/README.md. Причины решений оставь в Decisions плана до архивации, в теме сошлись на этот план.
- В docs/design/runner.md опиши интерактивный API и совместимые дополнения событий; в docs/usage.md добавь запуск tui, клавиши, выбор исполнителей, Pause/Stop, human block, восстановление и ограничения поддержки терминалов.
- Синхронно обнови README.md и README.ru.md кратким входом в TUI и добавь пользовательское изменение в CHANGELOG.md по bsv-changelog; не дублируй справочник.
- Расширь существующие installer/smoke-проверки наличием tui --help в установленном бинарнике; сохрани одну поставку, отсутствие runtime-зависимостей и CGO=0.
Verify: `go test -timeout 20m ./... && go build -o bin/docflow ./cmd/docflow && bash tools/test-install.sh && bash tools/parity/run.sh bin/docflow bin/docflow`
Commit: P052.3.3: document and package terminal operation

### [ ] P052.3.4 Build a bounded live acceptance driver
Read:
- internal/tui/terminal_test.go
- internal/tui/app_test.go
- tools/smoke-run.sh:L1-100
- tools/test-smoke-run.sh:L1-100
- internal/agentio/testdata/codex.jsonl
- internal/agentio/testdata/pi.jsonl
Do:
- Создай tools/tui-live.sh, драйвер tools/tui-live/main.go и фикстуру tools/tui-live/testdata/ без новых зависимостей: --run строит/использует проверяемый бинарник, создаёт временный проект вне checkout, через терминальный драйвер выполняет два сценария живой приёмки из P052.4.2; --check только проверяет свежую квитанцию результата в git-dir, не вызывает модели.
- Храни квитанцию вне репозитория с HEAD/хешем бинарника, macOS/архитектурой, версиями CLI, фактическими назначениями, подтверждёнными переходами, проверкой результата и путями логов; при превышении лимита вызовов/времени останавливайся. На --run требуется явный вызов; тесты инструмента используют фиктивные CLI.
- Создай tools/tui-live/main_test.go: по умолчанию все тесты драйвера используют фиктивные CLI; покрывают успешную квитанцию, неверную сборку, недоказанную паузу, отсутствие tool-call, исчерпание лимита и ошибку независимой проверки. --check не принимает квитанцию другого HEAD/бинарника и не запускает агентов.
- Переиспользуй терминальные сценарии и фикстуры приложения, не создавай второе определение успеха. Для каждого живого вызова учитывай agent/model/effort, фактический старт процесса и длительность; пауза не обнуляет счётчик вызовов драйвера.
- Проверь --run на фиктивных CLI и --check на созданной им квитанции; живые Codex/Pi в этом юните не вызывай.
Verify: `bash -n tools/tui-live.sh && go test -timeout 20m ./tools/tui-live/ ./internal/tui/`
Commit: P052.3.4: add bounded live acceptance driver

### [ ] P052.3.5 Review configuration recovery and delivery
Read:
- internal/runner/configuration.go
- internal/tui/models.go
- internal/tui/terminal_test.go
- tools/tui-live.sh
- docs/design/tui.md
Do:
- Проверь Phase diff по Goal, Constraints, Decisions и Do фазы: совпадение выбранных и фактических моделей, сохранность плана/ростера, восстановление после прерывания и изоляцию живого проекта.
- Проверь Test changes, лимит расхода живой приёмки и привязку квитанции к проверяемой сборке; квитанция без реальных инструментов/паузы не считается успехом.
- Ничего не редактируй; дефект оформляй blocked с blocker plan и конкретным сценарием.
Verify: `go test -timeout 20m ./...`
Depends: P052.3.1, P052.3.2, P052.3.3, P052.3.4
Role: review
Commit: P052.3.5: review interactive configuration

## Phase 4: Acceptance

### [ ] P052.4.1 Accept the complete terminal workflow
Read:
- internal/tui/terminal_test.go
- internal/tui/app_test.go
- internal/runner/pause_test.go
- internal/agentio/testdata/codex.jsonl
- internal/agentio/testdata/claude.jsonl
- internal/agentio/testdata/pi.jsonl
- .github/workflows/ci.yml
Do:
- Создай internal/tui/acceptance_test.go: запускай собранный CLI в настоящем PTY/консоли с фиктивными CLI того же формата потока, управляй клавишами и проверяй состояние/побочные эффекты, а не только наличие текста.
- Проверь пользовательский путь просмотра и раскрытия плана, выбора моделей до старта, запуска, чтения текущего и прошлого юнита, сообщений/tool result/stderr/diff/verify, ручной прокрутки и возврата к live.
- Проверь Pause во время агента с дочерним инструментом, отсутствие активности после Paused, Resume без расхода попытки на паузу, Stop after unit без старта следующего, Stop now и повторное открытие с продолжением.
- Проверь назначенные и фактические agent/model/effort, fallback при лимите, изменение модели на паузе с новой сессией, честные неизвестные квоты и сохранность плана/ростера.
- Проверь human block и заметку, отказ full без согласия, запрет запуска draft/чужого runner, чистое восстановление pending commit и неизменность результатов обычного run.
- Прогони narrow/resize/Unicode/NO_COLOR, большой поток, неизвестное событие, повреждённый checkpoint и восстановление терминала; подключи платформенные сценарии к существующей CI-матрице macOS/Linux/Windows. Не называй кросс-компиляцию проверкой поведения Windows.
- Разбери все Unverified из Handoff, Test changes и Replan changed check: закрой тестом, обоснуй Accepted либо оставь конкретный незакрытый сценарий. Малую ошибку исправь здесь; существенную оформи blocked с blocker plan, не ослабляя приёмку.
Verify: `go test -timeout 20m ./... && go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow && bash tools/test-install.sh`
Depends: P052.1.1, P052.1.2, P052.1.3, P052.1.4, P052.2.1, P052.2.2, P052.2.3, P052.2.4, P052.3.1, P052.3.2, P052.3.3, P052.3.4, P052.3.5
Role: acceptance
Commit: P052.4.1: accept terminal workflow

### [ ] P052.4.2 Accept live Codex and Pi execution
Read:
- tools/tui-live.sh
- internal/tui/acceptance_test.go
- docs/design/tui.md
- docs/usage.md#автозапуск
Do:
- До длительной работы проверь предусловия: этот Mac, действующая авторизация Codex и Pi/GLM и оставшаяся квота. Если нужен вход или доступ человека, сразу верни blocked с blocker human; не заменяй живую проверку фиктивной.
- Запусти bash tools/tui-live.sh --run для codex | gpt-6-luna | high и pi | zai/glm-5.3 | high: отдельные маленькие временные Go-проекты с двумя детерминированно проверяемыми юнитами, --attempts 1, авто-replan выключен, запасные исполнители для этого сценария выключены.
- В каждом прогоне через настоящий терминальный драйвер раскрой задачу, проверь выбранную модель и фактический процесс, дождись сообщения и живого tool-call, нажми Pause во время активного вызова, дождись Paused и подтверди отсутствие работающего агента/инструмента.
- Продолжи, проверь успешный Verify и commit первого юнита, Stop after unit до запуска следующего, выйди и открой TUI вновь, затем заверши второй юнит. Проверь результат независимым тестом, историю действий, фактические назначения и чистое дерево временного проекта.
- Соблюдай общий лимит Prerequisites: до четырёх реальных вызовов на каждого агента, до 300 секунд на вызов. Не подменяй реальный отказ фикстурой и не расходуй подписку ради искусственного лимита; если бюджет исчерпан, верни blocked с blocker human и точным оставшимся сценарием.
- Проверь квитанцию --check на текущей сборке, перечисли проверенный macOS/архитектуру и реальные исходы в Handoff; при любом изменении сборки повторная живая проверка необходима и остаётся в пределах разрешённого бюджета.
- Закрой оставшиеся Unverified/Test changes/Replan changed check по правилам приёмки. Малую ошибку исправь в этом юните с повторной проверкой; крупная требует blocked с blocker plan, без удаления живой приёмки.
Verify: `bash tools/tui-live.sh --check && go test -timeout 20m ./...`
Depends: P052.1.1, P052.1.2, P052.1.3, P052.1.4, P052.2.1, P052.2.2, P052.2.3, P052.2.4, P052.3.1, P052.3.2, P052.3.3, P052.3.4, P052.3.5, P052.4.1
Role: acceptance
Commit: P052.4.2: accept live terminal execution
