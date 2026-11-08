---
id: P060
title: dsh agent in the runner
type: feature
status: draft
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
- Среда dsh: Node; авторизация — логин DeepSeek-подписки в dsh (каналы
  по умолчанию: аккаунт-платформа, документ кредов `~/.dsh/.credentials.yaml`,
  env `DEEPSEEK_API_KEY`); Windows проверяется отдельно в фазе 3.

## Prerequisites

- Node и npm на машине прогона: `node --version`
- dsh установлен (`npm i -g @deepseek-ai/dsh`): `dsh --version`
- dsh авторизован в DeepSeek-подписке: логин аккаунт-платформы выполнен
  человеком; запасной канал — ключ из opencode-go в
  `~/.dsh/.credentials.yaml`; значение в план не пишется:
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

## Phase 1: Эмпирика

### [ ] P060.1.1 Спайк dsh headless
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
  opencode-go/deepseek-v4.1-flash; какой канал авторизации используется
  реально (аккаунт-платформа dsh, `~/.dsh/.credentials.yaml`, env).
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

### [ ] P060.1.2 Review phase 1
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

## Phase 2: Парсер и конфиг

### [ ] P060.2.1 Парсер stream dsh
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

### [ ] P060.2.2 Блок agents.dsh в DefaultAgents
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

### [ ] P060.2.3 Review phase 2
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

## Phase 3: Приёмка и доки

### [ ] P060.3.1 Приёмка на реальном dsh
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
