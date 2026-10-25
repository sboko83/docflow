---
id: P040
title: Windows support
type: feature
status: draft
created: 2026-10-25
gate: go test ./...
---
## Goal

On Windows 10/11 with Git for Windows: `go test ./...` and the 68 e2e
scenarios in `tools/parity/` pass, CI runs both on Linux, macOS and
Windows; `docflow run` drives a
real Claude and Codex unit, with timeouts killing the whole process tree;
`install.sh` in Git Bash installs a release, and `docflow update`,
`uninstall` and `install-skills` work.

## Context

Units run on a Windows machine, by hand: an agent session in the checkout
does the next unit by `bsv-docflow-run` (`docflow prompt` → work →
`docflow verify` → `done` → `commit`); `docflow` is the checkout binary
`bin/docflow.exe`, rebuilt after every Go change. No docflow skills are
installed on this machine until P040.3.1 fixes `install-skills`: read
`skills/bsv-docflow-run/SKILL.md` and `skills/bsv-changelog/SKILL.md`
from the checkout, and do not copy them into personal skill directories
(a copy is a conflict for `install-skills` later). Every unit with a
user-visible change adds its entry to `CHANGELOG.md` under
`## [Unreleased]` (bsv-changelog rules). `docs/` is in Russian; code,
comments and `CHANGELOG.md` are in English.

## Constraints

- Go standard library only; no new dependencies. Windows API calls only
  through `os/exec` of system tools (`taskkill`, `powershell`,
  `cmd /c mklink /J`), no `syscall` DLL loading.
- Installing and running docflow on Windows needs no Developer Mode and
  no administrator rights.
- macOS and Linux behavior stays unchanged; `go test ./...` keeps passing
  there.
- Plan format stays `docflow: 3`.
- Tests never touch the real `%USERPROFILE%\.docflow`, `.claude`, `.codex`
  or `.pi`: a temporary `HOME`/`USERPROFILE`, as the existing fixtures do.
- Shell scripts target macOS `/bin/bash` 3.2 and Git Bash; `bash -n`
  before finishing.

## Prerequisites

- Go 1.24+ installed: `go version`
- Git for Windows; its `bash` comes first in `PATH`, not
  `C:\Windows\System32\bash.exe` (WSL): `bash -c "uname -s | grep -q MINGW"`
- The checkout is cloned with `core.autocrlf=false`:
  `test "$(git config --get core.autocrlf)" = false`
- `claude` and `codex` are installed and logged in on the subscription;
  units P040.2.1 and P040.4.1 spend a few tiny Haiku and Codex calls

## Traps

- Before unit P040.1.1 lands, `docflow verify` runs `Verify:` through
  `cmd.exe`: that unit's check is written to work there.
- `go build -o bin/docflow ./cmd/docflow` on Windows writes a file without
  `.exe`; build `bin/docflow.exe` (`bin/docflow$(go env GOEXE)` in
  scripts).
- `go test ./...` takes ~3 min on macOS and may take longer on Windows;
  run a package's tests while working and the full gate once at the end.
- A unit does not commit by hand and does not rewrite history: `docflow
  commit` commits with the unit's `Commit:` line.

## Decisions

- Plan commands (`Verify:`, `gate:`, prerequisites) run on Windows through
  Git for Windows `bash -c` — plans are written in POSIX sh by agents on
  any OS — `cmd.exe` as the only shell was rejected; it stays the fallback
  when Git Bash is not found.
- On Windows `current` and personal skill links are directory junctions
  (`cmd /c mklink /J`, absolute target), and the user `Path` holds
  `%USERPROFILE%\.docflow\current` instead of `bin/` — junctions need no
  privilege, and `Path` through `current` needs no executable link — file
  and directory symlinks were rejected: unprivileged ones need Developer
  Mode, a system setting no CLI install should require; a copied
  `bin/docflow.exe` was rejected: replacing a running executable needs a
  rename-aside step that `current` makes unnecessary.
- Reviving the e2e scenarios moved here from P039 — one plan runs on one
  machine, P040 runs before P039, and the Windows e2e unit would block on
  failures common to every OS — leaving it in P039 was rejected. The
  revival is an OS-neutral unit; Windows failures stay in their own unit.
- The harness keeps its name and layout and runs one binary against
  itself — the Python version it compared against is gone, the scenarios
  remain the only end-to-end runs of the CLI — deleting it was rejected.
- `install.sh` runs in Git Bash and stays the only installer — Git for
  Windows is required anyway — a separate `install.ps1` was rejected.
- Release archives stay `.tar.gz` on Windows — one extractor in Go,
  `tar.exe` ships with Windows 10+ — `.zip` was rejected.
