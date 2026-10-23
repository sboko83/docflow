#!/bin/bash
# test_accept_p025.ReplanAcceptTest.test_blocked_replans_commits_and_notifies_once
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-autonomous.md
accept_fixture p025
head_route 'executor: claude | opus | high'
head_route 'replan: claude | haiku | low'
activate_fixture
export DOCFLOW_NOTIFY=1
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": {"status": "blocked", "reason": "нужна правка плана", "blocker": "plan"}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/1.sh" <<'STEP'
sed 's/### \[!\] P001.1.1/### [~] P001.1.1/' "$plan" >"$HOME/lifted"
mv "$HOME/lifted" "$plan"
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "r1", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/2.sh" <<'STEP'
printf x >f1.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s2", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/3.sh" <<'STEP'
printf x >f2.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s3", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
e2e_cli 0 run
state
agent_logs
