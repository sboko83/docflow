---
id: P052
title: Interactive terminal control for plan execution
type: feature
status: active
created: 2026-10-29
executor: pi | opencode-go/deepseek-v4.1-flash | low
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | medium
review: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6.1-sol | medium
acceptance: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6.1-sol | high
permissions: auto
replan: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6.1-sol | medium
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
автоматическими сценариями и живым прогоном Pi и Codex на этом Mac.

## Context

Запрос владельца: терминальное приложение по образцу CLI агентов, в
котором видно план и можно раскрыть задачу, понять действия LLM,
поменять исполнителей перед запуском и управлять паузой и продолжением.
Строка Backlog про интерактивный режим взята в этот план. Бенчмарк закрыт
в [P051](../archive/P051-benchmark.md); его инструмент и корпус не меняются.

- docs/design/runner.md#цикл-на-юнит
- docs/design/cycle.md#ветки-и-параллельность
- docs/design/cli.md#cli-docflow

## Constraints

- Одна команда `docflow tui` в существующем самодостаточном бинарнике.
  Минимальная версия Go поднимается до 1.26, CGO=0 сохраняется. macOS,
  Linux и Windows остаются целевыми платформами; обязательная живая
  приёмка — текущий Mac.
- Формат плана остаётся docflow: 3, события — v: 1 с совместимыми
  дополнениями. Существующие команды и обычный `docflow run` сохраняют
  проверки порядка, коды выхода, вывод, блокировки и работу без TTY.
- Приложение использует общий раннер и цикл prompt → verify → done →
  commit; собственного механизма проверки или коммита юнита нет.
  Пропуск проверки, ручное выставление [x] и обход lock запрещены.
- Назначения пересоставлены по ростеру 2026-11-01: исполнитель —
  opencode-go через Pi, запас — zai через Pi, затем Codex; review и
  acceptance ведёт opencode-go с запасной ступенью Codex. Strata из
  исполнителей плана исключён, Claude отсутствует. Права auto,
  авто-replan включён (см. Decisions: четвёртый блок подряд). Поддержка
  отображения и адаптера Claude в самом продукте сохраняется.
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
- Владелец обеспечивает действующую авторизацию Codex и подписку
  opencode-go в настройках CLI вне репозитория; запасной маршрут zai
  через Pi требует доступа к zai. Разрешён расход квоты Codex на
  маленьком временном проекте: до четырёх вызовов, не более 300 секунд
  на вызов, без автоматического расширения лимита; маршруты Pi
  расходуют подписку. Значения секретов в план и отчёты не попадают.

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
- Высота тела и высота контента панели — разные величины: заголовок
  панели и notice-строки занимают строки внутри `bodyHeight()`, поэтому
  видимость проверяется по отрисованному экрану, а не по числу строк,
  которое посчитал помощник панели.

## Decisions

- TUI входит в основной бинарник как `docflow tui` — единая установка и
  общий раннер — отвергнуты веб-интерфейс и отдельный docflow-tui.
- Bubble Tea v2.1.1 и Lip Gloss v2.0.6 — обработка клавиш, resize и
  отрисовка не пишутся вручную. Минимальная версия Go поднимается до
  1.26.0, которого требует v2.1.1: свежая ветка библиотеки дороже
  сохранения Go 1.24 — отвергнута привязка к Bubble Tea v1.3.10,
  оставляющая TUI на устаревающей ветке. Источник:
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
- Добавил Control host как единственную точку команд и ограниченную очередь событий — медленный UI не задерживает runner; вместо отдельного цикла используется существующий Run/runLoop.
- Интерактивный запуск использует общий checkout lock и отключает terminal input и Progress output — обычный Run сохраняет прежний терминальный режим.
- Replan P052.1.2: юнит не влез в одну сессию — первый прогон успел только checkpoint-примитив; разбил на P052.1.2 (Pause, устойчивые границы, checkpoint) и P052.1.5 (Resume, восстановление сессии, Stop after unit, учёт таймаута) — отвергнут один большой юнит.
- Replan P052.1.2 (2-й): блокер «roster-тест падает в gate» не воспроизведён — 2026-11-01 полный `go test ./...` зелёный, падение списано на нагрузку той сессии, сценарий не менялся; блокер «quota-тест не проверяет классификацию отмены провайдера» закрыт выписыванием сценария явно в тест-список Do — отвергнут вариант оставлять требование неявным в тексте классификации.
- A fixed calendar fixture in roster tests is replaced by a now-relative day — the old fixture expired on 2026-11-01 and broke the gate independently of runner code — rejected keeping a fixed date and changing LoadState's expiry rules.
- Store the remaining timeout in the checkpoint rather than active elapsed time — repeated resumes would otherwise subtract already-spent time twice — rejected an accumulated active-duration field.
- Pin the checkpoint agent/model/effort when that exact route is still available and only then resume its session; a mismatch starts one new full-prompt session — rejected silently pairing an old session with a new assignment.
- Detect a rejected resume from provider text with the same one-new-session guard — rejected adding an agent-specific exit-code contract that the three CLIs do not share.
- Write stderr to a sidecar <call>.stderr while keeping the original call log byte-identical — stderr offsets cannot be known live in the one-file layout, and rewriting the log layout breaks parity — rejected interleaving stderr into the main log or resolving stderr offsets only after the call.
- Do not persist one JSONL event per activity — an fsync per agent line and duplicated message bodies would slow ordinary runs and bloat the event stream; stream links plus the parser rebuild history — rejected per-line activity events.
- Name verify logs verify.<uid>.<n>.out — the uid.*.out glob is the call-log namespace counted by tests and checkpoint recovery — rejected <uid>.verify.<n>.out.
- Always write the stderr sidecar, even outside an interactive run, so every stream link resolves — rejected an interactive-only sidecar with dangling paths in normal runs.
- Turn auto-replan on for the rest of P052 after four stops in ten hours — a plan with review and acceptance units without self-healing stops on the first defect it finds, and review findings are expected work rather than a planning error; `check_replan` already forbids weakening the review's own `Verify:`/`Do:`/`Role:`. Rejected lifting every block by hand, as in P052.1.2 three times.
- Move the P052.1.4 findings into units P052.1.6 (Pause boundaries in the runner: return after the checkpoint on the rejected-resume path, publish `state: Paused` at the executor-chain wait and in pending-commit recovery) and P052.1.7 (clip `Activity.Input`), and return the review to `[ ]` with `Depends:` on them — rejected fixing the defects inside the review unit, which edits nothing.
- Correct the first block record: the P052.1.2 attempt was 745 seconds and ended with the agent's own request to rethink the scope, with a green `docflow verify` and no measured limit; the six-hour gap is the pause between runs, not a session. The unit was large, but "did not fit one session" was the replan agent's paraphrase, not a context overflow — rejected keeping it as a diagnosis.
- Classify Pause before `resumeRejected` on the success path too, not only where the resume was actually rejected — a pause must never race into another session or attempt — rejected guarding only the rejected-resume branch.
- The pre-loop executor wait reports `attempt: 0` through the shared `pauseState` helper — no attempt was spent there — rejected a separate event shape without the attempt field.
- Clip strings with a margin of 256 bytes under the inline limit — leaves room for keys, quotes and braces so one long argument is clipped rather than replaced wholesale by the marker — rejected a fixed per-string rune cap that ignores the number of values and the JSON overhead.
- Keep the marker as {"truncated": true, "bytes": original serialized size} — a machine-readable size, no duplicated payload text — rejected embedding the clipped text plus size in the marker.
- Return the P052.1.4 findings to a new Phase 1 unit P052.1.8 and reopen the review with `Depends` on it — a controlled Pause is journaled as `unit_end.status=error`, `run_end.reason=context canceled`, `exit_code=2`, and the complete boundary writes its checkpoint without `state: Paused`; the review unit edits nothing, and P052.1.6 is already committed — rejected fixing the defects inside the review and rejected reopening P052.1.6.
- A controlled Pause closes the journal as an event of its own, not as a failure: `unit_end.status: paused` for a unit the pause stopped, `run_end` with `reason: paused`, `paused: true` and `exit_code: 0` — the interactive host treats that stop as success; the unit finished at the complete boundary keeps `done` — rejected keeping the internal codes 7/2 in the journal, where 7 already means "no executor available" and 2 means a runner failure.
- Классифицировать паузу в отложенных эмиттерах unit_end/run_end через pauseCancellation, а не через новые коды — внутренние 7/2 уже заняты «нет исполнителя» и ошибкой раннера, журнал должен отличать паузу от сбоя — отвергнуто переиспользование кодов 7/2 как маркера паузы.
- pausedUnitStatus сохраняет done на complete boundary — юнит действительно выполнен, пауза видна в state и run_end — отвергнуто помечать paused любой юнит под паузой.
- Поле paused добавляется в run_end только под паузой — читатели версии 1 не видят ложной паузы в обычных и сигнальных прогонах — отвергнуто постоянное поле paused:false.
- Второй блокер P052.1.4 уходит в новый юнит P052.1.9, ревью возвращается в [ ] с Depends на него — запись checkpoint и событие state должны решаться в одном месте: writePauseCheckpoint возвращает признак записи, pauseState срабатывает только при нём; отвергнуты проверка ctx.Err() в каждом из трёх эмиттеров (она не отличает сигнал и отмену родителя от паузы) и повторное открытие P052.1.8 с уже закрытой первой находкой.
- Gated the state event on the write result at all five boundaries and removed the outer ctx.Err checks instead of adding pauseCancellation to each — a pre-filter duplicates the write decision and would leave the written flag untestable — rejected adding a second guard at the three emitters.
- Mutation-checked the new tests by forcing pauseState to fire regardless of the write — all four canceled-parent scenarios then failed, confirming they exercise the new gate — rejected relying on the green run alone.
- Находки P052.1.4 разведены в P052.1.10 (пауза в auto-replan, close и восстановлении pending commit: восстановить план, записать checkpoint и state, классифицировать событие) и P052.1.11 (stdout-ref активности несёт stream), ревью возвращено в [ ] с Depends на оба — правка ревью недопустима, а P052.1.8 и P052.1.9 уже закрыты.
- Пауза во время auto-replan восстанавливает план до записи checkpoint и не оставляет полувалидированную правку: следующий прогон читает исходный план, а checkpoint называет прерванную операцию; ResumeRun либо продолжает с восстановленного плана, либо явно сообщает нерезумируемую остановку — отвергнуты checkpoint поверх правки агента и ответ «no resumable checkpoint».
- События replan и close под управляемой паузой несут result: paused и exit_code 0, как run_end — журнал не показывает паузу как ошибку операции; отвергнут отдельный код паузы.
- Reclassified deferred replan/close results through one shared pauseOutcome helper — keeps future deferred operations consistent and avoids duplicating the pause predicate — rejected inline pauseCancellation in each defer.
- ResumeRun reports replan/close checkpoints as a recovery conflict instead of silently starting a fresh run from the restored plan — an explicit stop is auditable and avoids surprising a new run from a Resume action — rejected auto-starting startRun on the restored plan.
- Replan restores the plan only when the `before` snapshot exists; chain-wait pauses write the checkpoint without a restore — the plan is untouched before the first read — rejected a sentinel empty plan and rejecting the pause there.
- Left ReadActivityPage resolving any non-"stderr" stream to the call log instead of rejecting an empty stream — older persisted refs and journal-free consumers keep resolving — rejected strict stream validation that would break replay of existing state.
- Четвёртый блок P052.1.4 разведён на P052.1.12 (fingerprint закрытия и классификация паузы по записанному checkpoint), P052.1.13 (пауза в успешном окне replan/close до коммита) и P052.1.14 (единственный владелец checkout lock); ревью возвращено в `[ ]` с `Depends` на все три — правка ревью запрещена, а находки разные по проверяемому поведению.
- Fingerprint плана для close снимается до вызова агента: агент сам архивирует и удаляет план, и поздний `planFingerprint` падает; `PlanHash` не делаем необязательным, иначе checkpoint теряет привязку к плану — отвергнуто ослабление формата checkpoint ради записи после закрытия.
- Записанный checkpoint — единственный классификатор паузы: `unit_end`, `run_end`, отложенные `replan`/`close` и переход хоста в Paused смотрят на признак записи, а не на живое состояние Pausing; ошибка записи остаётся кодом 2/`error`/Failed — это распространяет правило P052.1.9 на хост и отложенные события — отвергнуто повторное вычисление `pauseCancellation` в каждом эмиттере.
- Пауза после успешного вызова replan/close останавливает операцию до коммита с checkpoint; после начала коммита операция завершается и журнал/хост дают done/Finished, а не Paused без checkpoint — отвергнуто завершение валидации и коммит под паузой.
- Checkout lock принадлежит `StartCheckout` на всё время `execute`; управляемый `runWithOptions` не берёт второй замок — отвергнуто повторное снятие внутреннего замка, которое открывает окно второму раннеру.
- Тест TestControlCommandsAreExclusive теперь пишет checkpoint в своём execute, чтобы по-прежнему проверять переход в Paused — иначе он проверял бы старое поведение «пауза по состоянию» — отвергнуто ослабление проверки до Finished.
- closeChecks использует runnerSelf вместо cycle.Self — иначе контролируемый тест не может довести close до коммита: cycle.Self запускает тестовый бинарник и lint падает вне fixture — отвергнут обход lint в тесте и отдельный seam для lint
- Пауза без checkpoint на границе цикла завершает прогон как done/Finished, а не Failed — требование Do для паузы, пришедшей в начавшийся коммит replan; закрыто общей проверкой в runLoop, а не отдельной веткой после replan — отвергнуто возвращение 0 только из ветки replan, оставляющее гонку до верхней проверки цикла
- Explicit runOptions.locked flag marks that the caller owns the checkout lock — the ownership is visible at the call site instead of inferred from a Control root — rejected inferring via host root
- A lock file naming any live pid now refuses a second owner, even the same pid — nested acquisition is gone and same-pid allowance would let two StartCheckout calls share a checkout — rejected keeping the pid==Getpid escape
- Release re-reads the lock and removes it only when it still names our pid — protects a lock another process took (e.g. a replacement after a stale file) from being deleted — rejected unconditional os.Remove
- Used the charm.land/bubbletea/v2 and charm.land/lipgloss/v2 module paths at the pinned versions — the old github.com module paths now declare the vanity path and fail go get — rejected forcing the old import path or falling back to Bubble Tea v1
- A malformed or missing queued plan puts the whole workspace in the error state with r retry — one actionable error instead of a partially rendered tree — rejected showing stale or partial plan content
- Kept Backlog out of the tree and exposed only its count implicitly through the docflow file load — the unit asks for Queue, plans, phases and units — rejected adding a third tree section before the Backlog UI is designed
- Raised the Go guard in tools/install-dev.sh, tools/release.sh and tools/parity/README.md to 1.26 in addition to the four files named in Do — go.mod now requires 1.26 and the old guards/messages would be wrong — rejected leaving the tooling text inconsistent
- History is keyed by runner.Event rather than separate message/tool queues — verify, route, quota and activities keep one order and one timestamp — rejected parallel histories per event class
- Render only a 200-entry window around the cursor/follow point and keep at most 8 paged log windows — a long run stays responsive and memory-bounded — rejected rendering the full 1000-entry bounded history every frame
- Verify overlay pages the same 4 KiB windows and reads the full verify log from its start, while git diff is capped at 512 KiB with a truncation note — rejected unbounded reads or a stdout-only verify view
- Manual movement pauses follow; G/f resume it — new events never move a reader who is inspecting older history — rejected unconditional autoscroll
- Polled Controller interface instead of one wait command per runner channel — avoids blocked goroutines and lets tests drive every transition synchronously; rejected channel-waiter commands in Bubble Tea.
- One start confirmation supplied by the runner with StartReview fields, not a separate TUI pre-flight plus runner confirmation — the person answers once and the displayed prerequisites come from the same helper the CLI uses; rejected duplicating selection/prerequisite logic in the UI.
- A human-block Continue is offered after the controlled run stops as Blocked, then runs existing unblock and starts a fresh run — preserves the runner's controlled-stop path; rejected adding a second Ask call inside runWithOptions.
- Stop now is a new Control.StopNow with no checkpoint, distinct from Pause — the journal and host do not misreport a non-resumable stop as Paused; rejected overloading Pause with a flag.
- The TUI invokes existing lifecycle commands through cycle.Self — lint, reference and archive checks stay identical to the CLI; rejected calling cycle functions and lint checks directly from the UI.
- A new Controller is created for Start/Resume after the old one finished and the old one is closed — resets context/channels cleanly; rejected reusing a finished Control indefinitely.
- Пятый блок P052.2.4 разведён на P052.2.5 (остановка прогона по сигналу терминала), P052.2.6 (страница результата текущего вызова), P052.2.7 (пути логов от корня checkout) и P052.2.8 (проверка lock в момент действия); ревью возвращено в `[ ]` с `Depends` на все четыре — правка ревью запрещена, а находки разные по проверяемому поведению.
- Остановка по сигналу живёт после `program.Run`, а не в отдельном обработчике клавиш: Bubble Tea превращает SIGTERM в QuitMsg и возвращается из Run мимо `requestQuit`, а SIGHUP не ловит вовсе — один путь остановки (Pause → ожидание Finished → Close) после Run покрывает оба сигнала и не заводит второй цикл событий.
- Обработчик SIGHUP ставится платформенным файлом под build tag: на Windows этого сигнала нет, а сборка под Windows остаётся целью — отвергнуто общее объявление SIGHUP без тегов и отвергнут собственный перехват SIGTERM вместо уже существующего в Bubble Tea.
- Страница запущенного инструмента читает лог от `ref.Offset` до текущего конца файла: `Ref.Length` — длина строки старта, и до прихода завершающей activity байты вызова недоступны — отвергнуто ожидание завершающего события (P052.2.2 требует читать результат текущего вызова) и отвергнуто расширение ref в `agentio`, ломающее формат ссылки.
- Пути логов из событий разрешаются в TUI от корня checkout в момент чтения: поток версии 1 хранит путь относительно checkout, а процесс может работать из подкаталога — отвергнуты абсолютные пути в событиях (формат версии 1) и смена cwd процесса (глобальное состояние на весь прогон).
- Чужой lock проверяется в момент действия, а не только при загрузке: lifecycle идёт через `cycle.Self`, который lock не проверяет, поэтому guard в UI — единственный — отвергнуты фоновый watcher (нет фонового сервиса) и опора на ошибку `runLock` у Start для lifecycle-действий.
- Stop a live run after program.Run rather than intercepting signals in handlers — SIGTERM already becomes QuitMsg inside Bubble Tea and SIGHUP is not its concern, so one path covers both and reuses the existing Pause → Finished → Close sequence — rejected a second event loop or custom SIGTERM handling.
- Added CancelStart to the TUI Controller instead of calling StopNow on a Starting run — StopNow requires Running and would miss the pending start confirmation — rejected overloading StopNow with a state check.
- If CancelStart loses the race to MarkRunning, fall back to Pause — otherwise the signal could leave a just-started run and its lock behind — rejected returning nil on that error.
- Added a separate agentio.ReadLiveActivityPage that reads the current file size, keeping ActivityRef.Length as the stream-line length for finished pages — widening Ref.Length would break the agentio ref test and the persisted link format — rejected inferring a live page from a modified ref
- The page is refreshed once on an event of the same attempt even when that event supersedes it, then syncLivePages freezes it — the reader sees bytes written up to the next record while later growth is never read — rejected refreshing only while the entry stays last, which would never fire for recorded events
- Live refresh reloads only the open, expanded first page of a running entry — one bounded 4 KiB read per event, no work for other pages — rejected refreshing every page of the unit on each event
- Resolve paths at read time with one helper used by both the activity page and the verify overlay — keeps the event format at version 1 and the runner unaware of the TUI cwd — rejected making event paths absolute or changing the process cwd
- Keep the original relative path in history and store the resolved path only on the rendered page — replay and journal consumers still see the persisted stream link — rejected rewriting history entries during load
- Treated a failing CheckoutLockOwner as an unknown lock that refuses control instead of a free checkout — an unreadable lock must not be silently read as "no runner" — rejected keeping the old err==nil-to-free branch
- Re-read the lock at every control action and after run completion rather than once at load — lifecycle goes through cycle.Self which takes no lock, so the UI guard is the only check and must see a runner that appeared while the workspace was open — rejected a background watcher (no background service) and a load-time-only check
- Stubbed lockCheck in the app-test loadedModel instead of giving every fixture a git repository — the fixtures are synthetic trees and the real checker would refuse all control actions — rejected adding git init to writeTestProject, which would change the diff/verify fixture behaviour
- Перезагрузка плана сохраняет notice исхода, а стирает только текст самой загрузки — «reloading» клавиши `r` при StateLoading: snapshot сейчас единственное место, которое затирает путь архива из close и причину паузы или ошибки прогона, и одно правило чинит все источники reload сразу — отвергнуты второе поле для исхода рядом с `notice` (два конкурирующих текста в статус-строке) и полный отказ от очистки (тогда «reloading» не исчезает).
- Human-block Continue проверяет lock в момент действия, как остальные lifecycle-клавиши, и не перекрывает отказ старта сообщением об unblock: диалог зовёт `cycle.Self` напрямую, поэтому guard из `lifecycleKey` его не покрывает — отвергнуты проверка внутри `cycle.Self` (он lock не берёт) и сохранение «starting the run again» после отказа.
- Test-side applyLoad fails loudly when a command carries no snapshotMsg — a lost loadCmd then breaks the test instead of passing without exercising the reload — rejected asserting only pollController/applyLifecycle cmd != nil with a separate manual snapshotMsg.
- The unblock lock guard lives in the dialogHuman Enter branch with the same refreshLock/lockRefusal pair as lifecycleKey — cycle.Self is a CLI dispatcher that takes no lock, so the UI is the only check — rejected checking the lock inside cycle.Self.
- applyLifecycle keeps the refusal notice instead of the unblock text when startRun created no controller — the reload only clears its own placeholder, so the refusal survives — rejected a second notice field.
- Шестой блок P052.2.4 разведён на P052.2.11 (лог текущего вызова из события attempt), P052.2.12 (остановка из стартового подтверждения через CancelStart), P052.2.13 (notice отдельной строкой над статусной), P052.2.14 (управляемый Stop now классифицируется как действие); ревью возвращено в `[ ]` с `Depends` на все четыре — правка ревью запрещена, а находки разные по проверяемому поведению.
- Лог вызова страница активности берёт из события `attempt`, а не из `stream`: `attempt` выходит до `CallAgent`, а `stream` — после возврата вызова со смещениями, известными только к этому моменту, поэтому ранний `stream` невозможен без искажения данных — отвергнуто перемещение `stream` перед вызовом и новое поле формата.
- Управляемый Stop now классифицируется по состоянию хоста и отменённому ctx, как пауза в P052.1.8: `unit_end.status: stopped`, `run_end.reason: stopped`, `exit_code: 0`, без поля `paused`; обычный сигнал и Ctrl-C идут прежним путём error/2 — отвергнуто сохранение «error/context canceled» в журнале при успешном Finished у хоста и отвергнут новый код.
- Notice исхода выводится отдельной строкой над статусной с переносом по ширине: `padLine` режет строку справа, поэтому notice в конце статусной строки не виден ни при каком приоритете внутри неё — отвергнуты notice в начале статусной строки (вытесняет run/lock/queue) и вывод notice в тело панели (дублирует дерево).
- ActivityHistory.Log returns an error naming the attempt instead of (string, bool) — the page shows the exact missing attempt without rebuilding the message — rejected keeping the boolean and composing the text at the caller
- Always call CancelStart instead of branching on pendingPrompt — s and q can arrive in one write before the runner publishes its confirmation, and answering a late prompt is not enough to stop the run — rejected the old pendingPrompt == nil guard that silently skipped the stop.
- Fall back to Pause only when CancelStart errors and the controller now reads Running — that is exactly the lost race to MarkRunning, while any other state means the run already stopped or is stopping — rejected unconditional Pause on a CancelStart error.
- The PTY regression test sends "sq\r" in one write instead of waiting for the start dialog — waiting would let the prompt arrive and exercise the already-covered quit path rather than the pending-start cancel — rejected a waitFor("Start the current Queue run?") before quitting.
- Gave the notice its own wrapped footer lines instead of raising its priority inside the status line — padLine truncates the joined status line from the right, so no ordering inside it can save a long archive path — rejected keeping the notice in statusLine()
- Cap the notice at two lines and mark the second with '…' — keeps the body panel at least one line on any supported height while signalling that text is missing — rejected rendering the full notice and shrinking the body to zero
- Classify a controlled Stop now with a dedicated stopCancellation predicate mirroring pauseCancellation instead of reusing internal codes 7/2 or the pause flag — the journal must distinguish a user action from 'no executor' and from a runner failure — rejected overloading pauseCancellation or adding new codes
- Normalize code and runErr inside the run_end defer rather than adding branches in runLoop/runUnit — one place covers the agent call, the early loop cancellation check and deferred operations, and keeps the host from receiving context.Canceled — rejected returning 0,nil from every stop point
- Седьмой блок P052.2.4 разведён на P052.2.15 (потерянный результат клампа прокрутки Details) и P052.2.16 (высота панели от отрисованного тела с notice-строками); ревью возвращено в `[ ]` с `Depends` на оба — правка ревью запрещена, а находки разные по проверяемому поведению: пустая панель после прокрутки и неверная видимая высота.
- Видимая высота считается от фактически отрисованного тела, а не от константы `height-5`: заголовок панели и notice-строки уже заняли строки, и повторённый в `treeLines`/`syncActivityScroll` расчёт отрезал последнюю выбранную строку — отвергнуто увеличение `bodyHeight()` на единицу (не покрывает notice) и отказ от двухстрочного notice (теряет текст исхода).
- Moved notice trimming into fitNotice and body-height arithmetic into bodyHeightFor shared by render and bodyHeight() — one source of geometry that cannot drift from the rendered screen — rejected recomputing the trim loop inside bodyHeight only
- paneContentHeight() clamps at 0, not 1 — a body of one line is only the title, so no phantom content line is reported to scroll math; supported heights always yield at least two content lines — rejected max(1, ...) which could show a clipped selection position
- The tall-tree test selects the last unit by key instead of End — the tree ends with the empty Frozen section row, so End would assert on a section rather than a unit — rejected removing Frozen from the fixture or accepting a section assertion
- Восьмой блок P052.2.4 разведён на P052.2.17 (исход блокировки по перечитанному плану), P052.2.18 (причина отказа до первого события), P052.2.19 (отменённый старт — не завершённый прогон), P052.2.20 (поздний prompt не забирает клавиши открытого диалога), P052.2.21 (shift-символы в поиске и заметке unblock), P052.2.22 (follow и курсор истории при смене юнита); ревью возвращено в `[ ]` с `Depends` на все шесть — правка ревью запрещена, а находки разные по проверяемому поведению.
- Исход блокировки решается по снимку, прочитанному после остановки прогона: `docflow block` пишет `[!]` во время прогона, поэтому снимок, актуальный до `unit_end`, не содержит причины; отложенное решение через уже запрошенную перезагрузку P052.2.9 — отвергнуто синхронное чтение плана в `handleFinished` (второй загрузчик в UI-потоке).
- Причина отказа до `run_start` доходит до хоста, не меняя журнал: предусловия выполняются раньше `totals.events`, поэтому `run_end` для них невозможен, а перенос `run_start` вперёд ломает порядок журнала и parity — отвергнуты событие в журнале до `run_start` и включение Progress в интерактивном режиме (хост не владеет stdout).
- Отменённый старт отличается от завершённого прогона состоянием хоста, а не кодом выхода: checkpoint не пишется, Paused не показывается, notice отказа сохраняется — отвергнуты `StateFinished` с текстом «run finished» и подделка отмены под Pause.
- Поздний prompt запоминается и показывается после закрытия текущего диалога, а выход отвечает на него отказом и зовёт `CancelStart` — отвергнуты замена открытого диалога стартовым (Enter в диалоге выхода подтверждал старт) и молчаливое игнорирование prompt (горутина `Ask` висит до отмены ctx).
- Текстовый ввод принимает `msg.Text` при shift и caps lock, а ctrl/alt/meta остаются командами: Shift+A приходит как `Text: "A"` с модификатором, а `keyMsg` с `Mod: 0` не воспроизводил верхний регистр — отвергнуты проверка `msg.Mod == 0` и разбор `Keystroke()`.
- Смена выбранного юнита включает follow и ставит курсор на новейшую запись: пауза чтения принадлежит истории одного юнита, а общий флаг делал новый юнит «замороженным» на первой записи — отвергнуто хранение позиции по юнитам (лишняя карта без требования).
- Kept blockedPending set when the post-block reload fails and worded the notice 'press r to try again' — a retry then still resolves the outcome from a valid snapshot — rejected dropping the decision and leaving only the old 'stopped on a block' text
- Treat a unit with status [!] but no Blocked Handoff item as the previous 'the run stopped on a block' outcome instead of formatting an empty needs/reason — matches how blockNeeds reports a hand-blocked unit
- Carry the pre-start reason as a host-only start_failure event plus the execute error — runWithOptions builds the journal only after prerequisites, so a journal event there would reorder run_start — rejected a journal event before run_start and enabling Progress output for the interactive host.
- Added an explicit MarkStartCancelled called by startRun on a declined confirmation instead of inferring cancellation from 'execute returned while still Starting' — the decline must be a recorded host decision that CancelStart and the dialog share, not a guess from the leftover state — rejected inferring from StateStarting, which leaves the classification implicit and untestable.
- Classified the startCancelled flag ahead of the Stopping branch in the Start goroutine — a cancel of a pending start also moves the host to Stopping, and the flag must win over the generic ControlledStop-to-Finished mapping — rejected a separate internal exit code or reusing StatePaused.
- Remember a late prompt and open its dialog via one showPendingPrompt helper instead of per-call branches — the invariant 'no dialog is replaced under the person's keys' is enforced in one place — rejected replacing the dialog in handlePrompt and re-opening it only in the exit-Esc branch
- Added one shared typedText helper masking command modifiers instead of checking Mod == 0 per handler — shift and lock states must not suppress printable text, and search and the unblock note must behave identically — rejected duplicating the condition in handleSearchKey and handleDialogKey
- Kept key matching on msg.String() and added the text predicate separately — String() returns the shifted character, so Keystroke() would misread Shift+A — rejected switching dispatch to Keystroke()

