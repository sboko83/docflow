---
id: P050
title: Verify guard against self-weakened checks
type: feature
status: active
created: 2026-10-28
executor: pi | strata/swift-1.5-iq3_s | high
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | high
review: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | medium
acceptance: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high
replan: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | medium
permissions: auto
timeout: 7200
gate: go build ./... && go vet ./... && go test ./internal/cycle/ ./internal/format/ ./internal/lint/
---
## Goal

Агент не может незаметно ослабить проверку собственной работы. Replan не
трогает опорные проверки — `Verify:`, `Do:` и `Role:` юнитов ролей review и
acceptance, — а каждую смену `Verify:` или пропуск `[-]` обычного юнита
docflow сам записывает в `## Decisions` плана, в сводку прогона и в поток
событий. Удалённые строки в существующих тестах, удалённые тест-файлы и
добавленные skip-маркеры отмечаются в коммите юнита (`Test changes:`), в
выводе `docflow commit` и прогона и попадают в промпты юнитов review и
acceptance.

## Context

Дыра 1 оценки 2026-10-28: отпечаток гарантирует, что закоммичено
проверенное дерево, но не то, что проверка осмысленна; исполнитель правит
тесты, replan переписывает `Verify:` открытых юнитов.

- docs/reviews/2026-10-28-agentic-review.md#дыры

## Constraints

- Новых ключей формата плана нет, версия формата (`docflow: 3`) не
  меняется: рост формата — слабое место из той же оценки.
- Поток событий остаётся `v: 1`: только новые поля, имена и смысл
  существующих не меняются.
- Правка тестов не запрещается — она бывает законной (рефакторинг,
  исправление неверного теста); задача — сделать её видимой ревью,
  приёмке и человеку.
- Шаблоны тест-файлов и skip-маркеров встроены в код, без конфига.

## Traps

- Replan законно снимает блок с юнита review или acceptance (`[!]` →
  `[ ]`/`[~]`) и добавляет юниты исправления в его `Depends:`: маркер
  статуса и `Depends:` у опорных юнитов меняться могут, неприкосновенны
  только `Verify:`, `Do:`, `Role:` и переход в `[-]`.
- Раннер берёт sha коммита первым словом stdout `docflow commit`
  (`firstWord`): новые строки вывода идут только после существующих.
- Тесты гоняются и на Windows (Git Bash): вывод git — с `/`, у фикстур
  возможны CRLF; `.gitattributes` репозитория это учитывает, временные
  репо фикстур — нет.
- `go test ./internal/runner/` целиком идёт ~7 минут на Windows: для
  итераций — `-run` по своим тестам.

## Decisions

- Опора и прозрачность вместо полного запрета: `gate:` (шапка неизменна
  для replan) и юниты review/acceptance — опорные проверки, их replan не
  трогает; смена `Verify:` обычного юнита разрешена, но записывается
  docflow, а не агентом — отвергнуто: стоп на человека при любой смене
  `Verify:` (убивает автономность на устаревших путях).
- Правки тестов помечаются, а не запрещаются — легитимный рефакторинг
  меняет тесты — отвергнуто: заморозка тест-файлов на юнит (новый ключ
  формата, ложные блоки).
- Сигнал правки тестов живёт в теле коммита юнита — git остаётся
  источником правды для ревью, приёмки и бенчмарка, работает и в
  интерактиве, и в раннере — отвергнуто: только поле события (теряется в
  интерактиве, локально в git-dir).

## Phase 1: Replan guard

