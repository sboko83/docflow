# Скиллы и самоприменение

## Граница с bsv-docs

- `bsv-docs` — общая документация проекта: жанры README, ARCHITECTURE, API,
  AGENTS и других документов; размещение, язык, актуальность и аудит.
  CHANGELOG ведёт отдельный `bsv-changelog`.
- `bsv-changelog` задаёт формат заметок и ручного релиза по Keep a Changelog и
  SemVer. Он применим отдельно и в проектах без docflow. Язык по умолчанию —
  английский; явные правила проекта и поручение имеют приоритет.
- `docflow` — движущиеся части: очередь, планы, статусы, handoff, архив.
- `bsv-docs` действует по маркеру: есть
  `docs/docflow.md` → планы, статусы, срок жизни задач и «почему» ведёт docflow;
  нет → действует прежний жанр `docs/PLAN.md`.
  Почему — [P010](../archive/P010-design-rationale.md).
- Подстраховка маркера: `docflow init` дописывает в AGENTS.md проекта строку
  `Plans: docflow (run docflow status), only on an explicit request; not the bsv-docs PLAN genre`.
  Старую строку без требования явного запроса заменяет, не создавая второй
  маркер ([P044](../archive/P044-unverified-discipline.md)).
- Перевод существующего проекта на docflow — только явно, не по факту
  появления скрипта.
- В docflow-проекте ARCHITECTURE — текущее устройство системы + ссылки на
  `archive/` за обоснованием. Правило «отменённое решение правится» в
  ARCHITECTURE остаётся в обоих режимах: `Superseded` касается только архива.

## Скиллы

Комплект и способ поставки — [P020](../archive/P020-github-skill-bundle.md).
Скиллы и README репозитория написаны на английском (публичный набор),
русская версия README — `README.ru.md`; внутренние доки — на русском.
Почему — [P023](../archive/P023-public-english.md).

Каждый скилл можно использовать отдельно. Общие правила не копируются в
процессные скиллы: они ссылаются на `bsv-docs` и `bsv-changelog`, когда задача
затрагивает соответствующие документы. Процессные скиллы описывают только
docflow-жизненный цикл. Политики коммитов и релизов задают правила проекта и
поручение пользователя.

- `bsv-docs` — общая документация; режим по маркеру (см. «Граница с bsv-docs» выше).
- `bsv-changelog` — записи CHANGELOG и ручной релиз по SemVer; работает
  самостоятельно или вместе с docflow.
- `bsv-docflow-plan` — задача → план: декомпозиция, ловушки, `Verify` и
  `Commit` в каждом юните, исполнители в плане, `lint` и
  `run --dry-run --plan <id> --all` перед сдачей; replan по `[!]`.
  Формат и приоритет — [format](format.md), [runner](runner.md).
- `bsv-docflow-run` — юнит из `prompt`: работа → цикл юнита; исполнителя
  назначает раннер, не агент.
- `bsv-docflow-close` — запускает `close`, заполняет Outcome, Gotchas,
  Follow-ups архивной записи, обновляет architecture, README и CHANGELOG
  (нет `CHANGELOG.md` — шаг пропускается) — [P002](../archive/P002-docflow-skills.md). `lint`
  отказывает архивной записи с пустой секцией.

`bsv-docflow-plan` не активирует план: активация остаётся отдельной командой по
прямой просьбе пользователя. `bsv-docflow-close` закрывает готовый план или
обрабатывает явно порученный отказ от него; он не меняет условия готовности
плана. Описания скиллов не пересекаются по триггерам.

## Установка скиллов

- Исходники — только `skills/` этого репозитория, включая `bsv-docs`.
- Есть Ruleblend — git-импорт из этого репозитория (Ruleblend запрещает
  править установленные им деревья, симлинки поверх них нельзя). Локальный
  репозиторий без remote импортируется; Ruleblend берёт закоммиченный HEAD,
  поэтому правка скилла видна ему только после коммита, обновление —
  `update_from_sources` + `install`.
- Ruleblend и симлинки сочетаются по агентам: агент, чьи скиллы снял
  Ruleblend, получает симлинки `install-skills` (Pi видит их в
  `~/.pi/agent/skills/`) ([P027](../archive/P027-skills-check-symlinks.md)).
- Нет Ruleblend — `docflow install-skills` ставит личные симлинки; симлинк,
  ведущий внутрь `~/.docflow/`, при установке из другого источника
  перенаправляется на новый источник, а чужой существующий путь вызывает отказ
  ([P038](../archive/P038-runner-backlog-batch.md)).
  `--agent claude|codex|pi|opencode` выбирает каталоги
  `~/.claude/skills/`, `~/.agents/skills/`, `~/.pi/agent/skills/` и
  `~/.config/opencode/skills/`
  соответственно; default — Claude.
  Явный `--target DIR` заменяет личный каталог.
- Standalone-установка (`install.sh` → `docflow install`, `docflow update`)
  ставит те же личные симлинки, но они ведут в
  `~/.docflow/current/skills/<имя>`, а не в checkout: `docflow update`
  переключает симлинк `current` на новую версию, не переписывая симлинки
  скиллов; `docflow uninstall` убирает их вместе с `~/.docflow`.
