#!/usr/bin/env bash
# Smoke test of `docflow run` against the real OpenCode CLI with the
# built-in agent profile on the OpenCode go subscription. Spends a small
# amount of subscription quota: two tiny units, the first on the #low
# variant with one harness-controlled Verify failure that forces the
# runner to resume the session, the second on #high of
# `opencode-go/deepseek-v4.1-flash`, assigned by `Executor:` in the plan.
# An isolated third scenario routes one attempt to the nonexistent
# `opencode-go/no-such-model`: one refused attempt, no generation, no
# waiting, exit 7. Real limit/outage refusals are never provoked.
#
# Runs in throwaway repositories kept inside a unique evidence directory
# that survives both success and failure (absolute path printed); never
# runs in this checkout. The evidence holds the final plans and commits,
# run stdout/stderr and exit codes, the runner's events.jsonl, the raw
# attempt streams recorded by a transparent wrapper around the real CLI,
# and a per-unit time/cost summary; a missing mandatory artifact fails
# the smoke. tools/smoke-opencode-check.py verifies the evidence.
# --dry-run checks the routes without agent calls and needs no opencode.
# --binary PATH runs another docflow executable than bin/docflow.
set -euo pipefail
# The script drives docflow by hand, like a person: drop the runner guard
# so it also works when launched from an agent session inside `docflow run`.
unset DOCFLOW_RUNNER_UNIT

model='opencode-go/deepseek-v4.1-flash'
bad_model='opencode-go/no-such-model'
dry_run=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --dry-run) dry_run=1 ;;
        --binary)
            [ "$#" -ge 2 ] || { echo "usage: $0 [--dry-run] [--binary PATH]" >&2; exit 2; }
            docflow="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
            shift ;;
        *) echo "usage: $0 [--dry-run] [--binary PATH]" >&2; exit 2 ;;
    esac
    shift
done

repo="$(cd "$(dirname "$0")/.." && pwd)"
check="$repo/tools/smoke-opencode-check.py"
if [ -z "${docflow:-}" ]; then
    docflow="$repo/bin/docflow"
    [ -x "$docflow" ] || docflow="$repo/bin/docflow$(cd "$repo" && go env GOEXE)"
fi
[ -x "$docflow" ] || {
    echo "smoke-opencode: no docflow binary at $docflow" >&2
    echo 'smoke-opencode: build it first: go build -o bin/docflow ./cmd/docflow (or pass --binary PATH)' >&2
    exit 1
}
[ -r "$check" ] || { echo "smoke-opencode: missing $check" >&2; exit 1; }
export PATH="$repo/bin:$PATH"

# Everything the live run produces stays here on success and on failure;
# the absolute path is printed so a human can inspect the evidence. A
# dry-run keeps nothing: it has no attempt streams to reconcile.
evidence="$(mktemp -d "${TMPDIR:-/tmp}/docflow-smoke-opencode.XXXXXX")"
evidence="$(cd "$evidence" && pwd -P)"
[ "$dry_run" -eq 1 ] || echo "smoke-opencode: evidence: $evidence"

fail() {
    echo "smoke-opencode: FAIL: $*" >&2
    exit 1
}
trap 'status=$?; if [ "$status" -ne 0 ]; then echo "smoke-opencode: evidence: $evidence" >&2; fi' EXIT

