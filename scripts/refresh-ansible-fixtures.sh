#!/usr/bin/env bash
# Re-render Xray inbound JSONs from Raven-server-install's Ansible templates
# and copy them into testdata/ansible-rendered/conf.d so the cross-repo
# contract test (internal/xray/contract_test.go) sees the latest schema.
#
# Run from any directory inside the raven-subscribe repo.
#
# Requires:
#   - Raven-server-install checked out as a sibling directory (or
#     RAVEN_SERVER_INSTALL_DIR pointing at it)
#   - ansible-playbook in PATH (the same toolchain used by tests/run.sh)
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
default_sibling="$(dirname "$repo_root")/Raven-server-install"
ansible_dir="${RAVEN_SERVER_INSTALL_DIR:-$default_sibling}"

if [ ! -d "$ansible_dir" ]; then
    echo "ERROR: Raven-server-install not found at $ansible_dir" >&2
    echo "Set RAVEN_SERVER_INSTALL_DIR or check it out as a sibling directory." >&2
    exit 1
fi

dest_dir="$repo_root/testdata/ansible-rendered/conf.d"
mkdir -p "$dest_dir"

echo "Rendering Ansible templates in $ansible_dir ..."
(cd "$ansible_dir" && SKIP_XRAY_TEST=1 ./tests/run.sh)

src_dir="$ansible_dir/tests/.output/conf.d"
if [ ! -d "$src_dir" ]; then
    echo "ERROR: rendered output not found at $src_dir" >&2
    exit 1
fi

# Wipe destination so removed inbound files don't linger as stale fixtures.
rm -f "$dest_dir"/*.json
cp "$src_dir"/*.json "$dest_dir"/

echo
echo "Refreshed fixtures in $dest_dir:"
ls -1 "$dest_dir"

cat <<'NEXT'

Now run the contract tests to see if the parser still matches the new schema:

    go test ./internal/xray/... -run TestAnsibleContract -v
NEXT
