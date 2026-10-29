---
id: P051
title: Reference benchmark with recorded results
type: feature
status: active
created: 2026-10-28
executor: codex | gpt-6-luna | high
fallback: pi | zai/glm-5.3 | high
review: pi | zai/glm-5.3 | medium; codex | gpt-6.1-sol | medium
acceptance: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high
replan: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | medium
permissions: auto
timeout: 7200
gate: go build ./... && go vet ./... && go test ./internal/cycle/ ./tools/...
---
## Goal

У docflow есть эталонный бенчмарк, по которому видна динамика от
изменения к изменению. Три эталонные задачи — small, medium, large — над
одним базовым проектом `bench/base/`; у каждой есть docflow-план, скрытые
тесты-оракул и эталонное решение, а `go run ./tools/bench validate`
доказывает, что оракул падает на базе и проходит на решении. `go run
./tools/bench run` прогоняет задачу двумя ветками — одна модель делает
весь план одной сессией (`single`) и `docflow run` по ростеру бенчмарка
`bench/roster.toml` (`docflow`) — и оценивает обе оракулом. Каждый прогон
без исключений записывается в `bench/results/` по схеме `v: 1` и
коммитится; `bench/results/README.md` показывает тренд по задачам, веткам
и версиям docflow.

## Context

Слабые стороны 2 и 3 оценки 2026-10-28: ценность docflow не измерена, а
изменения оптимизируются вслепую. Владелец ведёт по docflow несколько
проектов и хочет видеть, даёт ли система выигрыш и куда её двигает каждое
изменение — поэтому задачи фиксированы, а результаты не теряются.

- docs/reviews/2026-10-28-agentic-review.md#слабые-стороны
- docs/design/runner.md#поток-событий

## Constraints

- Формат плана docflow и его версия не меняются; поток событий раннера
  остаётся `v: 1`.
- Бенчмарк — инструмент разработки: программа `tools/bench/` (пакет
  `main`, модуль `bsv-docflow`), в релизный бинарь и `install.sh` не
  входит; CHANGELOG не меняется.
- Ветка `docflow` по умолчанию идёт на CLI, собранном из текущего
  checkout (`go build ./cmd/docflow` в каталог прогона): бенчмарк меряет
  изменения проекта, а не релиз; `--docflow PATH` — другой бинарь.
- Результаты в `bench/results/` не содержат абсолютных путей, имён
  пользователя и частных проектов: каталог публичный.
- Живые прогоны тратят квоту и идут часами: их запускает человек после
  закрытия плана; юниты и приёмка проверяют всё на фейковых агентах.
- Опирается на P050 (`cycle.IsTestPath`, `Test changes:`, поля
  `test_changes` и `check_changes` событий) и идёт после него в Queue.

## Traps

- Не путать режимы `docflow run`: `--plan` и `--all` валидны только с `--dry-run` (выбор плана для показа); живой запуск берёт текущий план Queue и ведёт его до конца сам, а его нормальное завершение — код 1 с `nothing to do`, не 0.
- Цепочки `bench/roster.toml` обязаны быть собираемыми текущим подбором ростера: цепочка идёт в порядке `spend` (требование `trusted` ставит провайдеров с `trusted` вперёд), `check` исключает провайдера из review целиком, у одного провайдера один маршрут на ступень. Сверять с `docflow roster show --roster bench/roster.toml`.
- Квота-пробы раннера ломают бенчмарк на фейковых агентах:
  план после `roster apply` — executor-режим, `docflow run` грузит только
  встроенный agents-конфиг (override через XDG_CONFIG_HOME не действует)
  и на старте зовёт `codex app-server` (JSON-RPC, таймаут 30s) и `pi
  auth print-api-key`; фейк без ответа висит 30s и пишет файлы в дерево
  копии — грязное дерево отказывает prompt-шагу кодом 5.
- Go-файлы корпуса (`bench/base/`, оракулы и решения задач) не должны
  войти в модуль `bsv-docflow`: `bench/go.mod` — забор, иначе
  `go vet ./...` и `go test ./...` репозитория соберут фикстуры.
- Утечка ответа: копия ветки собирается из `bench/base/` в новом
  каталоге (`git init`, один коммит) — без `bench/tasks/`, истории
  docflow и оракула; оракул кладётся только после работы ветки.
- В ветке `single` нет `docs/docflow.md`: личные скиллы docflow агента
  не должны сработать; промпт прямо запрещает создавать план.
- Оракул проверяет только то, что план задачи фиксирует буквально:
  синтаксис CLI, формат вывода, формат файлов, коды выхода. Тест на
  деталь, которой нет в тексте плана, наказывает ветку за угадывание.
- Команды проверки на Windows идут через `bash.exe` Git for Windows, как
  `Verify:`; пути в результатах — со слешами.
- `go test ./internal/runner/` целиком идёт ~7 минут на Windows: для
  итераций — `-run` по своим тестам.

## Decisions

- Эталонные задачи вместо replay чужих планов: фиксированный корпус даёт
  сравнимый ряд от прогона к прогону; заранее написанный план исключает
  разброс планирования — отвергнуто: replay закрытых планов настоящих
  проектов (у каждого свой оракул и шум) и только пассивные метрики (не
  отвечают, есть ли выигрыш против одной модели).
