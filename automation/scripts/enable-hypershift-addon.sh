#!/bin/bash

# enable-hypershift-addon.sh - Enable hypershift addon on MCE managed clusters
# This script automates the hypershift addon enablement from docs/discovering_hostedclusters.md

set -euo pipefail

# Configuration
ACM_NAMESPACE="${ACM_NAMESPACE:-multicluster-engine}"
ADDON_INSTALL_NAMESPACE="${ADDON_INSTALL_NAMESPACE:-open-cluster-management-agent-addon-discovery}"
DRY_RUN="${DRY_RUN:-false}"
CLUSTERS=""
TIMEOUT="${TIMEOUT:-600}" # 10 minutes

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
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

usage() {
    cat <<EOF
Usage: $0 --clusters CLUSTER_LIST [OPTIONS]

Enable hypershift addon on MCE managed clusters.

Required Arguments:
    --clusters LIST             Comma-separated list of cluster names

Optional Arguments:
    --timeout SECONDS          Timeout for addon enablement (default: 600)
    --dry-run                  Show what would be done without making changes
    --help, -h                 Show this help message

Environment Variables:
    ACM_NAMESPACE              ACM installation namespace (default: multicluster-engine)
    ADDON_INSTALL_NAMESPACE    Addon installation namespace (default: open-cluster-management-agent-addon-discovery)
    DRY_RUN                   Enable dry run mode (default: false)
    TIMEOUT                   Timeout in seconds (default: 600)

Examples:
    # Enable on single cluster
    $0 --clusters mce-a
    
    # Enable on multiple clusters
    $0 --clusters mce-a,mce-b,mce-c
    
    # With custom timeout
    $0 --clusters mce-a --timeout 900
EOF
}

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    if ! command -v oc &> /dev/null; then
        log_error "oc CLI is required but not installed"
        exit 1
    fi
    
    if ! command -v clusteradm &> /dev/null; then
        log_error "clusteradm CLI is required but not installed"
        log_error "Install from: https://github.com/open-cluster-management-io/clusteradm"
        exit 1
    fi
    
    if ! oc auth can-i patch addondeploymentconfig -n "$ACM_NAMESPACE" &> /dev/null; then
        log_error "Insufficient permissions to patch AddOnDeploymentConfig in namespace $ACM_NAMESPACE"
        exit 1
    fi
    
    if [[ -z "$CLUSTERS" ]]; then
        log_error "Cluster list is required"
        usage
        exit 1
    fi
    
    log_info "Prerequisites check passed"
}

