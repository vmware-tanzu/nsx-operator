#!/usr/bin/env bash

# ✓ NSX Operator Test Runner - Clean and Simple! ✓
# This script runs tests with visual progress indicators
# Testing made clear and efficient! 🎯

set -euo pipefail

# 🌈 Colors and effects
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly PURPLE='\033[0;35m'
readonly CYAN='\033[0;36m'
readonly WHITE='\033[1;37m'
readonly BOLD='\033[1m'
readonly DIM='\033[2m'
readonly BLINK='\033[5m'
readonly NC='\033[0m' # No Color

# ✓ Simple and clean icons
readonly CHECK='✓'
readonly CROSS='✗'
readonly PARTY='🎉'
readonly ROCKET='🚀'
readonly FIRE='🔥'
readonly THUNDER='⚡'
readonly SKULL='💀'
readonly SAD='😢'
readonly COOL='😎'
readonly NERD='🤓'
readonly ROBOT='🤖'

# Progress bar variables
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TEST_COUNT=0
PROGRESS_WIDTH=50
CURRENT_PACKAGE=""
PACKAGE_COUNT=0
TOTAL_PACKAGES=0

# ✓ Clean functions for clear feedback! ✓

# Success celebration animation
success_celebration() {
    echo -e "\n${YELLOW}${BLINK}🎉 SUCCESS! 🎉${NC}"
    for i in {1..3}; do
        echo -ne "\r${GREEN}${CHECK} ${PARTY} ${CHECK} ${ROCKET} ${CHECK} ${PARTY} ${CHECK}"
        sleep 0.3
        echo -ne "\r${YELLOW}${PARTY} ${CHECK} ${ROCKET} ${CHECK} ${PARTY} ${CHECK} ${ROCKET}"
        sleep 0.3
    done
    echo -e "\n${GREEN}${BOLD}All tests passed! Mission accomplished! ${CHECK}${NC}\n"
}

# Failure notification animation
failure_animation() {
    echo -e "\n${RED}${BLINK}💀 TEST FAILURES 💀${NC}"
    for i in {1..2}; do
        echo -ne "\r${RED}${CROSS} ${SAD} ${CROSS} Tests failed... ${CROSS} ${SAD} ${CROSS}"
        sleep 0.5
        echo -ne "\r${DIM}${CROSS} ${SAD} ${CROSS} Please fix and retry... ${CROSS} ${SAD} ${CROSS}${NC}"
        sleep 0.5
    done
    echo -e "\n${RED}${BOLD}Some tests failed. Please fix them and retry! ${CROSS}${NC}\n"
}

# Progress bar with checkmarks!
show_progress() {
    local current="$1"
    local total="$2"
    local percentage=$((current * 100 / total))
    local filled=$((current * PROGRESS_WIDTH / total))
    local empty=$((PROGRESS_WIDTH - filled))

    printf "\r${CYAN}${ROBOT} Progress: ["
    printf "%*s" "$filled" | tr ' ' '✓'
    printf "%*s" "$empty" | tr ' ' '░'
    printf "] %d%% (%d/%d) ${ROCKET}${NC}" "$percentage" "$current" "$total"
}

