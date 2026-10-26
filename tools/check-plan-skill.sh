#!/bin/bash
# Live check of the planning skill: builds the checkout binary, creates a
# fixture repository with one Backlog line, lets a cold claude session
# plan it by skills/bsv-docflow-plan/SKILL.md, then checks the produced
# plan against the skill's rules (roster routes in the header, no replan,
# review and acceptance roles, dry-run).
# Not part of the gate or CI: the agent call spends a subscription
# session and takes 5-15 minutes.
# Usage:
#   check-plan-skill.sh              full run: fresh work directory,
#                                    fixture, agent call, then the checks
#   check-plan-skill.sh --check-only reuse the work directory of a full
#                                    run and run only the checks
set -euo pipefail

CHECK_ONLY=0
case "${1:-}" in
    "") ;;
    --check-only) CHECK_ONLY=1 ;;
    *)
        printf 'usage: %s [--check-only]\n' "$0" >&2
        exit 2
        ;;
esac

fail() {
    printf 'check-plan-skill: %s\n' "$1" >&2
    exit "${2:-1}"
}

progress() {
    printf 'check-plan-skill: %s\n' "$1" >&2
}

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P) || fail 'cannot locate tools'
REPO_DIR=$(cd "$SCRIPT_DIR/.." && pwd -P) || fail 'cannot locate checkout'
SKILL="$REPO_DIR/skills/bsv-docflow-plan/SKILL.md"
[ -f "$SKILL" ] || fail "missing skill: $SKILL"

WORK=${TMPDIR:-/tmp}/docflow-plan-skill-check
REPO="$WORK/repo"
AGENT_LOG="$WORK/agent.log"

FAILED=0
ok() {
    printf 'ok %s\n' "$1"
}
fail_check() {
    printf 'FAIL %s\n' "$1"
    if [ -n "${2:-}" ]; then
        printf '%s\n' "$2" | sed 's/^/    /'
    fi
    FAILED=1
}
# run_check <name> <command...>: ok on exit 0, FAIL with the captured
# output otherwise
run_check() {
    local name=$1 out
    shift
    if out=$("$@" 2>&1); then
        ok "$name"
    else
        fail_check "$name" "$out"
    fi
}

if [ "$CHECK_ONLY" -eq 0 ]; then
    mkdir -p "$WORK/bin"
    progress "building the checkout binary into $WORK/bin/docflow"
    (cd "$REPO_DIR" && go build -o "$WORK/bin/docflow" ./cmd/docflow) \
        || fail 'go build failed'
    PATH="$WORK/bin:$PATH"
    export PATH

    command -v claude >/dev/null 2>&1 \
        || fail 'precondition not met: claude is not on PATH' 2
    ROSTER=$(docflow roster show) \
        || fail 'precondition not met: docflow roster show failed' 2
    printf '%s\n' "$ROSTER" | grep -Eq 'check:.*strata' \
        || fail 'precondition not met: roster check providers list no strata' 2
    printf '%s\n' "$ROSTER" \
        | grep -qF 'executor: pi | strata/swift-1.5-iq3_s | high' \
        || fail 'precondition not met: roster has no executor pi | strata/swift-1.5-iq3_s | high' 2
    printf '%s\n' "$ROSTER" \
        | grep -qF 'cheap: pi | strata/swift-1.5-iq3_s | low' \
        || fail 'precondition not met: roster has no cheap pi | strata/swift-1.5-iq3_s | low' 2

    progress "creating the fixture in $REPO"
    rm -rf "$REPO"
    mkdir -p "$REPO/docs/design" "$REPO/docs/plans"
    cat > "$REPO/go.mod" <<'EOF'
module example.com/greeter

go 1.21
EOF
    cat > "$REPO/main.go" <<'EOF'
package main

import "fmt"

func main() {
	fmt.Println("greeter")
}
EOF
    cat > "$REPO/main_test.go" <<'EOF'
package main

import "testing"

func TestGreeter(t *testing.T) {
	t.Log("tests for the greet subcommand live here once it exists")
}
EOF
    cat > "$REPO/README.md" <<'EOF'
# greeter

A tiny Go CLI used as a docflow planning fixture.
EOF
    cat > "$REPO/docs/design/README.md" <<'EOF'
# Design decisions

Index of design topics. Empty: the project has no recorded decisions yet.
EOF
    cat > "$REPO/docs/docflow.md" <<'EOF'
---
docflow: 3
read_budget: 40000
---
## Queue

## Backlog
- Add a `greet` subcommand with `--name`, `--shout` and `--format json`,
  a README usage section and a test per flag.
