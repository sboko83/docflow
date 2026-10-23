#!/bin/bash
# test_accept_p021.MigrateLegacyTest.test_routes_survive_migrate
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-files.md
accept_fixture p021
unit_route 1 'Model: fast'
activate_fixture
printf '[tiers]\nfast = {agent = "codex", model = "gpt-5.5", effort = "low"}\n' >"$XDG_CONFIG_HOME/docflow/agents.toml"
edit docs/docflow.md 's/docflow: 2/docflow: 1/'
save_fixture 'format 1'
e2e_cli 2 run --dry-run
e2e_cli 0 migrate
save_fixture migrate
e2e_cli 0 run --dry-run --all
e2e_cli 0 run --dry-run --model strong
state
agent_logs
