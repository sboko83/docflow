#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
# test_repos.ReposLintTest.test_repo_without_key
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
edit docs/docflow.md '/^repos:/d'
e2e_cli 0 lint
state