# Package header with style
show_package_header() {
    local package="$1"
    local count="$2"
    local total="$3"
    echo -e "\n${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}${NERD} Testing package ${count}/${total}: ${WHITE}$(basename "$package")${NC}"
    echo -e "${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

# Package progress display
show_package_progress() {
    local package="$1"
    local status="$2"
    local pkg_name="$(basename "$package")"

    if [[ "$status" == "PASSED" ]]; then
        echo -e "${GREEN}${CHECK} PASS${NC} ${pkg_name} ${FIRE}"
    else
        echo -e "${RED}${CROSS} FAIL${NC} ${pkg_name} ${SAD}"
    fi
}

# Spinning loader animation
spinner() {
    local pid="$1"
    local delay=0.1
    local spinstr='✓◐◑◒'
    while ps -p "$pid" > /dev/null 2>&1; do
        local temp=${spinstr#?}
        printf "\r${YELLOW} [%c] Processing tests...${NC}" "$spinstr"
        spinstr=$temp${spinstr%"$temp"}
        sleep $delay
    done
    printf "\r"
}

# Clean opening banner! 🚀
echo -e "${CYAN}${BOLD}"
echo "  ✓✓✓ NSX OPERATOR TEST RUNNER ✓✓✓"
echo "  🎯 Clean, Fast, and Reliable Testing! 🎯"
echo "  🚀 Let's ensure code quality! 🚀"
echo -e "${NC}"

for i in {1..3}; do
    echo -ne "\r${YELLOW}  ✓ Preparing test environment..."
    sleep 0.2
    echo -ne "\r${GREEN}  ◐ Preparing test environment..."
    sleep 0.2
    echo -ne "\r${BLUE}  ◑ Preparing test environment..."
    sleep 0.2
done
echo -e "\r${GREEN}  ${ROCKET} Test environment ready! Let's go!${NC}\n"

export GOARCH=amd64
export KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-bin/k8s/1.28.0-darwin-arm64/}"  # darwin is used to test on M1 Macs

# Enable clean test mode to suppress verbose logging
export NSX_TEST_CLEAN_MODE=true

# Set default parallelism for tests (can be overridden with PARALLEL env var)
PARALLEL="${PARALLEL:-4}"  # Default to 4 parallel tests

echo -e "${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${NERD} Configuration: GOARCH=amd64, KUBEBUILDER_ASSETS=$KUBEBUILDER_ASSETS"
echo -e "${THUNDER} Parallelism: ${PARALLEL} tests in parallel"
echo -e "${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"

# Create coverage directory if it doesn't exist
mkdir -p .coverage

# Get the package list exactly as in the user's rule
PACKAGES="$(go list ./... | grep -v mock | grep -v e2e | grep -v hack)"
COVERPKG="$(echo "$PACKAGES" | tr '\n' ',' | sed 's/,$//')"

echo ""

# Create temporary files to capture output and errors
TEMP_OUTPUT="$(mktemp)"
TEMP_ERRORS="$(mktemp)"
FAILED_TESTS=()
FAILURE_DETAILS=""

# Run the exact command from user's rule but capture output
echo -e "${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo "Executing complete test command:"
echo "GOARCH=amd64 KUBEBUILDER_ASSETS=${KUBEBUILDER_ASSETS} go test -gcflags=all=-l \\"
echo "  -coverpkg=\"${COVERPKG}\" \\"
echo "  -covermode=atomic \\"
echo "  -parallel=${PARALLEL} \\"
echo "  -v -coverprofile $(pwd)/.coverage/coverage-unit.out "
echo -e "${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo "  Testing packages:"
echo "${PACKAGES}" | sed 's/^/    /'
echo -e "${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Execute the command and capture both output and errors
if GOARCH=amd64 KUBEBUILDER_ASSETS="$KUBEBUILDER_ASSETS" go test -gcflags=all=-l \
    -coverpkg="$COVERPKG" -covermode=atomic -parallel="$PARALLEL" \
    ${PACKAGES} -v -coverprofile "$(pwd)/.coverage/coverage-unit.out" 2>&1 | \
    tee "$TEMP_OUTPUT" | \
    while IFS= read -r line; do
        # Capture failure details
        if [[ "$line" == *"--- FAIL:"* ]] || [[ "$line" == *"FAIL"* ]] || [[ "$line" == *"fatal error:"* ]] || [[ "$line" == *"panic:"* ]]; then
            echo "$line" >> "$TEMP_ERRORS"
        fi

        # Filter out noise and show only important lines
        case "$line" in
            *"=== RUN "*)
                # Count total tests and show test start (optional)
                ((TOTAL_TESTS++))
                # Uncomment to show test start
                # echo -e "${BLUE}${line}${NC}"
                ;;
            *"--- PASS:"*)
                # Show passed tests with checkmark ✓
                test_name="$(echo "$line" | sed 's/--- PASS: //' | awk '{print $1}')"
                echo -e "${GREEN}${CHECK}${NC} $test_name"
                ((PASSED_TESTS++))
                ;;
            *"--- FAIL:"*)
                # Show failed tests with cross ✗
                test_name="$(echo "$line" | sed 's/--- FAIL: //' | awk '{print $1}')"
                echo -e "${RED}${CROSS}${NC} $test_name"
                FAILED_TESTS+=("$test_name")
                ((FAILED_TEST_COUNT++))
                ;;
            *"PASS"*|*"ok "*)
                # Show package pass summary with celebration
                if [[ "$line" == *"ok "* ]]; then
                    pkg="$(echo "$line" | awk '{print $2}')"
                    ((PACKAGE_COUNT++))
                    show_package_progress "$pkg" "PASSED"
                fi
                ;;
            *"FAIL"*)
                # Show package fail summary with sadness
                if [[ "$line" == *"FAIL"* ]] && [[ "$line" != *"--- FAIL:"* ]]; then
                    pkg="$(echo "$line" | awk '{print $2}')"
                    ((PACKAGE_COUNT++))
                    show_package_progress "$pkg" "FAILED"
                fi
                ;;
            *"panic:"*|*"fatal error:"*|*"runtime error:"*)
                # Show critical errors
                echo -e "${RED}ERROR: $line${NC}"
                ;;
            *"coverage:"*)
                # Suppress individual package coverage during test run
                # We'll show total coverage at the end
                ;;
            *)
                # Suppress other output (logs, stack traces, etc.)
                # Uncomment the next line to see all output for debugging:
                # echo "$line"
                ;;
        esac
    done; then
    echo ""

    # Success celebration! 🎉
    success_celebration

    echo -e "${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}${CHECK} ALL TESTS COMPLETED SUCCESSFULLY! ${CHECK}${NC}"
    echo -e "${GREEN}${ROCKET} Coverage report saved to .coverage/coverage-unit.out ${ROCKET}${NC}"

    # Calculate total coverage with cool animation
    echo -ne "${YELLOW}${NERD} Calculating total coverage..."
    for i in {1..3}; do
        sleep 0.2
        echo -ne "."
    done
    echo ""

    TOTAL_COVERAGE="$(go tool cover -func=.coverage/coverage-unit.out | grep total | awk '{print $3}')"
    echo -e "${CYAN}${FIRE} Total Test Coverage: ${WHITE}${BOLD}${TOTAL_COVERAGE}${NC} ${FIRE}"

    # Coverage feedback based on percentage
    coverage_num="$(echo "${TOTAL_COVERAGE}" | sed 's/%//')"
    if (( $(echo "${coverage_num} >= 80" | bc -l) )); then
        echo -e "\n${GREEN}${BLINK}${FIRE} EXCELLENT COVERAGE! Outstanding work! ${FIRE}${NC}"
        echo -e "${CHECK}${CHECK}${CHECK} ${PARTY}${PARTY}${PARTY} ${ROCKET}${ROCKET}${ROCKET}"
    elif (( $(echo "${coverage_num} >= 60" | bc -l) )); then
        echo -e "\n${YELLOW}${FIRE} Good coverage! Well done! ${CHECK}${NC}"
    else
        echo -e "\n${YELLOW}${NERD} Coverage could be better, but good progress! ${CHECK}${NC}"
    fi

    echo -e "${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"

    # Clean up temp files
    rm -f "$TEMP_OUTPUT" "$TEMP_ERRORS"
    exit 0