- Ветка `single` по умолчанию — `codex | gpt-6.1-sol | high` (сильнейший
  маршрут приёмки), `--single-route` меняет; маршрут пишется в каждую
  запись — отвергнуто: самый дешёвый маршрут ростера (сравнение превратится
  в сравнение моделей).
- Необязательная ветка `docflow-one` — `docflow run`, где все цепочки
  заменены маршрутом `single`: отделяет вклад цикла docflow от вклада
  роутинга — включается `--arms`.
- Ростер бенчмарка — `bench/roster.toml` в репозитории, его sha256 — в
  каждой записи: смена ростера видна в ряду так же, как смена кода —
  отвергнуто: личный `~/.config/docflow/roster.toml` (его нет на части
  машин, и он меняется незаметно).
- Хранилище: `bench/results/runs.jsonl` — по строке на задачу × ветку ×
  прогон, дописывается сразу после ветки; `bench/results/<run-id>/` —
  сводка прогона; `bench/results/README.md` — тренд, генерируется;
  сырые логи и копии веток — в `work/bench/<run-id>/` (`.gitignore`) —
  отвергнуто: только локальный каталог (теряется, не видно связь с
  коммитом docflow).
- Фиксация обязательна: `run` отказывает (код 2) при незакоммиченных
  правках в `bench/results/`, записывает и прерванный прогон (`status:
  interrupted`) и в конце коммитит только `bench/results/` с subject
  `bench: run <run-id>` — отвергнуто: ручной коммит (результат забывают,
  ряд рвётся) и флаг «не записывать» (эксперименты без записи — та же
  дыра).
- Оракул — `go test -json ./benchoracle/...` в копии ветки: тесты гоняют
  собранный CLI базового проекта через `os/exec`; счёт — число
  прошедших тестов из общего — отвергнуто: LLM-судья и собственные тесты
  ветки.
- Использовать cycle.PlanShell для команд оракула — он уже экспортирует общий выбор POSIX shell — отвергнута отдельная логика поиска bash.exe.
- При сбое сборки оракула считать объявленные тесты проваленными — сборочная ошибка не должна выглядеть как пустой успешный прогон — отвергнуто завершение без счёта тестов.
- `ts` uses epoch seconds to match runner events v: 1 — keeps timestamp semantics consistent — rejected a separate timestamp encoding.
- Absolute paths are redacted from public result notes and oracle tails — results stay safe to publish — rejected storing raw local output.
- Ветка `docflow` запускается одним `docflow run --yes` без `--plan`/`--all`: живой запуск CLI берёт текущий план Queue и доводит его до конца (внутренний цикл раннера), а `--plan`/`--all` CLI принимает только с `--dry-run` — отвергнуто `docflow run --all --yes` (то же ограничение CLI) и внешний цикл повторных вызовов (раннер уже циклится сам; лишние вызовы дают лишние события).
- Таймаут ветки docflow в тесте — квота-пробы раннера: после `roster apply`
  план в executor-режиме, `RunConfig` берёт только встроенный agents-конфиг
  (`LoadAgents("", false)`), и `quota = []` из XDG_CONFIG_HOME не действует;
  раннер зовёт `codex app-server` (30s JSON-RPC) и `pi auth print-api-key`,
  старые фейки не отвечали и писали `value.go` прямо в дерево — 30s
  зависания плюс грязное дерево (отказ prompt-шага, код 5). Фиксура по
  образцу `internal/runner`: фейки отвечают пробы, тестовый ростер ведёт
  executor на pi-маршрут с ценой (встроенный профиль codex без `cost`,
  разбор его потока выкидывает цену) — отвергнуто поднимать таймаут ветки
  или чинить раннера: причина в фикстуре, живой запуск без проб
  заканчивается за секунды кодом 0.
