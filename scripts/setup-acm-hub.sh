#!/bin/bash

# Script: setup-acm-hub.sh
# Description: Prepare ACM Hub for MCE Integration (Step 1 from discovering_hostedclusters.md)
# Author: Generated automation script
# Usage: ./setup-acm-hub.sh

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
    
    # Check for MultiClusterEngine namespace (should exist with ACM)
    if ! oc get namespace multicluster-engine &> /dev/null; then
        log_error "MultiClusterEngine namespace not found"
        log_error "ACM installation appears incomplete"
        exit 1
    fi
    
    # Verify ACM hub cluster capabilities
    if ! oc get clustermanagementaddon &> /dev/null; then
        log_error "ClusterManagementAddOn CRD not found"
        log_error "ACM installation appears incomplete or not ready"
        exit 1
    fi
    
    log_success "ACM installation verified"
    log_success "Prerequisites check passed"
}

# Verify current addon state
verify_addon_state() {
    log_info "Verifying current addon state on local-cluster..."
    
    if oc get managedclusteraddon -n local-cluster &> /dev/null; then
        log_info "Current addons in local-cluster namespace:"
        oc get managedclusteraddon -n local-cluster
    else
        log_warning "No managedclusteraddon resources found in local-cluster namespace"
    fi
}

# Create addon deployment configuration
create_addon_deployment_config() {
    log_info "Creating AddOnDeploymentConfig..."
    
    cat <<EOF | oc apply -f -
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: AddOnDeploymentConfig
metadata:
  name: addon-ns-config
  namespace: multicluster-engine
spec:
  agentInstallNamespace: open-cluster-management-agent-addon-discovery
EOF
    
    if [ $? -eq 0 ]; then
        log_success "AddOnDeploymentConfig created successfully"
    else
        log_error "Failed to create AddOnDeploymentConfig"
        exit 1
    fi
}

# Update ClusterManagementAddOn resources
update_cluster_management_addons() {
    log_info "Updating ClusterManagementAddOn resources..."
    
    # Work Manager Addon
    log_info "Updating work-manager addon..."
    cat <<EOF | oc apply -f -
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: work-manager
spec:
  addOnMeta:
    displayName: work-manager
  installStrategy:
    placements:
    - name: global
      namespace: open-cluster-management-global-set
      rolloutStrategy:
        type: All
      configs:
      - group: addon.open-cluster-management.io
        name: addon-ns-config
        namespace: multicluster-engine
        resource: addondeploymentconfigs
    type: Placements
EOF
    
    # Managed Service Account Addon
    log_info "Updating managed-serviceaccount addon..."
    cat <<EOF | oc apply -f -
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: managed-serviceaccount
spec:
  addOnMeta:
    displayName: managed-serviceaccount
  installStrategy:
    placements:
    - name: global
      namespace: open-cluster-management-global-set
      rolloutStrategy:
        type: All
      configs:
      - group: addon.open-cluster-management.io
        name: addon-ns-config
        namespace: multicluster-engine
        resource: addondeploymentconfigs
    type: Placements
EOF
    
    # Cluster Proxy Addon
    log_info "Updating cluster-proxy addon..."
    cat <<EOF | oc apply -f -
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: cluster-proxy
spec:
  addOnMeta:
    displayName: cluster-proxy
  installStrategy:
    placements:
    - name: global
      namespace: open-cluster-management-global-set
      rolloutStrategy:
        type: All
      configs:
      - group: addon.open-cluster-management.io
        name: addon-ns-config
        namespace: multicluster-engine
        resource: addondeploymentconfigs
    type: Placements
EOF
    
    log_success "ClusterManagementAddOn resources updated successfully"
}

# Create Klusterlet configuration
create_klusterlet_config() {
    log_info "Creating KlusterletConfig..."
    
    cat <<EOF | oc apply -f -
apiVersion: config.open-cluster-management.io/v1alpha1
kind: KlusterletConfig
metadata:
  name: mce-import-klusterlet-config
spec:
  installMode:
    type: noOperator
    noOperator:
      postfix: mce-import
EOF
    
    if [ $? -eq 0 ]; then
        log_success "KlusterletConfig created successfully"
    else
        log_error "Failed to create KlusterletConfig"
        exit 1
    fi
}

# Verify configuration
verify_configuration() {
    log_info "Verifying configuration..."
    
    # Wait a bit for deployments to be created
    log_info "Waiting 30 seconds for deployments to be created..."
    sleep 30
    
    # Check if the namespace exists
    if oc get namespace open-cluster-management-agent-addon-discovery &> /dev/null; then
        log_info "Checking deployments in open-cluster-management-agent-addon-discovery namespace:"
        oc get deployment -n open-cluster-management-agent-addon-discovery
        
        # Check if expected deployments are ready
        expected_deployments=("cluster-proxy-proxy-agent" "klusterlet-addon-workmgr" "managed-serviceaccount-addon-agent")
        for deployment in "${expected_deployments[@]}"; do
            if oc get deployment "$deployment" -n open-cluster-management-agent-addon-discovery &> /dev/null; then
                ready=$(oc get deployment "$deployment" -n open-cluster-management-agent-addon-discovery -o jsonpath='{.status.readyReplicas}')
                desired=$(oc get deployment "$deployment" -n open-cluster-management-agent-addon-discovery -o jsonpath='{.spec.replicas}')
                if [ "$ready" = "$desired" ]; then
                    log_success "Deployment $deployment is ready ($ready/$desired)"
                else
                    log_warning "Deployment $deployment is not fully ready ($ready/$desired)"
                fi
            else
                log_warning "Expected deployment $deployment not found"
            fi
        done
    else
        log_warning "Namespace open-cluster-management-agent-addon-discovery not found yet"
        log_info "This is normal if addons haven't been deployed to any managed clusters yet"
    fi
}

# Main execution
main() {
    log_info "Starting ACM Hub setup for MCE integration..."
    
    check_prerequisites
    verify_addon_state
    create_addon_deployment_config
    update_cluster_management_addons
    create_klusterlet_config
    verify_configuration
    
    log_success "ACM Hub setup completed successfully!"
    log_info "Next steps:"
    log_info "1. Import your MCE clusters using the import-mce-cluster.sh script"
    log_info "2. Enable HyperShift addon using the enable-hypershift-addon.sh script"
    log_info "3. Set up auto-import policy using the setup-autoimport-policy.sh script"
}

# Run main function
main "$@"
