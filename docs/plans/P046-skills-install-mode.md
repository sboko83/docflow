---
id: P046
title: Project-only skills install
type: feature
status: active
created: 2026-10-26
permissions: auto
gate: go test ./...
executor: pi | strata/swift-1.5-iq3_s | high
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
heavy: pi | zai/glm-5.3 | high; codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
review: claude | claude-sonnet-5-5 | high; codex | gpt-6.1-sol | high
acceptance: claude | claude-sonnet-5-5 | high; codex | gpt-6.1-sol | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
---
## Goal

The standalone install can leave the agents' personal skill directories
alone: `docflow install` asks once whether skills go global (personal
links, as now) or only into projects, remembers the answer, and
`docflow update` follows it without asking again. Switching to
project-only removes only docflow's own personal links; project copies
and foreign skills stay untouched. The binary and `PATH` setup do not
change.

## Context

- docs/design/skills.md:L56-115

## Constraints

- Go standard library only.
- Without a stored mode, a flag or a terminal, `install` and `update` keep
  today's behavior (global links) and print one hint line — scripted
  installs and CI keep working.
- Project copies (`<project>/.claude/skills`, `.agents/skills`,
  `.pi/skills`) are never written or removed by `install`/`update`.
- `install.sh` runs as `curl … | sh`: stdin is the script pipe, so the
  question reads the controlling terminal, never stdin.
- Language: code, comments, `README.md`, `CHANGELOG.md` in English;
  `docs/` in Russian; `README.md` and `README.ru.md` change together.
- A unit does not commit and does not rewrite history; the runner commits
  with the unit's `Commit:` line.

## Traps

- `go test ./...` takes ~3 min; run a package's tests while working and the
  full gate once at the end.
- `docflow` may be missing from `PATH`; build the checkout binary with
  `go build -o bin/docflow ./cmd/docflow` (`bin/` is gitignored).
- Never run the real `docflow install`/`update` against the user's HOME
  while working; tests use a temporary HOME, as
  `cmd/docflow/release_test.go` and `tools/test-install.sh` do.

## Decisions

- The mode is one word in `~/.docflow/skills-mode` (`global` or
  `project`) — the release home already belongs to docflow and is removed
  by `uninstall` — rejected: a key in the agents config (that file
  configures runs, not the installation).
- `update` asks only when no mode is stored; `--skills global|project`
  on `install` and `update` sets it explicitly, and `update --skills …`
  applies a new mode even when the release is up to date — rejected:
  asking on every update.
- Project-only mode does not copy skills into projects by itself — which
  projects use docflow is the user's call — rejected: scanning for
  `docs/docflow.md` under HOME.
- Вопрос пишется в stderr, ответ читается через term.OpenTTY (/dev/tty), а не stdin — stdin у install.sh занят пайпом curl — отклонено: чтение stdin или вывод вопроса в stdout (перехватывается в скриптовых установках)
- update --check не резолвит режим вообще — чистый сетевой запрос не должен иметь терминального побочного эффекта — отклонено: резолвить и на --check
- Update без нового релиза и без --skills не трогает ссылки и сохраняет сегодняшний точный вывод «up to date» (+hint, если режим не хранится) — без неожиданных relink на no-op — отклонено: всегда применять хранимый режим
- Неопознанный ответ на вопрос повторяет вопрос; пустая строка/EOF даёт global — отклонено: падать на опечатке
- Файл skills-mode валиден только со словами global/project, иное содержимое игнорируется (режим спросится заново) — отклонено: ошибка на испорченном файле
- Хarness detachment через python subprocess start_new_session вместо ставки на отсутствие терминала в CI — человек запускает скрипт в терминале, лог перенаправлен, вопрос стал бы невидимым и чтение /dev/tty зависло — отклонено: читать ответ из stdin (docflow читает /dev/tty, не stdin), отклонено: завидеться на CI (локальный прогон виснет).
- На Windows режим закрепляется (DOCFLOW_SKILLS=global + сохранённый skills-mode), а кейс «без терминала» помечен skip — CONIN$ открывается без консоли и setsid там нет, проверит только windows-latest CI — отклонено: гонять тот же кейс на Windows (риск зависания CI-шага).
- Секция режима возвращает harness в global через docflow update --skills global — секция install-dev.sh ниже проверяет именно перелинковку релизных ссылок в checkout — отклонено: оставить project-режим и ослабить её утверждение.
- Чужой скилл для кейса возврата в project: симлинк на Unix, обычный каталог на Windows — для removeReleaseSkillLinks это одинаковое «не своё» (таргет вне ~/.docflow / не симлинк), Windows-ветка не требует привилегии симлинков, которой нет у раннера — отклонено: ln -s на Windows, отклонено: пропускать кейс на Windows.
- Сценарий 1 идёт настоящим бинарём в подпроцессе с Setsid+Setctty, а не через seam openSkillsTTY — так проверяется путь /dev/tty, как при curl|sh — отклонено: подсунуть PTY-slave в openSkillsTTY (вопрос не прошёл бы через /dev/tty)
- Новый релиз для update собирается вторым go build с -X main.version=1.1.0, а в сборке-источнике стоит 1.0.0 — иначе скопированный бинарь с той же версией обновлялся бы на каждый вызов update — отклонено: отдавать в архиве копию бинаря той же версии
- Дочерние процессы сценариев 2–4 запускаются с Setsid без управляющего терминала: любой нежданный вопрос тихо уйдёт в global-fallback и будет пойман проверкой отсутствия личных ссылок, а не повиснет — отклонено: запускать без Setsid, тогда под интерактивным go test вопрос мог бы ждать ввода

