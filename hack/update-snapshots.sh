#!/usr/bin/env bash

# ==============================================================================
# Automated Snapshot GC & Update Orchestration Tool
#
# Features:
# 1. Automatic Package Discovery: Identifies which Go packages contribute to a Feature snapshot.
# 2. Block-Level GC: Removes obsolete snapshot blocks by cleaning before regenerating.
# 3. Failure Safety / Auto-Rollback: Backs up snapshots and restores on test failure.
#
# Usage:
#   ./hack/update-snapshots.sh [FEATURE_NAME]
#   ./hack/update-snapshots.sh --all
#   FEATURE=subnet ./hack/update-snapshots.sh
# ==============================================================================

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

readonly GREEN='\033[0;32m'
readonly RED='\033[0;31m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly CYAN='\033[0;36m'
readonly BOLD='\033[1m'
readonly NC='\033[0m' # No Color

SNAPSHOTS_DIR="test/unittests/snapshots"
BACKUP_DIR="${REPO_ROOT}/.cache/snapshots_backup_$$"

cleanup_temp() {
    if [[ -d "$BACKUP_DIR" ]]; then
        rm -rf "$BACKUP_DIR"
    fi
}
trap cleanup_temp EXIT

TARGET_FEATURE="${1:-${FEATURE:-}}"
RUN_ALL=false

if [[ "${TARGET_FEATURE}" == "--all" || "${ALL:-false}" == "true" ]]; then
    RUN_ALL=true
fi

# Locate all referenced features in the repo
discover_all_features() {
    if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        git grep -E 'Feature:[[:space:]]*"[^"]+"' -- '*.go' | \
            sed -E 's/.*Feature:[[:space:]]*"([^"]+)".*/\1/' | sort -u
    else
        grep -rhE --include="*.go" \
            --exclude-dir=.git --exclude-dir=.cache --exclude-dir=vendor \
            'Feature:[[:space:]]*"[^"]+"' . | \
            sed -E 's/.*Feature:[[:space:]]*"([^"]+)".*/\1/' | sort -u
    fi
}

# Locate all Go packages contributing to a specific feature
discover_packages_for_feature() {
    local feature="$1"
    local pattern="Feature:[[:space:]]*\"${feature}\""
    local files=()

    if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        while IFS= read -r f; do
            [[ -n "$f" ]] && files+=("$f")
        done < <(git grep -l -E "$pattern" -- '*.go' || true)
    else
        while IFS= read -r f; do
            [[ -n "$f" ]] && files+=("$f")
        done < <(grep -rlE --include="*.go" --exclude-dir=.git --exclude-dir=.cache "$pattern" . || true)
    fi

    if [[ ${#files[@]} -eq 0 ]]; then
        return 0
    fi

    local packages=()
    for file in "${files[@]}"; do
        local dir="./$(dirname "$file")"
        packages+=("$dir")
    done

    printf "%s\n" "${packages[@]}" | sort -u
}

update_single_feature() {
    local feature="$1"
    echo -e "\n${BLUE}${BOLD}===> [Snapshot GC & Update] Processing Feature: ${CYAN}${feature}${NC} <==="

    local packages=()
    while IFS= read -r pkg; do
        [[ -n "$pkg" ]] && packages+=("$pkg")
    done < <(discover_packages_for_feature "$feature")

    if [[ ${#packages[@]} -eq 0 ]]; then
        echo -e "${YELLOW}Warning: No Go test packages found referencing Feature \"${feature}\". Skipping.${NC}"
        return 0
    fi

    echo -e "${CYAN}Contributing Packages:${NC}"
    for pkg in "${packages[@]}"; do
        echo -e "  - ${pkg}"
    done

    local snap_file="${SNAPSHOTS_DIR}/${feature}.snap"
    mkdir -p "$BACKUP_DIR"
    mkdir -p "$SNAPSHOTS_DIR"

    # Step 1: Backup existing snapshot if present
    local has_backup=false
    if [[ -f "$snap_file" ]]; then
        cp "$snap_file" "${BACKUP_DIR}/${feature}.snap"
        has_backup=true
        echo -e "${CYAN}Backed up existing snapshot to temporary storage.${NC}"
    fi

    # Step 2: Clean target snapshot to eliminate orphaned blocks
    rm -f "$snap_file"

    # Determine GOMODCACHE dynamically
    local gomodcache_val
    local gopath_val
    gopath_val="$(go env GOPATH 2>/dev/null || echo "$HOME/go")"
    if [[ -d "${HOME}/Documents/go/pkg/mod" ]]; then
        gomodcache_val="${HOME}/Documents/go/pkg/mod"
    elif [[ -n "${GOMODCACHE:-}" ]]; then
        gomodcache_val="${GOMODCACHE}"
    elif [[ -n "$gopath_val" && -d "${gopath_val}/pkg/mod" ]]; then
        gomodcache_val="${gopath_val}/pkg/mod"
    else
        gomodcache_val="$(go env GOMODCACHE)"
    fi

    # Ensure compatible go toolchain is in PATH and GOROOT if needed
    local latest_tc
    latest_tc="$(ls -d "${gomodcache_val}/golang.org/toolchain@v0.0.1-go"*"/bin" 2>/dev/null | sort -V | tail -n 1)"
    if [[ -n "$latest_tc" && -d "$latest_tc" ]]; then
        export PATH="$latest_tc:$PATH"
        export GOROOT="$(dirname "$latest_tc")"
    fi

    # Step 3: Run tests with UPDATE_SNAPS=true (use -p 1 to avoid gomonkey cross-package patch races)
    echo -e "${CYAN}Regenerating snapshots cleanly across packages...${NC}"
    set +e
    UPDATE_SNAPS=true GOMODCACHE="${gomodcache_val}" go test -p 1 -gcflags=all=-l "${packages[@]}"
    local test_exit_code=$?
    set -e

    # Step 4: Handle failure / rollback
    if [[ $test_exit_code -ne 0 ]]; then
        echo -e "\n${RED}${BOLD}❌ Test execution failed for feature \"${feature}\"!${NC}"
        if [[ "$has_backup" == "true" ]]; then
            echo -e "${YELLOW}Rolling back ${snap_file} to previous state...${NC}"
            cp "${BACKUP_DIR}/${feature}.snap" "$snap_file"
        fi
        return $test_exit_code
    fi

    echo -e "${GREEN}${BOLD}✓ Feature \"${feature}\" snapshot cleanly generated without orphan blocks.${NC}"
    return 0
}

# Main Execution Flow
if [[ "$RUN_ALL" == "true" ]]; then
    echo -e "${BLUE}${BOLD}===> Updating all feature snapshots with automatic block GC <===${NC}"
    all_features=()
    while IFS= read -r feat; do
        [[ -n "$feat" ]] && all_features+=("$feat")
    done < <(discover_all_features)

    if [[ ${#all_features[@]} -eq 0 ]]; then
        echo -e "${YELLOW}No features found referencing TestGuard in codebase.${NC}"
        exit 0
    fi

    failed_features=()
    for feat in "${all_features[@]}"; do
        if ! update_single_feature "$feat"; then
            failed_features+=("$feat")
        fi
    done

    if [[ ${#failed_features[@]} -gt 0 ]]; then
        echo -e "\n${RED}${BOLD}Failed to update snapshots for features: ${failed_features[*]}${NC}"
        exit 1
    fi
    echo -e "\n${GREEN}${BOLD}🎉 All feature snapshots successfully updated with 0 orphan blocks!${NC}"
elif [[ -n "${TARGET_FEATURE}" ]]; then
    update_single_feature "${TARGET_FEATURE}"
else
    echo -e "${YELLOW}Usage:${NC}"
    echo -e "  make test-snaps-update FEATURE=<feature_name>   # Clean & update specific feature"
    echo -e "  make test-snaps-update ALL=true                # Clean & update all feature snapshots"
    echo -e "\nAvailable Features in Codebase:"
    discover_all_features | while IFS= read -r f; do
        echo -e "  - $f"
    done
    exit 0
fi