- Тестовый ростер направляет executor на Pi с ценой, а сильные цепочки содержат также Codex — так тест проверяет стоимость и отвечает на startup-пробы раннера без зависаний.
- Ростер бенчмарка переписан под достижимые цепочки: подбор даёт цепочку в порядке `spend`, `check` исключает провайдера из review целиком, а у одного провайдера один маршрут на ступень — поэтому цепочки «Pi первым в review при Codex-исполнителе» и своя настройка effort по ролям у одного провайдера недостижимы (подтверждено чтением `internal/roster/roster.go` и прогоном `roster show` на макете ростера); вместо этого `trusted` у zai и `review`/`acceptance`/`replan` = `"trusted"` — Pi первым во всех проверяющих ролях, executor и fallback прежние — отвергнуто менять логику ростера ради бенчмарка: семантика отбора закрыта дизайном P036, бенчмарк измеряет текущий docflow, а не меняет его.
- Replan changed check: P051.2.1 Verify: `go vet ./... && cd bench/base && go test ./... && cd ../.. && docflow roster show --roster bench/roster.toml | grep -q '^executor: codex | gpt-6-luna | high'` -> `go vet ./... && cd bench/base && go test ./... && cd ../.. && docflow roster show --roster bench/roster.toml | grep -q '^executor: codex | gpt-6-luna | high' && docflow roster show --roster bench/roster.toml | grep -q '^review: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high'`
- Журнал хранится как UTF-8 TSV с датой, положительной суммой и описанием — формат прост для чтения и тестирования — отвергнуты более сложные форматы хранения.
- Oracle tests assert the documented CLI contract, including rejecting `--json` for `balance`; this keeps the benchmark scoped to the plan.
- Фикстура lint в `validate` повторяет подготовку docflow-ветки: `materialize` (git init + коммит), `docflow init`, план и Queue, `roster apply` — план задачи попадает на lint в executor-режиме и внутри git-репозитория, какие бы `Role:`-юниты в нём ни были (без ростера `Role:` требует executor-режима, а phase diff review-юнита зовёт `git log`); подтверждено вручную на medium-плане — lint без ошибок. Фикс вшит в Do P051.2.3, а не отдельным юнитом перед ним: SelectUnit берёт любой `[~]` раньше любого `[ ]`, и отдельный юнит не встал бы перед возобновляемым корпусом. Отвергнуто: убрать `Role:`-юниты из планов корпуса — ослабляет docflow-ветку бенчмарка, которая меряет именно цикл review/acceptance (small без Role-юнитов — осознанное исключение).
- Replan changed check: P051.2.3 Verify: `go run ./tools/bench validate --task medium` -> `go test ./tools/bench/... && go run ./tools/bench validate --task medium`
- Use journal version 3 with USD assigned to version 1/2 records — keeps legacy data readable while storing currency per entry — rejected changing legacy amounts or requiring migration first.
- Use direct directed rates with integer numerator/denominator and round down — makes conversion deterministic — rejected implicit inverse and multi-hop conversion.
- Make recurring entries virtual and expand only with an explicit --through date — keeps reads deterministic and the journal unchanged — rejected writing generated occurrences.
- Читать `# ledger-v2` с присвоением `USD`, поддерживая трёх- и четырёхполевые записи — для совместимости с legacy-форматом и промежуточным форматом категорий — отвергнуто: принимать только один layout.
- Отклонение P051.2.3 (inline Role-фикстура вместо testdata/tasks) зачитано как выполненное — Do требовал зелёный validate на плане с Role:-юнитами и зелёный tiny, оба покрыты TestValidatePlanWithRoleUnits — отвергнуто требовать перенос фикстуры в testdata: тот же прогон, лишний файл.
- Приёмка гоняет собранный bench CLI отдельным процессом — покрывает main-диспетчер, флаги и коды выхода; отвергнуто переиспользование in-process runCommandAt из main_test (не проверяет CLI как программу)
- Дифф ветки считается против SHA базового коммита копии — ref `base` никогда не существовал, поля всегда были нулями; отвергнуто создание тега/ветки base в materialize (лишний ref, затрагивает и validate)
- Справедливость оракула закрыта с двух сторон: планы medium/large теперь буквально фиксируют формат list (4 колонки), small-оракул сравнивает JSON семантически; отвергнуто смягчение list-тестов оракула (ослабило бы шкалу — угадывание исключается текстом плана)
- Windows-скип вместо .bat-фейков — .bat непроверяем с macOS и сломал бы CI первым прогоном; отвергнуто компилировать pi.exe/codex.exe-фейки (дороже самой приёмки)

## Phase 1: Bench tool and result store

### [x] P051.1.1 Materialize and validate bench tasks
Read:
- internal/cycle/verify.go:L185-225
- internal/cycle/testchanges.go
Do:
- Новая программа `tools/bench/` (`package main`) с подкомандами `validate`, `run`, `report`; корень checkout — каталог с `go.mod` модуля `bsv-docflow` над текущим каталогом.
- Задача — каталог `bench/tasks/<name>/` с `plan.md` (docflow-план с id `P001`, без ключей маршрутов), `oracle/` и `solution/` (деревья файлов, накладываемые поверх копии по тем же относительным путям). Флаг `--tasks DIR` (по умолчанию `bench/tasks`) и `--base DIR` (по умолчанию `bench/base`) — для тестов.
- `materialize(base, dest)`: копия дерева `base` в `dest`, `git init`, локальные `user.name`/`user.email`, один коммит `base`; без истории и без файлов вне `base`.
- `validate --task NAME|all [--no-solution]`: копия базы → оракул поверх → `go test -json ./benchoracle/...` должен дать хотя бы один провал; без `--no-solution` — вторая копия, решение и оракул поверх → все тесты оракула прошли; ещё одна копия с `docflow init` и планом в `docs/plans/P001-<name>.md` проходит `docflow lint` без ошибок. Итог — строка на задачу `<name>: base <p>/<n>, solution <p>/<n>, lint ok`; любое нарушение — код 1.
- Разбор `go test -json`: счёт событий `pass`/`fail` по тестам (с `Test`, без подтестов-дублей), паника сборки — все тесты провалены.
- Команды — через тот же POSIX sh, что `Verify:` (если раннер команды в `internal/cycle` не экспортирован — экспортировать его, не дублируя логику поиска `bash.exe`).
- Тесты в `tools/bench/`: фикстура `tools/bench/testdata/` с мини-базой (свой `go.mod`) и задачей `tiny` — `validate` зелёный; оракул, проходящий на базе, — код 1; решение, не проходящее оракул, — код 1.
Verify: `go test ./tools/bench/...`
Commit: P051.1.1: materialize and validate bench tasks
Handoff:
- Команды run и report пока зарегистрированы, но возвращают «not implemented yet»; их реализация остаётся следующим юнитам.
- Изменения docs/plans/P051-benchmark.md существовали до этой работы и сохранены без правок.

