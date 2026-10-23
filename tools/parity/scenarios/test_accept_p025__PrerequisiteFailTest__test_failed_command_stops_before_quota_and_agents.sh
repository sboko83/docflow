#!/bin/bash
# test_accept_p025.PrerequisiteFailTest.test_failed_command_stops_before_quota_and_agents
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-autonomous.md
accept_fixture p025
head_route 'executor: claude | opus | high'
edit "$plan" '/## Traps/i\
## Prerequisites\
\
- Test account signed in on the simulator; the credentials live in `$FEED_TEST_ACCOUNT`, not in the repository\
- The input file exists: `test -f missing.txt`\
'
activate_fixture
export DOCFLOW_NOTIFY=1
e2e_cli 2 run
state
agent_logs
