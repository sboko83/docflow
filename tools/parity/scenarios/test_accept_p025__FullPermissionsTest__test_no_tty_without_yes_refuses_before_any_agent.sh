#!/bin/bash
# test_accept_p025.FullPermissionsTest.test_no_tty_without_yes_refuses_before_any_agent
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-autonomous.md
accept_fixture p025
head_route 'executor: claude | opus | high'
head_route 'permissions: full'
edit "$plan" '/## Traps/i\
## Prerequisites\
\
- Test account signed in on the simulator; the credentials live in `$FEED_TEST_ACCOUNT`, not in the repository\
- The input file exists: `test -f a.txt`\
'
activate_fixture
export DOCFLOW_NOTIFY=1
e2e_cli 2 run
state
agent_logs
