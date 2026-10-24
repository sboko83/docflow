---
id: P035
title: z.ai quota
type: feature
status: active
created: 2026-10-24
gate: go test ./...
executor: pi | zai/glm-5.3-flash | max
fallback: codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
replan: claude | claude-opus-5-5 | high
permissions: auto
---
## Goal

`docflow run` знает квоту подписки z.ai для маршрутов pi с моделью `zai/…`:
печатает `quota: zai 5h NN% · 7d NN%` на старте и с дельтой после юнита,
пропускает исчерпанный маршрут z.ai в цепочке `fallback:` и при отказе
`limit` ждёт до времени сброса из квоты, а не фиксированные 30 минут.

## Context

- docs/design/runner.md:L160-170
- docs/design/runner.md:L218-226
- docs/design/runner.md:L314-330
- docs/design/runner.md:L355-368

Эндпоинт квоты (не описан в документации z.ai; им пользуется официальный
плагин z.ai `glm-plan-usage`): `GET
https://api.z.ai/api/monitor/usage/quota/limit`, заголовок `Authorization:
<ключ>` без `Bearer`, модель не вызывается и квоту не тратит. Живой ответ
от 2026-10-24 (форма для фикстур):

```json
{"code":200,"msg":"操作成功","success":true,"data":{"level":"pro","limits":[
 {"type":"CREDIT_LIMIT","unit":3,"number":5,"usage":2000,"currentValue":561,
  "remaining":1438,"percentage":28,"nextResetTime":1792877874249},
 {"type":"CREDIT_LIMIT","unit":6,"number":1,"usage":10000,"currentValue":3072,
  "remaining":6927,"percentage":30,"nextResetTime":1793393326999}]}}
```

`unit 3 / number 5` — окно 5 часов (сброс через ≤5 ч), `unit 6 / number 1`
— неделя (сброс через 6 дней). Ключ pi отдаёт командой
`pi auth print-api-key --provider zai` (stdout — ключ, код 0; нет
провайдера — код 1).

## Constraints

- Go, только стандартная библиотека (`net/http`, `encoding/json`).
- Ключ z.ai берётся только через `pi auth print-api-key --provider zai`;
  ключ не попадает ни в вывод, ни в ошибки, ни в логи вызовов, ни в файлы.
- URL эндпоинта — константа; переменная окружения
  `DOCFLOW_ZAI_QUOTA_URL` подменяет его только для тестов.
- Любой сбой зонда (нет `pi`, ненулевой код, HTTP не 200, таймаут
  `agentio.QuotaProbeTimeout`, невалидный JSON, `success` не `true`) —
  пустая квота, как у зондов Claude и Codex: прогон идёт дальше без строки.
- Маршруты pi с другим провайдером (модель без префикса `zai/`) и квоты
  Claude/Codex ведут себя как раньше; существующие тесты не меняются по
  смыслу.
- `open.bigmodel.cn` (Zhipu) вне плана.
- `docs/` — по-русски; код, комментарии, CHANGELOG — по-английски.

## Prerequisites

- Ключ z.ai известен pi (юниты плана идут на z.ai):
  `pi auth check --provider zai --no-refresh`

## Traps

- Квоты в раннере сейчас ключуются именем агента (`route.Agent`):
  `totals.quota`, `markQuotaRoutes`, `startQuota`, строки квоты юнита и
  replan. Маршрут pi с `zai/` должен получать имя `zai`, иначе
  исчерпание z.ai пометит недоступным pi на любом провайдере.
- Окна разбираются по `unit`/`number`, не по `type`: z.ai уже переименовал
  `TOKENS_LIMIT` в `CREDIT_LIMIT`. Неизвестный `unit` — окно пропускается.
- `QuotaWindow.Used` — доля (0..1), `percentage` — проценты: делить на
  100, как `CodexWindows`. `nextResetTime` — миллисекунды;
  `agentio.Epoch` их понимает.
