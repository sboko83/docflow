#!/bin/bash
# test_accept_p021.SkillSampleTest.test_sample
source "$(dirname "$parity_scenario")/../e2e.sh"
plan=docs/plans/P001-files.md
accept_fixture p021
activate_fixture
cp "$e2e_dir/fixtures/skill-sample.md" docs/plans/P007-offline-cache.md
mkdir -p shared/src/commonMain/kotlin/feed
for ((n=0; n<80; n++)); do printf '//\n'; done >shared/src/commonMain/kotlin/feed/FeedRepository.kt
edit docs/docflow.md '/- P001/a\
- P007
'
e2e_cli 0 lint
save_fixture 'draft P007'
e2e_cli 0 run --dry-run --plan P007 --all
agent_logs
