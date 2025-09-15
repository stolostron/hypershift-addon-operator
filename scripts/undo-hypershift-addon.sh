#!/bin/bash

# Script: undo-hypershift-addon.sh
# Description: Disable HyperShift addon from MCE clusters (Undo step 3)
# Author: Generated automation script
# Usage: ./undo-hypershift-addon.sh [<MCE_CLUSTER_NAMES>]

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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
    echo "Usage: $0 [<MCE_CLUSTER_NAMES>]"
    echo ""
    echo "Arguments:"
    echo "  MCE_CLUSTER_NAMES    Comma-separated list of MCE cluster names"
    echo "                       If not provided, will prompt for selection"
    echo ""
    echo "Examples:"
    echo "  $0                                    # Interactive mode"
    echo "  $0 mce-cluster-1                     # Single cluster"
    echo "  $0 mce-cluster-1,mce-cluster-2      # Multiple clusters"
    echo ""
    echo "This script will:"
    echo "  1. Disable HyperShift addon from specified clusters"
    echo "  2. Clean up addon agent deployments"
    echo "  3. Reset addon deployment configuration"
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

# Get available MCE clusters with HyperShift addon
get_mce_clusters_with_addon() {
    log_info "Getting list of MCE clusters with HyperShift addon..."
    
    local clusters=""
    for cluster in $(oc get managedcluster --no-headers | grep -v local-cluster | awk '{print $1}'); do
        if oc get managedclusteraddon hypershift-addon -n "$cluster" &> /dev/null; then
            if [ -n "$clusters" ]; then
                clusters="$clusters $cluster"
            else
                clusters="$cluster"
            fi
        fi
    done
    
    if [ -z "$clusters" ]; then
        log_warning "No MCE clusters found with HyperShift addon enabled"
        return 1
    fi
    
    echo "$clusters"
}

# Interactive cluster selection
select_clusters_interactively() {
    local available_clusters="$1"
    local clusters_array
    IFS=' ' read -ra clusters_array <<< "$available_clusters"
    
    echo ""
    log_info "MCE clusters with HyperShift addon:"
    for i in "${!clusters_array[@]}"; do
        echo "  $((i+1))) ${clusters_array[i]}"
    done
    
    echo ""
    echo "Select clusters to disable HyperShift addon:"
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

# Disable addon for clusters
disable_addon_for_clusters() {
    local cluster_names="$1"
    
    log_info "Disabling HyperShift addon for clusters: $cluster_names"
    
    # Use clusteradm to disable the addon
    clusteradm addon disable --names hypershift-addon --clusters "$cluster_names"
    
    if [ $? -eq 0 ]; then
        log_success "HyperShift addon disabled for clusters: $cluster_names"
    else
        log_error "Failed to disable HyperShift addon for some or all clusters"
        log_info "Check the addon status with: oc get managedclusteraddon --all-namespaces"
    fi
}

# Clean up addon deployments
cleanup_addon_deployments() {
    log_info "Cleaning up addon deployments..."
    
    # Check if hypershift-addon-agent deployment exists
    if oc get deployment hypershift-addon-agent -n open-cluster-management-agent-addon-discovery &> /dev/null; then
        log_info "Waiting for hypershift-addon-agent deployment to be removed..."
        
        local max_attempts=30
        local attempt=0
        
        while [ $attempt -lt $max_attempts ]; do
            if ! oc get deployment hypershift-addon-agent -n open-cluster-management-agent-addon-discovery &> /dev/null; then
                log_success "HyperShift addon agent deployment removed"
                break
            fi
            log_info "Waiting for deployment cleanup... (attempt $((attempt + 1))/$max_attempts)"
            sleep 10
            ((attempt++))
        done
        
        if [ $attempt -eq $max_attempts ]; then
            log_warning "Deployment cleanup timeout reached"
            log_info "You may need to manually clean up remaining resources"
        fi
    else
        log_info "No hypershift-addon-agent deployment found"
    fi
}

# Reset addon deployment configuration
reset_addon_configuration() {
    log_info "Resetting HyperShift addon deployment configuration..."
    
    # Reset to default configuration (remove custom variables)
    if oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine &> /dev/null; then
        oc patch addondeploymentconfig hypershift-addon-deploy-config \
            -n multicluster-engine \
            --type=json \
            -p='[{"op": "remove", "path": "/spec/customizedVariables"}]' 2>/dev/null || true
        
        oc patch addondeploymentconfig hypershift-addon-deploy-config \
            -n multicluster-engine \
            --type=json \
            -p='[{"op": "remove", "path": "/spec/agentInstallNamespace"}]' 2>/dev/null || true
        
        log_success "HyperShift addon configuration reset to defaults"
    else
        log_info "No hypershift-addon-deploy-config found"
    fi
}

# Verify cleanup
verify_cleanup() {
    local cluster_names="$1"
    
    log_info "Verifying addon cleanup..."
    
    # Convert comma-separated list to array
    IFS=',' read -ra clusters_array <<< "$cluster_names"
    
    local all_clean=true
    
    for cluster in "${clusters_array[@]}"; do
        log_info "Checking cleanup for cluster '$cluster'..."
        
        # Check if addon still exists
        if oc get managedclusteraddon hypershift-addon -n "$cluster" &> /dev/null; then
            log_warning "HyperShift addon still exists for cluster '$cluster'"
            all_clean=false
        else
            log_success "HyperShift addon removed from cluster '$cluster'"
        fi
    done
    
    # Check if agent deployment is gone
    if oc get deployment hypershift-addon-agent -n open-cluster-management-agent-addon-discovery &> /dev/null; then
        log_warning "HyperShift addon agent deployment still exists"
        all_clean=false
    else
        log_success "HyperShift addon agent deployment removed"
    fi
    
    if [ "$all_clean" = true ]; then
        log_success "All HyperShift addon components have been successfully removed!"
    else
        log_warning "Some cleanup operations may need manual intervention"
    fi
}

# Main execution
main() {
    local cluster_names="$1"
    
    log_info "Starting HyperShift addon removal process..."
    
    check_prerequisites
    
    # Get available clusters if not specified
    if [ -z "$cluster_names" ]; then
        local available_clusters
        if available_clusters=$(get_mce_clusters_with_addon); then
            cluster_names=$(select_clusters_interactively "$available_clusters")
        else
            log_info "No clusters with HyperShift addon found. Nothing to clean up."
            exit 0
        fi
    fi
    
    # Validate cluster names
    IFS=',' read -ra clusters_array <<< "$cluster_names"
    for cluster in "${clusters_array[@]}"; do
        cluster=$(echo "$cluster" | xargs)  # Trim whitespace
        if ! oc get managedcluster "$cluster" &> /dev/null; then
            log_error "Managed cluster '$cluster' not found"
            exit 1
        fi
        
        if ! oc get managedclusteraddon hypershift-addon -n "$cluster" &> /dev/null; then
            log_warning "HyperShift addon not found for cluster '$cluster'"
        fi
    done
    
    disable_addon_for_clusters "$cluster_names"
    cleanup_addon_deployments
    reset_addon_configuration
    verify_cleanup "$cluster_names"
    
    log_success "HyperShift addon removal completed!"
    log_info "The clusters no longer have hosted cluster discovery capabilities."
}

# Parse command line arguments
if [ $# -eq 0 ]; then
    main ""
else
    case $1 in
        -h|--help)
            usage
            exit 0
            ;;
        *)
            main "$1"
            ;;
    esac
fi
