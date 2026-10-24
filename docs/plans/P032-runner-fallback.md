---
id: P032
title: Runner fallback executors and run hygiene
type: feature
status: active
created: 2026-10-24
executor: codex | gpt-6-luna | max
permissions: auto
replan: codex | gpt-6.1-sol | high
gate: go test ./...
---
## Goal
Отказ провайдера — лимит подписки или недоступность — переводит юнит
`docflow run` на следующего исполнителя из `fallback:` плана; ждать раннер
начинает, только когда недоступна вся цепочка. Отказы Claude, Codex и pi
(z.ai) различаются по классам. Таймаут из плана гасит всё дерево процессов
агента; полный verify идёт один раз на дерево, его лог провала сохраняется в файл.

## Context
- docs/design/runner.md
- docs/design/format.md#план
- internal/runner/run.go
- internal/agentio/usage.go

Повод — прогон RMG P009 23.10.2026 на `pi | zai/glm-5.3 | max`.

Юнит P009.3.1 шёл 1ч34м, из них около 70 мин заняли сборки и тесты. Полный
`docflow verify` (gate RMG: бандлы, `./rmg test dfm`, `./rmg test maximum`,
10–13 мин) запускался пять раз: три раза агентом в попытке 1, причём третий
прогон убил таймаут 3600 с; один раз агентом в попытке 2; один раз раннером
на том же дереве после зелёного verify агента. Агент перезапускал 12-минутный
verify, потому что `| tail -25` скрыл причину провала. Он следует
`RunnerCycle`: «Check your work with `docflow verify {id}`; repeat until it
passes».

После таймаута попытки 1 процессы её verify остались живы: попытка 2 нашла
`sh -c tools/check-release-bundle.sh DFM && … ./rmg test dfm …` (pid 23049,
9772) и убила их сама. Раннер шлёт SIGTERM/SIGKILL группе процессов агента
(`internal/procgroup/procgroup_unix.go`), но потомки в своей сессии или
группе сигнал не получают. Гипотеза, не проверена: bash-инструмент pi
запускает команду с `detached: true`.

Лог первого вызова P009.3.1 (блок, 42 мин) перезаписан повторным
`docflow run` после replan: имя `<uid>.<calls>.out` считает вызовы внутри
одного `runUnit`.

Лимит z.ai раннер не распознал: regex `limit` профиля pi
(`usage limit|rate limit|hit your limit`) не совпал, время сброса — дата без
зоны, а раннер читает только epoch. Две попытки ушли впустую, хотя сброс
был через 46 ч. Подлинный ответ, `P009.3.2.2.out`: событие `message_end`
assistant; pi сам повторил запрос 3 раза, `auto_retry_end` с тем же
`finalError`; выход 0:

```json
{"type":"message_end","message":{"role":"assistant","content":[],"api":"openai-completions","provider":"zai","model":"glm-5.3","stopReason":"error","timestamp":1792788477151,"errorMessage":"429: {\"code\":\"1310\",\"message\":\"Weekly/Monthly Limit Exhausted. Your limit will reset at 2026-10-25 22:08:58\"}"}}
{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"429: {\"code\":\"1310\",\"message\":\"Weekly/Monthly Limit Exhausted. Your limit will reset at 2026-10-25 22:08:58\"}"}
{"type":"agent_settled","aborted":false}
```