### [ ] P050.1.1 Protect review and acceptance checks from replan
Read:
- internal/runner/replan.go:L1-130
- internal/runner/replan_rules.go
- internal/runner/replan_test.go:L78-128
- docs/design/runner.md#авто-replan
Do:
- В `checkReplan` (`internal/runner/replan.go`) для каждого юнита старого плана, который не `[x]`/`[-]` и у которого `Role:` равен `review` или `acceptance`, сравнить поля `Verify`, `Do` и `Role` старой и новой версии; любое отличие — проблема `unit <id> (<role>): <Field> changed`; новый статус `[-]` у такого юнита — проблема `unit <id> (<role>) skipped`.
- Маркер статуса в заголовке и `Depends:` у этих юнитов не проверять: replan снимает с них блок и добавляет юниты исправления.
- В `replanRules` (`internal/runner/replan_rules.go`) добавить в пункт 3 правило: `Verify`, `Do` и `Role` юнитов с `Role: review` или `Role: acceptance` не меняются, такие юниты не переводятся в `[-]`.
- Тесты в `TestCheckReplan`: смена `Verify` у acceptance — проблема; смена `Do` у review — проблема; `[-]` у acceptance — проблема; `[!]` → `[ ]` и новый id в `Depends:` у review — без проблем; смена `Verify` у юнита без роли — без проблем.
- В `docs/design/runner.md` раздела «Авто-replan» дописать в перечень проверок `check_replan` неприкосновенность опорных юнитов.
Verify: `go test ./internal/runner/ -run 'TestCheckReplan|TestReplanPrompt'`
Commit: P050.1.1: protect review and acceptance checks from replan

### [ ] P050.1.2 Record check changes made by replan
Read:
- internal/runner/replan.go:L130-540
- internal/runner/run.go:L40-70
- internal/runner/run.go:L529-560
- internal/runner/replan_test.go:L166-328
- docs/design/runner.md#поток-событий
Do:
- Добавить в `internal/runner/replan.go` функцию `replanCheckChanges(before, after string) []string`: для каждого юнита старого плана без роли review/acceptance и не `[x]`/`[-]` — пункт `<id> Verify: <старое> -> <новое>` при смене значения `Verify`, пункт `<id> skipped` при переходе в `[-]`.
- После успешных проверок replan и до коммита плана дописать в `## Decisions` файла плана по пункту `- Replan changed check: <пункт>` на каждое изменение; коммит replan содержит эти строки.
- Печатать каждое изменение строкой прогона `<uid> replan changed check: <пункт>` (жёлтым, как `unverified`), добавить в `runTotals` счётчик `checkChanges` и в сводку прогона `, N checks changed by replan` при N > 0.
- Событию `replan` добавить поле `check_changes` — массив строк-пунктов (пустой, если изменений нет).
- Тесты в `replan_test.go`: replan-агент меняет `Verify` обычного юнита → коммит replan содержит строку Decisions, вывод — строку изменения и итог сводки, событие `replan` — поле `check_changes`; replan без смены проверок — Decisions не тронут.
- В `docs/design/runner.md` описать запись изменений в разделе «Авто-replan» и поле `check_changes` в таблице раздела «Поток событий».
Verify: `go test ./internal/runner/ -run 'TestReplan|TestCheckReplan'`
Commit: P050.1.2: record check changes made by replan

### [ ] P050.1.3 Review replan guard
Read:
- internal/runner/replan.go:L1-130
- internal/runner/replan_rules.go
Do:
- Проверить дифф фазы из раздела `## Phase diff` промпта против Goal, Constraints, `## Decisions` плана и `Do:` юнитов P050.1.1 и P050.1.2; ничего не править.
- Отдельно проверить: может ли replan обойти защиту — удалить `Role:` у опорного юнита, заменить опорный юнит новым с другим id, перенести `Verify` в другое поле; найденный обход — дефект.
Verify: `go build ./... && go vet ./... && go test ./internal/cycle/ ./internal/format/ ./internal/lint/`
Role: review
Commit: P050.1.3: review replan guard

## Phase 2: Visible test changes