- Метки окон совпадают с Claude/Codex (`5h`, `7d`): по метке считаются
  дельты и сравниваются сбросы (`progress.go`, `sameReset`).

## Decisions

- Квота z.ai именуется `zai` и привязана к маршрутам pi с моделью `zai/…`
  — квота принадлежит подписке z.ai, а не агенту pi — отвергнут общий ключ
  `pi`: он пометил бы исчерпанным pi на других провайдерах.
- Ключ — через `pi auth print-api-key --provider zai`: pi сам разрешает
  окружение и свой `auth.json` — отвергнуто чтение `~/.pi/agent/auth.json`
  (внутренний формат pi, `PI_CODING_AGENT_DIR`).
- Недокументированный эндпоинт `quota/limit` — другого способа узнать
  остаток без вызова модели нет, им пользуется плагин самого z.ai —
  отвергнут отказ от квоты pi.
- Повторный зонд после каждого вызова маршрута z.ai — поток pi квоту не
  несёт — отвергнут только стартовый замер: без него нет дельты за юнит и
  исчерпание посреди прогона не видно до отказа.
- Окна по `unit`/`number` — поле `type` уже менялось — отвергнут разбор
  по `type`.
- Reset хранит сырые миллисекунды float64 — agentio.Epoch и numericEpoch уже нормализуют ms, а number() не читает *float64 — отвергнуто хранение результата Epoch (указателя)
- Команда ключа переиспользует CallAgent с пустым log — тот же механизм таймаута/kill, что у зонда Claude, и ничего не пишет на диск — отвергнут голый exec.Command
- P035.1.2 переназначен на `codex | gpt-6-luna | max` — z.ai исчерпал окно
  5h до 2026-10-25 05:37, а Codex на маршруте `fallback:` блокирует юнит по
  несовпадению с `Executor: pi` — отвергнуто ждать сброса квоты z.ai
- Дефекты ревью P035.2.1 закрываются двумя юнитами по пакетам
  (P035.2.2 `agentio`, P035.2.3 `runner`) и повторным ревью P035.2.4 —
  правки независимы и проверяются своим `go test` — отвергнут один юнит на
  все пять дефектов и снятие блока с P035.2.1 без ревью исправлений
- Зонд z.ai держит один бюджет `timeout` на ключ и HTTP — Constraints
  называют таймаут зонда, а не этапа — отвергнуто узаконить 2× таймаут
- Неположительный timeout даёт пустую квоту без запуска pi — без бюджета нельзя гарантировать ограниченное время зонда — отвергнута трактовка нуля как отсутствия таймаута.

## Phase 1: Зонд и раннер

### [x] P035.1.1 Зонд квоты z.ai
Read:
- internal/agentio/quota.go:L60-90
- internal/agentio/quota.go:L225-245
- internal/agentio/output.go:L174-193
- internal/agentio/process.go:L25-35
- internal/agentio/process_test.go:L253-310
Do:
- Создать `internal/agentio/zai.go`: константа URL эндпоинта из
  `## Context`, подмена через `DOCFLOW_ZAI_QUOTA_URL`.
- `ParseZaiQuota(body []byte) Quota`: окна из `data.limits[]` по
  `unit`/`number` — `3/N` → метка `Nh`, `6/N` → метка `{7·N}d`, прочие
  пропустить; `Used = percentage/100`, `Reset = nextResetTime`; `success`
  не `true` или невалидный JSON — пустая `Quota`.
- `ProbeZaiQuota(timeout time.Duration) Quota`: запустить
  `pi auth print-api-key --provider zai` с таймаутом, обрезать пробелы;
  пустой ключ или ненулевой код — пустая `Quota`; `GET` с заголовками
  `Authorization: <ключ>` и `Accept: application/json`, общий таймаут
  `timeout`; HTTP не 200 или сбой — пустая `Quota`.
