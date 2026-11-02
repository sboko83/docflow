#!/bin/bash
# Switch the personal skill links installed by docflow between the release
# copy (~/.docflow/current/skills) and this checkout.
#
# Usage: bash tools/switch-skills.sh [dev|release|status]
# Without an argument prints the current mode.
#
# A link is ours when its target lies under ~/.docflow/ or under this
# checkout. Foreign links (bsv-ui, ruleblend, ...) are never touched. The
# links themselves are the only state: there is no mode file.

set -u

# Plain Git Bash may copy a file instead of linking it; fail loudly instead.
case "${OSTYPE:-}" in msys*) export MSYS="${MSYS:+$MSYS }winsymlinks:nativestrict" ;; esac

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P) || exit 1
root=$(cd "$script_dir/.." && pwd -P) || exit 1
release_skills="$HOME/.docflow/current/skills"
managed_dirs="$HOME/.zcode/skills $HOME/.agents/skills $HOME/.claude/skills"

usage() {
    printf 'usage: bash tools/switch-skills.sh [dev|release|status]\n'
}

fail() {
    printf 'error: %s\n' "$1" >&2
    exit 1
}

# Absolute target of a symlink: readlink may return a relative path.
abs_target() {
    case "$2" in
        /*) printf '%s' "$2" ;;
        *) printf '%s' "$(dirname "$1")/$2" ;;
    esac
}

# Ours = the link target lies under ~/.docflow/ or under the checkout.
is_own() {
    case "$1" in
        "$HOME/.docflow/"*|"$root"/*|"$root") return 0 ;;
        *) return 1 ;;
    esac
}

shorten() {
    case "$1" in
        "$HOME"/*) printf '~%s' "${1#$HOME}" ;;
        *) printf '%s' "$1" ;;
    esac
}

do_dev() {
    local dir link name src target want
    for dir in $managed_dirs; do
        [ -d "$dir" ] || continue
        for src in "$root"/skills/*/; do
            [ -d "$src" ] || continue
            name=${src%/}
            name=${name##*/}
            link=$dir/$name
            want=$root/skills/$name
            if [ -e "$link" ] && [ ! -L "$link" ]; then
                printf 'skip: %s exists and is not a symlink\n' "$(shorten "$link")"
                continue
            fi
            if [ -L "$link" ]; then
                target=$(abs_target "$link" "$(readlink "$link")") || target=
                if [ -n "$target" ] && ! is_own "$target"; then
                    printf 'skip: %s -> %s (foreign)\n' \
                        "$(shorten "$link")" "$(shorten "$target")"
                    continue
                fi
                [ "$target" = "$want" ] && continue
            fi
            if ln -sfn "$want" "$link"; then
                printf 'dev: %s -> checkout\n' "$(shorten "$link")"
            else
                printf 'error: cannot link %s -> %s\n' \
                    "$(shorten "$link")" "$(shorten "$want")"
            fi
        done
        # A skill removed from the checkout keeps its old link, with a warning.
        for link in "$dir"/*; do
            [ -L "$link" ] || continue
            name=${link##*/}
            [ -d "$root/skills/$name" ] && continue
            target=$(abs_target "$link" "$(readlink "$link")") || continue
            is_own "$target" || continue
            printf 'warn: %s -> %s left as is (not in the checkout)\n' \
                "$(shorten "$link")" "$(shorten "$target")"
        done
    done
    printf 'new sessions pick this up; running sessions keep the old text.\n'
}

do_release() {
    local dir link name target want
    for dir in $managed_dirs; do
        [ -d "$dir" ] || continue
        for link in "$dir"/*; do
            [ -L "$link" ] || continue
            target=$(abs_target "$link" "$(readlink "$link")") || continue
            is_own "$target" || continue
            name=${link##*/}
            want=$release_skills/$name
            if [ ! -e "$want" ]; then
                printf 'warn: no release copy of %s, %s left -> %s\n' \
                    "$name" "$(shorten "$link")" "$(shorten "$target")"
                continue
            fi
            [ "$target" = "$want" ] && continue
            if ln -sfn "$want" "$link"; then
                printf 'release: %s -> release\n' "$(shorten "$link")"
            else
                printf 'error: cannot link %s -> %s\n' \
                    "$(shorten "$link")" "$(shorten "$want")"
            fi
        done
    done
    printf 'new sessions pick this up; running sessions keep the old text.\n'
}

do_status() {
    local dir link name target note
    local total=0 release_count=0 dev_count=0
    for dir in $managed_dirs; do
        [ -d "$dir" ] || continue
        for link in "$dir"/*; do
            [ -L "$link" ] || continue
            target=$(abs_target "$link" "$(readlink "$link")") || continue
            is_own "$target" || continue
            total=$((total + 1))
            name=${link##*/}
            case "$target" in
                "$release_skills"/*|"$release_skills")
                    release_count=$((release_count + 1))
                    printf '%s release\n' "$(shorten "$dir")/$name"
                    ;;
                "$root/skills"/*|"$root/skills")
                    dev_count=$((dev_count + 1))
                    if [ ! -d "$root/skills/$name" ]; then
                        note='missing in checkout'
                    elif [ ! -e "$release_skills/$name" ]; then
                        note='no release copy'
                    elif diff -rq "$root/skills/$name" "$release_skills/$name" >/dev/null 2>&1; then
                        note=same
                    else
                        note=differs
                    fi
                    printf '%s dev (%s)\n' "$(shorten "$dir")/$name" "$note"
                    ;;
                *)
                    printf '%s other -> %s\n' \
                        "$(shorten "$dir")/$name" "$(shorten "$target")"
                    ;;
            esac
        done
    done
    if [ "$total" -eq 0 ]; then
        printf 'mode: none (no managed skill links found)\n'
    elif [ "$dev_count" -eq "$total" ]; then
        printf 'mode: dev\n'
    elif [ "$release_count" -eq "$total" ]; then
        printf 'mode: release\n'
    else
        printf 'mode: mixed\n'
    fi
}

case "${1:-}" in
    ''|status) do_status ;;
    dev) do_dev ;;
    release) do_release ;;
    -h|--help) usage ;;
    *) usage >&2; fail "unknown mode: $1" ;;
esac
