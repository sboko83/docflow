#!/bin/bash
# test_repos.ReposCycleTest.test_nested_repo_without_commits
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
mkdir -p projects/fresh
git -C projects/fresh -c init.templateDir= init -q -b main
printf 'let x = 1\n' >projects/fresh/x.swift
e2e_cli 5 prompt
# Baseline the unborn repo while the unit is in progress.
edit "$plan" 's/\[ \] P001.1.1/[~] P001.1.1/'
e2e_cli 0 verify P001.1.1
printf 'let x = 2\n' >projects/fresh/x.swift
e2e_cli 5 done P001.1.1 --handoff h
state
