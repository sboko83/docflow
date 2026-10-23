#!/bin/bash
# test_accept_p021.PreflightTest.test_route_flags
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-files.md
accept_fixture p021
head_route 'executor: claude | claude-opus-5-5 | high'
activate_fixture
touch "$HOME/agents.toml"
e2e_cli 2 run --config "$HOME/agents.toml"
e2e_cli 2 run --agent codex
e2e_cli 2 run --model gpt-5.5
e2e_cli 2 run --effort low
state
agent_logs
