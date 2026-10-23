#!/bin/bash
# test_accept_p021.LegacyTest.test_config_files
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-files.md
accept_fixture p021
unit_route 1 'Model: fast'
activate_fixture
e2e_cli 0 run --dry-run --all
export DOCFLOW_AGENTS="$HOME/missing.toml"
e2e_cli 2 run --dry-run
unset DOCFLOW_AGENTS
e2e_cli 2 run --config "$HOME/missing.toml"
printf '[[broken\n' >"$XDG_CONFIG_HOME/docflow/agents.toml"
e2e_cli 2 run
state
agent_logs
