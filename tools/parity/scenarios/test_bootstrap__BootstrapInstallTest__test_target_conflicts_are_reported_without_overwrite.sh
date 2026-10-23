#!/bin/bash
# test_bootstrap.BootstrapInstallTest.test_target_conflicts_are_reported_without_overwrite
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
mkdir -p "$bin_dir"
for kind in executable file directory other-checkout dangling; do
    /bin/rm -rf "$bin_dir/docflow"
    case "$kind" in
        directory) mkdir "$bin_dir/docflow" ;;
        other-checkout) mkdir -p "$HOME/other checkout/bin"; printf '#!/bin/sh\nexit 0\n' >"$HOME/other checkout/bin/docflow"; ln -s "$HOME/other checkout/bin/docflow" "$bin_dir/docflow" ;;
        dangling) ln -s "$HOME/missing docflow" "$bin_dir/docflow" ;;
        *) printf 'foreign command\n' >"$bin_dir/docflow"; if [ "$kind" = executable ]; then /bin/chmod +x "$bin_dir/docflow"; fi ;;
    esac
    before=$(/bin/ls -id "$bin_dir/docflow")
    installer --bin-dir "$bin_dir" || [ "$?" -eq 1 ]
    [ "$before" = "$(/bin/ls -id "$bin_dir/docflow")" ]
    observe 'if [ -L "$HOME/.local/bin/docflow" ]; then readlink "$HOME/.local/bin/docflow"; elif [ -f "$HOME/.local/bin/docflow" ]; then cat "$HOME/.local/bin/docflow"; else test -d "$HOME/.local/bin/docflow"; fi'
done
