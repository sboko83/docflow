Task and documentation workflow for AI agents: plans split into one-session
units, handoff between sessions, decision archive, `docflow` CLI.

## Start of session
Run `docflow status` (prints `start:`), then read `README.md` (state) and
`docs/design/README.md` (index of decisions and formats). Read a topic file
when a unit's `Read:` links it or when planning. Plans are docflow plans,
not the bsv-docs PLAN genre.
Do not re-decide what the design topics already settle; challenge it
explicitly instead.

## Docs
- `docs/design/` — current decisions and format spec (architecture role),
  one file per topic, `README.md` is the index. A changed decision is edited
  in its topic in the same change; the "why" of a new decision goes to the
  plan's `## Decisions`, then to `docs/archive/`, and the topic links to the
  archive entry.
- `README.md` — current state only; next steps live in `docs/docflow.md`
  and `docs/plans/`.
- Language: `README.md`, code, comments, commits and `CHANGELOG.md` are in
  English; `docs/` is in Russian. `README.md` and `README.ru.md` change
  together.
- `CHANGELOG.md` follows the bsv-changelog skill: user-visible changes go
  under `## [Unreleased]`; a release turns it into `## [X.Y.Z] - date`.

## Code
- Go module `bsv-docflow`: CLI entry point `cmd/docflow`, implementation in
  `internal/`; standard library only, no external dependencies.
- Tests live with their Go packages; run `go test ./...` — it is also the
  plans' `gate:`. CI also runs `bash tools/test-install.sh` (installer and
  update against a local release layout) and the e2e scenarios
  `bash tools/parity/run.sh bin/docflow bin/docflow` after
  `go build -o bin/docflow ./cmd/docflow`.
- Format spec, `templates/` and `lint` change together. Bumping `docflow: N`
  requires a migration path.

## Release
- The version is `var version` in `cmd/docflow/main.go`; the tag `vX.Y.Z`
  must match it (`tools/check-release-tag.sh`). Pushing the tag runs
  `.github/workflows/release.yml`, which publishes the GitHub Release and
  smoke-tests the installed release.
- Cut a release with `tools/cut-release.sh`, only on a human request:
  `prepare patch|minor|major|X.Y.Z` runs the gates, bumps the version,
  moves `[Unreleased]`, commits and tags locally (without an argument it
  suggests a bump); the human picks the version. `publish` pushes main to
  `origin` (GitHub; another remote holds the old private history), pushes
  the tag after CI is green and checks the release — run it only on an
  explicit request to publish.
- Before a push to GitHub run `bash tools/audit-public.sh` (`prepare` runs
  it); every new allowance in `tools/audit-public.allow` is reviewed by a
  human.

## Skills
- Sources live only in `skills/`. Never edit installed skill directories.
- The installed CLI is the GitHub release (`install.sh`):
  `~/.docflow/bin/docflow` -> `~/.docflow/current`. `tools/install-dev.sh`
  is only for developing docflow itself; do not expect `~/.local/bin/docflow`.
- `bash tools/install-dev.sh` builds the CLI, links `~/.local/bin/docflow`
  to `bin/docflow`, and installs skills for agent CLIs found in `PATH`
  (`claude`, `codex`, `pi`) and in `~/.zcode/skills` when `~/.zcode` exists;
  `--no-skills` skips the skills. Direct installation uses
  `docflow install-skills --agent claude|codex|pi` or
  `docflow install-skills --target ~/.zcode/skills`. Working-tree edits,
  uncommitted ones included, are live at once; run the installer again for
  a new skill.
