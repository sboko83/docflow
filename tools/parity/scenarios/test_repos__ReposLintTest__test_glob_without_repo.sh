#!/bin/bash
# test_repos.ReposLintTest.test_glob_without_repo
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
edit docs/docflow.md 's@repos: projects/\*@repos: projects/*, libs/*@'
e2e_cli 0 lint
state
