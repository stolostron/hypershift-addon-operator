#!/bin/bash

# Script: undo-import-mce-cluster.sh
# Description: Remove MCE clusters from ACM (Undo step 2)
# Author: Generated automation script
# Usage: ./undo-import-mce-cluster.sh <MCE_CLUSTER_NAME>

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
    echo "Usage: $0 <MCE_CLUSTER_NAME>"
    echo ""
    echo "Arguments:"
    echo "  MCE_CLUSTER_NAME     Name of the MCE cluster to remove from ACM"
    echo ""
    echo "Examples:"
    echo "  $0 mce-cluster-1"
    echo "  $0 prod-mce"
    echo ""
    echo "This script will:"
    echo "  1. Gracefully detach the managed cluster from ACM"
    echo "  2. Force cleanup if graceful detach fails"
    echo "  3. Remove any remaining manifestworks"
    echo "  4. Clean up cluster namespace"
}

# Check prerequisites
check_prerequisites() {
    local cluster_name="$1"
    
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
    
    # Check if cluster exists
    if ! oc get managedcluster "$cluster_name" &> /dev/null; then
        log_error "Managed cluster '$cluster_name' not found"
        log_info "Available clusters:"
        oc get managedcluster --no-headers | awk '{print "  - " $1}'
        exit 1
    fi
    
    log_success "Prerequisites check passed"
}

# Graceful cluster detach
graceful_detach() {
    local cluster_name="$1"
    
    log_info "Attempting graceful detach of cluster '$cluster_name'..."
    
    # Check if cluster is local-cluster (should not be detached)
    if [ "$cluster_name" = "local-cluster" ]; then
        log_error "Cannot detach local-cluster (ACM hub cluster itself)"
        exit 1
    fi
    
    # Initiate detach
    oc delete managedcluster "$cluster_name" --wait=false
    
    if [ $? -eq 0 ]; then
        log_info "Detach initiated for cluster '$cluster_name'"
        
        # Wait for graceful detach (up to 5 minutes)
        local max_attempts=30
        local attempt=0
        
        while [ $attempt -lt $max_attempts ]; do
            if ! oc get managedcluster "$cluster_name" &> /dev/null; then
                log_success "Cluster '$cluster_name' successfully detached"
                return 0
            fi
            
            log_info "Waiting for graceful detach... (attempt $((attempt + 1))/$max_attempts)"
            sleep 10
            ((attempt++))
        done
        
        log_warning "Graceful detach timeout reached. Will attempt force cleanup."
        return 1
    else
        log_error "Failed to initiate cluster detach"
        return 1
    fi
}

# Force cleanup stuck resources
force_cleanup() {
    local cluster_name="$1"
    
    log_info "Performing force cleanup for cluster '$cluster_name'..."
    
    # Clean up manifestworks first
    log_info "Cleaning up manifestworks..."
    local manifestworks
    manifestworks=$(oc get manifestwork -n "$cluster_name" --no-headers 2>/dev/null | awk '{print $1}' || echo "")
    
    if [ -n "$manifestworks" ]; then
        for mw in $manifestworks; do
            log_info "Removing finalizers from manifestwork '$mw'..."
            oc patch manifestwork "$mw" -n "$cluster_name" --type=merge -p '{"metadata":{"finalizers":[]}}' 2>/dev/null || true
        done
        
        # Wait a moment for manifestworks to be cleaned up
        sleep 5
    fi
    
    # Force remove managedcluster finalizers
    log_info "Removing finalizers from managed cluster..."
    if oc get managedcluster "$cluster_name" &> /dev/null; then
        oc patch managedcluster "$cluster_name" --type=merge -p '{"metadata":{"finalizers":[]}}' 2>/dev/null || true
        
        # Wait for cleanup
        sleep 5
        
        # Check if cluster is gone
        if ! oc get managedcluster "$cluster_name" &> /dev/null; then
            log_success "Managed cluster '$cluster_name' successfully removed"
        else
            log_warning "Managed cluster '$cluster_name' still exists after force cleanup"
        fi
    fi
}

