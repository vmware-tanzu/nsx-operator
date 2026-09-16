#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

echo "===> Cleaning up obsolete snapshot files <==="

SNAPSHOTS_DIR="test/unittests/snapshots"

if [ ! -d "$SNAPSHOTS_DIR" ]; then
    echo "Snapshots directory $SNAPSHOTS_DIR does not exist. Skipping."
    exit 0
fi

# Find all .snap files in the centralized snapshots directory
SNAP_FILES=()
while IFS= read -r -d '' file; do
    SNAP_FILES+=("$file")
done < <(find "$SNAPSHOTS_DIR" -maxdepth 1 -name "*.snap" -print0)

if [ ${#SNAP_FILES[@]} -eq 0 ]; then
    echo "No snapshot files found."
    exit 0
fi

check_feature_referenced() {
    local feature="$1"
    # Match precise struct initialization like `Feature: "name"` to avoid false positives on common words
    local pattern="Feature:[[:space:]]*\"$feature\""

    if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        # Search for the feature initialization in all Go files using git grep
        git grep -q -E "$pattern" -- '*.go'
        return $?
    else
        # Fallback to recursive grep if not inside a git repository
        grep -rqE --include="*.go" \
            --exclude-dir=.git \
            --exclude-dir=.cache \
            --exclude-dir=vendor \
            --exclude-dir=node_modules \
            --exclude-dir=bin \
            --exclude-dir=build \
            --exclude-dir=snapshots \
            "$pattern" .
        return $?
    fi
}

for snap in "${SNAP_FILES[@]}"; do
    # Extract the feature name from the snapshot file path
    filename=$(basename "$snap")
    feature_name="${filename%.snap}"
    
    set +e
    check_feature_referenced "$feature_name"
    grep_exit_code=$?
    set -e

    if [ "$grep_exit_code" -eq 1 ]; then
        echo "Deleting obsolete snapshot file: $snap (feature \"$feature_name\" not referenced)"
        rm -f "$snap"
    elif [ "$grep_exit_code" -eq 0 ]; then
        echo "Snapshot in use: $snap"
    else
        echo "Error: Search command failed with exit code $grep_exit_code while checking $snap. Aborting cleanup to prevent data loss." >&2
        exit 1
    fi
done
