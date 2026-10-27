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

### [ ] P046.1.2 Cover the mode in the installer test and docs
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

### [ ] P046.1.3 Review the install mode phase
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

## Phase 2: Acceptance

### [ ] P046.2.1 Accept the project-only install
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
