# Скрипт и версия

## CLI docflow

Go 1.26+, модуль `bsv-docflow`: точка входа `cmd/docflow`, код по
слоям в `internal/`, шаблоны встроены в бинарник. Сторонние Go-библиотеки
разрешены там, где stdlib не покрывает нужду; каждая — осознанное решение
и постоянное обслуживание, инвариант — самодостаточная установка: CGO не
включать ([P042](../archive/P042-dependency-policy.md)). Пользовательская
установка — `install.sh` из корня репозитория: скачивает релиз и запускает
`docflow install` (без Go, см. [«Релизы»](#релизы)). Для разработки
`tools/install-dev.sh` собирает
`bin/docflow` и создаёт симлинк `docflow-dev` в выбранном каталоге
(`~/.local/bin` по умолчанию); `docflow` на PATH остаётся релизом, скиллы
он не трогает — личные симлинки скиллов переключает
`tools/switch-skills.sh`
([P057](../archive/P057-dev-cli-skill-switch.md)). Скиллы читаются рядом с бинарником: в dev-сборке — из
checkout, в standalone — из `~/.docflow/current`. Решение о переносе и его
ограничениях: [P024](../archive/P024-go-cli.md).

| Команда | Что делает |
|---|---|
| `init [--agent {claude,codex,pi,opencode}] [--skills]` | `docs/docflow.md`, `plans/`, `archive/`, строка в AGENTS.md; `--skills` — ещё копии скиллов в каталоге проекта выбранного агента; default `claude` |
| `status` | очередь, текущий план, прогресс; при блоке — класс (`needs human` / `needs plan`) и для человека строка `unblock:` с командой |
| `next` | id следующего доступного юнита |
| `prompt`, `verify`, `done`, `commit`, `block [--needs human\|plan]`, `unblock [--note NOTE]` | цикл юнита, см. [«Цикл юнита»](cycle.md#цикл-юнита) |
| `stats [--runs] [Pxxx]` | расход токенов по юнитам, см. [«Учёт токенов»](cycle.md#учёт-токенов) |
| `lint` | структура, id и их уникальность, Depends, бюджет `Read:` |
| `new <slug>` | план из шаблона со следующим id, строка в Queue |
| `activate <id>` | `draft` → `active` после lint плана без ошибок (иначе код 2, план не меняется); id в конец Queue, если его там нет; замороженный — отказ с подсказкой `thaw`; не коммитит, подсказка `commit: Pxxx: activate plan` |
| `close <id>` | проверяет: все юниты `[x]`/`[-]`, на план нет ссылок; пишет архивную запись с перенесёнными `## Decisions`, удаляет план и строку Queue или Frozen |
| `freeze <id> [--reason TEXT]` | план из Queue в `## Frozen`, `status: frozen`; юнит `[~]` → `[ ]`, при незакоммиченных правках — код 5; не коммитит, подсказка `commit: Pxxx: freeze plan` |
| `thaw <id>` | план из Frozen в конец Queue, `status: active`; предупреждения ссылок плана в stderr; не коммитит |
| `note <slug>` | архивная запись без плана |
| `migrate` | формат предыдущих версий → текущая |
| `install [--skills {global,project}]` | укладывает распакованный релиз (рядом с бинарником `skills/` и `LICENSE`) в `~/.docflow/versions/<X.Y.Z>`, переключает ссылку `current`, создаёт точку запуска, настраивает PATH и ставит личные ссылки скиллов (режим `--skills`: `global` — личные ссылки, `project` — только их чистка; без флага — сохранённый в `~/.docflow/skills-mode` режим, вопрос один раз в терминале, без терминала — `global`); раскладка — [«Релизы»](#релизы) и [skills.md](skills.md) |
| `update [--check] [--skills {global,project}]` | скачивает и активирует последний релиз (в `versions` остаются текущая и предыдущая версии; откат — ручное переключение `current`, следующий update ставит релиз поверх оставшегося каталога версии); `--check` — только проверить; явный `--skills` применяется к ссылкам и без нового релиза; в dev-checkout отказ |
| `uninstall` | убирает `~/.docflow`, свой блок PATH из профилей оболочки и личные симлинки скиллов; в dev-checkout отказ |
| `install-skills [--agent {claude,codex,pi,opencode}] [--target DIR]` | личные симлинки `skills/*` в `~/.claude/skills/` для Claude Code или `~/.agents/skills/` для Codex CLI или `~/.pi/agent/skills/` для Pi или `~/.config/opencode/skills/` для OpenCode; default `claude`, `--target` явно заменяет личный каталог |
| `install-skills --project [--agent {claude,codex,pi,opencode}]` | копии `skills/*` в `<project>/.claude/skills/` или `<project>/.agents/skills/` или `<project>/.pi/skills/` или `<project>/.opencode/skills/`; повтор обновляет свои копии; `--target` вместе с `--project` — ошибка |
| `doctor [--agent {claude,codex,pi,opencode}] [--target DIR]` | проверяет установленный `docflow`, личный каталог выбранного агента (или `--target`) и проектный каталог этого агента по полю `name:`; для `pi` ещё строка `pi: <версия> <путь>` из `pi --version`; default `claude` |
| `roster scan [--online] [--refresh] [--json]` | собирает каталог из Pi, OpenCode, встроенной таблицы Codex/Claude и auth-проб, пишет кэш `$XDG_CACHE_HOME/docflow/roster/catalog.json` и печатает сводку доступов; `--online` добавляет models.dev, `--refresh` перечитывает его кэш; ошибка источника — предупреждение в stderr при коде 0 |
| `roster models [ACCESS] [--all] [--json]` | модели доступа, ростерных доступов или всего каталога; читает только кэш; неизвестный `ACCESS` — код 2 со списком известных |
| `roster status [--json]` | доступы с каналами, auth, числом моделей и источниками, затем активные условия с источником; только кэш |
| `roster brief [--preset NAME] [--off A,B] [--expires A=DATE] [--json]` | условия в силе, цепочка, бюджет и scope каждой роли, матрица `review:`, `why:` и предупреждения; разовые флаги не сохраняются |
| `roster show` | алиас `brief` |
| `roster explain PLAN [--json]` | сравнивает шапку плана с брифом: `ключ: старое → новое` и предупреждения ролей плана; ничего не пишет |
| `roster apply PLAN [--preset NAME] [разовые флаги] [--dry-run] [--json]` | пишет в шапку плана `executor:`, `fallback:`, `replan:` и ключи ролей с `Role:`; ключи без юнитов удаляет, `Executor:` юнитов не трогает; разовые флаги те же, что у brief, и не сохраняются; отказ до записи, если открытому юниту не хватает цепочки; `run.lock` — код 5; после `lint`-ошибки восстанавливает файл — код 2; изменений нет — код 1; не коммитит |
| `roster off ACCESS [--until DATE]`, `on ACCESS`, `expire ACCESS DATE`, `reset` | правят только `roster.state.toml`; неизвестный доступ или пресет — код 2 до записи |
| `roster migrate [--write]` | переводит ростер v1, v2 или v3 на v4 вместе с файлами вариантов и условиями; `--write` сохраняет `ФАЙЛ.bak`; уже v4 — код 2 |
| `--version` | версия и sha репозитория (`+dirty` при изменениях) |
| `run [--dry-run] [--replans N]` | юниты текущего плана агентом до `ready to close`; `--replans` — лимит авто-replan на фазу (по умолчанию 3, максимум 2 на юнит), см. [«Раннер»](runner.md) |
| `tui [--attempts N] [--replans N] [--roster PATH]` | рабочее место из четырёх экранов: «Проект» — Queue, Frozen, Backlog, карточка юнита, параметры, новый план, редактор и коммит служебных файлов; «Ростер» — доступы, условия, варианты и запись варианта в шапку плана; «Прогон» — живой поток сообщений и рассуждений; «История» — прошлые прогоны и повтор потока; пауза, остановка и восстановление; `--roster` — файл ростера экрана; требует TTY, см. [tui.md](tui.md) |

Коды выхода: 0 — успех, 1 — делать нечего, 2 — ошибка формата, 3 — блок,
4 — проверка или commit-hook упал, 5 — нарушен порядок шагов, 6 — установка отстала
(`doctor`), 7 — агент не справился (`run`).
Подсказка обновления `doctor` сохраняет выбранные `--agent` и `--target`.
Отсутствующий скилл выводится как `not installed`, но сам по себе не даёт код 6.

## Версия

- Одна версия SemVer на весь репозиторий — CLI и скиллы меняются
  согласованно; версия — `var version` в `cmd/docflow/main.go`, релизная
  сборка подставляет её через `-ldflags -X main.version=` (dev-сборка без
  релизных флагов показывает исходное значение); история —
  `CHANGELOG.md`.
- Тег `vX.Y.Z` ставится на коммит закрытия плана: внутри юнита коммит
  делает `docflow commit`, тегу до него не на что встать. Push тега
  запускает workflow релиза (`.github/workflows/release.yml`): CI сверяет
  тег с `var version` (`tools/check-release-tag.sh`), гоняет тесты,
  собирает ассеты через
  `tools/release.sh` и публикует GitHub Release; заметки — секция версии
  из `CHANGELOG.md` (`tools/release-notes.sh`), без неё релиз не
  собирается ([P030](../archive/P030-public-release.md)). Затем job
  `smoke` ставит опубликованный релиз через `install.sh` на linux
  amd64/arm64 и macOS и запускает его.
- Релиз режет `tools/cut-release.sh`: `prepare BUMP` локально (main
  чистый и не отстаёт от `origin`, нет активного плана, гейты `go test`,
  `test-install.sh`, `audit-public.sh`; перенос `[Unreleased]`, bump,
  коммит `Release docflow X.Y.Z`, тег), `publish` — push main в `origin`,
  тег только после зелёного CI, ожидание релиза и проверка ассетов.
  Версию выбирает человек, push — только `publish`
  ([P034](../archive/P034-release-cut.md)).
- Основной репозиторий — GitHub `sboko83/docflow`, его история начата
  orphan-коммитом 0.7.0; прежняя история в него не входит
  ([P030](../archive/P030-public-release.md)).
- Версия формата `docflow: N` — отдельная: совместимость документов
  проекта, не кода.
- Отставание установленного скилла определяется сравнением содержимого с
  `skills/`, не полем версии: поле надо помнить поднимать —
  [P005](../archive/P005-versioning.md).

## Релизы

Раскладка URL, база `https://github.com/sboko83/docflow/releases`,
переопределение — переменная `DOCFLOW_RELEASE_URL` (действует в
`install.sh`, `docflow update` и ежедневной проверке):

- последняя версия: `<base>/latest/download/VERSION` (текст `X.Y.Z`);
- архив: `<base>/download/vX.Y.Z/docflow_<os>_<arch>.tar.gz`, в корне
  архива `docflow` (`docflow.exe` на Windows), `LICENSE`, `skills/`;
- суммы: `<base>/download/vX.Y.Z/checksums.txt` (формат `sha256sum`).

`tools/release.sh VERSION OUTDIR` собирает ту же раскладку локально — для
`python3 -m http.server` в тестах. Платформы: darwin, linux и windows,
amd64 и arm64.

На macOS и Linux `current` — симлинк на `versions/<X.Y.Z>`, точка
запуска — `~/.docflow/bin/docflow`. На Windows `current` и личные ссылки
скиллов — directory junction с абсолютной целью; точка запуска —
`%USERPROFILE%\.docflow\current\docflow.exe`, каталога `bin/` нет,
пользовательский `Path` указывает на `current`. Junction не требует
Developer Mode или прав администратора. При обновлении Windows-ссылка
`current` удаляется и создаётся заново; при неудаче переключения
восстанавливается предыдущая ссылка. Если старая версия ещё исполняется,
её очистка выдаёт предупреждение и откладывается до следующего обновления.
При `uninstall` Windows-бинарник переносится во временный каталог:
установка удаляется сразу, а отдельный PowerShell-процесс удаляет только
перенесённый файл и пустой временный каталог после выхода CLI.
Решения для Windows — [P040](../archive/P040-windows-support.md).
