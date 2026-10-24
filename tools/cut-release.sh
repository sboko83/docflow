#!/bin/bash
# Cut a docflow release in two explicit steps.
#
#   prepare [BUMP]  Local only. Checks the tree, runs the gates (go test,
#                   tools/test-install.sh, tools/audit-public.sh), moves the
#                   CHANGELOG.md [Unreleased] entries under the new version,
#                   bumps var version in cmd/docflow/main.go, commits
#                   "Release docflow X.Y.Z" and creates the annotated tag
#                   vX.Y.Z. BUMP is patch, minor, major or X.Y.Z; without
#                   it the script prints a suggestion from the CHANGELOG
#                   sections and exits 1.
#   publish         Pushes main to origin, waits for CI on the release
#                   commit, pushes the tag only when CI is green, waits for
#                   the release workflow (build, GitHub Release, install
#                   smoke) and checks the published assets. Steps already
#                   done on the remote are skipped, so a rerun resumes.
#
# Usage: bash tools/cut-release.sh prepare [BUMP] | publish
set -euo pipefail

REMOTE=origin
BRANCH=main

fail() {
    printf 'cut-release: %s\n' "$1" >&2
    exit 1
}

say() {
    printf 'cut-release: %s\n' "$1"
}

usage() {
    sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//' >&2
    exit 1
}

cd "$(dirname "$0")/.." || fail 'cannot locate the repository'
MAIN=cmd/docflow/main.go
LOG=CHANGELOG.md

current_version() {
    local v
    v=$(sed -n 's/^var version = "\(.*\)"$/\1/p' "$MAIN")
    [ -n "$v" ] || fail "var version not found in $MAIN"
    printf '%s\n' "$v"
}

is_semver() {
    printf '%s\n' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'
}

# Exit 0 when version $1 is strictly greater than $2.
version_gt() {
    awk -v a="$1" -v b="$2" 'BEGIN {
        split(a, x, "."); split(b, y, ".")
        for (i = 1; i <= 3; i++)
            if (x[i] + 0 != y[i] + 0) exit !(x[i] + 0 > y[i] + 0)
        exit 1
    }'
}

