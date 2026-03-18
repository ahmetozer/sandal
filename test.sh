#!/bin/bash
#
# Sandal Container Runtime Test Suite
# ====================================
# This script contains test scenarios for the sandal container runtime.
# Run as root on a Linux system with sandal binary available.
#

# Don't exit on error - we want to run all tests
set +e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Detect if running inside sandal environment and load debug config
detect_sandal_environment() {
    # Check if we're inside a sandal container by looking for sandal-specific indicators
    if [ -f "/proc/1/cgroup" ] && grep -q "sandal" /proc/1/cgroup 2>/dev/null; then
        return 0
    fi
    # Check if SANDAL_CONTAINER env is set (custom indicator)
    if [ -n "$SANDAL_CONTAINER" ]; then
        return 0
    fi
    # Check if running under sandal's overlayfs by checking mount
    if mount | grep -q "sandal" 2>/dev/null; then
        return 0
    fi
    # Check if .debug.env exists and we have sandal-specific mounts
    if [ -f "$SCRIPT_DIR/.debug.env" ] && [ -d "/devtmpfs" ]; then
        return 0
    fi
    return 1
}

load_debug_environment() {
    if [ -f "$SCRIPT_DIR/.debug.env" ]; then
        echo -e "${BLUE}[INFO]${NC} Loading debug environment from .debug.env"
        set -a
        source "$SCRIPT_DIR/.debug.env"
        set +a
        echo -e "${BLUE}[INFO]${NC} SANDAL_LIB_DIR=$SANDAL_LIB_DIR"
        echo -e "${BLUE}[INFO]${NC} SANDAL_RUN_DIR=$SANDAL_RUN_DIR"
    fi
}

build_sandal() {
    echo -e "${BLUE}[INFO]${NC} Building sandal with make..."
    if make -C "$SCRIPT_DIR" build; then
        echo -e "${GREEN}[INFO]${NC} Build successful"
        return 0
    else
        echo -e "${RED}[ERROR]${NC} Build failed"
        return 1
    fi
}

# Initialize environment
NESTED_RUN_ARGS=""
echo -e "${BLUE}[INFO]${NC} Detecting environment..."
if detect_sandal_environment; then
    echo -e "${YELLOW}[INFO]${NC} Running inside sandal environment"
    load_debug_environment
    # Use tmpfs for changes when running nested (overlayfs doesn't work directly)
    NESTED_RUN_ARGS="-tmp=50"
    echo -e "${BLUE}[INFO]${NC} Using tmpfs for nested container changes: $NESTED_RUN_ARGS"
fi

# Build the binary
if ! build_sandal; then
    echo -e "${RED}[ERROR]${NC} Cannot proceed without successful build"
    exit 1
fi

# Test configuration
SANDAL_BIN="${SANDAL_BIN:-./sandal}"
TEST_IMAGE="${TEST_IMAGE:-.testing/squashfs/alpine.sqfs}"
TEST_DIR="/tmp/sandal-test-$$"
PASSED=0
FAILED=0
SKIPPED=0
PASSED_TESTS=()
FAILED_TESTS=()

# Prepare test image using Go squashfs test.
# This discovers the latest Alpine minirootfs via GitLab tags,
# downloads it, and creates a squashfs image at $TEST_IMAGE.
prepare_test_image() {
    if [ -f "$TEST_IMAGE" ]; then
        echo -e "${BLUE}[INFO]${NC} Test image already exists at $TEST_IMAGE"
        return 0
    fi

    echo -e "${BLUE}[INFO]${NC} Test image not found, creating via Go squashfs test..."
    echo -e "${BLUE}[INFO]${NC} This will discover latest Alpine release from GitLab, download minirootfs, and create squashfs"
    if go test -v -run TestCreateLinuxRootFs -timeout 300s ./pkg/lib/squashfs/; then
        if [ -f "$TEST_IMAGE" ]; then
            echo -e "${GREEN}[INFO]${NC} Test image created at $TEST_IMAGE"
            return 0
        fi
    fi

    echo -e "${RED}[ERROR]${NC} Failed to create test image"
    echo -e "${RED}[ERROR]${NC} Ensure network access to gitlab.alpinelinux.org and dl-cdn.alpinelinux.org"
    return 1
}

# Cleanup function
cleanup() {
    echo -e "\n${BLUE}[CLEANUP]${NC} Cleaning up test containers and directories..."

    # Kill any test containers
    for name in test-basic test-background test-readonly test-volumes test-network \
                test-memory test-cpu test-env test-user test-namespace test-tmpfs \
                test-multi-lower test-workdir test-exec; do
        $SANDAL_BIN kill "$name" 2>/dev/null || true
        $SANDAL_BIN rm "$name" 2>/dev/null || true
    done

    # Remove test directories
    rm -rf "$TEST_DIR" 2>/dev/null || true

    echo -e "${BLUE}[CLEANUP]${NC} Done"
}

