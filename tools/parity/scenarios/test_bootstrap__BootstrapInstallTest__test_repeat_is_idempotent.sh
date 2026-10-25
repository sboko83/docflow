#!/bin/bash
# test_bootstrap.BootstrapInstallTest.test_repeat_is_idempotent
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
installer --bin-dir "$bin_dir"
before=$(/bin/ls -id "$bin_dir/docflow$e2e_exe")
installer --bin-dir "$bin_dir"
[ "$before" = "$(/bin/ls -id "$bin_dir/docflow$e2e_exe")" ]
installed_cli --version
