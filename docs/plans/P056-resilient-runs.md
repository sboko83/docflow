---
id: P056
title: Make autonomous runs self-healing
type: feature
status: draft
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
останавливает прогон один раз до первого агента, а replan не объясняет
падение нагрузкой без воспроизведения.

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

- Команда gate — POSIX sh через цикл и отдельную группу процессов
  (`cycle/verify.go`, `cycle/shell.go`): таймаут, закрытый stdin, полный
  вывод в лог, хвост в ошибку. Второй путь запуска не создавай.
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

## Phase 1: Defects stop the run only once

### [ ] P056.1.1 Check the plan gate before the first unit
Read:
- internal/runner/run.go:L344-435
- internal/cycle/verify.go:L90-200
- internal/cycle/shell.go
- internal/runner/run_test.go:L40-180
- internal/runner/start_test.go:L90-115
Do:
- На первой итерации `runLoop` (`planID == ""`), после старта плана и квоты, до первого `prompt`: если у плана непустой `gate:`, дерево чистое и выбранный юнит `[ ]`, выполни gate один раз теми же механизмами, что `verify` (shell цикла, отдельная группа процессов, таймаут, полный вывод в лог).
- При падении останови прогон кодом 2, напечатай последние 40 строк, имя упавшего пакета или теста и путь полного лога `<git-dir>/docflow/run/baseline.<n>.out`; ни один агент не вызывается.
- При успехе напечатай одну строку `baseline: gate passed`; при грязном дереве, `[~]`-юните или незавершённом commit — строку `baseline: skipped, the tree has work in progress` и продолжай без проверки.
- Внутри одного вызова повторный replan или следующий юнит baseline не запускает.
- Расширь `internal/runner/run_test.go`: красный baseline не вызывает агента и даёт код 2 с именем пакета; зелёный baseline выполняется один раз на два юнита; план без `gate:` молчит; `[~]`-юнит или грязное дерево пропускают проверку.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/cycle/`
Traps: не дублируй запуск команды — вынеси общий запуск проверки плана в экспортируемый помощник цикла, если `runVerifyCommand` недоступен из раннера; лог baseline не должен совпадать с glob `uid.*.out`.
Commit: P056.1.1: check the gate before the first unit

### [ ] P056.1.2 Turn auto-replan on by default
Read:
- internal/runner/replan.go:L254-290
- internal/runner/chain.go:L15-62
- internal/runner/run.go:L420-440
- internal/runner/replan_test.go:L240-330
- internal/lint/lint.go:L653-675
- skills/bsv-docflow-plan/SKILL.md#ask-before-units
- docs/design/format.md:L195-240
- docs/design/runner.md:L125-180
Do:
- В `runReplan` убери условие `plan.Meta["replan"] == ""`: в executor-режиме план-блок всегда идёт в replan. `--replans 0` остаётся единственным отказом и действует на запуск.
- В `replanExecutorChain`: если `replan:` пуст, построй цепочку из `executor:` и `fallback:` плана тем же резолвером, что для юнита без `Role:`; ростер `run` не читает. Непустой `replan:` перекрывает маршрут, как сейчас.
- Строка `<id> replan -> agent | model | effort`, событие `replan` и `--dry-run` показывают фактический маршрут, включая дефолтный.
- В lint не добавляй требование `replan:` у review/acceptance-планов (в раннем черновике P056 оно предлагалось); сохрани только существующее «`replan:` требует executor-режима».
- В `skills/bsv-docflow-plan/SKILL.md` убери вопрос про авто-replan из «Ask before units»: replan включён по умолчанию, `replan:` пишется только по явной просьбе о маршруте.
- Обнови `docs/design/runner.md#авто-replan` и `docs/design/format.md`: без ключа replan идёт по цепочке `executor:`, отказ — `--replans 0`; добавь строку в `CHANGELOG.md` под `[Unreleased]` по bsv-changelog, потому что поведение `docflow run` меняется.
- Тесты: план без `replan:` авто-репланится маршрутом из `executor:`; `--replans 0` останавливает; явный `replan:` выигрывает; legacy-план без executor не репланится; lint не требует ключ.
Verify: `go test -timeout 20m ./internal/runner/ ./internal/lint/ ./cmd/docflow/`
Traps: не читай ростер в `run` — маршрут по умолчанию только из шапки плана. Ключ `replan:` не удаляй из формата: меняется его роль, а не наличие. Лимиты replan и `check_replan` не трогай.
Commit: P056.1.2: turn auto-replan on by default

