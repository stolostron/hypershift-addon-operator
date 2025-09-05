#!/bin/bash

# trigger-acm-hub-setup.sh - Trigger ACM Hub Setup via ConfigMap
# This script provides a convenient way to trigger the ConfigMap-based ACM hub setup

set -euo pipefail

# Configuration
ACM_NAMESPACE="${ACM_NAMESPACE:-multicluster-engine}"
SETUP_REASON="${SETUP_REASON:-Manual trigger}"
REQUESTED_BY="${REQUESTED_BY:-$(whoami)}"
TIMEOUT="${TIMEOUT:-600}" # 10 minutes
WAIT="${WAIT:-true}"
DRY_RUN="${DRY_RUN:-false}"
UNDO="${UNDO:-false}"
UNDO_REASON="${UNDO_REASON:-Manual undo trigger}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_step() {
    echo -e "${BLUE}[STEP]${NC} $1"
}

usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Trigger ACM Hub Setup or Undo using ConfigMap-based controller.

Options:
    --reason TEXT              Reason for triggering setup (default: "Manual trigger")
    --undo                     Trigger undo instead of setup
    --undo-reason TEXT         Reason for triggering undo (default: "Manual undo trigger")
    --requested-by USER        User requesting setup/undo (default: current user)
    --timeout SECONDS          Timeout for waiting (default: 600)
    --no-wait                  Don't wait for completion
    --dry-run                  Show what would be done without making changes
    --status                   Show current setup status
    --help, -h                 Show this help message

Environment Variables:
    ACM_NAMESPACE             ACM namespace (default: multicluster-engine)
    SETUP_REASON              Setup reason (default: "Manual trigger")
    UNDO_REASON               Undo reason (default: "Manual undo trigger")
    REQUESTED_BY              Requesting user (default: current user)
    TIMEOUT                   Timeout in seconds (default: 600)
    WAIT                      Wait for completion (default: true)
    DRY_RUN                   Enable dry run mode (default: false)
    UNDO                      Enable undo mode (default: false)

Examples:
    # Basic setup trigger
    $0
    
    # Trigger undo
    $0 --undo
    
    # With custom reason
    $0 --reason "Initial deployment setup"
    
    # Undo with custom reason
    $0 --undo --undo-reason "Cleanup before reconfiguration"
    
    # Don't wait for completion
    $0 --no-wait
    
    # Check current status
    $0 --status
    
    # Dry run to see what would be done
    $0 --dry-run
EOF
}

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl CLI is required but not installed"
        exit 1
    fi
    
    if ! kubectl auth can-i create configmap -n "$ACM_NAMESPACE" &> /dev/null; then
        log_error "Insufficient permissions to create ConfigMap in namespace $ACM_NAMESPACE"
        exit 1
    fi
    
    # Check if namespace exists
    if ! kubectl get namespace "$ACM_NAMESPACE" &> /dev/null; then
        log_error "Namespace $ACM_NAMESPACE does not exist"
        exit 1
    fi
    
    log_info "Prerequisites check passed"
}

check_existing_setup() {
    if [[ "$UNDO" == "true" ]]; then
        log_info "Checking for existing setup to undo..."
        return check_existing_setup_for_undo
    else
        log_info "Checking for existing setup..."
        return check_existing_setup_for_setup
    fi
}

check_existing_setup_for_setup() {
    if kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" &> /dev/null; then
        local status
        status=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-status}' 2>/dev/null || echo "")
        
        case "$status" in
            "completed")
                log_warn "Setup already completed"
                log_info "To re-run setup, delete the existing ConfigMap first:"
                log_info "  kubectl delete configmap acm-hub-setup-trigger -n $ACM_NAMESPACE"
                log_info "Or trigger undo with: $0 --undo"
                return 1
                ;;
            "in-progress")
                log_warn "Setup already in progress"
                log_info "Current status: $status"
                if [[ "$WAIT" == "true" ]]; then
                    log_info "Will wait for current setup to complete..."
                    return 0
                else
                    return 1
                fi
                ;;
            "undo-completed")
                log_info "Previous setup was undone, will recreate ConfigMap..."
                kubectl delete configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE"
                ;;
            "failed")
                log_warn "Previous setup failed"
                local error
                error=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-error}' 2>/dev/null || echo "Unknown error")
                log_info "Previous error: $error"
                log_info "Will delete existing ConfigMap and retry..."
                kubectl delete configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE"
                ;;
            *)
                log_info "Found existing ConfigMap with status: ${status:-unknown}"
                log_info "Will delete and recreate..."
                kubectl delete configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE"
                ;;
        esac
    fi
    
    return 0
}