## Phase 1: Controllable runner and activity stream

### [x] P052.1.1 Expose an interactive runner host
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
Handoff:
- Интерактивный клиент должен отвечать на Requests и читать Events; StartRun ждёт явного подтверждения старта.
- Пауза во время verify/gate и устойчивой границы done/commit не покрыта отдельным тестовым сценарием.
- Unverified: Не проверен отдельным тестом интерактивный Pause во время verify/gate и завершения транзакции done/commit.

### [x] P052.1.2 Pause active work and write checkpoint
Read:
- internal/runner/control.go
- internal/runner/run.go:L299-435
- internal/runner/helpers.go:L246-337
- internal/runner/checkpoint.go
- internal/agentio/process.go
- internal/procgroup/procgroup.go
- internal/runner/verify_guard_acceptance_test.go:L330-420
- internal/roster/state_test.go:L100-145
Do:
- Реализуй Pause через общий контроллер: Pause отменяет текущий вызов агента, останавливает всю группу инструментов, ожидает завершения группы процессов, затем подтверждает Paused; ожидание квоты прерывается сразу, verify/gate и done/commit доходят до устойчивой границы.
- После Paused новые агенты не запускаются; классифицируй эту отмену до обработки отсутствующего result JSON: не считай Pause отказом, не запускай fallback и не сбрасывай лимиты попыток.
- При переходе в Paused сохраняй checkpoint через WriteCheckpoint: plan/unit, HEAD, fingerprint плана, этап, agent/model/effort, session id, счётчики и ссылки на логи; секреты не записывай.
- Почини календарную фикстуру internal/roster TestStateSaveLoadRoundTrip: даты Off/Until/Expires должны быть относительны time.Now, а не фиксированы на 2026-11-01, иначе после наступления этой даты LoadState вычищает значения и gate падает независимо от кода юнита.
- Создай internal/runner/pause_test.go: пауза на tool-call, quota wait, verify и commit boundary; полная остановка группы процессов без сирот; checkpoint отражает состояние прогона; отмена вызова провайдера паузой доходит до обработки отсутствующего result JSON как не-отказ — retry/fallback счётчики неизменны, fallback не запускается, лимиты попыток не сброшены.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/agentio/ ./internal/procgroup/ ./internal/roster/`
Traps: internal/runner/checkpoint.go и checkpoint_test.go уже лежат в дереве без commit — используй и дополняй их, не переписывай с нуля; ReadCheckpoint отвергает чужую версию и пустые plan/HEAD/stage.
Commit: P052.1.2: pause active work and checkpoint
Handoff:
- Blocked (plan): The unit requires safe Pause/Resume integrated with the runner, including quota waits, verify/commit boundaries, session recovery, and timeout accounting. Only the checkpoint storage primitive and its tests were implemented; these runner behaviors remain undone. `docflow verify P052.1.2` passed both the unit Verify and `go test ./...` gate. No commit was made because the unit is unfinished. An existing modification to the plan file was left untouched.
- Replan (2026-11-01): юнит разбит — Resume, восстановление сессии и Stop after unit переехали в P052.1.5; здесь остаются Pause, устойчивые границы и checkpoint.
- Blocked (plan): Ожидаемый полный verify с успешным gate не достигнут: воспроизводимо падает существующий тест roster, не относящийся к Pause. Кроме того, quota тест проверяет прерывание ожидания и checkpoint, но не полный путь отказа провайдера с проверкой неизменности retry/fallback счётчиков.
- Blocked (plan): Plan ожидает, что P052.1.2 закрывается при зелёном gate `go test -timeout 20m ./...`. Реальность: Do выполнено и Verify юнита (`go test -timeout 20m ./internal/runner/ ./internal/agentio/ ./internal/procgroup/`) зелёный, но gate падает на internal/roster TestStateSaveLoadRoundTrip (state_test.go:134): фикстура берёт day = 2026-11-01T00:00:00Z как будущий день, а LoadState вычищает просроченные Off/Until — после наступления этого мига (сейчас 2026-11-01 06:52Z) тест падает детерминированно 3/3 при -count=3 и на чистом worktree HEAD 7d97ade без локальных изменений. Второй replan P052.1.2 списал это падение на нагрузку сессии — гипотеза не выдержала: корень-caused датой, воспроизводится. Runner не сможет пройти done/commit, пока gate красен; починить ростер-тест — шаг плана (сделать даты фикстур относительными к time.Now), а не объём P052.1.2, и правку плана я не делал.
- Replan (2026-11-01, 3-й): починка календарной фикстуры roster добавлена в Do этого юнита как мелкая правка (отдельный юнит меньше получаса работы), Read и Verify расширены на internal/roster; некоммиченная работа Pause сохранена, статус возвращён в [~]. Маршруты плана пересоставлены без strata: opencode-go → zai через Pi → Codex.
- Pause is classified via pauseCancellation(ctx) (host Pausing + ctx canceled), checked at every agent/refusal/quota/commit boundary before missing-result handling; the checkpoint writer no-ops unless the host is Pausing.
- The paused call is recorded through agentio.CallSession so the session id survives even when the call wrote no result line; P052.1.5 must resume that session/route/effort.
- callLog carries the current call log into the checkpoint; only that one log is linked, not the whole run.
- The roster test fixture now builds its date from time.Now().UTC() truncated to a day; other tests using a fixed 2026-11-01 day do not pass through LoadState.
- Unverified: Pause during the provider refusal/reset wait (waitRefusal) has a classification branch but no dedicated test; only the executor-chain quota wait is exercised.
- Unverified: Windows process-group termination on Pause is untested: the runner pause tests use POSIX shell fixtures and skip on Windows.

### [x] P052.1.5 Resume from checkpoint safely
Read:
- internal/runner/control.go
- internal/runner/checkpoint.go
- internal/runner/run.go:L299-435
- internal/runner/helpers.go:L246-337
- internal/agents/agents.go:L922-980
- internal/runner/pause_test.go
Do:
- Реализуй Resume через общий контроллер: загрузи checkpoint, продолжи тот же unit на том же agent/model/effort; при расхождении fingerprint плана или HEAD не продолжай молча — покажи конфликт восстановления.
- Продолжай ту же сессию при совместимом назначении; если session id отсутствует или resume отвергнут, один раз начни новую сессию с полным промптом, текущим деревом и причиной восстановления.
- Не считай Pause отказом и не запускай fallback; не сбрасывай лимиты попыток и остаток активного timeout; исключи время паузы из таймаута.
- После возобновления не возвращай готовую проверку после изменения дерева и не повторяй завершённый commit; реализуй Stop after unit через контроллер — текущий юнит доходит до устойчивой границы, новые не запускаются.
- Расширь internal/runner/pause_test.go: resume без сессии и отвергнутый resume ровно с одной новой сессией, повторные команды Pause/Resume, исключение времени паузы из таймаута, отсутствие повторного commit.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/agentio/ ./internal/procgroup/`
Traps: формат session id у агентов разный (`json:session_id`, `thread_id`, `session`) — бери его из ответа агента, а не вычисляй сам; resume идёт только на той же тройке agent/model/effort.
Depends: P052.1.2
Commit: P052.1.5: resume from checkpoint safely
Handoff:
- P052.2.3 should call Control.ResumeRun(parent, root, args, skillDir); it validates the checkpoint, accepts both Idle (reopened app) and Paused states, publishes a recovery_conflict event and does not ask confirm_start.
- Checkpoint gained timeout_remaining_seconds; the resumed first call spends that remainder, later attempts get the full timeout again.
- Conflicts are a typed runner.RecoveryConflictError for plan-fingerprint or HEAD drift; a fresh checkpoint path read failure is reported separately as no resumable checkpoint.
- Stop after unit already travels through Control.StopAfterUnit and the stop file; a resumed run removes that file on start, so stopping is not carried into the next run.
- Unverified: The resume-rejection matcher is exercised only with a fake `session ... not found` stderr; the actual wording of a refused session from Pi, Codex and opencode is not covered by an integration test.