- Добавить `IsZaiModel(model string) bool` — префикс `zai/`.
- Тесты в `internal/agentio/zai_test.go`: ответ формы из `## Context`
  даёт `5h` 0.28 и `7d` 0.30 с ресетами; вариант с `type: TOKENS_LIMIT`
  разбирается так же; неизвестный `unit` пропущен; `success:false` —
  пусто; `httptest`-сервер проверяет заголовок `Authorization` равным
  ключу фейкового `pi` из `PATH`; нет `pi` в `PATH`, HTTP 500, зависший
  сервер — пусто за время не больше таймаута.
Verify: `go test ./internal/agentio/`
Traps: ключ не включать в текст ошибок и в `t.Log`/`t.Fatal` тестов.
Depends: -
Commit: P035.1.1: add z.ai quota probe
Handoff:
- ProbeZaiQuota идёт в два этапа последовательно (pi-ключ, затем HTTP GET), каждому передаётся полный timeout — худшее стенное время 2×QuotaProbeTimeout; учесть для стартовой строки квоты в раннере
- QuotaWindow.Reset хранит сырые миллисекунды nextResetTime как float64 (nil, если поля нет): agentio.Epoch и numericEpoch раннера сами делят ms; нельзя класть указатель *float64 от Epoch — number() указатели не читает
- Окно без числового percentage пропускается целиком (не показывать 0%); метки ровно 5h/7d — дельты и sameReset сравнивают по метке
- Тесты зонда zai скипаются на Windows, как остальные PATH-зависимые тесты зондов

### [x] P035.1.2 Квота z.ai в docflow run
Read:
- internal/agentio/zai.go
- internal/runner/run.go:L50-90
- internal/runner/run.go:L296-385
- internal/runner/run.go:L553-606
- internal/runner/run.go:L650-668
- internal/runner/replan.go:L168-180
- internal/runner/replan.go:L255-330
- internal/runner/chain.go:L170-245
- internal/runner/fallback_test.go:L100-160
- internal/runner/routes_test.go:L105-130
- docs/usage.md:L380-390
- docs/usage.md:L445-450
Do:
- Добавить в `internal/runner` функцию имени квоты маршрута: `zai`, если
  `route.Agent == "pi"` и `agentio.IsZaiModel(route.Model)`, иначе
  `route.Agent`. Использовать её везде, где квота ищется или пишется по
  маршруту: `startQuota`, `markQuotaRoutes`, `mergeQuota` после вызова,
  строки квоты юнита и replan.
- `startQuota`: имя `zai` опрашивается `agentio.ProbeZaiQuota`; для
  прочих имён — профиль `quota`, как сейчас.
- После каждого вызова агента на маршруте z.ai (юнит и replan) — новый
  зонд и `mergeQuota` под именем `zai`: дельта за юнит и пометка
  исчерпания перед следующим юнитом.
- Отказ `limit` на маршруте z.ai с `refusal.Reset == nil`: свежий зонд,
  `Reset` — самый поздний `nextResetTime` среди окон с `Used >= 1`; таких
  нет — оставить `nil` (действует `limit_wait` профиля). Места:
  `agentio.Classify` в `run.go` и `replan.go`.
- Тесты в `internal/runner`: строка старта `quota: zai 5h 28% · 7d 30%`;
  окно 5h на 100% со сбросом в будущем — `zai: quota exhausted until
  HH:MM, skipped` и юнит на маршруте `fallback:`; маршрут pi без `zai/`
  не опрашивается (сервер считает запросы) и не пропускается; отказ
  `limit` без времени в тексте ждёт до сброса из зонда.
- `docs/design/runner.md`: в разделе квоты (L314-330) — зонд z.ai для
  маршрутов pi с `zai/` и повторный зонд после вызова; в абзаце профиля
  pi (L355-368) заменить «Ключа `quota` нет» на исключение для z.ai.
  `docs/usage.md` — строка `quota` таблицы ключей (L448): у pi квота
  встроена для `zai/`, ключ профиля не нужен.
