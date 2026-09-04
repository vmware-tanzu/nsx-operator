#!/usr/bin/env bash

set -euo pipefail

echo "===> Cleaning up obsolete snapshot files <==="

SNAPSHOTS_DIR="test/unittests/snapshots"

if [ ! -d "$SNAPSHOTS_DIR" ]; then
    echo "Snapshots directory $SNAPSHOTS_DIR does not exist. Skipping."
    exit 0
fi

# Find all .snap files in the centralized snapshots directory
SNAP_FILES=($(find "$SNAPSHOTS_DIR" -maxdepth 1 -name "*.snap" 2>/dev/null || true))

if [ ${#SNAP_FILES[@]} -eq 0 ]; then
    echo "No snapshot files found."
    exit 0
fi

# Find all Go files in the repository, excluding standard non-code or large directories
GO_FILES=$(find . -type d \( -name .git -o -name .cache -o -name vendor -o -name node_modules -o -name bin -o -name build -o -name snapshots \) -prune -o -type f -name "*.go" -print)

for snap in "${SNAP_FILES[@]}"; do
    # Extract the feature name from the snapshot file path
    filename=$(basename "$snap")
    feature_name="${filename%.snap}"
    
    found=false
    # Check if the feature name is referenced in any Go file
    for go_file in $GO_FILES; do
        if grep -q "$feature_name" "$go_file" 2>/dev/null; then
            found=true
            break
        fi
    done
    
    # If the feature name is not referenced in any Go file, delete the snapshot file
    if [ "$found" = false ]; then
        echo "Deleting obsolete snapshot file: $snap"
        rm -f "$snap"
    fi
done
