#!/bin/bash
# test_accept_p021.CodexPlanTest.test_dry_run_and_run
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-files.md
accept_fixture p021
head_route 'executor: codex | gpt-5.5 | high'
activate_fixture
printf '[[broken\n' >"$XDG_CONFIG_HOME/docflow/agents.toml"
printf 'not = [toml\n' >"$HOME/broken.toml"
export DOCFLOW_AGENTS="$HOME/broken.toml"
e2e_cli 0 run --dry-run
export DOCFLOW_AGENTS="$HOME/no.toml"
e2e_cli 0 run --dry-run
e2e_cli 0 run --dry-run --all
export DOCFLOW_AGENTS="$HOME/broken.toml"
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
cat <<'RESULT'
{"type": "thread.started", "thread_id": "s1"}
{"type": "item.completed", "item": {"type": "agent_message", "text": "{\"status\": \"done\", \"handoff\": [\"готово\"], \"decisions\": [], \"unverified\": [], \"reason\": \"\"}"}}
{"type": "turn.completed", "usage": {"input_tokens": 10, "cached_input_tokens": 0, "output_tokens": 1}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/1.sh" <<'STEP'
printf 'x\n' >b.txt
cat <<'RESULT'
{"type": "thread.started", "thread_id": "s1"}
{"type": "item.completed", "item": {"type": "agent_message", "text": "{\"status\": \"done\", \"handoff\": [\"готово\"], \"decisions\": [], \"unverified\": [], \"reason\": \"\"}"}}
{"type": "turn.completed", "usage": {"input_tokens": 10, "cached_input_tokens": 0, "output_tokens": 1}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/2.sh" <<'STEP'
printf 'x\n' >c.txt
cat <<'RESULT'
{"type": "thread.started", "thread_id": "t2"}
{"type": "item.completed", "item": {"type": "agent_message", "text": "{\"status\": \"done\", \"handoff\": [\"готово\"], \"decisions\": [], \"unverified\": [], \"reason\": \"\"}"}}
{"type": "turn.completed", "usage": {"input_tokens": 10, "cached_input_tokens": 0, "output_tokens": 1}}
RESULT
STEP
e2e_cli 0 run
e2e_cli 0 status
state
agent_logs
