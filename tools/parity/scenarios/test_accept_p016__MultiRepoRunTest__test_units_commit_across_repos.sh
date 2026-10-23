#!/bin/bash
# test_accept_p016.MultiRepoRunTest.test_units_commit_across_repos
source "$(dirname "$parity_scenario")/../e2e.sh"
loop_fixture
cp "$e2e_dir/fixtures/multi-docflow.md" docs/docflow.md
cp "$e2e_dir/fixtures/multi.md" "$plan"
printf '/projects/\n/modules/\n' >.gitignore
save_fixture repos
nested projects/a
nested modules/b
cat >"$FAKE_AGENT_SCENARIO/0.sh" <<'STEP'
printf 'let n = 1\n' >projects/a/new.swift
printf 'let a = 2\n' >projects/a/main.swift
printf 'b\n' >modules/b/b.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s1", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
cat >"$FAKE_AGENT_SCENARIO/1.sh" <<'STEP'
printf b >b.txt
cat <<'RESULT'
{"type": "result", "is_error": false, "result": "ok", "session_id": "s2", "structured_output": {"status": "done", "handoff": ["готово"], "decisions": [], "unverified": [], "reason": ""}}
RESULT
STEP
e2e_cli 0 run --config "$HOME/agents.toml"
state
observe 'for repo in projects/a modules/b; do git -C "$repo" log --format=%B; git -C "$repo" status --porcelain; done'
agent_logs
