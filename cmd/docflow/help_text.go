package main

var helpTexts = map[string]string{
	"install": `usage: docflow install [-h]

install an unpacked release into ~/.docflow and link personal skills

Run the docflow binary beside skills/ and LICENSE from a release archive.
Configure PATH for zsh/bash and install personal skills for agent CLIs in
PATH (claude, codex, pi), and for ~/.zcode when it exists.

options:
  -h, --help  show this help message and exit
`,
	"update": `usage: docflow update [-h] [--check]

download and activate the latest release

Only standalone installations can update. For a development checkout use:
git pull && bash tools/install-dev.sh
Personal skill links point into ~/.docflow/current and follow the active
version. Stale project skill copies in the current project are reported with
their install-skills --project command.
The previous release stays in ~/.docflow/versions; roll back with
ln -sfn versions/<previous> ~/.docflow/current; the next update reinstalls
the latest release.
DOCFLOW_RELEASE_URL overrides the GitHub Releases base URL.
Daily terminal checks can be disabled with DOCFLOW_NO_UPDATE_CHECK=1.

options:
  -h, --help  show this help message and exit
  --check     check for a new release without installing it
`,
	"uninstall": `usage: docflow uninstall [-h]

remove the standalone installation and its personal skill links

Remove ~/.docflow, its managed PATH entries, and personal skill symlinks
pointing into it. Development checkouts cannot be uninstalled this way.

options:
  -h, --help  show this help message and exit
`,
	"init": `usage: docflow init [-h] [--agent {claude,codex,pi}] [--skills]

create docs/docflow.md, plans/, archive/ and a line in AGENTS.md; --skills
also copies the skills into the project

options:
  -h, --help            show this help message and exit
  --agent {claude,codex,pi}
                        skill directory set (default: claude)
  --skills              copy skills/* into the selected project directory
`,
	"status": `usage: docflow status [-h]

show the queue, the current plan and its progress

options:
  -h, --help  show this help message and exit
`,
	"next": `usage: docflow next [-h]

print the id of the next available unit

options:
  -h, --help  show this help message and exit
`,
	"prompt": `usage: docflow prompt [-h] [id]

print the prompt for the next unit

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"verify": `usage: docflow verify [-h] [--skip REASON] [--no-gate] id

run the Verify command of the current unit

positional arguments:
  id

options:
  -h, --help     show this help message and exit
  --skip REASON  record the fingerprint without running Verify
  --no-gate      skip the plan's gate; nothing is recorded
`,
	"done": `usage: docflow done [-h] --handoff HANDOFF [--decision DECISION] id

mark the current unit done and write its Handoff

positional arguments:
  id

options:
  -h, --help           show this help message and exit
  --handoff HANDOFF    Handoff item; repeat per item
  --decision DECISION  item for ## Decisions; repeat per item
`,
	"commit": `usage: docflow commit [-h] id

commit the current unit with its Commit subject

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"finish": `usage: docflow finish [-h] --handoff HANDOFF [--decision DECISION]
                      [--skip REASON]
                      id

verify, done and commit the current unit in one call

positional arguments:
  id

options:
  -h, --help           show this help message and exit
  --handoff HANDOFF    Handoff item; repeat per item
  --decision DECISION  item for ## Decisions; repeat per item
  --skip REASON        record the fingerprint without running Verify
`,
	"stats": `usage: docflow stats [-h] [PLAN]

token use per unit: context proxy against the real figures

positional arguments:
  PLAN        only the units of this plan, e.g. P009

options:
  -h, --help  show this help message and exit
`,
	"block": `usage: docflow block [-h] --reason REASON [--needs {human,plan}] id

mark the current unit blocked

positional arguments:
  id

options:
  -h, --help            show this help message and exit
  --reason REASON
  --needs {human,plan}  who unblocks the unit (default: human)
`,
	"unblock": `usage: docflow unblock [-h] [--note NOTE] id

resume a unit blocked for a human

The unit goes back to [~] and the note lands in its Handoff as
Unblocked: ...; the resumed agent checks the blocked condition first.
A block raised with --needs plan is lifted by replanning, not here.
The runner runs this command itself; the person confirms the step.

positional arguments:
  id

options:
  -h, --help   show this help message and exit
  --note NOTE  what the person did to lift the block
`,
	"lint": `usage: docflow lint [-h]

check structure, ids, Depends and the Read budget

options:
  -h, --help  show this help message and exit
`,
	"new": `usage: docflow new [-h] [--title TITLE]
                   [--type {feature,bugfix,perf,module,refactor}]
                   slug

create a plan from the template and add it to the Queue

positional arguments:
  slug

options:
  -h, --help            show this help message and exit
  --title TITLE
  --type {feature,bugfix,perf,module,refactor}
`,
	"activate": `usage: docflow activate [-h] id

make a draft plan active after a clean lint of it

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"close": `usage: docflow close [-h] id

archive a finished plan and delete it

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"freeze": `usage: docflow freeze [-h] [--reason REASON] id

move a plan from the Queue to Frozen

positional arguments:
  id

options:
  -h, --help       show this help message and exit
  --reason REASON  why the plan is frozen
`,
	"thaw": `usage: docflow thaw [-h] id

move a frozen plan back to the end of the Queue

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`,
	"note": `usage: docflow note [-h] [--title TITLE] slug

write an archive entry without a plan

positional arguments:
  slug

options:
  -h, --help     show this help message and exit
  --title TITLE
`,
	"migrate": `usage: docflow migrate [-h]

convert docs from previous format versions

options:
  -h, --help  show this help message and exit
`,
	"install-skills": `usage: docflow install-skills [-h] [--agent {claude,codex,pi}] [--target DIR]
                              [--project]

symlink skills/* into the selected agent directory, or copy them into the
project with --project

options:
  -h, --help            show this help message and exit
  --agent {claude,codex,pi}
                        skill directory set (default: claude)
  --target DIR          personal skills directory override
  --project             copy skills/* into the project
`,
	"roster": `usage: docflow roster [-h] [--profile NAME] [--roster PATH] {show,apply} [PLAN]

show the route chains per plan role from the personal roster, or write
them into a plan header

Reads ~/.config/docflow/roster.toml (or --roster PATH), picks the chain
of every role, and prints a spend summary line plus the plan-header
lines executor:, fallback:, cheap:, heavy:, review:, acceptance: and
replan:. roster apply PLAN writes executor:, fallback:, replan: and the
role keys of the unit Role: fields into the plan header in docs/plans
(draft, active or frozen), removes role keys without units, lints the
plan and restores it on lint errors; a run in progress stops apply. The
runner never reads the roster; a plan stays reproducible without it.

subcommands:
  show            print the chains per role
  apply PLAN      write the chains into the plan header

options:
  -h, --help      show this help message and exit
  --profile NAME  spend and provider-flag overrides from [profile.NAME]
  --roster PATH   roster.toml path (default ~/.config/docflow/roster.toml)
`,
	"doctor": `usage: docflow doctor [-h] [--agent {claude,codex,pi}] [--target DIR]

check that the installed script and skills match this repo

options:
  -h, --help            show this help message and exit
  --agent {claude,codex,pi}
                        skill directory set (default: claude)
  --target DIR          personal skills directory override
`,
	"run": `usage: docflow run [-h] [--config PATH] [--agent NAME] [--model M]
                   [--effort E] [--attempts N] [--replans N] [--max-units N]
                   [--no-wait] [--no-notify] [--dry-run] [--all] [--plan ID]
                   [--print-config] [--yes] [-v] [--stop]

drive the plan unit by unit through a coding agent

options:
  -h, --help      show this help message and exit
  --config PATH   agents.toml (default ~/.config/docflow/agents.toml); plans
                  without executors only
  --agent NAME    agent profile for every unit (default: by tier); plans
                  without executors only
  --model M       model id or tier for every unit; plans without executors
                  only
  --effort E      reasoning effort for every unit; plans without executors
                  only
  --attempts N    agent calls per unit (default 3)
  --replans N     auto-replan calls per phase (default 3, at most 2 per unit;
                  0 disables)
  --max-units N   stop after N units
  --no-wait       stop instead of waiting out a usage limit
  --no-notify     skip the end-of-run desktop notification
  --dry-run       show the next unit and the agent command
  --all           with --dry-run: the agent, model and effort of every unit
                  still to do
  --plan ID       with --dry-run: read this draft, active or frozen plan
                  instead of the Queue
  --print-config  print the built-in agents.toml
  --yes           answer the start confirmation (permissions full,
                  prerequisites) without asking
  -v, --verbose   add per-unit details: tokens, quota, eta
  --stop          ask the active runner to stop after its current unit
`,
}
