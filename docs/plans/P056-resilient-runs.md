---
id: P056
title: Make autonomous runs self-healing
type: feature
status: active
created: 2026-11-01
executor: pi | opencode-go/deepseek-v4.1-flash | low
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | medium
review: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6.1-sol | medium
acceptance: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6.1-sol | high
permissions: auto
replan: pi | opencode-go/deepseek-v4.1-flash | max; codex | gpt-6.1-sol | medium
timeout: 3600
gate: go test -timeout 20m ./...
---
## Goal

План с review/acceptance юнитами доводится `docflow run` до конца без
человека: auto-replan включён по умолчанию, юнит может отремонтировать
реальную чужую поломку, не ослабляя проверку, красный gate на чистом дереве
останавливает прогон один раз до первого агента, replan не объясняет
падение нагрузкой без воспроизведения, а ревью, не сошедшееся за три
блока, останавливается на человеке.

## Context

Инцидент P052: четыре остановки. Две из них — один и тот же просроченный
тест `internal/roster` (фикстура с датой `2026-11-01`), причём второй replan
списал падение на нагрузку сессии и потратил ещё сессию на повторную
диагностику. Первая — 12-минутный выход агента с просьбой пересмотреть
объём: в логе `P052.1.2.1.out` 745 с, зелёный verify и явное «с блокировкой
на пересмотр объёма»; это не контекст и не сбой, хотя в Decisions плана
записано «не влез в одну сессию». Четвёртая — review нашёл настоящие
дефекты, прогон встал из-за выключенного авто-replan; после включения
replan сам добавил юниты 1.8 и 1.9, и прогон продолжился без человека.
Почему это случилось и какие решения приняты —
[P052](P052-terminal-control.md).

- docs/design/runner.md#цикл-на-юнит
- docs/design/runner.md#авто-replan
- docs/design/format.md#план
- docs/design/cycle.md#цикл-юнита

## Constraints

- Формат остаётся `docflow: 3`, коды выхода `run` и коды проверок не
  меняются. Baseline — один шаг старта и одна строка вывода, не новый
  режим прогона.
- Baseline выполняется не больше одного раза за вызов `docflow run`, только
  на чистом дереве и только перед первым `[ ]` юнитом. `[~]`-юнит,
  незавершённый commit и грязное дерево пропускают его одной строкой.
- Линт-правило про `replan:` не добавляется: ключ остаётся только выбором
  маршрута, а не выключателем. Авто-replan включён по умолчанию в
  executor-режиме; `--replans 0` — единственный отказ и только на запуск.
- Юнит может отремонтировать падающую проверку вне `Do:`, только если
  поломка реальная (фикстура, ожидание, устаревший тест), проверка не
  ослабляется и ремонт назван в Handoff. Среда и крупный ремонт — блок.
- Правила replan остаются полными без установленного скилла: раннер не
  зависит от `bsv-docflow-plan`.
- Никаких новых зависимостей, CGO=0, одна поставка; существующие строки
  прогона и события сохраняются.

## Traps

- Команда gate — POSIX sh цикла (`cycle/shell.go`) в отдельной группе
  процессов (`runVerifyCommand` в `cycle/verify.go`): stdin закрыт, полный
  вывод в лог, хвост 40 строк в ошибку. Своего таймаута у verify нет — gate
  ограничивает себя сам (`go test -timeout`). Второй путь запуска не
  создавай.
- `runVerifyCommand` сам ловит SIGINT/SIGTERM и перевыбрасывает сигнал —
  процесс умирает мимо `run_end` и снятия блокировки. Из раннера команду
  запускай через вариант с `ctx`: Ctrl-C простого прогона и Pause/Stop
  интерактивного хоста отменяют контекст, группа процессов убивается, прогон
  кончается обычным путём.
- `RunnerCycle` закреплён SHA-256 в `TestStrictRunnerSchema`
  (`internal/agents/agents_test.go`): правка текста цикла меняет хэш в том
  же юните, и `Verify:` такого юнита включает `./internal/agents/`.
- Ключ `replan:` читает не только `runReplan`: стартовый замер квоты
  (`startQuota`), проверка скиллов (`agents.PlanAgents`), событие `config`
  (`eventConfigPlan`) и `--dry-run` (`chainLines`). Маршрут по умолчанию
  должен дойти до каждого, иначе квота и скиллы replan-агента молча
  выпадают.
- Лог baseline не должен попадать под glob `uid.*.out`: этот glob считают
  тесты call-логов и восстановление checkpoint.