### [ ] P050.2.1 Mark weakened tests in unit commits
Read:
- internal/cycle/commit.go:L1-330
- docs/design/cycle.md#коммиты
Do:
- Новый файл `internal/cycle/testchanges.go`: экспортируемая `IsTestPath(path string) bool` — путь со слешами, тест-файл по имени (`*_test.go`, `test_*.py`, `*_test.py`, `*.test.*`, `*.spec.*`, `*Test.java`, `*Test.kt`, `*Tests.swift`, `*Test.swift`) или по каталогу пути (`test/`, `tests/`, `__tests__/`, `spec/`, `Tests/`, `androidTest/`).
- Там же `stagedTestChanges(root string) ([]string, error)`: по индексу против `HEAD` (`git diff --cached --numstat` и `git diff --cached -U0` по тест-файлам) пункты `<path>: -N lines` для тест-файла, который был в `HEAD` и потерял строки; `<path>: deleted` для удалённого тест-файла; `<path>: +skip` для добавленной строки со skip-маркером (`t.Skip(`, `@skip`, `pytest.mark.skip`, `@Ignore`, `@Disabled`, `XCTSkip`, `xit(`, `xdescribe(`, `it.skip(`, `describe.skip(`, `test.skip(`, `.only(`). Без `HEAD` — пустой список.
- В `commitRepo` после `git add -A` вызвать `stagedTestChanges`; при непустом списке дописать в тело коммита пустую строку, `Test changes:` и пункты `- <пункт>`; для вложенных репо из `repos:` — то же в коммите каждого репо.
- В выводе `Commit` после всех существующих строк печатать по строке `test change: <пункт>`, для вложенного репо — `test change: <repo>/<пункт>`.
- Тесты `internal/cycle/testchanges_test.go`: `IsTestPath` на таблице путей; коммит юнита с удалённой строкой теста, удалённым тест-файлом и добавленным `t.Skip(` — тело коммита и вывод содержат три пункта; добавление новых тестов и правка не-тест-файла — пунктов нет.
- В `docs/design/cycle.md` раздела «Коммиты» описать `Test changes:` в теле и строки `test change:` вывода.
Verify: `go test ./internal/cycle/`
Commit: P050.2.1: mark weakened tests in unit commits

### [ ] P050.2.2 Show test changes in the run
Read:
- internal/runner/run.go:L40-70
- internal/runner/run.go:L529-560
- internal/runner/run.go:L615-630
- internal/runner/run.go:L1050-1120
- internal/runner/run_test.go:L81-242
- docs/design/runner.md#вывод-прогона
- docs/design/runner.md#поток-событий
Do:
- После успешного `commit` юнита в `runUnit` собрать строки stdout с префиксом `test change: ` и напечатать каждую строкой прогона под юнитом тем же стилем, что `unverified`.
- Добавить в `runTotals` счётчик `testChanges` и в сводку прогона `, N test changes` при N > 0.
- Событию `unit_end` добавить поле `test_changes` — число пунктов (0 по умолчанию), событию `commit` юнита — поле `test_changes` с массивом пунктов.
- Тест в `run_test.go`: фейковый агент удаляет строку из закоммиченного тест-файла фикстуры → вывод прогона содержит строку `test change:`, сводка — `1 test changes`, `unit_end` — `"test_changes":1`.
- В `docs/design/runner.md` описать строку и итог в разделе «Вывод прогона» и поля в таблице раздела «Поток событий».
Verify: `go test ./internal/runner/ -run 'TestRunLoop|TestRunTestChanges'`
Commit: P050.2.2: show test changes in the run

### [ ] P050.2.3 Feed test changes to review and acceptance
Read:
- internal/cycle/commands.go:L1-175
- docs/design/cycle.md#цикл-юнита
Do:
- В `internal/cycle/commands.go` для юнита с `Role: review` добавить после `## Phase diff` раздел `## Test changes`: пункты `Test changes:` из тел коммитов фазы (тот же диапазон `base..HEAD` и префикс, что у `phaseDiffForRepository`), строкой `- <subject коммита>: <пункт>`; нет пунктов — строка `none`.
- Для юнита с `Role: acceptance` — тот же раздел по всем коммитам плана (префикс `Pxxx.`, база — родитель первого коммита с этим префиксом); раздел стоит перед `## Cycle`.
- Для вложенных репо из `repos:` — подразделы по репо, как у `## Phase diff`.
- Тесты в пакете `cycle`: промпт review-юнита после коммита с ослабленным тестом в той же фазе содержит пункт; промпт acceptance-юнита содержит пункты всех фаз; коммит другой фазы в промпт review не попадает.
- В `docs/design/cycle.md` дополнить перечень «`prompt` содержит» разделом `## Test changes` для ролей review и acceptance.
Verify: `go test ./internal/cycle/`
Commit: P050.2.3: feed test changes to review and acceptance

### [ ] P050.2.4 Review visible test changes
Read:
- internal/cycle/testchanges.go
Do:
- Проверить дифф фазы из раздела `## Phase diff` промпта против Goal, Constraints, `## Decisions` плана и `Do:` юнитов P050.2.1–P050.2.3; ничего не править.
- Отдельно проверить: ложные срабатывания на переименовании тест-файла (`-M`), на файлах с `test` в середине имени (`latest.go`, `contest/`), пропуск skip-маркера в многострочной вставке.
Verify: `go build ./... && go vet ./... && go test ./internal/cycle/ ./internal/format/ ./internal/lint/`
Role: review
Commit: P050.2.4: review visible test changes

