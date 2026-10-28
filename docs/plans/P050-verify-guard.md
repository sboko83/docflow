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
  только `Verify:`, `Do:`, `Role:` и переход в `[-]` или `[x]`: открытый
  юнит (любой, не только опорный) replan не помечает `[x]` — раннер юнит
  `[x]` пропускает, проверка исчезла бы молча (запрет — P050.1.4).
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
- Anchor fields are compared as raw field lines from Field Line to End, not as Value and Items: raw text catches a rewritten Do list and a deleted Role line. Rejected: compare field.Value only, a Do field is a list and Value does not cover it.
- The anchor role is read from the old plan, not the new one: deleting the Role line of an anchor unit becomes the problem Role changed and the unit never stops being an anchor silently. Rejected: read the role from the new version.
- The guard lives inside the existing loop over old units with a continue structure: a removed anchor unit is already covered by unit removed and a done anchor unit by unit changed, so no duplicate problem text.
- Сравнивать обычные юниты по field.Value, а не по сырому тексту поля — пункт показывает старое и новое значение; отвергнуто: сырой текст поля (в пункте дважды печатался бы ключ Verify: и терялись бы переносы строк в многострочном значении).
- Записывать только то, что replan прошёл: опора и [x]/[-] не пишут пунктов, их смены останавливает checkReplan; отвергнуто: писать и смены опоры (план при этом восстанавливается, запись ушла бы в корзину).
- Печатать строку с префиксом `<uid>: ` как у остальных строк раннера; отвергнуто: голый `<uid> replan changed check: ...` (в терминальном фрейме префикс юнита теряется, строка повисает без контекста).
- Не давать пункта удалённому юиту: `unit removed` уже есть в проблемах checkReplan; отвергнуто: пункт `<id> Verify: ... -> ` для удаления (исчезнувшую проверку нельзя назвать изменённой).
- Переход открытого юнита в `[x]` запрещён replan для всех юнитов, не только опорных: раннер юнит `[x]` пропускает, replanCheckChanges переход в `[x]` не записывает — проверка стиралась бы молча (обход нашёл review P050.1.3); отвергнуто: записывать переход пунктом `Replan changed check` (юнит `[x]` раннер не запускает — запись создавала бы видимость пройденной проверки вместо стёртой).
- Проверка placed внутри существующего цикла по старым юнитам, сразу после ветки [x]/[-] и до разбора опоры, один problem плюс continue — why: смена статуса у опоры даёт одну проблему, а не набор Verify/Do/Role changed — rejected alternative: отдельный цикл по статусам, он дублировал бы тексты unit removed и unit changed.
- Новый текст правила дописан в конец правила 3, а не отдельным пунктом — why: правило 3 агент читает как блок о том, чего replan не меняет — rejected alternative: новый нумерованный пункт, он сдвигает нумерацию, которую зеркалит скилл (P025 gotcha).
- Пункты для уже сделанного коммита восстанавливаются по его собственному диффу (spanTestChanges), а не по индексу — why: после сбоя индекс чист против HEAD и stagedTestChanges вернул бы пусто, из-за чего сверка тела отклоняла бы законный коммит — rejected: оставить expected без блока Test changes (это дало бы молчаливый стоп `unexpected HEAD` на легитимном коммите).
- Разбор диффа идёт через `git diff -M --numstat -z` и `--name-status -z` с NUL-токенами, `-M` включён принудительно — why: rename-обнаружение не даёт ложного пункта на чистом переименовании и снимает двоизначность формата `{a => b}`/кавычек — rejected: разбор обычного текстового numstat (нужен解码 кавычек и `{old => new}`, плавающие значения при `core.quotepath`).
- Skip-маркер ищется подстрокой только в добавленных строках патча (`+`) — why: маркеры языконезависимы и ловятся в diff-потоке без парсера — rejected: парсинг AST каждого языка (новая зависимость и правила на файл).
- Блок `Test changes:` в теле и строки `test change:` в выводе — без новых ключей формата плана и без новых событий: v: 1 не тронут, вывод растёт только хвостом (sha юнита раннер берёт первым словом).
- Один общий префикс `test change: ` (`testChangeLabel`) служит и для печати строки прогона, и для разбора stdout `docflow commit` — why: одна форма у коммита, прогона и парнера, смена формы не разъедётся с cycle; rejected alternative: отдельный regex в раннере (вывод и парнер разошлись бы, форма из P050.2.1 дублировалась бы в двух местах).
- Пункты берутся из stdout коммита, а не пересчитываются раннером по дереву — why: источником правды остаётся cycle и git, раннер не парсит диффы; rejected alternative: пересчёт `stagedTestChanges` в раннере (расхождение с телом коммита и двойной учёт).
- `unit_end` всегда несёт `test_changes` (0, когда пунктов нет) — why: у событий одно поле и читатель v: 1 не различает «нет поля» и «ноль пунктов», поле new для старых прогонов игнорируется; rejected alternative: опускать поле при нуле (разная форма событий у юнитов одного прогона).
- Счётчик в сводке ставится после `, N checks changed by replan` и до `, N replans` — why: порядок строк, заложенный P050.1.3, сохраняется, новый счётчик его не ломает; rejected alternative: отдельная строка сводки про тесты.
- Пункты `## Test changes` фильтруются по префиксу и юниту, а не берутся из всего диапазона base..HEAD — why: диапазон шире фазы, в промпт review попадали чужие фазы и собственный коммит юнита — rejected: только диапазон без фильтра по префиксу
- Пункты читаются из тела коммита (`commitTestChanges` — инверсия `testChangesBody`), а не пересчитываются по дереву — why: git остаётся источником правды, промпт видит ровно то, что записал P050.2.1, и работает в интерактиве — rejected: пересчёт диффа в промпте (расхождение с телом коммита и двойной учёт)
- Phase diff и Test changes строят секции общим кодом (`promptRepositories`, `perRepositorySection`) — why: один список репозиториев, одна расстановка `###` и один порядок блоков — rejected: отдельная расстановка заголовков во второй секции (блоки разъехались бы по оформлению)
- `repos:` тянутся и для acceptance-роли — why: иначе у acceptance-промпта нет подразделов вложенных репозиториев и оценка read budget расходится с промптом — rejected: только корневой репозиторий в acceptance-промпте