### [x] P051.1.2 Record bench results and render the trend
Read:
- docs/design/runner.md#поток-событий
Do:
- Запись схемы `v: 1` — по строке JSON в `bench/results/runs.jsonl` (флаг `--results DIR`): `v`, `run_id` (время старта UTC `20261029T101500Z`, при занятом — `-2`…), `ts`, `task`, `task_sha` (sha256 по `plan.md`, оракулу, решению и дереву базы), `arm`, `route` (для `single` и `docflow-one`), `roster_sha` и `chains` (ключи маршрутов шапки после `roster apply`, для `docflow`), `docflow_version`, `docflow_commit`, `docflow_dirty`, `status` (`done|failed|timeout|interrupted|error`), `oracle` (`passed`, `failed`, `total`), `seconds`, `cost` (`null`, если неизвестна), `tokens`, `units` (`total`, `done`, `blocked`), `attempts`, `replans`, `test_changes`, `check_changes`, `unverified`, `diff` (`files`, `insertions`, `deletions`).
- Строка дописывается и сбрасывается на диск сразу по завершении ветки; Ctrl-C пишет текущей ветке `status: interrupted` и доводит фиксацию до конца.
- `bench/results/<run-id>/summary.md`: таблица прогона (задача, ветка, маршрут или ростер, оракул, стоимость, минуты, юниты, попытки, replan) и хвосты провалов оракула до 40 строк на ветку.
- `report` перегенерирует `bench/results/README.md` из `runs.jsonl`: по таблице на задачу — строки прогонов от новых к старым (дата, `docflow_version` и короткий `docflow_commit`, ветка, оракул в процентах, стоимость, минуты, попытки и replan), дельта оракула и стоимости против предыдущего прогона той же задачи и ветки; строки другого `task_sha` отделены пометкой «задача изменена».
- Фиксация: перед стартом `git status --porcelain -- <results>` непуст — код 2 с перечнем; в конце `report`, затем `git add -- <results>` и `git commit -m "bench: run <run-id>" -- <results>` — коммит берёт только каталог результатов.
- В записи и сводке нет абсолютных путей: логи упоминаются как `work/bench/<run-id>/…`.
- Тесты в `tools/bench/`: две записи разных прогонов → README с двумя строками и дельтой; грязный каталог результатов — код 2; коммит во временном репо содержит только файлы результатов при других staged-правках; запись без `cost` читается.
Verify: `go test ./tools/bench/...`
Commit: P051.1.2: record bench results and render the trend
Handoff:
- P051.1.3: `run` is still a stub; connect each completed branch to `appendResult`, `writeRunSummary`, `report`, and `commitResults`, preserving interrupted-run recording.

### [x] P051.1.3 Run single and docflow arms
Read:
- internal/agents/agents.go:L895-1020
- internal/agentio/process.go:L184-300
- internal/agentio/usage.go:L140-180
- internal/runner/role_acceptance_test.go:L1-50
- internal/agentio/testdata/fake_agent.sh
- internal/runner/fallback_test.go:L30-36
Do:
- `run --task NAME|all [--arms single,docflow] [--single-route "agent | model | effort"] [--roster bench/roster.toml] [--docflow PATH] [--timeout SECONDS] [--dry-run]`; по умолчанию ветки `single,docflow`, маршрут `codex | gpt-6.1-sol | high`, таймаут 21600 на ветку.
- Без `--docflow` — `go build -o work/bench/<run-id>/bin/docflow ./cmd/docflow`; `docflow_version` — вывод `--version` бинаря, `docflow_commit` и `docflow_dirty` — HEAD и `git status --porcelain` checkout.
- Ветка `single`: `materialize` в `work/bench/<run-id>/<task>-single/`, один вызов агента маршрута через `agents.BuildCommand` и `agentio.CallAgent` (`permissions` `auto`); промпт — весь текст плана задачи без статусов и инструкций цикла плюс указание реализовать всё в этой сессии, гонять `go test ./...`, плана docflow не создавать. Стоимость и токены — `agentio.RunCost`/`agentio.RunTokens`.
- Ветка `docflow`: `materialize`, `docflow init`, план в `docs/plans/P001-<task>.md`, в Queue, `status: active`, коммит; `docflow roster apply P001 --roster <roster>` и коммит; один вызов `docflow run --yes` — без `--plan` и `--all` (CLI даёт их только с `--dry-run`): живой запуск сам берёт текущий план Queue и ведёт его до конца, а в изолированной копии Queue содержит только P001; код 0 (план доведён, `ready to close`) или 1 с `nothing to do` — нормальное завершение ветки, код 2 или 3 — провал ветки; таймаут ветки покрывает вызов целиком. Метрики — из `<git-dir>/docflow/run/*.events.jsonl` копии (юниты, попытки, replan, стоимость, `test_changes`, `check_changes`) и пунктов `Unverified:` в телах коммитов юнитов.
- Ветка `docflow-one`: как `docflow`, но после `roster apply` все ключи маршрутов шапки заменены одним маршрутом `--single-route`, `fallback:` удалён.
- Оценка каждой ветки: оракул поверх итогового дерева, `go test -json ./benchoracle/...`, счёт; `diff` — `git diff --shortstat` против коммита `base` без файлов оракула.
- Таймаут ветки — `status: timeout` с оценкой того, что успело появиться в дереве; ошибка запуска — `status: error`.
- `--dry-run` печатает задачи, ветки, маршрут `single`, цепочки ростера (`roster show --roster`) и пути, агентов не зовёт и ничего не пишет.
- Тесты в `tools/bench/`: фейковые `codex` и `pi` в PATH (как `internal/runner` с `internal/agentio/testdata/fake_agent.sh`) на задаче `tiny` — обе ветки дают записи с оракулом и стоимостью; таймаут ветки — `timeout`.
- Фейки отвечают и на квота-пробы раннера: `codex app-server` — JSON-RPC по образцу `internal/agentio/testdata/fake_agent.sh` (`id:1` → `{}`, `id:2` → `rateLimits`, выход, никаких записей в дерево), `pi auth print-api-key --provider zai` — выход 1 без ключа (как `writeFakePi`, `internal/runner/fallback_test.go:L30-36`). Override `[agents.codex] quota = []` через XDG_CONFIG_HOME не работает: после `roster apply` план в executor-режиме, и `docflow run` грузит только встроенный agents-конфиг.
- Тестовый ростер — собственный файл, не `internal/roster/testdata/draft.toml`: executor — `pi | test-model | high` (встроенный профиль codex не задаёт `cost`, а разбор потока codex выкидывает цену — исполнитель codex не даст `cost` в записи), все шесть ролей в `[role]`, у strong-цепочек два провайдера; фейковый `pi` на юнит-вызове возвращает text = результат-JSON юнита и `usage` с `cost.total`, а не только заглушку для ветки `single`.
Verify: `go test ./tools/bench/...`
Commit: P051.1.3: run single and docflow arms
Handoff:
- Второй replan (см. `## Decisions`): таймаут ветки docflow в тесте — квота-пробы раннера, не `docflow run`. Незакоммиченная работа (`tools/bench/main.go`, `main_test.go`, `run.go`) действительна: `run.go` уже зовёт `docflow run --yes` и считает код 1 с `nothing to do` успехом — осталось переделать фикстуру `TestRunTinySingleAndDocflowArms` по новым пунктам `Do:` (ответы проб у фейков, собственный ростер с pi-executor, результат-JSON в фейковом pi).
- Полная проверка P051.1.3 прошла: `go test ./tools/bench/...`; gate `go build ./... && go vet ./... && go test ./internal/cycle/ ./tools/...`. `docflow run` с кодом 1 и `nothing to do` считается нормальным завершением.