validate_clusters() {
    local cluster_list="$1"
    local invalid_clusters=()
    
    log_info "Validating managed clusters..."
    
    IFS=',' read -ra cluster_array <<< "$cluster_list"
    for cluster in "${cluster_array[@]}"; do
        cluster=$(echo "$cluster" | xargs) # trim whitespace
        if ! oc get managedcluster "$cluster" &> /dev/null; then
            invalid_clusters+=("$cluster")
        fi
    done
    
    if [[ ${#invalid_clusters[@]} -gt 0 ]]; then
        log_error "Invalid managed clusters found: ${invalid_clusters[*]}"
        log_error "Please ensure these clusters are imported first"
        exit 1
    fi
    
    log_info "All clusters validated successfully"
}

configure_hypershift_addon_deployment() {
    log_info "Configuring hypershift addon deployment config..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would configure hypershift addon deployment"
        return
    fi
    
    # Patch hypershift addon deployment config for discovery namespace
    oc patch addondeploymentconfig hypershift-addon-deploy-config -n "$ACM_NAMESPACE" --type=merge -p '{
        "spec": {
            "agentInstallNamespace": "'$ADDON_INSTALL_NAMESPACE'"
        }
    }'
    
    log_info "Set addon installation namespace to: $ADDON_INSTALL_NAMESPACE"
    
    # Configure hypershift addon with discovery settings
    oc patch addondeploymentconfig hypershift-addon-deploy-config -n "$ACM_NAMESPACE" --type=merge -p '{
        "spec": {
            "customizedVariables": [
                {"name": "disableMetrics", "value": "true"},
                {"name": "disableHOManagement", "value": "true"}
            ]
        }
    }'
    
    log_info "Configured hypershift addon with discovery settings"
}

enable_hypershift_addon() {
    local cluster_list="$1"
    
    log_info "Enabling hypershift addon on clusters: $cluster_list"
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would enable hypershift addon on clusters: $cluster_list"
        return
    fi
    
    # Use clusteradm to enable the addon
    clusteradm addon enable --names hypershift-addon --clusters "$cluster_list"
    
    log_info "Hypershift addon enablement initiated"
}

wait_for_addon_deployment() {
    local cluster_list="$1"
    local timeout="$2"
    
    log_info "Waiting for hypershift addon deployments..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would wait for addon deployments"
        return
    fi
    
    IFS=',' read -ra cluster_array <<< "$cluster_list"
    local pending_clusters=("${cluster_array[@]}")
    local elapsed=0
    
    while [[ ${#pending_clusters[@]} -gt 0 ]] && [[ $elapsed -lt $timeout ]]; do
        local remaining_clusters=()
        
        for cluster in "${pending_clusters[@]}"; do
            cluster=$(echo "$cluster" | xargs) # trim whitespace
            local addon_status
            addon_status=$(oc get managedclusteraddon hypershift-addon -n "$cluster" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || echo "")
            
            if [[ "$addon_status" == "True" ]]; then
                log_info "Hypershift addon is available on cluster: $cluster"
            else
                remaining_clusters+=("$cluster")
            fi
        done
        
        pending_clusters=("${remaining_clusters[@]}")
        
        if [[ ${#pending_clusters[@]} -gt 0 ]]; then
            sleep 10
            elapsed=$((elapsed + 10))
            log_info "Still waiting for clusters: ${pending_clusters[*]} ($elapsed/${timeout}s)"
        fi
    done
    
    if [[ ${#pending_clusters[@]} -gt 0 ]]; then
        log_error "Timeout waiting for hypershift addon on clusters: ${pending_clusters[*]}"
        return 1
    fi
    
    log_info "All hypershift addons are available"
}

verify_addon_deployment() {
    local cluster_list="$1"
    
    log_info "Verifying hypershift addon deployments..."
    
    IFS=',' read -ra cluster_array <<< "$cluster_list"
    local failed_clusters=()
    
    for cluster in "${cluster_array[@]}"; do
        cluster=$(echo "$cluster" | xargs) # trim whitespace
        
        log_info "Checking cluster: $cluster"
        
        # Check ManagedClusterAddOn status
        local addon_available
        addon_available=$(oc get managedclusteraddon hypershift-addon -n "$cluster" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || echo "False")
        
        if [[ "$addon_available" == "True" ]]; then
            log_info "✓ Hypershift addon is available on $cluster"
        else
            log_error "✗ Hypershift addon is not available on $cluster"
            failed_clusters+=("$cluster")
        fi
        
        # Show addon status
        echo "Addon status for $cluster:"
        oc get managedclusteraddon hypershift-addon -n "$cluster" -o wide 2>/dev/null || log_warn "Could not get addon status"
    done
    
    if [[ ${#failed_clusters[@]} -gt 0 ]]; then
        log_error "Hypershift addon verification failed for clusters: ${failed_clusters[*]}"
        return 1
    fi
    
    log_info "All hypershift addon deployments verified successfully"
}

show_discovery_instructions() {
    log_info "Hypershift addon is now configured for discovery mode"
    log_info "The addon will:"
    log_info "  - Discover hosted clusters from MCE"
    log_info "  - Create DiscoveredCluster resources in ACM hub"
    log_info "  - Install agents in namespace: $ADDON_INSTALL_NAMESPACE"
    log_info ""
    log_info "Next step: Deploy auto-import policy using ./setup-auto-import-policy.sh"
}

main() {
    echo "=========================================="
    echo "Hypershift Addon Enablement"
    echo "=========================================="
    echo "Clusters: $CLUSTERS"
    echo "ACM Namespace: $ACM_NAMESPACE"
    echo "Addon Install Namespace: $ADDON_INSTALL_NAMESPACE"
    echo "Timeout: ${TIMEOUT}s"
    echo "Dry Run: $DRY_RUN"
    echo "=========================================="
    
    check_prerequisites
    validate_clusters "$CLUSTERS"
    
    log_info "Step 1: Configuring hypershift addon deployment..."
    configure_hypershift_addon_deployment
    
    log_info "Step 2: Enabling hypershift addon on clusters..."
    enable_hypershift_addon "$CLUSTERS"
    
    log_info "Step 3: Waiting for addon deployment..."
    if wait_for_addon_deployment "$CLUSTERS" "$TIMEOUT"; then
        log_info "Step 4: Verifying addon deployment..."
        verify_addon_deployment "$CLUSTERS"
    else
        log_error "Addon deployment failed or timed out"
        exit 1
    fi
    
    echo "=========================================="
    log_info "Hypershift addon enablement completed!"
    echo "=========================================="
    
    show_discovery_instructions
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --clusters)
            CLUSTERS="$2"
            shift 2
            ;;
        --timeout)
            TIMEOUT="$2"
            shift 2
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