### [x] P052.1.3 Preserve inspectable agent activity
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
Handoff:
- Activity API for P052.2.2: agentio.NewActivityParser(agent), Feed(stream, text, ref), Finalize(state), ReadActivityPage(log, ref, skip, limit), StderrLog(log); refs carry stream+absolute offset+length and the original call log stays unchanged.
- Interactive runner publishes Control.Event{Type:"activity", Data: agentio.Activity} only when a Control host is present; a normal run writes no per-activity event, history is rebuilt from logs with the parser.
- New JSONL v1 events to consume: config (plan/permissions/executor/fallback/replan/roles, no secrets or bodies), state (Paused with stage+attempt), stream (log plus stdout/stderr path/offset/length; replan/close flags). Existing event names and fields are unchanged.
- Verify output is now stored at .git/docflow/run/verify.<uid>.<n>.out and linked from the existing verify event (log, stage, stdout/stderr ranges); the verify.<uid> prefix deliberately avoids the uid.*.out call-log namespace.
- Activity.Seq is renumbered run-wide by the runner; the parser's own sequence is per call. Delta messages are emitted as running activities and the final text is trimmed to its unseen remainder.
- Unverified: Activity normalization is verified against the repository fixtures and fake agents, not against live Pi/Codex/opencode output; streaming event shapes such as Pi message_update/tool_execution_update and Codex mcp_tool_call payloads are implemented from documented shapes without a live check (live acceptance is P052.4.2).
- Unverified: ReadActivityPage is tested on a completed log; reading a page while the log is still being appended by a running agent (partial JSON line at the tail) is not covered end-to-end.

### [x] P052.1.6 Fix pause boundaries in the runner
Read:
- internal/runner/run.go:L760-830
- internal/runner/run.go:L960-1060
- internal/runner/pause_test.go
- internal/runner/control.go
Do:
- В ветке ошибки вызова агента (err от `agentio.CallAgent`) при `pauseCancellation(ctx)` после `writePauseCheckpoint` и события `state: Paused` немедленно верни `7, "", context.Canceled`: ветка `resumeRejected` ниже не должна начинать новую сессию с уже отменённым ctx.
- Проверь тот же порядок в success-пути после ответа агента и в refusal-пути: классификация Pause стоит до `resumeRejected`, и ни один путь не запускает второй процесс агента после Paused.
- В ожидании executor-chain (`waitForExecutorChain`) после `writePauseCheckpoint` публикуй событие `state` с `state: Paused`, `stage: quota`; в ветке восстановления pending commit после checkpoint `stage: complete` — то же событие. Форму бери у помощника `paused` в `runUnit`, не дублируй поля.
- Расширь `internal/runner/pause_test.go`: (а) отвергнутый resume с паузой до обработки ошибки — в логе ровно один старт, с `--resume`, и ни одного старта без него; (б) пауза в ожидании executor-chain — checkpoint и событие `stage: quota`; (в) пауза после восстановленного pending commit — checkpoint и событие `stage: complete`.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: сценарий (а) воспроизводится overlay-тестом из ревью: фиктивный CLI отвечает `session S not found`. Не ослабляй существующие тесты resume ради зелёного прогона.
Commit: P052.1.6: fix pause boundaries in runner
Handoff:
- `pauseState(totals, uid, stage, attempt)` in internal/runner/run.go is now the only emitter of a `state: Paused` event: every new pause boundary must call it right after `writePauseCheckpoint`.
- The `agentio.CallAgent` error branch returns `(7, context.Canceled)` immediately after the pause checkpoint, so the `resumeRejected` retry below it now only runs for real provider refusals.
- The success path classifies Pause before `resumeRejected`: a pause that lands while a call returns normally stops the unit at stage `agent` (resume continues that session) instead of running verify/commit.
- New test helpers in pause_test.go: `awaitEvent(control, type)` and `pausedStateEvents(f, stage)`; the latter reads every `*.events.jsonl` because a resume creates a second run file while `fixtureEvents` requires exactly one.
- Executor mode refuses `--config`, so the executor-chain wait test runs on the built-in claude profile with a PATH shim answering `claude -p /usage`; it pauses after the `quota` event, since the loop's ctx check precedes the wait and an earlier Pause exits before any checkpoint.
- Unverified: A Pause that lands while a call returns successfully has no deterministic test: reaching that state needs the pause between the reply and `CallAgent` returning. The new success-path check is exercised only by the rejected-resume scenario.

### [x] P052.1.7 Clip large tool input in activity
Read:
- internal/agentio/activity.go:L235-290
- internal/agentio/activity_test.go
- internal/agentio/output.go:L205-374
Do:
- Ограничь `Activity.Input` лимитом `ActivityInlineLimit` по сериализованному JSON: сначала обрежь строковые значения, а если payload всё ещё больше лимита — замени Input маркером усечения с исходным размером; полный текст остаётся доступен через `Ref`.
- Применяй ограничение и в `toolStart`, и в `toolEnd`; `Command` и `Result` не меняй. Не теряй сохранённый input между началом и завершением инструмента.
- Добавь в `internal/agentio/activity_test.go`: аргумент 1 МБ — Input в пределах лимита, а `Ref` указывает на полный фрагмент; маленький Input не меняется; событие завершения инструмента повторяет усечённый Input.
Verify: `go test -timeout 20m ./internal/agentio/`
Traps: `toolEnd` копирует input из `toolStart` — усекай один раз, иначе повторное усечение съест маркер.
Commit: P052.1.7: clip large tool input
Handoff:
- clipInput is idempotent: an already clipped map (or the size marker) is within ActivityInlineLimit, so toolEnd re-applying it to the copy from toolStart never clips twice.
- Finalize emits tool.input taken from toolStart, so it is already clipped; a new emission path must call clipInput itself.
- The string-clip budget is (ActivityInlineLimit-256)/countStrings; payloads with many keys/values that still exceed the limit become the {"truncated": true, "bytes": N} marker.
- Unverified: Rendering of the new size marker and of clipped argument strings in the TUI activity detail is not covered: no UI test or live run in this unit.

### [x] P052.1.8 Classify paused runs in the event stream
Read:
- internal/runner/run.go:L293-306
- internal/runner/run.go:L734-750
- internal/runner/run.go:L1400-1420
- internal/runner/events.go:L132-157
- internal/runner/control.go:L114-175
- docs/design/runner.md#поток-событий
- internal/runner/pause_test.go:L130-200
- internal/runner/pause_test.go:L202-350
- internal/runner/events_test.go:L56-102
Do:
- Добавь в internal/runner/run.go единую классификацию управляемой паузы по `pauseCancellation(ctx)`: юнит, который остановила пауза, получает `unit_end.status: paused`, а `run_end` — `paused: true`, `reason: paused`, `exit_code: 0`. Отмена сигналом, Stop now, отмена родительского контекста и обычный `run` без хозяина сохраняют прежние статусы, причины и коды: `context canceled` и 2 в потоке означают ошибку раннера, а не паузу.
- Примени классификацию в обеих отложенных записях — `unit_end` (run.go:747) и `run_end` (run.go:302); путь паузы на вызове агента, в ожидании квоты и после восстановленного pending commit даёт `paused` на обоих уровнях.
- Доведённый паузой до complete boundary юнит остаётся `done` в `unit_end` — он выполнен; пауза видна в событии `state` и в `run_end`.
- Вызови `pauseState(totals, uid, "complete", attempt)` сразу после `writePauseCheckpoint(..., "complete", ...)` в успешном пути run.go:1413; grep'ом проверь, что ни один `writePauseCheckpoint` не остался без `pauseState`.
- Обнови docs/design/runner.md: в потоке событий опиши `unit_end.status: paused`, поля `paused` и `reason: paused` у `run_end` и правило, что пауза на complete boundary закрывает юнит как `done`; `v: 1` и остальные значения набора не меняй.
- Расширь internal/runner/pause_test.go: (а) TestPauseStopsAgentGroupAndWritesCheckpoint и TestPauseCancelsQuotaWait — `unit_end.status == "paused"` и `run_end` с `paused: true`, `reason: "paused"`, `exit_code: 0`; (б) TestPauseDuringVerifyFinishesCommitBoundary, TestPauseAtCommitBoundaryCompletesUnit и TestPauseAfterRecoveredPendingCommit — событие `state: Paused` со `stage: "complete"`, `unit_end.status == "done"` и тот же `paused` `run_end`; (в) в events_test.go — обычный прогон и прогон с сигналом не несут `paused` и сохраняют прежний `exit_code`.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: классифицируй только через `pauseCancellation` — `errors.Is(err, context.Canceled)` без состояния хозяина пометит паузой Ctrl-C, Stop now и отмену родителя; `fixtureEvents` требует ровно один файл прогона, а resume создаёт второй — читай события через `readEvents` по glob, как `pausedStateEvents`; `pauseState` остаётся единственным источником события `state` (Handoff P052.1.6) — не добавляй второй эмиттер и не дублируй событие там, где оно уже есть.
Depends: P052.1.1, P052.1.6
Commit: P052.1.8: classify paused runs in events
Handoff:
- assertPausedJournals требует ровно один unit_end и один run_end на все файлы прогона: для сценариев с Resume (два events.jsonl) нужен отдельный вариант, читающий последний файл.
- Тест паузы в ожидании квоты с журнальными проверками — TestPauseDuringExecutorChainWait; TestPauseCancelsQuotaWait остаётся без потока событий (ручной execute).
- Классификация живёт в отложенных эмиттерах: pauseCancellation(ctx) читает host.State() == StatePausing, что верно во время выполнения Run/runUnit, потому что Control переводит состояние после возврата execute.
- Unverified: Пауза в окне между юнитами (в runLoop после unit_end, до unit_start следующего юнита): run_end получит paused:true, а unit_end паузы не будет — отдельного теста на это окно нет.
- Unverified: Отмена родительского контекста при host.State()==StatePausing (совпадение паузы и отмены извне) классифицируется как пауза — сценарий не воспроизведён тестом.

### [x] P052.1.9 Guard pause state by a written checkpoint
Read:
- internal/runner/run.go:L760-845
- internal/runner/run.go:L1395-1430
- internal/runner/run.go:L1448-1480
- internal/runner/checkpoint.go:L115-135
- internal/runner/pause_test.go:L680-760
- internal/runner/pause_test.go:L799-876
- internal/runner/events_test.go:L17-56
Do:
- Свяжи запись checkpoint и событие `state` в одно решение: `writePauseCheckpoint` возвращает признак записи, `pauseState` вызывается только при нём. Сейчас без хозяина или вне `StatePausing` запись молча no-op, а событие всё равно уходит. Не заменяй это проверкой `errors.Is(ctx.Err(), context.Canceled)`: она верна и для сигнала, и для отмены родителя.
- Приведи к правилу все пять вызовов: ожидание executor-chain (`quota`, run.go:771-774), восстановленный pending commit (`complete`, 835-838), complete boundary (1420-1423), а также уже гварженные ветки `paused` (931-934) и отмены вызова (1031-1034) — переиспользуй их условие, не копируй тело ветки.
- Сохрани прежние исходы: ошибка записи checkpoint остаётся возвратом `2`, ветка `paused` — `7, context.Canceled`, обычная отмена — как сейчас; `pauseState` остаётся единственным эмиттером события `state`.
- Расширь internal/runner/pause_test.go: (а) управляемый прогон с отменяемым родителем при поднятом commit gate — отменить родителя, отпустить gate: ни одного `state: Paused`, `run_end` без `paused`, `unit_end` не `paused`; (б) то же для ожидания executor-chain и для восстановленного pending commit; (в) обычный прогон без хозяина с отменяемым `runOptions.ctx` в ожидании executor-chain — ни одного `state: Paused` и прежний exit code; (г) `TestPauseDuringExecutorChainWait` и `TestPauseAfterRecoveredPendingCommit` остаются зелёными и дают ровно одно событие `state` со своим stage.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: overlay-тест ревью в репозиторий не попал — сценарий отмены родителя воспроизводится обычной фикстурой; сигнал и отмена `runOptions.ctx` идут одной веткой через `ctx.Err()`, отдельный subprocess с Ctrl-C не нужен; отсутствие события проверяй через `eventFiles`/`readEvents` (у `pausedStateEvents` пустой срез и есть отсутствие); второй файл прогона появляется только с Resume, поэтому в этих сценариях `fixtureEvents` уместен; docs/design/runner.md менять не нужно — контракт «сигнал, Stop now и отмена родителя — не пауза» уже записан, юнит приводит код к нему.
Depends: P052.1.8
Commit: P052.1.9: guard pause state by checkpoint
Handoff:
- writePauseCheckpoint now returns (bool, error); pauseCheckpoint in internal/runner/run.go is the only place that pairs a written checkpoint with the state event — new pause call sites must use it, not call writePauseCheckpoint directly.
- The quota, recovered-pending-commit and complete boundaries no longer guard on ctx.Err(): the write decision is authoritative, so a canceled parent or signal is silent there.
- pauseCancellation remains only in the paused closure and the canceled-agent-call branch, where it also separates a Pause from other cancellation handling (cost recording, rejected-resume guard).
- Reusable test helpers added in pause_test.go: startControlledRunWith, awaitDone, pendingCommitFixture, assertNoPause, awaitRecord.
- Unverified: The pause and parent-cancel fixtures use POSIX shell fakes and are skipped on Windows, so the new scenarios do not execute on that target; the changed code path is platform-neutral but untested there.

### [x] P052.1.10 Recover a Pause during replan and close
Read:
- internal/runner/replan.go:L254-420
- internal/runner/replan.go:L500-610
- internal/runner/close.go:L320-470
- internal/runner/run.go:L795-845
- internal/runner/run.go:L905-935
- internal/runner/run.go:L1437-1462
- internal/runner/checkpoint.go:L115-140
- internal/runner/control.go:L190-230
- internal/runner/pause_test.go:L740-940
- internal/runner/replan_test.go:L259-420
- docs/design/runner.md#поток-событий
Do:
- Классифицируй управляемую паузу вне unit loop через `pauseCancellation(ctx)`: отменённый вызов replan-агента (replan.go:459), ожидание executor-chain, квоты и refusal (replan.go:332, 535, 581); отменённый вызов close-агента, ожидание цепочки и refusal (close.go:333, 389, 446); failure-ветка восстановления pending commit (run.go:815-820). Сигнал и отмена родителя паузой не становятся.
- Для replan восстанови план из снимка `before` существующим путём stop/restorePlan до записи checkpoint: полувалидированная правка не переживает паузу, следующий прогон читает исходный план. Close план ещё не менял — восстанавливать нечего.
- Запиши checkpoint через `pauseCheckpoint` с этапом, называющим прерванную операцию (`replan` или `close`), сохранёнными route/session/attempt/логами; опубликуй `state: Paused`. `ResumeRun` на такой checkpoint либо продолжает с восстановленного плана, либо явно сообщает нерезумируемую остановку, а не «no resumable checkpoint». Второй вызов агента после Paused не запускай.
- Классифицируй отложенные события `replan` и `close` так же, как `run_end`: под управляемой паузой `result: paused`, `exit_code: 0`, а не `error`/7.
- Расширь `internal/runner/pause_test.go`: (а) auto-replan по фикстуре ревью — юнит отвечает `blocker: plan`, replan-агент правит Do и спит, Pause: план восстановлен и не грязный, checkpoint и `state: Paused` записаны, `replan` не error, второго старта нет; (б) Pause во время close-агента — checkpoint, state и paused `run_end`, close-коммита нет; (в) Pause в failure-ветке восстановления pending commit — checkpoint этапа `complete`, state и paused `run_end`, провал проверки не выдаётся за паузу.
- Обнови `docs/design/runner.md`: события `replan` и `close` под управляемой паузой несут `result: paused` и `exit_code: 0`; checkpoint называет прерванную операцию.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: replan уже пишет ответ агента в план до проверки `check_replan` — восстановление идёт из снимка `before`, а не из `after`; не ослабляй существующие тесты отказа replan и close ради зелёного прогона. Overlay-тест ревью в репозиторий не попал — сценарий воспроизводится обычной фикстурой.
Depends: P052.1.8, P052.1.9
Commit: P052.1.10: recover pause during replan and close
Handoff:
- New shared helpers: pauseOutcome(ctx,result,exit) in events.go for deferred replan/close events, and ValidateResumable(cp) in resume.go for stages with no resumable session. New deferred operations must go through pauseOutcome; a new stage without a session belongs in ValidateResumable.
- A replan pause restores the `before` snapshot taken at replan start, so the block's [!] edit made by runUnit before replan stays in the tree; the next run reads the pre-replan plan, not the pre-block one.
- A close pause checkpoint has an empty Unit (plan-level operation); consumers must key it by Plan.
- The pending-commit recovery failure branch checks pauseCancellation before result.Code, so a Pause wins over any non-zero verify result; its checkpoint stage is `complete`, not `verify`.
- ResumeRun rejects replan/close checkpoints with RecoveryConflictError and publishes a recovery_conflict event; it does not start a run and does not delete the checkpoint.
- Unverified: Pause during a replan or close executor-chain wait and refusal wait has no automated test: Do's three scenarios cover only the replan agent call, the closing agent call and the pending-commit failure branch.
- Unverified: A genuine non-zero verify in pending-commit recovery that races a pending Pause is classified as a pause and not as the failure; no test distinguishes the two.
- Unverified: How the TUI renders checkpoint stages `replan`/`close` and the recovery_conflict message is not exercised by this unit's Verify; the TUI surface is built by later P052 units.