## Phase 1: Replan guard

### [x] P050.1.1 Protect review and acceptance checks from replan
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
Handoff:
- New fixture units in replanCheckPlan: P001.1.5 with Role review and P001.1.6 with Role acceptance. Their Verify and Do lines are unique strings: echo review, echo accept, review the phase, accept the plan. Cases about anchor fields are built by ReplaceAll on those strings, so later units must reuse these ids and texts.
- The fixture frontmatter now carries executor, review and acceptance chains. Without an executor key lint reports Role needs executor mode and that error enters checkReplan problems, so anchor fixtures must stay in executor mode.
- The test helper lifted replaces the whole status marker, so it moves the review unit P001.1.5 from blocked to tilde. Cases that assert an anchor status must build before and after explicitly, and a new fix unit referenced in Depends has to be inserted before the anchor unit: Depends to a later unit is a lint error.
- Unit P050.3.1 has to update the section Replan under the runner in skills/bsv-docflow-plan/SKILL.md to match the new rule 3 text. Replan rules exist in two places, the P025 gotcha, this unit changed only the Go rule string.
- Unverified: No end to end check: no test runs the CLI path where a replanning agent rewrites the Verify field of an acceptance unit and the run restores the plan with code 3. The guard is covered by direct checkReplan calls in Go unit tests only, the scenario is scheduled for P050.4.1.
- Unverified: No parity check: replanRules text changed and the section Replan under the runner in skills/bsv-docflow-plan/SKILL.md did not, nothing compares the two rule texts, no test and no grep checks it (the P025 gotcha).