- The process tree is killed with `taskkill /T /F /PID` — stdlib only —
  Job Objects were rejected: they need `syscall` DLL calls.

## Phase 1: Tests on Windows

### [ ] P040.1.1 Run plan commands through Git Bash and pass go test
Read:
- internal/cycle/verify_shell_unix.go
- internal/cycle/verify_shell_windows.go
- internal/cycle/verify.go:L170-210
- internal/runner/helpers.go:L250-290
- docs/design/cycle.md:L1-60
Do:
- Add `.gitattributes` with `* text=auto eol=lf`, so a checkout keeps LF
  in scripts and fixtures regardless of `core.autocrlf`.
- Make one shell selector for plan commands in `internal/cycle`, used by
  `Verify:`/`gate:` and by the runner's prerequisites in
  `internal/runner/helpers.go` (today `/bin/sh -c`). Unix: `/bin/sh -c`
  as now. Windows: `bash.exe -c` of Git for Windows — found from
  `exec.LookPath("git")` as `<git root>\bin\bash.exe` (git.exe lives in
  `<git root>\cmd` or `<git root>\bin`); never `System32\bash.exe`; not
  found → `%COMSPEC% /c` as today.
- Add a Windows-only test for the selector (Git Bash found → `bash.exe`
  path under the git root) and keep the Unix tests as they are.
- Run `go test ./...` on Windows and fix each failure:
  - a test whose fixture is a Unix shell script (fake agents in `sh`)
    and has a Windows-portable form → port it; no portable form → skip on
    Windows with the reason, as existing `runtime.GOOS` skips do;
  - a product failure (paths, line endings, `.exe`, `HOME` vs
    `USERPROFILE`) → fix the code;
  - a product failure that is the subject of a later unit (process tree
    kill — P040.2.1; install, update, uninstall, skill links — P040.3.1,
    P040.3.2) → skip on Windows with `t.Skip("windows: P040.x.y")`; that
    unit removes the skip.
- More than ~8 files of fixes beyond the selector → fix what fits, then
  block with `docflow block --needs plan` listing the remaining failures
  by package.
- Update `docs/design/cycle.md` where it says how `Verify:` runs: the
  shell per OS.
Verify: `go test ./...`
Commit: P040.1.1: run plan commands through Git Bash on Windows

### [ ] P040.1.2 Revive the e2e scenarios
Read:
- tools/parity/README.md
- tools/parity/run.sh
- tools/parity/driver.sh
- tools/parity/e2e.sh
- tools/parity/fixtures/repos-docflow.md
- tools/parity/scenarios/commit_after_other_binary_verify.sh
- docs/design/format.md:L1-120
- docs/design/format.md:L257-300
- .github/workflows/ci.yml
- AGENTS.md
Do:
- Make `tools/parity/run.sh` run every scenario instead of stopping at the
  first failure: print `parity: FAIL <name> (<side> scenario)` per failure,
  a final `parity: N of M failed` line, and exit 1 when any failed; exit
  codes 0 and 2 keep their meaning.
- Build `bin/docflow.exe` and run
  `bash tools/parity/run.sh bin/docflow.exe bin/docflow.exe`; 29 of 68
  scenarios failed on macOS before this unit, mostly plans and fixtures
  written in `docflow: 2`.
- Fix each failing scenario and fixture to the current format and the
  behavior described in `docs/design/`: `docflow: 3` headers, unit fields
  and command output as the current binary documents them.
- A scenario whose subject is the legacy format or migration (names with
  `migrate` or `legacy`) keeps its `docflow: 2` input and checks the
  current migration behavior instead.
- A failure where the binary contradicts `docs/design/` is a bug, not a
  scenario fix: block with `blocker: "plan"` naming the scenario and the
  design line.
- A failure caused by Windows (binary name, `.exe`, path separators, line
  endings, temp directories, Unix-only tools) is left to P040.1.3: list
  each such scenario with its error in the Handoff.
- Rewrite the purpose paragraph of `tools/parity/README.md`: the harness
  runs end-to-end scenarios of one binary (`run.sh bin/docflow
  bin/docflow`) or compares two builds; drop mentions of the Python
  version.
- In `.github/workflows/ci.yml` add to the `test` job, after `go test`,
  the steps `go build -o bin/docflow ./cmd/docflow` and
  `bash tools/parity/run.sh bin/docflow bin/docflow`, for both matrix OS.