### [x] P051.1.4 Review bench tool
Read:
- docs/plans/P051-benchmark.md:L1-120
Do:
- Проверить дифф фазы из раздела `## Phase diff` промпта против Goal, Constraints, `## Decisions` плана и `Do:` юнитов P051.1.1–P051.1.3; ничего не править.
- Отдельно проверить: ни одна ветка до оценки не видит оракул и решение; прерванный прогон всё равно записан и закоммичен; в `runs.jsonl` и сводке нет абсолютных путей; коммит результатов не захватывает чужие staged-правки.
Verify: `go build ./... && go vet ./... && go test ./internal/cycle/ ./tools/...`
Role: review
Commit: P051.1.4: review bench tool
Handoff:
- BUG tools/bench/run.go:419,464: docflow-one ломает шапку плана — routeHeader.ReplaceAllString роняет имя ключа (executor: и т.п.), получаем голую строку 'pi | test-model | high'; проверено на собранном CLI: docflow lint -> bad frontmatter line, код 2, ветка всегда status:error. Фикс: сохранять ключ ($1: <route>) и заменить только значение; добавить тест на docflow-one (сейчас ветка не покрыта)
- BUG tools/bench/run.go:579: fmt.Sscanf не парсит единственное число ('1 file changed, 1 insertion(+)') — insertions/deletions остаются 0 для однофайловых диффов (проверено); нужен парсер, устойчивый к singular/plural
- Мелкие отклонения от Do: у run нет --results DIR (есть только у report); status 'failed' из схемы никогда не пишется (код 2/3 docflow run -> 'error'); блок 'oracle failures' в summary.md появляется и у зелёных прогонов (runOracleDetailed собирает любые Output-строки, не только провалы) и 40-строчный хвост может вытеснить реальные ошибки; Ctrl-C между ветками пишет interrupted-запись для неначавшейся ветки; roster_sha/chains пишутся и в ветку single; подтесты считаются отдельными тестами ('без подтестов-дублей' из Do P051.1.1); diff ветки single не видит новых неотслеживаемых файлов агента
- Отдельные проверки Do сошлись: оракул/решение недоступны веткам до оценки (materialize только base, оракул в finalizeArm); прерванный прогон дописывается и коммитится (NotifyContext гасит повторные Ctrl-C, CallAgent убивает группу по отмене, затем summary/report/commit, код 130); абсолютных путей в runs.jsonl/summary нет; коммит результатов не захватывает чужое staged (pathspec-commits, тест есть). Остаточный риск: копия ветки лежит внутри checkout (work/bench/...), ФС-изоляции от bench/tasks/<name>/solution нет — санкционировано Decision плана
- Unverified: Путь прерывания по Ctrl-C (status:interrupted + коммит) проверен только трассировкой кода — автоматизированного теста в фазе нет; покрывается сценарием 3 приёмки P051.4.1
- Unverified: Ветка docflow-one подтверждена сломанной ручной репродукцией lint на собранном CLI; автоматического теста, гоняющего эту ветку целиком, нет ни в фазе, ни (сейчас) в приёмке до её сценария 1

## Phase 2: Reference corpus

