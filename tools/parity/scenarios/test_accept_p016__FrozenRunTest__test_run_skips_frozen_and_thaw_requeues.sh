#!/bin/bash
# test_accept_p016.FrozenRunTest.test_run_skips_frozen_and_thaw_requeues
source "$(dirname "$parity_scenario")/../e2e.sh"
loop_fixture
printf a >a.txt
save_fixture init
e2e_cli 0 freeze P001 --reason later
git commit -qam 'P001: freeze plan'
e2e_cli 0 status
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
printf c >c.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
e2e_cli 0 run --config "$HOME/agents.toml"
e2e_cli 0 thaw P001
e2e_cli 0 next
state
agent_logs
