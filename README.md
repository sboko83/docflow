# bsv-docflow

English | [Русский](README.ru.md)

bsv-docflow runs software work with AI agents as a small spec-driven
process. A task is written up as a plan, the plan splits into one-session
units, each unit is executed, verified, and committed on its own, and the
closed plan collapses into an archive entry that keeps the decisions.
The CLI is a single Go binary, standard library only, installed with one
command; a Go toolchain is needed only for development.

## Why

A long agent session degrades: context fills up, the agent loses track,
and nothing proves the result was checked. bsv-docflow keeps the working
state in the repository instead of the agent's memory.

- **Spec first.** A task becomes a plan of phases and one-session units.
  A unit carries Goal, Read list, Do, Verify, Traps, and a ready commit
  subject; `lint` checks it against a read budget (target ~100k tokens
  per session), so a session opens with its own bounded context instead
  of the whole repository.
- **The cycle is enforced, not promised.** `prompt → work → finish`
  (verify → done → commit); a command refuses when the previous step is
  missing. `verify` fingerprints the working tree and `commit` commits
  exactly that tree — what was verified is what landed; one unit is one
  commit.
- **The whole plan can run unattended.** `docflow run` drives the units
  without a human: prompts the agent, verifies, commits, retries a
  failed unit with the failure tail, switches to the plan's fallback
  executor on a provider limit or outage and waits only when all of them
  are unavailable, and stops safely on a timeout or a soft stop. Claude Code, Codex
  CLI and pi are built in, one run can mix them, and the plan routes each unit
  to its own assistant, model, and reasoning effort
  ([details](docs/usage.md#автозапуск), in Russian).
- **Sessions hand off.** A finished unit leaves Handoff lines and
  Decisions, and the next unit's prompt already carries the handoff of
  its dependencies and the plan's decisions. Work spans sessions without
  re-reading history.
- **Decisions survive, bloat does not.** Closing a plan writes an
  archive entry — outcome, decisions, gotchas, follow-ups — and the
  working plan is deleted. Docs keep decisions and link to the archive
  instead of retelling it.

## How it works

A plan is a Markdown file made of fixed blocks. The agent writes what goes
inside them; the `docflow` CLI owns their state and moves them through the
cycle.

```
docs/docflow.md             Queue · Backlog
docs/plans/P007-cache.md    Goal · Context · Traps · Decisions
  ## Phase 1
  ### [x] P007.1.1          Read · Do · Verify · Commit · Handoff
  ### [~] P007.1.2          ← in progress
  ### [ ] P007.2.1          Depends: P007.1.2
        │  close: every unit is [x]
        ▼
docs/archive/P007-cache.md  Outcome · Decisions · Gotchas · Follow-ups
```

`prompt` builds a unit's prompt from its block, the plan's Decisions, and
the Handoff of its dependencies, and marks it `[~]`. `verify` runs the
unit's `Verify:` command and fingerprints the tree, `done` marks it `[x]`
and writes Handoff, `commit` commits that exact tree under the `Commit:`
subject. A unit that cannot proceed because its plan went stale is marked
`[!]` by `block` and goes to replanning. `lint` checks the structure and the `Read:` budget;
`docflow run` drives the same commands unit after unit.

## See it run

A real `docflow run` of a small demo plan: a Go `slug` command built in
three phases by Codex, pi, and Claude. `bash tools/demo-run.sh`
reproduces it in a throwaway repository and spends agent quota.

![docflow run: start and the first units](docs/img/demo-run.svg)

Each unit goes to the agent, model, and reasoning effort the plan routes
it to. The runner verifies and commits the unit itself; whatever the agent
could not check is reported as `Unverified` instead of being passed off as
done. Quota bars show how much of each subscription window a unit used.

![docflow run: a stale unit and an automatic replan](docs/img/demo-replan.svg)

Unit 2.1 moved `main.go` under `cmd/slug/`, so unit 2.2 still read a file
that no longer exists. `prompt` refuses a unit whose plan no longer
matches the tree; the agent from the plan's `replan:` key repairs the plan
and commits it, and the unit runs again in the same run.

![docflow run: the last unit, the summary, and docflow status](docs/img/demo-finish.svg)

The summary counts units, time, cost, unverified notes, and replans.
`docflow status` reports the plan ready to close: closing turns it into an
archive entry with its decisions.

## Skills

- [bsv-docs](skills/bsv-docs/SKILL.md) — project documentation.
- [bsv-changelog](skills/bsv-changelog/SKILL.md) — changelog and manual release.
- [bsv-docflow-plan](skills/bsv-docflow-plan/SKILL.md) — planning and replan.
- [bsv-docflow-run](skills/bsv-docflow-run/SKILL.md) — running a plan unit.
- [bsv-docflow-close](skills/bsv-docflow-close/SKILL.md) — closing a plan and the archive.

The first two skills work without docflow. The other three cover the
docflow planning cycle.

## Quick start

Requirements: macOS or Linux, amd64 or arm64. No Go toolchain needed:

~~~bash
curl -fsSL https://raw.githubusercontent.com/sboko83/docflow/main/install.sh | sh
~~~

The installer downloads the latest release, verifies its sha256 checksum,
and runs `docflow install`: the binary, skills, and LICENSE land under
`~/.docflow` (`bin/docflow` → `current` → `versions/<version>`), PATH is
configured in the zsh/bash profile, and personal skill links are installed
for agent CLIs found in `PATH`. `DOCFLOW_VERSION=X.Y.Z` installs a specific
release.

~~~bash
docflow doctor --agent codex
cd <project>
docflow init --agent codex
~~~

Claude Code is the default; omit `--agent codex` for it. For pi use
`--agent pi` (personal skills in `~/.pi/agent/skills/`, project copies in
`.pi/skills/`). To copy the skills
into a project, use `docflow init --skills`, or `docflow install-skills
--project` in an already connected project.

`docflow update` downloads and activates the latest release, keeping the
previous one; `docflow update --check` only reports it. A daily terminal
check announces new releases (`DOCFLOW_NO_UPDATE_CHECK=1` turns it off).
`docflow uninstall` removes the installation. Details: [installation,
updates, and usage](docs/usage.md) (in Russian).

### Development

From a checkout (requires Go 1.24+ and git):

~~~bash
git clone https://github.com/sboko83/docflow bsv-docflow
cd bsv-docflow
bash tools/install-dev.sh
~~~

The script builds `bin/docflow`, creates its symlink (it points into the
checkout, so keep the checkout in place), and installs skills for agent
CLIs found in `PATH`. `docflow update` and `docflow uninstall` refuse in
such a build; update by checking out a newer tag and rerunning the script.

## Project links

- [Decisions and format](docs/design/README.md) (in Russian)
- [Command reference](docs/design/cli.md) (in Russian)
- [Skill boundaries and installation](docs/design/skills.md) (in Russian)
- [Task queue](docs/docflow.md) (in Russian)
- [Changelog](CHANGELOG.md)

## Acknowledgements

bsv-docflow was written with the assistance of Claude Code, Codex, ZCode, and pi.

## License

MIT — see [LICENSE](LICENSE).
