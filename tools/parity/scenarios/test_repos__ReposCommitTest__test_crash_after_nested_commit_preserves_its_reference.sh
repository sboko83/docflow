#!/bin/bash
# test_repos.ReposCommitTest.test_crash_after_nested_commit_preserves_its_reference
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
nested projects/b
nested projects/c
e2e_cli 0 prompt
printf 'let a = 2\n' >projects/a/main.swift
e2e_cli 0 verify P001.1.1
e2e_cli 0 done P001.1.1 --handoff h
mkdir -p projects/a/.git/hooks
cat >projects/a/.git/hooks/post-commit <<'EOF'
#!/bin/bash
read -r cli_pid <"$HOME/crash.pid"
kill -KILL "$cli_pid"
EOF
chmod +x projects/a/.git/hooks/post-commit
crash_commit
rm projects/a/.git/hooks/post-commit
e2e_cli 0 finish P001.1.1 --handoff h
state
observe 'git -C projects/a log --format=%B; git -C projects/a status --porcelain'
