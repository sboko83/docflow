#!/bin/bash
# test_accept_p025.StaleReadTest.test_prompt_refusal_replans_the_link_and_continues
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-autonomous.md
accept_fixture p025-stale
head_route 'executor: claude | opus | high'
head_route 'replan: claude | haiku | low'
activate_fixture
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
printf x >f1.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/1.sh" <<'STEP'
printf x >f2.txt
rm c.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s2", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/2.sh" <<'STEP'
sed 's/- c.txt/- docs\/docflow.md/' "$plan" >"$HOME/lifted"
mv "$HOME/lifted" "$plan"
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "r1", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/3.sh" <<'STEP'
printf x >f3.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s3", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
e2e_cli 0 run
state
agent_logs
