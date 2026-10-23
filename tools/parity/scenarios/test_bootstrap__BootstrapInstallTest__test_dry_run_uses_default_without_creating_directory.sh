#!/bin/bash
# test_bootstrap.BootstrapInstallTest.test_dry_run_uses_default_without_creating_directory
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
installer --dry-run
[ ! -d "$bin_dir" ]
