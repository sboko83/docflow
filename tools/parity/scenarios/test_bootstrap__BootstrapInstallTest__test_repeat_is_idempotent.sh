#!/bin/bash
# test_bootstrap.BootstrapInstallTest.test_repeat_is_idempotent
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
installer --bin-dir "$bin_dir"
before=$(/bin/ls -id "$bin_dir/docflow")
installer --bin-dir "$bin_dir"
[ "$before" = "$(/bin/ls -id "$bin_dir/docflow")" ]
installed_cli --version
