#!/bin/bash
# test_accept_p020.CheckoutAcceptanceTest.test_foreign_docflow_on_path_is_preserved
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
printf '#!/bin/sh\nexit 99\n' >"$HOME/isolated path/docflow"
/bin/chmod +x "$HOME/isolated path/docflow"
before=$(/bin/ls -id "$HOME/isolated path/docflow")
installer --bin-dir "$bin_dir" --dry-run || [ "$?" -eq 1 ]
installer --bin-dir "$bin_dir" || [ "$?" -eq 1 ]
[ ! -d "$bin_dir" ]
[ "$before" = "$(/bin/ls -id "$HOME/isolated path/docflow")" ]
observe 'cat "$HOME/isolated path/docflow"'