## Phase 3: Skills and docs

### [ ] P050.3.1 Teach skills about guarded checks
Read:
- skills/bsv-docflow-run/SKILL.md
- skills/bsv-docflow-plan/SKILL.md#review
- skills/bsv-docflow-plan/SKILL.md#acceptance
- skills/bsv-docflow-plan/SKILL.md#replan-under-the-runner
- docs/usage.md#блок-и-replan
- CHANGELOG.md:L1-30
Do:
- `skills/bsv-docflow-run/SKILL.md`, «What not to do»: не удалять и не ослаблять существующие тесты и не добавлять skip-маркеры, чтобы пройти `Verify`; неверный тест — исправление с объяснением в Handoff либо блок `--needs plan`; такие правки помечаются в коммите (`Test changes:`) и видны ревью и приёмке.
- `skills/bsv-docflow-plan/SKILL.md`, «Review»: каждый пункт `## Test changes` промпта разобран — обоснован дифом и `Do:` или дефект (блок `plan`).
- Там же, «Acceptance»: каждый пункт `## Test changes` и каждая строка `Replan changed check:` в `## Decisions` закрываются как пункты `Unverified:` — тестом, `Accepted: … — …` или остаются `Unverified:`.
- Там же, «Replan under the runner»: `Verify`, `Do`, `Role` юнитов review/acceptance не меняются и не переводятся в `[-]`; смены `Verify` обычных юнитов docflow записывает в `## Decisions` сам.
- `docs/usage.md`, «Блок и replan»: одна-две фразы про опорные юниты и запись изменений.
- `CHANGELOG.md`, `## [Unreleased]`: `Added` — пометка ослабленных тестов в коммите, выводе и промптах review/acceptance; `Changed` — replan не меняет проверки review/acceptance и записывает смены `Verify`.
Verify: `docflow lint && grep -q 'Test changes' skills/bsv-docflow-run/SKILL.md && grep -q 'Replan changed check' skills/bsv-docflow-plan/SKILL.md && grep -q '^- .*Test changes' CHANGELOG.md`
Commit: P050.3.1: teach skills about guarded checks

## Phase 4: Acceptance

### [ ] P050.4.1 Accept verify guard end to end
Read:
- internal/runner/run_test.go:L81-242
- internal/runner/replan_test.go:L166-328
- internal/runner/role_acceptance_test.go:L1-50
Do:
- Новый файл `internal/runner/verify_guard_acceptance_test.go`, сценарии через собранный CLI и фейковых агентов (как в `run_test.go` и `replan_test.go`).
- Сценарий 1: агент юнита удаляет assert-строку из существующего тест-файла и добавляет `t.Skip(` в другой → прогон завершается, тело коммита юнита содержит `Test changes:` с двумя пунктами, вывод прогона — строки `test change:` и итог сводки, `unit_end` — `"test_changes":2`.
- Сценарий 2: в той же фикстуре `docflow prompt` юнита `Role: review` фазы и юнита `Role: acceptance` содержит раздел `## Test changes` с этими пунктами.
- Сценарий 3: юнит блокируется с `blocker: plan`, replan-агент меняет `Verify:` юнита `Role: acceptance` → план восстановлен текстом до вызова, код 3, вывод называет юнит и поле.
- Сценарий 4: replan-агент меняет `Verify:` обычного юнита → коммит replan содержит `Replan changed check:` в `## Decisions`, сводка — `1 checks changed by replan`, событие `replan` — непустой `check_changes`.
- Каждый пункт `Unverified:` из Handoff зависимостей закрыть тестом, `Accepted: … — …` или оставить `Unverified:` только при разрыве с Goal.
Verify: `go test ./...`
Traps: полный `go test ./...` идёт ~7 минут на Windows — для итераций `go test ./internal/runner/ -run VerifyGuard`.
Depends: P050.1.1, P050.1.2, P050.1.3, P050.2.1, P050.2.2, P050.2.3, P050.2.4, P050.3.1
Role: acceptance
Commit: P050.4.1: accept verify guard end to end