### [x] P052.1.11 Name stdout in activity refs
Read:
- internal/agentio/process.go:L168-260
- internal/agentio/activity.go:L40-60
- internal/agentio/activity.go:L740-770
- internal/agentio/activity_test.go:L272-330
Do:
- Задай stdout-писателю `eventWriter` имя потока `stdout` в `CallAgent`: сейчас `out := &eventWriter{}` оставляет `Stream` пустым, и каждый stdout-ref приходит с `Stream: ""`, хотя stderr помечен.
- Не меняй формат call-лога, абсолютные offset'ы и sidecar stderr; `ReadActivityPage` по-прежнему выбирает файл по `ref.Stream`.
- В `TestCallAgentActivitySubscription` проверь `Ref.Stream` каждой активности: stdout — `stdout`, stderr — `stderr`, и что `ReadActivityPage` читает тот же фрагмент. Существующие проверки количества и вида активностей не ослабляй.
Verify: `go test -timeout 20m ./internal/agentio/ ./internal/runner/`
Traps: `ActivityParser.Feed` сам подставляет `stdout` при пустом stream, поэтому тесты парсера дефект не видят — проверять надо ref, который отдаёт `CallAgent`. Поле `Stream` в `CallOptions` — имя декодера профиля, его не переиспользуй как имя потока.
Depends: P052.1.3
Commit: P052.1.11: name stdout in activity refs
Handoff:
- Interactive activity events now carry Ref.Stream = "stdout" for stdout and "stderr" for stderr, so a consumer can page both streams by ref.Stream alone; before this a stdout ref had Stream "" and only worked because ReadActivityPage falls back to the call log.
- Quota probing (internal/agentio/quota.go) also constructs an eventWriter with no stream, but it never sets activity/onActivity, so it emits no ActivityRef and needs no name.

### [x] P052.1.12 Classify a pause by its written checkpoint
Read:
- internal/runner/checkpoint.go:L115-175
- internal/runner/close.go:L240-430
- internal/runner/events.go:L130-170
- internal/runner/run.go:L293-310
- internal/runner/run.go:L1437-1490
- internal/runner/pause_test.go:L1109-1150
- internal/runner/close_test.go:L40-53
Do:
- Сними fingerprint плана до вызова close-агента: агент сам выполняет `<cli> close P001`, архивирует и удаляет план, поэтому `planFingerprint` после вызова падает и checkpoint не пишется. Передай снятое значение в писатель pause-checkpoint (пустое — вычислить на месте, как сейчас), не делай `PlanHash` необязательным для close.
- Сделай записанный checkpoint единственным признаком паузы: `pauseCheckpoint` отмечает запись в totals, а `unit_end`, `run_end`, отложенные `replan`/`close` и переход хоста Pausing → Paused используют только этот признак. Ошибка записи остаётся ошибкой раннера: код 2, статус `error`, хост Failed, без события `state`; не переклассифицируй её в `paused`/0.
- Проверь, что обычная пауза и пауза на complete boundary сохраняют `paused`/0, а отмена сигналом и родителя не получает признака.
- Обнови docs/design/runner.md#поток-событий: checkpoint для close снимает fingerprint до вызова агента; пауза в журнале и у хоста видна только при записанном checkpoint, ошибка записи — ошибка раннера.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: `readyCloseFixture` сейчас не архивирует план до сна — новый сценарий должен выполнить `closeArchiveStep(f.binary)` до `sleep`, иначе дефект не воспроизводится. Для сценария отказа записи положи обычный файл на место каталога `.git/docflow/tui`; не ослабляй существующие проверки паузы ради зелёного.
Depends: P052.1.10
Commit: P052.1.12: classify pause by written checkpoint
Handoff:
- writePauseCheckpoint/pauseCheckpoint получили параметр fingerprint после stage: пустое значение — вычислить на месте, непустое — использовать как есть. Новые вызовы вне close передают "".
- runClose снимает fingerprint в начале функции, до вызова закрывающего агента (агент сам архивирует и удаляет план); ошибка снятия игнорируется, писатель повторит попытку на границе.
- runTotals.paused (markPaused/isPaused) — единственный признак паузы: на него смотрят unit_end (pausedUnitStatus), run_end, pauseOutcome для отложенных replan/close. pauseOutcome теперь принимает totals, а не ctx.
- Control.checkpointed сбрасывается в Start и выставляется только после успешной записи WriteCheckpoint; переход Pausing -> Paused и возврат nil в startRun используют Checkpointed(), а не живой State().
- Unverified: Ошибка записи checkpoint в отложенных операциях replan/close не покрыта тестом: сценарий отказа записи проверен только на пути агента юнита, классификация deferred-событий при этой ошибке держится на общем pauseOutcome.

### [x] P052.1.13 Stop replan and close before commit on a Pause
Read:
- internal/runner/replan.go:L290-330
- internal/runner/replan.go:L440-721
- internal/runner/close.go:L240-430
- internal/runner/close.go:L460-540
- internal/runner/control.go:L120-240
- internal/runner/events.go:L130-170
- internal/runner/run.go:L1437-1490
- internal/runner/pause_test.go:L1039-1150
- internal/runner/replan_test.go:L233-257
Do:
- Сразу после успешного ответа replan- или close-агента, до валидации и коммита, проверь `pauseCancellation(ctx)` и остановись существующим путём паузы: replan восстанавливает снимок `before`, close пишет checkpoint со снятым до вызова fingerprint; запиши checkpoint/`state: Paused` и верни паузу. Полувалидированный ответ не коммить.
- Повтори проверку непосредственно перед `cycle.CommitReplan` и перед close-коммитом: пауза, пришедшая во время проверок, останавливает операцию так же, а не коммитит её.
- Если коммит уже начался, операция завершается: журнал `replan`/`close` и `run_end` дают `done`, хост переходит в Finished, а не в Paused без checkpoint. Paused остаётся только для остановки с записанным checkpoint (P052.1.12).
- Для детерминированного сценария успешного окна добавь тестовые швы на границах после вызова агента и после коммита (пакетные переменные-функции, no-op в проде): пауза между возвратом вызова и валидацией иначе не воспроизводится. Не ослабляй существующие тесты паузы на вызове агента.
- Обнови docs/design/runner.md#поток-событий: пауза после успешного вызова replan/close до коммита останавливает операцию с checkpoint, после начала коммита операция завершена и журнал/хост дают done/Finished.
- Расширь internal/runner/pause_test.go: (а) replan по фикстуре ревью — fake поднимает `### [!] P001.1.1` до `### [~]` и возвращает done, пауза на шве: план восстановлен, HEAD не сдвинут, коммита replan нет, checkpoint `stage: replan`, `state: Paused`, `replan`/`run_end` paused/0, второго вызова агента нет, ResumeRun даёт явный нерезумируемый стоп; (б) close — fake архивирует план и возвращает done, пауза на шве: checkpoint `stage: close` со снятым fingerprint, close-коммита нет, `close`/`run_end` paused/0, хост Paused; (в) пауза после завершённого коммита: журнал done и хост Finished, checkpoint не появляется.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: `newReplanFixture` и `closeArchiveStep` уже дают нужные фикстуры; fake replan должен именно вернуть `done` после правки, иначе сценарий уйдёт в error-ветку вызова. Шов не должен влиять на обычный прогон и не должен оставаться единственной проверкой: существующие тесты паузы на вызове агента остаются.
Depends: P052.1.12
Commit: P052.1.13: stop replan and close before commit on pause
Handoff:
- Тестовые швы replanSeam/closeSeam живут в internal/runner/seams.go и вызываются с именами границ after-agent, before-commit, after-commit; в проде это no-op, тесты подменяют их пакетные переменные и обязаны восстанавливать в t.Cleanup.
- closeChecks теперь зовёт runnerSelf(root, "lint") вместо cycle.Self: в проде поведение то же, но контролируемый тест может подменить self-вызов через controlRunSelf и довести close до коммита.
- Пауза без записанного checkpoint, обнаруженная на верхней проверке ctx.Err() в runLoop, завершает прогон кодом 0 без ошибки: это случай паузы, пришедшей в уже начавшийся коммит replan; run_end получает exit_code 0 и не получает поле paused.
- Для checkpoint паузы вне вызова агента сохранён lastLog — путь лога последнего вызова (replan/close), иначе lastLog пуст и checkpointLogs не находит лог.
- Unverified: Пауза, пришедшая непосредственно внутрь git-команды коммита (а не на шов сразу после неё), проверена только косвенно: тесты ставят паузу на seam после коммита, сама гонка внутри CommitReplan/close-коммита не воспроизводится.

### [x] P052.1.14 Give the checkout lock one owner
Read:
- internal/runner/control.go:L165-190
- internal/runner/run.go:L165-240
- internal/runner/helpers.go:L250-290
- internal/runner/control_test.go:L90-130
- internal/runner/run_test.go:L740-790
- internal/runner/start_test.go:L1-40
Do:
- Оставь один владелец замка `<git-dir>/docflow/run/run.lock`: `StartCheckout` берёт его на всё время `execute` и освобождает после возврата; `runWithOptions` не должен повторно брать и снимать тот же замок, когда управляемый прогон уже под ним. Передай владение явно (флаг в runOptions или проверка `Control` по root), чтобы `RunControlled` без `StartCheckout` и обычный `Run` по-прежнему брали замок сами.
- Освобождай только свой замок: внутреннее завершение не удаляет файл, пока внешний владелец работает, и не удаляет чужой lock; файл с pid живого раннера остаётся виден `run --stop` на весь прогон.
- Расширь control_test.go: внутри `StartCheckout` после управляемого прогона замок ещё существует, второй `StartCheckout`/`run` получает отказ; после возврата `execute` замок снят и новый запуск проходит; два конкурирующих запуска не могут одновременно удерживать один checkout. Сохрани существующие проверки занятого замка и обычного run.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: `requestStop` читает `run.lock` и pid — не заменяй файл на in-memory замок. Обычный `Run` без хозяина должен сохранить прежний код 2 на занятом checkout.
Depends: P052.1.1
Commit: P052.1.14: give the checkout lock one owner
Handoff:
- runOptions gained locked: true; startRun passes it because StartCheckout already holds the lock, while the public RunControlled and ordinary Run still acquire it themselves
- runLock now refuses a lock held by any live pid, including the current process: same-process nesting is no longer needed after the single-owner change
- startControlledRun/StartRun tests keep working unchanged; TestControlledRunKeepsTheOuterCheckoutLock is the reference for asserting lock presence at an inner runWithOptions return
- Unverified: Windows lock refusal/release paths are not exercised in this run; the suite ran on macOS
- Unverified: run --stop was not pointed at a live controlled run's lock; the tests inspect the lock file directly rather than the stop-request path

