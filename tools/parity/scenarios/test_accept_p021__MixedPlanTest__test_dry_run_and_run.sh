#!/bin/bash
# test_accept_p021.MixedPlanTest.test_dry_run_and_run
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-files.md
accept_fixture p021
head_route 'executor: claude | claude-opus-5-5 | high'
unit_route 2 'Executor: codex | gpt-5.5 | -'
activate_fixture
e2e_cli 0 run --dry-run --all
e2e_cli 0 run --dry-run
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
printf 'x\n' >b.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/1.sh" <<'STEP'
printf 'x\n' >c.txt
cat <<'RESULT'
{"type": "thread.started", "thread_id": "s1"}
{"type": "item.completed", "item": {"type": "agent_message", "text": "{\"status\": \"done\", \"handoff\": [\"готово\"], \"decisions\": [], \"unverified\": [], \"reason\": \"\"}"}}
{"type": "turn.completed", "usage": {"input_tokens": 10, "cached_input_tokens": 0, "output_tokens": 1}}
RESULT
STEP
e2e_cli 0 run
e2e_cli 0 status
state
agent_logs
