#!/bin/bash
set -e

usage() {
    cat <<'EOF'
Usage: bash tools/install-dev.sh [--bin-dir DIR] [--no-skills] [--dry-run]

Build and install docflow from this checkout. The default directory is
~/.local/bin. The checkout must remain in place while the symlink is used.
EOF
}

fail() {
    printf 'error: %s\n' "$1" >&2
    exit 1
}

BIN_DIR="${HOME:+$HOME/.local/bin}"
DRY_RUN=0
NO_SKILLS=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --bin-dir)
            [ "$#" -ge 2 ] || fail '--bin-dir requires a directory'
            BIN_DIR=$2
            shift 2
            ;;
        --no-skills)
            NO_SKILLS=1
            shift
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            fail "unknown option: $1"
            ;;
    esac
done

[ -n "$BIN_DIR" ] || fail 'HOME is not set; pass --bin-dir DIR'
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P) \
    || fail 'cannot locate tools directory'
REPO_DIR=$(cd "$SCRIPT_DIR/.." && pwd -P) \
    || fail 'cannot locate checkout'
# Windows executables carry .exe: LookPath and doctor need it on PATH.
EXE=
case "${OSTYPE:-}" in msys*|cygwin*) EXE=.exe ;; esac
# Plain Git Bash may copy a file instead of linking it; fail loudly instead.
case "${OSTYPE:-}" in msys*) export MSYS="${MSYS:+$MSYS }winsymlinks:nativestrict" ;; esac
SOURCE="$REPO_DIR/bin/docflow$EXE"
GO_MOD="$REPO_DIR/go.mod"
MAIN_SOURCE="$REPO_DIR/cmd/docflow/main.go"
[ -f "$GO_MOD" ] || fail "incomplete checkout: missing $GO_MOD"
[ -f "$MAIN_SOURCE" ] || fail "incomplete checkout: missing $MAIN_SOURCE"
[ -d "$REPO_DIR/internal" ] || fail "incomplete checkout: missing $REPO_DIR/internal"
[ -d "$REPO_DIR/skills" ] || fail "incomplete checkout: missing $REPO_DIR/skills"
[ -d "$REPO_DIR/templates" ] || fail "incomplete checkout: missing $REPO_DIR/templates"

GO=$(command -v go 2>/dev/null) \
    || fail 'Go 1.24 or newer is required (go was not found)'
GO_VERSION=$("$GO" version 2>&1) \
    || fail "cannot run Go toolchain: $GO_VERSION"
if ! printf '%s\n' "$GO_VERSION" | grep -qE 'go1\.(2[4-9]|[3-9][0-9])'; then
    fail "Go 1.24 or newer is required (found $GO_VERSION)"
fi

