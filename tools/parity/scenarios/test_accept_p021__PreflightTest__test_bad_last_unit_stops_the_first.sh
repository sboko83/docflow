#!/bin/bash
# test_accept_p021.PreflightTest.test_bad_last_unit_stops_the_first
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-files.md
accept_fixture p021
head_route 'executor: claude | claude-opus-5-5 | high'
activate_fixture
unit_route 2 'Executor: codex | gpt 5.5 | high'
save_fixture 'edit plan'
e2e_cli 2 run
e2e_cli 2 run --dry-run
e2e_cli 2 run --dry-run --all
state
agent_logs