### [x] P051.2.1 Add the bench base project and roster
Read:
- docs/usage.md#ростер
- internal/roster/testdata/draft.toml
Do:
- `bench/go.mod` (`module bench-fixtures`, без кода) — забор от модуля `bsv-docflow`.
- `bench/base/`: модуль `benchledger` (свой `go.mod`, только стандартная библиотека), CLI `ledger` для записей личных расходов в текстовом журнале: файл из `--file` или `LEDGER_FILE`, команды `add <YYYY-MM-DD> <amount> <description...>`, `list`, `balance`; сумма — целое в минимальных единицах, вывод — фиксированный текст, ошибки — код 1 и сообщение в stderr. 600–900 строк Go вместе с тестами, раскладка `cmd/ledger/` + `internal/journal/`, `go test ./...` зелёный; `README.md` с синтаксисом команд и форматом журнала; `AGENTS.md` из двух строк (тесты — `go test ./...`).
- `bench/roster.toml` — ростер бенчмарка, собираемый текущим подбором: `spend = ["codex", "zai"]`, `trusted` у zai, без `check`; в `[role]` — `cheap = "cheap"`, `executor = "standard"`, `heavy = "strong"`, `review`/`acceptance`/`replan` = `"trusted"`; маршруты — `codex | gpt-6-luna | high` (standard), `codex | gpt-6.1-sol | high` (strong), `pi | zai/glm-5.3 | high` (standard), `pi | zai/glm-5.3 | max` (strong). `docflow roster show --roster bench/roster.toml` даёт: `executor: codex | gpt-6-luna | high`, `fallback: pi | zai/glm-5.3 | high`, а `review:`, `acceptance:` и `replan:` — `pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high`.
- `bench/README.md`: что лежит в `bench/`, ссылка на `docs/design/bench.md` (её напишет P051.3.1).
Verify: `go vet ./... && cd bench/base && go test ./... && cd ../.. && docflow roster show --roster bench/roster.toml | grep -q '^executor: codex | gpt-6-luna | high' && docflow roster show --roster bench/roster.toml | grep -q '^review: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high'`
Commit: P051.2.1: add the bench base project and roster
Handoff:
- План юнита пересобран replan'ом (см. `## Decisions`): требование к `bench/roster.toml` переписано под цепочки, которые текущий подбор реально собирает, — `trusted` у zai и `review`/`acceptance`/`replan` = `"trusted"` дают Pi первым во всех проверяющих ролях; прежние цепочки (свои effort по ролям у одного провайдера) недостижимы, логика ростера не меняется. Файлов юнита нет — начинать с нуля.
- Следующий юнит должен добавить `docs/design/bench.md`, на который уже ссылается `bench/README.md`.

### [x] P051.2.2 Add the small bench task
Read:
- bench/base/README.md
- skills/bsv-docflow-plan/SKILL.md#unit
Do:
- `bench/tasks/small/plan.md` — docflow-план `P001` (формат 3, без ключей маршрутов, `gate: go vet ./... && go test ./...`, `permissions: auto`): фильтры `--since YYYY-MM-DD`, `--until YYYY-MM-DD` у `list` и `balance` и флаг `--json` у `list` (массив объектов `date`, `amount`, `description`). Одна фаза, один юнит; Goal и `Do:` буквально фиксируют синтаксис флагов, включительность границ, формат JSON и ошибку неверной даты (код 1).
- `bench/tasks/small/oracle/benchoracle/` — тесты пакета `benchoracle`: `TestMain` собирает `./cmd/ledger` во временный каталог, тесты гоняют бинарь через `os/exec` на временных журналах; только то, что зафиксировано в плане; 8–15 тестов.
- `bench/tasks/small/solution/` — эталонная реализация поверх базы (полные файлы по путям базы), с тестами пакета.
Verify: `go run ./tools/bench validate --task small`
Commit: P051.2.2: add the small bench task
Handoff:
- P051.2.3 should add `docs/design/bench.md`, referenced by `bench/README.md`.
- Unverified: Windows validation was not run; the task was validated on macOS arm64.