- `lint` не знает о прогоне: правило читает шапку плана и `Role:` открытых
  юнитов, а не историю прогонов. Требования `replan:` у review-планов нет —
  не добавляй его.
- Маршрут replan по умолчанию берётся из `executor:`/`fallback:` плана, а не
  из ростера: `run`/`lint` ростер не читают, это правило сохраняется.
- `replanRules` — Go-константа, её текст сверяют дословно; правило и его
  тест меняются одним юнитом.
- `## Prerequisites` и baseline — разные вещи: пункт без `(once)`
  повторяется на каждом вызове, а с `(once)` пропускается на уже начатом
  плане.
- Baseline ловит красный gate до прогона, но не тест, который краснеет
  посреди юнита (дата, внешний сервис). Этот класс закрывает правило
  replan про невоспроизведённое падение, а не baseline.

## Decisions

- Run the baseline gate at the start of a `docflow run` invocation instead of
  writing it as a `## Prerequisites` item — `(once)` is skipped as soon as the
  plan has started, which is exactly the P052 case at 09:01, while an item
  without `(once)` reruns on every resume over the half-written tree of a
  paused unit. Rejected both prerequisite forms.
- A red baseline stops with code 2 and never triggers replan — a failure that
  exists before any change is a repository condition, not plan drift, and
  auto-adding a fix unit would widen the plan's Goal silently. Rejected
  auto-repair.
- Auto-replan is on by default in executor mode; `replan:` only selects the
  route and `--replans 0` is the only opt-out, at run time — a plan-class block
  is normal work, not a decision the planner has to make, and a survey question
  nobody can answer with "yes" is noise. Rejected the earlier draft that made
  the key mandatory through lint.
- A unit may repair a failing check outside its `Do:` when the failure is real,
  the repair does not weaken the check and Handoff names it — a one-line
  expired-fixture fix is cheaper than a stop, and `Test changes` already makes
  a weakened check visible to review and acceptance. Rejected both extremes:
  never touch a foreign test (two stops in P052) and free rein (an agent can
  silence a check).
- The reproduction rule lives in the runner's replan prompt and is repeated in
  the planning skill — the run must work without the skill installed.
  Rejected skill-only wording.
- A green run at replan time does not lift a block whose trigger names a
  failing gate — the roster fixture passed at 09:01 and failed at 10:12 when
  its date boundary passed, and treating the green run as proof of "load"
  sent P052.1.2 into the same failure an hour later. The replan adds a unit
  that reproduces the failure at HEAD or stops as human. Rejected calling an
  unreproduced failure a flake.
- Four fixes need no new plan-level mechanism: the baseline, the default
  replan, the bounded repair and the replan rule are independent changes of
  existing steps. Rejected a new "run policy" section in the format.
- The P052.1.2 first block was a 12-minute self-assessment, not a context
  overflow: the run log shows a 745-second attempt, a green verify and the
  agent's own "rethink the scope" message, while the six-hour gap was between
  runs. Rejected the "unit did not fit one session" wording as a diagnosis;
  the unit was large, but nothing measured a limit.

- A review or acceptance unit that blocks a third time stops the run for a
  human instead of another replan — the budget counts `Blocked (plan)` items
  in the unit's Handoff, so it survives restarts, while the per-call limit of
  two replans let P052.2.4 block ten times over several runs. Default-on
  replan would otherwise make that loop the default for every plan. Rejected
  shipping P056 first and leaving the Backlog item open (its record said to
  decide before P056), and the other Backlog options (blocking scope,
  incremental re-review, stats signal) — they stay in Backlog.

## Phase 1: Defects stop the run only once