## Phase 1: Install mode

### [x] P046.1.1 Add the skills mode to install and update
Read:
- cmd/docflow/release.go:L80-330
- cmd/docflow/main.go:L50-60
- cmd/docflow/help_text.go
- internal/runner/terminal_unix.go
- internal/runner/terminal_windows.go
- cmd/docflow/release_test.go:L1-80
Do:
- Move `isTerminal` from `internal/runner/terminal_unix.go` and
  `terminal_windows.go` into a new package `internal/term` (same build
  tags) and use it from the runner and from `cmd/docflow`; add
  `term.OpenTTY()` returning the controlling terminal for reading
  (`/dev/tty` on Unix, `CONIN$` on Windows) or an error.
- `install` and `update` get `--skills global|project` (choice option).
  Mode resolution: the flag; else `~/.docflow/skills-mode`; else, when a
  terminal can be opened, ask
  `skills: link into agent directories (global) or only per project (project)? [G/p] `
  and store the answer; else `global` without storing, plus the line
  `skills: global (set docflow update --skills project to keep agent directories clean)`.
  Store the resolved mode whenever it came from the flag or the question.
- `global` → `installReleaseSkills` as now. `project` → for every agent
  directory `installReleaseSkills` would use, call
  `removeReleaseSkillLinks(target, home)` and print
  `skills: project mode — removed N personal links; add skills to a project with docflow init --skills or docflow install-skills --project`.
- `update --skills <mode>` with no newer release: apply the mode to the
  links, print `up to date: <version>` and the skills line, exit 0.
- Help text and the command table (`help:` of `install`, `update`).
- Tests in `cmd/docflow/release_test.go` with a temp HOME: flag `project`
  removes only links into `~/.docflow` (a foreign link and a project copy
  stay), mode stored; `update` with a stored mode does not ask; no
  terminal and no mode → global and the hint, nothing stored;
  `update --skills global` when up to date relinks.
- Docs: `docs/design/skills.md` section «Установка скиллов» — the mode, its
  file, the question and the fallback (Russian).
Verify: `go test ./cmd/docflow/ ./internal/runner/ ./internal/term/`
Traps: `cmd/docflow/main_test.go` pins command help and options; update it
with the new flag.
Depends: -
Role: heavy
Commit: P046.1.1: add project-only skills mode
Handoff:
- Сид для тестов — var openSkillsTTY в cmd/docflow/release.go (default term.OpenTTY); фикстура newReleaseFixture подменяет её ошибкой «нет терминала». Тестам PTY-сценария P046.2.1 подсовывать *os.File с готовой строкой ответа в этот seam, сам вопрос askSkillsMode пишет в os.Stderr
- Вопрос идёт в stderr, ответ читается из /dev/tty: при curl|sh работает, но интерактивный запуск tools/test-install.sh человеком получит невидимый вопрос (вывод перенаправлен в логи) — P046.1.2 должен провести все вызовы install/update через DOCFLOW_SKILLS или иной способ погасить вопрос; в CI терминала нет, там fallback тихий
- update --check полностью пропускает резолюцию режима: не спрашивает, не пишет файл, не печатает hint — не считать это упущением
- removeReleaseSkillLinks теперь возвращает (int, error); uninstall игнорирует счётчик
- Я обновил строки install/update в docs/design/cli.md сверх списка Do:, чтобы таблица команд не протухла — на взгляд ревьюера P046.1.3
- Unverified: Интерактивный вопрос на реальном терминале (текст, default G, запись ответа) — unit-тесты заменяют openSkillsTTY заглушкой, живой путь проверит только PTY-сценарий P046.2.1
- Unverified: Windows: term.OpenTTY через CONIN$ (O_RDWR) и прохождение новых тестов режима на Windows — локально не запускалось, увидит CI/юнит P046.1.2 (test-install.sh)

