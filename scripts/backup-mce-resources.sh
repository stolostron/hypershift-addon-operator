#!/bin/bash

# Script: backup-mce-resources.sh
# Description: Label and backup critical MCE-ACM integration resources for disaster recovery
# Author: Generated automation script
# Usage: ./backup-mce-resources.sh [--backup-dir <directory>] [--dry-run]

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
DRY_RUN=false

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
    echo "Usage: $0 [--dry-run]"
    echo ""
    echo "Options:"
    echo "  --dry-run           Show what would be done without making changes"
    echo ""
    echo "Examples:"
    echo "  $0                                    # Label resources for ACM backup system"
    echo "  $0 --dry-run                         # Preview actions without execution"
    echo ""
    echo "This script will:"
    echo "  1. Label critical resources with cluster.open-cluster-management.io/backup=true"
    echo "  2. Integrate with ACM's native backup system"
    echo "  3. Create a manifest documenting labeled resources"
}

# Parse command line arguments
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --dry-run)
                DRY_RUN=true
                shift
                ;;
            -h|--help)
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
}

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check if oc command is available
    if ! command -v oc &> /dev/null; then
        log_error "oc CLI tool is not installed or not in PATH"
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


# Label resource for backup
label_resource() {
    local resource_type="$1"
    local resource_name="$2"
    local namespace="${3:-}"
    
    local label_cmd="oc label $resource_type $resource_name cluster.open-cluster-management.io/backup=true"
    if [ -n "$namespace" ]; then
        label_cmd="oc label $resource_type $resource_name -n $namespace cluster.open-cluster-management.io/backup=true"
    fi
    
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: Would label $resource_type/$resource_name"
        return 0
    fi
    
    # Check if resource exists first
    local check_cmd="oc get $resource_type $resource_name"
    if [ -n "$namespace" ]; then
        check_cmd="oc get $resource_type $resource_name -n $namespace"
    fi
    
    if $check_cmd &> /dev/null; then
        if $label_cmd --overwrite &> /dev/null; then
            log_success "Labeled $resource_type/$resource_name for backup"
            return 0
        else
            log_error "Failed to label $resource_type/$resource_name"
            return 1
        fi
    else
        log_warning "$resource_type/$resource_name not found - skipping"
        return 0
    fi
}


# Label and backup configuration resources
backup_configuration_resources() {
    log_info "=== Labeling and Backing Up Configuration Resources ==="
    
    # AddOnDeploymentConfig resources
    log_info "Processing AddOnDeploymentConfig resources..."
    label_resource "addondeploymentconfig" "addon-ns-config" "multicluster-engine"
    
    label_resource "addondeploymentconfig" "hypershift-addon-deploy-config" "multicluster-engine"
    
    # KlusterletConfig
    log_info "Processing KlusterletConfig..."
    label_resource "klusterletconfig" "mce-import-klusterlet-config"
}

# Label and backup addon resources
backup_addon_resources() {
    log_info "=== Labeling and Backing Up Addon Resources ==="
    
    local addons=("work-manager" "cluster-proxy" "managed-serviceaccount" "hypershift-addon")
    
    for addon in "${addons[@]}"; do
        log_info "Processing ClusterManagementAddOn: $addon"
        label_resource "clustermanagementaddon" "$addon"
    done
}

# Label and backup policy resources
backup_policy_resources() {
    log_info "=== Labeling and Backing Up Policy Resources ==="
    
    # Auto-import policy
    log_info "Processing auto-import policy..."
    label_resource "policy" "policy-mce-hcp-autoimport" "open-cluster-management-global-set"
    
    # Placement
    label_resource "placement" "policy-mce-hcp-autoimport-placement" "open-cluster-management-global-set"
    
    # PlacementBinding
    label_resource "placementbinding" "policy-mce-hcp-autoimport-placement-binding" "open-cluster-management-global-set"
    
    # Discovery config ConfigMap
    label_resource "configmap" "discovery-config" "open-cluster-management-global-set"
}

# Backup managed cluster configurations
backup_managed_cluster_configs() {
    log_info "=== Backing Up Managed Cluster Configurations ==="
    
    # Get all MCE clusters (excluding local-cluster)
    local clusters
    clusters=$(oc get managedcluster --no-headers | grep -v local-cluster | awk '{print $1}' | tr '\n' ' ')
    
    if [ -z "$clusters" ]; then
        log_info "No MCE managed clusters found"
        return
    fi
    
    for cluster in $clusters; do
        log_info "Processing managed cluster: $cluster"
        
        # Label ManagedCluster
        label_resource "managedcluster" "$cluster"
        
        # Check ManagedClusterAddOns for this cluster (for informational purposes)
        local cluster_addons
        cluster_addons=$(oc get managedclusteraddon -n "$cluster" --no-headers 2>/dev/null | awk '{print $1}' | tr '\n' ' ' || echo "")
        
        if [ -n "$cluster_addons" ]; then
            log_info "  Found addons for cluster $cluster: $cluster_addons"
            log_info "  Note: ManagedClusterAddOn resources are recreated automatically and don't need backup"
        fi
    done
}



# Display backup summary
display_backup_summary() {
    log_info "=== Backup Summary ==="
    
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: No actual labeling was performed"
        return
    fi
    
    log_info "Resource labeling completed successfully!"
    log_info "Backup Date: $(date)"
    log_info "OpenShift Cluster: $(oc whoami --show-server)"
    
    echo ""
    log_info "Labeled Resources:"
    echo "  ✓ AddOnDeploymentConfig resources"
    echo "  ✓ ClusterManagementAddOn resources" 
    echo "  ✓ KlusterletConfig resources"
    echo "  ✓ Policy resources (if present)"
    echo "  ✓ ManagedCluster resources"
    
    echo ""
    log_info "Resources have been labeled with 'cluster.open-cluster-management.io/backup=true'"
    log_info "This ensures they will be included in ACM's backup operations (if configured)"
    echo ""
    log_info "For disaster recovery, use the setup scripts to recreate the integration:"
    echo "  1. ./setup-acm-hub.sh"
    echo "  2. ./import-mce-cluster.sh <cluster-name> <kubeconfig-file>"
    echo "  3. ./enable-hypershift-addon.sh <cluster-names>"
    echo "  4. ./setup-autoimport-policy.sh"
}

# Main execution
main() {
    parse_arguments "$@"
    
    log_info "Starting MCE-ACM integration backup process..."
    
    if [ "$DRY_RUN" = true ]; then
        log_warning "DRY RUN MODE: No changes will be made"
    fi
    
    check_prerequisites
    backup_configuration_resources
    backup_addon_resources
    backup_policy_resources
    backup_managed_cluster_configs
    display_backup_summary
    
    if [ "$DRY_RUN" = true ]; then
        log_success "DRY RUN completed - no actual labeling was performed"
    else
        log_success "Resource labeling completed successfully!"
        log_info "Your MCE-ACM integration resources are now labeled for ACM backup system"
    fi
}

# Run main function
main "$@"