Коды z.ai по стороннему описанию (официальная таблица не найдена): 1308 —
лимит окна, сообщение называет время сброса; 1310 — недельный/месячный
лимит; 1305 — частота запросов. Настоящих отказов Claude и Codex на машине
нет — только `rate_limit_event` со `status: allowed_warning` и полем
`unifiedWindows`. Claude CLI пишет ошибки API (`rate_limit_error`,
`overloaded_error`) текстом ответа assistant (issue anthropics/claude-code
#53461). Формат отказа Codex по лимиту не задокументирован; раннер уже
читает rollout-файл (`rate_limit_reached_type`, `used_percent` ≥ 100).

## Constraints
- Go, только стандартная библиотека; `go test ./...` зелёный после
  каждого юнита.
- Версия формата остаётся 2: `fallback:` и `timeout:` — необязательные
  ключи шапки. Старый CLI предупреждает `unknown frontmatter key` и работает
  как раньше.
- План без `fallback:` ведёт себя как сейчас: цепочка из одного исполнителя.
- Ключи профиля `limit`/`limit_wait` пользовательского конфига (legacy-режим)
  продолжают работать.
- Windows: `internal/procgroup/procgroup_windows.go` не меняется.
- Формат, `templates/` и `lint` меняются вместе; `README.md` и
  `README.ru.md` — вместе; `docs/` на русском, код, комментарии и
  `CHANGELOG.md` — на английском.

## Traps
- Агенты в тестах — исполняемые заглушки `claude`/`codex`/`pi` в `PATH`
  (образец `internal/runner/routes_test.go:L120-200`): режим исполнителей
  берёт только встроенные адаптеры, конфиг с fake-агентом в нём не работает.
- Сессия принадлежит агенту: id сессии pi нельзя отдать в `resume_args`
  codex. При смене исполнителя — новая сессия и полный промпт.
- Regex и правила отказов применяются только к ответу с ошибкой (флаг
  `error` или код ≠ 0): агент может цитировать файл про «rate limit».

## Decisions
- Цепочка запасных — ключ шапки плана `fallback:`, тройки через `;` — план
  выбирает исполнителя, dry-run проверяет маршруты
  ([P021](../archive/P021-plan-executors.md)) — отвергнут конфиг машины:
  режим исполнителей не читает пользовательский конфиг.
- Три класса отказа: `limit` (известен или оценён срок сброса), `outage`
  (5xx, 529, нет соединения, локальная модель не запущена), `throttle`
  (частота запросов) — у них разная реакция — отвергнут один regex `limit`.
- Ждать — только когда вся цепочка недоступна, до ближайшего срока —
  отвергнуто ожидание основного исполнителя при живых запасных.
- Ключ доступности — `agent|model`; общая подписка двух моделей одного
  провайдера выясняется одним быстрым отказом — отвергнут ключ по
  провайдеру: нужен каталог моделей, которого в коде нет.
- Возврат к основному — только на границе юнита; смена посреди юнита —
  новая сессия поверх незакоммиченного дерева — отвергнут возврат посреди
  юнита: ещё одна потеря сессии.
- Время сброса z.ai без зоны читается как UTC+8: ранний возврат стоит
  одного короткого отказа, поздний — простоя — отвергнуты UTC и локальная
  зона (обе дают срок позже).
- Таймаут — по стене, ключ `timeout:` — отвергнут таймаут простоя:
  `xcodebuild` молчит по 13 мин.
- Раннер переиспользует запись verify агента при совпавшем отпечатке
  дерева, кроме записи `verify --skip` — та же запись, которой уже доверяет
  `done` — отвергнут обязательный повтор verify раннером.
- Квота до юнита пропускает исполнителя только с окном ≥ 100% — отвергнут
  порог 97%: юнит часто успевает закончиться.
- replan при отказе идёт по той же цепочке `fallback:` — отвергнута
  остановка прогона из-за лимита исполнителя replan.
- Kill ждёт завершения снимка дерева и только затем возвращает управление — агент может завершиться раньше detached-потомка — отвергнут немедленный возврат по `cmd.Wait`.
- Номер лога берётся после наибольшего существующего номера этого юнита и типа вызова — локальные счётчики сбрасываются при новом Run — отвергнут локальный счётчик.
- VerifiedCurrent принимает resolver вложенных репозиториев — runner должен сравнивать тот же fingerprint, что и verify, не создавая зависимости cycle от lint — отклонён fingerprint только корневого репозитория
- Коды z.ai: 1302 → throttle, 1305 → outage — [официальная таблица](https://docs.z.ai/api-reference/api-code) относит их к лимиту запросов и временной перегрузке соответственно — отклонено стороннее соответствие 1305 частотному лимиту.
- Сигналы встроенных исполнителей проверяются до regex профиля — так сохраняется классификация throttle и outage, а regex остаётся для legacy-конфига — отклонён порядок с regex первым.

## Phase 1: Прогон без лишних часов

### [x] P032.1.1 Остановка всего дерева процессов агента, уникальные логи вызовов
Read:
- internal/procgroup/procgroup_unix.go
- internal/procgroup/procgroup_unix_test.go
- internal/agentio/process.go:L154-226
- internal/runner/run.go:L505-540
- internal/runner/replan.go:L225-250
- docs/design/runner.md:L193-204
Do:
- Проверить гипотезу о pi: в установленном пакете pi (`readlink -f "$(command -v pi)"`, затем
  исходники bash-инструмента рядом) найти, как запускается команда
  (`detached`, `setsid`). Итог — в handoff; правка ниже нужна в любом случае.
- Добавить тест `TestKillStopsDetachedDescendants` в
  `internal/procgroup/procgroup_unix_test.go`: процесс-агент запускает
  внука в новой сессии (`perl -e 'use POSIX; setsid(); sleep 300'`),
  после `Kill` внук мёртв. Тест падает на текущем коде.
- В `Kill` до SIGTERM снять снимок `ps -A -o pid=,ppid=,pgid=`, собрать
  потомков агента транзитивно по `ppid`, послать SIGTERM группе агента и
  каждой другой группе потомков; через grace — SIGKILL тем же группам и
  pid. Группу самого раннера (`syscall.Getpgrp()`) не трогать.
- Убедиться, что этот же путь срабатывает на Ctrl-C/SIGTERM/SIGHUP
  раннера, а не только на таймауте (`CallAgent` с отменённым `Context`).
- Имя лога вызова: `<uid>.<n>.out` и `<uid>.replan<n>.out`, где `n` —
  следующий свободный номер в каталоге `run/`, а не счётчик вызовов
  внутри `runUnit`/`replanUnit`. Тест `TestRunLogNamesUnique`: два
  запуска `Run` одного юнита оставляют два разных лога.
- Обновить абзац «Таймаут» в `docs/design/runner.md` и имя лога вызова там,
  где оно описано; запись в `CHANGELOG.md` `## [Unreleased]` → `Fixed`.
Verify: `go test ./internal/procgroup/ ./internal/runner/ -run 'TestKillStopsDetachedDescendants|TestRunLogNamesUnique' -count=1`
Traps: `ps` на macOS и Linux принимает `-A -o pid=,ppid=,pgid=`; после смерти агента его дети уходят к launchd/init — снимок снимается до первого сигнала. `setsid` в perl на macOS есть через POSIX, `setsid(1)` — нет.
Commit: P032.1.1: kill agent descendants, unique call logs
Handoff:
- На macOS `docflow verify P032.1.1` и его gate `go test ./...` прошли.
- Установленный Pi 1.4.0 запускает shell в `dist/core/tools/bash.js` с `detached: true` на не-Windows системах; гипотеза о новой группе процессов подтверждена.

### [x] P032.1.2 Verify: один прогон на дерево, полный лог провала
Read:
- internal/agents/agents.go:L137-175
- internal/cycle/verify.go:L33-140
- internal/cycle/verify.go:L200-240
- internal/cycle/verify.go:L300-380
- internal/runner/run.go:L670-700
- skills/bsv-docflow-run/SKILL.md:L30-50
- docs/design/runner.md:L35-66
Do:
- Переписать шаг 2 `RunnerCycle`: итерации — команда `Verify` юнита или
  `docflow verify {id} --no-gate`; полный `docflow verify {id}` — один раз
  перед ответом, раннер переиспользует его запись при неизменном дереве.
  Вывод длинных проверок не резать `tail`/`grep`: провал verify печатает
  путь к полному логу.
- `docflow verify`: полный stdout+stderr каждого прогона писать в
  `<git-dir>/docflow/run/<id>.verify.log` (перезапись); при провале
  последней строкой печатать `log: <путь от корня>`.
- В `internal/cycle` экспортировать `VerifiedCurrent(root, unitID)`: true,
  когда сохранённый отпечаток есть, записи `.skip` нет, gate прогонялся
  (запись `--no-gate` не делается) и отпечаток равен текущему.
- В `runUnit` перед `cycle.Self(root, "verify", uid)`: `VerifiedCurrent` →
  строка `<uid>: verify reused (tree unchanged since the agent's verify)`
  и переход к `done`; иначе как сейчас. В отклик агенту при провале
  verify раннера добавить путь к логу.
- Тесты: `TestRunReusesAgentVerify` — агент-заглушка запускает
  `docflow verify`, gate дописывает строку в файл-счётчик, после юнита в
  счётчике одна строка; `TestRunIgnoresSkipRecord` — запись
  `verify --skip` не переиспользуется.
- Согласовать раздел режима раннера в `skills/bsv-docflow-run/SKILL.md`;
  обновить цикл в `docs/design/runner.md`; запись в `CHANGELOG.md` →
  `Changed`.
Verify: `go test ./internal/cycle/ ./internal/runner/ -run 'TestRunReusesAgentVerify|TestRunIgnoresSkipRecord|VerifyLog' -count=1`
Traps: `DOCFLOW_RUNNER_UNIT` запрещает агенту `finish`/`done`/`commit`/`block`, но не `verify` — так и остаётся. Запись `verify --skip` агента не должна обходить verify раннера.
Commit: P032.1.2: reuse agent verify, keep verify log
Handoff:
- `docflow` в PATH — установленная сборка от 23.10.2026; для проверки текущего исходника используй `go run ./cmd/docflow` или пересобери `bin/docflow`.
- Sandbox запрещает `/bin/ps` и часть Unix process-group проверок; полный gate прошёл в разрешённом запуске.

## Phase 2: Классы отказов

### [x] P032.2.1 Классификация отказов агента
Read:
- internal/agentio/usage.go
- internal/agentio/quota.go:L87-140
- internal/agentio/quota.go:L244-300
- internal/agentio/testdata/pi.jsonl
- internal/agents/agents.go:L50-125
- internal/runner/run.go:L596-625
- internal/runner/replan.go:L265-295
- docs/design/runner.md:L176-192
Do:
- Создать `internal/agentio/refusal.go`: `type Refusal struct { Kind
  string; Reset *float64; Text string }`, `Kind` — `limit|outage|throttle`;
  `Classify(agent map[string]any, run AgentRun, now time.Time) *Refusal` —
  nil для обычного провала и для ответа без ошибки.
- Правила, первое совпавшее:
  - claude: `rate_limit_event` со `status: rejected` → `limit`,
    `resetsAt` (логика из `LimitReset`); текст ошибки с `rate_limit_error`
    или `429` → `limit`, срок — `UsageReset` по тексту; `overloaded_error`,
    `529`, `5\d\d`, `api_error` → `outage`.
  - codex: rollout-файл (`CodexLimits`) → `limit`, как сейчас; текст
    `usage limit`, `usage_limit_reached`, `429` → `limit`;
    `stream disconnected`, `5\d\d`, `overloaded` → `outage`.
  - pi: поле `error` (`errorMessage`) вида `<HTTP>: <тело>`; тело JSON с
    `code` z.ai: `1308`, `1310` → `limit`, срок из
    `reset at YYYY-MM-DD HH:MM:SS` как UTC+8; `1305` → `throttle`; прочий
    `429` → `limit`; `5\d\d`, `fetch failed`, `ECONNREFUSED`,
    `connection error`, `terminated` → `outage`.
  - Любой агент с ключом `limit` в профиле: совпадение regex → `limit`,
    срок по `LimitUntil` (`limit_wait`).
- Заменить пару `LimitReset`/`LimitText` в `runUnit` и `replanUnit` на
  `Classify`: `limit` — нынешнее ожидание `WaitLimit` до сброса + 60 с;
  `throttle` — 30 с и та же сессия, четвёртый подряд считается `outage`;
  `outage` — 600 с и та же сессия. Ни один класс не тратит попытку;
  `LimitTries` считает отказы всех классов подряд, `--no-wait` — код 7.
  Строка прогона называет класс: `usage limit until HH:MM`,
  `provider unavailable, retry at HH:MM`, `throttled, retry in 30 s`.
- Тестовые данные: `internal/agentio/testdata/pi-limit-zai.jsonl` из
  ответа z.ai в `## Context` плана P032 (`docs/plans/P032-runner-fallback.md`);
  синтетические образцы claude/codex/pi-outage — в тестах с `Synthetic` в
  имени. `TestClassify` — таблица по всем правилам, включая обычный
  ответ, где агент цитирует «rate limit».
- Оставить regex `limit` встроенных профилей для legacy-конфига, добавить
  pi `limit exhausted`.
- Переписать абзац «Лимит подписки» в `docs/design/runner.md` в таблицу
  классов (класс, признаки по агентам, реакция) со ссылкой на источник
  каждого признака; удалить из профиля pi фразу «строки `limit` … для z.ai
  не проверены»; запись в `CHANGELOG.md` → `Fixed` (лимит z.ai), `Added`
  (классы отказов).
Verify: `go test ./internal/agentio/ -run 'TestClassify' -count=1`
Traps: ошибка API pi выходит с кодом 0, текст виден только через поле `error`. pi сам повторяет 429 трижды (`auto_retry_end`) — классифицируется итог. Время z.ai в образце — 2026-10-25 22:08:58 UTC+8 = 1792937338 epoch.
Commit: P032.2.1: classify agent refusals
Handoff:
- Реальных отказов Claude и Codex на машине нет; их ветки классификатора покрыты синтетическими ответами. Если появятся живые логи, сверить фактический формат событий.
- Unverified: Живые ответы с отказами Claude и Codex не проверены; на машине таких отказов нет.

## Phase 3: Запасные исполнители

### [x] P032.3.1 Ключи `fallback:` и `timeout:` в шапке плана
Read:
- internal/lint/lint.go:L25-35
- internal/lint/lint.go:L400-530
- internal/lint/lint.go:L750-770
- internal/agents/agents.go:L616-660
- internal/runner/run.go:L788-860
- docs/design/format.md:L140-185
- docs/usage.md:L296-380
Do:
- `lint`: ключи `fallback` и `timeout` в `planOptionalKeys`.
  `fallback: <тройка>; <тройка>…` — каждая тройка по `ParseExecutor`, `;`
  в модели запрещён, только в режиме исполнителей, повтор тройки
  основного `executor:` — ошибка. `timeout:` — целое число секунд от 60 до
  86400, в любом режиме.
- `agents.CheckExecutors` проверяет каждую тройку `fallback:` так же, как
  `replan:`.
- `run --dry-run --all` печатает строку `fallback: <agent model effort>; …`
  после маршрутов юнитов.
- `timeout:` плана перекрывает `timeout` профиля для юнитов и replan.
- Тесты `TestLintFallback`, `TestLintPlanTimeout`, `TestDryRunFallback`,
  `TestRunPlanTimeout` (агент-заглушка спит дольше `timeout: 60` с
  подменой часов или сокращённым порогом в тесте — выбрать по устройству
  `CallOptions.Timeout`).
- Описать ключи в `docs/design/format.md` (рядом с `replan:`, с ссылкой
  на раздел раннера) и `docs/usage.md#автозапуск`; запись в `CHANGELOG.md`
  → `Added`.
Verify: `go test ./internal/lint/ ./internal/agents/ ./internal/runner/ -run 'TestLintFallback|TestLintPlanTimeout|TestDryRunFallback|TestRunPlanTimeout' -count=1`
Commit: P032.3.1: add fallback and timeout plan keys
Handoff:
- `fallback:` is validated and shown after routes by `run --dry-run --all`; runtime switching for units and replan remains for a later P032 unit.
- Unverified: Runtime fallback switching is not implemented or verified in this unit; the runner still invokes the primary route.

### [ ] P032.3.2 Цепочка исполнителей в прогоне юнита
Read:
- internal/runner/run.go:L198-285
- internal/runner/run.go:L410-760
- internal/runner/progress.go:L1-120
- internal/runner/routes_test.go:L120-200
- docs/design/runner.md:L1-34
Do:
- Создать `internal/runner/chain.go`: цепочка маршрутов — тройка юнита,
  затем `fallback:` плана без повторов; недоступность по ключу
  `agent|model` до срока; `pick(now)` — первый доступный, `mark(route,
  refusal, now)`, `earliest()`. Часы — параметр, для тестов.
- В начале каждого юнита маршрут — `pick` с начала цепочки: основной
  исполнитель возвращается, как только его срок прошёл.
- Отказ `limit` или `outage` в `runUnit`: `mark`, затем `pick`. Нашёлся
  другой маршрут → строка `<uid>: <agent model>: <класс> until HH:MM;
  switching to <agent model>`, новая сессия, полный промпт с разделом
  `## Previous attempt`: прошлый исполнитель остановлен отказом
  провайдера, незакоммиченная работа в дереве — продолжать её. Попытку
  не тратит. `throttle` — на том же маршруте, как в P032.2.1.
- Доступных нет → ожидание до `earliest()` (строка
  `all executors unavailable until HH:MM`), затем `pick`; `--no-wait` —
  код 7 с той же строкой, юнит остаётся `[~]`.
- Блок юнита в TTY и текстовом выводе показывает нового исполнителя;
  итог юнита — `(… via <agent model>)`, когда юнит закончил не первый
  маршрут.
- Тесты: `chain_test.go` (порядок, возврат к основному после срока,
  `earliest`); `TestRunFallbackOnLimit` — заглушка pi отдаёт ответ из
  `internal/agentio/testdata/pi-limit-zai.jsonl`, заглушка codex
  заканчивает юнит; `TestRunFallbackOnOutage`;
  `TestRunAllExhaustedNoWait`.
- Обновить схему и «Лимит подписки» в `docs/design/runner.md` (узел
  «Ждать сброса» → «следующий исполнитель, иначе ждать»), `docs/usage.md`;
  запись в `CHANGELOG.md` → `Added`.
Verify: `go test ./internal/runner/ -run 'Chain|TestRunFallbackOnLimit|TestRunFallbackOnOutage|TestRunAllExhaustedNoWait' -count=1`
Traps: после смены исполнителя проверки HEAD и статуса юнита, `DOCFLOW_RUNNER_UNIT` и учёт квоты идут по имени нового агента. План без `fallback:` — цепочка из одного маршрута, поведение P032.2.1.
Commit: P032.3.2: switch to fallback executors on refusal

### [ ] P032.3.3 Квота до юнита и replan по цепочке
Read:
- internal/runner/run.go:L286-354
- internal/runner/replan.go:L130-320
- internal/runner/chain.go
- internal/agentio/quota.go:L1-86
- docs/design/runner.md:L67-130
Do:
- `startQuota` пробует квоту и у агентов `fallback:` (у кого в профиле
  есть `quota`). Окно с использованием ≥ 100% и сроком в будущем →
  исполнитель недоступен до срока (`chain.mark`), строка
  `<agent>: quota exhausted until HH:MM, skipped`.
- Перед каждым юнитом та же проверка по квоте, накопленной из потока
  агентов, без новой пробы.
- `replanUnit`: цепочка — тройка `replan:`, затем `fallback:`; отказы
  обрабатываются тем же кодом, что в `runUnit` (общая функция, если
  циклы сводятся без потери поведения; иначе — handoff с причиной).
- Итог прогона: число смен исполнителя (`N switches`) рядом с числом replan.
- Тесты `TestRunQuotaSkip`, `TestReplanFallback`.
- Обновить «Авто-replan» и «Старт прогона» в `docs/design/runner.md`;
  запись в `CHANGELOG.md` → `Added`.
Verify: `go test ./internal/runner/ -run 'TestRunQuotaSkip|TestReplanFallback' -count=1`
Commit: P032.3.3: quota precheck and fallback for replan

## Phase 4: Приёмка

### [ ] P032.4.1 Приёмка прогона на агентах-заглушках
Read:
- internal/runner/routes_test.go:L120-200
- internal/agentio/testdata/pi-limit-zai.jsonl
- internal/runner/chain.go
- tools/test-install.sh
Do:
- Тест `internal/runner/acceptance_fallback_test.go`: собрать `cmd/docflow`
  во временный каталог и запускать `docflow run` как процесс на временном
  репозитории с планом в режиме исполнителей и заглушками
  `claude`/`codex`/`pi` в `PATH`. Сценарии:
  - лимит z.ai у pi (подлинный образец) → юнит закончил codex из
    `fallback:`, в выводе строка смены, коммит юнита есть;
  - pi отвечает `fetch failed` (локальная модель не запущена) → смена
    исполнителя;
  - отказ у всех исполнителей цепочки, `--no-wait` → код 7, строка
    `all executors unavailable until`, юнит `[~]`;
  - `timeout: 60` и заглушка, запустившая внука в новой сессии с меткой
    в argv, — после прогона процесса с меткой нет; два прогона одного
    юнита оставляют разные логи вызовов;
  - агент прогнал `docflow verify` → gate выполнился один раз, строка
    `verify reused`.
- Каждый пункт `Unverified:` в handoff юнитов P032 закрыть тестом или
  перенести в `Unverified:` этого юнита. Без подписок остаются
  непроверенными: настоящий отказ Claude и Codex по лимиту, возврат к
  основному после сброса на живом прогоне.
Verify: `go test ./... -count=1 && bash tools/test-install.sh`
Traps: провалившийся сценарий с небольшой правкой — чинить в этом юните; большая правка — `blocked` с причиной. Ожидание до сброса в тесте не ждать: срок и часы задаются заглушкой и `--no-wait`.
Depends: P032.1.1, P032.1.2, P032.2.1, P032.3.1, P032.3.2, P032.3.3
Executor: claude | claude-opus-5-5 | high
Commit: P032.4.1: accept fallback run end to end
