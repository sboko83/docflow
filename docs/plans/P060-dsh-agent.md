---
id: P060
title: dsh agent in the runner
type: feature
status: active
created: 2026-11-08
permissions: auto
gate: go test -short -timeout 20m ./...
executor: opencode | opencode-go/deepseek-v4.1-flash | high
fallback: pi | strata/swift-1.5-iq3_s | medium; pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | high
cheap: opencode | opencode-go/deepseek-v4.1-flash | low; pi | strata/swift-1.5-iq3_s | low; pi | zai/glm-5.3-flash | low; codex | gpt-6-luna | low
heavy: opencode | opencode-go/deepseek-v4.1-flash | max; pi | zai/glm-5.3-flash | max; codex | gpt-6.1-sol | high
review: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | medium
acceptance: pi | zai/glm-5.3 | max; codex | gpt-6.1-sol | high
replan: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | medium
---
## Goal

`docflow run` ведёт юниты через пятого агента — dsh (DeepSeek Harness,
headless-профиль): блок `[agents.dsh]` в `DefaultAgents`, парсер
`stream = "dsh"` в agentio, приёмка на реальном dsh. Формат не растёт:
блок — данные существующей схемы, `dsh` — значение существующего ключа
`stream`.

## Context

- internal/agents/agents.go:L36-170
- internal/agentio/output.go:L85-250

## Constraints

- Роста формата нет: новый ключ `AgentKeys`, новая секция или версия
  формата не вводятся; freeze соблюдается без сносок о generalization.
- dsh — developer preview: парсер живёт на зафиксированных в
  `internal/agentio/testdata` транскриптах; проверенная версия dsh
  фиксируется в `## Decisions`.
- Контракт dsh headless (из документации на 2026-11-08, живьём не
  проверен): one-shot процесс, NDJSON-события `session`, `status`,
  `text`, `thinking`, `tool_call`, `tool_result`, `final`, `error`;
  успех = exit 0 **и** `final.reason.kind === 'completed'`; строки/объекты
  капятся 8 KiB, строка события 32 KiB, флаг `truncated`; schema-флага нет;
  задача принимается через stdin (`-`).
- Среда dsh: Node; Windows проверяется отдельно в фазе 3.
- Авторизация (проверено на dsh 0.2.2-rc.1): бандл `dsh-headless`
  выключает `authorization`, `deepseek-account` и `credentials`, а
  модель по умолчанию идёт маршрутом `deepseek-official`, которому нужен
  только env `DEEPSEEK_API_KEY`. Подписка работает через патч профиля
  `$DSH_HOME/profiles/headless/cordis.patch.yml`: эти три entry с
  `disabled: false` и `agent-default-model` с `provider: deepseek-account`.
  Ключ opencode-go этому маршруту не подходит.

## Prerequisites

- Node и npm на машине прогона: `node --version`
- dsh установлен (`npm i -g @deepseek-ai/dsh`): `dsh --version`
- dsh авторизован в DeepSeek-подписке: логин аккаунт-платформы выполнен
  человеком (в web-профиле), профиль headless пропатчен на
  `deepseek-account` (см. Constraints):
  `dsh headless "Ответь одним словом: ок"`

## Traps

- `--approval-policy ask` в non-interactive зависает до idle_timeout:
  в `cmd` всегда `never` (или совместимый preset по итогам спайка).
- Поток молчит при долгих командах (text/thinking приходят при коммите
  шага): замер спайка решает, нужен ли dsh idle_timeout больше
  стандартных 1200.
- `error`-событие приходит без `final` (валидный поток при невалидном
  запуске); `final.reason` может быть `null`.
- Капы 8 KiB/32 KiB и `truncated`: парсер не падает на обрезанных
  событиях.
- Коммит юнита делает сам агент — нужна запись в `.git`. Если
  workspace-write её не покрывает, грант решается preset'ом в dsh-профиле
  (`$DSH_HOME`), а не плейсхолдером пути: рост конфиг-поверхности
  недопустим.
- Комментарий «Keep its bytes aligned with the Python CLI» над
  `DefaultAgents`: Python CLI в репо нет — уточнить у владельца, жив ли
  он, прежде чем менять байты константы.
- Убийство агента по таймауту: Node-потомки dsh не должны оставаться.

## Decisions

- Промпт — через stdin (`-`), без `{prompt}`: у claude/codex/opencode
  промпт тоже идёт через stdin, `CallAgent` уже принимает stdin; отвергнут
  плейсхолдер в argv — экранирование и нестандарт для реестра.
