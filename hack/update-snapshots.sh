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
BACKUP_READY=false

# Packages can write several features, so protect the entire snapshot set for
# the whole invocation (including all iterations of --all).
backup_snapshots() {
    [[ "$BACKUP_READY" == "true" ]] && return 0
    mkdir -p "$BACKUP_DIR" "$SNAPSHOTS_DIR" || return 1
    local snapshot
    for snapshot in "$SNAPSHOTS_DIR"/*.snap; do
        [[ -f "$snapshot" ]] || continue
        cp -p "$snapshot" "$BACKUP_DIR/" || return 1
    done
    BACKUP_READY=true
}

restore_snapshots() {
    local snapshot
    # Remove newly generated snapshots as well as partially rewritten ones.
    for snapshot in "$SNAPSHOTS_DIR"/*.snap; do
        [[ -f "$snapshot" ]] || continue
        rm -f "$snapshot" || return 1
    done
    for snapshot in "$BACKUP_DIR"/*.snap; do
        [[ -f "$snapshot" ]] || continue
        cp -p "$snapshot" "$SNAPSHOTS_DIR/" || return 1
    done
}

cleanup_temp() {
    local exit_code=$?
    if [[ $exit_code -ne 0 && "$BACKUP_READY" == "true" ]]; then
        echo -e "${YELLOW}Rolling back all snapshots to their pre-run state...${NC}"
        if ! restore_snapshots; then
            echo -e "${RED}Rollback failed; backups retained at ${BACKUP_DIR}.${NC}" >&2
            exit 1
        fi
    fi
    if [[ -d "$BACKUP_DIR" ]]; then
        rm -rf "$BACKUP_DIR"
    fi
    exit "$exit_code"
}
trap cleanup_temp EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

TARGET_FEATURE="${1:-${FEATURE:-}}"
RUN_ALL=false

if [[ "${TARGET_FEATURE}" == "--all" || "${ALL:-false}" == "true" ]]; then
    RUN_ALL=true
fi

# Locate all referenced features in the repo
discover_all_features() {
    if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        git grep --untracked --exclude-standard -E 'Feature:[[:space:]]*"[^"]+"' -- '*.go' | \
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
        done < <(git grep --untracked --exclude-standard -l -E "$pattern" -- '*.go' || true)
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
    # Respect Go's environment, persisted configuration and default cache path.
    local gomodcache_val
    gomodcache_val="$(go env GOMODCACHE)" || return 1

    # Ensure compatible go toolchain is in PATH and GOROOT if needed
    local latest_tc
    latest_tc="$(ls -d "${gomodcache_val}/golang.org/toolchain@v0.0.1-go"*"/bin" 2>/dev/null | sort -V | tail -n 1 || true)"
    if [[ -n "$latest_tc" && -d "$latest_tc" ]]; then
        export PATH="$latest_tc:$PATH"
        export GOROOT="$(dirname "$latest_tc")"
    fi

    # Back up before the first mutation, then remove obsolete target blocks.
    backup_snapshots || return 1
    rm -f "$snap_file" || return 1

    # Run every test package serially and bypass cached results when regenerating.
    echo -e "${CYAN}Regenerating snapshots cleanly across packages...${NC}"
    local test_exit_code=0
    UPDATE_SNAPS=true GOMODCACHE="${gomodcache_val}" go test -p 1 -count=1 -gcflags=all=-l "${packages[@]}" || test_exit_code=$?

    # Step 4: Handle failure / rollback
    if [[ $test_exit_code -ne 0 ]]; then
        echo -e "\n${RED}${BOLD}❌ Test execution failed for feature \"${feature}\"!${NC}"
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