### [ ] P056.1.1 Check the plan gate before the first unit
Read:
- internal/runner/run.go:L403-520
- internal/runner/run.go:L296-330
- internal/cycle/verify.go:L90-225
- internal/cycle/shell.go
- internal/runner/helpers.go:L205-245
- internal/runner/run_test.go:L40-240
Do:
- На первой итерации `runLoop` (`planID == ""`), после старта плана и квоты, до первого `runUnit`: если у плана непустой `gate:`, выбран юнит (`sel.Kind == "unit"`) со статусом `[ ]`, нет незавершённого commit (`cycle.CommitPending`) и `git status --porcelain` пуст, выполни gate один раз тем же запуском, что `verify`.
- Вынеси запуск в экспортируемый помощник цикла с `ctx` (например `cycle.RunPlanCommand(ctx, root, command) (string, int)`): отмена контекста убивает группу процессов; `runVerifyCommand` остаётся обёрткой с обработкой сигналов поверх него. Пока идёт gate, показывай спиннер `baseline`.
- При падении: полный вывод в `<git-dir>/docflow/run/baseline.<n>.out` (номер — `nextCallLog` с префиксом `baseline.`), напечатай последние 40 строк вывода (у `go test` в них строка `FAIL <пакет>`) и строку `baseline: gate failed (exit N): <команда>` с путём лога; `totals.stop` — эта строка, чтобы её получили `run_end` и уведомление; код 2, ни один агент не вызывается. Имя пакета не разбирай: gate — произвольная команда.
- При успехе — одна строка `baseline: gate passed`; при грязном дереве, `[~]`-юните или незавершённом commit — одна строка `baseline: skipped, the tree has work in progress` и прогон идёт дальше. План без `gate:` и выбор `close` ничего не печатают.
- Внутри одного вызова следующий юнит и юнит после replan baseline не запускают.
- Расширь `internal/runner/run_test.go`: красный baseline не вызывает агента, даёт код 2, хвост с упавшим тестом и путь лога; зелёный baseline выполняется один раз на два юнита; план без `gate:` молчит; `[~]`-юнит и грязное дерево пропускают проверку.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/cycle/`
Traps: не дублируй запуск команды и не вызывай из раннера `runVerifyCommand` с его перевыбросом сигнала. Лог baseline не должен совпадать с glob `<uid>.*.out`: имя без id плана. Baseline не заменяет проверку юнита: `docflow verify` по-прежнему гоняет gate после работы.
Commit: P056.1.1: check the gate before the first unit

### [ ] P056.1.2 Turn auto-replan on by default
Read:
- internal/runner/replan.go:L254-270
- internal/runner/chain.go:L15-72
- internal/runner/run.go:L560-590
- internal/runner/run.go:L1697-1740
- internal/runner/events.go:L215-230
- internal/agents/agents.go:L780-805
- internal/agents/agents.go:L891-919
- internal/runner/replan_test.go:L240-330
- internal/lint/lint.go:L645-675
- skills/bsv-docflow-plan/SKILL.md:L60-125
- skills/bsv-docflow-plan/SKILL.md:L370-425
- skills/bsv-docflow-run/SKILL.md:L50-67
- docs/design/format.md:L185-215
- docs/design/runner.md:L125-180
- README.md:L95-106
Do:
- Маршрут по умолчанию — одна функция (например `agents.ReplanRoutes(plan)`): непустой `replan:` — его цепочка, как сейчас; иначе в executor-режиме — `executor:` шапки и `fallback:` (как `UnitRoleRoutes` для юнита без `Role:` и `Executor:`); иначе пусто. Ростер `run` не читает.
- `runReplan` вместо `plan.Meta["replan"] == ""` проверяет пустой маршрут по умолчанию: в executor-режиме план-блок всегда идёт в replan, `--replans 0` — единственный отказ и действует на запуск. Legacy-план и executor-план без `executor:` в шапке останавливаются прежней строкой.
- Той же функцией пользуются `replanExecutorChain`, `startQuota` (квота replan-маршрута), `agents.PlanAgents` (проверка скиллов) и `eventConfigPlan` (поле `replan` события `config`). `--dry-run` печатает `replan: … (default)`, когда ключа нет. Строка `<id> replan -> agent | model | effort` и событие `replan` показывают фактический маршрут.
- В lint не добавляй требование `replan:` (в раннем черновике P056 оно предлагалось); сохрани только существующее «`replan:` требует executor-режима».
- `skills/bsv-docflow-plan/SKILL.md`: убери вопрос про авто-replan из шага 6 Procedure и из «Ask before units», удали из шага 9 удаление строки `replan:`, перепиши примечание под Sample («without them … a plan-class block stops the run») и «Replan under the runner» — replan включён по умолчанию, `replan:` выбирает маршрут, отказ — `--replans 0`. `skills/bsv-docflow-run/SKILL.md`: «no `replan:` key» больше не стоп.
- Обнови `docs/design/runner.md#авто-replan` (в том числе «план без `replan:` — прежний стоп») и `docs/design/format.md` (`replan:`): без ключа replan идёт по цепочке `executor:` + `fallback:`. В `README.md` и `README.ru.md` абзац о демо replan больше не называет ключ `replan:` условием. Строка в `CHANGELOG.md` под `[Unreleased]` по bsv-changelog: поведение `docflow run` меняется.
- Тесты: план без `replan:` авто-репланится маршрутом из `executor:`; `--replans 0` останавливает; явный `replan:` выигрывает; legacy-план не репланится; `--dry-run` показывает маршрут по умолчанию; lint не требует ключ.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/agents/ ./internal/lint/ ./cmd/docflow/`
Traps: не читай ростер в `run` — маршрут по умолчанию только из шапки плана. Ключ `replan:` не удаляй из формата: меняется его роль, а не наличие. Лимиты replan и `check_replan` не трогай. Миграция 2 → 3 (`cycle/lifecycle.go`, «replan carries the fallback chain») касается только планов с ключом — не трогай.
Commit: P056.1.2: turn auto-replan on by default

### [ ] P056.1.3 Make replan prove the failure
Read:
- internal/runner/replan_rules.go
- internal/runner/replan.go:L30-46
- internal/runner/verify_guard_acceptance_test.go:L400-425
- skills/bsv-docflow-plan/SKILL.md:L382-430
- docs/design/runner.md#авто-replan
Do:
- Добавь в `replanRules` правило: если триггер называет падающий тест, пакет или команду, выполни её на HEAD до правки плана (несколько раз, `-count=3`, если инструмент это умеет) и запиши команду и результат в `## Decisions`.
- Красный результат — поломка: добавь юнит, который её чинит. Зелёный результат блок не снимает: падение юнита не объясняется нагрузкой, флейком или «другой сессией», «не воспроизвелось» — не доказательство. Тогда добавь юнит, который воспроизводит падение (условия юнита: дата, окружение, порядок тестов) и чинит его, либо ответь `blocked` с `blocker: human`.
- Перенумеруй правила, сохранив все существующие; текст должен оставаться полным без скилла.
- Повтори то же правило в разделе Replan скилла `skills/bsv-docflow-plan/SKILL.md` и в `docs/design/runner.md#авто-replan`.
- Тесты: фраза есть в `replanRules` и в собранном промпте replan; добавь её в фразы `TestVerifyGuardReplanRulesSkillParity`, чтобы скилл не потерял правило.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: скилл ставится отдельно и может отсутствовать у агента; источник правды для раннера — `replanRules`.
Commit: P056.1.3: make replan prove the failure

