#!/bin/bash

# Script: enable-hypershift-addon.sh
# Description: Enable HyperShift Addon for MCE Clusters (Step 3 from discovering_hostedclusters.md)
# Author: Generated automation script
# Usage: ./enable-hypershift-addon.sh [<MCE_CLUSTER_NAMES>] [--discovery-prefix <prefix>]

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
DISCOVERY_PREFIX=""
DISABLE_METRICS="true"
DISABLE_HO_MANAGEMENT="true"

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Usage information
usage() {
    echo "Usage: $0 [<MCE_CLUSTER_NAMES>] [--discovery-prefix <prefix>]"
    echo ""
    echo "Arguments:"
    echo "  MCE_CLUSTER_NAMES    Comma-separated list of MCE cluster names"
    echo "                       If not provided, will prompt for selection"
    echo ""
echo "Options:"
echo "  --discovery-prefix   Set custom prefix for discovered cluster names"
echo "                       If not specified, uses default MCE cluster name prefix"
    echo ""
    echo "Examples:"
    echo "  $0                                    # Interactive mode"
    echo "  $0 mce-cluster-1                     # Single cluster"
    echo "  $0 mce-cluster-1,mce-cluster-2      # Multiple clusters"
    echo "  $0 --discovery-prefix custom-        # Set custom prefix"
}

# Parse command line arguments
parse_arguments() {
    local cluster_names=""
    
    while [[ $# -gt 0 ]]; do
        case $1 in
            --discovery-prefix)
                DISCOVERY_PREFIX="$2"
                shift 2
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            -*)
                log_error "Unknown option: $1"
                usage
                exit 1
                ;;
            *)
                cluster_names="$1"
                shift
                ;;
        esac
    done
    
    echo "$cluster_names"
}

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check if oc command is available
    if ! command -v oc &> /dev/null; then
        log_error "oc CLI tool is not installed or not in PATH"
        exit 1
    fi
    
    # Check if clusteradm command is available
    if ! command -v clusteradm &> /dev/null; then
        log_error "clusteradm CLI tool is not installed or not in PATH"
        log_info "Install it from: https://github.com/open-cluster-management-io/clusteradm/releases"
        exit 1
    fi
    
    # Check if logged into OpenShift
    if ! oc whoami &> /dev/null; then
        log_error "Not logged into OpenShift. Please run 'oc login' first"
        exit 1
    fi
    
    # Check if user has cluster-admin privileges
    if ! oc auth can-i '*' '*' --all-namespaces &> /dev/null; then
        log_error "Insufficient privileges. cluster-admin access required"
        exit 1
    fi
    
    # Check if ACM is installed
    log_info "Checking ACM installation..."
    
    # Check for ACM namespace
    if ! oc get namespace open-cluster-management &> /dev/null; then
        log_error "ACM namespace 'open-cluster-management' not found"
        log_error "This script requires Red Hat Advanced Cluster Management (ACM) to be installed"
        log_error "Found components:"
        oc get csv -A | grep -E "(advanced-cluster-management|multicluster-engine)" || log_error "  No ACM/MCE components found"
        exit 1
    fi
    
    # Check for ACM CSV
    local acm_csv
    acm_csv=$(oc get csv -A | grep "advanced-cluster-management" | head -1 | awk '{print $1}' || echo "")
    
    if [ -z "$acm_csv" ]; then
        log_error "ACM ClusterServiceVersion not found"
        log_error "This script requires Red Hat Advanced Cluster Management (ACM) to be installed"
        log_error "Currently installed components:"
        oc get csv -A | grep -E "(advanced-cluster-management|multicluster-engine)" || log_error "  No ACM/MCE components found"
        log_error ""
        log_error "Please install ACM before running this script. MCE-only installations are not supported."
        exit 1
    fi
    
    log_success "Found ACM installation: $acm_csv"
    log_success "Prerequisites check passed"
}

# Get available MCE clusters
get_mce_clusters() {
    log_info "Getting list of available MCE clusters..."
    
    local clusters
    clusters=$(oc get managedcluster --no-headers | grep -v local-cluster | awk '{print $1}' | tr '\n' ' ')
    
    if [ -z "$clusters" ]; then
        log_error "No MCE clusters found (excluding local-cluster)"
        log_error "Please import MCE clusters first using import-mce-cluster.sh"
        exit 1
    fi
    
    echo "$clusters"
}

