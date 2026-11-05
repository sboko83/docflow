#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
# test_repos.ReposLintTest.test_repo_not_in_repos
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
nested modules/b
e2e_cli 0 lint
state
