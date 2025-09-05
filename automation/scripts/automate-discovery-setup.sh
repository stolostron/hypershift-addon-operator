#!/bin/bash

# automate-discovery-setup.sh - Complete automation of MCE hosted cluster discovery
# This script orchestrates all steps from docs/discovering_hostedclusters.md

set -euo pipefail

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DRY_RUN="${DRY_RUN:-false}"
SKIP_HUB_SETUP="${SKIP_HUB_SETUP:-false}"
SKIP_AUTO_IMPORT_POLICY="${SKIP_AUTO_IMPORT_POLICY:-false}"
CONFIG_FILE="${CONFIG_FILE:-}"

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

Complete automation of MCE hosted cluster discovery setup.

Optional Arguments:
    --config FILE              Configuration file with cluster definitions
    --skip-hub-setup           Skip ACM hub configuration (if already done)
    --skip-auto-import-policy  Skip auto-import policy deployment
    --dry-run                  Show what would be done without making changes
    --help, -h                 Show this help message

Environment Variables:
    DRY_RUN                   Enable dry run mode (default: false)
    SKIP_HUB_SETUP           Skip hub setup (default: false)
    SKIP_AUTO_IMPORT_POLICY  Skip policy setup (default: false)
    CONFIG_FILE              Configuration file path

Configuration File Format (YAML):
    clusters:
      - name: mce-a
        apiUrl: https://api.mce-a.example.com:6443
        kubeconfig: /path/to/kubeconfig  # optional
        token: <token>                   # optional
      - name: mce-b
        apiUrl: https://api.mce-b.example.com:6443
        autoImportSecret: /path/to/secret.yaml
    
    settings:
      filterConfig: "prod-"              # optional cluster name filter
      timeout: 600                       # optional timeout in seconds

Interactive Mode:
    If no config file is provided, the script will prompt for cluster information.

Examples:
    # Interactive setup
    $0
    
    # With configuration file
    $0 --config clusters.yaml
    
    # Skip hub setup (if already configured)
    $0 --config clusters.yaml --skip-hub-setup
    
    # Dry run to see what would be done
    $0 --config clusters.yaml --dry-run
EOF
}

load_config() {
    if [[ -n "$CONFIG_FILE" ]]; then
        if [[ ! -f "$CONFIG_FILE" ]]; then
            log_error "Configuration file not found: $CONFIG_FILE"
            exit 1
        fi
        log_info "Loading configuration from: $CONFIG_FILE"
        # Note: This is a simplified version. In production, you'd want to use yq or similar
        log_warn "Configuration file parsing not fully implemented in this example"
        log_warn "Please use interactive mode or modify this script for your YAML parser"
    fi
}