- `CHANGELOG.md`, `## [Unreleased]` → `### Added`: квота z.ai в
  `docflow run`.
Verify: `go test ./...`
Traps: `name` в `run.go` при `mergeQuota` из потока — имя агента для
Claude/Codex; для z.ai поток квоту не несёт, не сливать пустое под
`pi`. Статус-строка квоты печатает имена в порядке `totals.names` —
`zai` встаёт туда при стартовом замере. В дереве — незакоммиченная
частичная реализация первой попытки (pi): `chain.go`, `run.go`,
`replan.go`, `fallback_test.go`, `zai_test.go`; `go test ./...` по ней не
запускался, `docs/design/runner.md`, `docs/usage.md` и `CHANGELOG.md` не
тронуты. Продолжить на ней, сверив каждый пункт `Do:`.
Executor: codex | gpt-6-luna | max
Commit: P035.1.2: show and use z.ai quota in run
Handoff:
- Зонд получает ключ и выполняет HTTP-запрос последовательно; каждый этап использует полный QuotaProbeTimeout, поэтому стартовый зонд может занять до двух таймаутов.
- Unverified: Живые ключ pi и эндпоинт z.ai не проверялись; интеграционные тесты использовали фиктивный ключ и локальный HTTP-сервер.

## Phase 2: Ревью

### [-] P035.2.1 Ревью фазы 1
Read:
- internal/agentio/zai.go
- docs/design/runner.md:L314-368
Do:
- Получить дифф фазы: `git diff "$(git log --format=%H --grep='^P035\.1\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Сверить дифф с `## Goal`, `## Constraints`, `## Decisions` и каждым
  пунктом `Do:` юнитов P035.1.1 и P035.1.2: утечка ключа (вывод, ошибки,
  логи, тесты), имя квоты `zai` во всех местах, где раньше был
  `route.Agent`, разбор по `unit`/`number`, сбой зонда — пустая квота,
  тест на каждый пункт `Do:`.
- Прогнать `go test ./...` и `go vet ./...`.
- Код не править. Дефект — поведение расходится с Goal/Constraints, нет
  теста на пункт `Do:`, утечка ключа — ответ `blocked`, `blocker: "plan"`,
  в `reason` каждый дефект с `file:line`: replan добавит юнит исправления.
  Замечания без дефекта — в Handoff `Unverified:`.
Verify: `go test ./... && go vet ./...`
Depends: P035.1.2
Executor: codex | gpt-6.1-sol | high
Commit: P035.2.1: review z.ai quota phase
Handoff:
- Blocked (plan): 1. internal/agentio/zai.go:89,93: этапы получения ключа и HTTP используют полный timeout каждый вместо общего бюджета. Воспроизведение: квота возвращена через 2,69 с при timeout=2 с. internal/agentio/zai_test.go:109 допускает 3 с при timeout=200 мс и нарушение не выявляет. 2. internal/runner/chain.go:98,150: использованные маршруты по-прежнему учитываются через route.Agent. При цепочке pi другого провайдера → pi zai/ и вызове только первого agentsUsed() возвращает [pi zai], ошибочно включая квоту z.ai в строку юнита. Воспроизведено. 3. internal/runner/zai_test.go:95,172: ответы до и после вызова одинаковые; проверяется только (+0). Нет тестов изменения квоты, ненулевой дельты и пропуска z.ai перед следующим юнитом после исчерпания посреди прогона — требований P035.1.2. 4. internal/runner/replan.go:327 и internal/runner/chain.go:259: отсутствуют тесты получения Reset при limit в replan и сохранения nil при отсутствии исчерпанных окон. Имеющийся тест internal/runner/zai_test.go:140 покрывает только обычный юнит с исчерпанными окнами. 5. internal/agentio/zai_test.go:56: фейковый pi всегда успешно возвращает ключ. Не проверены требования P035.1.1: пустой/пробельный ключ, ненулевой код при непустом stdout и таймаут команды получения ключа.

