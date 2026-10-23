#!/bin/bash
# test_accept_p020.CheckoutAcceptanceTest.test_codex_new_user_path
source "$(dirname "$parity_scenario")/../e2e.sh"
parity_repo
checkout_fixture
installer --bin-dir "$bin_dir" --dry-run
installer --bin-dir "$bin_dir"
export PATH="$bin_dir:$PATH"
installed_cli --version
installed_cli install-skills --agent codex
installed_cli doctor --agent codex
project="$HOME/new codex project with spaces"
mkdir -p "$project"
cd "$project"
git -c init.templateDir= init -q -b main
git config user.name 'docflow parity'
git config user.email parity@example.invalid
printf '# Acceptance fixture\n' >README.md
installed_cli init --skills --agent codex
installed_cli doctor --agent codex
installed_cli lint
installed_cli new acceptance --title 'Offline acceptance'
plan=docs/plans/P001-acceptance.md
# Add the same offline decision and user unit as the source acceptance.
/usr/bin/sed '/## Goal/a\
Write an offline result.
/## Decisions/a\
- Use a local text fixture — keep acceptance offline — rejected a paid agent call.
' "$plan" >"$HOME/edited"
/bin/mv "$HOME/edited" "$plan"
/bin/cat "$e2e_dir/fixtures/p020-unit.md" >>"$plan"
installed_cli lint
installed_cli next || [ "$?" -eq 1 ]
git add -A
git commit -q -m 'Save draft fixture'
installed_cli activate P001
git commit -qam 'Activate fixture'
installed_cli prompt
installed_cli verify P001.1.1 || [ "$?" -eq 4 ]
printf 'accepted\n' >result.txt
installed_cli verify P001.1.1
installed_cli done P001.1.1 --handoff 'Created an offline result.'
installed_cli commit P001.1.1
observe 'git log -1 --format=%B; git show HEAD:result.txt; git status --porcelain'
installed_cli status
installed_cli close P001
installed_cli lint || [ "$?" -eq 2 ]
archive=docs/archive/P001-acceptance.md
/usr/bin/sed '/^## Outcome/a\
\
An offline result is available.
/^## Gotchas/a\
\
None.
/^## Follow-ups/a\
\
None.
' "$archive" >"$HOME/edited"
/bin/mv "$HOME/edited" "$archive"
installed_cli lint
printf '\nAcceptance update fixture.\n' >>"$checkout/skills/bsv-docs/SKILL.md"
installed_cli doctor --agent codex || [ "$?" -eq 6 ]
installed_cli install-skills --project --agent codex
installed_cli doctor --agent codex
installed_cli lint
observe 'cat docs/archive/*.md; cat docs/docflow.md'