# Interactive cluster selection
select_clusters_interactively() {
    local available_clusters="$1"
    local clusters_array
    IFS=' ' read -ra clusters_array <<< "$available_clusters"
    
    echo ""
    log_info "Available MCE clusters:"
    for i in "${!clusters_array[@]}"; do
        echo "  $((i+1))) ${clusters_array[i]}"
    done
    
    echo ""
    echo "Select clusters to enable HyperShift addon:"
    echo "  - Enter numbers separated by spaces (e.g., '1 3 4')"
    echo "  - Enter 'all' to select all clusters"
    echo "  - Enter 'quit' to exit"
    
    local selection
    read -p "Your selection: " selection
    
    case "$selection" in
        "quit")
            log_info "Exiting..."
            exit 0
            ;;
        "all")
            echo "${available_clusters// /,}"
            ;;
        *)
            local selected_clusters=""
            for num in $selection; do
                if [[ "$num" =~ ^[0-9]+$ ]] && [ "$num" -ge 1 ] && [ "$num" -le "${#clusters_array[@]}" ]; then
                    local cluster_name="${clusters_array[$((num-1))]}"
                    if [ -n "$selected_clusters" ]; then
                        selected_clusters="$selected_clusters,$cluster_name"
                    else
                        selected_clusters="$cluster_name"
                    fi
                else
                    log_warning "Invalid selection: $num (ignored)"
                fi
            done
            
            if [ -z "$selected_clusters" ]; then
                log_error "No valid clusters selected"
                exit 1
            fi
            
            echo "$selected_clusters"
            ;;
    esac
}

# Configure HyperShift addon deployment
configure_hypershift_addon() {
    log_info "Configuring HyperShift addon deployment..."
    
    # Set installation namespace
    log_info "Setting addon installation namespace..."
    oc patch addondeploymentconfig hypershift-addon-deploy-config \
        -n multicluster-engine \
        --type=merge \
        -p '{"spec":{"agentInstallNamespace":"open-cluster-management-agent-addon-discovery"}}'
    
    if [ $? -ne 0 ]; then
        log_error "Failed to set addon installation namespace"
        exit 1
    fi
    
    # Prepare customized variables
    local custom_vars='[{"name":"disableMetrics","value":"'$DISABLE_METRICS'"},{"name":"disableHOManagement","value":"'$DISABLE_HO_MANAGEMENT'"}]'
    
    # Add discovery prefix if specified
    if [ -n "$DISCOVERY_PREFIX" ]; then
        log_info "Setting discovery prefix to: '$DISCOVERY_PREFIX'"
        custom_vars='[{"name":"disableMetrics","value":"'$DISABLE_METRICS'"},{"name":"disableHOManagement","value":"'$DISABLE_HO_MANAGEMENT'"},{"name":"discoveryPrefix","value":"'$DISCOVERY_PREFIX'"}]'
    fi
    
    # Configure addon variables
    log_info "Configuring addon variables..."
    oc patch addondeploymentconfig hypershift-addon-deploy-config \
        -n multicluster-engine \
        --type=merge \
        -p '{"spec":{"customizedVariables":'$custom_vars'}}'
    
    if [ $? -eq 0 ]; then
        log_success "HyperShift addon configuration updated successfully"
    else
        log_error "Failed to configure HyperShift addon"
        exit 1
    fi
}

# Enable addon for clusters
enable_addon_for_clusters() {
    local cluster_names="$1"
    
    log_info "Enabling HyperShift addon for clusters: $cluster_names"
    
    # Use clusteradm to enable the addon
    clusteradm addon enable --names hypershift-addon --clusters "$cluster_names"
    
    if [ $? -eq 0 ]; then
        log_success "HyperShift addon enabled for clusters: $cluster_names"
    else
        log_error "Failed to enable HyperShift addon for some or all clusters"
        log_info "Check the addon status with: oc get managedclusteraddon --all-namespaces"
        exit 1
    fi
}