### [x] P035.2.2 Общий таймаут зонда z.ai и тесты ключа
Read:
- internal/agentio/zai.go:L80-140
- internal/agentio/zai_test.go
- internal/agentio/process.go:L25-35
Do:
- `ProbeZaiQuota` (`internal/agentio/zai.go:L88-99`): один бюджет
  `timeout` на оба этапа — срок `time.Now().Add(timeout)`, команда ключа
  получает `timeout`, HTTP-запрос — остаток до срока; остаток ≤ 0 —
  пустая `Quota` без запроса.
- Тест общего бюджета: фейковый `pi` спит ~0,6 таймаута и печатает ключ,
  `httptest`-сервер отвечает валидным телом формы из `## Context` через
  ~0,6 таймаута — результат пустая `Quota`, время не больше таймаута плюс
  запас. До правки этот тест возвращает квоту.
- `TestProbeZaiQuotaHangingServer` (`zai_test.go:L98-112`): граница
  времени — таймаут плюс небольшой запас, а не 3 с при таймауте 200 мс.
- `fakeZaiPi` параметризовать скриптом; тесты: ключ из пробелов и
  переводов строк — пустая `Quota`, сервер не получил запроса; код 1 при
  непустом stdout — пустая `Quota`, запроса нет; `pi` дольше таймаута —
  пустая `Quota` не позже таймаута плюс запас, запроса нет; ключ с
  пробелами вокруг — заголовок `Authorization` равен ключу без пробелов.
Verify: `go test ./internal/agentio/`
Traps: ключ не включать в текст ошибок и в `t.Log`/`t.Fatal`. Тесты с
фейковым `pi` скипаются на Windows, как `fakeZaiPi`. Handoff P035.1.1 и
P035.1.2 про «до двух таймаутов» после юнита неверен — не переносить его
в docs. Фича не выпущена: запись `CHANGELOG.md` не нужна.
Depends: P035.1.2
Executor: codex | gpt-6-luna | max
Commit: P035.2.2: bound z.ai probe by one timeout
Handoff:
- Живые pi auth и endpoint z.ai не проверялись; использовались фиктивный ключ и локальный httptest-сервер.
- Полный verify прошёл; шаги done и commit оставлены runner'у.
- Unverified: Интеграция с реальными pi auth и endpoint z.ai не запускалась; проверены пакетные тесты и проектный gate.

### [ ] P035.2.3 Учёт маршрутов z.ai и тесты раннера
Read:
- internal/runner/chain.go:L90-190
- internal/runner/chain.go:L236-265
- internal/runner/zai_test.go
- internal/runner/run.go:L600-680
- internal/runner/replan.go:L300-335
- internal/runner/fallback_test.go:L100-160
Do:
- `executorChain.used` (`chain.go:L98`, `L150`) ключевать
  `quotaName(route)`, а не `route.Agent`: цепочка `pi <не zai>` →
  `pi zai/…`, вызван только первый — `agentsUsed()` равно `[pi]`, без
  `zai`; обратный случай — `[zai]`. Тест на обе цепочки.
- Тест дельты: сервер отдаёт 28/30 на старте и 40/30 после вызова —
  строка юнита `quota: zai 5h 40% (+12) · 7d 30% (+0)`.
- Тест исчерпания посреди прогона: два юнита на `pi | zai/…`, зонд после
  первого юнита — 5h 100% со сбросом в будущем; второй юнит —
  `zai: quota exhausted until HH:MM, skipped` и выполнен маршрутом
  `fallback:`. Не проходит — довести пометку исчерпания перед следующим
  юнитом в `run.go`.
- Тест replan: маршрут `replan:` на `pi | zai/…`, отказ `limit` без
  времени в тексте, `--no-wait` — время в сообщении совпадает со сбросом
  из ответа сервера (`replan.go:L327`).