else
    exit_code=$?
    echo ""

    # Show failure animation
    failure_animation

    echo -e "${PURPLE}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${RED}${CROSS} Some tests failed (exit code: ${exit_code}) ${CROSS}${NC}"

    # Show detailed failure information
    if [[ -s "$TEMP_ERRORS" ]]; then
        echo ""
        echo -e "${RED}${BOLD}━━━━━━━━━━━━ FAILURE DETAILS ━━━━━━━━━━━━${NC}"
        echo ""

        # Extract and show detailed failure information from the full output
        if [[ -s "$TEMP_OUTPUT" ]]; then
            # Look for panic, fatal error, and stack trace information
            grep -A 20 -B 5 "fatal error\|panic\|FAIL" "$TEMP_OUTPUT" | \
            grep -v "^--$" | \
            while IFS= read -r line; do
                if [[ "$line" == *"fatal error:"* ]] || [[ "$line" == *"panic:"* ]]; then
                    echo -e "${RED}${line}${NC}"
                elif [[ "$line" == *"FAIL"* ]]; then
                    echo -e "${RED}${line}${NC}"
                elif [[ "$line" == *"goroutine"* ]] || [[ "$line" == *".go:"* ]]; then
                    echo -e "${YELLOW}${line}${NC}"
                else
                    echo "${line}"
                fi
            done
        fi

        echo ""
        echo -e "${RED}${BOLD}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    fi

    # Clean up temp files
    rm -f "$TEMP_OUTPUT" "$TEMP_ERRORS"
    exit "${exit_code}"
fi
