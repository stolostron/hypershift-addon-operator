#!/bin/bash

# import-mce-cluster.sh - Automate MCE cluster import into ACM
# This script automates the MCE import steps from docs/discovering_hostedclusters.md

set -euo pipefail

# Configuration
ACM_NAMESPACE="${ACM_NAMESPACE:-multicluster-engine}"
DRY_RUN="${DRY_RUN:-false}"
CLUSTER_NAME=""
API_URL=""
AUTO_IMPORT_SECRET=""
KUBECONFIG_PATH=""
TOKEN=""

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
Usage: $0 --cluster-name CLUSTER_NAME --api-url API_URL [OPTIONS]

Import an MCE cluster into ACM hub as a managed cluster.

Required Arguments:
    --cluster-name NAME         Name for the managed cluster
    --api-url URL              API URL of the MCE cluster

Optional Arguments:
    --auto-import-secret PATH   Path to auto-import secret file
    --kubeconfig PATH          Path to MCE cluster kubeconfig
    --token TOKEN              Service account token for MCE cluster
    --dry-run                  Show what would be done without making changes
    --help, -h                 Show this help message

Environment Variables:
    ACM_NAMESPACE              ACM installation namespace (default: multicluster-engine)
    DRY_RUN                   Enable dry run mode (default: false)

Examples:
    # Basic import with manual secret creation
    $0 --cluster-name mce-a --api-url https://api.mce-a.example.com:6443
    
    # Import with auto-import secret
    $0 --cluster-name mce-a --api-url https://api.mce-a.example.com:6443 --auto-import-secret /path/to/secret.yaml
    
    # Import with kubeconfig
    $0 --cluster-name mce-a --api-url https://api.mce-a.example.com:6443 --kubeconfig /path/to/kubeconfig
EOF
}

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    if ! command -v oc &> /dev/null; then
        log_error "oc CLI is required but not installed"
        exit 1
    fi
    
    if ! oc auth can-i create managedcluster &> /dev/null; then
        log_error "Insufficient permissions to create ManagedCluster resources"
        exit 1
    fi
    
    if [[ -z "$CLUSTER_NAME" ]]; then
        log_error "Cluster name is required"
        usage
        exit 1
    fi
    
    if [[ -z "$API_URL" ]]; then
        log_error "API URL is required"
        usage
        exit 1
    fi
    
    log_info "Prerequisites check passed"
}

create_managed_cluster() {
    log_info "Creating ManagedCluster resource: $CLUSTER_NAME"
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create ManagedCluster $CLUSTER_NAME"
        return
    fi
    
    # Check if managed cluster already exists
    if oc get managedcluster "$CLUSTER_NAME" &> /dev/null; then
        log_warn "ManagedCluster $CLUSTER_NAME already exists, skipping creation"
        return
    fi
    
    cat <<EOF | oc apply -f -
apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  annotations:
    agent.open-cluster-management.io/klusterlet-config: mce-import-klusterlet-config
  labels:
    cloud: auto-detect
    vendor: auto-detect
  name: $CLUSTER_NAME
spec:
  hubAcceptsClient: true
  leaseDurationSeconds: 60
EOF
    
    log_info "Created ManagedCluster: $CLUSTER_NAME"
}

wait_for_namespace() {
    local namespace="$1"
    local timeout="${2:-300}" # 5 minutes default
    local elapsed=0
    
    log_info "Waiting for namespace $namespace to be created..."
    
    while [[ $elapsed -lt $timeout ]]; do
        if oc get namespace "$namespace" &> /dev/null; then
            log_info "Namespace $namespace is ready"
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done
    
    log_error "Timeout waiting for namespace $namespace"
    return 1
}

create_auto_import_secret_from_kubeconfig() {
    local kubeconfig_path="$1"
    local namespace="$CLUSTER_NAME"
    
    log_info "Creating auto-import secret from kubeconfig..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create auto-import secret from kubeconfig"
        return
    fi
    
    if [[ ! -f "$kubeconfig_path" ]]; then
        log_error "Kubeconfig file not found: $kubeconfig_path"
        return 1
    fi
    
    # Wait for namespace to be created
    wait_for_namespace "$namespace"
    
    oc create secret generic auto-import-secret \
        --from-file=kubeconfig="$kubeconfig_path" \
        --from-literal=autoImportRetry=5 \
        -n "$namespace"
    
    oc label secret auto-import-secret \
        cluster.open-cluster-management.io/type=auto-import \
        -n "$namespace"
    
    log_info "Created auto-import secret from kubeconfig"
}

