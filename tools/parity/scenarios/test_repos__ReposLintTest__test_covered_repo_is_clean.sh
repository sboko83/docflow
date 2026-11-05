#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
# test_repos.ReposLintTest.test_covered_repo_is_clean
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
e2e_cli 0 lint
state
