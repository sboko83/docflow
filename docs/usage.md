# Как пользоваться docflow

Процесс от идеи до архива: кто что делает, какие команды, что куда пишется.
Формат файлов, команды и коды выхода — [docs/design/](design/README.md); здесь —
порядок работы и справочник команд.

## Установка

### Установить CLI

Требования: macOS, Linux или Windows 10/11 (amd64 или arm64). Go и sudo не
нужны; install.sh требует curl или wget, install.ps1 — только PowerShell:

~~~bash
curl -fsSL https://raw.githubusercontent.com/sboko83/docflow/main/install.sh | sh
~~~

Windows, PowerShell (install.sh работает и в Git Bash):

~~~powershell
irm https://raw.githubusercontent.com/sboko83/docflow/main/install.ps1 | iex
~~~

Установщик определяет OS и архитектуру, скачивает последний релиз, сверяет
sha256 по checksums.txt и запускает из архива `docflow install`. Тот
укладывает бинарник, скиллы и LICENSE в версию и переключает `current`:

~~~
~/.docflow/
  bin/docflow     -> ../current/docflow
  current         -> versions/<version>
  versions/<version>/docflow
  versions/<version>/skills/<имя>/
  versions/<version>/LICENSE
  last-update-check
~~~

Затем `docflow install` добавляет `~/.docflow/bin` в PATH (блок в
`.zshrc`, `.bashrc` или `.bash_profile`; на Windows — каталог `current` в
пользовательский PATH, симлинки заменяют directory junction без Developer
Mode и прав администратора) и ставит личные симлинки скиллов агентам, чьи
CLI есть в PATH (`claude`, `codex`, `pi`, `opencode`), и в `~/.zcode/skills`,
если есть `~/.zcode`. Симлинки ведут в `~/.docflow/current/skills/<имя>` и
при обновлении не переписываются.

Режим скиллов спрашивается один раз: `global` — личные симлинки, как выше;
`project` — каталоги агентов чистые, скиллы несут только проектные копии
(их ставят `docflow init --skills` и `docflow install-skills --project`).
Ответ — одно слово в `~/.docflow/skills-mode`, `docflow update` следует ему
без вопроса. Вопрос идёт в stderr, ответ — с управляющего терминала, так что
`curl … | sh` его не гасит; без терминала (CI) действует `global`, файл не
пишется, печатается подсказка.

Переменные install.sh и install.ps1: `DOCFLOW_VERSION=X.Y.Z` — конкретный
релиз; `DOCFLOW_RELEASE_URL` — базовый URL релизов (по умолчанию
`https://github.com/sboko83/docflow/releases`); `DOCFLOW_SKILLS=global|project`
— ответ на вопрос о режиме заранее (`docflow install --skills`). В PowerShell:
`$env:DOCFLOW_VERSION = 'X.Y.Z'` перед командой.

После установки перезапустите оболочку или выполните
`export PATH="$HOME/.docflow/bin:$PATH"` и проверьте `docflow --version`.

### Проверить комплект и подключить проект

~~~bash
docflow doctor --agent codex
cd <project>
docflow init --agent codex
~~~

Для Claude Code `--agent` опускается (цель по умолчанию); для Pi — `--agent
pi`, личные симлинки лягут в `~/.pi/agent/skills/`, а doctor добавит строку
версии Pi. `install-skills` без `--project` ставит личные симлинки, `--target
DIR` задаёт другой личный каталог. `docflow init --skills` сразу копирует
скиллы в проект; в подключённом проекте — `docflow install-skills --project
[--agent codex]`. Проектные копии лежат в `.agents/skills/` (Codex),
`.pi/skills/` (Pi) или `.claude/skills/` (Claude) и коммитятся с проектом.

