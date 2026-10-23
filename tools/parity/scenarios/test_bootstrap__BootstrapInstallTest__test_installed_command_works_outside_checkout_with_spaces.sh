#!/bin/bash
# test_bootstrap.BootstrapInstallTest.test_installed_command_works_outside_checkout_with_spaces
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
installer
export PATH="$bin_dir:$PATH"
installed_cli --version
installed_cli install-skills --target "$HOME/installed skills"
observe 'for skill in "$HOME/installed skills"/*; do readlink "$skill"; done'
