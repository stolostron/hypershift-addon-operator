#!/bin/bash

# Script: import-mce-cluster.sh
# Description: Import MCE Clusters into ACM (Step 2 from discovering_hostedclusters.md)
# Author: Generated automation script
# Usage: ./import-mce-cluster.sh <MCE_CLUSTER_NAME> <KUBECONFIG_FILE>

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
    echo "Usage: $0 <MCE_CLUSTER_NAME> <KUBECONFIG_FILE>"
    echo ""
    echo "Arguments:"
    echo "  MCE_CLUSTER_NAME     Name for the MCE cluster in ACM"
    echo "  KUBECONFIG_FILE      Path to kubeconfig file for the MCE cluster"
    echo ""
    echo "Examples:"
    echo "  $0 mce-cluster-1 /path/to/mce-cluster-1-kubeconfig"
    echo "  $0 prod-mce ~/.kube/prod-mce-config"
    echo ""
    echo "This script will:"
    echo "  1. Create a ManagedCluster resource with the specified name"
    echo "  2. Create an auto-import secret using the provided kubeconfig"
    echo "  3. Monitor the import progress until completion"
}

# Check prerequisites
check_prerequisites() {
    local kubeconfig_file="$1"
    
    log_info "Checking prerequisites..."
    
    # Check if oc command is available
    if ! command -v oc &> /dev/null; then
        log_error "oc CLI tool is not installed or not in PATH"
        exit 1
    fi
    
    # Check if logged into OpenShift (ACM Hub)
    if ! oc whoami &> /dev/null; then
        log_error "Not logged into OpenShift. Please run 'oc login' first"
        exit 1
    fi
    
    # Check if user has cluster-admin privileges on ACM Hub
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
    
    # Check if kubeconfig file exists
    if [ ! -f "$kubeconfig_file" ]; then
        log_error "Kubeconfig file not found: $kubeconfig_file"
        exit 1
    fi
    
    # Check if required resources exist
    if ! oc get klusterletconfig mce-import-klusterlet-config &> /dev/null; then
        log_error "KlusterletConfig 'mce-import-klusterlet-config' not found"
        log_error "Please run setup-acm-hub.sh first"
        exit 1
    fi
    
    log_success "Prerequisites check passed"
}

# Create ManagedCluster resource
create_managed_cluster() {
    local cluster_name="$1"
    
    log_info "Creating ManagedCluster resource for '$cluster_name'..."
    
    cat <<EOF | oc apply -f -
apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  annotations:
    agent.open-cluster-management.io/klusterlet-config: mce-import-klusterlet-config
  labels:
    cloud: auto-detect
    vendor: auto-detect
  name: $cluster_name
spec:
  hubAcceptsClient: true
  leaseDurationSeconds: 60
EOF
    
    if [ $? -eq 0 ]; then
        log_success "ManagedCluster '$cluster_name' created successfully"
    else
        log_error "Failed to create ManagedCluster '$cluster_name'"
        exit 1
    fi
}

# Create auto-import secret
create_auto_import_secret() {
    local cluster_name="$1"
    local kubeconfig_file="$2"
    
    log_info "Creating auto-import secret for cluster '$cluster_name'..."
    
    # Read the kubeconfig content
    local kubeconfig_content
    if ! kubeconfig_content=$(cat "$kubeconfig_file"); then
        log_error "Failed to read kubeconfig file: $kubeconfig_file"
        exit 1
    fi
    
    # Extract server URL from kubeconfig for verification
    local server_url
    server_url=$(oc --kubeconfig="$kubeconfig_file" config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null)
    
    if [ -z "$server_url" ]; then
        log_error "Failed to extract server URL from kubeconfig file"
        exit 1
    fi
    
    log_info "Detected server URL: $server_url"
    
    # Create secret using YAML with stringData
    cat <<EOF | oc apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: auto-import-secret
  namespace: $cluster_name
  labels:
    cluster.open-cluster-management.io/type: auto-import-secret
type: Opaque
stringData:
  autoImportRetry: "5"
  kubeconfig: |-
$(echo "$kubeconfig_content" | sed 's/^/    /')
EOF
    
    if [ $? -eq 0 ]; then
        log_success "Auto-import secret created successfully"
        log_info "The cluster should be imported automatically within a few minutes"
        log_info "Auto-import retry count: 5"
    else
        log_error "Failed to create auto-import secret"
        exit 1
    fi
}

# Monitor import progress
monitor_import_progress() {
    local cluster_name="$1"
    
    log_info "Monitoring import progress for cluster '$cluster_name'..."
    log_info "This may take a few minutes. Press Ctrl+C to stop monitoring."
    
    local max_attempts=60  # 10 minutes with 10-second intervals
    local attempt=0
    
    while [ $attempt -lt $max_attempts ]; do
        local status
        status=$(oc get managedcluster "$cluster_name" -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterJoined")].status}' 2>/dev/null || echo "Unknown")
        
        case $status in
            "True")
                log_success "Cluster '$cluster_name' has been successfully imported and joined!"
                break
                ;;
            "False")
                log_info "Cluster '$cluster_name' import in progress... (attempt $((attempt + 1))/$max_attempts)"
                ;;
            *)
                log_info "Waiting for cluster '$cluster_name' to start import process... (attempt $((attempt + 1))/$max_attempts)"
                ;;
        esac
        
        sleep 10
        ((attempt++))
    done
    
    if [ $attempt -eq $max_attempts ]; then
        log_warning "Import monitoring timeout reached"
        log_info "Check the cluster status manually with: oc get managedcluster $cluster_name"
    fi
}

# Verify import
verify_import() {
    local cluster_name="$1"
    
    log_info "Verifying import status..."
    
    if oc get managedcluster "$cluster_name" &> /dev/null; then
        log_info "ManagedCluster status for '$cluster_name':"
        oc get managedcluster "$cluster_name"
        
        # Check if cluster is available
        local available
        available=$(oc get managedcluster "$cluster_name" -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterConditionAvailable")].status}' 2>/dev/null || echo "Unknown")
        
        if [ "$available" = "True" ]; then
            log_success "Cluster '$cluster_name' is available and ready for addon installation"
        else
            log_warning "Cluster '$cluster_name' is not yet available. This may be normal if import is still in progress."
        fi
    else
        log_error "ManagedCluster '$cluster_name' not found"
        exit 1
    fi
}

# Main execution
main() {
    if [ $# -ne 2 ]; then
        usage
        exit 1
    fi
    
    local cluster_name="$1"
    local kubeconfig_file="$2"
    
    log_info "Starting MCE cluster import process for '$cluster_name'..."
    log_info "Using kubeconfig: $kubeconfig_file"
    
    check_prerequisites "$kubeconfig_file"
    create_managed_cluster "$cluster_name"
    create_auto_import_secret "$cluster_name" "$kubeconfig_file"
    monitor_import_progress "$cluster_name"
    verify_import "$cluster_name"
    
    log_success "MCE cluster import process completed!"
    log_info "Next steps:"
    log_info "1. Enable HyperShift addon using: ./enable-hypershift-addon.sh $cluster_name"
    log_info "2. Set up auto-import policy using: ./setup-autoimport-policy.sh"
}

# Run main function
main "$@"