### [ ] P056.1.5 Allow a bounded repair of a failing gate
Read:
- internal/agents/agents.go:L174-225
- internal/agents/agents_test.go:L592-618
- internal/runner/helpers.go:L380-400
- docs/design/cycle.md#цикл-юнита
- skills/bsv-docflow-run/SKILL.md:L17-67
Do:
- В `RunnerCycle` пункт 2 (итерации с `Verify:` и полный `docflow verify`): разреши ограниченный ремонт проверки вне `Do:` — падает `gate:` или `Verify:` на реальной поломке (просроченная фикстура, неверное ожидание, устаревший тест) → сделай минимальную правку, не ослабляя проверку (не skip, не удаление теста, не размытый ассерт), и назови её в Handoff. Пункт 1 («ровно то, что говорит `Do:`») сошлись на это исключение, а не противоречь ему.
- Среда (sandbox, права, отсутствующий инструмент) и крупный ремонт остаются блоком: их не «чинят» правкой теста.
- Повтори правило в `skills/bsv-docflow-run/SKILL.md` (цикл юнита) и в `docs/design/cycle.md#цикл-юнита`: правка теста видна в `Test changes` и в ревью, поэтому ремонт разрешён, а ослабление — дефект.
- Обнови SHA-256 `RunnerCycle` в `TestStrictRunnerSchema` и добавь в его список фраз фразу про ремонт и запрет ослаблять проверку.
Verify: `go test -timeout 20m ./internal/agents/ ./internal/runner/`
Traps: правило не отменяет блок для среды — падения process group в sandbox из P052 не повод править тест. `--skip` остаётся аварийным и с согласием человека, а не заменой ремонта.
Commit: P056.1.5: allow a bounded gate repair