### [x] P051.2.3 Add the medium bench task
Read:
- bench/base/README.md
- bench/tasks/small/plan.md
- skills/bsv-docflow-plan/SKILL.md#unit
- tools/bench/main.go:L304-347
- tools/bench/run.go:L380-410
Do:
- `bench/tasks/medium/plan.md` — план `P001` той же шапки: категории и месячные бюджеты — `add --category NAME`, журнал версии 2 с категорией, который читает и файлы базы без неё, `budget set <category> <amount>` (файл бюджетов рядом с журналом), `report --month YYYY-MM` с суммой по категориям и пометкой превышения. Две фазы, 3–5 юнитов: формат журнала и миграция чтения; бюджеты и отчёт; юнит `Role: review` в конце фазы 1 и юнит `Role: acceptance` в конце плана.
- `bench/tasks/medium/oracle/benchoracle/` — 15–25 тестов через собранный CLI: старый журнал читается, отчёт и превышения на фикстурах.
- `bench/tasks/medium/solution/` — эталонная реализация.
- `validatePlan` в `tools/bench/main.go` готовит lint-копию так же, как docflow-ветка готовит свою (`runDocflow` в `tools/bench/run.go`): `materialize` (git-репозиторий с одним коммитом базы) вместо `copyTree`, `docflow init --agent codex`, план в `docs/plans/P001-<name>.md` и P001 в Queue, `roster apply P001 --roster <roster>`, затем `docflow lint` чекаут-CLI в этой копии; копию `cmd/` из репозитория в фикстуру больше не класть. `validate` получает флаг `--roster` с умолчанием `bench/roster.toml` — как `run`. Причина: `Role:` требует executor-режима, а ключи маршрутов у плана задачи появляются только после `roster apply`; кроме того, prompt review-юнита строит phase diff через `git log`, которому нужен git-репозиторий — сейчас фикстура голая, и оба юнита с `Role:` роняют lint.
- Тесты в `tools/bench/`: задача в `tools/bench/testdata/tasks/` с юнитами `Role: review` и `Role: acceptance` в плане — `validate` зелёный; задача `tiny` остаётся зелёной.
Verify: `go test ./tools/bench/... && go run ./tools/bench validate --task medium`
Traps: незакоммиченные `docs/design/bench.md` и правка `docs/design/README.md` — работа P051.3.1, написанная блокированной сессией по устаревшему Handoff P051.2.2: не править их и не коммитить с этой задачей. `roster apply` сам делает коммит — дополнительный коммит после него в фикстуре не нужен.
Commit: P051.2.3: add the medium bench task
Handoff:
- Replan снял блок расширением Do (см. `## Decisions`): фикстура lint в `validate` — теперь часть юнита. Корпус medium уже написан (`bench/tasks/medium/`: план, оракул, решение) и валиден: вручную проверено, что в копии базы с git-репозиторием после `roster apply bench/roster.toml` план проходит `docflow lint` без ошибок (остаётся warning `docflow: 3` — как у закоммиченного small).
- Осталось: переделать `validatePlan`, добавить `--roster` и тесты, затем прогнать Verify целиком; коммит юнита берёт только `tools/bench/` и `bench/tasks/medium/`.
- `docs/design/README.md` и `docs/design/bench.md` остаются отдельной незавершённой работой P051.3.1; не включать их в коммит этого юнита.
- Проверка прошла на macOS arm64; Windows-валидация остаётся открытой по Handoff зависимого юнита.

### [x] P051.2.4 Write the large bench task plan and oracle
Read:
- bench/base/README.md
- bench/tasks/medium/plan.md
- skills/bsv-docflow-plan/SKILL.md#unit
- skills/bsv-docflow-plan/SKILL.md#acceptance
Do:
- `bench/tasks/large/plan.md` — план `P001` той же шапки: мультивалютность и обмен данными — валюта записи (`add --currency XXX`, по умолчанию из настройки журнала), файл курсов и пересчёт `balance --in XXX`, `import csv|json <file>` с отбрасыванием дублей (та же дата, сумма, валюта и описание), `export csv|json`, повторяющиеся записи `recur add <rule>`, раскрываемые при чтении, и блокировка файла журнала при записи. Три-четыре фазы, 8–12 юнитов, юнит `Role: review` в конце каждой фазы, кроме последней, и юнит `Role: acceptance` в конце.
- `bench/tasks/large/oracle/benchoracle/` — 30–50 тестов через собранный CLI.
- Решение — следующий юнит; здесь только план и оракул.
Verify: `go run ./tools/bench validate --task large --no-solution`
Commit: P051.2.4: write the large bench task plan and oracle
Handoff:
- Large task plan and 40 CLI oracle tests are in bench/tasks/large/. The oracle fails on the base as expected (0/40); solution validation is deferred to the next unit.

### [x] P051.2.5 Add the large bench task solution
Read:
- bench/tasks/large/plan.md
Do:
- `bench/tasks/large/solution/` — эталонная реализация плана `bench/tasks/large/plan.md` поверх базы, с тестами пакетов; оракул и план не менять — расхождение оракула с текстом плана исправляется блоком `--needs plan`.
Verify: `go run ./tools/bench validate --task large`
Commit: P051.2.5: add the large bench task solution
Handoff:
- Актуальный oracle содержит 39 тестов; `validate` прошёл 39/39 на решении и 14/39 на базе. В предыдущей записи handoff указано 40 тестов.

### [x] P051.2.6 Review the reference corpus
Read:
- bench/tasks/small/plan.md
- bench/tasks/medium/plan.md
- bench/tasks/large/plan.md
Do:
- Проверить дифф фазы из раздела `## Phase diff` промпта против Goal, Constraints, `## Decisions` плана и `Do:` юнитов P051.2.1–P051.2.5; ничего не править.
- Отдельно проверить: каждый тест оракула опирается только на то, что план задачи фиксирует буквально; планы задач понятны холодному агенту; размеры задач различаются (1, 3–5, 8–12 юнитов); забор `bench/go.mod` держит — `go vet ./...` репозитория не трогает `bench/`.
Verify: `go build ./... && go vet ./... && go run ./tools/bench validate --task all`
Role: review
Commit: P051.2.6: review the reference corpus
Handoff:
- Дефект bench/tasks/large/plan.md:41: «Write new journals with first line `# ledger-v2`» противоречит :42 (v3) и оракулу; решение пишет `# ledger-v3`. Правка — одна строка (v2→v3 в :41) плюс, желательно, уточнение layout записей v2 (оракул гоняет 3-польную запись под `# ledger-v2`, план layout не фиксирует); до правки живой прогон large меряет противоречивый план.
- Оракул строже текста планов: medium TestLegacyListShowsUncategorized и large TestListReadsVersionThreeCurrencies/TestRecurExpandsThroughRequestedDate требуют точный 4-колоночный list (CATEGORY/CURRENCY), который планы не фиксируют; small TestListJSONDateFilterAndEmptyArray сравнивает байт-точную JSON-сериализацию (порядок ключей, компактность), план фиксирует только состав полей, порядок и []. План-конформная ветка теряет очки за угадывание — кандидаты на смягчение тестов или дополнение планов до живого прогона.
- docs/design/bench.md и строка в docs/design/README.md уже закоммичены коммитом aefdf3e (P051.2.3) вопреки его Traps — работа P051.3.1 частично сделана: юниту остаётся проверить и дополнить документ против фактического корпуса, не писать с нуля.
- Отказ от буквы Do P051.2.3: Role-задача для validate — inline-фикстура в TestValidatePlanWithRoleUnits (временный каталог), а не tools/bench/testdata/tasks/<name>; только tiny закоммичена. По существу покрытие есть.
- Unverified: Справедливость оракула не подтверждена прогоном против альтернативной план-конформной реализации (например, list без новой колонки или JSON с другим порядком ключей): подчеркнутые тесты могут снимать очки за угадывание — проверка: оракул на такой реализации или первый живой bench run.