# write_wrapper <dir>: a transparent OpenCode wrapper. It delegates to the
# real CLI and streams stdout through — the runner reads the event stream
# line by line while the call runs — while keeping argv, stdout and the
# exit code of every call as evidence of the raw attempt streams.
write_wrapper() {
    mkdir -p "$1"
    cat > "$1/opencode" <<'WRAPPER'
#!/usr/bin/env bash
# Transparent OpenCode wrapper kept by the smoke as evidence: delegates to
# the real CLI (OPENCODE_REAL) and records argv, stdout and the exit code
# of every call in OPENCODE_CALLS. The prompt arrives on stdin and is
# passed through untouched.
set -o pipefail
n_file="$OPENCODE_CALLS/.count"
n=$(cat "$n_file" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$n_file"
{
    echo "call $n"
    for arg in "$@"; do
        printf '%s\n' "$arg"
    done
} > "$OPENCODE_CALLS/call-$n.argv"
"$OPENCODE_REAL" "$@" | tee "$OPENCODE_CALLS/call-$n.stdout"
rc=$?
echo "$rc" > "$OPENCODE_CALLS/call-$n.exit"
exit "$rc"
WRAPPER
    chmod +x "$1/opencode"
}

clean_path="$PATH"
work="$evidence/repo"
mkdir -p "$work"
cd "$work"
git init -q
git config core.autocrlf false
git config user.name smoke
git config user.email smoke@example.invalid
"$docflow" init --agent opencode >/dev/null
printf 'Use the absolute docflow executable path for verification: `%s`.\n' \
    "$docflow" > README.md

cat > docs/docflow.md <<'EOF'
---
docflow: 3
read_budget: 40000
---
## Queue
- P001

## Backlog
EOF

# The first unit's Verify fails exactly once by harness design: the check
# counts the runner's own attempt logs, so the condition is controlled
# from the harness side and lives outside the files entrusted to the
# agent. The second attempt finds two logs and passes.
cat > docs/plans/P001-smoke.md <<EOF
---
id: P001
title: OpenCode smoke
type: feature
status: active
created: 2026-10-30
gate: true
---
## Goal
Two trivial files written by the runner's agent on the OpenCode go
subscription: unit 1 on the #low variant of $model — its first Verify
fails once by harness arrangement, forcing a resumed second attempt —
and unit 2 on #high.

## Context

## Constraints
Use the absolute docflow executable path from README.md for verification;
an agent's shell profile may replace the inherited PATH.

## Traps

## Decisions

## Phase 1: Files

### [ ] P001.1.1 Write hello
Executor: opencode | $model | low
Read:
- docs/docflow.md
- README.md
Do:
- Create \`hello.txt\` containing the single line \`hello\`.
Verify: \`grep -qx hello hello.txt && [ "\$(ls .git/docflow/run/P001.1.1.*.out 2>/dev/null | wc -l)" -ge 2 ]\`
Traps:
- The check fails until the runner has recorded two attempts; that first
  failing Verify is the harness's controlled failure. Do not create,
  change or remove anything under .git/docflow/run and do not keep
  retrying the check: finish with the result JSON and let the runner
  verify again.
Commit: P001.1.1: write hello

### [ ] P001.1.2 Write bye
Executor: opencode | $model | high
Read:
- docs/docflow.md
- README.md
Do:
- Create \`bye.txt\` containing the single line \`bye\`.
Verify: \`grep -qx bye bye.txt\`
Commit: P001.1.2: write bye
EOF

git add -A
git commit -qm init

"$docflow" run --dry-run --all >"$evidence/dry-run.txt" 2>&1
cat "$evidence/dry-run.txt"
grep -qF "P001.1.1 [ ] Write hello: opencode | $model | low (unit)" \
    "$evidence/dry-run.txt" \
    || fail 'dry-run: no #low route line for P001.1.1'
grep -qF "P001.1.2 [ ] Write bye: opencode | $model | high (unit)" \
    "$evidence/dry-run.txt" \
    || fail 'dry-run: no #high route line for P001.1.2'
if [ "$dry_run" -eq 1 ]; then
    echo 'smoke-opencode: dry-run ok'
    rm -rf "$evidence"
    exit 0
fi

command -v python3 >/dev/null 2>&1 \
    || fail 'python3 is required for the evidence checks (smoke-opencode-check.py)'
command -v opencode >/dev/null 2>&1 \
    || fail 'opencode CLI is not in PATH; the live smoke needs it (opencode --version)'
real_opencode="$(command -v opencode)"

calls="$evidence/opencode-calls"
write_wrapper "$calls"
export OPENCODE_REAL="$real_opencode" OPENCODE_CALLS="$calls"
PATH="$calls:$clean_path"
export PATH

out="$evidence/run.stdout"
err="$evidence/run.stderr"
code=0
"$docflow" run --yes --no-wait --attempts 2 --no-notify >"$out" 2>"$err" || code=$?
echo "$code" > "$evidence/run.exit"
cat "$out"
cat "$err" >&2
[ "$code" -eq 0 ] || fail "docflow run exited with $code, want 0"

# The routes are in the unit lines: a silent fallback switch would show
# another executor there. The machine check below then compares these
# lines with the route of every recorded attempt in events.jsonl.
grep -qF "P001.1.1 Write hello -> opencode | $model | low" "$out" \
    || fail 'no #low route line for P001.1.1 in the run output'
grep -qF "P001.1.2 Write bye -> opencode | $model | high" "$out" \
    || fail 'no #high route line for P001.1.2 in the run output'
if grep -qF 'switching to' "$out"; then
    fail 'the run switched to a fallback route'
fi

plan=docs/plans/P001-smoke.md
grep -q '^### \[x\] P001.1.1' "$plan" || fail 'P001.1.1 is not [x]'
grep -q '^### \[x\] P001.1.2' "$plan" || fail 'P001.1.2 is not [x]'
grep -qx hello hello.txt || fail 'hello.txt is wrong'
grep -qx bye bye.txt || fail 'bye.txt is wrong'

log="$(git log --format=%s)"
for subject in 'P001.1.1: write hello' 'P001.1.2: write bye'; do
    grep -qxF "$subject" <<<"$log" || fail "no commit '$subject'"
done
"$docflow" status | grep -q 'ready to close' || fail 'plan is not ready to close'

set -- .git/docflow/run/*.events.jsonl
if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
    fail 'the run wrote no events.jsonl'
fi
cp "$1" "$evidence/run.events.jsonl"

python3 "$check" main "$evidence" || fail 'evidence checks failed'
[ -f "$evidence/summary.tsv" ] \
    || fail 'the evidence checker wrote no summary.tsv (mandatory artifact)'

# Isolated refusal scenario: a separate repository routes one attempt to
# a model the subscription does not have, without fallback, one attempt,
# no waiting. The provider refuses before generation; the run must exit 7
# with the diagnostic refusal and leave the unit [~] with no commit.
echo "smoke-opencode: $bad_model scenario"
ns="$evidence/no-such-model"
nswork="$ns/repo"
mkdir -p "$nswork"
cd "$nswork"
git init -q
git config core.autocrlf false
git config user.name smoke
git config user.email smoke@example.invalid
"$docflow" init --agent opencode >/dev/null
printf 'Use the absolute docflow executable path for verification: `%s`.\n' \
    "$docflow" > README.md

cat > docs/docflow.md <<'EOF'
---
docflow: 3
read_budget: 40000
---
## Queue
- P001

## Backlog
EOF

cat > docs/plans/P001-nosuch.md <<EOF
---
id: P001
title: OpenCode no-such-model
type: feature
status: active
created: 2026-10-30
gate: true
---
## Goal
One unit routed to the nonexistent $bad_model: the attempt must be
refused diagnostically, without generation, without waiting and without
a fallback.

## Context

## Constraints
Use the absolute docflow executable path from README.md for verification.

## Traps

## Decisions

## Phase 1: Files

### [ ] P001.1.1 Never runs
Executor: opencode | $bad_model | low
Read:
- docs/docflow.md
- README.md
Do:
- Create \`never.txt\` containing the single line \`never\`.
Verify: \`grep -qx never never.txt\`
Commit: P001.1.1: write never
EOF

git add -A
git commit -qm init

ns_calls="$ns/opencode-calls"
write_wrapper "$ns_calls"
export OPENCODE_CALLS="$ns_calls"
PATH="$ns_calls:$clean_path"
export PATH

code=0
"$docflow" run --yes --no-wait --attempts 1 --no-notify \
    >"$ns/run.stdout" 2>"$ns/run.stderr" || code=$?
echo "$code" > "$ns/run.exit"
cat "$ns/run.stdout"
cat "$ns/run.stderr" >&2
[ "$code" -eq 7 ] || fail "$bad_model: exit $code, want 7"
# The refusal line goes to stderr when the progress writes it as a
# warning, so both streams are searched.
grep -qE 'P001\.1\.1: (all executors unavailable until [0-9]{2}:[0-9]{2}|provider unavailable \(--no-wait\)); the unit stays \[~\]' \
    "$ns/run.stdout" "$ns/run.stderr" \
    || fail "$bad_model: no diagnostic refusal line"
grep -qF "P001.1.1 Never runs -> opencode | $bad_model | low" "$ns/run.stdout" \
    || fail "$bad_model: no route line for P001.1.1"
grep -q '^### \[~\] P001.1.1' docs/plans/P001-nosuch.md \
    || fail "$bad_model: the unit did not stay [~]"
if [ -n "$(git log --format=%s | grep '^P001\.1\.' || true)" ]; then
    fail "$bad_model: a unit commit exists"
fi
set -- .git/docflow/run/*.events.jsonl
if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
    fail "$bad_model: the run wrote no events.jsonl"
fi
cp "$1" "$ns/run.events.jsonl"

python3 "$check" no-such-model "$ns" || fail "$bad_model: evidence checks failed"
[ -f "$ns/summary.tsv" ] \
    || fail "$bad_model: the evidence checker wrote no summary.tsv (mandatory artifact)"

echo 'smoke-opencode: ok'
echo "smoke-opencode: evidence: $evidence"