### [x] P052.1.4 Review runner control and recovery
Read:
- internal/runner/control.go
- internal/runner/checkpoint.go
- internal/agentio/activity.go
- internal/runner/pause_test.go
Do:
- Проверь Phase diff по Goal, Constraints, Decisions и Do юнитов фазы; особенно конкуренцию stdin, полноту остановки процессов, паузу в commit boundary, сохранение лимитов и совместимость обычного run.
- Разбери каждый пункт Test changes: обоснованное изменение допускается, ослабленная ради зелёного Verify проверка — дефект.
- Убедись, что находки предыдущего ревью закрыты сценариями с проверкой побочных эффектов: (а) после Paused на отвергнутом resume второго старта нет, (б) `Activity.Input` на аргументе 1 МБ в пределах лимита при живом `Ref`, (в) `state: Paused` публикуется на ожидании executor-chain и в pending-commit recovery.
- Ничего не редактируй; при дефекте верни blocked с blocker plan и конкретным сценарием, иначе зафиксируй вывод в Handoff.
Verify: `go test -timeout 20m ./...`
Depends: P052.1.1, P052.1.2, P052.1.3, P052.1.5, P052.1.6, P052.1.7, P052.1.8, P052.1.9, P052.1.10, P052.1.11, P052.1.12, P052.1.13, P052.1.14
Role: review
Commit: P052.1.4: review runner control
Handoff:
- Blocked (plan): Phase 1 records a controlled Pause as a runner error in the durable event stream and drops the state event at the completion boundary. Reproduced on the existing fixtures: (1) pause while the fake agent sleeps (TestPauseStopsAgentGroupAndWritesCheckpoint) - events contain state:Paused, but unit_end.status=error and run_end has reason "context canceled", exit_code=2; (2) pause while verify/commit completes the unit (TestPauseDuringVerifyFinishesCommitBoundary, TestPauseAtCommitBoundaryCompletesUnit) - checkpoint stage=complete and unit_end.status=done, but there is no state:Paused event at all and run_end still reports context canceled/exit 2. docs/design/runner.md defines unit_end.status=error as a runner-call failure and run_end.exit_code as the runner's final code; P052.1.6 requires pauseState right after every writePauseCheckpoint, but run.go:1413 writes the complete checkpoint without it. A TUI reading the journal (P052.3.2) cannot show the paused stage and presents the paused run as failed. Fix: classify a controlled Pause in the unit_end/run_end emission (a paused status/reason, not error/context canceled/2) and call pauseState after the checkpoint at run.go:1413; extend pause_test.go to assert the state event at the complete boundary and the paused unit_end/run_end records in both scenarios.
- Blocked (plan): P052.1.8 expects pause classification only through pauseCancellation, but three state emitters are guarded by generic ctx cancellation: run.go:771-774 (executor-chain wait), 831-838 (recovered pending commit), and 1418-1423 (complete boundary). writePauseCheckpoint no-ops when the host is not StatePausing, yet pauseState still emits state:Paused. Reproduced with an overlay test (go test -overlay, repository untouched): start a controlled run with a cancelable parent, gate commit, cancel the parent, release; the journal contains state:Paused stage=complete while unit_end is done and run_end reports context canceled/exit_code 2. The same happens while waiting for the executor chain (stage=quota) and after a recovered pending commit. A normal run's Ctrl-C reaches the same branches with no host, so an ordinary v1 stream also gets a false state:Paused. Fix: guard every pauseState with pauseCancellation(ctx), or return a wrote flag from writePauseCheckpoint and emit only when it wrote; add a parent-cancellation/signal test asserting no state event. The previous blocker is closed: real Pause now journals unit_end.status=paused (done at the complete boundary) and run_end paused/reason paused/exit 0, and pauseState follows every writePauseCheckpoint.
- Blocked (plan): Blocked (plan): a Pause during an auto-replan agent call stops the run as Paused with no checkpoint, no state event and a half-edited plan. Reproduced with an overlay test (go test -overlay, repository untouched) on the executor fixture: plan header `replan: pi | zai/glm-5.3 | max`, Args{Replans: 1}; the unit answers blocker: plan; the replan fake edits the unit's Do and then sleeps; Pause during that call. Result: control.State() = Paused; ReadCheckpoint = no such file; git status = `M docs/plans/P001-plan.md`; run events = run_start, config, unit_start, attempt, stream, unit_end, attempt, stream, replan, run_end, where replan is result:error exit_code:7 and run_end is paused:true exit_code:0 with no state event. The next run reads the edited, unvalidated plan, and ResumeRun answers 'no resumable checkpoint'. This violates P052.1.2's Do (checkpoint on every transition to Paused) and the decision that a Pause preserves continuation: runReplan returns 7, err on a canceled call (replan.go:459) without restorePlan or pauseCheckpoint, and its chain/quota/refusal waits (replan.go:332, 535, 581) do the same; runClose (close.go:389, 446) and the pending-commit recovery failure path share the pattern. Fix: classify a controlled Pause in runReplan and runClose, restore the plan through the existing stop/restorePlan path so no half-validated edit survives, and write a checkpoint plus state event that either resumes the replan or explicitly reports a non-resumable stop; extend pause_test.go with this scenario (plan restored or checkpoint written, no second agent, no dirty plan). Second finding: every stdout activity from CallAgent carries Ref.Stream: '' although the P052.1.3 handoff promises a stream name; ReadActivityPage silently treats '' as stdout and TestCallAgentActivitySubscription never asserts the field. Reproduced with an overlay probe: CallAgent with Stream: codex returns refs {Stream:'', Offset:96, Length:89}. Set the stdout writer's stream in internal/agentio/process.go and assert Ref.Stream in the subscription test. The rest of the review passed: the three previous findings are closed with side-effect checks (single --resume start after a paused rejected resume, 1 MB Input within the limit with a live Ref, state at the executor-chain wait and the recovered pending commit); all six Test changes are fixture/index updates with no weakened assertion; stdin, signals and Progress stay with the UI; the checkout lock is shared; docflow verify P052.1.4 (preflight + gate go test -timeout 20m ./...) passed on macOS.
- Blocked (plan): Blocked (plan): two reproduced Pause gaps remain in the replan/close paths, plus a checkout-lock race found by reading. (1) A Pause during the closing agent after it archived the plan cannot write its checkpoint: closePrompt tells the agent to run '<cli> close P001' first, which deletes the plan, and writePauseCheckpoint then calls planFingerprint -> cycle.FindPlan fails. Reproduced with an overlay test on readyCloseFixture (repository untouched): the fake close agent runs closeArchiveStep and sleeps; Pause during the sleep gives control.State() = Failed, ReadCheckpoint = no such file, no state event, while the deferred close event is result: paused, exit_code: 0 and run_end is paused: true, reason: paused, exit_code: 0. This violates P052.1.10 Do (b) and the trap 'Paused is visible only after ... writing the state'. Fix: capture the plan fingerprint before the closing call (or make it optional for a non-resumable close checkpoint), and do not let pauseOutcome/run_end reclassify a failed checkpoint as paused/0. (2) A Pause after the replan/close agent returns successfully but before its validation/commit is ignored by the operation, which commits, yet the host reports Paused with no checkpoint. Reproduced on newReplanFixture: unit answers blocker: plan; the replan fake lifts ### [!] P001.1.1 to ### [~] and returns done; Pause after the replan stream event; the replan validates and commits (HEAD = 'P001: replan P001.1.1', plan has [~]), control.State() = Paused, ReadCheckpoint = no such file, no state event, replan = result: paused, exit_code: 0, run_end = paused: true, exit_code: 0. runClose has the same gap after a successful closing call (no pause check before closeChecks/commit), so a Pause there commits the close while the journal says paused. Fix: classify pauseCancellation immediately after a successful replan/close call and stop with the existing checkpoint (replan restores before), or complete the operation to its commit boundary and report it done/finished; never Paused without a checkpoint. Extend pause_test.go with both success-window scenarios and make the close fake archive before sleeping. (3) Checkout lock: StartCheckout takes run.lock and runWithOptions takes it again; the inner release removes the file while the outer holder is still finishing, so another runner can acquire the lock and then have it removed by the outer release, allowing two runners on one checkout. Code reading, not reproduced; fix by one lock owner. The rest passed: the three previous findings are closed with side-effect checks (single --resume start after a paused rejected resume; 1 MB Input within the limit with a live Ref; state at the executor-chain wait and the recovered pending commit); all six Test changes are fixture/index updates with no weakened assertion; stdin, signals and Progress stay with the UI; the ordinary run and signal paths keep their old statuses and codes; docflow verify P052.1.4 (preflight + gate go test -timeout 20m ./...) passed on macOS.
- Phase 1 reviewed at HEAD eeab723 against the plan's Goal, Constraints, Decisions and the Do of units 1.1-1.14; no defect found, no file edited.
- Previous findings are closed with side-effect tests: one resumed call after a paused rejected resume; 1 MB tool Input within ActivityInlineLimit with a Ref that pages the full original line; state:Paused plus checkpoint at the executor-chain wait and in recovered pending-commit success/failure; replan/close restore or keep the pre-call fingerprint and stop before commit; the after-commit window ends Finished without a checkpoint.
- The seven Test changes are fixture/index/refactor updates (event index shifts for config/stream, a now-relative roster day, writePauseCheckpoint's signature, extraction of pendingCommitFixture, a checkpoint instead of a nil return, the foreign-pid skip); no assertion was weakened, and terminal_run_test.go gained an assertion that a signal run_end carries no paused field.
- Interactive runs keep stdin and signals with the UI: runControlled uses io.Discard, sets Yes, skips signal.Notify/watchKeys, and confirm/askUnblock never read a terminal; StartCheckout is the only checkout-lock owner and the inner runWithOptions does not release it.
- Verification: docflow verify P052.1.4 passed (preflight + gate go test -timeout 20m ./...) on macOS; the targeted pause and activity tests pass with -v, none skipped.

## Phase 2: Terminal workspace and live inspection

### [x] P052.2.1 Build the terminal plan workspace
Read:
- cmd/docflow/main.go:L1-110
- cmd/docflow/main.go:L209-321
- internal/format/doc.go:L1-180
- internal/cycle/cycle.go:L20-165
- internal/term/term_test.go
- internal/runner/pty_darwin_test.go
Do:
- Подними `go` в go.mod до 1.26 и обнови упоминания Go 1.24+ в README.md,
  README.ru.md, docs/usage.md и docs/design/cli.md; CI и release читают
  версию из go.mod (`go-version-file`), workflow не трогай.
- Добавь закреплённые зависимости из Decisions; создай internal/tui/model.go, view.go, keys.go, app.go и app_test.go, подключи команду tui в cmd/docflow.
- Отобрази Queue, планы, фазы и юниты со статусами; поддержи раскрытие, перемещение, фокус, прокрутку и карточку Goal/Do/Verify/Depends/Handoff выбранного юнита. Читай Markdown существующим парсером, не дублируй формат.
- Покажи loading, empty, error и content с доступным действием, помощь клавиш и текстовый статус кроме цвета. Сохраняй выбор при обновлении плана.
- От 100 колонок используй две панели, ниже — переключаемую одну; проверь 120x40, 80x24, 40x12, кириллицу, длинные пути, resize и NO_COLOR. При недостаточном размере покажи понятное сообщение, не панику.
- Добавь model/view-тесты и реальный терминальный smoke на доступной платформе: открытие, Enter, Tab, resize и выход восстанавливают echo, курсор и экран. Без TTY команда отказывает с подсказкой обычных команд.
Verify: `go test -timeout 20m ./internal/tui/ ./cmd/docflow/`
Depends: P052.1.4
Commit: P052.2.1: add terminal plan workspace
Handoff:
- internal/tui is read-only for this unit: Load() parses docs/docflow.md and queue/frozen plans with format.LoadPlan; no agent, lock, checkpoint or plan edit is wired yet. Later units extend Model, rows/detailLines and Keys from here.
- The command wrapper is cmd/docflow.handleTUI: it resolves projectRoot, enables VT processing on stdout when it is a file, then calls tui.Run with os.Stdin and the supplied stdout. tui.Run refuses non-terminals before loading plans.
- Layout contract implemented: two panes at width >= 100, one switchable pane below; sections/plans/phases default expanded; unit rows carry the status word and the details card shows plan Goal plus Do/Verify/Depends/Handoff.
- PTY smoke builds ./cmd/docflow into a temp dir and drives a real terminal; it asserts queue rendering, Enter collapse, resize to too-small and back, q exit, alternate-screen exit, cursor show and echo restore.
- Unverified: Terminal behavior on Windows consoles and Linux terminals is not exercised locally: Verify ran the darwin PTY smoke and cross-compiled builds/tests for windows/amd64 and linux/amd64, but did not run them.

### [x] P052.2.2 Inspect live messages tools and changes
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
Handoff:
- Activity API for P052.2.3: feed runner.Event into Model.Update via ActivityMsg{Event} or call Model.ApplyEvent(Event); activity.go owns the history, rendering, follow, search and paging state.
- Keep publishing the runner's `stream` event with attempt and log path before activities of the same call: the UI resolves Activity.Ref to that log by attempt and reports `call log unknown` without it.
- Keys in details: arrows/page move the activity cursor, Enter expands, [ ] page results and the verify overlay, / searches activity, f/G follow the newest, d opens git diff, v opens the verify log; tree navigation stays local.
- Git diff is `git -C root diff HEAD` with untracked paths listed separately as not part of the diff; verify overlay pages the whole verify log including its stderr section.
- The plan file docs/plans/P052-terminal-control.md was already modified before this unit; this unit did not edit or commit it.
- Unverified: No live Control publish loop is wired in this unit, so follow, bounded queue drops and paging under a real Pi/Codex/opencode stream are exercised only by synthetic events; the live path is P052.2.3 and final acceptance.
- Unverified: Paging across a 4 KiB boundary inside a multibyte UTF-8 rune is repaired to a replacement character by Sanitize but is not asserted by a dedicated test.

### [x] P052.2.3 Connect execution and lifecycle controls
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
Handoff:
- The TUI uses the polled Controller seam in internal/tui/control.go: Host wraps runner.Control, Start/Resume create a fresh Host, and the model polls events/prompts/done from the Bubble Tea loop. No Bubble Tea command blocks on a runner channel.
- Start confirmation carries runner.StartReview in runner.Request.Fields; the dialog renders permissions and prerequisites from it. StartRequirements is now shared by the CLI prompt and the dialog, so their skip rules cannot drift.
- runner.CheckoutLockOwner(root) reports a live foreign lock without acquiring it; the TUI refuses Start and lifecycle actions while it names another pid but still renders the plans.
- Lifecycle keys call cycle.Self with the same subcommands as the CLI (activate/freeze/thaw/close/unblock); tests inject this seam. A successful Close checks the archive Outcome section and names the path when the person still has to write it.
- Terminal tests follow runner files (scenario counter, checkpoint, run.lock, plan status) rather than redrawn status text: Bubble Tea's PTY diff output contains only changed suffixes, so full status words are not reliable in raw captures.
- Unverified: The live Pi/Codex/opencode paths are not exercised by this unit: terminal tests use the shell fake agent, so real session resume, provider wording and platform-specific child-process termination remain for live acceptance.
- Unverified: The Stop-after-unit path has only the fake-controller app test; no terminal test runs a real plan through it to a stable boundary.
- Unverified: The Close success path is tested with a synthetic archive file, not by closing a real completed plan with a filled Outcome through the TUI.
- Unverified: Terminal tests are darwin/linux only; Windows console behavior for these new controls is not covered.

### [x] P052.2.5 Stop the run on a terminal signal
Read:
- internal/tui/app.go:L1-46
- internal/tui/execute.go:L362-407
- internal/tui/control.go:L96-146
- internal/runner/control.go:L120-170
- internal/runner/control.go:L320-347
- internal/runner/run.go:L228-250
- internal/procgroup/procgroup_unix.go:L20-30
- internal/tui/terminal_test.go:L390-506
- internal/tui/app_test.go:L776-806
- cmd/docflow/main.go:L351-362
Do:
- Воспроизведи сначала тестом: собери CLI, запусти его в PTY на `writeRunnableProject` (fake agent спит 5 секунд), дождись старта прогона и пошли процессу TUI SIGTERM, затем отдельным сценарием SIGHUP. Сейчас агент доживает сон и пишет `a.txt`, `run.lock` остаётся, `checkpoint.json` не появляется: `program.Run` возвращается по QuitMsg от Bubble Tea мимо `requestQuit`/`stopAndExit`, а SIGHUP никто не ловит и процесс умирает сразу.
- Сделай один путь остановки после `program.Run` в `tui.Run`: возьми возвращённую модель и, если прогон жив, останови его как `stopAndExit` — Pause для Running, CancelStart для Starting с ожидающим подтверждением, ожидание Finished для Pausing/Stopping — затем закрой хост. Работу, не дошедшую до старта (Starting), заверши без checkpoint и без ошибки; прогон, уже начавший юнит, обязан дать `StatePaused` с `Checkpointed()`.
- Ограничь ожидание тестовым швом (пакетная переменная с разумным значением в проде): по таймауту закрой хост и верни ошибку «прогон не остановился за N»; при `StatePaused` без checkpoint верни ошибку, называющую незаписанное состояние. Не рапортуй успешный выход, если checkpoint не записан.
- Поставь собственный обработчик SIGHUP в платформенных файлах под build tag (unix — SIGHUP, windows — пустая реализация): сигнал штатно завершает программу (`tea.QuitMsg` через `program.Send`), чтобы отработал тот же путь остановки и восстановление терминала. SIGTERM не перехватывай повторно — его уже превращает в QuitMsg Bubble Tea.
- Не добавляй второй цикл событий и не читай клавиатуру в обход Bubble Tea; отмена ctx хоста уже убивает группу дочерних процессов (`procgroup.Detach` даёт агенту свою сессию, поэтому без отмены он переживает родителя) и отпускает `run.lock` — не удаляй lock вручную и не трогай обычный `docflow run`.
- Расширь `terminal_test.go` двумя сценариями по образцу `TestTerminalQuitWithLiveRunStopsAgent`: после SIGTERM и после SIGHUP процесс TUI выходит с кодом 0, `checkpoint.json` есть со `stage`, `run.lock` снят, `a.txt` не появляется после сна, терминал восстановлен (курсор и echo). В `app_test.go` добавь тест остановки на фейковом контроллере: Pause вызван один раз, ожидание Finished, Close один раз, ошибка при незавершившемся за таймаут прогоне и при Paused без checkpoint.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: `os.Exit` и `defer` в момент сигнала не спасают: SIGHUP без обработчика убивает процесс мгновенно, а SIGTERM Bubble Tea проглатывает. SIGKILL не покрывается — не обещай его в тексте. Не запускай остановку в горутине без ожидания: выход процесса обязан дождаться записи checkpoint. Не сокращай таймаут до нуля: пауза на границе verify/commit должна успеть записать состояние. Поведение q/Ctrl+C и существующий диалог выхода не меняй. Пользовательский текст контракта — P052.3.3.
Depends: P052.2.3
Commit: P052.2.5: stop the run on a terminal signal
Handoff:
- The terminal-signal stop lives in tui.Run after program.Run via Model.stopAfterSignal; Bubble Tea returns the final model, so the run and checkout lock are stopped there, not in a key handler.
- Controller gained CancelStart(); any new Controller implementation must provide it (fakeController in internal/tui/app_test.go already does).
- stopWaitTimeout is a package variable (60s in production) shortened by tests; do not move the wait into a goroutine, the process must not exit before the checkpoint is written.
- watchHangup is split by build tags: SIGHUP is handled in signal_unix.go, signal_windows.go is an empty hook because Windows has no SIGHUP.
- Unverified: Windows console behavior for SIGTERM/SIGHUP is only compile-checked; there is no Windows runtime test for the empty hangup hook or for stopping a live run.
- Unverified: Child-process termination on a terminal signal is exercised with the shell fake agent, not with real Pi/Codex/opencode child process trees; live acceptance remains P052.3.3.
- Unverified: SIGKILL (and a crash that bypasses deferred code) is not covered, as the unit explicitly excludes it.

### [x] P052.2.6 Read the result of the call that is still running
Read:
- internal/tui/activity.go:L433-470
- internal/tui/activity.go:L937-1013
- internal/tui/keys.go:L240-350
- internal/tui/activity_test.go:L1-70
- internal/tui/activity_test.go:L155-185
- internal/agentio/activity.go:L740-780
- internal/tui/model.go:L100-170
Do:
- Воспроизведи: running-запись инструмента (`item.started`) с ref на строку старта, после старта в лог дописывается вывод; expand и `]` показывают только строку старта, `total` равен длине этой строки — байты, появившиеся во время вызова, недоступны до прихода завершающей activity.
- Читай страницу запущенного инструмента от `ref.Offset` до текущего конца файла: длину бери из размера лога в момент чтения, а не из `ref.Length`, и показывай в подвале «call still running, N bytes so far».
- Разреши живой хвост: `]` в последнем доступном окне перечитывает лог с текущей длиной, а открытая первая страница записи обновляется при новом событии того же attempt, чтобы рост был виден без повторного раскрытия. Живой режим действует, только пока запись последняя в истории юнита и её состояние running; следующая запись переводит страницу в обычный режим и больше не дочитывает лог.
- Не выдавай неполный вывод за результат: состояние инструмента остаётся running, результат помечается неподтверждённым, завершающая activity со своим ref остаётся источником полного результата; исходный лог не меняется, частично записанная последняя строка не ломает страницу (Sanitize), окно остаётся 4 КиБ и окон не больше `activityPagesPerEntry`.
- Покрой в `activity_test.go`: (а) живой лог растёт между нажатиями `]` — новые байты появляются, `total` растёт; (б) после завершающей записи полный результат читается из неё, а запущенная запись больше не дочитывает лог; (в) многократный `]` по живому логу не превышает лимит окон; (г) исчезнувший или пустой лог даёт явную ошибку, а не пустую страницу.
Verify: `go test -timeout 20m ./internal/tui/ ./internal/agentio/`
Traps: `Ref.Length` — длина строки в потоке, а не окно результата; расширять её в `agentio` нельзя, иначе поедет проверка ref в `internal/agentio/activity_test.go`. Размер файла меняется между stat и чтением — короткое чтение допустимо и не считается ошибкой. Не обновляй страницы всех записей на каждом тике: только открытую и только первую. Не блокируй цикл UI больше одного чтения 4 КиБ и не держи лог в памяти.
Depends: P052.2.5
Commit: P052.2.6: read the result of a running call
Handoff:
- agentio now exposes ReadLiveActivityPage(log, ref, skip, limit): it stats the file and reads ref.Offset..current EOF, unlike ReadActivityPage which still uses ref.Length. Both share readActivityPageAt.
- An activityPage now carries Unit, Seq, End and Live. Live mode is computed by Model.liveActivity (running tool and last entry of the unit) and frozen by syncLivePages on every event; refreshLivePage rereads page 0 before that freeze for an event of the same attempt.
- keys.pageActivity reloads the last live window at the same Skip when page.Live && page.End >= page.Total; it never refreshes pages other than the open first one.
- P052.2.7 changes path resolution in loadActivityPageAs: the log path still comes from history.Log and must be resolved from the checkout root there.
- Unverified: The live tail and same-attempt refresh are tested with synthetic events and a manually growing file, not with a real Pi/Codex call log while a tool runs; live acceptance remains P052.3.3.
- Unverified: A live 4 KiB window boundary inside a multibyte UTF-8 rune is only neutralized by Sanitize (replacement character) and has no dedicated assertion in the live-page tests.

### [x] P052.2.7 Resolve event log paths from the checkout root
Read:
- internal/tui/activity.go:L194-210
- internal/tui/activity.go:L433-470
- internal/tui/keys.go:L355-435
- internal/runner/events.go:L250-300
- internal/tui/app_test.go:L94-160
- internal/tui/terminal_test.go:L183-318
Do:
- Воспроизведи: запусти TUI из подкаталога проекта (в модельном тесте `t.Chdir(sub)`, в терминальном `child.Dir = project/sub`); страница активности и оверлей verify отвечают `cannot read .git/docflow/run/…: no such file or directory`, потому что событие хранит путь относительно checkout (`relativeLog`), а TUI открывает его от cwd процесса.
- Разрешай путь в момент чтения: относительный — от `m.root` (корень checkout из `format.FindRoot`), абсолютный — как есть; после `Clean` отвергай выход за корень (`..`) и показывай разрешённый путь в ошибке и в подвале страницы.
- Храни в истории исходный путь события: `relativeLog` остаётся форматом потока версии 1; не делай пути абсолютными в событиях, не меняй cwd процесса и не дублируй разрешение в двух местах — один помощник для страницы активности и оверлея verify.
- Покрой: тест помощника (относительный, абсолютный, `../..`, пустой путь) без чтения файлов; модельный тест из подкаталога читает страницу активности и оверлей verify; терминальный тест из подкаталога — прогон до verify, клавиша `v` показывает маркер из `Verify` юнита, а не ошибку чтения.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: не склеивай root с уже абсолютным путём и используй `filepath.FromSlash` для путей из событий; verify-лог тоже лежит в `<git-dir>/docflow/run` и его путь относителен. Несуществующий лог остаётся явной ошибкой, а не пустой страницей. Не подменяй корень на cwd: TUI специально работает от корня checkout.
Depends: P052.2.6
Commit: P052.2.7: resolve event log paths from the root
Handoff:
- Log paths from version 1 events now go through resolveLogPath(root, path) in internal/tui/activity.go; activity pages keep the resolved location in page.Path, verifyReference returns the resolved path so m.verifyLog is absolute.
- The activity footer now names the resolved call-log path. resolveLogPath treats absolute event paths as-is and only joins relative ones to m.root; a relative path escaping the root and an empty path are explicit read errors.
- Tests added: TestResolveLogPath, TestLogPathsResolveFromCheckoutRoot (t.Chdir into project/sub), TestTerminalVerifyFromSubdirectory; writeRunnableProject now delegates to writeRunnableProjectVerify(t, verify).
- Unverified: Windows path resolution (drive letters, backslash separators) is not exercised: tests run on the current macOS target and the terminal PTY tests are POSIX-only.

### [x] P052.2.8 Refuse control under a live foreign runner
Read:
- internal/tui/execute.go:L48-60
- internal/tui/execute.go:L80-135
- internal/tui/execute.go:L407-425
- internal/tui/model.go:L295-310
- internal/tui/app_test.go:L808-831
- internal/runner/helpers.go:L254-275
- internal/tui/terminal_test.go:L183-260
Do:
- Воспроизведи: TUI открыт на свободном checkout, затем появляется живой чужой раннер (в терминальном тесте — процесс `sleep`, его pid записан в `.git/docflow/run/run.lock`); клавиша `a` активирует draft, хотя должна отказать, а отказ виден только если lock был при загрузке.
- Проверяй lock в момент действия, а не только при загрузке: `refreshLock` перед Start/Resume и перед каждым lifecycle-действием, а также после завершения прогона, чтобы статус-строка и guard не расходились; решение принимай по живому pid, а не по наличию файла.
- Ошибку `CheckoutLockOwner` показывай как unknown и отказывай в управлении, а не считай checkout свободным; просмотр планов и истории остаётся доступен.
- Покрой в `app_test.go`: `lockCheck` меняется после загрузки — `s` и `a` отказывают без вызова фабрики контроллера и lifecycle-шва, после снятия lock те же клавиши проходят, ошибка `lockCheck` даёт отказ с причиной. В `terminal_test.go`: открыть TUI, подложить `run.lock` живого процесса — `a` не меняет `status` плана на диске и называет pid; убрать lock — `a` активирует план (`status: active` на диске и id в Queue).
Verify: `go test -timeout 20m ./internal/tui/`
Traps: свой живой прогон держит тот же lock с нашим pid — не считай его чужим (`refreshLock` уже сравнивает с `os.Getpid`). Не удаляй и не перезаписывай `run.lock` чужого процесса, lifecycle идёт через `cycle.Self`, который сам lock не проверяет. Не вызывай `refreshLock` на каждое нажатие стрелки: только перед действиями и после прогона. В терминальном тесте убей процесс и сними lock в `t.Cleanup`, иначе lock переживёт тест.
Depends: P052.2.7
Commit: P052.2.8: refuse control under a foreign runner
Handoff:
- lockCheck in app_test's loadedModel is now stubbed to report a free checkout: test fixtures are plain directories without a git dir, so the real runner.CheckoutLockOwner would return an error and (under the new rules) refuse every action. Tests that exercise the lock set the seam explicitly.
- Model.lockErr carries a failed lock check; refreshLock is now called at the start of startRun (Start and Resume), at the start of lifecycleKey, and in handleFinished so the status line and the guard agree after the workspace's own run released its lock.
- The refusal text comes from one helper m.lockRefusal(purpose): held lock names the pid, an unreadable lock names the error; both keep viewing/reading allowed. The status line shows lock:unknown for an unreadable lock.
- TestTerminalLockAtAction runs at 240 columns: the notice sits at the end of the redrawn status line and is truncated below ~200 columns, so a narrower PTY cannot assert the pid text; selection is done via the / filter plus one down, because the Queue section row is always kept as a match header.
- Unverified: The unreadable-lock path is driven through the lockCheck seam in app_test only; a real CheckoutLockOwner error on the target (e.g. an unreadable run.lock or a non-git root) is not exercised end-to-end by any test.

### [x] P052.2.9 Keep outcome notices across a plan reload
Read:
- internal/tui/model.go:L210-313
- internal/tui/execute.go:L157-250
- internal/tui/execute.go:L550-580
- internal/tui/keys.go:L84-92
- internal/tui/view.go:L575-633
- internal/tui/app_test.go:L448-461
- internal/tui/app_test.go:L744-797
- internal/tui/terminal_test.go:L280-300
Do:
- Воспроизведи: `applySnapshot` стирает `m.notice` на каждом успешном snapshot, поэтому сообщение close с путём архива (P052.2.3), причина паузы и ошибка прогона живут только до ближайшей перезагрузки плана. В TestCloseNoticeNamesUnwrittenArchiveText применяется лишь ответ lifecycle-команды, а возвращённый ею loadCmd отбрасывается: доведи тест до применения snapshotMsg и получи пустой notice; то же для успешной активации клавишей `a`.
- Воспроизведи завершение прогона: fakeController финиширует как Failed и как Paused; после `hostPollMsg` примени snapshotMsg, который запрашивает pollController, — «run failed: …» и «paused; Resume continues…» исчезают так же. Покрой случай события: `unit_end` во время Pausing приходит ActivityMsg и планирует reload; notice о паузе обязан пережить и его.
- Исправь правило в `Model.applySnapshot`: успешный snapshot сохраняет notice исхода, а стирается только текст самой загрузки — «reloading», который ставит клавиша `r` (до загрузки модель в StateLoading). Новое действие по-прежнему заменяет прежний текст.
- Не меняй поведение ошибки загрузки: StateError с текстом ошибки, без устаревшего успеха рядом; `r` показывает «reloading» до завершения загрузки.
- Обнови устаревший комментарий в TestTerminalRunPauseResumeQuit (terminal_test.go:L295-296) про замену notice перезагрузкой дерева; остальные утверждения этого теста не трогай.
- Раздели модельные проверки по сценариям — успешный close, успешная активация, завершение Failed и Paused, notice паузы под `unit_end`. В каждом утверждай, что reload действительно запланирован (команда не пуста), и что после применённого snapshotMsg статус-строка показывает прежний текст.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: путь завершения прогона отдаёт loadCmd внутри tea.Batch — проверяй поведение через snapshotMsg и отдельно утверждай, что pollController и applyLifecycle возвращают непустую команду, иначе тест перестанет замечать отсутствие reload. TestRefreshKeyReloads ожидает «reloading» до загрузки и StateReady после неё — его ожидания не переписывай под новое правило. Не делай notice вечным: иначе статус-строка врёт про результат после следующего действия. Не выводи notice в тело панели и не дублируй его в дереве. Bubble Tea PTY-снимок содержит только изменённые хвосты строк — полный текст notice в терминальном тесте не проверяй (Handoff P052.2.3), оракул — модельный тест.
Depends: P052.2.8
Commit: P052.2.9: keep outcome notices across reload
Handoff:
- applySnapshot now clears m.notice only when m.state == StateLoading && m.notice == reloadingText (the constant moved to model.go); the error branch still clears it, and any new action overwrites the text.
- Test helper applyLoad(t, model, cmd) flattens a tea.BatchMsg, applies every snapshotMsg it yields and fails when the command carries none — use it for any reload-path assertion.
- The close test now applies the lifecycle loadCmd; the terminal test's comment about 'notice replaced by the tree reload' was corrected, its assertions untouched.
- New tests (activate, Failed, Paused, unit_end under Pausing, reload placeholder cleared) were mutation-checked: reverting to the unconditional m.notice = "" made all five fail.
- Unverified: The live terminal no longer asserts the notice text after reload anywhere: the PTY test only checks exit code and the plan unit, so the full status-line text survives only as a model-level claim (terminal_test.go:295 comment).
- Unverified: applyLoad handles a one-level tea.BatchMsg only; a future wrapper such as SequenceMsg would make such tests fatal rather than silently wrong, so a real regression in that wrapper shape is not covered until a test adapts applyLoad.

### [x] P052.2.10 Refuse unblock under a foreign runner
Read:
- internal/tui/execute.go:L48-82
- internal/tui/execute.go:L100-160
- internal/tui/execute.go:L324-386
- internal/tui/execute.go:L490-528
- internal/tui/execute.go:L550-575
- internal/cycle/commit.go:L500-515
- internal/tui/app_test.go:L576-594
- internal/tui/app_test.go:L693-743
- internal/tui/app_test.go:L896-923
- internal/tui/terminal_test.go:L807-1053
Do:
- Воспроизведи модельным тестом: после прогона как Blocked с needs human диалог открыт; `lockCheck` возвращает живой чужой pid 4242; Enter с заметкой сейчас вызывает lifecycle-шов с `unblock`, меняет план на диске и пишет «unblocked; starting the run again». Ожидаемое: шов не вызван, файл плана побайтно прежний, диалог остался открыт с введённой заметкой, статус называет pid.
- Повтори с ошибкой `lockCheck` (os.ErrPermission): отказ в формулировке неизвестного lock, шов не вызван, заметка сохранена.
- Поставь в ветке dialogHuman (Enter) ту же проверку, что в lifecycleKey: `refreshLock`, затем `lockRefusal` до постройки команды. Проверка принадлежит UI — `cycle.Self` это диспетчер CLI и lock не берёт.
- Устрани перезапись в ветке `unblock` из applyLifecycle: она присваивает «…unblocked; starting the run again» поверх решения startRun, поэтому отказ старта выглядит как перезапуск. Пусть startRun сообщает, создан ли контроллер: при отказе остаётся notice отказа, при старте текст называет и unblock, и запуск.
- Покрой гонку: lifecycle-шов при вызове переключает `lockCheck` на чужой pid — раннер появился между unblock и стартом. startRun обязан отказать, контроллер не создан, notice называет чужой pid и не подменяется перезагрузкой после unblock.
- Сохрани свободный путь: lock свободен — unblock вызывается с `--note=…`, второй контроллер стартует, notice переживает перезагрузку (P052.2.9) и называет и unblock, и запуск. TestHumanBlockOffersContinueWithExistingUnblock расширь, а не переписывай.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: заметка живёт в `m.dialogInput` — отказ не должен её очищать, иначе человек набирает заново. Проверка перед unblock и проверка внутри startRun — два разных момента; покрывай оба, иначе гонка вернётся. Шов `model.lifecycle` имеет вид `func(root string, args ...string)`, и существующий тест сверяет аргументы `unblock`, `P001.2.2`, `--note=вошёл` — порядок не меняй. Не эмулируй чужой раннер удалением плана или `run.lock`: меняется только шов `lockCheck`, файлы плана и lock не трогай. Не добавляй `git init` в фикстуры — `loadedModel` уже заглушает `lockCheck` (P052.2.8). Не проверяй отказ по тексту статуса в PTY-тесте: diff-снимок Bubble Tea содержит только изменённые хвосты строк, оракул — модельный тест; TestTerminalLockAtAction не ослабляй.
Depends: P052.2.9
Commit: P052.2.10: refuse unblock under a foreign runner
Handoff:
- startRun now returns (Model, tea.Cmd, bool) where the bool reports a created controller; future callers that combine the start with another action must branch on it instead of overwriting m.notice.
- The unblock refusal text is '<lock notice>; viewing is allowed, the unblock is refused' — the dialog stays open and m.dialogInput keeps the typed note, so the person does not retype it.
- Test helper humanBlockModel(t, controllers...) drives a workspace to an open human-block dialog and returns the model plus the P001 plan path; use it for further human-block cases.

### [x] P052.2.11 Take the call log from the attempt event
Read:
- internal/runner/run.go:L1020-1050
- internal/runner/events.go:L215-274
- internal/tui/activity.go:L87-120
- internal/tui/activity.go:L195-210
- internal/tui/activity.go:L495-530
- internal/tui/activity_test.go:L40-130
Do:
- Воспроизведи реальным порядком событий: `attempt` с путём лога приходит до `CallAgent`, `stream` — только после возврата вызова. Страница активности, построенная по `attempt` и activity без `stream`, сейчас отвечает «the stream link has not arrived», хотя лог уже назван; `activity_test.go` этого не видит, потому что подаёт `streamEvent` до activity — порядок, которого раннер не даёт.
- Записывай лог попытки из события `attempt` (поля `log` и `n`) в историю юнита; формат события не меняй: `stream` остаётся ссылкой с offset/length после вызова и обновляет те же данные.
- В `ActivityHistory.Log` не подставляй `lastLog` для попытки с номером больше нуля: неизвестная попытка — явная ошибка «call log unknown for attempt N», а `lastLog` остаётся только для записей без номера (attempt 0).
- Покрой в `activity_test.go`: (а) порядок `attempt` → activity открывает лог и показывает байты текущего вызова; (б) resumed/fallback-попытка (attempt 2) не берёт лог attempt 1; (в) activity без номера попытки по-прежнему пользуется последним логом; (г) отсутствие лога даёт ошибку с номером попытки, а не пустую страницу.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: не двигай `stream` до `CallAgent` — смещения известны только после вызова, а формат v1 менять нельзя. Номер у `attempt` лежит в поле `n`, у activity — в `attempt`, не перепутай. Не расширяй `Ref.Length` (P052.2.6): страница по-прежнему читает от `ref.Offset` до текущего конца файла. `TestCallAgentActivitySubscription` и проверки ref в `internal/agentio` не трогай.
Depends: P052.2.6
Commit: P052.2.11: take the call log from the attempt event
Handoff:
- activityHistory.logs is filled by both 'attempt' (n/log) and 'stream' (attempt/log); the attempt event arrives before CallAgent, stream only after it returns.
- ActivityHistory.Log now returns (string, error): numbered attempts never fall back to lastLog; attempt 0 keeps using the newest log.
- Unverified: The real runner order attempt → activity without a stream is not exercised end-to-end; the new tests construct runner events directly.

### [x] P052.2.12 Stop from the start confirmation
Read:
- internal/tui/execute.go:L340-500
- internal/tui/execute.go:L160-200
- internal/runner/control.go:L289-345
- internal/tui/app_test.go:L495-545
- internal/tui/app_test.go:L1165-1195
- internal/tui/terminal_test.go:L184-323
- internal/tui/terminal_test.go:L523-643
Do:
- Воспроизведи модельным тестом: контроллер в `StateStarting`, prompt старта ещё не пришёл, `q` и Enter; `stopAndExit` зовёт `StopNow`, который требует Running, ошибку глотает, и выхода не происходит. Ожидаемое: `CancelStart` вызван один раз, `StopNow` и `Pause` — ни разу, `Close` один раз, команда выхода появляется только после `Finished`.
- Воспроизведи в терминальном тесте одним write `s`, `q`, `Enter`: сейчас процесс остаётся жив, открывает стартовый диалог, держит `run.lock` и не пишет checkpoint. Ожидаемое: код выхода 0, `run.lock` снят, `checkpoint.json` отсутствует, агент не вызван, терминал восстановлен.
- В `stopAndExit` для `StateStarting` сначала ответь на пришедший prompt отказом, затем вызови `CancelStart` независимо от `m.pendingPrompt`; если `CancelStart` проиграл гонку `MarkRunning`, переходи в Pause и жди checkpoint, как в `stopAfterSignal`. Ожидание `Finished` — существующим poll-путём с `exitAfterRun`, без второго цикла и без `os.Exit`.
- Не рапортуй успешный выход раньше факта: пока `Finished` не наступил и `Close` не сделан, процесс не завершается; при `Finished` без ошибки — выход 0.
- Закрой пробел ревью в `TestTerminalRunPauseResumeQuit`: проверь, что checkpoint после паузы имеет стадию агента и `a.txt` отсутствует до `R`. Сейчас тест проходит и тогда, когда пауза попала на границу complete после 5-секундного сна фейка.
- Раздели модельные проверки по сценариям: Starting без prompt, Starting с пришедшим prompt, гонка `CancelStart` → Pause; в каждом утверждай, что `StopNow` не вызван.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: `s` и `q` в одном write приходят до prompt, поэтому условие `m.pendingPrompt == nil` не должно отменять `CancelStart`. Поведение q/Ctrl+C, диалог выхода и путь паузы не меняй; `stopAfterSignal` не переписывай, общий код выноси только если он действительно общий. Фейковому контроллеру понадобится отдельный признак «`CancelStart` ещё не Finished», иначе тест не докажет ожидание (по образцу `pauseFinishes`). Не проверяй терминал по тексту статуса: diff-снимок содержит только хвосты строк; оракул — модельный тест, у PTY — код выхода и файлы.
Depends: P052.2.3, P052.2.5
Commit: P052.2.12: stop from the start confirmation
Handoff:
- stopAndExit for StateStarting now answers an already-delivered prompt with a refusal, then calls CancelStart regardless of m.pendingPrompt; on a CancelStart error it falls back to Pause only when the controller state reads Running, and the exit still goes through the existing exitAfterRun poll path (no second loop, no os.Exit).
- fakeController gained cancelNoFinish (a cancelled start stays live until the test calls finish) and cancelToRunning (CancelStart loses the race to MarkRunning): use them to observe 'no quit before Finished' and the Pause fallback.
- The start dialog consumes keys itself (handleDialogKey has no q branch for dialogStart), so a prompt that arrives after q replaces the exit dialog with the start dialog; the only model state with dialogExit + pendingPrompt is reached through requestQuit directly, which is what TestQuitFromStartDeclinesTheArrivedPrompt drives.
- TestTerminalRunPauseResumeQuit now asserts the pause checkpoint has stage agent and that a.txt is absent before R, so a pause that slipped onto the complete boundary fails the test.
- New PTY regression test TestTerminalQuitFromStartPrompt writes "sq\r" in one write and asserts exit 0, no run.lock, no checkpoint.json, no scenario counter, no a.txt, and restored alt screen/cursor/echo.
- Unverified: The race where a start confirmation arrives between q and Enter is not exercised at the PTY level; the CancelStart-lost-to-MarkRunning fallback is only proven by the fake controller, so a real runner transition remains for live acceptance (P052.3.3).
- Unverified: The new exit path and its terminal restoration are tested on darwin only; Windows behavior stays compile-checked, as the terminal tests are darwin/linux.

### [x] P052.2.13 Keep the outcome notice on screen
Read:
- internal/tui/view.go:L55-95
- internal/tui/view.go:L575-635
- internal/tui/view.go:L700-745
- internal/tui/model.go:L120-165
- internal/tui/app_test.go:L620-760
- internal/tui/app_test.go:L900-945
Do:
- Воспроизведи модельным тестом на отрендеренном экране, а не на `statusLine()`: закрытие P001 при непустом Queue даёт notice с путём архива, а `Model.String()` на 120 и 80 колонках его обрезает; то же с «run finished: context canceled» после Stop now — виден только хвост технической части.
- Выведи notice отдельной строкой над статусной: перенос по ширине существующим `wrapLine`, не больше двух строк, тело панели не меньше одной строки. Статусная строка оставляет технические части (state/run/lock/queue/selected/focus/activity), без notice.
- Убери notice из `statusLine()` и не дублируй его в дереве и в теле панели; пустой notice не занимает строку.
- Перепиши модельные проверки с `statusLine()` на отрендеренный экран так, чтобы они падали при обрезке: путь архива при 80 и 120 колонках, «run finished: context canceled», notice паузы, notice отказа lock, отсутствие «reloading» после загрузки.
- Проверь 40x12 и resize со 120 на 80: notice либо видим, либо экран «Terminal too small» — без паники и без выхода за ширину.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: `padLine`/`truncate` режут справа — notice в конце статусной строки не спасти приоритетом внутри неё. PTY-снимок Bubble Tea содержит только изменённые хвосты строк, поэтому оракул — `Model.String()` при заданных width/height, а не `terminal_test.go`. Не выводи notice в тело панели и не делай его вечным: новое действие по-прежнему заменяет текст (P052.2.9). Строку клавиш не трогай.
Depends: P052.2.9
Commit: P052.2.13: keep the outcome notice on screen
Handoff:
- Model.render() now builds notice lines via Model.noticeLines(width) and puts them between the footer separator and the dim status line; statusLine() no longer mentions m.notice.
- noticeLines wraps with wrapLine, caps at two lines and marks a capped notice by truncating the joined remainder with '…'; an empty notice returns nil and adds no line.
- Footer/body arithmetic drops notice lines only if the body would fall below one line; at minHeight 10 a two-line notice still leaves three body lines.
- New tests: TestStopNowNoticeRendersOnScreen ('run finished: context canceled' at 120 and 80 columns) and TestNoticeFitsSmallAndResizedScreens (120->80 resize, 40x12 line width/line count, two-line cap). Existing close/activate/failure/pause/reload checks now assert on Model.String(); the close test also asserts statusLine() no longer carries the archive path.
- Unverified: No PTY-level check at 40x12 or after a live resize: the notice layout is asserted only against Model.String(), so a real terminal's line wrapping on resize (and Bubble Tea's partial redraw after shrinking height) is not exercised.

