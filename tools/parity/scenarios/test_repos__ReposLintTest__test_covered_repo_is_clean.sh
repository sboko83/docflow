#!/bin/bash
# test_repos.ReposLintTest.test_covered_repo_is_clean
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
e2e_cli 0 lint
state
