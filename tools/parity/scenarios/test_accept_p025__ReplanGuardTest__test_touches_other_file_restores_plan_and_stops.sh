#!/bin/bash
# test_accept_p025.ReplanGuardTest.test_touches_other_file_restores_plan_and_stops
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-autonomous.md
accept_fixture p025
head_route 'executor: claude | opus | high'
head_route 'replan: claude | haiku | low'
activate_fixture
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": {"status": "blocked", "reason": "нужна правка плана", "blocker": "plan"}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/1.sh" <<'STEP'
sed 's/### \[!\] P001.1.1/### [~] P001.1.1/' "$plan" >"$HOME/lifted"
mv "$HOME/lifted" "$plan"
printf x >x.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "r1", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
e2e_cli 3 run
state
agent_logs
