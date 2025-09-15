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
BACKUP_DIR="./mce-acm-backup-$(date +%Y%m%d-%H%M%S)"
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
    echo "Usage: $0 [--backup-dir <directory>] [--dry-run]"
    echo ""
    echo "Options:"
    echo "  --backup-dir <dir>   Directory to store backup files"
    echo "                       Default: ./mce-acm-backup-YYYYMMDD-HHMMSS"
    echo "  --dry-run           Show what would be done without making changes"
    echo ""
    echo "Examples:"
    echo "  $0                                    # Label resources and backup to default directory"
    echo "  $0 --backup-dir /tmp/my-backup       # Use custom backup directory"
    echo "  $0 --dry-run                         # Preview actions without execution"
    echo ""
    echo "This script will:"
    echo "  1. Label critical resources for backup with cluster.open-cluster-management.io/backup=true"
    echo "  2. Export labeled resources to YAML files for disaster recovery"
    echo "  3. Create a restore script for easy recovery"
}

# Parse command line arguments
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --backup-dir)
                BACKUP_DIR="$2"
                shift 2
                ;;
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

# Create backup directory
create_backup_directory() {
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: Would create backup directory: $BACKUP_DIR"
        return
    fi
    
    log_info "Creating backup directory: $BACKUP_DIR"
    
    if mkdir -p "$BACKUP_DIR"; then
        log_success "Backup directory created successfully"
    else
        log_error "Failed to create backup directory: $BACKUP_DIR"
        exit 1
    fi
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

# Export resource to file
export_resource() {
    local resource_type="$1"
    local resource_name="$2"
    local namespace="${3:-}"
    local filename="$4"
    
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: Would export $resource_type/$resource_name to $filename"
        return 0
    fi
    
    local export_cmd="oc get $resource_type $resource_name -o yaml"
    if [ -n "$namespace" ]; then
        export_cmd="oc get $resource_type $resource_name -n $namespace -o yaml"
    fi
    
    # Check if resource exists
    local check_cmd="oc get $resource_type $resource_name"
    if [ -n "$namespace" ]; then
        check_cmd="oc get $resource_type $resource_name -n $namespace"
    fi
    
    if $check_cmd &> /dev/null; then
        if $export_cmd > "$BACKUP_DIR/$filename" 2>/dev/null; then
            log_success "Exported $resource_type/$resource_name to $filename"
            return 0
        else
            log_error "Failed to export $resource_type/$resource_name"
            return 1
        fi
    else
        log_warning "$resource_type/$resource_name not found - skipping export"
        return 0
    fi
}

# Label and backup configuration resources
backup_configuration_resources() {
    log_info "=== Labeling and Backing Up Configuration Resources ==="
    
    # AddOnDeploymentConfig resources
    log_info "Processing AddOnDeploymentConfig resources..."
    label_resource "addondeploymentconfig" "addon-ns-config" "multicluster-engine"
    export_resource "addondeploymentconfig" "addon-ns-config" "multicluster-engine" "addon-ns-config.yaml"
    
    label_resource "addondeploymentconfig" "hypershift-addon-deploy-config" "multicluster-engine"
    export_resource "addondeploymentconfig" "hypershift-addon-deploy-config" "multicluster-engine" "hypershift-addon-deploy-config.yaml"
    
    # KlusterletConfig
    log_info "Processing KlusterletConfig..."
    label_resource "klusterletconfig" "mce-import-klusterlet-config"
    export_resource "klusterletconfig" "mce-import-klusterlet-config" "" "mce-import-klusterlet-config.yaml"
}

# Label and backup addon resources
backup_addon_resources() {
    log_info "=== Labeling and Backing Up Addon Resources ==="
    
    local addons=("work-manager" "cluster-proxy" "managed-serviceaccount" "hypershift-addon")
    
    for addon in "${addons[@]}"; do
        log_info "Processing ClusterManagementAddOn: $addon"
        label_resource "clustermanagementaddon" "$addon"
        export_resource "clustermanagementaddon" "$addon" "" "clustermanagementaddon-$addon.yaml"
    done
}

# Label and backup policy resources
backup_policy_resources() {
    log_info "=== Labeling and Backing Up Policy Resources ==="
    
    # Auto-import policy
    log_info "Processing auto-import policy..."
    label_resource "policy" "policy-mce-hcp-autoimport" "open-cluster-management-global-set"
    export_resource "policy" "policy-mce-hcp-autoimport" "open-cluster-management-global-set" "policy-mce-hcp-autoimport.yaml"
    
    # Placement
    label_resource "placement" "policy-mce-hcp-autoimport-placement" "open-cluster-management-global-set"
    export_resource "placement" "policy-mce-hcp-autoimport-placement" "open-cluster-management-global-set" "policy-mce-hcp-autoimport-placement.yaml"
    
    # PlacementBinding
    label_resource "placementbinding" "policy-mce-hcp-autoimport-placement-binding" "open-cluster-management-global-set"
    export_resource "placementbinding" "policy-mce-hcp-autoimport-placement-binding" "open-cluster-management-global-set" "policy-mce-hcp-autoimport-placement-binding.yaml"
    
    # Discovery config ConfigMap
    label_resource "configmap" "discovery-config" "open-cluster-management-global-set"
    export_resource "configmap" "discovery-config" "open-cluster-management-global-set" "discovery-config.yaml"
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
        
        # Label and export ManagedCluster
        label_resource "managedcluster" "$cluster"
        export_resource "managedcluster" "$cluster" "" "managedcluster-$cluster.yaml"
        
        # Export ManagedClusterAddOns for this cluster
        local cluster_addons
        cluster_addons=$(oc get managedclusteraddon -n "$cluster" --no-headers 2>/dev/null | awk '{print $1}' | tr '\n' ' ' || echo "")
        
        if [ -n "$cluster_addons" ]; then
            log_info "  Found addons for cluster $cluster: $cluster_addons"
            
            for addon in $cluster_addons; do
                # Note: We don't label ManagedClusterAddOn resources as they are recreated automatically
                export_resource "managedclusteraddon" "$addon" "$cluster" "managedclusteraddon-$cluster-$addon.yaml"
            done
        fi
    done
}

# Create restore script
create_restore_script() {
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: Would create restore script"
        return
    fi
    
    log_info "Creating restore script..."
    
    local restore_script="$BACKUP_DIR/restore.sh"
    
    cat > "$restore_script" <<'EOF'
#!/bin/bash

# MCE-ACM Integration Restore Script
# Generated by backup-mce-resources.sh

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

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
    
    if ! command -v oc &> /dev/null; then
        log_error "oc CLI tool is not installed or not in PATH"
        exit 1
    fi
    
    if ! oc whoami &> /dev/null; then
        log_error "Not logged into OpenShift. Please run 'oc login' first"
        exit 1
    fi
    
    if ! oc auth can-i '*' '*' --all-namespaces &> /dev/null; then
        log_error "Insufficient privileges. cluster-admin access required"
        exit 1
    fi
    
    log_success "Prerequisites check passed"
}

# Restore resources
restore_resources() {
    log_info "Starting resource restoration..."
    
    local backup_dir="$(dirname "$0")"
    local restored=0
    local failed=0
    
    # Restore in dependency order
    local restore_order=(
        "mce-import-klusterlet-config.yaml"
        "addon-ns-config.yaml"
        "hypershift-addon-deploy-config.yaml"
        "clustermanagementaddon-work-manager.yaml"
        "clustermanagementaddon-cluster-proxy.yaml"
        "clustermanagementaddon-managed-serviceaccount.yaml"
        "clustermanagementaddon-hypershift-addon.yaml"
        "discovery-config.yaml"
        "policy-mce-hcp-autoimport-placement.yaml"
        "policy-mce-hcp-autoimport.yaml"
        "policy-mce-hcp-autoimport-placement-binding.yaml"
    )
    
    for file in "${restore_order[@]}"; do
        if [ -f "$backup_dir/$file" ]; then
            log_info "Restoring $file..."
            if oc apply -f "$backup_dir/$file" &> /dev/null; then
                log_success "Restored $file"
                ((restored++))
            else
                log_error "Failed to restore $file"
                ((failed++))
            fi
        else
            log_warning "File $file not found - skipping"
        fi
    done
    
    # Restore ManagedCluster resources
    for file in "$backup_dir"/managedcluster-*.yaml; do
        if [ -f "$file" ]; then
            local filename=$(basename "$file")
            log_info "Restoring $filename..."
            if oc apply -f "$file" &> /dev/null; then
                log_success "Restored $filename"
                ((restored++))
            else
                log_error "Failed to restore $filename"
                ((failed++))
            fi
        fi
    done
    
    log_info "Restoration completed: $restored succeeded, $failed failed"
    
    if [ $failed -gt 0 ]; then
        log_warning "Some resources failed to restore. Check the errors above."
        log_info "You may need to manually import MCE clusters and re-enable addons."
    else
        log_success "All resources restored successfully!"
        log_info "MCE clusters should automatically reconnect within a few minutes."
    fi
}

# Post-restore verification
post_restore_verification() {
    log_info "Performing post-restore verification..."
    
    # Wait a bit for resources to be processed
    log_info "Waiting 30 seconds for resources to be processed..."
    sleep 30
    
    # Check key resources
    local checks_passed=0
    local total_checks=5
    
    if oc get klusterletconfig mce-import-klusterlet-config &> /dev/null; then
        log_success "KlusterletConfig restored"
        ((checks_passed++))
    else
        log_error "KlusterletConfig not found"
    fi
    
    if oc get addondeploymentconfig addon-ns-config -n multicluster-engine &> /dev/null; then
        log_success "AddOnDeploymentConfig restored"
        ((checks_passed++))
    else
        log_error "AddOnDeploymentConfig not found"
    fi
    
    if oc get clustermanagementaddon work-manager &> /dev/null; then
        log_success "ClusterManagementAddOns restored"
        ((checks_passed++))
    else
        log_error "ClusterManagementAddOns not found"
    fi
    
    if oc get policy policy-mce-hcp-autoimport -n open-cluster-management-global-set &> /dev/null; then
        log_success "Auto-import policy restored"
        ((checks_passed++))
    else
        log_error "Auto-import policy not found"
    fi
    
    local mce_clusters=$(oc get managedcluster --no-headers | grep -v local-cluster | wc -l)
    if [ "$mce_clusters" -gt 0 ]; then
        log_success "MCE managed clusters found: $mce_clusters"
        ((checks_passed++))
    else
        log_warning "No MCE managed clusters found"
    fi
    
    log_info "Post-restore verification: $checks_passed/$total_checks checks passed"
    
    if [ $checks_passed -eq $total_checks ]; then
        log_success "All verification checks passed!"
    else
        log_warning "Some verification checks failed. Manual intervention may be required."
    fi
}

# Main execution
main() {
    echo "MCE-ACM Integration Restore Script"
    echo "=================================="
    
    check_prerequisites
    restore_resources
    post_restore_verification
    
    echo ""
    log_info "Restore process completed!"
    log_info "Next steps:"
    log_info "1. Wait for MCE clusters to reconnect (may take a few minutes)"
    log_info "2. Verify cluster status: oc get managedcluster"
    log_info "3. Check addon status: oc get managedclusteraddon --all-namespaces"
    log_info "4. Run verification script: ./verify-mce-integration.sh"
}

main "$@"
EOF
    
    chmod +x "$restore_script"
    log_success "Restore script created: $restore_script"
}

