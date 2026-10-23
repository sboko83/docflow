#!/bin/bash
# test_accept_p021.PreflightTest.test_resume_takes_the_same_path
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-files.md
accept_fixture p021
head_route 'executor: claude | claude-opus-5-5 | high'
activate_fixture
edit "$plan" 's/\[ \] P001.1.1/[~] P001.1.1/'
unit_route 2 'Executor: robot | x | y'
save_fixture 'stopped run'
e2e_cli 2 run
state
agent_logs
