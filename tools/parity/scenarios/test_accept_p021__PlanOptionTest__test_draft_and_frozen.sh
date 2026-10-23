#!/bin/bash
# test_accept_p021.PlanOptionTest.test_draft_and_frozen
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-files.md
accept_fixture p021
head_route 'executor: codex | gpt-5.5 | high'
activate_fixture
sed 's/P001/P002/g; /created:/a\
executor: claude | claude-sonnet-5-5 | -
' "$e2e_dir/fixtures/p021.md" >docs/plans/P002-next.md
save_fixture 'draft P002'
e2e_cli 0 run --dry-run --plan P002 --all
e2e_cli 0 run --dry-run --plan P002
e2e_cli 0 freeze P001 --reason later
save_fixture 'P001: freeze plan'
e2e_cli 1 run --dry-run
e2e_cli 0 run --dry-run --all --plan P001
e2e_cli 2 run --plan P001
e2e_cli 2 run --all
e2e_cli 2 run --plan P002 --max-units 1
e2e_cli 2 run --dry-run --plan 'P0*'
e2e_cli 2 run --dry-run --plan P009
state
agent_logs