interactive_cluster_input() {
    local clusters=()
    
    echo "=========================================="
    echo "Interactive Cluster Configuration"
    echo "=========================================="
    
    while true; do
        echo ""
        echo "Enter MCE cluster information (or 'done' to finish):"
        
        read -p "Cluster name: " cluster_name
        if [[ "$cluster_name" == "done" ]]; then
            break
        fi
        
        if [[ -z "$cluster_name" ]]; then
            log_warn "Cluster name cannot be empty"
            continue
        fi
        
        read -p "API URL: " api_url
        if [[ -z "$api_url" ]]; then
            log_warn "API URL cannot be empty"
            continue
        fi
        
        echo "Authentication method:"
        echo "1) Kubeconfig file"
        echo "2) Service account token"
        echo "3) Auto-import secret file"
        echo "4) Manual (create secret later)"
        read -p "Choose (1-4): " auth_method
        
        local auth_arg=""
        case $auth_method in
            1)
                read -p "Kubeconfig file path: " kubeconfig_path
                if [[ -n "$kubeconfig_path" ]] && [[ -f "$kubeconfig_path" ]]; then
                    auth_arg="--kubeconfig $kubeconfig_path"
                else
                    log_warn "Kubeconfig file not found, will use manual method"
                fi
                ;;
            2)
                read -s -p "Service account token: " token
                echo
                if [[ -n "$token" ]]; then
                    auth_arg="--token $token"
                fi
                ;;
            3)
                read -p "Auto-import secret file path: " secret_path
                if [[ -n "$secret_path" ]] && [[ -f "$secret_path" ]]; then
                    auth_arg="--auto-import-secret $secret_path"
                else
                    log_warn "Secret file not found, will use manual method"
                fi
                ;;
            4)
                log_info "Will use manual authentication method"
                ;;
            *)
                log_warn "Invalid choice, will use manual method"
                ;;
        esac
        
        clusters+=("$cluster_name|$api_url|$auth_arg")
        log_info "Added cluster: $cluster_name"
    done
    
    if [[ ${#clusters[@]} -eq 0 ]]; then
        log_error "No clusters configured"
        exit 1
    fi
    
    echo "${clusters[@]}"
}

setup_acm_hub() {
    log_step "Setting up ACM Hub configuration..."
    
    local cmd="$SCRIPT_DIR/setup-acm-hub.sh"
    if $DRY_RUN; then
        cmd="$cmd --dry-run"
    fi
    
    if ! bash "$cmd"; then
        log_error "ACM Hub setup failed"
        exit 1
    fi
    
    log_info "ACM Hub setup completed"
}

import_mce_clusters() {
    local clusters=("$@")
    
    log_step "Importing MCE clusters..."
    
    local cluster_names=()
    for cluster_config in "${clusters[@]}"; do
        IFS='|' read -r cluster_name api_url auth_args <<< "$cluster_config"
        
        log_info "Importing cluster: $cluster_name"
        
        local cmd="$SCRIPT_DIR/import-mce-cluster.sh --cluster-name $cluster_name --api-url $api_url"
        if [[ -n "$auth_args" ]]; then
            cmd="$cmd $auth_args"
        fi
        if $DRY_RUN; then
            cmd="$cmd --dry-run"
        fi
        
        if ! bash $cmd; then
            log_error "Failed to import cluster: $cluster_name"
            exit 1
        fi
        
        cluster_names+=("$cluster_name")
    done
    
    log_info "All MCE clusters imported successfully"
    echo "${cluster_names[@]}"
}

enable_hypershift_addons() {
    local cluster_names=("$@")
    
    log_step "Enabling hypershift addons..."
    
    local cluster_list
    cluster_list=$(IFS=','; echo "${cluster_names[*]}")
    
    local cmd="$SCRIPT_DIR/enable-hypershift-addon.sh --clusters $cluster_list"
    if $DRY_RUN; then
        cmd="$cmd --dry-run"
    fi
    
    if ! bash "$cmd"; then
        log_error "Failed to enable hypershift addons"
        exit 1
    fi
    
    log_info "Hypershift addons enabled successfully"
}

setup_auto_import_policy() {
    log_step "Setting up auto-import policy..."
    
    local cmd="$SCRIPT_DIR/setup-auto-import-policy.sh"
    if $DRY_RUN; then
        cmd="$cmd --dry-run"
    fi
    
    if ! bash "$cmd"; then
        log_error "Auto-import policy setup failed"
        exit 1
    fi
    
    log_info "Auto-import policy setup completed"
}

show_final_status() {
    log_info "=========================================="
    log_info "Discovery Setup Complete!"
    log_info "=========================================="
    
    if ! $DRY_RUN; then
        echo ""
        log_info "Current managed clusters:"
        oc get managedcluster -o wide 2>/dev/null || log_warn "Could not list managed clusters"
        
        echo ""
        log_info "Discovered clusters (may take a few minutes to appear):"
        oc get discoveredcluster --all-namespaces 2>/dev/null || log_warn "Could not list discovered clusters"
        
        echo ""
        log_info "Policy status:"
        oc get policy -n open-cluster-management-global-set 2>/dev/null || log_warn "Could not list policies"
    fi
    
    echo ""
    log_info "What happens next:"
    log_info "1. Hypershift addons will discover hosted clusters in MCE clusters"
    log_info "2. DiscoveredCluster resources will be created for each hosted cluster"
    log_info "3. Auto-import policy will trigger import of discovered clusters"
    log_info "4. Hosted clusters will appear as managed clusters in ACM"
    
    echo ""
    log_info "Monitoring commands:"
    log_info "  oc get discoveredcluster --all-namespaces"
    log_info "  oc get managedcluster"
    log_info "  oc get policy -n open-cluster-management-global-set"
}

main() {
    echo "=========================================="
    echo "MCE Hosted Cluster Discovery Automation"
    echo "=========================================="
    echo "Dry Run: $DRY_RUN"
    echo "Skip Hub Setup: $SKIP_HUB_SETUP"
    echo "Skip Auto-Import Policy: $SKIP_AUTO_IMPORT_POLICY"
    echo "=========================================="
    
    # Load configuration or use interactive input
    load_config
    
    local clusters
    if [[ -n "$CONFIG_FILE" ]]; then
        log_error "Configuration file parsing not implemented in this example"
        log_error "Please use interactive mode or implement YAML parsing"
        exit 1
    else
        clusters=($(interactive_cluster_input))
    fi
    
    # Step 1: Setup ACM Hub (if not skipped)
    if ! $SKIP_HUB_SETUP; then
        setup_acm_hub
    else
        log_info "Skipping ACM Hub setup (already configured)"
    fi
    
    # Step 2: Import MCE clusters
    local cluster_names
    cluster_names=($(import_mce_clusters "${clusters[@]}"))
    
    # Step 3: Enable hypershift addons
    enable_hypershift_addons "${cluster_names[@]}"
    
    # Step 4: Setup auto-import policy (if not skipped)
    if ! $SKIP_AUTO_IMPORT_POLICY; then
        setup_auto_import_policy
    else
        log_info "Skipping auto-import policy setup"
    fi
    
    # Step 5: Show final status
    show_final_status
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --config)
            CONFIG_FILE="$2"
            shift 2
            ;;
        --skip-hub-setup)
            SKIP_HUB_SETUP=true
            shift
            ;;
        --skip-auto-import-policy)
            SKIP_AUTO_IMPORT_POLICY=true
            shift
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
