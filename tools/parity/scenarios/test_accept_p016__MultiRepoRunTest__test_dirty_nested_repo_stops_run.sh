#!/bin/bash
# test_accept_p016.MultiRepoRunTest.test_dirty_nested_repo_stops_run
source "$(dirname "$parity_scenario")/../e2e.sh"
loop_fixture
cp "$e2e_dir/fixtures/multi-docflow.md" docs/docflow.md
cp "$e2e_dir/fixtures/multi.md" "$plan"
printf '/projects/\n/modules/\n' >.gitignore
save_fixture repos
nested projects/a
nested modules/b
printf 'let a = 3\n' >modules/b/main.swift
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
printf b >b.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
e2e_cli 5 run --config "$HOME/agents.toml"
state
agent_logs
