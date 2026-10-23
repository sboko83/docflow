#!/bin/bash
# test_repos.ReposLintTest.test_plain_dir_is_not_a_repo
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
mkdir -p projects/notes
printf 'x\n' >projects/notes/readme.md
e2e_cli 0 lint
state
