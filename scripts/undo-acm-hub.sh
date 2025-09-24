#!/bin/bash

# Script: undo-acm-hub.sh
# Description: Undo ACM Hub MCE integration setup (Undo step 1)
# Author: Generated automation script
# Version: 2.0 - Updated to handle Application Manager addon
# Usage: ./undo-acm-hub.sh [--force]

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
FORCE_MODE=false

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
    echo "Usage: $0 [--force]"
    echo ""
    echo "Options:"
    echo "  --force    Force removal even if MCE clusters are still imported"
    echo ""
    echo "This script will:"
    echo "  1. Check for imported MCE clusters (warns if found)"
    echo "  2. Reset ClusterManagementAddOn resources to defaults (work-manager, managed-serviceaccount, cluster-proxy, application-manager)"
    echo "  3. Remove custom AddOnDeploymentConfig resources"
    echo "  4. Remove custom KlusterletConfig"
    echo "  5. Clean up addon deployment namespace if empty"
    echo ""
    echo "WARNING: This will undo the ACM Hub setup for MCE integration."
    echo "Make sure to remove all MCE clusters first unless using --force."
}

# Parse command line arguments
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --force)
                FORCE_MODE=true
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
    
    log_success "Prerequisites check passed"
}

# Reset ClusterManagementAddOn resources
reset_cluster_management_addons() {
    log_info "Resetting ClusterManagementAddOn resources..."
    
    local addons=("work-manager" "managed-serviceaccount" "cluster-proxy" "application-manager")
    
    for addon in "${addons[@]}"; do
        if oc get clustermanagementaddon "$addon" &> /dev/null; then
            log_info "Resetting $addon addon configuration..."
            
            # Remove custom configuration by resetting to basic config
            cat <<EOF | oc apply -f -
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: $addon
spec:
  addOnMeta:
    displayName: $addon
  installStrategy:
    placements:
    - name: global
      namespace: open-cluster-management-global-set
      rolloutStrategy:
        type: All
    type: Placements
EOF
            
            if [ $? -eq 0 ]; then
                log_success "Reset $addon addon configuration"
            else
                log_error "Failed to reset $addon addon configuration"
            fi
        else
            log_info "ClusterManagementAddOn $addon not found"
        fi
    done
}

# Remove custom AddOnDeploymentConfig resources
remove_addon_deployment_configs() {
    log_info "Removing custom AddOnDeploymentConfig resources..."
    
    # Remove addon-ns-config
    if oc get addondeploymentconfig addon-ns-config -n multicluster-engine &> /dev/null; then
        oc delete addondeploymentconfig addon-ns-config -n multicluster-engine
        if [ $? -eq 0 ]; then
            log_success "Removed addon-ns-config AddOnDeploymentConfig"
        else
            log_error "Failed to remove addon-ns-config AddOnDeploymentConfig"
        fi
    else
        log_info "addon-ns-config AddOnDeploymentConfig not found"
    fi
    
    # Reset hypershift-addon-deploy-config if it exists
    if oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine &> /dev/null; then
        log_info "Resetting hypershift-addon-deploy-config to defaults..."
        
        # Remove custom configurations
        oc patch addondeploymentconfig hypershift-addon-deploy-config \
            -n multicluster-engine \
            --type=json \
            -p='[{"op": "remove", "path": "/spec/customizedVariables"}]' 2>/dev/null || true
        
        oc patch addondeploymentconfig hypershift-addon-deploy-config \
            -n multicluster-engine \
            --type=json \
            -p='[{"op": "remove", "path": "/spec/agentInstallNamespace"}]' 2>/dev/null || true
        
        log_success "Reset hypershift-addon-deploy-config to defaults"
    else
        log_info "hypershift-addon-deploy-config not found"
    fi
}

# Remove custom KlusterletConfig
remove_klusterlet_config() {
    log_info "Removing custom KlusterletConfig..."
    
    if oc get klusterletconfig mce-import-klusterlet-config &> /dev/null; then
        oc delete klusterletconfig mce-import-klusterlet-config
        if [ $? -eq 0 ]; then
            log_success "Removed mce-import-klusterlet-config KlusterletConfig"
        else
            log_error "Failed to remove mce-import-klusterlet-config KlusterletConfig"
        fi
    else
        log_info "mce-import-klusterlet-config KlusterletConfig not found"
    fi
}