### [x] P046.1.2 Cover the mode in the installer test and docs
Read:
- install.sh
- tools/test-install.sh:L1-130
- tools/test-install.sh:L230-309
- README.md:L115-150
- README.ru.md:L113-150
- docs/usage.md:L7-151
Do:
- `install.sh`: pass `DOCFLOW_SKILLS` (when set) to `docflow install` as
  `--skills "$DOCFLOW_SKILLS"`; mention it in the header comment.
- `tools/test-install.sh`: cases with the stub agents — install with
  `DOCFLOW_SKILLS=project` leaves no links in `~/.claude/skills`,
  `~/.agents/skills`, `~/.pi/agent/skills`, `~/.zcode/skills`; a following
  `docflow update` keeps them empty; `docflow update --skills global`
  creates them; a foreign link in `~/.claude/skills` survives the switch
  back to `project`; the default install without the variable and without
  a terminal still links globally.
- `README.md` and `README.ru.md` install paragraph: the question,
  `DOCFLOW_SKILLS`, `docflow update --skills`. `docs/usage.md` sections
  «Установить CLI» and «Обновить CLI и проектные копии» (Russian).
  `CHANGELOG.md` `## [Unreleased]` → `### Added`.
Verify: `bash -n install.sh tools/test-install.sh && bash tools/test-install.sh`
Traps: `install.sh` is POSIX `sh` and runs under `dash` in CI
(`DOCFLOW_TEST_SH=dash`): no bash-only syntax. The test harness strips
`PATH` down to stubs; keep new cases inside that sandbox.
Depends: P046.1.1
Commit: P046.1.2: test and document skills install mode
Handoff:
- tools/test-install.sh запускает install.sh и docflow через no_tty (python3 subprocess start_new_session=True): /dev/tty тогда не открывается, docflow берёт скриптовый fallback. PTY-сценарию P046.2.1 нельзя идти через no_tty — звать бинарь напрямую или в собственном pty, иначе вопрос исчезнет.
- На Windows харнесс закрепляет режим: export DOCFLOW_SKILLS=global для install.sh, дальше режим уже записан в ~/.docflow/skills-mode. Любой новый вызов install/update на Windows обязан нести --skills/DOCFLOW_SKILLS или опираться на сохранённый режим — CONIN$ открывается без консоли, чтения без ответа вешают шаг CI.
- Секция режима заканчивается возвратом в global (docflow update --skills global) и удаляет созданный чужой скилл: секция install-dev.sh ниже требует, чтобы релизные ссылки были на месте, иначе вместо 'relinked' будет 'linked' и её проверка падает. Новую секцию нельзя оставлять в project-режиме.
- Гейт go test ./... на хосте не компилирует windows-байт-код, а tools/release.sh в test-install.sh компилирует: P046.1.1 оставил неиспользуемый импорт term в internal/runner/terminal_windows.go, и test-install.sh падал на windows/amd64. Для правок Go в этом плане прогонять GOOS=windows go build ./... (и linux) руками — гейт этого не видит.
- Живой интерактивный вопрос на терминале test-install.sh намеренно не трогает: все вызовы погашены режимом/сеansiрованием; путь вопроса остаётся за PTY-сценарием P046.2.1.
- Unverified: Windows-ветка новых кейсов (export DOCFLOW_SKILLS=global для install.sh, install_sh через env, чужой скилл как каталог, очистка rm -rf, skip кейса без терминала) локально не запускалась — проверит её только шаг tools/test-install.sh на windows-latest в CI.

### [x] P046.1.3 Review the install mode phase
Read:
- docs/design/skills.md:L56-115
Do:
- Review the diff named in the prompt's `## Phase diff` against `## Goal`,
  `## Constraints`, `## Decisions` and each unit's `Do:`. Check that no
  path removes a project copy or a link outside `~/.docflow`. Edit
  nothing.
- A defect — reply `blocked` with `blocker: "plan"`; observations without
  a defect — plain Handoff items.
