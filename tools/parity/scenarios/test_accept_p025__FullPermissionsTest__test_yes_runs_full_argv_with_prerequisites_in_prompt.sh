#!/bin/bash
# test_accept_p025.FullPermissionsTest.test_yes_runs_full_argv_with_prerequisites_in_prompt
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
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
printf x >f1.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/1.sh" <<'STEP'
printf x >f2.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s2", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
e2e_cli 0 run --yes
state
agent_logs