check_existing_setup_for_undo() {
    if ! kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" &> /dev/null; then
        log_error "No setup ConfigMap found - nothing to undo"
        log_info "Run setup first with: $0"
        return 1
    fi
    
    local status
    status=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-status}' 2>/dev/null || echo "")
    
    case "$status" in
        "completed")
            log_info "Setup completed - ready for undo"
            return 0
            ;;
        "undo-completed")
            log_warn "Undo already completed"
            log_info "Current status: $status"
            return 1
            ;;
        "undo-in-progress")
            log_warn "Undo already in progress"
            log_info "Current status: $status"
            if [[ "$WAIT" == "true" ]]; then
                log_info "Will wait for current undo to complete..."
                return 0
            else
                return 1
            fi
            ;;
        "undo-failed")
            log_warn "Previous undo failed, will retry..."
            return 0
            ;;
        "in-progress"|"failed")
            log_error "Cannot undo while setup is $status"
            log_info "Wait for setup to complete first"
            return 1
            ;;
        *)
            log_warn "Unknown status: ${status:-unknown}"
            log_info "Will attempt undo anyway..."
            return 0
            ;;
    esac
}

create_trigger_configmap() {
    if [[ "$UNDO" == "true" ]]; then
        trigger_undo_configmap
    else
        create_setup_configmap
    fi
}

create_setup_configmap() {
    log_step "Creating setup trigger ConfigMap..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create acm-hub-setup-trigger ConfigMap"
        cat <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: acm-hub-setup-trigger
  namespace: $ACM_NAMESPACE
data:
  setup-requested: "true"
  setup-reason: "$SETUP_REASON"
  requested-by: "$REQUESTED_BY"
  created-at: "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
EOF
        return
    fi
    
    kubectl create configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" \
        --from-literal=setup-requested=true \
        --from-literal=setup-reason="$SETUP_REASON" \
        --from-literal=requested-by="$REQUESTED_BY" \
        --from-literal=created-at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    
    log_info "Setup trigger ConfigMap created successfully"
}

trigger_undo_configmap() {
    log_step "Updating ConfigMap to trigger undo..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would update acm-hub-setup-trigger ConfigMap for undo"
        cat <<EOF
# Patch command that would be executed:
kubectl patch configmap acm-hub-setup-trigger -n $ACM_NAMESPACE --type=merge -p='{
  "data": {
    "undo-requested": "true",
    "undo-reason": "$UNDO_REASON",
    "undo-requested-by": "$REQUESTED_BY",
    "undo-requested-at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  }
}'
EOF
        return
    fi
    
    kubectl patch configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" --type=merge -p="{
        \"data\": {
            \"undo-requested\": \"true\",
            \"undo-reason\": \"$UNDO_REASON\",
            \"undo-requested-by\": \"$REQUESTED_BY\",
            \"undo-requested-at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"
        }
    }"
    
    log_info "ConfigMap updated to trigger undo"
}

wait_for_completion() {
    if [[ "$WAIT" != "true" ]] || $DRY_RUN; then
        log_info "Not waiting for completion"
        return
    fi
    
    if [[ "$UNDO" == "true" ]]; then
        log_step "Waiting for undo completion (timeout: ${TIMEOUT}s)..."
    else
        log_step "Waiting for setup completion (timeout: ${TIMEOUT}s)..."
    fi
    
    local elapsed=0
    local status=""
    
    while [[ $elapsed -lt $TIMEOUT ]]; do
        status=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-status}' 2>/dev/null || echo "")
        
        if [[ "$UNDO" == "true" ]]; then
            case "$status" in
                "undo-completed")
                    log_info "Undo completed successfully!"
                    show_results
                    return 0
                    ;;
                "undo-failed")
                    log_error "Undo failed!"
                    show_error
                    return 1
                    ;;
                "undo-in-progress")
                    log_info "Undo in progress... (${elapsed}s elapsed)"
                    ;;
                *)
                    log_info "Waiting for undo to start... (${elapsed}s elapsed)"
                    ;;
            esac
        else
            case "$status" in
                "completed")
                    log_info "Setup completed successfully!"
                    show_results
                    return 0
                    ;;
                "failed")
                    log_error "Setup failed!"
                    show_error
                    return 1
                    ;;
                "in-progress")
                    log_info "Setup in progress... (${elapsed}s elapsed)"
                    ;;
                *)
                    log_info "Waiting for setup to start... (${elapsed}s elapsed)"
                    ;;
            esac
        fi
        
        sleep 10
        elapsed=$((elapsed + 10))
    done
    
    if [[ "$UNDO" == "true" ]]; then
        log_error "Timeout waiting for undo completion"
    else
        log_error "Timeout waiting for setup completion"
    fi
    log_info "Current status: $status"
    return 1
}

