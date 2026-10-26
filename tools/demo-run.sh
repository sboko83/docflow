#!/usr/bin/env bash
# Demo of `docflow run` for the README screenshots. Builds a throwaway Go
# project with an English plan of three phases, runs it with real agents
# (Codex and Pi/GLM Flash) in a pseudo-terminal, and renders scenes of the
# recording into SVG terminal windows with tools/term2svg.py.
# Spends Codex and Z.ai subscription quota.
#
# The plan is built to show the whole loop: unit 2.1 moves main.go, unit 2.2
# still reads it, so its prompt is refused and the `replan:` agent fixes the
# plan inside the same run.
#
# usage: tools/demo-run.sh [--out DIR] [--dry-run]
#                                            run the demo, or only check routes
#        tools/demo-run.sh --render RAW [--out DIR]
#                                            re-render a kept recording
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
docflow="$repo/bin/docflow"
out="$repo/docs/img"
raw=''
cols=100
dry_run=0

while [ $# -gt 0 ]; do
    case "$1" in
        --out) out="$2"; shift 2 ;;
        --render) raw="$2"; shift 2 ;;
        --dry-run) dry_run=1; shift ;;
        *) echo "usage: $0 [--out DIR] [--render RAW] [--dry-run]" >&2; exit 2 ;;
    esac
done

render() {
    local rec="$1"
    mkdir -p "$out"
    python3 "$repo/tools/term2svg.py" "$rec" "$out/demo-run.svg" --cols "$cols" \
        --title 'docflow run' --prompt 'docflow run' --from '^╭─ run: P001' --to '^┌─ P001\.2\.2:'
    python3 "$repo/tools/term2svg.py" "$rec" "$out/demo-replan.svg" --cols "$cols" \
        --title 'docflow run' --from '^┌─ P001\.2\.2:' --to '^┌─ P001\.3\.1:'
    python3 "$repo/tools/term2svg.py" "$rec" "$out/demo-finish.svg" --cols "$cols" \
        --title 'docflow run' --from '^┌─ P001\.3\.1:'
}

if [ -n "$raw" ]; then
    render "$raw"
    exit 0
fi

export PATH="$repo/bin:$PATH"
[ -x "$docflow" ] || { echo "demo-run: build $docflow first (tools/install-dev.sh)" >&2; exit 1; }

work="$(mktemp -d)/slug"
mkdir -p "$work"
cd "$work"
git init -q
git config user.name demo
git config user.email demo@example.invalid

cat > go.mod <<'EOF'
module example.com/slug

go 1.22
EOF

cat > main.go <<'EOF'
package main

import "fmt"

func main() {
	fmt.Println("slug: not implemented yet")
}
EOF

cat > README.md <<'EOF'
# slug

Turns titles into URL slugs.
EOF

"$docflow" init --agent pi >/dev/null

cat > docs/docflow.md <<'EOF'
---
docflow: 3
read_budget: 40000
start_read: README.md
start_budget: 4000
---
## Queue
- P001

## Backlog
EOF

cat > docs/plans/P001-slug-cli.md <<EOF
---
id: P001
title: Slug CLI
type: feature
status: active
created: $(date +%F)
executor: codex | gpt-6.1-sol | medium
replan: codex | gpt-6.1-sol | high
gate: go vet ./... && go test ./...
---
## Goal
A \`slug\` command that turns titles into URL slugs, with a reusable package.

## Context

## Constraints
- Go standard library only, no third-party modules.
- Write every report (handoff, decisions, unverified notes) in English.

## Traps

## Decisions

## Phase 1: Library

### [ ] P001.1.1 Slug package
Read:
- main.go
Do:
- Create package \`slug\` in \`slug/slug.go\` with \`func Make(s string) string\`:
  lowercase; ASCII letters and digits are kept; every other run of
  characters becomes one \`-\`; no leading or trailing \`-\`.
- Table tests in \`slug/slug_test.go\`.
Verify: \`go test ./slug\`
Commit: P001.1.1: add slug package

### [ ] P001.1.2 Latin diacritics
Read:
- slug/slug.go
Do:
- Map Latin letters with diacritics to ASCII before slugifying through a
  lookup table: \`é\` → \`e\`, \`ß\` → \`ss\`, \`ø\` → \`o\`, \`ł\` → \`l\`.
- Tests: \`Crème Brûlée\` → \`creme-brulee\`, \`Straße\` → \`strasse\`.
Verify: \`go test ./slug\`
Executor: pi | zai/glm-5.3-flash | high
Commit: P001.1.2: transliterate latin diacritics

## Phase 2: Command

### [ ] P001.2.1 Command under cmd/
Read:
- main.go
Do:
- Move the entry point to \`cmd/slug/main.go\` and delete the root \`main.go\`.
- The command reads lines from stdin and prints one slug per line. Input
  may come from Windows with CRLF line endings.
Verify: \`printf 'Hello, World!\r\n' | go run ./cmd/slug | grep -qx hello-world && test ! -e main.go\`
Executor: pi | zai/glm-5.3-flash | high
Commit: P001.2.1: move command under cmd

### [ ] P001.2.2 Flags
Read:
- main.go
Do:
- Add \`-sep\` (default \`-\`) and \`-max N\` (cut at a word boundary,
  0 means no limit) flags.
Verify: \`echo 'A long title for a blog post' | go run ./cmd/slug -max 12 | grep -qx a-long-title\`
Commit: P001.2.2: add sep and max flags

## Phase 3: Docs

### [ ] P001.3.1 Usage in README
Read:
- README.md
Do:
- Document installation with \`go install example.com/slug/cmd/slug@latest\`
  and both flags, with examples whose output matches the real command.
Verify: \`grep -q -- '-max' README.md\`
Executor: pi | zai/glm-5.3-flash | low
Commit: P001.3.1: document usage
EOF

git add -A
git commit -qm 'Initial slug skeleton'
"$docflow" lint
"$docflow" run --dry-run --all
if [ "$dry_run" -eq 1 ]; then
    cd /
    rm -rf "${work%/slug}"
    echo 'demo-run: dry-run ok'
    exit 0
fi

rec="$work.raw"
script -q "$rec" /bin/bash -c "stty cols $cols rows 50
'$docflow' run
printf '\n\033[32m\$\033[0m docflow status\n'
'$docflow' status" </dev/null

"$docflow" status | grep -q 'ready to close' || {
    echo "demo-run: plan is not ready to close; recording kept: $rec, repo: $work" >&2
    exit 1
}
render "$rec"
echo "demo-run: ok; recording kept: $rec, repo: $work"