# Body of the [Unreleased] section, blank lines dropped.
unreleased_body() {
    awk '
        index($0, "## [Unreleased]") == 1 { on = 1; next }
        on && (/^## / || /^\[[^]]+\]: /) { exit }
        on && NF { print }
    ' "$LOG"
}

# Suggest a bump from the section headings. Before 1.0 a breaking change
# is a minor bump, so major is suggested only from 1.0 on.
suggest_bump() {
    local body=$1 major=$2
    if [ "$major" -ge 1 ] && printf '%s\n' "$body" | grep -q '^### Removed'; then
        echo major
    elif printf '%s\n' "$body" | grep -Eq '^### (Added|Changed|Deprecated|Removed)'; then
        echo minor
    else
        echo patch
    fi
}

check_branch_clean() {
    [ "$(git rev-parse --abbrev-ref HEAD)" = "$BRANCH" ] \
        || fail "not on $BRANCH"
    [ -z "$(git status --porcelain)" ] \
        || fail 'working tree is not clean; commit or stash first'
}

# owner/repo of the remote, for gh -R.
remote_slug() {
    git remote get-url "$REMOTE" \
        | sed -E 's#^(https://github\.com/|git@github\.com:)##; s#\.git$##'
}

prepare() {
    check_branch_clean
    git fetch --quiet "$REMOTE" "$BRANCH" \
        || fail "cannot fetch $REMOTE/$BRANCH"
    git merge-base --is-ancestor "$REMOTE/$BRANCH" HEAD \
        || fail "$BRANCH is behind or diverged from $REMOTE/$BRANCH"

    local active
    active=$(grep -l '^status: active$' docs/plans/*.md 2>/dev/null || true)
    [ -z "$active" ] \
        || fail "active plan, close it before a release: $active"

    local cur body
    cur=$(current_version)
    body=$(unreleased_body)
    [ -n "$body" ] || fail "CHANGELOG.md [Unreleased] is empty, nothing to release"

    local maj min pat
    IFS=. read -r maj min pat <<EOF
$cur
EOF
    local bump=${1-}
    if [ -z "$bump" ]; then
        say "current $cur; [Unreleased] suggests: $(suggest_bump "$body" "$maj")"
        say "run: bash tools/cut-release.sh prepare patch|minor|major|X.Y.Z"
        exit 1
    fi

    local new
    case $bump in
        patch) new=$maj.$min.$((pat + 1)) ;;
        minor) new=$maj.$((min + 1)).0 ;;
        major) new=$((maj + 1)).0.0 ;;
        *)
            is_semver "$bump" || fail "BUMP must be patch, minor, major or X.Y.Z, got: $bump"
            new=$bump
            ;;
    esac
    version_gt "$new" "$cur" || fail "$new is not greater than $cur"
    local tag=v$new
    [ -z "$(git tag -l "$tag")" ] || fail "tag $tag already exists locally"
    [ -z "$(git ls-remote --tags "$REMOTE" "refs/tags/$tag")" ] \
        || fail "tag $tag already exists on $REMOTE"

    local link
    link=$(sed -n 's#^\[Unreleased\]: \(.*\)/compare/v[0-9.]*\.\.\.HEAD$#\1#p' "$LOG")
    [ -n "$link" ] || fail 'CHANGELOG.md has no [Unreleased]: <repo>/compare/vX.Y.Z...HEAD link'

    say "release $cur -> $new; running the gates"
    go test ./... >/dev/null || fail 'go test ./... failed'
    bash tools/test-install.sh >/dev/null || fail 'tools/test-install.sh failed'
    bash tools/audit-public.sh >/dev/null \
        || fail 'tools/audit-public.sh found unreviewed data; a human reviews new allowances'

    local today tmp
    today=$(date +%Y-%m-%d)
    tmp=$(mktemp)
    sed "s/^var version = \".*\"$/var version = \"$new\"/" "$MAIN" >"$tmp"
    cat "$tmp" >"$MAIN"
    awk -v new="$new" -v cur="$cur" -v day="$today" -v link="$link" '
        $0 == "## [Unreleased]" {
            print; print ""; print "## [" new "] - " day; next
        }
        index($0, "[Unreleased]: ") == 1 {
            print "[Unreleased]: " link "/compare/v" new "...HEAD"
            print "[" new "]: " link "/compare/v" cur "...v" new
            next
        }
        { print }
    ' "$LOG" >"$tmp"
    cat "$tmp" >"$LOG"
    rm -f "$tmp"

    # The release workflow runs the same two checks; fail here, not in CI.
    bash tools/check-release-tag.sh "$tag" || fail 'version bump did not apply'
    bash tools/release-notes.sh "$new" >/dev/null || fail 'release notes are empty'
    [ -z "$(unreleased_body)" ] || fail 'CHANGELOG.md [Unreleased] is not empty after the move'

    git add "$MAIN" "$LOG"
    git commit --quiet -m "Release docflow $new" \
        -m "- Bump version to $new
- Move CHANGELOG [Unreleased] entries under $new"
    git tag -a "$tag" -m "docflow $new"
    say "committed and tagged $tag locally; nothing was pushed"
    say "review: git show --stat HEAD; publish: bash tools/cut-release.sh publish"
}

# Print the id of the newest run of workflow $1 for commit $2, waiting up
# to two minutes for GitHub to register it.
find_run() {
    local wf=$1 sha=$2 slug=$3 id i
    for i in $(seq 1 24); do
        id=$(gh run list -R "$slug" --workflow "$wf" --commit "$sha" \
            --limit 1 --json databaseId --jq '.[0].databaseId // empty')
        [ -n "$id" ] && { printf '%s\n' "$id"; return 0; }
        sleep 5
    done
    return 1
}

watch_run() {
    local wf=$1 sha=$2 slug=$3 id
    id=$(find_run "$wf" "$sha" "$slug") || fail "no $wf run for $sha on GitHub"
    say "waiting for $wf run $id"
    gh run watch -R "$slug" "$id" --exit-status --interval 10 >/dev/null \
        || fail "$wf run $id failed: gh run view -R $slug $id --log-failed"
}

publish() {
    command -v gh >/dev/null 2>&1 || fail 'gh is required'
    check_branch_clean
    local ver tag sha slug
    ver=$(current_version)
    tag=v$ver
    sha=$(git rev-parse HEAD)
    [ "$(git rev-parse "$tag^{commit}" 2>/dev/null)" = "$sha" ] \
        || fail "HEAD is not tagged $tag; run prepare first"
    slug=$(remote_slug)

    git fetch --quiet "$REMOTE" "$BRANCH" || fail "cannot fetch $REMOTE/$BRANCH"
    if [ "$(git rev-parse "$REMOTE/$BRANCH")" != "$sha" ]; then
        git merge-base --is-ancestor "$REMOTE/$BRANCH" HEAD \
            || fail "$REMOTE/$BRANCH has diverged from $BRANCH"
        say "pushing $BRANCH to $REMOTE"
        git push --quiet "$REMOTE" "$BRANCH"
    fi
    watch_run ci.yml "$sha" "$slug"

    if [ -z "$(git ls-remote --tags "$REMOTE" "refs/tags/$tag")" ]; then
        say "CI is green; pushing $tag"
        git push --quiet "$REMOTE" "$tag"
    fi
    watch_run release.yml "$sha" "$slug"

    local assets want
    assets=$(gh release view -R "$slug" "$tag" --json assets --jq '.assets[].name')
    for want in checksums.txt VERSION docflow_darwin_amd64.tar.gz \
        docflow_darwin_arm64.tar.gz docflow_linux_amd64.tar.gz \
        docflow_linux_arm64.tar.gz; do
        printf '%s\n' "$assets" | grep -qx "$want" || fail "release $tag lacks $want"
    done
    local latest
    latest=$(curl -fsSL "https://github.com/$slug/releases/latest/download/VERSION") \
        || fail 'cannot read latest/download/VERSION'
    [ "$latest" = "$ver" ] || fail "latest release is $latest, expected $ver"
    say "published $tag: https://github.com/$slug/releases/tag/$tag"
}

case ${1-} in
    prepare) shift; [ "$#" -le 1 ] || usage; prepare "${1-}" ;;
    publish) [ "$#" -eq 1 ] || usage; publish ;;
    *) usage ;;
esac
