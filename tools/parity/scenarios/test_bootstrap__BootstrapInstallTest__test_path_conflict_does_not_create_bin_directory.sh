#!/bin/bash
# test_bootstrap.BootstrapInstallTest.test_path_conflict_does_not_create_bin_directory
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
printf '#!/bin/sh\nexit 0\n' >"$HOME/isolated path/docflow"
/bin/chmod +x "$HOME/isolated path/docflow"
installer --bin-dir "$HOME/not created/bin" || [ "$?" -eq 1 ]
[ ! -d "$HOME/not created/bin" ]