# Clean up cluster namespace
cleanup_cluster_namespace() {
    local cluster_name="$1"
    
    log_info "Cleaning up cluster namespace '$cluster_name'..."
    
    if oc get namespace "$cluster_name" &> /dev/null; then
        # Check for any remaining resources
        local resources
        resources=$(oc get all -n "$cluster_name" --no-headers 2>/dev/null | wc -l || echo "0")
        
        if [ "$resources" -gt 0 ]; then
            log_info "Found $resources remaining resources in namespace '$cluster_name'"
            log_info "Attempting to clean up remaining resources..."
            
            # Force delete remaining resources
            oc delete all --all -n "$cluster_name" --force --grace-period=0 2>/dev/null || true
            
            # Wait a moment
            sleep 5
        fi
        
        # Try to delete the namespace
        log_info "Removing namespace '$cluster_name'..."
        oc delete namespace "$cluster_name" --wait=false 2>/dev/null || true
        
        # Wait for namespace deletion (up to 2 minutes)
        local max_attempts=12
        local attempt=0
        
        while [ $attempt -lt $max_attempts ]; do
            if ! oc get namespace "$cluster_name" &> /dev/null; then
                log_success "Namespace '$cluster_name' successfully removed"
                return 0
            fi
            
            log_info "Waiting for namespace deletion... (attempt $((attempt + 1))/$max_attempts)"
            sleep 10
            ((attempt++))
        done
        
        # Force cleanup namespace if stuck
        log_warning "Namespace deletion timeout. Attempting force cleanup..."
        oc patch namespace "$cluster_name" --type=merge -p '{"metadata":{"finalizers":[]}}' 2>/dev/null || true
        
        sleep 5
        
        if ! oc get namespace "$cluster_name" &> /dev/null; then
            log_success "Namespace '$cluster_name' force removed"
        else
            log_warning "Namespace '$cluster_name' still exists after force cleanup"
        fi
    else
        log_info "Namespace '$cluster_name' does not exist"
    fi
}

# Verify cleanup
verify_cleanup() {
    local cluster_name="$1"
    
    log_info "Verifying cleanup for cluster '$cluster_name'..."
    
    local cleanup_success=true
    
    # Check if managed cluster is gone
    if oc get managedcluster "$cluster_name" &> /dev/null; then
        log_warning "Managed cluster '$cluster_name' still exists"
        cleanup_success=false
    else
        log_success "Managed cluster '$cluster_name' successfully removed"
    fi
    
    # Check if namespace is gone
    if oc get namespace "$cluster_name" &> /dev/null; then
        log_warning "Namespace '$cluster_name' still exists"
        cleanup_success=false
    else
        log_success "Namespace '$cluster_name' successfully removed"
    fi
    
    # Check for any remaining manifestworks
    local remaining_mw
    remaining_mw=$(oc get manifestwork -A | grep "$cluster_name" | wc -l || echo "0")
    if [ "$remaining_mw" -gt 0 ]; then
        log_warning "Found $remaining_mw remaining manifestworks related to '$cluster_name'"
        cleanup_success=false
    else
        log_success "No remaining manifestworks found"
    fi
    
    if [ "$cleanup_success" = true ]; then
        log_success "Cluster '$cluster_name' cleanup completed successfully!"
    else
        log_warning "Some cleanup operations may require manual intervention"
        echo ""
        log_info "Manual cleanup commands if needed:"
        echo "  # Remove stuck managedcluster"
        echo "  oc patch managedcluster $cluster_name --type=merge -p '{\"metadata\":{\"finalizers\":[]}}'"
        echo ""
        echo "  # Remove stuck namespace"
        echo "  oc patch namespace $cluster_name --type=merge -p '{\"metadata\":{\"finalizers\":[]}}'"
        echo ""
        echo "  # List remaining manifestworks"
        echo "  oc get manifestwork -A | grep $cluster_name"
    fi
}

# Main execution
main() {
    if [ $# -ne 1 ]; then
        usage
        exit 1
    fi
    
    local cluster_name="$1"
    
    log_info "Starting removal process for MCE cluster '$cluster_name'..."
    
    check_prerequisites "$cluster_name"
    
    # Attempt graceful detach first
    if ! graceful_detach "$cluster_name"; then
        # If graceful detach fails, force cleanup
        force_cleanup "$cluster_name"
    fi
    
    cleanup_cluster_namespace "$cluster_name"
    verify_cleanup "$cluster_name"
    
    log_success "MCE cluster removal process completed!"
    log_info "Cluster '$cluster_name' has been removed from ACM management."
}

# Run main function
main "$@"
