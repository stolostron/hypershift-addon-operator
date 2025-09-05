#!/bin/bash

# setup-acm-hub.sh - Automate ACM Hub configuration for MCE discovery
# This script automates the manual steps from docs/discovering_hostedclusters.md

set -euo pipefail

# Configuration
ACM_NAMESPACE="${ACM_NAMESPACE:-multicluster-engine}"
ADDON_INSTALL_NAMESPACE="${ADDON_INSTALL_NAMESPACE:-open-cluster-management-agent-addon-discovery}"
BACKUP_ENABLED="${BACKUP_ENABLED:-true}"
DRY_RUN="${DRY_RUN:-false}"

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

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    if ! command -v oc &> /dev/null; then
        log_error "oc CLI is required but not installed"
        exit 1
    fi
    
    if ! oc auth can-i create addOnDeploymentConfig -n "$ACM_NAMESPACE" &> /dev/null; then
        log_error "Insufficient permissions to create resources in namespace $ACM_NAMESPACE"
        exit 1
    fi
    
    log_info "Prerequisites check passed"
}

create_addon_deployment_config() {
    log_info "Creating AddOnDeploymentConfig for addon namespace configuration..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create addon-ns-config AddOnDeploymentConfig"
        return
    fi
    
    cat <<EOF | oc apply -f -
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: AddOnDeploymentConfig
metadata:
  name: addon-ns-config
  namespace: $ACM_NAMESPACE
spec:
  agentInstallNamespace: $ADDON_INSTALL_NAMESPACE
EOF
    
    log_info "Created addon-ns-config AddOnDeploymentConfig"
}

update_cluster_management_addon() {
    local addon_name="$1"
    log_info "Updating ClusterManagementAddOn: $addon_name"
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would update ClusterManagementAddOn $addon_name"
        return
    fi
    
    # Check if addon exists
    if ! oc get clustermanagementaddon "$addon_name" &> /dev/null; then
        log_warn "ClusterManagementAddOn $addon_name not found, skipping"
        return
    fi
    
    # Add the config reference to the addon
    oc patch clustermanagementaddon "$addon_name" --type=merge -p '{
        "spec": {
            "installStrategy": {
                "placements": [{
                    "name": "global",
                    "namespace": "open-cluster-management-global-set",
                    "rolloutStrategy": {
                        "type": "All"
                    },
                    "configs": [{
                        "group": "addon.open-cluster-management.io",
                        "name": "addon-ns-config",
                        "namespace": "'$ACM_NAMESPACE'",
                        "resource": "addondeploymentconfigs"
                    }]
                }],
                "type": "Placements"
            }
        }
    }'
    
    log_info "Updated ClusterManagementAddOn: $addon_name"
}

create_klusterlet_config() {
    log_info "Creating KlusterletConfig for MCE import..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create mce-import-klusterlet-config KlusterletConfig"
        return
    fi
    
    cat <<EOF | oc apply -f -
kind: KlusterletConfig
apiVersion: config.open-cluster-management.io/v1alpha1
metadata:
  name: mce-import-klusterlet-config
spec:
  installMode:
    type: noOperator
    noOperator:
       postfix: mce-import
EOF
    
    log_info "Created mce-import-klusterlet-config KlusterletConfig"
}

apply_backup_labels() {
    if [[ "$BACKUP_ENABLED" != "true" ]]; then
        log_info "Backup labels disabled, skipping"
        return
    fi
    
    log_info "Applying backup labels for disaster recovery..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would apply backup labels to resources"
        return
    fi
    
    local resources=(
        "addondeploymentconfig/addon-ns-config -n $ACM_NAMESPACE"
        "addondeploymentconfig/hypershift-addon-deploy-config -n $ACM_NAMESPACE"
        "clustermanagementaddon/work-manager"
        "clustermanagementaddon/cluster-proxy"
        "clustermanagementaddon/managed-serviceaccount"
        "klusterletconfig/mce-import-klusterlet-config"
    )
    
    for resource in "${resources[@]}"; do
        if oc get $resource &> /dev/null; then
            oc label $resource cluster.open-cluster-management.io/backup=true --overwrite
            log_info "Applied backup label to: $resource"
        else
            log_warn "Resource not found, skipping backup label: $resource"
        fi
    done
}

setup_hypershift_addon_config() {
    log_info "Configuring hypershift addon for discovery mode..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would configure hypershift addon"
        return
    fi
    
    # Patch hypershift addon deployment config for discovery namespace
    oc patch addondeploymentconfig hypershift-addon-deploy-config -n "$ACM_NAMESPACE" --type=merge -p '{
        "spec": {
            "agentInstallNamespace": "'$ADDON_INSTALL_NAMESPACE'"
        }
    }' || log_warn "Failed to patch hypershift-addon-deploy-config (may not exist yet)"
    
    # Configure hypershift addon with discovery settings
    oc patch addondeploymentconfig hypershift-addon-deploy-config -n "$ACM_NAMESPACE" --type=merge -p '{
        "spec": {
            "customizedVariables": [
                {"name": "disableMetrics", "value": "true"},
                {"name": "disableHOManagement", "value": "true"}
            ]
        }
    }' || log_warn "Failed to patch hypershift addon customized variables"
    
    log_info "Configured hypershift addon for discovery mode"
}

main() {
    echo "=========================================="
    echo "ACM Hub Setup for MCE Discovery"
    echo "=========================================="
    echo "ACM Namespace: $ACM_NAMESPACE"
    echo "Addon Install Namespace: $ADDON_INSTALL_NAMESPACE"
    echo "Backup Enabled: $BACKUP_ENABLED"
    echo "Dry Run: $DRY_RUN"
    echo "=========================================="
    
    check_prerequisites
    
    log_info "Step 1: Creating AddOnDeploymentConfig..."
    create_addon_deployment_config
    
    log_info "Step 2: Updating ClusterManagementAddOns..."
    update_cluster_management_addon "work-manager"
    update_cluster_management_addon "managed-serviceaccount"
    update_cluster_management_addon "cluster-proxy"
    
    log_info "Step 3: Creating KlusterletConfig..."
    create_klusterlet_config
    
    log_info "Step 4: Configuring Hypershift Addon..."
    setup_hypershift_addon_config
    
    log_info "Step 5: Applying backup labels..."
    apply_backup_labels
    
    echo "=========================================="
    log_info "ACM Hub setup completed successfully!"
    echo "=========================================="
    
    log_info "Next steps:"
    log_info "1. Import MCE clusters using: ./import-mce-cluster.sh"
    log_info "2. Enable hypershift addon using: ./enable-hypershift-addon.sh"
    log_info "3. Deploy auto-import policy using: ./setup-auto-import-policy.sh"
}

# Show usage if help is requested
if [[ "${1:-}" == "--help" ]] || [[ "${1:-}" == "-h" ]]; then
    cat <<EOF
Usage: $0 [OPTIONS]

Automate ACM Hub configuration for MCE discovery.

Options:
    --dry-run           Show what would be done without making changes
    --help, -h          Show this help message

Environment Variables:
    ACM_NAMESPACE                   ACM installation namespace (default: multicluster-engine)
    ADDON_INSTALL_NAMESPACE         Addon installation namespace (default: open-cluster-management-agent-addon-discovery)
    BACKUP_ENABLED                  Enable backup labels (default: true)
    DRY_RUN                        Enable dry run mode (default: false)

Examples:
    # Standard setup
    $0
    
    # Dry run to see what would be done
    DRY_RUN=true $0
    
    # Custom namespace
    ACM_NAMESPACE=custom-mce $0
EOF
    exit 0
fi

# Handle command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

main
