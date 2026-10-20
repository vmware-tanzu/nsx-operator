#!/usr/bin/env bash
set -euo pipefail

# Compile first. The binary owns cancellation/cleanup; go test's timeout panic
# would otherwise bypass t.Cleanup. No images are built/deployed by this script.
repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
binary_dir=$(mktemp -d)
trap 'rm -rf -- "$binary_dir"' EXIT
go test -c -o "$binary_dir/podv2-e2e.test" ./test/e2e

child_pid=''
forward_signal() {
    if [[ -n "$child_pid" ]]; then
        kill -TERM "$child_pid" 2>/dev/null || true
    fi
}
trap forward_signal INT TERM
e2e=true "$binary_dir/podv2-e2e.test" -test.v -test.timeout=0 "$@" &
child_pid=$!
result=0
wait "$child_pid" || result=$?
# A signal can interrupt wait before the child's rollback finishes.
if kill -0 "$child_pid" 2>/dev/null; then
    wait "$child_pid" || result=$?
fi
exit "$result"