case "$BIN_DIR" in
    /*) ;;
    *) BIN_DIR="$PWD/$BIN_DIR" ;;
esac
check_path_components() {
    local path="$1"
    [ -n "$path" ] || path=/
    while [ "$path" != / ]; do
        if [ -e "$path" ] || [ -L "$path" ]; then
            [ -d "$path" ] || fail "conflict: $path is not a directory"
        fi
        path=${path%/*}
        [ -n "$path" ] || path=/
    done
}
check_path_components "$BIN_DIR"
TARGET="$BIN_DIR/docflow$EXE"

resolve_path() {
    local path="$1" link parent
    case "$path" in
        /*) ;;
        *) path="$PWD/$path" ;;
    esac
    while [ -L "$path" ]; do
        link=$(readlink "$path") || return 1
        case "$link" in
            /*) path=$link ;;
            *) path="$(dirname "$path")/$link" ;;
        esac
    done
    parent=$(cd "$(dirname "$path")" 2>/dev/null && pwd -P) || return 1
    printf '%s/%s\n' "$parent" "$(basename "$path")"
}

TARGET_OWN=0
if [ -L "$TARGET" ]; then
    RESOLVED_TARGET=$(resolve_path "$TARGET") \
        || fail "cannot resolve $TARGET"
    if [ "$RESOLVED_TARGET" = "$SOURCE" ]; then
        TARGET_OWN=1
    else
        printf 'conflict: %s points to %s; this checkout is %s\n' \
            "$TARGET" "$RESOLVED_TARGET" "$SOURCE" >&2
        exit 1
    fi
elif [ -e "$TARGET" ]; then
    printf 'conflict: %s already exists and is not this checkout\n' \
        "$TARGET" >&2
    exit 1
fi

PATH_DOCFLOW=$(command -v docflow 2>/dev/null || true)
if [ -n "$PATH_DOCFLOW" ]; then
    RESOLVED_PATH_DOCFLOW=$(resolve_path "$PATH_DOCFLOW") \
        || fail "cannot resolve PATH command $PATH_DOCFLOW"
    if [ "$RESOLVED_PATH_DOCFLOW" != "$SOURCE" ]; then
        printf 'conflict: PATH resolves docflow to %s (resolves to %s)\n' \
            "$PATH_DOCFLOW" "$RESOLVED_PATH_DOCFLOW" >&2
        exit 1
    fi
fi

install_skills() {
    local agent
    [ "$NO_SKILLS" -eq 0 ] || return 0
    for agent in claude codex pi; do
        if command -v "$agent" >/dev/null 2>&1; then
            if [ "$DRY_RUN" -eq 1 ]; then
                printf 'would install skills: %s install-skills --agent %s\n' \
                    "$TARGET" "$agent"
            else
                "$TARGET" install-skills --agent "$agent"
            fi
        fi
    done
    if [ -n "${HOME:-}" ] && [ -d "$HOME/.zcode" ]; then
        if [ "$DRY_RUN" -eq 1 ]; then
            printf 'would install skills: %s install-skills --target %s\n' \
                "$TARGET" "$HOME/.zcode/skills"
        else
            "$TARGET" install-skills --target "$HOME/.zcode/skills"
        fi
    fi
}

if [ "$DRY_RUN" -eq 1 ]; then
    printf 'would build: go build -o %s ./cmd/docflow\n' "$SOURCE"
    if [ "$TARGET_OWN" -eq 1 ]; then
        printf 'already installed: %s -> %s\n' "$TARGET" "$SOURCE"
    else
        printf 'would install symlink: %s -> %s\n' "$TARGET" "$SOURCE"
    fi
    install_skills
    exit 0
fi

if ! (cd "$REPO_DIR" && "$GO" build -o "$SOURCE" ./cmd/docflow); then
    fail 'Go build failed'
fi
printf 'built: %s\n' "$SOURCE"

if [ "$TARGET_OWN" -eq 0 ]; then
    mkdir -p "$BIN_DIR" || fail "cannot create $BIN_DIR"
    # Recheck the leaf immediately before ln; ln itself never overwrites it.
    if [ -e "$TARGET" ] || [ -L "$TARGET" ]; then
        if [ -L "$TARGET" ] && [ "$(resolve_path "$TARGET")" = "$SOURCE" ]; then
            TARGET_OWN=1
        else
            printf 'conflict: %s appeared during installation\n' "$TARGET" >&2
            exit 1
        fi
    else
        ln -s "$SOURCE" "$TARGET" || fail "cannot create symlink $TARGET"
    fi
fi

printf 'installed: %s -> %s\n' "$TARGET" "$SOURCE"
install_skills

BIN_DIR_PHYSICAL=$(cd "$BIN_DIR" && pwd -P) \
    || fail "cannot resolve $BIN_DIR"
PATH_HAS_BIN=0
SAVED_IFS=$IFS
IFS=:
set -f
for PATH_ENTRY in $PATH; do
    [ -n "$PATH_ENTRY" ] || PATH_ENTRY=$PWD
    if [ -d "$PATH_ENTRY" ]; then
        PATH_ENTRY=$(cd "$PATH_ENTRY" && pwd -P) || continue
    fi
    [ "$PATH_ENTRY" = "$BIN_DIR_PHYSICAL" ] && PATH_HAS_BIN=1
done
set +f
IFS=$SAVED_IFS
if [ "$PATH_HAS_BIN" -eq 0 ]; then
    printf 'Add this line to your shell profile, then open a new shell:\n'
    printf 'export PATH="%s:$PATH"\n' "$BIN_DIR_PHYSICAL"
fi
