#!/bin/bash
# test_repos.ReposCommitTest.test_hook_fails_in_nested_repo
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
nested projects/b
nested projects/c
e2e_cli 0 prompt
mkdir -p projects/b/.git/hooks
cat >projects/b/.git/hooks/pre-commit <<'EOF'
#!/bin/sh
test ! -f .git/fail-marker || { echo hook says no; exit 1; }
EOF
chmod +x projects/b/.git/hooks/pre-commit
touch projects/b/.git/fail-marker
printf 'let a = 2\n' >projects/a/main.swift
printf 'let a = 2\n' >projects/b/main.swift
e2e_cli 4 finish P001.1.1 --handoff h
state
rm projects/b/.git/fail-marker
e2e_cli 0 finish P001.1.1 --handoff h
e2e_cli 5 finish P001.1.1 --handoff h
state