# Verify addon installation
verify_addon_installation() {
    local cluster_names="$1"
    
    log_info "Verifying addon installation..."
    
    # Convert comma-separated list to array
    IFS=',' read -ra clusters_array <<< "$cluster_names"
    
    local all_ready=true
    
    for cluster in "${clusters_array[@]}"; do
        log_info "Checking addon status for cluster '$cluster'..."
        
        # Wait for addon to be created
        local max_attempts=30
        local attempt=0
        
        while [ $attempt -lt $max_attempts ]; do
            if oc get managedclusteraddon hypershift-addon -n "$cluster" &> /dev/null; then
                break
            fi
            log_info "Waiting for addon to be created... (attempt $((attempt + 1))/$max_attempts)"
            sleep 10
            ((attempt++))
        done
        
        if [ $attempt -eq $max_attempts ]; then
            log_error "HyperShift addon was not created for cluster '$cluster'"
            all_ready=false
            continue
        fi
        
        # Check addon status
        local available
        available=$(oc get managedclusteraddon hypershift-addon -n "$cluster" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || echo "Unknown")
        
        if [ "$available" = "True" ]; then
            log_success "HyperShift addon is available for cluster '$cluster'"
        else
            log_warning "HyperShift addon is not yet available for cluster '$cluster'"
            all_ready=false
        fi
        
        # Check deployment in the cluster namespace
        if oc get deployment hypershift-addon-agent -n open-cluster-management-agent-addon-discovery &> /dev/null; then
            local ready
            ready=$(oc get deployment hypershift-addon-agent -n open-cluster-management-agent-addon-discovery -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
            local desired
            desired=$(oc get deployment hypershift-addon-agent -n open-cluster-management-agent-addon-discovery -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "1")
            
            if [ "$ready" = "$desired" ]; then
                log_success "HyperShift addon agent deployment is ready ($ready/$desired)"
            else
                log_warning "HyperShift addon agent deployment is not fully ready ($ready/$desired)"
                all_ready=false
            fi
        else
            log_warning "HyperShift addon agent deployment not found"
            all_ready=false
        fi
    done
    
    if [ "$all_ready" = true ]; then
        log_success "All HyperShift addons are ready!"
    else
        log_warning "Some addons are not fully ready yet. This may be normal during initial deployment."
        log_info "Monitor addon status with: oc get managedclusteraddon --all-namespaces"
    fi
}

# Display configuration summary
display_configuration_summary() {
    log_info "HyperShift Addon Configuration Summary:"
    echo "  - Installation namespace: open-cluster-management-agent-addon-discovery"
    echo "  - Metrics disabled: $DISABLE_METRICS"
    echo "  - HO Management disabled: $DISABLE_HO_MANAGEMENT"
    
    if [ -n "$DISCOVERY_PREFIX" ]; then
        echo "  - Discovery prefix: '$DISCOVERY_PREFIX'"
    else
        echo "  - Discovery prefix: <cluster-name>- (default)"
    fi
}

# Main execution
main() {
    local cluster_names
    cluster_names=$(parse_arguments "$@")
    
    log_info "Starting HyperShift addon enablement process..."
    
    check_prerequisites
    
    # Get available clusters if not specified
    if [ -z "$cluster_names" ]; then
        local available_clusters
        available_clusters=$(get_mce_clusters)
        cluster_names=$(select_clusters_interactively "$available_clusters")
    fi
    
    # Validate cluster names
    IFS=',' read -ra clusters_array <<< "$cluster_names"
    for cluster in "${clusters_array[@]}"; do
        if ! oc get managedcluster "$cluster" &> /dev/null; then
            log_error "Managed cluster '$cluster' not found"
            log_info "Available clusters: $(get_mce_clusters)"
            exit 1
        fi
    done
    
    configure_hypershift_addon
    enable_addon_for_clusters "$cluster_names"
    verify_addon_installation "$cluster_names"
    display_configuration_summary
    
    log_success "HyperShift addon enablement completed!"
    log_info "Next steps:"
    log_info "1. Create hosted clusters in your MCE clusters"
    log_info "2. Set up auto-import policy using: ./setup-autoimport-policy.sh"
    log_info "3. Monitor discovered clusters with: oc get discoveredcluster --all-namespaces"
}

# Run main function
main "$@"