- In `AGENTS.md` extend the sentence "CI also runs `bash
  tools/test-install.sh`" with the e2e run command.
Verify: `go build -o bin/docflow.exe ./cmd/docflow && { bash tools/parity/run.sh bin/docflow.exe bin/docflow.exe; test $? -le 1; } && grep -q 'tools/parity/run.sh' .github/workflows/ci.yml`
Traps: scenarios run with `set -euo pipefail` and system/user git config
disabled; a scenario that expects a non-zero exit code writes
`cmd || [ "$?" -eq N ]`, as `README.md` shows — keep that form. Run one
scenario alone with `PARITY_SKIP` listing all the others. `Verify:` only
checks that the harness runs; the full pass is P040.1.3's check, and CI on
Linux and macOS runs only after a push — record it as `Unverified:`.
Commit: P040.1.2: revive e2e scenarios in CI

### [ ] P040.1.3 Pass the e2e scenarios and add Windows to CI
Read:
- tools/parity/README.md
- tools/parity/run.sh
- tools/parity/driver.sh
- tools/parity/e2e.sh
- .github/workflows/ci.yml
Do:
- Build `bin/docflow.exe` and run
  `bash tools/parity/run.sh bin/docflow.exe bin/docflow.exe` in Git Bash;
  start from the Windows failures listed in P040.1.2's Handoff.
- Fix harness portability (binary name, `.exe`, path separators in
  compared output, temp directories); a product failure → fix the code
  when it stays within ~4 files, otherwise block with
  `docflow block --needs plan` naming the scenarios.
- In `.github/workflows/ci.yml` add `windows-latest` to the `test` matrix;
  run steps with `shell: bash`; build the e2e binary as
  `bin/docflow$(go env GOEXE)` and pass that path to `run.sh`; keep
  `bash tools/test-install.sh` off Windows with
  `if: runner.os != 'Windows'` until P040.3.2.
- Update the run command in `tools/parity/README.md` with the `.exe` form.
Verify: `go build -o bin/docflow.exe ./cmd/docflow && bash tools/parity/run.sh bin/docflow.exe bin/docflow.exe && grep -q windows-latest .github/workflows/ci.yml`
Commit: P040.1.3: pass e2e scenarios on Windows, add Windows CI

## Phase 2: Live run on Windows

### [ ] P040.2.1 Kill process trees and run real agents
Read:
- internal/procgroup/procgroup.go
- internal/procgroup/procgroup_windows.go
- internal/procgroup/procgroup_unix.go
- internal/agentio/process.go
- tools/smoke-run.sh
- docs/design/runner.md:L440-470
Do:
- In `procgroup_windows.go` make `Kill` run `taskkill /T /F /PID <pid>`,
  fall back to `cmd.Process.Kill()` when taskkill fails, and wait for
  `done` within the grace period as the Unix version does.
- Add a Windows test: a helper process (the test binary re-executed with
  an environment flag) starts a grandchild; after `Kill` the grandchild
  is gone (`tasklist /FI "PID eq <pid>"` lists no process). Remove the
  matching `windows: P040.2.1` skips.
- Run `bash tools/smoke-run.sh` (Claude Haiku) and then
  `bash tools/smoke-run.sh --mixed` (adds Codex) in Git Bash; fix what
  breaks on Windows: agent executable lookup (`.exe`, `.cmd` shims),
  stdin/stdout encoding, paths in prompts, the run lock and notification
  (Windows: no notification, no error).
- Document the Windows kill and the agent lookup in
  `docs/design/runner.md` next to the process-group paragraph.
Verify: `go test ./internal/procgroup ./internal/agentio ./internal/runner && bash tools/smoke-run.sh`
Traps: each `smoke-run.sh` spends a few Haiku calls; `--mixed` also
spends Codex — run it once in `Do:`, not in a loop.
Commit: P040.2.1: kill process trees and run agents on Windows

## Phase 3: Install from release

### [ ] P040.3.1 Ship Windows archives and activate them
Read:
- tools/release.sh
- internal/release/release.go:L1-60
- internal/release/release.go:L376-500
- cmd/docflow/release.go:L1-120
- internal/skills/skills.go:L445-510
- cmd/docflow/release.go:L290-320
- docs/design/cli.md:L55-95
Do:
- `tools/release.sh`: add `windows/amd64` and `windows/arm64`; the archive
  `docflow_windows_<arch>.tar.gz` holds `docflow.exe`, `LICENSE`,
  `skills/`.
- `internal/release`: the binary name is `docflow` + `.exe` on Windows
  everywhere (`Activate`, the extract check, `InstalledExecutable`).
- One link helper for directory links: Unix `os.Symlink` as now; Windows
  `cmd /c mklink /J <link> <absolute target>`. `Activate` and
  `install-skills` use it.
- `Activate` on Windows: replace `current` by removing the old junction
  and creating the new one (a rename over a directory link fails there);
  no `bin/` link — `Path` points at `current` (P040.3.2).
- Every place that recognizes docflow's own links (`Activate`'s check of
  `current`, `install-skills` re-linking, `uninstall` removing skill
  links, `update`'s check that the running binary is under `versions/`)
  accepts a junction as well as a symlink.
- Removing old versions skips a directory still in use (the running
  `docflow.exe`) with one warning line instead of failing.
- `expandUser` in `internal/skills` splits `~/...` on `filepath.Separator`
  only, so on Windows `~/.docflow` reads as user `/.docflow` and
  `install-skills` fails with `user "/.docflow" does not exist`: accept
  `/` as well, with a test.
- Remove the `windows: P040.3.1` skips.
- Tests on Windows, without Developer Mode: activate twice (switch
  `current`), update cleanup with a locked file, skills linking and
  uninstall in a temporary `USERPROFILE`.
- Update `docs/design/cli.md`: the platforms list and the Windows layout
  (junctions, no `bin/`, no Developer Mode).
Verify: `go test ./internal/release ./internal/skills ./cmd/docflow && bash tools/release.sh 0.0.1 bin/release-test && test -f bin/release-test/docflow_windows_amd64.tar.gz`
Traps: `tools/release.sh` arguments and output layout are described in
`docs/design/cli.md`; check them before calling it, `bin/` is gitignored.
Since Go 1.23 `os.Lstat` reports a junction as `ModeIrregular`, not
`ModeSymlink`; check `os.Readlink` and `filepath.EvalSymlinks` on a
junction in a test before relying on them. `os.Remove` on a junction
removes the link, not its target — keep a test for that.
Commit: P040.3.1: ship and activate Windows releases

### [ ] P040.3.2 Install, update and uninstall from Git Bash
Read:
- install.sh
- tools/test-install.sh
- internal/release/release.go:L495-600
- cmd/docflow/release.go:L120-300
- .github/workflows/ci.yml
- .github/workflows/release.yml
- README.md
- README.ru.md
Do:
- `install.sh`: `MINGW*`, `MSYS*`, `CYGWIN*` from `uname -s` → `windows`;
  check and run `docflow.exe` from the unpacked archive.
- `EnsurePath`/`RemovePath` on Windows: add or remove
  `%USERPROFILE%\.docflow\current` in the user `Path` through
  `powershell -NoProfile -Command` with
  `[Environment]::GetEnvironmentVariable('Path','User')` and
  `SetEnvironmentVariable(..., 'User')`; never `setx` (it truncates at
  1024 characters). Git Bash inherits that `Path`.
- Make `tools/test-install.sh` pass in Git Bash on Windows (binary name,
  `HOME`/`USERPROFILE`, the PATH check through the user `Path` with a
  test override so the real registry value is not touched).
- CI: drop the `if: runner.os != 'Windows'` from the `test-install.sh`
  step; in `release.yml` add `windows-latest` to the smoke matrix with
  `shell: bash`.
- README.md and README.ru.md: the install command for Windows (Git Bash);
  `CHANGELOG.md` `### Added` entry.
