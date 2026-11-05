#!/bin/bash
declare -F parity_repo >/dev/null || { echo "run via tools/parity/run.sh" >&2; exit 2; }
# test_accept_p020.CheckoutAcceptanceTest.test_foreign_docflow_on_path_is_preserved
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
printf '#!/bin/sh\nexit 99\n' >"$HOME/isolated path/docflow"
/bin/chmod +x "$HOME/isolated path/docflow"
before=$(/bin/ls -id "$HOME/isolated path/docflow")
# The development CLI installs as docflow-dev beside the release docflow on
# PATH (P057): the foreign docflow neither blocks it nor is replaced.
installer --bin-dir "$bin_dir" --dry-run
installer --bin-dir "$bin_dir"
[ -L "$bin_dir/docflow-dev$e2e_exe" ]
[ ! -e "$bin_dir/docflow$e2e_exe" ]
[ "$before" = "$(/bin/ls -id "$HOME/isolated path/docflow")" ]
observe 'cat "$HOME/isolated path/docflow"'