Verify: `go test ./...`
Role: review
Commit: P046.1.3: review skills install mode
Handoff:
- Ревью фазы (75b58d7..HEAD) без дефектов. Путь удаления один: projectSkillsMode -> removeReleaseSkillLinks, и только по личным каталогам (AgentSkillsDir(agent,"") и ~/.zcode/skills). Он удаляет лишь симлинки, чей путь лежит под ~/.docflow. Ссылка на чекаут (install-dev), чужая ссылка, обычный каталог и проектные копии не трогаются. Проектные каталоги install/update не пишут и не читают. Тесты TestInstallSkillsProjectFlag и tools/test-install.sh это проверяют (чужой скилл и проектная копия переживают переключение).
- Проверено: go build ./... для GOOS=windows, linux, darwin — ок; go vet по cmd, internal/term и internal/runner для трёх ОС — ок; полный docflow verify P046.1.3 (go test ./...) — passed, darwin.
- Наблюдение для P046.2.1: `docflow doctor` не знает режима. В project-режиме он печатает «not installed» по каждому личному скиллу и выходит с кодом 6 (EXIT_STALE) — проверено на временном HOME с skills-mode=project. Это буквально верно, но для project-пользователя шумит. В Goal и в планах P046 doctor не упомянут, поэтому не блокирую; решить стоит в docs/usage.md (где doctor описан для личных скиллов) или отдельной задачей в Backlog.
- Наблюдение: в update режим пишется (--skills или ответ на вопрос) до запроса latest и до activate. Если сеть упала после записи, skills-mode уже сменён, а ссылки остались старыми. Выравнивает следующий `update --skills …` или новый релиз. Опасности нет, ссылки при этом не теряются.
- Наблюдение: openSkillsTTY читает /dev/tty. Процесс в фоновой группе терминала (`docflow update &` из интерактивной оболочки без сохранённого режима) получит SIGTTIN и остановится. Вопрос идёт в stderr: при перенаправленном stderr на живом терминале он невидим, а чтение ждёт ответа. Это следствие принятых решений (stderr + /dev/tty), в обычном `curl | sh` не проявляется. PTY-сценарий P046.2.1 может зафиксировать это одной строкой.
- Наблюдение: перенос isTerminal в internal/term сделал stderrIsTerminal на Windows настоящим (GetConsoleMode); раньше releaseTerminal там всегда возвращал false. Фоновая проверка нового релиза (checkReleaseUpdate) теперь может срабатывать на Windows-консоли. Это поведенческое изменение вне Goal; в CHANGELOG оно не отражено.
- Наблюдение: releaseSkillOptions берёт агентов, найденных в PATH. Если агент ушёл из PATH, его личные ссылки остаются при переключении на project. Полностью их снимает только uninstall, у которого список целей фиксирован. Для project-режима достаточно, но пользователю это можно сказать в docs.

## Phase 2: Acceptance

### [x] P046.2.1 Accept the project-only install
Read:
- tools/test-install.sh:L1-130
Do:
- One scenario per Goal statement through a local release layout and the
  built CLI, in a temp HOME with stub agents (as `tools/test-install.sh`
  sets up):
  1. Install under a pseudo-terminal, answer `p` → no personal links, the
     mode file says `project`.
  2. `docflow update` to a newer local release → no question, still no
     personal links.
  3. `docflow init --skills` in a fresh project → the project copy exists
     and `docflow update --skills project` leaves it untouched.
  4. `docflow update --skills global` → personal links back, pointing into
     `~/.docflow/current/skills`.
- Close every `Unverified:` item of earlier units with a test, or give it a
  disposition as the plan skill's Acceptance section requires.
Verify: `go test ./... && bash tools/test-install.sh`
Traps: A failing scenario is fixed inside this unit when the fix is small;
otherwise reply `blocked` with the reason. Scenario 1 needs a real
terminal: write it as a Go test in `cmd/docflow`; the existing PTY helpers
are test-only files of `internal/runner` (`pty_darwin_test.go`,
`pty_linux_test.go`) and are not importable — copy the minimal opener.
Depends: P046.1.1, P046.1.2, P046.1.3
Role: acceptance
Commit: P046.2.1: accept project-only skills install
Handoff:
- Приёмка P046 — Go-тест TestAcceptProjectOnlySkillsInstall (cmd/docflow/skills_mode_acceptance_test.go, darwin||linux): настоящий бинарь, PTY с Setsid+Setctty, вопрос в stderr, ответ из /dev/tty, stdin — неиспользуемый пайп.
- Unverified P046.1.1 «интерактивный вопрос на реальном терминале» закрыт этим тестом (текст вопроса, ответ p, запись skills-mode=project).
- Windows-пункты Unverified из P046.1.1 и P046.1.2 (CONIN$ через term.OpenTTY, Windows-ветки test-install.sh) не закрыты: PTY-тест только для darwin/linux, на Windows test-install.sh закрепляет режим через DOCFLOW_SKILLS=global и вопрос не проверяет. Остаются на своих юнитах и CI windows-latest.
- macOS: выход лидера сессии с PTY блокируется, пока master не вычитан (процесс виснет в состоянии exiting). Тесты с PTY обязаны читать master до конца, а не только до вопроса.
- Наблюдение из P046.1.3 про docflow doctor не решено: в project-режиме doctor печатает «not installed» по каждому личному скиллу и выходит с кодом 6. Решить в docs/usage.md или в Backlog отдельной задачей.
- Наблюдения P046.1.3 без правок: skills-mode пишется в update до запроса latest; фоновый `docflow update &` без сохранённого режима получит SIGTTIN; изменилось поведение stderrIsTerminal на Windows; агент вне PATH сохраняет личные ссылки при переключении на project.
