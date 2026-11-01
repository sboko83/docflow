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
человека: красный gate на чистом дереве останавливает прогон один раз до
первого агента и называет пакет, план без `replan:` с review-юнитом не
проходит lint, а replan не объясняет падающий тест нагрузкой, не
воспроизведя его.

## Context

Инцидент P052: за десять часов четыре блока подряд. Три из них — P052.1.2:
юнит не влез в сессию, затем дважды предсуществующий красный тест
`internal/roster` (фикстура с датой `2026-11-01`), причём второй replan
списал падение на нагрузку сессии и потратил сессию на повторную
диагностику. Четвёртый — review нашёл настоящие дефекты, и прогон встал,
потому что в плане был выключен авто-replan. Почему это случилось и какие
решения приняты — [P052](P052-terminal-control.md).

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
- Линт-правило действует только в executor-режиме; планы без
  `executor:`/`Executor:` не меняются.
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
  юнитов, а не историю прогонов.
- `replanRules` — Go-константа, её текст сверяют дословно; правило и его
  тест меняются одним юнитом.
- `## Prerequisites` и baseline — разные вещи: пункт без `(once)`
  повторяется на каждом вызове, а с `(once)` пропускается на уже начатом
  плане.

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
- A plan in executor mode with open review/acceptance units and no `replan:`
  is a lint error, not a warning — the combination deterministically stops the
  run on the first defect review finds, and the repository already carries
  more warnings than anyone reads. Rejected a warning.
- The reproduction rule lives in the runner's replan prompt and is repeated in
  the planning skill — the run must work without the skill installed.
  Rejected skill-only wording.
- Four fixes need no new plan-level mechanism: the baseline, the lint rule and
  the replan rule are independent changes of existing steps. Rejected a new
  "run policy" section in the format.

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

### [ ] P056.1.2 Require replan for review plans
Read:
- internal/lint/lint.go:L591-700
- internal/lint/lint.go:L920-1010
- internal/lint/lint_test.go:L260-340
- internal/lint/lint_test.go:L960-1044
- docs/design/format.md:L195-240
- docs/design/runner.md:L125-180
Do:
- В `lintPlan`: если план в executor-режиме (шапка `executor:` или `Executor:` открытого юнита) и хотя бы один открытый юнит имеет `Role: review` или `Role: acceptance`, а `replan:` в шапке пуст — ошибка на строке шапки: `plan has review/acceptance units but no replan: the run stops on the first defect it finds; add replan: or remove the review unit`.
- Добавь в `internal/lint/lint_test.go`: executor-план с review-юнитом без `replan:` — ошибка; с `replan:` — нет; legacy-план без executor и review-юнит — нет; единственный review-юнит `[x]` — нет.
- Обнови `docs/design/format.md` (ключ `replan:`) и `docs/design/runner.md#авто-replan`: правило и причина — review-дефекты это ожидаемая работа, без самолечения прогон встаёт на первом.
Verify: `go test -timeout 20m ./internal/lint/ ./cmd/docflow/`
Traps: lint идёт и по draft-планам — правило должно срабатывать до активации, а не только на active.
Commit: P056.1.2: require replan for review plans

### [ ] P056.1.3 Make replan prove the failure
Read:
- internal/runner/replan_rules.go
- internal/runner/replan.go:L30-46
- internal/runner/verify_guard_acceptance_test.go:L390-425
- skills/bsv-docflow-plan/SKILL.md#replan
- docs/design/runner.md#авто-replan
Do:
- Добавь в `replanRules` правило: если триггер называет падающий тест, пакет или команду, выполни её до правки плана и приведи результат в `## Decisions`; падение не считается нагрузкой, флейком или «другой сессией», пока не воспроизведено не меньше трёх раз (`-count=3`, если инструмент это умеет).
- Перенумеруй правила, сохранив все существующие; текст должен оставаться полным без скилла.
- Повтори то же правило в разделе Replan скилла `skills/bsv-docflow-plan/SKILL.md` и в `docs/design/runner.md#авто-replan`.
- Расширь тест промпта: фраза есть в `replanRules` и в собранном промпте replan.
Verify: `go test -timeout 20m ./internal/runner/`
Traps: скилл ставится отдельно и может отсутствовать у агента; источник правды для раннера — `replanRules`.
Commit: P056.1.3: make replan prove the failure

### [ ] P056.1.4 Review the resilience changes
Read:
- internal/runner/run.go:L344-435
- internal/lint/lint.go:L591-700
- internal/runner/replan_rules.go
- internal/runner/run_test.go:L40-180
Do:
- Проверь Phase diff по Goal, Constraints, Decisions и Do юнитов фазы: baseline не обходит вызов агента, не повторяет verify и не стреляет на `[~]`-юните; линт-ошибка не задевает legacy-планы; правило replan не отменяет существующие и не требует скилла.
- Разбери каждый пункт Test changes: обоснованное изменение допускается, ослабленная ради зелёного Verify проверка — дефект.
- Ничего не редактируй; при дефекте верни blocked с blocker plan и конкретным сценарием, иначе зафиксируй вывод в Handoff.
Verify: `go test -timeout 20m ./...`
Depends: P056.1.1, P056.1.2, P056.1.3
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
- Добавь сценарии на реальном бинарнике в фикстуре раннера (временный репозиторий, фиктивный CLI): (а) план с review-юнитом без `replan:` падает `docflow lint`, с `replan:` активируется; (б) план с красным на чистом дереве `gate:` останавливает `docflow run` кодом 2 до вызова агента и называет пакет; (в) review-юнит отвечает blocked, replan добавляет fix-юнит, прогон продолжается и завершает второй юнит без человека; (г) промпт replan содержит правило воспроизведения; (д) после прогона дерево чистое.
- Повтори инцидент P052: gate, покрасневший между двумя прогонами, останавливает следующий прогон один раз на старте с путём baseline-лога, а не внутри юнита и не дважды.
- Прогони полный gate, сборку и parity.
Verify: `go test -timeout 20m ./... && go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow`
Depends: P056.1.1, P056.1.2, P056.1.3, P056.1.4
Role: acceptance
Commit: P056.2.1: accept hands-off run