EOF
    git -C "$REPO" init -q
    git -C "$REPO" add -A
    git -C "$REPO" -c user.name=docflow-check -c user.email=docflow-check@example.com \
        commit -qm 'initial commit'

    progress "calling claude (up to 20 minutes); log: $AGENT_LOG"
    cd "$REPO"
    PROMPT="Read and follow $SKILL and plan the Backlog line from docs/docflow.md as the skill says. The survey is already answered: routes come from the roster's default profile (docflow roster apply without --profile), permissions: auto, auto-replan off, there are no prerequisites. Ask nothing, do not activate the plan, do not commit."
    claude -p "$PROMPT" --model claude-sonnet-5-5 \
        --permission-mode auto --permission-prompts none \
        > "$AGENT_LOG" 2>&1 &
    AGENT_PID=$!
    (
        sleep 1200
        kill "$AGENT_PID" 2>/dev/null
        wait "$AGENT_PID" 2>/dev/null
    ) &
    WATCHDOG_PID=$!
    AGENT_RC=0
    wait "$AGENT_PID" || AGENT_RC=$?
    kill "$WATCHDOG_PID" 2>/dev/null || true
    wait "$WATCHDOG_PID" 2>/dev/null || true
    cd "$REPO_DIR"
    progress "agent call finished with code $AGENT_RC"
else
    [ -x "$WORK/bin/docflow" ] \
        || fail "no $WORK/bin/docflow from a full run; run without --check-only first" 2
    PATH="$WORK/bin:$PATH"
    export PATH
fi

# Checks run in the fixture with the built binary first on PATH.
cd "$REPO"

PLAN_FILE=
set -- docs/plans/*
if [ $# -eq 1 ] && [ -f "$1" ]; then
    PLAN_FILE=$1
fi
PLAN_ID=
if [ -n "$PLAN_FILE" ]; then
    PLAN_ID=${PLAN_FILE##*/}
    PLAN_ID=${PLAN_ID%%-*}
fi

if [ -n "$PLAN_FILE" ]; then
    ok exactly-one-plan-file
else
    fail_check exactly-one-plan-file \
        "expected exactly one file in docs/plans/ (see $AGENT_LOG)"
fi

run_check docflow-lint docflow lint

if [ -z "$PLAN_FILE" ]; then
    fail_check status-draft "no plan file in docs/plans/"
elif grep -q '^status: draft' "$PLAN_FILE"; then
    ok status-draft
else
    fail_check status-draft "plan frontmatter does not say status: draft"
fi

FRONT=
if [ -n "$PLAN_FILE" ]; then
    FRONT=$(sed -n '1,/^---$/p' "$PLAN_FILE")
fi
for key in executor review acceptance; do
    if printf '%s\n' "$FRONT" | grep -q "^$key: "; then
        ok "header-$key"
    else
        fail_check "header-$key" "plan frontmatter has no $key: chain"
    fi
done

if [ -z "$PLAN_FILE" ]; then
    fail_check no-replan "no plan file in docs/plans/"
elif ! printf '%s\n' "$FRONT" | grep -q '^replan:'; then
    ok no-replan
else
    fail_check no-replan "plan frontmatter still has replan: (auto-replan is off)"
fi

if [ -n "$PLAN_ID" ]; then
    run_check dry-run docflow run --dry-run --plan "$PLAN_ID" --all
else
    fail_check dry-run "no plan id discovered in docs/plans/"
fi

if [ -n "$PLAN_FILE" ] && awk '
    /^### \[.\] / { role = ""; seen = 1 }
    /^Role:[ \t]*/ { role = $2 }
    END { exit !(seen && role == "acceptance") }
' "$PLAN_FILE"; then
    ok last-unit-acceptance
else
    fail_check last-unit-acceptance "the last unit must have Role: acceptance"
fi

if [ -n "$PLAN_FILE" ] && awk '
    function close_unit() {
        if (!inunit) return
        if (unitrole != "review" && unitrole != "acceptance") plain = 1
        lastrole = unitrole
        unitrole = ""
        inunit = 0
    }
    /^## / {
        close_unit()
        if (inphase && plain && lastrole != "review") bad = 1
        inphase = ($0 ~ /^## Phase/)
        plain = 0
        lastrole = ""
        next
    }
    /^### / {
        close_unit()
        inunit = 1
        unitrole = ""
        next
    }
    /^Role: / { if (inunit) unitrole = $2 }
    END {
        close_unit()
        if (inphase && plain && lastrole != "review") bad = 1
        exit bad
    }
' "$PLAN_FILE"; then
    ok phase-review-closure
else
    fail_check phase-review-closure \
        "every phase holding an ordinary unit must end with a Role: review unit"
fi

if [ "$FAILED" -eq 0 ]; then
    progress 'all checks passed'
    exit 0
fi
progress 'some checks failed'
exit 1