# Create backup manifest
create_backup_manifest() {
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: Would create backup manifest"
        return
    fi
    
    log_info "Creating backup manifest..."
    
    local manifest_file="$BACKUP_DIR/backup-manifest.txt"
    
    cat > "$manifest_file" <<EOF
MCE-ACM Integration Backup Manifest
===================================

Backup Date: $(date)
Backup Directory: $BACKUP_DIR
OpenShift Cluster: $(oc whoami --show-server)
User: $(oc whoami)

Files in this backup:
EOF
    
    # List all YAML files in the backup directory
    if ls "$BACKUP_DIR"/*.yaml &> /dev/null; then
        for file in "$BACKUP_DIR"/*.yaml; do
            local filename=$(basename "$file")
            echo "  - $filename" >> "$manifest_file"
        done
    fi
    
    cat >> "$manifest_file" <<EOF

Scripts:
  - restore.sh (restoration script)
  - backup-manifest.txt (this file)

Instructions:
1. To restore from this backup, run: ./restore.sh
2. Ensure you are logged into the target OpenShift cluster first
3. The restore script will apply resources in the correct dependency order
4. After restore, wait for clusters to reconnect and verify with verify-mce-integration.sh

Important Notes:
- This backup contains configuration resources only
- Managed clusters will need to reconnect automatically after restore
- Some resources (like ManagedClusterAddOn) are recreated automatically
- Secrets and credentials are not included in this backup for security reasons
EOF
    
    log_success "Backup manifest created: $manifest_file"
}

# Display backup summary
display_backup_summary() {
    log_info "=== Backup Summary ==="
    
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: No actual backup was performed"
        return
    fi
    
    local file_count
    file_count=$(find "$BACKUP_DIR" -name "*.yaml" | wc -l)
    
    log_info "Backup completed successfully!"
    log_info "Backup location: $BACKUP_DIR"
    log_info "YAML files backed up: $file_count"
    log_info "Restore script: $BACKUP_DIR/restore.sh"
    log_info "Manifest file: $BACKUP_DIR/backup-manifest.txt"
    
    echo ""
    log_info "Backup contents:"
    ls -la "$BACKUP_DIR"
    
    echo ""
    log_info "To restore from this backup:"
    echo "  1. cd $BACKUP_DIR"
    echo "  2. oc login <target-cluster>"
    echo "  3. ./restore.sh"
    
    echo ""
    log_info "Resources have been labeled with 'cluster.open-cluster-management.io/backup=true'"
    log_info "This ensures they will be included in ACM's backup operations (if configured)"
}

# Main execution
main() {
    parse_arguments "$@"
    
    log_info "Starting MCE-ACM integration backup process..."
    
    if [ "$DRY_RUN" = true ]; then
        log_warning "DRY RUN MODE: No changes will be made"
    fi
    
    check_prerequisites
    create_backup_directory
    backup_configuration_resources
    backup_addon_resources
    backup_policy_resources
    backup_managed_cluster_configs
    create_restore_script
    create_backup_manifest
    display_backup_summary
    
    if [ "$DRY_RUN" = true ]; then
        log_success "DRY RUN completed - no actual backup was performed"
    else
        log_success "Backup process completed successfully!"
        log_info "Your MCE-ACM integration is now backed up and ready for disaster recovery"
    fi
}

# Run main function
main "$@"
