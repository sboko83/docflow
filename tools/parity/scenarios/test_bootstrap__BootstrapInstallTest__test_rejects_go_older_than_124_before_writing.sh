#!/bin/bash
# test_bootstrap.BootstrapInstallTest.test_rejects_go_older_than_124_before_writing
source "$(dirname "$parity_scenario")/../e2e.sh"
checkout_fixture
/bin/rm "$HOME/isolated path/go"
cat >"$HOME/isolated path/go" <<'EOF'
#!/bin/sh
if [ "$1" = version ]; then echo "go version go1.23.9 darwin/arm64"; exit 0; fi
exit 1
EOF
/bin/chmod +x "$HOME/isolated path/go"
installer || [ "$?" -eq 1 ]
[ ! -d "$bin_dir" ]