### [ ] P056.1.3 Make replan prove the failure
Read:
- internal/runner/replan_rules.go
- internal/runner/replan.go:L30-46
- internal/runner/verify_guard_acceptance_test.go:L390-425
- skills/bsv-docflow-plan/SKILL.md#replan
- docs/design/runner.md#авто-replan
Do:
- Добавь в `replanRules` правило: если триггер называет падающий тест, пакет или команду, выполни её до правки плана и приведи результат в `## Decisions`; падение не считается нагрузкой, флейком или «другой сессией», пока не воспроизведено не меньше трёх раз (`-count=3`, если инструмент это умеет).
- То же правило для обратного случая: если названный тест сейчас зелёный, снимать блок как флейк нельзя — «не воспроизвелось» не доказательство. Либо добавь юнит, который воспроизводит падение на HEAD и чинит его, либо ответь `blocked` с `blocker: human`; зелёный прогон в момент replan не отменяет красный прогон юнита.
- Перенумеруй правила, сохранив все существующие; текст должен оставаться полным без скилла.
- Повтори то же правило в разделе Replan скилла `skills/bsv-docflow-plan/SKILL.md` и в `docs/design/runner.md#авто-replan`.
- Расширь тест промпта: фраза есть в `replanRules` и в собранном промпте replan.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: скилл ставится отдельно и может отсутствовать у агента; источник правды для раннера — `replanRules`.
Commit: P056.1.3: make replan prove the failure

### [ ] P056.1.5 Allow a bounded repair of a failing gate
Read:
- internal/agents/agents.go:L175-215
- internal/runner/helpers.go:L346-420
- internal/runner/run_test.go:L290-310
- docs/design/cycle.md#цикл-юнита
- skills/bsv-docflow-run/SKILL.md
Do:
- В `RunnerCycle` пункт 1: разреши ограниченный ремонт проверки вне `Do:` — падает `gate:` или `Verify:` на реальной поломке (просроченная фикстура, неверное ожидание, устаревший тест) → сделай минимальную правку, не ослабляя проверку (не skip, не удаление теста, не размытый ассерт), и назови её в Handoff.
- Среда (sandbox, права, отсутствующий инструмент) и крупный ремонт остаются блоком: их не «чинят» правкой теста.
- Повтори правило в `skills/bsv-docflow-run/SKILL.md` (цикл юнита) и в `docs/design/cycle.md#цикл-юнита`: правка теста видна в `Test changes` и в ревью, поэтому ремонт разрешён, а ослабление — дефект.
- Тест в `internal/runner/run_test.go`: в промпте есть фраза про ремонт и запрет ослаблять проверку.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: правило не отменяет блок для среды — падения process group в sandbox из P052 не повод править тест. `--skip` остаётся аварийным и с согласием человека, а не заменой ремонта.
Commit: P056.1.5: allow a bounded gate repair

### [ ] P056.1.4 Review the resilience changes
Read:
- internal/runner/run.go:L344-435
- internal/runner/replan.go:L254-290
- internal/runner/chain.go:L15-62
- internal/agents/agents.go:L175-215
- internal/runner/run_test.go:L40-180
Do:
- Проверь Phase diff по Goal, Constraints, Decisions и Do юнитов фазы: baseline не обходит вызов агента, не повторяет verify и не стреляет на `[~]`-юните; авто-replan по умолчанию берёт маршрут из `executor:` и не читает ростер; `--replans 0` останавливает; правило ремонта не разрешает ослаблять проверку и не отменяет блок для среды; правило replan не отменяет существующие и не требует скилла.
- Разбери каждый пункт Test changes: обоснованное изменение допускается, ослабленная ради зелёного Verify проверка — дефект.
- Ничего не редактируй; при дефекте верни blocked с blocker plan и конкретным сценарием, иначе зафиксируй вывод в Handoff.
Verify: `go test -timeout 20m ./...`
Depends: P056.1.1, P056.1.2, P056.1.3, P056.1.5
Role: review
Commit: P056.1.4: review resilient run changes

## Phase 2: Acceptance

### [ ] P056.2.1 Accept a hands-off run
Read:
- internal/runner/run_test.go:L40-180
- internal/runner/start_test.go:L90-115
- internal/lint/lint_test.go:L260-340
- internal/runner/replan_test.go:L240-330
- tools/parity/run.sh
Do:
- Добавь сценарии на реальном бинарнике в фикстуре раннера (временный репозиторий, фиктивный CLI): (а) план без `replan:` с review-юнитом — review блокируется, replan идёт маршрутом из `executor:` и добавляет fix-юнит, прогон завершает второй юнит без человека; тот же сценарий с `--replans 0` останавливается; (б) план с красным на чистом дереве `gate:` останавливает `docflow run` кодом 2 до вызова агента и называет пакет; (в) юнит с реальной поломкой чужого теста чинит его в том же коммите, а `Test changes` показывает правку; ослабленный тест (skip или удаление) виден в `Test changes`; (г) промпт replan содержит правило воспроизведения, промпт юнита — правило ограниченного ремонта; (д) после прогона дерево чистое.
- Повтори инцидент P052: gate, покрасневший между двумя прогонами, останавливает следующий прогон один раз на старте с путём baseline-лога, а не внутри юнита и не дважды.
- Прогони полный gate, сборку и parity.
Verify: `go test -timeout 20m ./... && go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow`
Depends: P056.1.1, P056.1.2, P056.1.3, P056.1.4, P056.1.5
Role: acceptance
Commit: P056.2.1: accept hands-off run
