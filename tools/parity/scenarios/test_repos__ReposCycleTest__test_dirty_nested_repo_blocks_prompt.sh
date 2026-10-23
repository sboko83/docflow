#!/bin/bash
# test_repos.ReposCycleTest.test_dirty_nested_repo_blocks_prompt
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
printf 'let b = 2\n' >projects/a/new.swift
printf 'let a = 3\n' >projects/a/main.swift
e2e_cli 5 prompt
state