Verify: `bash -n install.sh && bash -n tools/test-install.sh && bash tools/test-install.sh && go test ./internal/release ./cmd/docflow`
Traps: the test override for the user `Path` must be an environment
variable read only by `EnsurePath`/`RemovePath`, so a test run never
writes the real registry; check it in the test before any write.
Commit: P040.3.2: install docflow from Git Bash on Windows

## Phase 4: Acceptance

### [ ] P040.4.1 Accept Windows support end to end
Read:
- tools/test-install.sh
- tools/smoke-run.sh
- tools/parity/README.md
- .github/workflows/ci.yml
- .github/workflows/release.yml
Do:
- On Windows, one scenario per Goal statement:
  - `go test ./...` and
    `bash tools/parity/run.sh bin/docflow.exe bin/docflow.exe` pass;
  - `bash tools/smoke-run.sh --mixed` passes, and a unit whose agent
    exceeds `timeout:` leaves no child process (`tasklist`);
  - `bash tools/test-install.sh` passes; then a real install from a local
    release layout into a temporary `USERPROFILE`: `docflow --version`,
    `docflow update` to a newer local version, `docflow install-skills
    --agent claude`, `docflow uninstall`.
- Check that `ci.yml` and `release.yml` run on `windows-latest` with no
  step excluded except where the plan says so.
- Close every `Unverified:` item from earlier Handoffs with a check, or
  keep it as `Unverified:` in this Handoff.
Verify: `go test ./... && bash tools/test-install.sh && go build -o bin/docflow.exe ./cmd/docflow && bash tools/parity/run.sh bin/docflow.exe bin/docflow.exe`
Traps: a failing scenario is fixed inside this unit when the fix is
small; otherwise block with the reason.
Depends: P040.1.1, P040.1.2, P040.1.3, P040.2.1, P040.3.1, P040.3.2
Commit: P040.4.1: accept Windows support