Личная установка — для своих проектов, проектные копии — для команды. При
обеих сразу Codex может показать имена дважды; приоритеты каталогов Claude и
Codex различаются — [каталоги и приоритеты](design/skills.md#установка-скиллов).

`doctor` сравнивает файлы, но не подтверждает, что агент видит скиллы. Для
полного комплекта проверьте пять строк `<имя скилла>: ok`;
[коды выхода](design/cli.md#cli-docflow) не заменяют чтение вывода.

### Обновить CLI и проектные копии

~~~bash
docflow update --check            # только сообщить о новом релизе
docflow update                    # скачать и активировать последний релиз
docflow update --skills project   # переключить режим скиллов
~~~

`update` скачивает релиз как install.sh, сверяет sha256 и переключает
`current`; в `~/.docflow/versions` остаются текущая и предыдущая версии. Нет
нового релиза — `up to date`. `DOCFLOW_RELEASE_URL` действует и здесь. После
`updated:` — строка на каждый каталог личных скиллов с числом ссылок, `(N
new)` — скиллы, появившиеся в релизе; сами ссылки смотрят в `current` и не
переписываются. Проектные копии update только проверяет — в проекте, из
которого запущен, — и печатает команду обновления; другие проекты проверяет
`docflow doctor` в их каталоге.

Режим скиллов update берёт из `~/.docflow/skills-mode`. `--skills
global|project` переключает его и применяет даже без нового релиза: `global`
создаёт личные симлинки, `project` убирает только свои (ведущие в
`~/.docflow`), не трогая чужие ссылки и проектные копии. `--check` режим не
трогает.

Откат — переключить `current` вручную (скиллы откатываются вместе с CLI;
следующий `update` снова скачает последний релиз):

~~~bash
ls ~/.docflow/versions                      # текущая и предыдущая
ln -sfn versions/<предыдущая> ~/.docflow/current
~~~

Раз в сутки терминальный запуск проверяет последний релиз (таймаут 2 с) и
пишет в stderr `docflow 0.4.1 available (current 0.4.0): docflow update`;
`DOCFLOW_NO_UPDATE_CHECK=1` выключает. `docflow uninstall` убирает
`~/.docflow`, свой блок PATH и личные симлинки, ведущие в `~/.docflow`;
проектные копии и чужие файлы остаются.

Проектные копии обновляются в каждом проекте:

~~~bash
cd <project>
docflow install-skills --project --agent codex
docflow doctor --agent codex
~~~

Обновление заменяет содержимое распознанных копий — локальные правки
сохраните до него; чужие каталоги и симлинки остаются, о конфликте
сообщается.

### Режим разработки

Из checkout репозитория (Go 1.26+ и git):

~~~bash
git clone https://github.com/sboko83/docflow bsv-docflow
cd bsv-docflow
bash tools/install-dev.sh --dry-run
bash tools/install-dev.sh
~~~

Скрипт собирает `bin/docflow` и ставит симлинк `docflow-dev` в `~/.local/bin`
(другой — `--bin-dir DIR`); `docflow` на PATH остаётся релизом, PATH скрипт не
меняет, а лишь печатает команду, если каталога там нет. Checkout держите на
месте: симлинк ведёт в него. Скиллы переключает `bash tools/switch-skills.sh`
(`dev` — на checkout, `release` — на релиз, без аргумента — текущий режим).

`docflow-dev update` и `uninstall` отказывают. Обновление — новый тег в
чистом checkout (локальные изменения сохраните отдельно, не сбрасывая) и
повтор установщика:

~~~bash
git status --short
git fetch --tags origin
git checkout vX.Y.Z
bash tools/install-dev.sh
docflow-dev doctor --agent codex
~~~

Личные скиллы, поставленные в произвольный `--target DIR` вне
`~/.zcode/skills`, переустановите вручную: `docflow install-skills --target DIR`.

### Отдельное применение скиллов и Ruleblend

bsv-docs и bsv-changelog работают без docflow и подходят проектам без
docs/docflow.md. Три скилла bsv-docflow-* описывают планирование, исполнение
и закрытие планов. Правила языка, релиза, коммитов и структуры проекта имеют
приоритет.

Ruleblend необязателен: скиллы импортируются git-импортом этого
GitHub-репозитория; Ruleblend читает закоммиченный HEAD — выбирайте тег, без
отдельной копии скиллов.

## Подключение проекта

```bash
cd <project>
docflow init
```

- Создаёт `docs/docflow.md` (очередь, backlog), `docs/plans/`,
  `docs/archive/` и строку в `AGENTS.md`:
  `Plans: docflow (run docflow status), only on an explicit request; not the bsv-docs PLAN genre`.
- Существующий проект переводится только явно. Старый `docs/PLAN.md`
  после перевода не ведётся: открытые задачи — строками в Backlog.
- Architecture проекта (`docs/architecture.md` или что названо в AGENTS.md)
  остаётся: в ней текущее устройство и ссылки на `docs/archive/`.
- Стартовое чтение — необязательные ключи `docs/docflow.md`: `start_read`
  (пути и `path#anchor` через запятую, что читать в начале сессии) и
  `start_budget` (токены, размер / 4). `docflow status` печатает `start:`,
  превышение бюджета — ошибка `lint`.
- Несколько git-репозиториев (корень + вложенные) — ключ `repos:` во
  frontmatter `docs/docflow.md`: пути через запятую, без масок. Проверка
  чистоты, отпечаток `verify` и коммит юнита охватывают все репо; без ключа —
  только корень.
- Закоммитить результат `init` до первого плана: `prompt` требует чистое
  дерево. Проектные копии скиллов — [«Установка»](#установка), коммитятся с
  проектом.

## Жизненный цикл задачи

```
Backlog ──plan──▶ план draft ──docflow activate──▶ active ──run × N──▶ ready to close ──close──▶ archive
                                              │
                                                     └─ [!] блок ──replan──▶ active
```

| Этап | Что сказать агенту | Скилл | Коммит |
|---|---|---|---|
| Записать задачу | «добавь в backlog …» или руками | — | вместе с ближайшим изменением |
| Спланировать | «возьми в работу …», «составь план» | `bsv-docflow-plan` | `Pxxx: add plan` |
| Активировать | `docflow activate Pxxx` сам или «активируй Pxxx» | — | `Pxxx: activate plan` |
| Выполнить юнит | «продолжай», «следующий юнит», «юнит P004.1.2» | `bsv-docflow-run` | `Pxxx.N.M: …` |
| Перепланировать | «docflow status сообщает о блоке», «перепланируй» | `bsv-docflow-plan` | `Pxxx: replan` |
| Закрыть план | «закрой план» (готовый закрывается сразу) | `bsv-docflow-close` | `Pxxx: close plan` |
| Решение без плана | «запиши решение в архив» | `bsv-docflow-close` | `Pxxx: add note` |
| Бросить план | «брось план P00x» | `bsv-docflow-close` | `Pxxx: close plan` |

Где мы сейчас — `docflow status`: планы очереди с прогрессом, текущий юнит
или `blocked:`.

## Планирование

0. Задача помещается в один юнит — агент спрашивает «делаем без плана?»;
   да → работа, коммит, решение с «почему» — `docflow note`.
1. Агент читает `docs/docflow.md`, индекс architecture и нужные темы, затем
   только код, без которого задачу не разрезать.
2. `docflow new <slug>` — план `docs/plans/Pxxx-slug.md` в статусе `draft`,
   id в конец Queue.
3. Шапка: `Goal` (что будет после закрытия), `Context` (файлы для каждого
   юнита, repro бага — прозой), `Constraints`, `Traps`, `Decisions`.
4. Фазы и юниты. Юнит — одна сессия: ~100k контекста, 5–8 изменяемых
   файлов, один `Verify`. План, помещающийся в ~100k, — один юнит.
   Калибровка — `docflow stats`. План меняет то, что видит пользователь, —
   последний юнит «Приёмка»: сценарии по Goal и полный прогон тестов.
   Маршруты ролей подбирает личный ростер (см. [«Ростер»](#ростер)).
5. `docflow lint` без ошибок → план показывается пользователю.
6. Коммит `Pxxx: add plan` (план в `draft`), строка Backlog удаляется.
7. Активация — отдельно и только по решению человека: `docflow activate
   Pxxx` (lint плана, `status: active`) + коммит `Pxxx: activate plan`
   (забытый коммит делают сами `run`/`prompt`, пока дерево грязно только
   следами активации). План без явной просьбы агент не создаёт и после
   активации юнит сам не берёт.

Черновик (`draft`) `prompt` пропускает: план можно писать, не мешая
текущей работе.

## Выполнение юнита

```
docflow prompt → работа → docflow verify <id>* → docflow finish <id>
```

- `prompt` — промпт юнита: Goal, Constraints, Traps, Context, Decisions
  плана, сам юнит, Handoff юнитов из `Depends` (целиком) и их предков по
  `Depends` (ближние первыми, пока влезают в ~2000 токенов, остальные —
  строкой `Omitted for size:`), раздел `## Archive gotchas`
  (Gotchas записей архива, на которые ссылаются Context, Decisions или
  `Read:`; считаются как ловушки). Ставит `[~]`. Требует чистое дерево
  (кроме файла плана).
- Работа — ровно `Do:`. Найденное по пути — пункт Handoff или вопрос
  пользователю, не правка «заодно».
- `verify` — `preflight:` из `docs/docflow.md`, если задан, затем `Verify:`
  и `gate:` плана; при падении — хвост вывода, при успехе — одна строка и
  отпечаток дерева. `--no-gate` — быстрая итерация без gate и отпечатка.
- `finish --handoff "…" [--decision "…"]` — `verify`, `done` и `commit` одним
  вызовом; первый ненулевой код — стоп. Путь агента; отдельные `verify`,
  `done`, `commit` — для промежуточных прогонов и раннера.
- `done --handoff "…" [--decision "…"]` — `[x]`, Handoff и Decisions в
  план. Handoff — то, чего следующий юнит не увидит в коде; decision —
  выбор, которого не было в плане: `решение — почему — альтернатива`.
- `commit` — `git add -A` и коммит с subject из `Commit:`, body из Handoff.
  Новые файлы с секретными именами, > 5 МиБ, транскрипты и экспорты сессий
  агента (файл содержит промпт юнита) и добавленные строки с токенами
  (AWS, GitHub, Slack, Google, `sk-`, приватные ключи) — отказ кодом 5, обход
  `DOCFLOW_ALLOW_STAGED=1`. Отказывает, если дерево изменилось после
  `verify`.

Незавершённый коммит (отказ commit-hook и т. п.) виден как `commit pending`:
следующий `run` или `finish <id>` завершает этот юнит перед следующим,
сохраняя уже созданные коммиты вложенных репо; ошибки подписи и доступа
останавливают запуск с причиной.

Один юнит — одна сессия: следующий лучше начинать в новой (`/clear`), иначе
контекст прошлого оплачивается на каждом запросе. Несколько мелких юнитов
подряд допустимы.

`docflow stats [Pxxx]` — расход на юнит по транскриптам Claude: прокси
размера из `prompt`, запросы, базовый и пиковый контекст, `peak/proxy` и
`grow/proxy` с медианами — по ним калибруется размер юнита. Строки
`review-blocks:` — ревью-юниты, заблокированные три раза и больше: ревью не
сходится, нужно решение человека.

`docflow stats --runs [Pxxx]` — сводка run-событий по юнитам: версия
docflow, попытки, провалы проверки, replan, блоки, правки тестов, свои
`Unverified:`, время, стоимость и `rework` — сколько коммитов вне docflow
(без префикса `Pxxx`) за 7 дней после коммита юнита правят или удаляют его
строки (при `repos:` — и во вложенных репо); `--json` — `{"units": [...]}`.

## Автозапуск

```
docflow run --dry-run    # следующий юнит, агент, модель, команда — без запуска
docflow run --dry-run --all            # исполнители всех оставшихся юнитов
docflow run --dry-run --plan P002 --all  # то же для draft/frozen-плана
docflow run              # юниты текущего плана подряд до ready to close
```

Как устроен цикл, смены исполнителя, replan, поток событий и вывод —
[раннер](design/runner.md); здесь — что нужно знать для запуска.

- Перед запуском: чистое дерево и вход в CLI агента (`claude auth login`
  для профиля по умолчанию).
- Исполнителя задаёт план: `executor: agent | model | effort` в шапке,
  `Executor:` в юните ([формат](design/format.md#план)); `run` берёт
  встроенные адаптеры, а `--config`, `--agent`, `--model`, `--effort` —
  ошибка. Назначения всех оставшихся юнитов проверяются до старта; ошибка —
  код 2 с `путь:строка`.
- `fallback: agent | model | effort; ...` — резервные маршруты по порядку:
  при лимите или недоступности провайдера раннер переходит на первый
  доступный с новой сессией; ждёт сброса, только если недоступна вся
  цепочка. Использованный резервный исполнитель виден в строке юнита.
- `timeout: 60..86400` — стенное время вызова агента в секундах, перекрывает
  профильный `timeout` для юнитов и replan.
- `--dry-run --all` — строка на оставшийся юнит `id [статус] заголовок: agent
  | model | effort (unit|plan|legacy)`, затем цепочки. `--plan <id>` — только
  с `--dry-run`, для draft, active или frozen плана без активации и правки
  Queue; иначе, как и `--all` без `--dry-run`, — код 2. Dry-run не проверяет
  вход и квоту.
- Режим прав и предусловия: `permissions: auto|full` (нет ключа — `auto`) и
  секция `## Prerequisites` — пункты с командой проверки и пункты, которые
  обеспечивает только человек. Команды выполняются на старте (провал — код
  2); пункты с `(once)` проверяются только до первого юнита, на начатом
  плане — строка `prerequisite skipped (once, plan started): <текст>`.
  `full` и пункты без команды подтверждаются вопросом `continue? [y/N]`
  (`--yes` — без вопроса, без терминала обязателен; «n» — код 1). `full`
  снимает песочницу агента (claude `--permission-mode bypassPermissions`,
  codex `--dangerously-bypass-approvals-and-sandbox`, Pi `--approve`).
- Песочница `auto` относится только к процессу агента: `preflight`,
  `Verify:` и `gate:` раннер запускает с правами пользователя, исполняя код,
  только что написанный агентом. Недоверенные репозитории и многочасовой
  `full` — в контейнере или под отдельной учётной записью
  ([граница песочницы](design/runner.md#граница-песочницы)).
- Авто-replan: блок `needs plan` или устаревшая ссылка `Read:`/Context —
  раннер сам вызывает replan-агента, проверяет правку (только план, только
  разрешённые места, `lint`), коммитит `Pxxx: replan Pxxx.N.M` и продолжает;
  нарушение — план восстановлен, код 3. Маршрут — `replan:` в шапке, без
  ключа — `executor:` + `fallback:`. Лимиты — 2 replan на юнит и `--replans
  N` (по умолчанию 3) на фазу; `--replans 0` выключает; `blocker: human`
  replan не вызывает ([правила](design/runner.md#авто-replan)).
- Флаги: `--max-units N`, `--attempts N`, `--replans N`, `--no-wait` (не
  ждать сброса лимита), `--no-notify`, `--yes`, `-v/--verbose` (токены, время
  сброса квоты, оценка остатка, лог каждого вызова; строка `quota:` на старте
  и после юнита печатается и без него). Только для плана без исполнителей:
  `--model M` (ярус или id модели), `--effort E`, `--agent NAME`, `--config`.
- Остановить сразу — Ctrl-C, `kill` или закрыть терминал: агент
  останавливается с раннером, юнит остаётся `[~]`. После текущего юнита —
  `s` + Enter в терминале раннера или `docflow run --stop`; код 0.
- В конце прогона, кроме остановки сигналом, — системное уведомление
  (`ready to close`, блок или причина); `--no-notify` или `DOCFLOW_NOTIFY=0`
  выключают.
- На старте — предупреждение, если у агента плана нет скиллов docflow или
  они устарели; прогон идёт дальше, `DOCFLOW_SKILLS_CHECK=0` выключает.
- После остановки — разобраться и запустить `docflow run` снова: юнит `[~]`
  продолжается с текущего дерева. Лог вызова агента
  `.git/docflow/run/<id>.<n>.out` пишется во время вызова — `tail -f` для
  наблюдения: `$ argv`, `--- stdout`, после выхода `--- stderr` и `--- exit
  N` (с ` (timed out)` или ` (stalled)`). Спиннер TTY показывает
  `<id> <elapsed> · turn <n> · <age> ago` и `· tool <duration>`, пока
  инструмент в работе.

Коды выхода:

- 2 перед юнитом — `prompt` отверг юнит: файла из `Read:` или `## Context`
  нет (прошлый юнит переименовал или удалил). С `replan:` раннер чинит план
  сам; без него — replan руками (`bsv-docflow-plan`), потом `run`.
- 3 — агент заблокировал юнит; класс виден в строке юнита и в Handoff:
  `needs human` — нужен человек (вход, доступ, секрет, устройство, решение);
  `needs plan` — править план (сам при авто-replan, иначе руками). На `needs
  human` строка остановки подсказывает `docflow unblock <id> --note "…", then
  docflow run`. Если `run` стартует на таком юните `[!]`, в терминале без
  `--yes` он печатает причину и спрашивает `done? [y/N] `: `y` и заметка
  снимают блок, и прогон идёт дальше; `n`, `--yes` или без терминала — стоп
  с кодом 3. Блок в середине прогона останавливает его.
- 4 — `Verify` не прошёл за все попытки: работа агента в дереве; доделать
  руками обычным циклом или запустить `run` снова.
- 5 — агент закоммитил файл плана, переписал историю или сменил статус
  юнита: разобрать коммит и план руками. Коммиты инструментов проекта не
  мешают.
- 7 — агент не справился: нет валидного result JSON, таймаут, лимит при
  `--no-wait`, CLI не залогинен; причина — в логе.

Закрытие плана — `docflow run --close` или `bsv-docflow-close` вручную
(см. [«Закрытие»](#закрытие)).

### Конфиг агентов

Конфиг — только для плана без исполнителей. TOML: `--config` >
`$DOCFLOW_AGENTS` > `$XDG_CONFIG_HOME/docflow/agents.toml` (`~/.config/…`);
названный в `--config` или `$DOCFLOW_AGENTS` файл, которого нет, — ошибка.
Встроенные профили `claude`, `codex`, `pi`, `opencode` и `dsh` печатает
`docflow run --print-config`; файл накладывается на них по ключам
(`model_extra` и `permissions` — тоже по ключам), добавляет профили и
накладывается на `[tiers.<fast|default|strong|max>]` (ключи `agent` — имя
профиля, `model`, `effort`). Неизвестный ключ, неверный тип или ярус с
несуществующим профилем — код 2; прежние `models` и `default_agent` — код 2
с подсказкой. Новый ассистент — конфиг, не код.

| Ключ профиля | Смысл |
|---|---|
| `cmd` | argv вызова; промпт — в stdin, если нет `{prompt}` |
| `model_args`, `resume_args` | добавляются к `cmd`, когда есть модель / сессия |
| `model` | модель профиля: берётся при `--agent` вместо модели яруса |
| `effort_args` | добавляются после `model_args` и `model_extra`, когда задано усилие |
| `model_extra` | таблица id модели (после маршрутизации) → аргументы после `model_args` |
| `permissions` | таблица режима прав плана → аргументы после `cmd` (`auto`/`full`); накладывается на встроенную по ключам; профиль без таблицы: `auto` — `cmd` как есть, `full` — код 2 |
| `result`, `text`, `session`, `error`, `cost` | пути `json:a.b` в JSON-выводе: result JSON, текст ответа, id сессии, флаг ошибки, стоимость |
| `tokens` | список путей `json:a.b`; сумма — токены вызова (`claude`: `usage.input_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`); нет ключа — не считаются |
| `stream` | декодер JSON Lines: `"claude"`, `"codex"`, `"pi"`, `"opencode"`, `"dsh"` или пусто; сворачивает поток в объект для путей `json:` и даёт живость, действие агента и (у `claude`) квоту — [профили](design/runner.md#профили-агентов); без декодера — только итоги |
| `output_tokens` | путь `json:a.b` к выходным токенам (`claude`: `usage.output_tokens`) |
| `turns` | путь `json:a.b` к числу ходов агента (`claude`: `num_turns`) |
| `limit`, `limit_wait` | regex сообщения о лимите; ожидание, если время сброса не найдено (1800 с) |
| `auth` | regex «CLI агента не залогинен»: прогон останавливается сразу, без повторов |
| `timeout` | секунд на вызов; 0, отрицательное или нет ключа — без таймаута. Наступивший при активном агенте (события моложе 60 с или инструмент в работе) — не отказ: та же сессия продолжается, в итоге — `hint: <id> needs more than <cap> on <route>: split the unit or set timeout: in the plan header` |
| `idle_timeout` | секунд тишины потока без инструмента в работе до снятия вызова как зависшего: 0 — выключено, 5–86400; у встроенных 1200 |
| `instructions` | текст в конец промпта юнита; только в режиме прав `auto` |
| `quota` | argv опроса квоты на старте `run` без вызова модели; вывод разбирает `stream` (`claude`: `/usage`, `codex`: JSON-RPC `app-server`); пусто — не опрашивать; у Pi квота встроена для `zai/...` |

Таблица `[display]` (рядом с `[tiers]` и `[agents]`) настраивает экран квоты;
неизвестный ключ, значение или тип — код 2:

| Ключ `[display]` | Смысл |
|---|---|
| `quota` | `"used"` (по умолчанию) — доля израсходованного окна; `"left"` — остаток: процент, полоса, дельта и цвета от него, пороги зеркальные (>50% зелёный, 21–50% жёлтый, ≤20% красный) |
| `resets` | `true` (по умолчанию) — время сброса в строке квоты: `↻ HH:MM` для окна 5h, `↻ Mon HH:MM` для длинных; `false` — без него. В текстовом выводе — только с `-v` |

Плейсхолдеры аргументов: `{prompt}`, `{model}`, `{effort}`, `{model_variant}`
(`model#effort`, без усилия — модель без `#`; для `opencode`), `{schema}`
(JSON-схема result inline), `{schema_file}`, `{session}`.

```toml
[tiers]
default = {model = "opus"}    # ярус default теперь opus
fast = {model = "claude-haiku-x"}
strong = {agent = "codex", model = "<id модели OpenAI>", effort = "high"}

[display]
quota = "left"                # остаток подписки, а не израсходованное

[agents.claude.model_extra]   # модели без режима auto — явные права
claude-haiku-x = ["--allowedTools", "Write", "Edit", "Bash"]

[agents.codex]
timeout = 7200
```

Профиль без `result` берёт result JSON из последнего блока ` ```json `, без
`resume_args` повторяет попытку полным промптом. `docflow run --agent codex`
— все юниты на одном ассистенте; `agent = "codex"` в ярусе — только его.

Встроенные ассистенты — что нужно человеку (аргументы, декодеры, права —
[профили агентов](design/runner.md#профили-агентов)):

- `claude` — `claude auth login`.
- `codex` — `codex login`; песочница workspace-write с сетью, запись вне
  репозитория идёт через эскалацию с автоматическим ревьюером.
- `pi` — вход в провайдера Pi; модель в тройке как есть: `executor: pi |
  zai/glm-5.3 | high`, `zai/glm-5.3-flash` (подписка z.ai) или
  `local-lan/<id>`. Квота опрашивается только для `zai/…`. Скиллы —
  `docflow install-skills --agent pi`; headless Pi пропускает `.pi/skills/`
  без project trust (`~/.pi/agent/trust.json` или `--approve`), личный
  каталог trust не требует.
- `opencode` — собственный вход CLI; тройка `opencode |
  opencode-go/deepseek-v4.1-flash | high`. Песочницы нет: `auto` и `full`
  оба дают `--auto`, `full` изоляцией не является. Квота подписки go —
  окна `rolling`, `weekly`, `monthly` в строке `quota:`. Скиллы —
  `--agent opencode` (`.opencode/skills`, `~/.config/opencode/skills`).
- `dsh` (DeepSeek Harness, `npm i -g @deepseek-ai/dsh`) — тройка `dsh |
  deepseek-account/deepseek-v4-pro | high`: провайдер `deepseek-account`
  (подписка) или `deepseek-official` (API-ключ), усилие `off`, `low`,
  `high`, `max`; голая `deepseek-flash` — ошибка. Подписка вместо
  `DEEPSEEK_API_KEY`: войти в `dsh web`, в
  `$DSH_HOME/profiles/headless/cordis.patch.yml` включить entry
  `authorization`, `deepseek-account` и `credentials` (`disabled: false`) и
  задать `agent-default-model` с `provider: deepseek-account`; проверка —
  `dsh headless "Ответь одним словом: ок"`. Песочница снята в обоих режимах
  (`full` изоляцией не является); квоты и стоимости нет. Скиллы — `--agent
  dsh` (`.dsh/skills`, `~/.dsh/skills`); в ростере — канал доступов
  `deepseek-account` и `deepseek-official` (`agents = ["dsh"]`).

## Ростер

Ростер подбирает маршруты ролей при составлении плана, а не во время
прогона: `docflow roster brief` показывает цепочки, бюджеты и объяснение,
`docflow roster apply Pxxx` пишет их в шапку плана. `run`, `lint` и `cycle`
ростер не читают, формат плана остаётся `docflow: 3`. Устройство, резолвер и
бюджеты — [design/roster.md](design/roster.md); маршрут `strata/…` — `Local`
без квоты.

| Файл | Что это |
|---|---|
| `~/.config/docflow/roster.toml` | политика: доступы в порядке приоритета и таблица «роль → модель@усилие»; правится в `roster web` или руками |
| `$XDG_CACHE_HOME/docflow/roster/catalog.json` | каталог: что нашли `scan` и пробы авторизации; расходный кэш вне рабочего дерева |
| `~/.config/docflow/roster.state.toml` | временные условия: выключения, expiry, активная политика |
| `~/.config/docflow/roster.variants.toml` | глобальные политики поверх таблицы: `off`, порядок, ячейки; машинный файл |
| `docs/roster.variants.toml` | варианты проекта от корня checkout; машинный файл, коммитится с проектом |

`docflow roster web` — локальный редактор таблицы: строки-доступы,
столбцы-роли, в ячейке модель из каталога и усилие (у модели с ризонингом,
чьих уровней каталог не знает, — уровни агента, например `--thinking` у Pi),
номер — место в цепочке роли; вкладки — политика `default` и именованные;
под таблицей — итоговые цепочки. Каждое действие проверяется и пишется в
`roster.toml` (комментарии не сохраняются) или в файл вариантов. Варианты
создаёт, выбирает, удаляет и применяет к шапке плана только `roster web`;
условия правят команды `roster off/on/…`, каталог собирает `roster scan`.

`--roster PATH` переопределяет путь политики; файл состояния — рядом с
суффиксом `.state.toml`, глобальные варианты — `roster.variants.toml` там же.
Нет файла состояния или вариантов — пустой слой, не ошибка. Имя политики
уникально в обоих слоях вариантов (дубль — ошибка с путями обоих файлов):
`--preset NAME` значит одно и то же. Файлы вариантов переписываются целиком.

### Каталог

```sh
docflow roster scan            # Pi, OpenCode, Codex/Claude, auth-пробы
docflow roster scan --online   # плюс необязательные метаданные models.dev
docflow roster models          # модели доступов из ростера
docflow roster models zai      # модели одного доступа
docflow roster status          # каналы, auth, число моделей, условия
```

`scan` пишет кэш и печатает сводку. Проблема одного источника (нет CLI, HTML
вместо JSON, битый кэш) — предупреждение в stderr, код 0, каталог частичный.
Модели не вызываются, квота не тратится, ключи не печатаются. `models` и
`status` читают только кэш: пустой — код 0 с подсказкой `docflow roster
scan`, неизвестный `models ACCESS` — код 2 со списком доступов. Неизвестные
контекст, `max_out` и цена печатаются как `unknown`.

### Политика `roster.toml`

Формат — `version = 4`: таблица «доступ × роль». Старый ростер (v1–v3) даёт
код 2 и подсказку `docflow roster migrate`.

```toml
version = 4

# Порядок доступов — приоритет: цепочка роли — её столбец сверху вниз.
[access.opencode-go]
agents = ["pi", "opencode"]   # два канала, один доступ и одна квота
kind   = "subscription"       # необязательно: скан сам знает opencode-go

[access.opencode-go.roles]    # роль = "МОДЕЛЬ@УСИЛИЕ"
executor = "deepseek-v4.1@high"
cheap    = "deepseek-v4.1-flash@high"
review   = "deepseek-v4.1@high"

[access.zai]
agents  = ["pi"]
kind    = "subscription"
check   = true                # нужен независимый ревьюер, сам не судит
expires = "2026-12-31"

[access.zai.roles]
executor = "glm-5.3@high"
cheap    = "glm-5.3-flash@low"

[access.codex]
agents = ["codex"]
kind   = "subscription"

[access.codex.roles]
heavy      = "gpt-6.1-sol@high"
acceptance = "gpt-6.1-sol@medium"
replan     = "gpt-6.1-sol@medium"
```

Доступ — единица квоты и независимости: `opencode-go` через `pi` и через
`opencode` — один доступ, fallback повторяет доступ, а не пару `agent|model`.
`agents` обязателен; маршрут идёт через первый канал, который знает модель и
залогинен (`roster status` показывает отказавший: `ready(pi:not_ready)`).
`kind` — `subscription`, `metered` (за токены) или `local`; известным доступам
скан ставит его сам. `opencode` — OpenCode Zen за токены, `opencode-go` —
подписка OpenCode Go. Ячейка `[access.NAME.roles]` — модель и усилие роли
(`МОДЕЛЬ` без `@` — без усилия); нет ключа — роль доступ не берёт. `check`
запрещает судейские ячейки (`review`, `acceptance`, `replan`). Новая модель
каталога сама в роль не встаёт.

Политики (варианты) лежат в `roster.variants.toml` и проектном
`docs/roster.variants.toml`; политика по умолчанию — сама таблица:

```toml
version = 3

[preset.zai-first]
order = ["zai"]               # эти доступы первыми
off   = ["claude"]            # выключены в этой политике

[preset.zai-first.roles.codex]
executor = "gpt-6-luna@high"  # другая ячейка
heavy    = ""                 # пустая строка — очистить ячейку
```

### Условия

Постоянное условие пишется в `roster.state.toml`, политику не меняет:

```sh
docflow roster off claude --until 2026-11-30  # до конца дня включительно
docflow roster on claude                      # снять off и его дату
docflow roster expire zai 2026-12-31
docflow roster reset                          # удалить файл состояния
```

Неизвестный доступ или пресет — код 2 до записи. Истёкший `off`
отбрасывается при загрузке, файл переписывает следующая мутация.

Разовое условие — флаги одного вызова, в памяти: `--preset NAME`, `--off
claude,local`, `--expires zai=2026-12-31`. Их принимают `roster brief` и
`roster apply`, так что бриф и запись шапки видят одно условие.

### Бриф, объяснение и применение

```sh
docflow roster brief
docflow roster show             # алиас brief
docflow roster explain P055
docflow roster apply P055 --preset low --dry-run
docflow roster apply P055 --preset low
```

`brief` печатает условия в силе, по строке на роль (`chain`, `budget`,
`scope`), матрицу `review:`, `why:` и предупреждения. `budget` —
рекомендация чтения для нарезки юнита, не ключ плана и не проверка `lint`;
у судейских ролей бюджета нет, их `scope` — `diff/test`. Пустая цепочка и
цепочка из одного маршрута — предупреждения брифа. `check`-доступ без
независимого ревьюера — ошибка политики.

`explain PLAN` печатает `ключ: старое → новое` и предупреждения ролей,
которые план использует; файлы не меняет.

`apply PLAN` пишет в шапку `executor:`, `fallback:`, `replan:` и ключи ролей,
на которые ссылается `Role:` хотя бы одного юнита; ключи ролей без юнитов
удаляет, `Executor:` юнитов и прочие ключи с их порядком не трогает, новые
ставит после `executor:`/`fallback:`. Открытый юнит без цепочки `executor`
или своей роли останавливает `apply` до записи. План не `draft`/`active`/
`frozen` — код 2; идёт прогон (`run.lock`) — код 5; `lint` после записи
упал — файл восстановлен, код 2. Предупреждения (юнит с `Executor:` вне
условий, `Read:`-прокси больше бюджета, роль без fallback) — в stderr.
Изменений нет — код 1; `apply` не коммитит. `--dry-run` — без записи,
`--json` сохраняет код выхода.

### Миграция

```sh
docflow roster migrate --dry-run # напечатать v4-политику и файлы рядом
docflow roster migrate           # заменить их, сохранив ФАЙЛ.bak
```

`migrate` ведёт v1 и v2 через v3 в v4: правила классов, бюджетов и
`trusted` v3 прогоняются один раз по кэшу каталога и записываются ячейками —
поэтому сначала `roster scan`; порядок доступов — порядок кандидатов v3.
Пресеты становятся политиками в файле вариантов, `budget = "off"` — условием
`off`, условия бюджетов и allowlist отбрасываются с примечанием. Уже v4 — код
2, ничего не пишется.

## Бенчмарк

Эталонный корпус сравнивает задачи `small`, `medium` и `large` на общей
базе ([формат корпуса и результатов](design/bench.md)):

```sh
go run ./tools/bench validate --task all     # оракулы падают на базе и проходят на эталоне
go run ./tools/bench run --task all --dry-run  # задачи, маршруты, рабочие пути
go run ./tools/bench run --task small
go run ./tools/bench report                  # таблица тренда из записей
```

Прогон вызывает модели и расходует квоту; `large` идёт несколько часов.
Каждый прогон записывается и коммитится в `bench/results/`; `--results DIR`
у `run` и `report` выбирает другой каталог внутри checkout.

## Блок и replan

- План разошёлся с кодом, `Verify` не проверяет честно, три `verify`
  подряд без прогресса → `docflow block <id> --reason "…"`; выдача юнитов
  останавливается (код 3).
- Шаг, который доступен только человеку (вход в аккаунт, устройство,
  доступ, решение) → `docflow block <id> --reason "…" --needs human`;
  прогон встаёт (код 3), `status` по последнему пункту `Blocked` печатает
  `(needs human)` и строку `unblock:` с командой.
- Снял человеческий блок сам человек:
  `docflow unblock <id> --note "что сделано"` → юнит снова `[~]`, в
  Handoff — пункт `Unblocked: …`; снятый юнит сначала проверяет условие
  блока, заметка — заявление, не доказательство. Команда не коммитит и
  блок `plan` не снимает (код 5).
- Replan правит только юниты `[ ]` и `[!]`; новые юниты — следующими
  номерами, существующие не перенумеровываются.
- Replan не трогает опорные проверки: `Verify:`, `Do:`, `Role:` юнитов
  `Role: review` и `Role: acceptance` и их переход в `[-]` запрещены;
  статус и `Depends:` опорных юнитов менять можно. Открытый юнит не
  помечается `[x]` — раннер `[x]` пропускает, проверка исчезла бы молча.
- Смену `Verify:` обычного юнита docflow записывает в `## Decisions` сам:
  пункт `Replan changed check: …` виден в сводке прогона и в промптах
  review и acceptance. Правки тестов юнита помечает его коммит: блок
  `Test changes:` в теле и строки `test change:` в выводе — они видны
  ревью, приёмке и человеку.
- Файлы коммита, которых не называют `Read/Do/Traps/Verify` юнита (путь, имя
  файла или каталог; файл плана не считается), раннер печатает под юнитом
  строками `outside Do:` — подсказка человеку, юнит не падает.
- Снятие блока `plan`: незакоммиченная работа остаётся в силе → `[~]`; работа
  не нужна → решает пользователь; работы нет → `[ ]`; юнит не нужен → `[-]`.
- `verify --skip "<причина>"` — только когда проверку нельзя запустить в
  окружении (нет устройства, сети) и пользователь согласен.

## Закрытие

`docflow run --close` поручает закрытие агенту после последнего юнита или
сразу, если план уже `ready to close`; маршрут — цепочка `cheap:`, без неё —
`executor:` + `fallback:` (виден в `docflow run --dry-run --close`). Раннер
проверяет результат и коммитит `<id>: close plan`; провал проверки — код 2
со списком причин, изменения остаются для исправления
([граница плана](design/runner.md#незавершённый-коммит-и-граница-плана)).
Без `--close` готовый план ждёт отдельного закрытия.

Шаги `bsv-docflow-close` (агент выполняет их сам):

1. `docflow status` пишет `ready to close` — все юниты `[x]`/`[-]`.
2. Ссылки на `docs/plans/Pxxx…` переводятся на будущую архивную запись.
3. `docflow close Pxxx` — архивная запись с перенесёнными `## Decisions`;
   план и строка Queue удаляются.
4. Агент заполняет запись: `Outcome`, сжатые `Decisions`, `Gotchas`,
   `Follow-ups`.
5. Architecture — текущее устройство и ссылка на запись; README — строки
   состояния; CHANGELOG — наблюдаемое поведение (нет файла — шаг
   пропускается); Follow-ups — строками в Backlog, включая незакрытые
   пункты Handoff `Unverified: …`; только наблюдение вне Goal, ждущее
   внешнего события, остаётся в записи строкой `Waits: …`.

Брошенный план закрывается так же: оставшиеся юниты вручную в `[-]`, в
`Decisions` — `план брошен — почему — что вместо`.

## Что куда идёт

| Информация | Место | Кто пишет | Живёт до |
|---|---|---|---|
| Идея, задача | строка `## Backlog` в `docs/docflow.md` | человек, `close` из Follow-ups | взятия в план |
| Порядок планов | `## Queue` в `docs/docflow.md` | `new`, `close` | закрытия плана |
| Цель, рамки, ловушки плана, repro | шапка плана | `bsv-docflow-plan` | закрытия плана |
| Что сделать в сессии | юнит: `Read`, `Do`, `Verify`, `Traps`, `Commit` | `bsv-docflow-plan` | закрытия плана |
| Статус юнита | маркер `[ ]` `[~]` `[x]` `[-]` `[!]` в плане | `docflow` | закрытия плана |
| Записка следующему юниту | `Handoff:` юнита | `docflow done` | закрытия плана, в архив не идёт |
| Решение по ходу | `## Decisions` плана | `docflow done --decision`, планировщик | переносится в архив |
| Почему так (закрытое) | `docs/archive/Pxxx-slug.md` | `close` + `bsv-docflow-close` | навсегда; правка — только `superseded_by` |
| Ловушки после закрытия | `## Gotchas` архивной записи | `bsv-docflow-close` | навсегда |
| Несделанное | `## Follow-ups` записи → Backlog, кроме `Waits:` | `bsv-docflow-close` | взятия в план |
| Текущее устройство | architecture проекта + ссылка на архив | `bsv-docflow-close` | пока решение действует |
| Текущее состояние | `README.md` | `bsv-docflow-close` | следующего изменения |
| Что изменилось для пользователя | `CHANGELOG.md` | `bsv-docflow-close` по `bsv-changelog` | навсегда |
| Изменения кода | git-коммит юнита | `docflow commit` | навсегда |
| Счётчики тестов, логи сборки | ответ в чате | агент | — в доки не пишется |

Отменённое решение: в architecture правится на новое, у старой архивной
записи — `superseded_by: Pyyy` во frontmatter.

## Ветки

- Один план — одна ветка или worktree; параллельно — только разные планы.
- Ветки создаёт и переключает пользователь, `docflow` их не проверяет.
- Совпадение id планов из разных веток ловит `lint` после merge.

## Типовые ситуации

| Ситуация | Что делать |
|---|---|
| `prompt`: `working tree is dirty` (код 5) | чужие изменения: закоммитить или убрать самому, агент их не трогает |
| `prompt`: `… is in progress` | другой юнит `[~]` — продолжить его: `docflow prompt` без id |
| `verify` падает (код 4) | исправить по хвосту вывода; три раза без прогресса — `block` |
| `status`: `blocked:` + `unblock:` (код 3) | сделать шаг самому, затем `docflow unblock <id> --note "что сделано"` |
| `status`: `blocked:` (код 3) | replan через `bsv-docflow-plan` |
| `status`: `ready to close` | закрыть через `bsv-docflow-close` |
| `next`/`prompt`: делать нечего (код 1) | очередь пуста или все планы ждут `close` |
| Код 2 и подсказка `docflow migrate` | формат старой версии: `docflow migrate`, затем `lint` |
| Мелкая правка что-то решила | `docflow note <slug>` — запись в архиве без плана |
| Нужно пропустить юнит | replan: `[-]` |
| Отложить план на время | `docflow freeze <id> --reason "…"` + коммит, затем `docflow thaw <id>` + коммит когда вернуться |
| Юнит меняет несколько репо | в плане указан ключ `repos:`, агент не коммитит сам — `docflow commit` охватит все |