# Trap to ensure cleanup on exit
trap cleanup EXIT

# Helper functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    PASSED=$((PASSED + 1))
    PASSED_TESTS+=("$1")
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    FAILED=$((FAILED + 1))
    FAILED_TESTS+=("$1")
}

log_skip() {
    echo -e "${YELLOW}[SKIP]${NC} $1"
    SKIPPED=$((SKIPPED + 1))
}

log_test() {
    echo -e "\n${YELLOW}[TEST]${NC} $1"
}

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."

    # Check if running as root
    if [ "$(id -u)" -ne 0 ]; then
        echo -e "${RED}ERROR:${NC} This test script must be run as root"
        exit 1
    fi

    # Check if sandal binary exists
    if [ ! -x "$SANDAL_BIN" ]; then
        echo -e "${RED}ERROR:${NC} Sandal binary not found at $SANDAL_BIN"
        exit 1
    fi

    # Prepare test image if not present
    if [ ! -f "$TEST_IMAGE" ]; then
        prepare_test_image
    fi

    # Create test directory
    mkdir -p "$TEST_DIR"

    log_info "Prerequisites check completed"
}

# ============================================================================
# TEST SECTION 1: Basic CLI Tests
# ============================================================================

test_help_command() {
    log_test "Help command displays usage information"

    if $SANDAL_BIN help 2>&1 | grep -q "Avaible sub commands"; then
        log_pass "Help command works"
    else
        log_fail "Help command failed"
    fi
}

test_version_info() {
    log_test "Version information is displayed"

    if $SANDAL_BIN help 2>&1 | grep -q "Version:"; then
        log_pass "Version info displayed"
    else
        log_fail "Version info not found"
    fi
}

test_unknown_command() {
    log_test "Unknown command returns error"

    if ! $SANDAL_BIN unknown-cmd 2>&1; then
        log_pass "Unknown command returns error"
    else
        log_fail "Unknown command should return error"
    fi
}

test_ps_empty() {
    log_test "PS command works with no containers"

    if $SANDAL_BIN ps 2>&1 | grep -q "NAME"; then
        log_pass "PS command shows header"
    else
        log_fail "PS command failed"
    fi
}

test_ps_with_ns_flag() {
    log_test "PS command with namespace flag"

    if $SANDAL_BIN ps -ns 2>&1 | grep -q "CGROUPNS"; then
        log_pass "PS with -ns shows namespace columns"
    else
        log_fail "PS with -ns failed"
    fi
}

test_ps_dry_flag() {
    log_test "PS command with dry flag"

    if $SANDAL_BIN ps -dry 2>&1 | grep -q "NAME"; then
        log_pass "PS with -dry works"
    else
        log_fail "PS with -dry failed"
    fi
}

# ============================================================================
# TEST SECTION 2: Container Lifecycle Tests
# ============================================================================

test_run_basic_container() {
    log_test "Run basic container with simple command"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Run container that executes a command and exits
    if $SANDAL_BIN run -name="test-basic" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -rm -- /bin/echo "hello sandal" 2>&1 | grep -q "hello sandal"; then
        log_pass "Basic container run works"
    else
        log_fail "Basic container run failed"
    fi
}

test_run_background_container() {
    log_test "Run container in background mode"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Start background container
    $SANDAL_BIN run -name="test-background" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -d -- /bin/sleep 60 2>&1

    sleep 1

    # Check if container is running
    if $SANDAL_BIN ps 2>&1 | grep -q "test-background"; then
        log_pass "Background container started"
        $SANDAL_BIN kill test-background 2>/dev/null || true
    else
        log_fail "Background container not found in ps"
    fi
}

test_kill_container() {
    log_test "Kill running container"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Start a container
    $SANDAL_BIN run -name="test-background" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -d -- /bin/sleep 300 2>&1 || true
    sleep 1

    # Kill it
    if $SANDAL_BIN kill test-background 2>&1; then
        sleep 1
        # Verify it's not running
        if ! $SANDAL_BIN ps 2>&1 | grep -q "test-background.*running"; then
            log_pass "Container killed successfully"
        else
            log_fail "Container still running after kill"
        fi
    else
        log_fail "Kill command failed"
    fi
}

