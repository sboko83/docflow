#!/bin/bash
# test_repos.ReposCycleTest.test_root_is_blind_to_ignored_repo
source "$(dirname "$parity_scenario")/../e2e.sh"
repos_fixture
printf 'let a = 3\n' >projects/a/main.swift
observe 'git status --porcelain'
e2e_cli 5 prompt
state