create_auto_import_secret_from_token() {
    local token="$1"
    local namespace="$CLUSTER_NAME"
    
    log_info "Creating auto-import secret from token..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create auto-import secret from token"
        return
    fi
    
    # Wait for namespace to be created
    wait_for_namespace "$namespace"
    
    cat <<EOF | oc apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: auto-import-secret
  namespace: $namespace
  labels:
    cluster.open-cluster-management.io/type: auto-import
type: Opaque
data:
  autoImportRetry: $(echo -n "5" | base64 -w 0)
  token: $(echo -n "$token" | base64 -w 0)
  server: $(echo -n "$API_URL" | base64 -w 0)
EOF
    
    log_info "Created auto-import secret from token"
}

apply_auto_import_secret() {
    local secret_path="$1"
    local namespace="$CLUSTER_NAME"
    
    log_info "Applying auto-import secret from file..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would apply auto-import secret from $secret_path"
        return
    fi
    
    if [[ ! -f "$secret_path" ]]; then
        log_error "Auto-import secret file not found: $secret_path"
        return 1
    fi
    
    # Wait for namespace to be created
    wait_for_namespace "$namespace"
    
    oc apply -f "$secret_path" -n "$namespace"
    
    log_info "Applied auto-import secret from file"
}

wait_for_cluster_registration() {
    local cluster_name="$1"
    local timeout="${2:-600}" # 10 minutes default
    local elapsed=0
    
    log_info "Waiting for cluster $cluster_name to be registered..."
    
    while [[ $elapsed -lt $timeout ]]; do
        local status
        status=$(oc get managedcluster "$cluster_name" -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterJoined")].status}' 2>/dev/null || echo "")
        
        if [[ "$status" == "True" ]]; then
            log_info "Cluster $cluster_name successfully registered"
            return 0
        fi
        
        sleep 10
        elapsed=$((elapsed + 10))
        log_info "Still waiting... ($elapsed/${timeout}s)"
    done
    
    log_error "Timeout waiting for cluster $cluster_name registration"
    return 1
}

show_cluster_status() {
    log_info "Current managed cluster status:"
    oc get managedcluster "$CLUSTER_NAME" -o wide 2>/dev/null || log_warn "Failed to get cluster status"
}

main() {
    echo "=========================================="
    echo "MCE Cluster Import"
    echo "=========================================="
    echo "Cluster Name: $CLUSTER_NAME"
    echo "API URL: $API_URL"
    echo "ACM Namespace: $ACM_NAMESPACE"
    echo "Dry Run: $DRY_RUN"
    echo "=========================================="
    
    check_prerequisites
    
    log_info "Step 1: Creating ManagedCluster resource..."
    create_managed_cluster
    
    log_info "Step 2: Setting up auto-import..."
    if [[ -n "$AUTO_IMPORT_SECRET" ]]; then
        apply_auto_import_secret "$AUTO_IMPORT_SECRET"
    elif [[ -n "$KUBECONFIG_PATH" ]]; then
        create_auto_import_secret_from_kubeconfig "$KUBECONFIG_PATH"
    elif [[ -n "$TOKEN" ]]; then
        create_auto_import_secret_from_token "$TOKEN"
    else
        log_warn "No auto-import credentials provided"
        log_info "You need to manually create an auto-import secret in namespace: $CLUSTER_NAME"
        log_info "See: https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.10/html-single/clusters/index#importing-clusters-auto-import-secret"
    fi
    
    if [[ -n "$AUTO_IMPORT_SECRET" ]] || [[ -n "$KUBECONFIG_PATH" ]] || [[ -n "$TOKEN" ]]; then
        if ! $DRY_RUN; then
            log_info "Step 3: Waiting for cluster registration..."
            if wait_for_cluster_registration "$CLUSTER_NAME"; then
                show_cluster_status
            else
                log_error "Cluster registration failed or timed out"
                exit 1
            fi
        fi
    fi
    
    echo "=========================================="
    log_info "MCE cluster import completed!"
    echo "=========================================="
    
    if [[ -z "$AUTO_IMPORT_SECRET" ]] && [[ -z "$KUBECONFIG_PATH" ]] && [[ -z "$TOKEN" ]]; then
        log_info "Next steps:"
        log_info "1. Create auto-import secret in namespace: $CLUSTER_NAME"
        log_info "2. Wait for cluster to be registered"
        log_info "3. Enable hypershift addon using: ./enable-hypershift-addon.sh --clusters $CLUSTER_NAME"
    else
        log_info "Next step:"
        log_info "Enable hypershift addon using: ./enable-hypershift-addon.sh --clusters $CLUSTER_NAME"
    fi
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --cluster-name)
            CLUSTER_NAME="$2"
            shift 2
            ;;
        --api-url)
            API_URL="$2"
            shift 2
            ;;
        --auto-import-secret)
            AUTO_IMPORT_SECRET="$2"
            shift 2
            ;;
        --kubeconfig)
            KUBECONFIG_PATH="$2"
            shift 2
            ;;
        --token)
            TOKEN="$2"
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