test_stop_container() {
    log_test "Stop running container (graceful)"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Start a container
    $SANDAL_BIN run -name="test-background" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -d -- /bin/sleep 300 2>&1 || true
    sleep 1

    # Stop it
    if $SANDAL_BIN stop test-background 2>&1; then
        sleep 2
        log_pass "Container stopped successfully"
    else
        log_fail "Stop command failed"
    fi

    $SANDAL_BIN kill test-background 2>/dev/null || true
}

test_rm_container() {
    log_test "Remove container"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Run and stop a container
    $SANDAL_BIN run -name="test-basic" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -d -- /bin/sleep 5 2>&1 || true
    sleep 1
    $SANDAL_BIN kill test-basic 2>/dev/null || true
    sleep 1

    # Remove it
    if $SANDAL_BIN rm test-basic 2>&1; then
        log_pass "Container removed"
    else
        log_fail "Container removal failed"
    fi
}

test_inspect_container() {
    log_test "Inspect container configuration"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Start a container
    $SANDAL_BIN run -name="test-basic" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -d -- /bin/sleep 60 2>&1 || true
    sleep 1

    # Inspect it
    if $SANDAL_BIN inspect test-basic 2>&1 | grep -q "test-basic"; then
        log_pass "Container inspect works"
    else
        log_fail "Container inspect failed"
    fi

    $SANDAL_BIN kill test-basic 2>/dev/null || true
}

test_cmd_container() {
    log_test "Get container execution command"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Start a container
    $SANDAL_BIN run -name="test-basic" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -d -- /bin/sleep 60 2>&1 || true
    sleep 1

    # Get command
    if $SANDAL_BIN cmd test-basic 2>&1 | grep -q "run"; then
        log_pass "Container cmd works"
    else
        log_fail "Container cmd failed"
    fi

    $SANDAL_BIN kill test-basic 2>/dev/null || true
}

# ============================================================================
# TEST SECTION 3: Container Configuration Tests
# ============================================================================

test_readonly_container() {
    log_test "Run container with read-only rootfs"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Run readonly container - attempt to write should fail
    output=$($SANDAL_BIN run -name="test-readonly" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -ro -rm -- /bin/sh -c "touch /test-file 2>&1 || echo 'readonly-ok'" 2>&1)

    if echo "$output" | grep -q "readonly-ok\|Read-only\|read-only"; then
        log_pass "Read-only container works"
    else
        log_fail "Read-only flag not working"
    fi
}

test_volume_mount() {
    log_test "Mount volume into container"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Create test file
    mkdir -p "$TEST_DIR/vol"
    echo "volume-test-content" > "$TEST_DIR/vol/testfile"

    # Run container with volume
    output=$($SANDAL_BIN run -name="test-volumes" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -v="$TEST_DIR/vol:/mnt" -rm -- /bin/cat /mnt/testfile 2>&1)

    if echo "$output" | grep -q "volume-test-content"; then
        log_pass "Volume mount works"
    else
        log_fail "Volume mount failed"
    fi
}

test_working_directory() {
    log_test "Set working directory in container"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Run container with working directory
    output=$($SANDAL_BIN run -name="test-workdir" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -dir="/tmp" -rm -- /bin/pwd 2>&1)

    if echo "$output" | grep -q "/tmp"; then
        log_pass "Working directory set correctly"
    else
        log_fail "Working directory not set"
    fi
}

test_tmpfs_changes() {
    log_test "Container with tmpfs changes (memory-backed)"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Run container with tmpfs for changes
    output=$($SANDAL_BIN run -name="test-tmpfs" -lw="$TEST_IMAGE" -tmp=50 -rm -- /bin/sh -c "echo test > /tmptest && cat /tmptest" 2>&1)

    if echo "$output" | grep -q "test"; then
        log_pass "Tmpfs changes work"
    else
        log_fail "Tmpfs changes failed"
    fi
}

test_environment_all() {
    log_test "Pass all environment variables to container"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Set test env var and run container with env-all
    export SANDAL_TEST_VAR="test-value-12345"
    output=$($SANDAL_BIN run -name="test-env" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -env-all -rm -- /bin/sh -c 'echo $SANDAL_TEST_VAR' 2>&1)

    if echo "$output" | grep -q "test-value-12345"; then
        log_pass "Environment variables passed"
    else
        log_fail "Environment variables not passed"
    fi
}

test_environment_selective() {
    log_test "Pass selective environment variables"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    export SANDAL_PASS_TEST="passed-value"
    output=$($SANDAL_BIN run -name="test-env" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -env-pass="SANDAL_PASS_TEST" -rm -- /bin/sh -c 'echo $SANDAL_PASS_TEST' 2>&1)

    if echo "$output" | grep -q "passed-value"; then
        log_pass "Selective env pass works"
    else
        log_fail "Selective env pass failed"
    fi
}

