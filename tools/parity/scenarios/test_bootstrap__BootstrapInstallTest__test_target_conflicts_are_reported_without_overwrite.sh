#!/bin/bash
# test_bootstrap.BootstrapInstallTest.test_target_conflicts_are_reported_without_overwrite
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
mkdir -p "$bin_dir"
for kind in executable file directory other-checkout dangling; do
    /bin/rm -rf "$bin_dir/docflow$e2e_exe"
    case "$kind" in
        directory) mkdir "$bin_dir/docflow$e2e_exe" ;;
        other-checkout) mkdir -p "$HOME/other checkout/bin"; printf '#!/bin/sh\nexit 0\n' >"$HOME/other checkout/bin/docflow$e2e_exe"; ln -s "$HOME/other checkout/bin/docflow$e2e_exe" "$bin_dir/docflow$e2e_exe" ;;
        dangling) ln -s "$HOME/missing docflow" "$bin_dir/docflow$e2e_exe" ;;
        *) printf 'foreign command\n' >"$bin_dir/docflow$e2e_exe"; if [ "$kind" = executable ]; then /bin/chmod +x "$bin_dir/docflow$e2e_exe"; fi ;;
    esac
    before=$(/bin/ls -id "$bin_dir/docflow$e2e_exe")
    installer --bin-dir "$bin_dir" || [ "$?" -eq 1 ]
    [ "$before" = "$(/bin/ls -id "$bin_dir/docflow$e2e_exe")" ]
    observe 'if [ -L "$HOME/.local/bin/docflow$e2e_exe" ]; then readlink "$HOME/.local/bin/docflow$e2e_exe"; elif [ -f "$HOME/.local/bin/docflow$e2e_exe" ]; then cat "$HOME/.local/bin/docflow$e2e_exe"; else test -d "$HOME/.local/bin/docflow$e2e_exe"; fi'
done
