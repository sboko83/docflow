#!/bin/bash
# test_accept_p025.HumanBlockTest.test_stops_without_replan_and_notifies_reason
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-autonomous.md
accept_fixture p025
head_route 'executor: claude | opus | high'
head_route 'replan: claude | haiku | low'
activate_fixture
export DOCFLOW_NOTIFY=1
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": {"status": "blocked", "reason": "нужен вход в аккаунт на симуляторе", "blocker": "human"}}
RESULT
STEP
e2e_cli 3 run
state
agent_logs