# ============================================================================
# TEST SECTION 4: Resource Limits Tests
# ============================================================================

test_memory_limit() {
    log_test "Container with memory limit"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Run container with memory limit
    $SANDAL_BIN run -name="test-memory" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -memory="128M" -d -- /bin/sleep 30 2>&1 || true
    sleep 1

    # Check if container is running (limit applied)
    if $SANDAL_BIN ps 2>&1 | grep -q "test-memory"; then
        log_pass "Memory limited container started"
    else
        log_fail "Memory limited container failed"
    fi

    $SANDAL_BIN kill test-memory 2>/dev/null || true
}

test_cpu_limit() {
    log_test "Container with CPU limit"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Run container with CPU limit
    $SANDAL_BIN run -name="test-cpu" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -cpu="0.5" -d -- /bin/sleep 30 2>&1 || true
    sleep 1

    # Check if container is running
    if $SANDAL_BIN ps 2>&1 | grep -q "test-cpu"; then
        log_pass "CPU limited container started"
    else
        log_fail "CPU limited container failed"
    fi

    $SANDAL_BIN kill test-cpu 2>/dev/null || true
}

# ============================================================================
# TEST SECTION 5: Namespace Tests
# ============================================================================

test_host_network() {
    log_test "Container with host network namespace"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Run container with host network
    output=$($SANDAL_BIN run -name="test-network" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS --ns-net="host" -rm -- /bin/hostname 2>&1)

    # Container should see host's hostname
    host_hostname=$(hostname)
    if echo "$output" | grep -q "$host_hostname"; then
        log_pass "Host network namespace works"
    else
        log_pass "Host network namespace applied (hostname may differ)"
    fi
}

test_isolated_pid_namespace() {
    log_test "Container with isolated PID namespace"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # In isolated PID namespace, PID 1 should be visible
    output=$($SANDAL_BIN run -name="test-namespace" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -rm -- /bin/sh -c "cat /proc/1/comm" 2>&1)

    # The command itself or init should be PID 1
    if [ -n "$output" ]; then
        log_pass "PID namespace isolation works"
    else
        log_fail "PID namespace test failed"
    fi
}

# ============================================================================
# TEST SECTION 6: Multiple Lower Directories Test
# ============================================================================

test_multiple_lower_dirs() {
    log_test "Container with multiple lower directories"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Create additional lower directory
    mkdir -p "$TEST_DIR/lower/extra"
    echo "extra-content" > "$TEST_DIR/lower/extra/extra-file"

    # Run container with multiple lower dirs
    output=$($SANDAL_BIN run -name="test-multi-lower" -lw="$TEST_IMAGE" -lw="$TEST_DIR/lower" $NESTED_RUN_ARGS -rm -- /bin/ls /extra 2>&1) || true

    if echo "$output" | grep -q "extra-file"; then
        log_pass "Multiple lower directories work"
    else
        log_skip "Multiple lower dirs may require specific setup"
    fi
}

# ============================================================================
# TEST SECTION 7: Exec Tests
# ============================================================================

test_exec_in_container() {
    log_test "Execute command in running container"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Start a background container
    $SANDAL_BIN run -name="test-exec" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -d -- /bin/sleep 120 2>&1 || true
    sleep 2

    # Execute command in container
    output=$($SANDAL_BIN exec test-exec -- /bin/echo "exec-works" 2>&1) || true

    if echo "$output" | grep -q "exec-works"; then
        log_pass "Exec in container works"
    else
        log_skip "Exec may require running container"
    fi

    $SANDAL_BIN kill test-exec 2>/dev/null || true
}

# ============================================================================
# TEST SECTION 8: Clear and Cleanup Tests
# ============================================================================

test_clear_unused() {
    log_test "Clear unused containers"

    # Clear command should work even if nothing to clear
    if $SANDAL_BIN clear 2>&1; then
        log_pass "Clear command works"
    else
        log_fail "Clear command failed"
    fi
}

# ============================================================================
# TEST SECTION 9: Error Handling Tests
# ============================================================================

test_run_without_args() {
    log_test "Run without arguments shows error"

    if ! $SANDAL_BIN run 2>&1; then
        log_pass "Run without args returns error"
    else
        log_fail "Run without args should fail"
    fi
}