- Тест `nil`: отказ `limit` на z.ai без времени в тексте, свежий зонд без
  окон с `Used >= 1` — ожидание по `limit_wait` профиля, не по сбросу
  окна (`chain.go:L258`).
Verify: `go test ./...`
Traps: тела сервера в `zai_test.go` — очередь `server.bodies` по
запросам; число зондов (старт + после каждого вызова z.ai) задаёт длину
очереди. Ожидание проверять с `--no-wait`, без настоящего сна. Фича не
выпущена: запись `CHANGELOG.md` не нужна.
Depends: P035.1.2
Executor: codex | gpt-6-luna | max
Commit: P035.2.3: fix z.ai route usage and tests

### [ ] P035.2.4 Повторное ревью фазы 1
Read:
- internal/agentio/zai.go
- internal/runner/chain.go:L90-190
- docs/design/runner.md:L314-368
Do:
- Получить дифф: `git diff "$(git log --format=%H --grep='^P035\.1\.' | tail -1)^" HEAD -- . ':!docs/plans'`.
- Проверить, что закрыт каждый из пяти дефектов Handoff P035.2.1 —
  поведение и тест, который до правки падал бы.
- Повторить сверку P035.2.1: `## Goal`, `## Constraints`,
  `## Decisions`, каждый пункт `Do:` юнитов P035.1.1, P035.1.2, P035.2.2,
  P035.2.3; утечка ключа; имя `zai` везде, где раньше был `route.Agent`.
- Прогнать `go test ./...` и `go vet ./...`.
- Код не править. Дефект — `blocked`, `blocker: "plan"`, в `reason`
  каждый дефект с `file:line`. Замечания без дефекта — в Handoff
  `Unverified:`.
Verify: `go test ./... && go vet ./...`
Depends: P035.2.2, P035.2.3
Executor: codex | gpt-6.1-sol | high
Commit: P035.2.4: re-review z.ai quota phase

## Phase 3: Приёмка

### [ ] P035.3.1 Приёмка квоты z.ai через docflow run
Read:
- internal/runner/routes_test.go:L105-130
- internal/runner/fallback_test.go:L100-160
- internal/agentio/zai.go
Do:
- Создать `internal/runner/zai_acceptance_test.go`: настоящий
  `docflow run` на фикстуре плана с маршрутом
  `pi | zai/glm-5.3 | max`, фейковый `pi` в `PATH` (на
  `auth print-api-key` печатает ключ, иначе ведёт себя как агент через
  `agentio/testdata/fake_agent.sh`), `httptest`-сервер с ответом формы из
  `## Context`, `DOCFLOW_ZAI_QUOTA_URL`.
- Сценарий 1: старт печатает `quota: zai 5h 28% · 7d 30%`, после юнита —
  строка с дельтой.
- Сценарий 2: окно 5h на 100% — `zai: quota exhausted until HH:MM,
  skipped`, юнит выполнен маршрутом `fallback:`.
- Сценарий 3: сервер отвечает 500 и, отдельно, `pi` без ключа — прогон
  завершается кодом 0 без строки `quota: zai`.
- Сценарий 4: маршрут `pi | local/x | -` — сервер не получил ни одного
  запроса.
- Сценарий 5: отказ `limit` на z.ai без времени сброса в тексте,
  `--no-wait` — время в сообщении совпадает со сбросом из ответа сервера.
- Сценарий 6: ключ фейкового `pi` не встречается ни в stdout/stderr, ни в
  логах вызовов `<git-dir>/docflow/run/`.
- Закрыть тестом каждый `Unverified:` из Handoff P035.1.1, P035.1.2,
  P035.2.2–P035.2.4 или оставить его в своём Handoff.
Verify: `go test ./...`
Traps: упавший сценарий чинится в этом юните, если правка мала; иначе
`blocked` с причиной.
Depends: P035.1.1, P035.1.2, P035.2.4
Executor: claude | claude-opus-5-5 | high
Commit: P035.3.1: accept z.ai quota in run
