#!/bin/bash
# Audit the published tree for sensitive data: absolute home paths,
# e-mails, private-key headers, token prefixes, mentions of the private
# hosting, private IP addresses.
#
# Walks the files from `git ls-files` (git history is not audited).
# Prints every finding as `file:line: pattern`; a finding covered by
# tools/audit-public.allow gets an `(allowed)` suffix. Exits 1 when at
# least one finding is not covered. The allow file holds one
# `path:pattern` line per allowance; a human reviews every allow line
# before a publication.

set -u
export LC_ALL=C

cd "$(dirname "$0")/.." || exit 2

allow_file=tools/audit-public.allow

# One entry per check: an id printed on findings and an extended regex.
ids=(
    home
    email
    begin
    token
    gitflic
    ip
)
regexes=(
    '/Users/|/home/[A-Za-z0-9_]'
    '[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+'
    '-----BEGIN'
    'ghp_|github_pat_|sk-|xox|AKIA'
    'gitflic'
    '(^|[^0-9.])(10\.[0-9]+\.[0-9]+\.[0-9]+|192\.168\.[0-9]+\.[0-9]+|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]+\.[0-9]+)'
)

files=()
while IFS= read -r -d '' file; do
    [ -f "$file" ] && files+=("$file")
done < <(git ls-files -z)
[ "${#files[@]}" -gt 0 ] || exit 0

allowed() {
    [ -f "$allow_file" ] || return 1
    grep -qx "$1:$2" "$allow_file"
}

status=0
i=0
for id in "${ids[@]}"; do
    regex=${regexes[$i]}
    i=$((i + 1))
    hits=$(grep -nIE -- "$regex" "${files[@]}" 2>/dev/null || true)
    while IFS= read -r hit; do
        [ -n "$hit" ] || continue
        path=${hit%%:*}
        rest=${hit#*:}
        line=${rest%%:*}
        if allowed "$path" "$id"; then
            printf '%s:%s: %s (allowed)\n' "$path" "$line" "$id"
        else
            printf '%s:%s: %s\n' "$path" "$line" "$id"
            status=1
        fi
    done <<< "$hits"
done

if [ "$status" -ne 0 ]; then
    printf 'audit failed: clean the findings or cover them in %s as `path:pattern`\n' \
        "$allow_file" >&2
fi
exit "$status"