### [x] P050.1.2 Record check changes made by replan
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
Handoff:
- Строка записи ровно такая: `- Replan changed check: <id> Verify: <старое> -> <новое>` и `- Replan changed check: <id> skipped`; значения — field.Value, то есть бэтики и пробел сохраняются. P050.3.1 в Verify ищет `Replan changed check` в skills/bsv-docflow-plan/SKILL.md — совпадать должен префикс `- Replan changed check: ` (префикс `- ` добавляет AppendSectionItems).
- Один юнит, сменивший Verify и упавший в [-], даёт два пункта подряд: сначала Verify, затем skipped; удалённый юнит пункта не даёт, опора ([x]/[-], review, acceptance) — не даёт никогда: их смены останавливает checkReplan. P050.4.1 (сценарий 4) опирается на этот порядок.
- Режим check-change живёт в таблице режимов TestReplanRun: агент слабит Verify юнита P001.1.2 до `true` (единственная уникальная строка `test -f d.txt` в фикстуре newReplanFixture) и снимает блок liftReply; editPlan меняет только первое вхождение, поэтому новые режимы обязаны брать уникальные строки. Строка вывода — с префиксом `P001.1.1: replan changed check: ...` (в fancy-режиме префикс срезается).
- Событие replan теперь всегда несёт check_changes: при успехе — массив пунктов, при отказе/отклонении — пустой массив []; читатель v: 1 обязан игнорировать поле у старых прогонов. Итог прогона вставляется после `, N unverified` и до `, N replans, M switches` — точный порядок строк сводки меняется, если P050.2.2 добавит свой счётчик test changes.
- recordReplanChecks парит файл плана с диска и сохраняет plan.Save до коммита: коммит replan содержит дифф Decisions, а план после реплана становится изменённым; base проверки чужих коммитов юнит берёт заново после коммита replan.
- Unverified: Путь `[-]` не гоняется сквозным прогоном: в TestReplanRun/сcheck-change слабится только Verify, нет сценария, где replan переводит открытый юнит в [-] и проверяют строку Decisions, строку прогона и check_changes — нужен отдельный режим прогона.
- Unverified: Не проверен отказ recordReplanChecks (нет секции ## Decisions, незаписываемый файл): код 2 с восстановлением плана; docflow lint такие планы отсекает раньше, сценария нет.
- Unverified: Правка плана с CRLF не проверена: фикстуры — LF, insertRaw берёт перевод строки из первой строки файла, на Windows-плане с CRLFappend не гонялся.
- Unverified: Терминальный (fancy) вид жёлтой строки не виден: тестовый конвейер даёт некрашеный вывод, блок-фрейм с `! ` и срезанным префиксом юнита не проверялся ни одним прогоном.

### [x] P050.1.4 Forbid replan marking open units done
Read:
- internal/runner/replan.go:L48-135
- internal/runner/replan_rules.go
- internal/runner/replan_test.go:L99-159
- docs/design/runner.md#авто-replan
Do:
- В `checkReplan` (`internal/runner/replan.go`) в цикле по юнитам старого плана: открытый (не `[x]`/`[-]`) юнит, чья новая версия имеет статус `x`, — проблема `unit <id> marked [x] by replan` с `continue`, проверка стоит до разбора опорных юнитов и действует на обычные юниты и опорные одинаково; `replanCheckChanges` не меняется — запрещённое checkReplan не записывается.
- В `replanRules` (`internal/runner/replan_rules.go`) в пункте 3 после «such a unit never moves to `[-]`» дописать: юниты старого плана, открытые на момент replan, replan не помечает `[x]` — раннер юнит `[x]` пропускает, проверка исчезла бы молча.
- Тесты в `TestCheckReplan`: открытый обычный юнит → `[x]` — проблема; опорный (review) `[!]` → `[x]` — проблема; `[!]` → `[~]` и `[ ]` — без проблем; юнит старого плана `[x]` остаётся `[x]` — без проблем.
- В `docs/design/runner.md` в разделе «Авто-replan» в перечень проверок `check_replan` дописать запрет помечать открытые юниты `[x]`.
Verify: `go test ./internal/runner/ -run 'TestCheckReplan|TestReplanPrompt'`
Commit: P050.1.4: forbid replan marking open units done
Handoff:
- Проблема ровно такая: unit <id> marked [x] by replan. Печатается в цикле по старым юнитам до ветки разбора опоры и с continue: опорный юнит, помеченный в [x], даёт только эту проблему, без Verify/Do/Role changed; в check_changes и ## Decisions переход не попадает — запрещённое останавливает checkReplan, replanCheckChanges для отклонённого вызова не запускается.
- Новые кейсы TestCheckReplan используют id P001.1.2/1.3/1.5/1.6 и хелпер lifted; кейс done-stays-done вставляет юнит P001.1.4 Extra только в after-текст, в самой фикстуре replanCheckPlan id P001.1.4 свободен — новым юнитам его занимать нельзя.
- Правило 3 в REPLAN_RULES теперь кончается фразой про [x]: P050.3.1 обязан перенести её в раздел Replan under the runner скилла bsv-docflow-plan (P025 gotcha: правила живут в двух местах).
- tools/parity/scenarios/run_replan.sh запускается только через tools/parity/run.sh: прямой запуск делает git init и git commit в репозитории вызывающего. Нужен гард в сценарии или запись в tools/parity/README.md.
- Коммит юнита: 5 файлов — internal/runner/replan.go, internal/runner/replan_rules.go, internal/runner/replan_test.go, docs/design/runner.md и файл плана (статус [~]).
- Unverified: Сквозного сценария нет: ни один тест не гоняет путь CLI, где replan-агент помечает открытый юнит в [x], а раннер восстанавливает план кодом 3 — Go-тесты вызывают checkReplan напрямую, parity-сценария с таким кейсом нет; нужен сценарий P050.4.1.
- Unverified: Не проверен паритет текстов правил: replanRules (правило 3) изменён, раздел Replan under the runner в skills/bsv-docflow-plan/SKILL.md — нет; ни один тест и ни один grep эти тексты не сверяет — нет команды, ловящей расхождение.
- Unverified: Не покрыт комбинированный кейс: открытый юнит одновременно помечен в [x] и ослабил Verify — должен быть единственный problem marked [x] by replan и пустой check_changes; такого элемента в таблице TestCheckReplan нет.
- Unverified: Linux-ветка не проверялась: parity (70 сценариев) и Go-набор прогнаны только на Windows Git Bash, тестов на Linux для этой правки не запускалось.

### [x] P050.1.3 Review replan guard
Read:
- internal/runner/replan.go:L1-130
- internal/runner/replan_rules.go
Do:
- Проверить дифф фазы из раздела `## Phase diff` промпта против Goal, Constraints, `## Decisions` плана и `Do:` юнитов P050.1.1, P050.1.2 и P050.1.4; ничего не править.
- Отдельно проверить: может ли replan обойти защиту — удалить `Role:` у опорного юнита, заменить опорный юнит новым с другим id, перенести `Verify` в другое поле, пометить открытый юнит `[x]`; найденный обход — дефект.
Verify: `go build ./... && go vet ./... && go test ./internal/cycle/ ./internal/format/ ./internal/lint/`
Depends: P050.1.4
Role: review
Commit: P050.1.3: review replan guard
Handoff:
- Blocked (plan): План ожидал, что replan не может обойти защиту опорных проверок; в реальности replan помечает открытый юнит review/acceptance (или обычный) как [x]: checkReplan не запрещает переход открытых юнитов в [x] (только [-]), replanCheckChanges его не записывает, раннер юнит [x] пропускает — опорная проверка стирается молча. Нужен юнит-исправление: запрет v.Status == "x" для открытых юнитов старого плана в checkReplan, правило 3 replanRules и docs/design/runner.md
- Ревью пройдено, дефектов нет: четыре обхода закрыты — удаление Role: у опоры даёт Role changed (anchorRole читает старый план), замена опоры новым id даёт unit removed, перенос Verify в другое поле у опоры даёт Verify changed, а у обычного юнита — записанный пункт Decisions; открытый юнит в [x] ловит marked [x] by replan до разбора опоры (P050.1.4)
- Replan может менять Read:, Commit: и заголовок опорного юнита — это не ослабление проверки и так задумано (Goal объявляет неприкосновенными только Verify/Do/Role); приёмка P050.4.1 не должна считать это дефектом
- Дублированное поле Verify: в новой версии плана не ослабляет проверку: format/doc.go Field берёт первое поле с ключом, и guard, и исполнитель юнита читают одно и то же первое поле

## Phase 2: Visible test changes

### [x] P050.2.1 Mark weakened tests in unit commits
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
Handoff:
- Формы строк ровно такие: пункт коммита `<path>: -N lines` / `<path>: deleted` / `<path>: +skip`; строка вывода `test change: <пункт>`, для вложенного репо `test change: <repo>/<пункт>`. Порядок вывода: сначала существующие строки (sha+subject, файлы, Repo:/sha репо), затем все строки `test change:`; пункты идут по репозториям в порядке commits (вложенные), затем корневые. P050.2.2 считает именно префикс `test change: ` и не трогает первую строку stdout.
- Тело коммита: блок `Test changes:` ставится последним — пустая строка, строка `Test changes:`, затем `- <пункт>`; у корневого коммита он идёт после строк `Repo: <path> <sha>`. P050.2.3 берёт тот же префикс `Test changes:` и маркер `- ` при разборе тел коммитов фазы.
- commitRepo теперь возвращает 4 значения: sha, files, []string пунктов, error. Для уже сделанного коммита пункты чинит `spanTestChanges(cwd, before)` (diff `<before>..HEAD`): после сбоя индекс чист против HEAD, `stagedTestChanges` пуст, и именно этот diff даёт те же пункты. Сверка тела в `pending commit` тоже строит `expected` через `testChangesBody`, иначе коммит, сделанный до апгрейда (без блока `Test changes:`), даёт EXIT_ORDER `unexpected HEAD` и требует ручного разбора — это ожидаемое поведение для старых незакоммиченных юнитов.
- IsTestPath экспортирована: совпадение по имени (`*_test.go`, `test_*.py`, `*_test.py`, подстроки `.test.`/`.spec.`, `*Test.java`, `*Test.kt`, `*Tests.swift`, `*Test.swift`) и по каталогу пути (`test`, `tests`, `__tests__`, `spec`, `Tests`, `androidTest`) — регистронезависимо по образцу, путь приводится к слешам. Чистый rename (`-M`) пункта не даёт, rename с удалёнными строками даёт пункт на назначенном пути; новый тест-файл (`A`) пункта `-N lines` не даёт, `t.Skip(` в новом файле даёт `+skip`.
- Unverified из зависимостей (P050.1.3/1.4) здесь не повторяются; сквозного прогона через CLI для этого юнита нет — сценарий за P050.4.1, а вывод в прогоне и в промптах review/acceptance — за P050.2.2 и P050.2.3.
- Unverified: Нет фикстуры с CRLF в содержимом тест-файла: `-z` numstat и разбор `-U0` заголовков прогнаны только на LF-содержимом на Windows (trap плана про CRLF фикстур) — нужен тест с фиксурой CRLF.
- Unverified: Нет теста на путь, который git печатает в кавычках (пробел, не-ASCII): ветка `cleanDiffPath` (снятие кавычек в `--- "a b/c"`) не проверена ни одним прогоном.
- Unverified: Нет теста на бинарный тест-файл: numstat даёт `-` для счётчиков, поэтому удалённый бинарный тест-файл пункта не даёт — проверка того, что это именно пропуск, а не потеря, отсутствует.
- Unverified: Нет теста на skip-маркер, разнесённый по добавленным строкам (например `t.` и `Skip(` в разных строках одной вставки): детектор работает построчно, такой случай не покрыт.

### [x] P050.2.2 Show test changes in the run
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
Handoff:
- Форма строки прогона: текстовый вывод — ` test change: <пункт>` (строка вывода `docflow commit` дословно, два пробела в начале), TTY — блок `! Test changes:` и пункты в `│ `; печатается под юнитом после `Unverified`. Общий префикс задаёт константа `testChangeLabel = "test change: "` в internal/runner/progress.go, её же использует парнер `testChangeItems` (run.go) — она не экспортирована, P050.2.3 для разбора тел коммитов берёт префикс `Test changes:` и маркер `- ` из internal/cycle (они не экспортированы, нужен экспорт хидера, если разбор по телу), а не из раннера.
- Порядок строк сводки прогона фиксирован: `, N unverified` → `, N checks changed by replan` → `, N test changes` → `, N replans, M switches`; счётчик test changes печатается только при N > 0.
- События v: 1: у `commit` юнита поле `test_changes` — массив пунктов (JSON `null`, когда пунктов нет, как `check_changes` у replan), у `unit_end` — `test_changes` число и печатается всегда, 0 когда пунктов нет. Событие `commit` закрытия (close.go) поля не несёт — вне юнита.
- Пункты в прогоне — ровно те же строки, что в блоке `Test changes:` тела коммита: `<path>: -N lines`, `<path>: deleted`, `<path>: +skip`, для вложенного репо `<repo>/<пункт>` — P050.2.3 сверяет промпты review/acceptance по этим же текстам.
- Unverified: TTY-фрейм `! Test changes:` (fancy) не покрыт тестом: тесты раннера гоняют вывод в pipe, тестов прогресса для него нет, реального терминала с несколькими пунктами никто не прогонял — нужен тест в internal/runner/progress_test.go (как `! Unverified:` в TestTerminalLayoutUnitResultsAndUnverified) или ручной прогон в TTY.
- Unverified: Вложенный репо: строка прогона `test change: <repo>/<пункт>` не проверена — фикстура run_test с одним корневым репо, прогона с `repos:` в docs/docflow.md и коммитами вложенного репо нет.
- Unverified: Возобновление незавершённого коммита: путь, где пункты приходят из `spanTestChanges` (коммит сделан до апгрейда, индекс чист против HEAD), в прогоне не проверен — нет сценария сбоя между `git commit` и выводом `docflow commit`, чтобы раннер напечатал восстановленные пункты.

### [x] P050.2.3 Feed test changes to review and acceptance
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
Handoff:
- Форма строк раздела `## Test changes`: `- <subject коммита>: <пункт>`; при `repos:` блоки `### <path>` с пустой строкой между блоками, а когда пунктов нет — строка `none` (без тире); на эти строки опираются review и приёмка
- Пункты раздела берутся из тела коммита (`commitTestChanges` в internal/cycle, не экспортирована): разбор тел коммитов строится как `git log --format=%s%x00%b%x00 base..HEAD` + этот парсер; экспорт для приёмки не делался
- Фильтр по префиксу и собственный коммит юнита в `testChangesForRepository` обязательны: диапазон `base..HEAD` шире фазы и без фильтра тянул бы чужие фазы в промпт review
- `repos:` теперь читается и для acceptance: `Prompt` в internal/cycle и `runnerPrompt` в internal/runner — у acceptance-промпта появились подразделы вложенных репозиториев, read budget `lint` это учитывает
- Unverified: Изменение `internal/runner/runnerPrompt` (repos для acceptance) вне `gate:` — полный `go test ./internal/runner/` не запускался, проверялся только subset `-run 'Prompt|PhaseDiff|Role|Acceptance|Batch'`
- Unverified: Подразделы вложенных репозиториев в acceptance-промпте тестом не покрыты: фикстура с `repos:` есть только для review-юнита — нет сценария acceptance с вложенным репозиторием

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
- Там же, «Replan under the runner»: `Verify`, `Do`, `Role` юнитов review/acceptance не меняются и не переводятся в `[-]`; открытые юниты replan не помечает `[x]`; смены `Verify` обычных юнитов docflow записывает в `## Decisions` сам.
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
- Сценарий 5: replan-агент помечает открытый юнит `[x]` (обычный и `Role: acceptance`) → план восстановлен текстом до вызова, код 3, вывод называет юнит.
- Каждый пункт `Unverified:` из Handoff зависимостей закрыть тестом, `Accepted: … — …` или оставить `Unverified:` только при разрыве с Goal.
Verify: `go test ./...`
Traps: полный `go test ./...` идёт ~7 минут на Windows — для итераций `go test ./internal/runner/ -run VerifyGuard`.
Depends: P050.1.1, P050.1.2, P050.1.3, P050.1.4, P050.2.1, P050.2.2, P050.2.3, P050.2.4, P050.3.1
Role: acceptance
Commit: P050.4.1: accept verify guard end to end