# Clean up addon deployment namespace
cleanup_addon_namespace() {
    log_info "Checking addon deployment namespace..."
    
    local namespace="open-cluster-management-agent-addon-discovery"
    
    if oc get namespace "$namespace" &> /dev/null; then
        # Check if namespace has any deployments
        local deployments
        deployments=$(oc get deployment -n "$namespace" --no-headers 2>/dev/null | wc -l || echo "0")
        
        if [ "$deployments" -eq 0 ]; then
            log_info "Addon namespace is empty, removing it..."
            oc delete namespace "$namespace" --wait=false
            
            # Wait for namespace deletion (up to 2 minutes)
            local max_attempts=12
            local attempt=0
            
            while [ $attempt -lt $max_attempts ]; do
                if ! oc get namespace "$namespace" &> /dev/null; then
                    log_success "Addon namespace removed"
                    return
                fi
                
                log_info "Waiting for namespace deletion... (attempt $((attempt + 1))/$max_attempts)"
                sleep 10
                ((attempt++))
            done
            
            log_warning "Addon namespace deletion timeout - may require manual cleanup"
        else
            log_info "Addon namespace still has $deployments deployment(s) - keeping namespace"
            log_info "Deployments in $namespace:"
            oc get deployment -n "$namespace" --no-headers | awk '{print "  - " $1}'
        fi
    else
        log_info "Addon deployment namespace does not exist"
    fi
}

# Verify cleanup
verify_cleanup() {
    log_info "Verifying ACM Hub cleanup..."
    
    local cleanup_success=true
    
    # Check if custom resources are removed
    if oc get addondeploymentconfig addon-ns-config -n multicluster-engine &> /dev/null; then
        log_warning "addon-ns-config AddOnDeploymentConfig still exists"
        cleanup_success=false
    fi
    
    if oc get klusterletconfig mce-import-klusterlet-config &> /dev/null; then
        log_warning "mce-import-klusterlet-config KlusterletConfig still exists"
        cleanup_success=false
    fi
    
    # Check if ClusterManagementAddOns are reset
    local addons=("work-manager" "managed-serviceaccount" "cluster-proxy" "application-manager")
    for addon in "${addons[@]}"; do
        if oc get clustermanagementaddon "$addon" &> /dev/null; then
            local has_configs
            has_configs=$(oc get clustermanagementaddon "$addon" -o jsonpath='{.spec.installStrategy.placements[0].configs}' 2>/dev/null | grep -c "addon-ns-config" || echo "0")
            if [ "$has_configs" -gt 0 ]; then
                log_warning "ClusterManagementAddOn $addon still has custom configuration"
                cleanup_success=false
            fi
        fi
    done
    
    if [ "$cleanup_success" = true ]; then
        log_success "ACM Hub cleanup completed successfully!"
        log_info "The hub is now reset to default configuration"
    else
        log_warning "Some cleanup operations may require manual intervention"
        echo ""
        log_info "Manual cleanup commands if needed:"
        echo "  # Remove remaining custom configs"
        echo "  oc delete addondeploymentconfig addon-ns-config -n multicluster-engine"
        echo "  oc delete klusterletconfig mce-import-klusterlet-config"
    fi
}

# Display summary
display_summary() {
    log_info "=== ACM Hub Cleanup Summary ==="
    echo ""
    log_info "What was reset:"
    echo "  ✓ ClusterManagementAddOn resources reset to defaults"
    echo "  ✓ Custom AddOnDeploymentConfig resources removed"
    echo "  ✓ Custom KlusterletConfig removed"
    echo "  ✓ Addon deployment namespace cleaned up (if empty)"
    echo ""
    log_info "Your ACM Hub is now in the default state."
    echo ""
    if [ "$FORCE_MODE" = true ]; then
        log_warning "Note: Force mode was used - some MCE clusters may still be imported"
        log_info "You may want to clean them up manually using undo-import-mce-cluster.sh"
    fi
}

# Main execution
main() {
    parse_arguments "$@"
    
    log_info "Starting ACM Hub MCE integration cleanup..."
    
    if [ "$FORCE_MODE" = true ]; then
        log_warning "Running in FORCE mode"
    fi
    
    check_prerequisites
    
    echo ""
    log_warning "This will reset your ACM Hub to default configuration"
    log_warning "All MCE integration settings will be removed"
    
    if [ "$FORCE_MODE" = false ]; then
        read -p "Are you sure you want to proceed? (y/N): " confirm
        if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
            log_info "Operation cancelled by user"
            exit 0
        fi
    fi
    
    echo ""
    reset_cluster_management_addons
    remove_addon_deployment_configs
    remove_klusterlet_config
    cleanup_addon_namespace
    verify_cleanup
    display_summary
    
    log_success "ACM Hub cleanup completed!"
}

# Run main function
main "$@"