- `result = "json:text"` + fenced JSON-блок: schema-флага у headless dsh
  нет — та же схема, что у codex.
- Квота и токены вне первой версии: квота-команды в CLI-доке dsh не видно,
  ключи опциональны (pi живёт без quota); вернётся отдельным юнитом, если
  спайк найдёт usage в событиях.
- Спайк: версия — dsh 0.2.2-rc.1 (npm @deepseek-ai/dsh), Node 24, Windows 11; фикстуры internal/agentio/testdata/dsh*.jsonl сняты на ней.
- Спайк: авторизация — happy path без DEEPSEEK_API_KEY в env проходит через маршрут deepseek-account (stderr: [llm-deepseek-account]); патч профиля headless применяется сам, cmd агента без --patch. Пустой DSH_HOME даёт final.reason.kind=error, code MISSING_CREDENTIAL; неверный ключ — code AUTH, status 401, текст 'Authentication Fails'. Оба — exit 1 и строка 'dsh: <CODE>: …' в stderr: regex auth профиля — 'MISSING_CREDENTIAL|Authentication Fails|AUTH:'.
- Спайк: поток — session(sessionId,cwd), status(phase turn_start|step_start|step_end|turn_end; step_end несёт usage, turn_end — reason), thinking(text, бывает пустым), text, tool_call(callId,tool,input), tool_result(callId,status completed|error,result,truncated?), final(sessionId,reason,text). Успех — exit 0 и final.reason.kind=completed; ошибка запуска (неизвестный флаг, чужой --session-id, неизвестный preset) — одно событие error{message} без session и final, exit 1. final.reason=completed бывает и при невыполненной задаче — решает result JSON в final.text.
- Спайк: усечение — tool_result с выводом >8 KiB приходит строкой 8656 байт с truncated:true; JSON валиден, парсеру достаточно не требовать полноты result.
- Спайк: resume — --session-id <sessionId из события session> продолжает ту же сессию (turn 2, тот же sessionId); чужой id — событие error, exit 1. resume_args = ['--session-id', '{session}'].
- Спайк: живость — во время команды поток молчит полностью (sleep 90: 91 с между tool_call и tool_result). Таймаут команды pwsh по умолчанию 300 с, агент сам ставит timeoutMs (выбирал 180000/300000); тишина ограничена длиной одной команды, idle_timeout = стандартные 1200, instructions просят timeoutMs выше ожидаемой длительности долгих проверок.
- Спайк: usage — у status step_end есть usage{inputTokens,outputTokens,cacheReadTokens,cacheWriteTokens,totalTokens} на шаг; cost и квоты нет. Парсер суммирует шаги: tokens = input+cacheRead+cacheWrite, output_tokens = output; cost/quota в профиль не идут.
- Спайк: модель — флага модели у headless нет, модель задаёт профиль (agent-default-model: provider deepseek-account, model deepseek-flash); в событиях и сессии имя модели не пишется. Профиль без model_args/effort_args; модель в маршруте плана — пометка, не аргумент. Совпадение deepseek-flash подписки с opencode-go/deepseek-v4.1-flash проверить нельзя.
- Спайк: права — workspace-write на Windows: запись в .git обычного клона проходит, но запись вне cwd запрещена (кэш go-build → go test падает 'Access is denied'), HTTPS из песочницы падает (schannel SEC_E_NO_CREDENTIALS), коммит из linked worktree падает на .git/worktrees/*/index.lock; --add-dir на Windows ломает каждый shell-вызов ('Windows ACL confinement does not support additional writable directories'). Эскалация в headless невозможна: ask не зависает, а отказывает 'no approval channel is available' — ask и never равны. danger-full-access коммитит из worktree. Решение для P060.2.2: auto и full → --permission-mode danger-full-access --approval-policy never (как opencode, где auto=full); отвергнут workspace-write — без эскалации агент не может выполнить docflow verify в Go-репо и не коммитит в worktree, а перенос GOCACHE в дерево пачкает рабочее дерево.
- Неизвестный тип события dsh в живой строке показывается своим именем (как у codex), в истории активности — unknown с сырой строкой; DshSilent — session, status, thinking, tool_result, final, error — по замеру спайка: это не действия агента.
- auto и full у dsh — оба danger-full-access + never, как auto=full у opencode — спайк: workspace-write на Windows ломает go test (кэш go-build), HTTPS и коммит из worktree, эскалация в headless невозможна — отвергнут auto=workspace-write с instructions про обход: юниты падали бы на собственном docflow verify.
- Комментарий 'Keep its bytes aligned with the Python CLI' заменён причиной, которая жива (--print-config печатает текст для копирования) — Python CLI в репо нет — отвергнуто держать мёртвую ссылку до ответа владельца.

## Phase 1: Эмпирика

### [x] P060.1.1 Спайк dsh headless
Read:
- internal/agents/agents.go:L40-170
- internal/agentio/output.go:L85-250
- internal/agentio/process.go:L200-260
Do:
- Установить dsh (`npm i -g @deepseek-ai/dsh`); записать версию в
  `## Decisions`.
- Снять транскрипты `dsh headless --json` с задачей через stdin (`-`):
  happy path (задача с одной командой) **без** `DEEPSEEK_API_KEY` —
  подтверждает, что headless берёт авторизацию из хранилища логина
  подписки, а не из env; транскрипт отказа (usage-ошибка неизвестным
  флагом или auth-отказ); resume через `--session-id`.
- Замерить живость: задача с `sleep 90` внутри — какие события приходят
  в окно сна.
- Проверить права: покрывает ли `workspace-write` запись в `.git`
  (`git commit` внутри задачи); поведение `--approval-policy ask|never`;
  нужен ли `--add-dir` и в каком виде.
- Выяснить: есть ли usage/tokens в событиях; как выбирается модель
  (флаг, preset, adapter) и какая модель подписки отвечает — сравнить с
  opencode-go/deepseek-v4.1-flash; подтвердить канал авторизации
  `deepseek-account` из патча профиля (см. Constraints) и что `cmd`
  агента не требует `--patch`.
- Обезличить транскрипты и положить в
  `internal/agentio/testdata/dsh.jsonl` и
  `internal/agentio/testdata/dsh-error.jsonl`.
- Записать ответы в `## Decisions` строками `Спайк: <вопрос> — <ответ и
  решение, которое он фиксирует>`.
Verify: `test -f internal/agentio/testdata/dsh.jsonl && test -f internal/agentio/testdata/dsh-error.jsonl && grep -q 'Спайк:' docs/plans/P060-dsh-agent.md`
Traps: в транскриптах не должно остаться значений ключей и абсолютных
путей пользователя; `--approval-policy ask` зависает — снимать только
с `never`.
Depends: -
Role: heavy
Commit: P060.1.1: capture dsh headless fixtures
Handoff:
- Фикстуры: dsh.jsonl (happy, final.text с fenced result JSON), dsh-error.jsonl (только error, exit 1), dsh-auth.jsonl (final.reason.kind=error, code AUTH, exit 1), dsh-truncated.jsonl (tool_result truncated:true, строка 8656 байт). Пути обезличены в C:\work\<dir>.
- У dsh нет флагов модели и усилия: блок без model_args/effort_args; instructions нужны только если auto остаётся песочницей — по решению спайка auto=full=danger-full-access, instructions пустые.
- Limit-regex по живому отказу не снят (лимит подписки не воспроизвести); брать 'rate limit|429|RATE_LIMIT|quota' как предположение.

### [x] P060.1.2 Review phase 1
Read:
- internal/agentio/testdata/dsh.jsonl
- internal/agents/agents.go:L40-170
Do:
- Сверить `## Phase diff` фазы 1 с Goal, Constraints и `Do:` P060.1.1;
  правок не вносить; блокировать только нарушение Constraints, Traps
  или регресс; остальное — в Handoff.
Verify: `go test -short -timeout 20m ./...`
Depends: P060.1.1
Role: review
Commit: P060.1.2: review dsh spike phase
Handoff:
- Review: нарушений Constraints/Traps и регресса нет; фикстуры без личных путей и значений ключей (в dsh-auth.jsonl только маска '****alue' фиктивного ключа).
- Trap плана про зависание '--approval-policy ask' спайк опроверг для headless (отказ 'no approval channel'); в cmd всё равно 'never' — поведение одинаковое, never явнее.
- Транскрипт resume в testdata не сохранён — контракт записан в Decisions; P060.2.1 при нужде тестирует resume через fake_agent.sh.
- Unverified: сеть (HTTPS) и запись в кэш go-build под danger-full-access не проверены живьём (прогон с сетью остановил классификатор сессии); закрывает приёмка P060.3.1 на реальном dsh.

## Phase 2: Парсер и конфиг

### [x] P060.2.1 Парсер stream dsh
Read:
- internal/agentio/output.go:L530-575
- internal/agentio/activity.go:L130-200
- internal/agents/agents.go:L160-200
- internal/agentio/testdata/dsh.jsonl
- internal/agentio/testdata/dsh-error.jsonl
Do:
- Во всех switch-точках по `stream` (основной разбор `output.go`,
  `reasoningBlock`, извлечение usage) добавить ветку `"dsh"`:
  `session`→session_id, `text`→text, `thinking`→reasoning-блок,
  `tool_call`/`tool_result`→активность, `final`→result+text+session и
  `is_error = (exit≠0 || reason≠completed)`, `error`→is_error.
- Добавить `"dsh"` в `Streams` (agents.go).
- Задать silent-список активности (аналог `CodexSilent`) по замеру
  спайка: какие события dsh не считаются активностью.
- Тесты на фикстурах по образцу `output_test.go`: happy path,
  error-без-final, обрезанная строка (truncated); process-тест через
  `fake_agent.sh`.
Verify: `go test ./internal/agentio ./internal/agents`
Traps: парсер не падает на error-без-final, null-`reason` и truncated;
успех только при exit 0 и `reason.kind = completed` одновременно.
Depends: P060.1.1
Role: heavy
Commit: P060.2.1: parse dsh event stream
Handoff:
- Декодер DshOutput (output.go) отдаёт session, text, usage{input,output,cache_read,cache_write,total} (сумма step_end), is_error, error и error_code (код из final.reason.error, например AUTH, MISSING_CREDENTIAL). Пути профиля: result/text json:text, session json:session, error json:is_error, tokens json:usage.input/cache_read/cache_write, output_tokens json:usage.output.
- Тесты dsh в internal/agentio/dsh_test.go идут через локальный dshAgent() — когда P060.2.2 добавит профиль, заменить его на profile(t, "dsh"), чтобы тесты держали json-пути профиля.
- Отказы dsh идут общим путём refusal.go (regex limit/auth профиля по FailureText: final.text + error + stderr при exit≠0); отдельной ветки классификатора нет — лимитный ответ dsh не снят.
- Liveness: tool_call держит вызов живым, пока нет tool_result; поэтому молчание во время долгой команды не будит idle-watchdog.

### [x] P060.2.2 Блок agents.dsh в DefaultAgents
Read:
- internal/agents/agents.go:L40-170
- docs/design/runner.md:L738-829
- docs/usage.md:L482-586
Do:
- Добавить `[agents.dsh]` в `DefaultAgents`: `cmd = ["dsh", "headless",
  "--json", "--permission-mode", <preset>, "--approval-policy", "never",
  "-"]`, `resume_args = ["--session-id", "{session}"]`, экстракторы
  `json:` по внутренним полям парсера, `limit`/`auth` regex по спайку,
  `timeout`/`idle_timeout` по замеру живости, `instructions` про sandbox
  по образцу codex, `permissions.auto|full`; `quota`/`tokens`/`cost` —
  только если спайк показал их в событиях.
- Если workspace-write не покрывает `.git` — описать в `instructions`
  настройку preset'а в dsh-профиле и сослаться на него из `cmd`.
- Словарь потока dsh и блок агента — в design-топик раннера
  (docs/design/runner.md или topic, где описаны агенты).
- Уточнить у владельца судьбу Python CLI (см. Traps плана) и либо
  зеркалировать байты константы, либо обновить комментарий.
Verify: `go test ./internal/agents && go run ./cmd/docflow run --print-config | grep -q 'agents.dsh'`
Traps: freeze — только существующие ключи `AgentKeys`; константа
`DefaultAgents` держится синхронной с Python CLI, если он жив.
Depends: P060.2.1
Role: heavy
Commit: P060.2.2: add dsh agent config block
Handoff:
- Блок [agents.dsh] в DefaultAgents: cmd dsh headless --json -, resume --session-id {session} (dsh разбирает флаги после '-'), без model_args/effort_args, auto=full=--permission-mode danger-full-access --approval-policy never, instructions про timeoutMs, limit/auth regex, timeout 7200, idle_timeout 1200, без quota/cost.
- dsh добавлен в lint executorAgents и в format.md (значение agent тройки) — иначе executor: dsh | … не проходил lint. Тройка: dsh | deepseek-flash | - (модель — пометка).
- Вне плана остались: roster.PolicyAgents (roster apply не ставит dsh в ячейки), agents.InteractiveArgv (TUI-планирование через dsh — отказ 'unknown agent'), install-skills/doctor --agent (каталог скиллов dsh неизвестен). Это отдельные решения, не дефекты P060.
- Комментарий над DefaultAgents больше не упоминает Python CLI: в репо и его истории Python-реализации CLI нет, только скрипты tools/*.py; владельцу — подтвердить.
- Ссылку design-топика runner.md на архив P060 добавить при закрытии плана (архивного файла ещё нет).

### [x] P060.2.3 Review phase 2
Read:
- internal/agentio/output.go:L85-250
- internal/agents/agents.go:L40-170
Do:
- Сверить `## Phase diff` фазы 2 с Goal, Constraints, Decisions и `Do:`
  юнитов фазы; правок не вносить; блокировать только нарушение
  Constraints/Decisions или регресс; ослабленный ради `Verify:` тест —
  дефект.
Verify: `go test -short -timeout 20m ./...`
Depends: P060.2.1,P060.2.2
Role: review
Commit: P060.2.3: review dsh parser phase
Handoff:
- Review: нарушений Constraints/Decisions и регресса нет; формат не вырос (dsh — значение stream и agent, AgentKeys не тронут). Test changes не ослабляют проверки: заглушка dshAgent заменена встроенным профилем, хэш/число профилей и текст lint следуют новому блоку.
- Дефект для P060.3.1: limit-regex dsh содержит голое '429'; refusal (лимит) классифицируется раньше auth, а текст auth-отказа dsh несёт hex request_id — id с подстрокой 429 превратит отказ входа в 30-минутное ожидание лимита. Заменить на 'usage limit|rate limit|rate_limit|too many requests|hit your limit|quota' и добавить тест: auth-фикстура с request_id ...429... даёт AuthReason, а не Refusal.

## Phase 3: Приёмка и доки

### [x] P060.3.1 Приёмка на реальном dsh
Read:
- internal/runner/run.go:L860-910
- internal/runner/liveness_acceptance_test.go:L1-60
Do:
- Интеграционный тест в internal/runner с пропуск-защитой (нет `dsh` на
  PATH или не проходит проверка авторизации — skip): прогон одного юнита
  тестового плана через реальный dsh до коммита юнита.
- Проверить вручную на Windows (Node + dsh): запуск, коммит юнита,
  остановка по таймауту без оставшихся Node-потомков; результат — в
  Handoff.
Verify: `go test -timeout 20m ./internal/runner -run 'Dsh'`
Traps: тест не требует сеть и ключ на CI — skip без dsh; таймаут-тест
не должен убивать процессы мимо дерева dsh.
Depends: P060.2.2
Role: heavy
Commit: P060.3.1: accept dsh end to end
Handoff:
- Limit-regex dsh без голого 429 (тест: request_id с 429 → AuthReason). Ручная проверка Windows: timeout-kill dsh — 3 node.exe + pwsh.exe порождены, после kill не осталось ни одного. TestDshRealUnitCommits запущен владельцем на реальном dsh.
- Unverified закрыт: HTTPS и кэш go-build под danger-full-access проверены реальным прогоном юнита (verify с gate).

### [ ] P060.3.2 README и CHANGELOG
Read:
- README.md:L25-45
- README.ru.md:L25-45
- CHANGELOG.md:L1-20
Do:
- `README.md` и `README.ru.md` вместе: добавить dsh в список
  поддерживаемых run агентов (рядом с Claude Code, Codex CLI, Pi,
  OpenCode).
- `CHANGELOG.md`: `## [Unreleased]` → Added — dsh (DeepSeek Harness)
  agent support.
Verify: `grep -q 'dsh' README.md && grep -q 'dsh' README.ru.md && grep -q 'dsh' CHANGELOG.md`
Depends: P060.3.1
Role: cheap
Commit: P060.3.2: document dsh agent support

### [ ] P060.3.3 Acceptance
Read:
- internal/agents/agents.go:L40-170
Do:
- Один сценарий на каждое утверждение Goal: `go run ./cmd/docflow run
  --print-config` содержит `[agents.dsh]`; интеграционный тест
  P060.3.1 зелёный на реальном dsh; README обоих языков упоминают dsh.
- Каждый `Unverified:` из Handoff юнитов закрыть тестом, записать
  `Accepted: <пункт> — <почему это не риск>` или оставить `Unverified:`
  как осознанный риск.
Verify: `go test -timeout 20m ./...`
Depends: P060.1.1,P060.1.2,P060.2.1,P060.2.2,P060.2.3,P060.3.1,P060.3.2
Role: acceptance
Commit: P060.3.3: accept dsh agent plan