### [x] P052.2.14 Journal a controlled stop as an action
Read:
- internal/runner/run.go:L315-345
- internal/runner/run.go:L770-790
- internal/runner/run.go:L1490-1525
- internal/runner/events.go:L120-175
- internal/runner/control.go:L325-345
- internal/runner/pause_test.go:L140-195
- internal/runner/pause_test.go:L738-765
- internal/tui/execute.go:L160-200
- internal/tui/execute.go:L230-250
Do:
- Воспроизведи: контролируемый прогон, Stop now во время вызова агента. Сейчас `unit_end.status=error`, `run_end.reason=context canceled`, `exit_code=2`, хотя хост рапортует Finished, а UI пишет «run finished: context canceled».
- Классифицируй управляемую остановку так же, как паузу в P052.1.8/P052.1.9: для хоста в `StateStopping` с отменённым ctx — `unit_end.status: stopped` для юнита, прерванного остановкой, и `run_end` с `reason: stopped`, `exit_code: 0`, без поля `paused`; юнит, дошедший до commit, остаётся `done`.
- Обычный сигнал/Ctrl-C без контролируемого хоста сохраняет прежние `error`/`context canceled`/код сигнала; `Stop after unit` остаётся успешным завершением после юнита.
- В TUI не показывай `stopped` как ошибку: `run_end` с `reason: stopped` не попадает в `runErr`, статус-строка называет остановку действием («run stopped»), а не «run finished: context canceled».
- Покрой в `pause_test.go` по образцу `assertPausedJournals`: хелпер сверяет unit_end/run_end для StopNow и отсутствие поля `paused`; сценарий с отменой родителя без хоста по-прежнему даёт error/2; Stop after unit — done и обычный run_end. В `app_test.go` — notice после Stop now без текста ошибки.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/tui/`
Traps: не переиспользуй внутренние коды 7/2 и не добавляй новые — классификация идёт через состояние хоста и ctx, как у паузы. Не считай Stop after unit остановкой: он не отменяет ctx, файл stop проверяется между юнитами. Поле `paused` в run_end не добавляй вне паузы (P052.1.9). Обычный прогон без хоста идёт теми же ветками — не сломай его журнал.
Depends: P052.1.8, P052.2.12
Commit: P052.2.14: journal a controlled stop as an action
Handoff:
- The stop classification lives in two deferred emitters: runWithOptions' run_end defer clears (code, runErr) and writes reason stopped/exit_code 0, while runUnit's unit_end defer overrides any non-done status to stopped. Both read stopCancellation(ctx), so the host must still be StateStopping when execute returns; a future change that moves the state transition earlier would lose the classification.
- The TUI reads the stop from the run_end reason, not from runner state: Model.runStopped is reset in startRun and consulted in handleFinished and when ignoring controller.Err(). Any new run-start path must reset it or a later genuine failure would render as 'run stopped'.
- assertStoppedJournals, like assertPausedJournals, expects exactly one unit_end and one run_end across all event files; scenarios with Resume (two events.jsonl) need the last-file variant noted in P052.1.8.
- Unverified: Stop now during an auto-replan or close operation is not reclassified by pauseOutcome; its deferred replan/close event can still carry an error result while run_end says stopped — no test covers that window
- Unverified: WatchKeys/signal paths and the real TUI stop transition are only covered by fake controllers here; the live Pi/Codex PTY acceptance (P052.3.3) does not exercise this journal shape
- Unverified: Stop now landing in pending-commit recovery may still run writeFailure and set totals.stop before the deferred reclassification; the journal is corrected, but no test asserts that path's output

### [x] P052.2.15 Keep the details scroll inside the pane
Read:
- internal/tui/keys.go:L172-240
- internal/tui/view.go:L501-570
- internal/tui/model.go:L118-132
- internal/tui/app_test.go:L335-360
Do:
- Воспроизведи модельным тестом на `Model.String()`: в `writeTestProject` выбери юнит без активности (`P001.1.2`), открой Details и нажми `down` больше, чем строк в карточке, при 80x24. `detailTop` растёт мимо `maxTop`, `visibleDetailLines` отдаёт пустой срез, и панель Details показывает только заголовок — тело пустое, хотя прокрутка должна упереться в последнюю страницу.
- Сохраняй результат клампа в модели: в `handleDetailsKey` вызов `m.clampDetailTop()` (keys.go:233) только вычисляет новое значение, а `m` остаётся прежним; после серии `down`/`pgdown` смещение обязано остаться в `[0, maxTop]`.
- Не допускай смещения больше видимого окна: `visibleDetailLines` не должна получать offset, при котором в панели не остаётся ни одной строки контента. Расчёт высоты здесь не дублируй — его сводит в один помощник P052.2.16.
- Покрой рендером: после `down` × N и `pgdown` панель Details не пуста и содержит текст карточки; после `up`/`pgup` прежние строки возвращаются; `esc` по-прежнему возвращает фокус в дерево.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: `end`/`G` в Details без оверлея не обрабатывается, а оверлеи diff/verify клампят себя сами (keys.go:203) — их не переписывай. Высотой (`bodyHeight()`, notice-строки, заголовок панели) занимается P052.2.16: здесь чинится только потерянный результат клампа. Не проверяй прокрутку по `statusLine()`: оракул — `Model.String()`.
Depends: P052.2.13
Commit: P052.2.15: clamp the details scroll offset
Handoff:
- Model.maxDetailTop(lines) in internal/tui/view.go is now the single scroll-bound helper; clampDetailTop and visibleDetailLines both use it, so P052.2.16 only needs to change what height it gets, not the call sites.
- TestDetailScrollStopsAtTheLastPage in internal/tui/app_test.go is the scroll oracle for Details; its fixture (P001.1.2 at 80x24) yields maxTop=1, so a height change in P052.2.16 will shift these expectations.
- Overlays (diff/verify) keep their own clamping in handleDetailsKey and were not touched; only the non-overlay branch changed.
- Unverified: The last line of the scrolled page is still clipped because maxDetailTop subtracts bodyHeight() while the pane spends a line on its title; the test asserts the Handoff heading rather than the final bullet — the rendered pane height is P052.2.16's scope, not checked here.

### [x] P052.2.16 Fit panes to the rendered body
Read:
- internal/tui/view.go:L62-140
- internal/tui/view.go:L255-320
- internal/tui/view.go:L501-570
- internal/tui/activity.go:L1210-1246
- internal/tui/keys.go:L145-170
- internal/tui/app_test.go:L245-283
- internal/tui/activity_test.go:L322-360
Do:
- Воспроизведи модельными тестами на `Model.String()`. (а) Дерево выше панели: фикстура с тремя фазами по 12 юнитов, 120x40, `end` выбирает последний юнит, но строки с маркером `>` выбранного юнита в отрисованном экране нет — `treeLines` считает строки по `bodyHeight()`, а `pane` тратит одну строку на заголовок. (б) Раскрытый юнит (`enter`) с длинной историей и follow: последняя строка тела новейшей записи срезана — `syncActivityScroll` ставит `len(detailLines)-bodyHeight()` тем же завышенным значением. (в) То же с непустым notice (текст ошибки прогона): `render` вычитает notice-строки из тела, а `bodyHeight()` — нет, поэтому срезано ещё на строки notice.
- Сведи геометрию к одному источнику: `bodyHeight()` возвращает фактически отрисованное тело (заголовок, notice-строки, разделитель, статус и строка клавиш уже вычтены), высота контента панели — `bodyHeight()` минус заголовок панели; `render` вычисляет тело тем же помощником. `treeLines`, `syncActivityScroll`, `clampDetailTop` и страничные перемещения (`pgup`/`pgdown`) используют высоту контента, а не константу.
- Не давай `pane` обрезать вычисленный контент: выбранная `end` строка дерева и тело новейшей записи при follow обязаны быть на экране; при пустом notice смещение не меняется.
- Покрой 80x24 и 120x40 с notice и без, а также 40x12 с notice: экран либо содержит выбранную строку и новейшее тело, либо «Terminal too small»; без паники и без строк шире терминала.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: правило P052.2.13 — notice не больше двух строк и тело не меньше одной — не меняй; `pane`/truncate и оверлеи не трогай. Не дублируй расчёт высоты в `treeLines` и `syncActivityScroll`: второй расчёт снова разойдётся с notice и заголовком панели. Оракул — `Model.String()`; PTY-снимок Bubble Tea содержит только изменённые хвосты строк. Тесты клампа из P052.2.15 не переписывай.
Depends: P052.2.15
Commit: P052.2.16: fit panes to the rendered body
Handoff:
- Pane geometry now has two levels: bodyHeight() is the rendered panel height (header/footer/notice already subtracted), paneContentHeight() is bodyHeight()-1 for the panel title. Scroll and page code must use paneContentHeight(); bodyHeight() is only for whole-body views (message/help/dialog).
- fitNotice/bodyHeightFor in view.go are shared by render and bodyHeight(), so any notice change automatically resizes tree/detail scroll windows; do not reintroduce a height constant at a call site.
- TestTreeAndDetailsFitTheRenderedBody is the screen oracle for this geometry; TestDetailScrollStopsAtTheLastPage still derives its maxTop dynamically and passes with the new -1 height.
- Unverified: Real alt-screen Bubble Tea rendering was not exercised: the oracle is Model.String() at fixed sizes, so a live terminal's own wrapping/resize behaviour at these geometries is not checked.

### [x] P052.2.17 Decide a finished block from the reloaded plan
Read:
- internal/tui/execute.go:L160-259
- internal/tui/model.go:L211-330
- internal/tui/app_test.go:L865-1001
- internal/tui/terminal_test.go:L466-540
- internal/runner/run.go:L1320-1350
Do:
- Воспроизведи модельным тестом с заданным порядком: прогон стартует, `[!]` с `Handoff: - Blocked (human): …` появляется в файле плана во время прогона (как его пишет `docflow block` из `run.go`), затем контроллер завершается `StateBlocked`. Сейчас `handleFinished` читает `m.snapshot`, загруженный до блокировки: `blockedInfo()` не находит `!`, `dialogHuman` не открывается, notice — «the run stopped on a block». В живой PTY это воспроизводилось не в каждом прогоне, поэтому тест сам задаёт порядок «блок записан → Finished → перезагрузка».
- Решай исход блокировки по снимку, прочитанному после остановки прогона: снимок, актуальный до `unit_end`/`run_end`, для этого решения не годится. Перезагрузка уже запрошена тем же `pollController` — используй её результат (отложенное решение в `applySnapshot`/`snapshotMsg`), а не второй загрузчик и не синхронное чтение плана в `handleFinished`.
- Сохрани P052.2.9: перезагрузка не затирает notice исхода. После неё для `needs: human` открывается `dialogHuman` с unit и reason из Handoff, для `needs: plan` — notice с причиной, для блока без Handoff — прежнее сообщение; диалог открывается один раз.
- Если перезагрузка после блокировки упала, notice объясняет ошибку и что план остался заблокирован, вместо пустого экрана или повторного «stopped on a block».
- Добавь терминальный сценарий на `writeRunnableProject`: фиктивный агент отдаёт результат `status: blocked` с `blocker: human`; дождись текста диалога Continue и проверь `[!]` в файле плана. Клавиши unblock не нажимай — это путь P052.2.10, оракул диалога остаётся модельным.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: не открывай диалог из `blockedInfo()` и не отвечай на prompt human автоматически. PTY-снимок содержит только изменённые хвосты строк: в терминальном тесте жди текст диалога через `waitFor`, а не строку статуса. Загрузку после блока не отменяй ради порядка событий: `unit_end` и так требует перезагрузки.
Depends: P052.2.3, P052.2.9
Commit: P052.2.17: decide a finished block from the reloaded plan
Handoff:
- Block resolution is deferred through Model.blockedPending: handleFinished only marks it, applySnapshot consumes it after the reload, and a failed load keeps it so the r retry still opens the dialog.
- Model tests that finish a run as StateBlocked must use the new pollLoad helper (poll + applyLoad); the old poll-only path leaves the dialog unopened by design.
- The terminal block scenario uses writeRunnableProjectAgent with writeBlockedProject; the fake agent returns structured_output status blocked/blocker human and the runner writes [!] via cycle.Self.
- Unverified: The failed-reload notice is exercised with an injected loader error, not a real malformed plan file on disk.

### [x] P052.2.18 Report the reason of a pre-start failure
Read:
- internal/runner/run.go:L288-300
- internal/runner/run.go:L326-345
- internal/runner/helpers.go:L409-455
- internal/runner/control.go:L217-260
- internal/tui/control.go:L96-148
- internal/tui/execute.go:L160-259
- internal/runner/run_test.go:L599-645
Do:
- Воспроизведи: план с падающим предусловием — `token` с командой `echo no-token >&2; exit 3` — затем `s` и подтверждение старта. Сейчас причина уходит в `Progress`, который контролируемый прогон направляет в `io.Discard`; `runPrerequisitesWithTimeout` возвращает `EXIT_FORMAT`, `Control.startRun` превращает это в «runner: run exited with code 2», `Host.Err()` пуст, и TUI пишет «run failed: the run failed».
- Доведи причину отказа, случившегося до первого события прогона, до интерактивного хоста: ошибка/поле хоста или событие только для хоста (`Publish`), но `handleFinished` обязан получить текст. Несколько упавших предусловий — все строки, а не первая.
- Не меняй журнал и обычный прогон: `run_start` остаётся первым событием, вывод `docflow run`, коды выхода и parity-сценарии прежние. Отказ подтверждения старта остаётся случаем P052.2.19.
- Покрой на уровне раннера: контролируемый прогон с падающим предусловием отдаёт хосту текст причины; в TUI фейковый контроллер с этой причиной даёт notice с ней и без «the run failed». Существующие `TestRunStartPrerequisitesAndPermissions` и `TestRunFailureNoticeSurvivesReload` продолжают проходить.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/tui/`
Traps: `runWithOptions` создаёт `totals.events` только после предусловий, поэтому `run_end` для этого отказа невозможен без перестановки порядка — порядок журнала ради сообщения не меняй. Progress в интерактивном режиме не включай: хост не владеет stdout. Новый код выхода не заводи.
Depends: P052.2.3
Commit: P052.2.18: report a pre-start failure reason
Handoff:
- A pre-start refusal reaches the TUI as a host-only runner.Event{Type: "start_failure", Message: <all failure lines joined by newline>}; a new unit that adds another pre-start refusal (skills warning error, selection/config error) must publish the same event or the TUI still falls back to 'the run failed'.
- runPrerequisites and runPrerequisitesWithTimeout now return (int, []string); any future caller must take both values.
- Unverified: The multi-line prerequisite reason is capped at two footer lines by fitNotice; the runner test asserts the full message and the TUI test a single line, so truncation of several failed prerequisites is not checked on a rendered screen.
- Unverified: No PTY/terminal test runs a failing prerequisite through the real host; the reason path is covered only with the fake controller and the runner-level Control test.