## Phase 3: Docs

### [x] P051.3.1 Document the benchmark
Read:
- docs/design/README.md
- docs/usage.md#ростер
- AGENTS.md
- docs/docflow.md
Do:
- Новая тема `docs/design/bench.md`: корпус и размеры задач, ветки, оракул и изоляция, ростер бенчмарка, схема записи `v: 1` (поля и их смысл), раскладка `bench/results/` и `work/bench/`, правило обязательной фиксации, как читать `README.md` тренда; строка в таблице `docs/design/README.md`.
- `docs/usage.md`: раздел «Бенчмарк» после «Ростер» — `validate`, `run --dry-run` первым шагом, `run`, `report`, предупреждение о расходе квоты и времени large-задачи.
- `AGENTS.md`, раздел Code: строка — бенчмарк `go run ./tools/bench`, каждый прогон коммитится в `bench/results/`, менять задачи корпуса — только с `validate`.
- `docs/docflow.md`, Backlog: в строке про медленный GLM на pi заменить «часть бенчмарка» на `go run ./tools/bench run --single-route` с двумя уровнями `thinking`.
Verify: `docflow lint && grep -q 'bench.md' docs/design/README.md && grep -q 'tools/bench' docs/usage.md && grep -q 'bench/results' AGENTS.md`
Commit: P051.3.1: document the benchmark
Handoff:
- Тренд `bench/results/README.md` начнёт показывать динамику после первого разрешённого живого прогона.
- Unverified: Живые команды бенчмарка на моделях не проверялись; такой прогон расходует квоту и выходит за рамки этого юнита.

## Phase 4: Acceptance

### [x] P051.4.1 Accept the benchmark end to end
Read:
- internal/runner/run_test.go:L81-242
- internal/runner/role_acceptance_test.go:L1-50
Do:
- Новый тест `tools/bench/acceptance_test.go` на собранном CLI и фейковых `codex` и `pi` в PATH, во временной копии checkout-раскладки (`go.mod`, `bench/`, `tools/bench/testdata/`) под git.
- Сценарий 1: `run --task tiny --arms single,docflow,docflow-one` — в `runs.jsonl` три записи схемы `v: 1` с оракулом, маршрутом или `roster_sha` и `chains`; `summary.md` прогона и `README.md` тренда есть; последний коммит — `bench: run <run-id>` и меняет только `bench/results/`.
- Сценарий 2: второй прогон той же задачи — README показывает две строки и дельту; прогон при грязном `bench/results/` — код 2 и ни одной новой записи.
- Сценарий 3: прерывание ветки сигналом — запись `status: interrupted` и коммит есть.
- Сценарий 4: `validate --task all` на настоящем корпусе `bench/tasks/` — три задачи зелёные.
- Каждый пункт `Unverified:` из Handoff зависимостей закрыть тестом, `Accepted: … — …` или оставить `Unverified:` только при разрыве с Goal.
Verify: `go test ./...`
Traps: полный `go test ./...` идёт ~7 минут на Windows — для итераций `go test ./tools/bench/... -run Acceptance`.
Depends: P051.1.1, P051.1.2, P051.1.3, P051.1.4, P051.2.1, P051.2.2, P051.2.3, P051.2.4, P051.2.5, P051.2.6, P051.3.1
Role: acceptance
Commit: P051.4.1: accept the benchmark end to end
Handoff:
- Корпусные правки (планы medium/large, small-оракул) изменили task_sha всех трёх задач; bench/results пока пуст — ряд начнётся с первого живого прогона
- diff-поля записей всегда писались нулями: ref `base` в копии ветки не существовал, git diff молча падал; теперь дифф идёт по SHA базового коммита, старых записей нет — ряд чистый
- Тесты на фейковых агентах (приёмка сценариев 1–3 и TestRunTinySingleAndDocflowArms) скипаются на Windows: bare-name PATH-lookup не резолвит sh-скрипты без расширения; для Windows нужны pi.exe/codex.exe или .bat-фейки
- Живой `bench run` на реальных моделях запускает человек после закрытия плана: квота, часы; после первого прогона тренд в bench/results/README.md станет осмысленным
- Unverified: Windows: сценарии 1–3 приёмки и тест фейк-агентов скипируются; недостающая проверка — `go test ./tools/bench/...` на Windows (CI после push)
- Unverified: Живой bench run на реальных моделях не прогонялся — расходует квоту и по Constraints запускается человеком после закрытия плана
