#!/bin/bash
parity_repo
mkdir -p docs .git/docflow/stats
cat >docs/docflow.md <<'EOF'
---
docflow: 3
---
## Queue

## Backlog
EOF
cat >.git/docflow/stats/P001.1.1.json <<'EOF'
{
  "unit": "P001.1.1",
  "proxy_tokens": 250
}
EOF

# Native Windows names Claude project directories from the Windows path.
project_path="$({ pwd -W 2>/dev/null || pwd -P; } | sed 's/[^A-Za-z0-9]/-/g')"
transcripts="$HOME/.claude/projects/$project_path"
mkdir -p "$transcripts"
cat >"$transcripts/session.jsonl" <<'EOF'
{"type":"assistant","message":{"id":"m0","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":1,"cache_read_input_tokens":10,"cache_creation_input_tokens":100}}}
{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"docflow prompt P001.1.1"}}],"usage":{"input_tokens":2,"cache_read_input_tokens":20,"cache_creation_input_tokens":200}}}
{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":5,"cache_read_input_tokens":50,"cache_creation_input_tokens":500}}}
{"type":"assistant","message":{"id":"m3","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"docflow finish P001.1.1"}}],"usage":{"input_tokens":3,"cache_read_input_tokens":30,"cache_creation_input_tokens":300}}}
{"type":"assistant","message":{"id":"m4","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":9,"cache_read_input_tokens":90,"cache_creation_input_tokens":900}}}
EOF

docflow stats P001
grep -Fqx 'unit proxy reqs sum_ctx base_ctx peak_ctx peak/proxy grow/proxy source' "$parity_output/0001.stdout"
grep -Fqx 'P001.1.1 250 3 1110 222 555 2.22 1.33 transcript' "$parity_output/0001.stdout"
grep -Fqx 'median peak/proxy: 2.22' "$parity_output/0001.stdout"
grep -Fqx 'median grow/proxy: 1.33' "$parity_output/0001.stdout"

code=0
docflow stats P999 || code=$?
[ "$code" -eq 1 ]
grep -Fqx 'no stats: run docflow prompt first' "$parity_output/0002.stderr"