### [x] P052.2.19 Report a declined start as a cancelled run
Read:
- internal/runner/control.go:L120-165
- internal/runner/control.go:L217-260
- internal/tui/execute.go:L160-259
- internal/tui/execute.go:L274-297
- internal/tui/execute.go:L480-526
- internal/tui/app_test.go:L768-826
Do:
- Воспроизведи: стартовый диалог, `esc`/`n` → `answerPrompt(false)`; `Control.startRun` возвращает nil без `MarkRunning`, `Start` переводит состояние в `StateFinished`, и `handleFinished` заменяет notice «start declined; nothing ran» на «run finished». То же происходит после `CancelStart` из q-пути P052.2.12: «cancelling the start; nothing runs» тоже затирается.
- Отличай отменённый старт от завершённого прогона: хост сообщает, что работа не начиналась (отдельное состояние или признак отмены), а TUI сохраняет notice отказа и не пишет «run finished». Отмена не становится Paused: checkpoint не пишется, Resume не предлагается.
- Сохрани исходы: после отказа `s` снова запускает (существующий сценарий), lock не взят, unit и план не меняются, повторный запуск создаёт новый контроллер; принятый старт по-прежнему даёт Running и Finished.
- Покрой модельным тестом обе отмены — отказ в диалоге и `CancelStart`; проверь notice, состояние хоста и отсутствие checkpoint. Терминальные тесты P052.2.12 не переписывай: их оракул — код выхода и файлы.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/tui/`
Traps: не переиспользуй `StatePaused` и не добавляй внутренний код выхода — отмена старта отличается состоянием хоста. `TestStartDialogShowsPreconditionsAndDeclineRunsNothing` ожидает `StateFinished` после отказа: обнови ожидание вместе с новым состоянием, но не удаляй проверку «повторный s работает».
Depends: P052.2.3, P052.2.12
Commit: P052.2.19: report a declined start as cancelled
Handoff:
- runner.StateCancelled is a new terminal host state: Start accepts it for a fresh run, runIsActive does not treat it as live, and TUI handleFinished keeps the pre-set notice instead of writing 'run finished'.
- Control startCancelled is set in two places — startRun calls MarkStartCancelled when the confirmation is declined, and CancelStart sets it directly; the Start goroutine classifies the flag ahead of the Stopping branch, so a cancelled start cannot read as Finished even when execute returns context.Canceled.
- A cancelled start writes no checkpoint and does not become Paused; runner tests check both via Checkpointed and ReadCheckpoint.
- Unverified: No terminal (PTY) scenario declines the start dialog against the real runner: the model test fakes the cancelled state and the runner test covers StartRun's decline, but the joined TUI dialogs → real Control path for a decline is not exercised end to end.

### [x] P052.2.20 Keep the open dialog's keys on a late prompt
Read:
- internal/tui/execute.go:L274-297
- internal/tui/execute.go:L335-405
- internal/tui/execute.go:L407-431
- internal/tui/execute.go:L480-526
- internal/tui/keys.go:L47-60
- internal/tui/app_test.go:L1414-1580
Do:
- Воспроизведи модельным тестом: `q` при `StateStarting` открывает `dialogExit`, затем приходит `confirm_start`; сейчас `handlePrompt` перезаписывает `m.dialog = dialogStart`, и Enter в диалоге выхода подтверждает старт вместо остановки и выхода. `TestQuitFromStartDeclinesTheArrivedPrompt` этот порядок обходит, вызывая `requestQuit` после prompt, — сценарий с поздним prompt не покрыт.
- Приходящий prompt не меняет открытый диалог: пока `m.dialog != dialogNone`, prompt только запоминается в `pendingPrompt`, а его диалог показывается после закрытия текущего. Enter в диалоге выхода по-прежнему значит «остановить и выйти», `esc` — «остаться».
- При выходе ожидающий prompt получает отказ, затем вызывается `CancelStart`, как в P052.2.12. При отказе от выхода стартовый диалог появляется и его можно подтвердить или отклонить: prompt не остаётся без ответа, прогон не зависает до отмены ctx.
- Покрой оба порядка — prompt до `q` (существующий тест) и prompt после `q` (новый): Enter не подтверждает старт, `esc` показывает стартовый диалог, ответ на prompt уходит ровно один раз.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: prompt без ответа держит горутину `Ask` до отмены ctx — «просто проигнорировать» нельзя. Не отвечай на prompt в обход `handlePrompt`. Порядок диалогов проверяй моделью: PTY-снимок не годится как оракул.
Depends: P052.2.3, P052.2.12
Commit: P052.2.20: keep an open dialog on a late prompt
Handoff:
- handlePrompt now only stores a prompt when m.dialog != dialogNone; showPendingPrompt opens its dialog and is called from every dialog-close path (exit Esc, human Enter/Esc). A new dialog-close path must call it too or the prompt stays unanswered.
- Unverified: Only confirm_start late prompts are tested; a non-start prompt (kind default) arriving over dialogHuman/exit follows the same showPendingPrompt path but has no model test.

### [x] P052.2.21 Accept shifted characters in text input
Read:
- internal/tui/keys.go:L449-502
- internal/tui/execute.go:L335-405
- internal/tui/app_test.go:L157-195
- internal/tui/app_test.go:L332-363
- internal/tui/app_test.go:L865-1001
Do:
- Воспроизведи: `tea.KeyPressMsg` с `Text: "A"` и `Mod: tea.ModShift` — так Bubble Tea v2 отдаёт Shift+A. В поиске (`handleSearchKey`) и в заметке unblock (`handleDialogKey`) текст принимается только при `msg.Mod == 0`, поэтому верхний регистр теряется; тестовый `keyMsg` собирает `Mod: 0` и дефект не видит.
- Принимай печатаемый текст с shift и caps lock, но не с ctrl/alt/meta: `ctrl+u` остаётся очисткой строки, alt/meta не попадают в запрос и заметку. Раскладку и escape-последовательности не переписывай.
- Расширь тестовый помощник так, чтобы он собирал shift-нажатие (отдельный `shiftKeyMsg` или параметр), не ломая существующие вызовы; добавь проверки: запрос `TUI` фильтрует дерево и историю (поиск регистронезависимый), заметка `Continue P001.` уходит в unblock целиком.
- Проверь, что управляющие клавиши не изменились: `/`, esc, enter, backspace, `ctrl+u`, `?`, `f`, `G`, `shift+tab`.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: `msg.String()` для Shift+A возвращает сам символ, а не «shift+a», поэтому не переводи обработку на `Keystroke()` и не ломай клавиши с настоящим shift. Проверяй принятый текст и отфильтрованные строки, а не только notice.
Depends: P052.2.2, P052.2.3
Commit: P052.2.21: accept shifted characters in text input
Handoff:
- Text input now goes through typedText in internal/tui/keys.go: it accepts msg.Text unless a command modifier (ctrl/alt/meta/hyper/super) is held, so shift and caps lock arrive as typed characters; use it for any new text field instead of checking msg.Mod == 0.
- Tests build shifted events with shiftKeyMsg in internal/tui/app_test.go; it sets Text to the shifted character and Mod to tea.ModShift, matching Bubble Tea v2 delivery.
- The plan file docs/plans/P052-terminal-control.md was already modified before this unit; this unit did not edit or commit it.
- Unverified: Shifted and caps-lock input is asserted only with synthetic KeyPressMsg values; a live terminal (Kitty protocol / macOS console) typing into search and the unblock note is not exercised by Verify.

### [ ] P052.2.22 Keep follow and the cursor per selected unit
Read:
- internal/tui/model.go:L140-170
- internal/tui/model.go:L577-608
- internal/tui/keys.go:L239-295
- internal/tui/activity.go:L1210-1253
- internal/tui/view.go:L360-380
- internal/tui/activity_test.go:L322-360
- internal/tui/app_test.go:L207-217
Do:
- Воспроизведи модельным тестом на `Model.String()`: у юнита A длинная история, читатель нажал `up` (follow выключен), затем выбирает юнита B. Сейчас `setCursor` сбрасывает `activityCursor` в 0, `followActivity` оставляет выключенным и не вызывает `syncActivityScroll`: история B показывает первую запись и статус «paused», хотя читатель её ещё не читал.
- Считай паузу чтения принадлежащей истории выбранного юнита: при переходе на другой юнит follow включается, курсор и прокрутка встают на новейшую запись; возврат к прежнему юниту не показывает чужую позицию. Ручная прокрутка дерева и поиск по истории не меняются.
- Вызови существующий `syncActivityScroll` из пути смены выбора, а не дублируй расчёт смещения; `activityMatches` пересчитай для нового юнита.
- Покрой: pause на A → выбор B через дерево → новейшая запись B и `following` на экране; затем `up` на B снова выключает follow и новые события B не двигают вид; смена выделения на юнит без истории не паникует и не показывает историю A.
Verify: `go test -timeout 20m ./internal/tui/`
Traps: `followActivity` — общий флаг модели; карту позиций по юнитам не заводи (это отдельное решение, здесь его нет). `detailTop` уже сбрасывается при смене строки — не дублируй сброс. Оракул — `Model.String()`, не `syncActivityCursor` в отрыве от отрисовки.
Depends: P052.2.2, P052.2.16
Commit: P052.2.22: keep follow per selected unit

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
Depends: P052.2.1, P052.2.2, P052.2.3, P052.2.5, P052.2.6, P052.2.7, P052.2.8, P052.2.9, P052.2.10, P052.2.11, P052.2.12, P052.2.13, P052.2.14, P052.2.15, P052.2.16, P052.2.17, P052.2.18, P052.2.19, P052.2.20, P052.2.21, P052.2.22
Role: review
Commit: P052.2.4: review terminal controls
Handoff:
- Blocked (plan): Фаза ожидает: любой выход/закрытие TUI с живым прогоном останавливает процессы и пишет checkpoint, живая история позволяет открыть результат текущего вызова, логи читаются при запуске из подкаталога, а lifecycle-действия отказывают под живым чужим раннером. Реально: SIGTERM/SIGHUP оставляет detached агента и lock без checkpoint, страница результата недоступна до конца вызова, относительные пути логов не разрешаются от корня, lock проверяется только при загрузке. План должен развести это на юниты с воспроизведениями выше.
- Blocked (plan): The phase Do expects accurate statuses, confirmations and no state change under a foreign runner. Real: the plan reload erases every success/failure notice, so the P052.2.3 close message with the archive path (and the run failure/pause reason) is never readable; and the human-block Continue runs 'unblock' through cycle.Self without refreshLock, modifying the plan under a live foreign runner, then overwrites the refused start with 'starting the run again'. Both reproduced deterministically with overlay tests; the plan needs two units with those scenarios.
- Blocked (plan): Three product defects and one status question remain after the Phase 2 diff; all were reproduced on this Mac with the built CLI and a fake codex stream in a real PTY, not only with model tests. 1. The live activity page cannot resolve the current call log. internal/runner/run.go:1028 emits the attempt event with the log path before CallAgent, but run.go:1042 emits the stream link only after CallAgent returns; internal/tui/activity.go records log links only from stream, and ActivityHistory.Log falls back to lastLog. In a real run (fake agent: codex item.started, sleep, item.completed; 120x40 PTY) expanding the running tool shows 'result: call log unknown: the stream link has not arrived'; on a resumed or fallback attempt the same lookup returns the previous attempt's log. P052.2.6 tests feed streamEvent before the activity, an order the runner never produces. Fix: record the attempt event's log (or publish the link before the call), reject the lastLog fallback when attempt > 0, and test the real event order. 2. q + Enter while the run is StateStarting does nothing. internal/tui/execute.go:487 calls Control.StopNow for Starting, but StopNow requires Running and the error is ignored; the start prompt arrives after the exit confirmation. Repro: write s, q, Enter in one PTY write; the process stays alive, opens the start dialog, keeps run.lock and writes no checkpoint. The signal path already uses CancelStart in stopAfterSignal; the q path must do the same and wait for Finished without a checkpoint, with a state-enforcing test. 3. Outcome notices are clipped off the only status line. statusLine appends the notice after state/run/lock/queue/selected/focus/activity, and view.padLine(truncate(...)) cuts it. At 120x40, closing P001 while P002 remains in Queue renders '...the archive needs your Outcome text:...' without the archive path required by P052.2.3's Do; Stop now renders the final status ending 'run fi...', hiding 'run finished: context canceled'; at 80 columns the notice is off the line. Model tests assert statusLine() and PTY tests avoid the notice text. Fix: prioritize, wrap or overlay the notice and assert the rendered screen at 80/120 columns. 4. Stop now is journaled as a runner failure: the same run writes unit_end status 'error' and run_end exit_code 2 with reason 'context canceled' while the host reports Finished and the UI says 'run finished: context canceled'. A controlled Stop now is an action, not a failure; the plan needs a classification like P052.1.8 did for Pause, or an explicit decision that this journal is intended. Test gap from the Test changes review: TestTerminalRunPauseResumeQuit only checks that checkpoint.json exists; it passes when Pause lands at the complete boundary after the fake agent's 5s sleep. Assert the checkpoint stage is the agent stage and a.txt is absent before R, otherwise the terminal test does not prove the current call was interrupted.
- Blocked (plan): Phase 2's navigation does not survive a full pane: the details clamp result is discarded (keys.go:233), so scrolling past the end blanks Details, and the pane content height is one line smaller than bodyHeight() (plus notice lines), so End hides the selected tree row and follow hides the newest activity body. Both are reproduced with overlay key-driven tests against Model.String(); existing tests never fill a pane past its height. The plan needs units for these two navigation defects with the scenarios above; no file was edited.
- Blocked (plan): The plan expects accurate start/block/failure outcomes and working confirmations. Real: the human-block Continue dialog is skipped when the block is written during the run because handleFinished reads the pre-block snapshot (reproduced in a real PTY, 2 of 4 runs); a pre-event failure shows only 'run failed: the run failed' and loses the prerequisite reason (real PTY); shifted characters are dropped in search and the unblock note (Bubble Tea decoder shape); a declined start ends as 'run finished'; a late start prompt turns Enter in the exit dialog into a start confirmation; paused follow/cursor leaks to the next selected unit. The plan needs units for these behaviors with the scenarios above.

## Phase 3: Model setup recovery and delivery

### [ ] P052.3.1 Configure models for the run
Read:
- internal/agents/agents.go:L548-605
- internal/agents/agents.go:L701-745
- internal/roster/resolve.go:L14-100
- internal/roster/resolve_chain.go
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
- Создай docs/design/tui.md с действующим контрактом состояния, паузы, конфигурации, восстановления, выхода по сигналу, живой страницы результата текущего вызова, разрешения путей логов от корня checkout и проверки lock в момент действия; добавь его в индекс docs/design/README.md. Причины решений оставь в Decisions плана до архивации, в теме сошлись на этот план.
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

### [ ] P052.4.2 Accept live Pi and Codex execution
Read:
- tools/tui-live.sh
- internal/tui/acceptance_test.go
- docs/design/tui.md
- docs/usage.md#автозапуск
Do:
- До длительной работы проверь предусловия: этот Mac, действующая авторизация Codex и подписка opencode-go; запасной маршрут zai через Pi требует доступа к zai. Если нужен вход или доступ человека, сразу верни blocked с blocker human; не заменяй живую проверку фиктивной.
- Запусти bash tools/tui-live.sh --run для pi | opencode-go/deepseek-v4.1-flash | low и codex | gpt-6-luna | high: отдельные маленькие временные Go-проекты с двумя детерминированно проверяемыми юнитами, --attempts 1, авто-replan выключен, запасные исполнители для этого сценария выключены.
- В каждом прогоне через настоящий терминальный драйвер раскрой задачу, проверь выбранную модель и фактический процесс, дождись сообщения и живого tool-call, нажми Pause во время активного вызова, дождись Paused и подтверди отсутствие работающего агента/инструмента.
- Продолжи, проверь успешный Verify и commit первого юнита, Stop after unit до запуска следующего, выйди и открой TUI вновь, затем заверши второй юнит. Проверь результат независимым тестом, историю действий, фактические назначения и чистое дерево временного проекта.
- Соблюдай общий лимит Prerequisites: до четырёх реальных вызовов Codex и до 300 секунд на вызов любого агента; маршруты Pi расходуют подписку opencode-go. Не подменяй реальный отказ фикстурой и не расходуй подписку ради искусственного лимита; если бюджет исчерпан, верни blocked с blocker human и точным оставшимся сценарием.
- Проверь квитанцию --check на текущей сборке, перечисли проверенный macOS/архитектуру и реальные исходы в Handoff; при любом изменении сборки повторная живая проверка необходима и остаётся в пределах разрешённого бюджета.
- Закрой оставшиеся Unverified/Test changes/Replan changed check по правилам приёмки. Малую ошибку исправь в этом юните с повторной проверкой; крупная требует blocked с blocker plan, без удаления живой приёмки.
Verify: `bash tools/tui-live.sh --check && go test -timeout 20m ./...`
Depends: P052.1.1, P052.1.2, P052.1.3, P052.1.4, P052.2.1, P052.2.2, P052.2.3, P052.2.4, P052.3.1, P052.3.2, P052.3.3, P052.3.4, P052.3.5, P052.4.1
Role: acceptance
Commit: P052.4.2: accept live terminal execution