### [ ] P056.1.6 Stop a review that does not converge
Read:
- internal/runner/replan.go:L120-140
- internal/runner/replan.go:L254-290
- internal/cycle/verify.go:L574-660
- internal/runner/replan_test.go:L240-330
- docs/design/runner.md#авто-replan
- skills/bsv-docflow-plan/SKILL.md:L306-332
Do:
- В `runReplan` до `replanAllowed`: перечитай юнит из файла плана (`cycle.FindUnit` — `plan` из выбора не видит только что записанный блок); если это опорный юнит (`anchorRole` — `review` или `acceptance`) и в его Handoff не меньше трёх пунктов `Blocked (plan):`, replan не вызывается.
- Вместо него — строка `<id>: blocked N times, review budget spent; decide by hand` (жёлтым), `totals.stop` — она же, код 3; бюджет считается по Handoff плана и потому переживает перезапуски `docflow run`, в отличие от счётчиков вызова.
- Обычный юнит под бюджет не попадает; лимиты `--replans` и «2 на юнит» не меняются.
- Обнови `docs/design/runner.md#авто-replan` и раздел Review скилла `skills/bsv-docflow-plan/SKILL.md` (третий блок ревью — решение человека: править вручную или закрыть находки Handoff-ом); строка в `CHANGELOG.md` под `[Unreleased]`.
- В `docs/docflow.md` в записи Backlog «Ревью-юнит не сходится» отметь, что пункт (а) сделан в P056, а (б)–(г) остаются.
- Тесты в `internal/runner/replan_test.go`: review-юнит с двумя прежними `Blocked (plan)` в Handoff и новым блоком останавливается кодом 3 без вызова replan-агента; с одним прежним — репланится; обычный юнит с тремя блоками — репланится.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: не храни счётчик вне плана — именно счётчик вызова не остановил P052.2.4. Не меняй `check_replan` и формат пункта `Blocked (plan):`.
Commit: P056.1.6: stop a review that does not converge

### [ ] P056.1.4 Review the resilience changes
Read:
- internal/runner/run.go:L403-520
- internal/runner/replan.go:L254-270
- internal/runner/chain.go:L15-72
- internal/agents/agents.go:L174-225
- internal/runner/run_test.go:L40-240
Do:
- Проверь Phase diff по Goal, Constraints, Decisions и Do юнитов фазы: baseline идёт до вызова агента, один раз за вызов, не стреляет на `[~]`-юните и грязном дереве и прерывается Ctrl-C/Pause без перевыброса сигнала; авто-replan по умолчанию берёт маршрут из `executor:`/`fallback:` во всех местах, где читался `replan:` (replan, квота, скиллы, событие `config`, `--dry-run`), и не читает ростер; `--replans 0` останавливает; правило ремонта не разрешает ослаблять проверку и не отменяет блок для среды; правило replan не отменяет существующие и не требует скилла; бюджет ревью считается по Handoff плана, а не счётчиком вызова, и не задевает обычные юниты.
- Разбери каждый пункт Test changes: обоснованное изменение допускается, ослабленная ради зелёного Verify проверка — дефект.
- Ничего не редактируй; при дефекте верни blocked с blocker plan и конкретным сценарием, иначе зафиксируй вывод в Handoff.
Verify: `go test -timeout 20m ./...`
Depends: P056.1.1, P056.1.2, P056.1.3, P056.1.5, P056.1.6
Role: review
Commit: P056.1.4: review resilient run changes

## Phase 2: Acceptance

### [ ] P056.2.1 Accept a hands-off run
Read:
- internal/runner/run_test.go:L40-240
- internal/lint/lint_test.go:L271-330
- internal/runner/replan_test.go:L240-330
- tools/parity/run.sh
Do:
- Добавь сценарии на реальном бинарнике в фикстуре раннера (временный репозиторий, фиктивный CLI): (а) план без `replan:` с review-юнитом — review блокируется, replan идёт маршрутом из `executor:` и добавляет fix-юнит, прогон завершает второй юнит без человека; тот же сценарий с `--replans 0` останавливается; (б) план с красным на чистом дереве `gate:` останавливает `docflow run` кодом 2 до вызова агента, печатает хвост с упавшим тестом и путь `baseline.<n>.out`; (в) юнит с реальной поломкой чужого теста чинит его в том же коммите, а `Test changes` показывает правку; ослабленный тест (skip или удаление) виден в `Test changes`; (г) промпт replan содержит правило воспроизведения, промпт юнита — правило ограниченного ремонта; (д) review-юнит, блокирующийся третий раз, останавливает прогон кодом 3 без replan; (е) после прогона дерево чистое.
- Повтори инцидент P052: gate, покрасневший между двумя прогонами, останавливает следующий прогон один раз на старте с путём baseline-лога, а не внутри юнита и не дважды.
- Прогони полный gate, сборку и parity.
Verify: `go test -timeout 20m ./... && go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow`
Depends: P056.1.1, P056.1.2, P056.1.3, P056.1.4, P056.1.5, P056.1.6
Role: acceptance
Commit: P056.2.1: accept hands-off run