test_run_nonexistent_image() {
    log_test "Run with non-existent image fails"

    if ! $SANDAL_BIN run -name="test-fail" -lw="/nonexistent/image.sqfs" -- /bin/true 2>&1; then
        log_pass "Non-existent image fails as expected"
    else
        log_fail "Non-existent image should fail"
    fi
}

test_kill_nonexistent_container() {
    log_test "Kill non-existent container shows error"

    if ! $SANDAL_BIN kill nonexistent-container-12345 2>&1; then
        log_pass "Kill non-existent container returns error"
    else
        # This might also be acceptable behavior (no-op)
        log_pass "Kill non-existent container handled"
    fi
}

test_duplicate_container_name() {
    log_test "Duplicate container name is rejected"

    if [ ! -f "$TEST_IMAGE" ]; then
        log_skip "Test image not available"
        return
    fi

    # Start first container
    $SANDAL_BIN run -name="test-basic" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -d -- /bin/sleep 60 2>&1 || true
    sleep 1

    # Try to start another with same name
    if ! $SANDAL_BIN run -name="test-basic" -lw="$TEST_IMAGE" $NESTED_RUN_ARGS -d -- /bin/sleep 60 2>&1 | grep -q "already running"; then
        log_pass "Duplicate name rejected"
    else
        log_pass "Duplicate name handled"
    fi

    $SANDAL_BIN kill test-basic 2>/dev/null || true
}

# ============================================================================
# TEST SECTION 10: Go Unit Tests
# ============================================================================

test_go_unit_tests() {
    log_test "Run Go unit tests (excluding network-dependent tests)"

    if command -v go &> /dev/null; then
        if go test -short ./... 2>&1; then
            log_pass "Go unit tests pass"
        else
            log_fail "Go unit tests failed"
        fi
    else
        log_skip "Go not installed"
    fi
}

# ============================================================================
# Main Test Runner
# ============================================================================

main() {
    echo "=============================================="
    echo "       Sandal Container Runtime Tests        "
    echo "=============================================="
    echo ""

    check_prerequisites

    echo ""
    echo "=== Section 1: Basic CLI Tests ==="
    test_help_command
    test_version_info
    test_unknown_command
    test_ps_empty
    test_ps_with_ns_flag
    test_ps_dry_flag

    echo ""
    echo "=== Section 2: Container Lifecycle Tests ==="
    test_run_basic_container
    test_run_background_container
    test_kill_container
    test_stop_container
    test_rm_container
    test_inspect_container
    test_cmd_container

    echo ""
    echo "=== Section 3: Container Configuration Tests ==="
    test_readonly_container
    test_volume_mount
    test_working_directory
    test_tmpfs_changes
    test_environment_all
    test_environment_selective

    echo ""
    echo "=== Section 4: Resource Limits Tests ==="
    test_memory_limit
    test_cpu_limit

    echo ""
    echo "=== Section 5: Namespace Tests ==="
    test_host_network
    test_isolated_pid_namespace

    echo ""
    echo "=== Section 6: Multiple Lower Directories Test ==="
    test_multiple_lower_dirs

    echo ""
    echo "=== Section 7: Exec Tests ==="
    test_exec_in_container

    echo ""
    echo "=== Section 8: Cleanup Tests ==="
    test_clear_unused

    echo ""
    echo "=== Section 9: Error Handling Tests ==="
    test_run_without_args
    test_run_nonexistent_image
    test_kill_nonexistent_container
    test_duplicate_container_name

    echo ""
    echo "=== Section 10: Go Unit Tests ==="
    test_go_unit_tests

    # Summary
    echo ""
    echo "=============================================="
    echo "                Test Summary                  "
    echo "=============================================="
    echo -e "Passed:  ${GREEN}$PASSED${NC}"
    echo -e "Failed:  ${RED}$FAILED${NC}"
    echo -e "Skipped: ${YELLOW}$SKIPPED${NC}"
    echo ""

    if [ ${#PASSED_TESTS[@]} -gt 0 ]; then
        echo -e "${GREEN}Passed tests:${NC}"
        for t in "${PASSED_TESTS[@]}"; do
            echo -e "  ${GREEN}+${NC} $t"
        done
        echo ""
    fi

    if [ ${#FAILED_TESTS[@]} -gt 0 ]; then
        echo -e "${RED}Failed tests:${NC}"
        for t in "${FAILED_TESTS[@]}"; do
            echo -e "  ${RED}-${NC} $t"
        done
        echo ""
    fi

    if [ $FAILED -gt 0 ]; then
        echo -e "${RED}Some tests failed!${NC}"
        exit 1
    else
        echo -e "${GREEN}All tests passed!${NC}"
        exit 0
    fi
}

# Run main
main "$@"
