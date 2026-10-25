# Скрипт и версия

## CLI docflow

Go 1.24+, stdlib, модуль `bsv-docflow`: точка входа `cmd/docflow`, код по
слоям в `internal/`, шаблоны встроены в бинарник. Пользовательская
установка — `install.sh` из корня репозитория: скачивает релиз и запускает
`docflow install` (без Go, см. [«Релизы»](#релизы)). Для разработки
`tools/install-dev.sh` собирает
`bin/docflow`, создаёт симлинк в выбранном каталоге (`~/.local/bin` по
умолчанию) и ставит скиллы агентам, чей CLI есть в PATH, и в
`~/.zcode/skills` при наличии `~/.zcode`; `--no-skills` пропускает скиллы
([P028](../archive/P028-go-cutover.md)). Скиллы читаются рядом с бинарником: в dev-сборке — из
checkout, в standalone — из `~/.docflow/current`. Решение о переносе и его
ограничениях: [P024](../archive/P024-go-cli.md).

| Команда | Что делает |
|---|---|
| `init [--agent {claude,codex,pi}] [--skills]` | `docs/docflow.md`, `plans/`, `archive/`, строка в AGENTS.md; `--skills` — ещё копии скиллов в каталоге проекта выбранного агента; default `claude` |
| `status` | очередь, текущий план, прогресс |
| `next` | id следующего доступного юнита |
| `prompt`, `verify`, `done`, `commit`, `block [--needs human\|plan]` | цикл юнита, см. [«Цикл юнита»](cycle.md#цикл-юнита) |
| `stats [Pxxx]` | расход токенов по юнитам, см. [«Учёт токенов»](cycle.md#учёт-токенов) |
| `lint` | структура, id и их уникальность, Depends, бюджет `Read:` |
| `new <slug>` | план из шаблона со следующим id, строка в Queue |
| `activate <id>` | `draft` → `active` после lint плана без ошибок (иначе код 2, план не меняется); id в конец Queue, если его там нет; замороженный — отказ с подсказкой `thaw`; не коммитит, подсказка `commit: Pxxx: activate plan` |
| `close <id>` | проверяет: все юниты `[x]`/`[-]`, на план нет ссылок; пишет архивную запись с перенесёнными `## Decisions`, удаляет план и строку Queue или Frozen |
| `freeze <id> [--reason TEXT]` | план из Queue в `## Frozen`, `status: frozen`; юнит `[~]` → `[ ]`, при незакоммиченных правках — код 5; не коммитит, подсказка `commit: Pxxx: freeze plan` |
| `thaw <id>` | план из Frozen в конец Queue, `status: active`; предупреждения ссылок плана в stderr; не коммитит |
| `note <slug>` | архивная запись без плана |
| `migrate` | формат предыдущих версий → текущая |
| `install` | укладывает распакованный релиз (рядом с бинарником `skills/` и `LICENSE`) в `~/.docflow/versions/<X.Y.Z>`, переключает симлинк `current`, создаёт `bin/docflow`, настраивает PATH и ставит личные симлинки скиллов |
| `update [--check]` | скачивает и активирует последний релиз (в `versions` остаются текущая и предыдущая версии; откат — ручное переключение `current`, следующий update ставит релиз поверх оставшегося каталога версии); `--check` — только проверить; в dev-checkout отказ |
| `uninstall` | убирает `~/.docflow`, свой блок PATH из профилей оболочки и личные симлинки скиллов; в dev-checkout отказ |
| `install-skills [--agent {claude,codex,pi}] [--target DIR]` | личные симлинки `skills/*` в `~/.claude/skills/` для Claude Code или `~/.agents/skills/` для Codex CLI или `~/.pi/agent/skills/` для Pi; default `claude`, `--target` явно заменяет личный каталог |
| `install-skills --project [--agent {claude,codex,pi}]` | копии `skills/*` в `<project>/.claude/skills/` или `<project>/.agents/skills/` или `<project>/.pi/skills/`; повтор обновляет свои копии; `--target` вместе с `--project` — ошибка |
| `doctor [--agent {claude,codex,pi}] [--target DIR]` | проверяет установленный `docflow`, личный каталог выбранного агента (или `--target`) и проектный каталог этого агента по полю `name:`; для `pi` ещё строка `pi: <версия> <путь>` из `pi --version`; default `claude` |
| `roster show [--profile NAME] [--roster PATH]` | печатает цепочки ролей из личного ростера `~/.config/docflow/roster.toml`: строку `spend: … · check: … · trusted: …` и шапку плана `executor:`, `fallback:`, `cheap:`, `heavy:`, `review:`, `acceptance:`, `replan:`; формат и подбор — [«Ростер» в usage](../usage.md#ростер); `run` ростер не читает; ошибка ростера — код 2 с файлом и строкой |
| `roster apply PLAN [--profile NAME] [--roster PATH]` | пишет цепочки ростера в шапку плана из `docs/plans/`: `executor:`, `fallback:`, `replan:` и ключи ролей юнитов с `Role:`; ключи ролей без юнитов удаляет, прочие ключи и их порядок не трогает, новые ключи ставит после `executor:`/`fallback:`; статусы `draft|active|frozen`, иначе код 2; идёт прогон (`run.lock`) — код 5; после записи план проверяется `lint`, при ошибках восстанавливается — код 2; печатает `ключ: старое → новое` и подсказку commit, изменений нет — код 1; не коммитит |
| `--version` | версия и sha репозитория (`+dirty` при изменениях) |
| `run [--dry-run] [--replans N]` | юниты текущего плана агентом до `ready to close`; `--replans` — лимит авто-replan на фазу (по умолчанию 3, максимум 2 на юнит), см. [«Раннер»](runner.md) |

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
  архива `docflow`, `LICENSE`, `skills/`;
- суммы: `<base>/download/vX.Y.Z/checksums.txt` (формат `sha256sum`).

`tools/release.sh VERSION OUTDIR` собирает ту же раскладку локально — для
`python3 -m http.server` в тестах. Платформы: darwin и linux, amd64 и
arm64; Windows не поддерживается.
