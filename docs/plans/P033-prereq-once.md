---
id: P033
title: One-time prerequisites
type: feature
status: active
created: 2026-10-24
executor: codex | gpt-6-luna | max
permissions: auto
---
## Goal

Пункт `## Prerequisites` с меткой `(once)` проверяется только до старта
плана; продолженный `docflow run` его пропускает. Пункт без метки, как и
раньше, проверяется при каждом запуске.

## Context

- docs/design/format.md#план
- docs/design/runner.md:L140-158

## Constraints

- Go, только стандартная библиотека.
- Версия формата остаётся `docflow: 2`: метка необязательна, новый ключ не
  вводится.
- Формат, `lint`, раннер и скилл планирования меняются вместе.
- `docs/` — по-русски; код, комментарии, CHANGELOG — по-английски.

## Traps

- Разбор пункта есть в двух местах: `format.PlanPrerequisites`
  (`prerequisiteParts`) и регулярка `prereqRE` в `internal/lint/lint.go`.
  Метка должна пониматься обоими, иначе `lint` и раннер разойдутся.
- `(once)` стоит после закрывающего бэктика команды: в нынешнем разборе
  хвост пункта после `:` должен кончаться бэктиком, поэтому команду надо
  отделить от метки до поиска команды.

## Decisions

- Метка `(once)` в конце пункта, а не правило «все предусловия только до
  старта» — пункты окружения (вход, устройство, инструменты) должны
  проверяться при каждом продолжении прогона; базовая проверка кода,
  которую план сам временно ломает (parity красный до RMG P008.3.6), — нет —
  отвергнуты «всё только до старта» (пропадает проверка окружения при
  продолжении) и «провал при продолжении — предупреждение» (сбой окружения
  тоже становится предупреждением).
- План начат, если хотя бы один юнит не `[ ]` и не `[-]` — `[~]`/`[!]`
  тоже значат, что `prompt` уже был и дерево могло измениться — отвергнуто
  «есть `[x]`» (прерванный `[~]` на первом юните снова проверял бы базу).
- Старый CLI видит `` `cmd` (once)`` как пункт без команды: показывает его
  в подтверждении старта и команду не выполняет, `lint` не падает —
  безопасная деградация без смены версии формата.

## Phase 1: Marker

### [x] P033.1.1 Add the (once) prerequisite marker
Read:
- internal/format/doc.go:L734-783
- internal/format/doc_test.go:L306-320
- internal/lint/lint.go:L40-50
- internal/lint/lint.go:L563-600
- internal/lint/lint_test.go:L271-320
- internal/runner/helpers.go:L239-320
- internal/runner/helpers.go:L372-395
- internal/runner/run.go:L160-172
- internal/runner/run_test.go:L581-625
- docs/design/format.md:L184-195
- docs/design/runner.md:L140-158
- docs/usage.md:L346-358
- skills/bsv-docflow-plan/SKILL.md:L93-103
- CHANGELOG.md:L1-20
Do:
- `format.Prerequisite`: добавить поле `Once bool`. `PlanPrerequisites`
  снимает суффикс `(once)` (после пробела, в конце текста пункта) до разбора
  команды; формы `- текст: `cmd` (once)` и `- текст (once)`.
- Добавить `format.PlanStarted(plan) bool`: true, если хотя бы один юнит не
  `[ ]` и не `[-]`.
- `lint`: `prereqRE` и проверка пустой команды принимают суффикс `(once)`;
  `` - x: `` (once)`` по-прежнему ошибка «empty command in backticks».
- Раннер: на начатом плане пункт `Once` не выполняется и не попадает в
  подтверждение `continue? [y/N]`; вместо этого строка
  `prerequisite skipped (once, plan started): <текст>`. Если после пропуска
  не осталось пунктов без команды и режим не `full` — вопрос не задаётся.
- Dry-run (`prerequisiteLines`) печатает пункт с суффиксом ` (once)`.
- Тесты: разбор обеих форм в `doc_test.go`, `lint` с меткой в
  `lint_test.go`, в `run_test.go` — начатый план пропускает падающий
  `(once)` и проходит, не начатый — код 2.
- Доки: определение метки и «план начат» в `docs/design/format.md` (пункт
  `## Prerequisites`), поведение старта в `docs/design/runner.md` и
  `docs/usage.md`.
- `skills/bsv-docflow-plan/SKILL.md`, пункт «What only a human provides»:
  проверку состояния кода, которое план сам меняет, помечать `(once)`.
- `CHANGELOG.md`, `## [Unreleased]` → `### Added`: метка `(once)`.
Verify: `go test ./...`
Commit: P033.1.1: add one-time prerequisite marker
Handoff:
- Метка распознаётся только как суффикс ` (once)` в самом конце пункта; `PlanStarted` считает план начатым при любом юните не `[ ]`/`[-]`, включая `[!]`.
- Блок юнита был из-за параллельного коммита релизного скрипта (3bd3aa7) в то же дерево; работа юнита не менялась.

## Phase 2: Acceptance

### [ ] P033.2.1 Accept one-time prerequisites end to end
Read:
- internal/runner/run_test.go:L1-170
- docs/design/format.md#план
Do:
- В `internal/runner` добавить тест `TestRunOncePrerequisitesAcceptance` на
  `newRunFixture` (настоящий `docflow run`, фейковый агент; образец настройки предусловий — `TestRunStartPrerequisitesAndPermissions`) — по сценарию на
  каждое утверждение Goal:
  - план с юнитом `[x]`, падающим `(once)` и проходящим обычным пунктом:
    код 0, агент вызван, в выводе `prerequisite skipped (once, plan started)`;
  - тот же план, все юниты `[ ]`: код 2, `prerequisite failed`, агент не
    вызван;
  - план с юнитом `[x]` и падающим пунктом без метки: код 2;
  - план с юнитом `[x]` и пунктом без команды `(once)`, stdin не TTY, без
    `--yes`: подтверждения нет, прогон идёт;
  - `run --dry-run --all` печатает пункт с ` (once)`.
- `docflow lint` на плане с меткой в обеих формах — без ошибок.
- Каждый `Unverified:` из Handoff P033.1.1 закрыть тестом или оставить в
  Handoff этого юнита.
Verify: `go test ./...`
Traps: Падающий сценарий с малой правкой чинить в этом же юните; иначе —
`blocked` с причиной.
Depends: P033.1.1
Executor: claude | claude-opus-5-5 | high
Commit: P033.2.1: accept one-time prerequisites
