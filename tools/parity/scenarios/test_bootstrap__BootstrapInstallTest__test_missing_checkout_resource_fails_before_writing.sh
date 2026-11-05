#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
# test_bootstrap.BootstrapInstallTest.test_missing_checkout_resource_fails_before_writing
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
/bin/rm -rf "$checkout/templates"
installer || [ "$?" -eq 1 ]
[ ! -d "$bin_dir" ]