show_results() {
    log_info "Setup Results:"
    
    local results
    results=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-results}' 2>/dev/null || echo "")
    
    if [[ -n "$results" ]]; then
        if command -v jq &> /dev/null; then
            echo "$results" | jq .
        else
            echo "$results"
        fi
    else
        log_warn "No detailed results available"
    fi
    
    # Show summary
    local summary
    summary=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-results}' 2>/dev/null | jq -r '.summary // empty' 2>/dev/null || echo "")
    
    if [[ -n "$summary" ]]; then
        log_info "Summary: $summary"
    fi
}

show_error() {
    local error
    error=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-error}' 2>/dev/null || echo "Unknown error")
    
    log_error "Setup Error: $error"
    
    # Show failed steps
    local results
    results=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-results}' 2>/dev/null || echo "")
    
    if [[ -n "$results" ]] && command -v jq &> /dev/null; then
        log_info "Failed steps:"
        echo "$results" | jq '.results[] | select(.status == "failed")'
    fi
}

show_status() {
    log_info "Current Setup Status:"
    
    if ! kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" &> /dev/null; then
        log_info "No setup trigger ConfigMap found"
        return
    fi
    
    local status
    status=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-status}' 2>/dev/null || echo "unknown")
    
    local timestamp
    timestamp=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-timestamp}' 2>/dev/null || echo "unknown")
    
    local message
    message=$(kubectl get configmap acm-hub-setup-trigger -n "$ACM_NAMESPACE" -o jsonpath='{.data.setup-message}' 2>/dev/null || echo "")
    
    echo "Status: $status"
    echo "Last Update: $timestamp"
    if [[ -n "$message" ]]; then
        echo "Message: $message"
    fi
}

main() {
    if [[ "$UNDO" == "true" ]]; then
        echo "=========================================="
        echo "ACM Hub Setup Undo Trigger"
        echo "=========================================="
        echo "ACM Namespace: $ACM_NAMESPACE"
        echo "Undo Reason: $UNDO_REASON"
        echo "Requested By: $REQUESTED_BY"
        echo "Timeout: ${TIMEOUT}s"
        echo "Wait for Completion: $WAIT"
        echo "Dry Run: $DRY_RUN"
        echo "=========================================="
    else
        echo "=========================================="
        echo "ACM Hub Setup Trigger"
        echo "=========================================="
        echo "ACM Namespace: $ACM_NAMESPACE"
        echo "Setup Reason: $SETUP_REASON"
        echo "Requested By: $REQUESTED_BY"
        echo "Timeout: ${TIMEOUT}s"
        echo "Wait for Completion: $WAIT"
        echo "Dry Run: $DRY_RUN"
        echo "=========================================="
    fi
    
    check_prerequisites
    
    if ! check_existing_setup; then
        exit 1
    fi
    
    create_trigger_configmap
    
    if [[ "$WAIT" == "true" ]]; then
        wait_for_completion
    else
        if [[ "$UNDO" == "true" ]]; then
            log_info "Undo triggered. Monitor progress with:"
        else
            log_info "Setup triggered. Monitor progress with:"
        fi
        log_info "  kubectl get configmap acm-hub-setup-trigger -n $ACM_NAMESPACE -o yaml -w"
        log_info ""
        log_info "Check status with:"
        log_info "  $0 --status"
    fi
    
    echo "=========================================="
    if [[ "$UNDO" == "true" ]]; then
        log_info "Undo trigger completed!"
    else
        log_info "Setup trigger completed!"
    fi
    echo "=========================================="
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --reason)
            SETUP_REASON="$2"
            shift 2
            ;;
        --undo)
            UNDO=true
            shift
            ;;
        --undo-reason)
            UNDO_REASON="$2"
            shift 2
            ;;
        --requested-by)
            REQUESTED_BY="$2"
            shift 2
            ;;
        --timeout)
            TIMEOUT="$2"
            shift 2
            ;;
        --no-wait)
            WAIT=false
            shift
            ;;
        --status)
            show_status
            exit 0
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

main