- Режим скиллов standalone-установки: `docflow install` и `docflow update`
  принимают `--skills global|project`; режим хранится одним словом в
  `~/.docflow/skills-mode` и удаляется вместе с `~/.docflow`. Без флага
  берётся сохранённый режим; при первом запуске, когда stderr — терминал
  (вопрос пишется туда; иначе он невидим, а на Windows-раннере `CONIN$`
  скрытой консоли открывается и чтение висит), docflow один раз
  спрашивает `skills: link into agent directories (global) or only per
  project (project)? [G/p]` и запоминает ответ; без терминала действует
  `global`, файл не пишется и печатается одна подсказка про
  `docflow update --skills project`. Режим `project` убирает только личные
  симлинки docflow (ведущие в `~/.docflow`) в каталогах, которые глобальный
  режим использовал бы; проектные копии и чужие скиллы не трогаются.
  Режим, выбранный флагом или ответом, применяется к ссылкам и в `docflow
  update` без нового релиза и записывается только после применения:
  упавшая команда не оставляет режима, которому ссылки не следуют.
  Почему — [P046](../archive/P046-skills-install-mode.md).
  В проект скиллы ставит `docflow init --skills` или
  `docflow install-skills --project` — сам project-режим ничего не копирует.
- `install-skills --project` копирует скиллы (не симлинки) в
  `<project>/.claude/skills/` для Claude или `<project>/.agents/skills/` для
  Codex или `<project>/.pi/skills/` для Pi или `<project>/.opencode/skills/` для OpenCode. `--project` и `--target` несовместимы. `init --skills` использует тот
  же проектный каталог; `init --agent codex|pi` без `--skills` скиллы не ставит.
  Копии коммитят. Каталог с тем же `name:` в `SKILL.md` считается копией
  docflow и обновляется целиком; чужой каталог или симлинк — конфликт, весь
  набор проверяется до записи. Повторная установка новых скиллов не требует
  отдельного списка имён.
- Приоритет личных и проектных каталогов не считается одинаковым у обоих
  агентов. Для Claude сохраняется правило личного скилла поверх проектного
  [P008](../archive/P008-project-skills.md). Codex сканирует `.agents/skills`
  в каталогах от cwd до корня репозитория, поддерживает симлинки; скиллы с
  одинаковым `name` не объединяются и могут отображаться как дубли — [Codex
  local skill locations](https://learn.chatgpt.com/docs/build-skills#where-codex-loads-local-skills).
- `tools/check-plan-skill.sh` — живая проверка скилла планирования
  `bsv-docflow-plan`: холодная сессия агента планирует строку Backlog во
  временном репозитории, скрипт проверяет план по правилам ростера; вне CI
  и гейта — тратит сессию подписки.
  В этом сценарии запрошен default-профиль: маршруты сверяются со снимком
  ростера на момент вызова; `--check-only` требует сохранённый успешный
  код агента. Раннер читает назначения из согласованного плана.
  Проверка жизненного цикла без модели: `python3 tools/test-check-plan-skill.py`.
  Вызывается Pi с `zai/glm-5.3-flash`; `--model` принимает маршруты
  `zai/…` и `strata/…`. Результат относится к выбранной модели в Pi —
  [P039](../archive/P039-e2e-and-live-checks.md).
- Pi читает личные скиллы из `~/.pi/agent/skills/` и проектные из
  `.pi/skills/`; симлинки подхватывает в обоих (проверено на Pi 1.3.0 с
  `PI_CODING_AGENT_DIR` в scratch-каталоге), поэтому личная установка Pi —
  симлинки, как у claude и codex. Проектные скиллы Pi в headless-режиме
  (`-p`, `docflow run`) грузит только при project trust: без записи в
  `~/.pi/agent/trust.json` или флага `--approve` каталог `.pi/skills/`
  молча пропускается; личный каталог trust не требует
  ([P026](../archive/P026-pi-agent.md)).
- OpenCode читает ещё `.claude/skills` и `.agents/skills`: установка для
  Codex делает скиллы видимыми и OpenCode. Собственные каталоги OpenCode
  ставит только `--agent opencode`
  ([P053](../archive/P053-opencode-adapter.md)).
- Один установщик на проект: куда ставит Ruleblend, `--project` не применяют —
  [P008](../archive/P008-project-skills.md).

## Самоприменение

- Этот репозиторий ведётся по docflow: `docs/docflow.md`, планы, архив.
  `docs/design/` играет роль architecture: текущие решения и формат;
  новое «почему» — в архивной записи, тема design ссылается на неё.
- Bootstrap: первым пишется `bsv-docflow-plan` — им составлен план
  реализации. Юниты до готового цикла (`verify`/`done`/`commit`)
  выполняются по циклу вручную; дальше — через `docflow prompt`.
  Готовый `lint` проверяет уже написанные планы.
- Скилл до установки опробуется чтением `skills/<name>/SKILL.md` из
  репозитория; установка — по явному запросу.
