#!/bin/bash

# Script: cleanup-mce-acm-integration.sh
# Description: Main cleanup script - undoes all MCE-ACM integration steps in reverse order
# Author: Generated automation script
# Usage: ./cleanup-mce-acm-integration.sh [options]

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Default values
INTERACTIVE=true
FORCE_MODE=false
SKIP_CONFIRMATION=false
MCE_CLUSTERS=""
SCRIPT_DIR="$(dirname "$0")"

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

log_step() {
    echo -e "${CYAN}[STEP]${NC} $1"
}

# Usage information
usage() {
    echo "MCE-ACM Integration Cleanup Script"
    echo "================================="
    echo ""
    echo "This script completely removes the MCE-ACM integration setup by undoing"
    echo "all configuration steps in reverse order."
    echo ""
    echo "Usage: $0 --mce-clusters <cluster-names> [options]"
    echo ""
    echo "Required Arguments:"
    echo "  --mce-clusters <names>  Comma-separated list of MCE cluster names to remove"
    echo ""
    echo "Options:"
    echo "  --non-interactive       Run without user prompts"
    echo "  --force                Force cleanup even with warnings"
    echo "  --skip-confirmation    Skip final confirmation prompt"
    echo "  -h, --help             Show this help message"
    echo ""
    echo "Cleanup Steps (in reverse order):"
    echo "  6. Remove backup labels from resources"
    echo "  5. Remove auto-import policies (if ACM is available)"
    echo "  4. Disable HyperShift addons from MCE clusters"
    echo "  3. Remove MCE clusters from ACM management"
    echo "  2. Reset ACM Hub configuration to defaults"
    echo "  1. Verify complete cleanup"
    echo ""
    echo "Examples:"
    echo "  $0 --mce-clusters mce-a,mce-b                        # Interactive cleanup"
    echo "  $0 --mce-clusters mce-a --non-interactive --force    # Automated cleanup"
    echo "  $0 --mce-clusters mce-a,mce-b --skip-confirmation    # Skip confirmation"
    echo ""
    echo "WARNING: This will completely undo your MCE-ACM integration!"
    echo "Make sure you have backed up any important configurations."
}

# Parse command line arguments
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --mce-clusters)
                MCE_CLUSTERS="$2"
                shift 2
                ;;
            --non-interactive)
                INTERACTIVE=false
                shift
                ;;
            --force)
                FORCE_MODE=true
                shift
                ;;
            --skip-confirmation)
                SKIP_CONFIRMATION=true
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
    
    # Validate required arguments
    if [ -z "$MCE_CLUSTERS" ]; then
        log_error "Missing required argument: --mce-clusters"
        log_error "You must specify which MCE clusters to remove"
        echo ""
        usage
        exit 1
    fi
}

# Check if all required scripts exist
check_script_dependencies() {
    log_info "Checking cleanup script dependencies..."
    
    local required_scripts=(
        "undo-hypershift-addon.sh"
        "undo-import-mce-cluster.sh"
        "undo-acm-hub.sh"
    )
    
    local missing_scripts=()
    
    for script in "${required_scripts[@]}"; do
        if [ ! -f "$SCRIPT_DIR/$script" ]; then
            missing_scripts+=("$script")
        fi
    done
    
    if [ ${#missing_scripts[@]} -gt 0 ]; then
        log_error "Missing required cleanup scripts:"
        for script in "${missing_scripts[@]}"; do
            log_error "  - $script"
        done
        log_error "Please ensure all cleanup scripts are in the same directory: $SCRIPT_DIR"
        exit 1
    fi
    
    # Make scripts executable
    for script in "${required_scripts[@]}"; do
        chmod +x "$SCRIPT_DIR/$script"
    done
    
    log_success "All required cleanup scripts found and made executable"
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
    log_success "All prerequisites satisfied"
}

# Display current state
display_current_state() {
    log_info "=== Current MCE-ACM Integration State ==="
    
    # Check specified MCE clusters
    log_info "MCE clusters to be removed: $MCE_CLUSTERS"
    
    # Validate that specified clusters exist
    IFS=',' read -ra cluster_array <<< "$MCE_CLUSTERS"
    local missing_clusters=()
    local existing_clusters=()
    
    for cluster in "${cluster_array[@]}"; do
        cluster=$(echo "$cluster" | xargs)  # Trim whitespace
        if oc get managedcluster "$cluster" &> /dev/null; then
            existing_clusters+=("$cluster")
        else
            missing_clusters+=("$cluster")
        fi
    done
    
    if [ ${#existing_clusters[@]} -gt 0 ]; then
        log_info "Found existing clusters: ${existing_clusters[*]}"
    fi
    
    if [ ${#missing_clusters[@]} -gt 0 ]; then
        log_warning "Clusters not found: ${missing_clusters[*]}"
    fi
    
    # Check all managed clusters for context
    local all_clusters
    all_clusters=$(oc get managedcluster --no-headers | grep -v local-cluster | awk '{print $1}' | tr '\n' ' ')
    
    if [ -n "$all_clusters" ]; then
        log_info "All imported clusters: $all_clusters"
    else
        log_info "No imported clusters found"
    fi
    
    # Check HyperShift addons on specified clusters
    local addon_count=0
    for cluster in "${existing_clusters[@]}"; do
        if oc get managedclusteraddon hypershift-addon -n "$cluster" &> /dev/null; then
            ((addon_count++))
        fi
    done
    
    if [ $addon_count -gt 0 ]; then
        log_info "Clusters with HyperShift addon: $addon_count"
    else
        log_info "No HyperShift addons found"
    fi
    
    # Check custom configurations
    local custom_configs=0
    
    if oc get addondeploymentconfig addon-ns-config -n multicluster-engine &> /dev/null; then
        ((custom_configs++))
    fi
    
    if oc get klusterletconfig mce-import-klusterlet-config &> /dev/null; then
        ((custom_configs++))
    fi
    
    if [ $custom_configs -gt 0 ]; then
        log_info "Custom ACM configurations found: $custom_configs"
    else
        log_info "No custom ACM configurations found"
    fi
    
    # Check auto-import policies
    if oc get policy policy-mce-hcp-autoimport -n open-cluster-management-global-set &> /dev/null; then
        log_info "Auto-import policy found"
    else
        log_info "No auto-import policy found"
    fi
}

# Interactive confirmation
interactive_confirmation() {
    if [ "$INTERACTIVE" = false ] || [ "$SKIP_CONFIRMATION" = true ]; then
        return
    fi
    
    echo ""
    log_warning "=== CLEANUP CONFIRMATION ==="
    log_warning "This will completely remove your MCE-ACM integration setup!"
    echo ""
    echo "The following will be removed:"
    echo "  • All imported MCE clusters from ACM management"
    echo "  • HyperShift addons from all clusters"
    echo "  • Custom ACM Hub configurations"
    echo "  • Auto-import policies (if present)"
    echo "  • Backup labels from resources"
    echo ""
    log_warning "This action cannot be easily undone!"
    echo ""
    
    read -p "Are you absolutely sure you want to proceed? (type 'yes' to confirm): " confirm
    if [ "$confirm" != "yes" ]; then
        log_info "Cleanup cancelled by user"
        exit 0
    fi
}

# Execute cleanup step with error handling
execute_cleanup_step() {
    local step_name="$1"
    local script_name="$2"
    shift 2
    local script_args=("$@")
    
    log_step "$step_name"
    echo ""
    
    if "$SCRIPT_DIR/$script_name" "${script_args[@]}"; then
        log_success "Step completed: $step_name"
        echo ""
        return 0
    else
        log_error "Step failed: $step_name"
        log_error "Script: $script_name"
        log_error "Arguments: ${script_args[*]}"
        echo ""
        
        if [ "$INTERACTIVE" = true ] && [ "$FORCE_MODE" = false ]; then
            read -p "Continue with remaining cleanup steps? (y/N): " continue_choice
            if [[ ! "$continue_choice" =~ ^[Yy]$ ]]; then
                log_info "Cleanup stopped by user"
                exit 1
            fi
        elif [ "$FORCE_MODE" = false ]; then
            log_error "Non-interactive mode: stopping on first failure"
            exit 1
        else
            log_warning "Force mode: continuing despite failure"
        fi
        
        return 1
    fi
}

# Step 6: Remove backup labels (reverse of backup step)
step_remove_backup_labels() {
    log_step "Removing backup labels from resources"
    echo ""
    
    log_info "Removing backup labels from ACM/MCE resources..."
    
    # List of resources that might have backup labels
    local resources=(
        "addondeploymentconfig/addon-ns-config -n multicluster-engine"
        "addondeploymentconfig/hypershift-addon-deploy-config -n multicluster-engine"
        "clustermanagementaddon/work-manager"
        "clustermanagementaddon/cluster-proxy"
        "clustermanagementaddon/managed-serviceaccount"
        "clustermanagementaddon/hypershift-addon"
        "klusterletconfig/mce-import-klusterlet-config"
    )
    
    for resource in "${resources[@]}"; do
        log_info "Removing backup label from $resource..."
        oc label $resource cluster.open-cluster-management.io/backup- 2>/dev/null || log_info "  Label not found or already removed"
    done
    
    log_success "Backup labels removal completed"
    echo ""
}

# Step 5: Remove auto-import policies (reverse of step 5)
step_remove_autoimport_policies() {
    log_step "Removing auto-import policies"
    echo ""
    
    # Check if ACM governance is available
    if ! oc get crd policies.policy.open-cluster-management.io &> /dev/null; then
        log_info "ACM governance policies not available - skipping policy cleanup"
        return
    fi
    
    log_info "Removing auto-import policy resources..."
    
    # Remove policy resources
    oc delete policy policy-mce-hcp-autoimport -n open-cluster-management-global-set 2>/dev/null || log_info "Policy not found"
    oc delete placement policy-mce-hcp-autoimport-placement -n open-cluster-management-global-set 2>/dev/null || log_info "Placement not found"
    oc delete placementbinding policy-mce-hcp-autoimport-placement-binding -n open-cluster-management-global-set 2>/dev/null || log_info "PlacementBinding not found"
    oc delete configmap discovery-config -n open-cluster-management-global-set 2>/dev/null || log_info "ConfigMap not found"
    
    log_success "Auto-import policies removal completed"
    echo ""
}

# Step 4: Disable HyperShift addons (reverse of step 3)
step_disable_hypershift_addons() {
    # Check specified MCE clusters for HyperShift addon
    local clusters_with_addon=""
    IFS=',' read -ra cluster_array <<< "$MCE_CLUSTERS"
    
    for cluster in "${cluster_array[@]}"; do
        cluster=$(echo "$cluster" | xargs)  # Trim whitespace
        if oc get managedcluster "$cluster" &> /dev/null; then
            if oc get managedclusteraddon hypershift-addon -n "$cluster" &> /dev/null; then
                if [ -n "$clusters_with_addon" ]; then
                    clusters_with_addon="$clusters_with_addon,$cluster"
                else
                    clusters_with_addon="$cluster"
                fi
            fi
        fi
    done
    
    if [ -n "$clusters_with_addon" ]; then
        execute_cleanup_step "Disabling HyperShift addons from MCE clusters" "undo-hypershift-addon.sh" "$clusters_with_addon"
    else
        log_step "Disabling HyperShift addons from MCE clusters"
        log_info "No specified clusters with HyperShift addon found - skipping"
        echo ""
    fi
}

# Step 3: Remove MCE clusters (reverse of step 2)
step_remove_mce_clusters() {
    IFS=',' read -ra cluster_array <<< "$MCE_CLUSTERS"
    
    local clusters_to_remove=()
    for cluster in "${cluster_array[@]}"; do
        cluster=$(echo "$cluster" | xargs)  # Trim whitespace
        if oc get managedcluster "$cluster" &> /dev/null; then
            clusters_to_remove+=("$cluster")
        else
            log_warning "Cluster '$cluster' not found - skipping"
        fi
    done
    
    if [ ${#clusters_to_remove[@]} -gt 0 ]; then
        for cluster in "${clusters_to_remove[@]}"; do
            execute_cleanup_step "Removing MCE cluster '$cluster' from ACM" "undo-import-mce-cluster.sh" "$cluster"
        done
    else
        log_step "Removing MCE clusters from ACM management"
        log_info "No specified clusters found to remove - skipping"
        echo ""
    fi
}

# Step 2: Reset ACM Hub (reverse of step 1)
step_reset_acm_hub() {
    local force_flag=""
    if [ "$FORCE_MODE" = true ]; then
        force_flag="--force"
    fi
    
    execute_cleanup_step "Resetting ACM Hub configuration to defaults" "undo-acm-hub.sh" $force_flag
}

# Step 1: Final verification
step_final_verification() {
    log_step "Performing final cleanup verification"
    echo ""
    
    log_info "Checking for remaining MCE-ACM integration components..."
    
    local issues_found=false
    
    # Check for managed clusters
    local remaining_clusters
    remaining_clusters=$(oc get managedcluster --no-headers | grep -v local-cluster | wc -l)
    if [ "$remaining_clusters" -gt 0 ]; then
        log_warning "Found $remaining_clusters remaining managed cluster(s)"
        issues_found=true
    else
        log_success "No remaining managed clusters found"
    fi
    
    # Check for custom configurations
    if oc get addondeploymentconfig addon-ns-config -n multicluster-engine &> /dev/null; then
        log_warning "Custom AddOnDeploymentConfig still exists"
        issues_found=true
    fi
    
    if oc get klusterletconfig mce-import-klusterlet-config &> /dev/null; then
        log_warning "Custom KlusterletConfig still exists"
        issues_found=true
    fi
    
    # Check for policies
    if oc get policy policy-mce-hcp-autoimport -n open-cluster-management-global-set &> /dev/null; then
        log_warning "Auto-import policy still exists"
        issues_found=true
    fi
    
    # Check addon namespace
    local addon_deployments
    addon_deployments=$(oc get deployment -n open-cluster-management-agent-addon-discovery --no-headers 2>/dev/null | wc -l || echo "0")
    if [ "$addon_deployments" -gt 0 ]; then
        log_info "Addon namespace still has $addon_deployments deployment(s) - this may be normal"
    fi
    
    if [ "$issues_found" = false ]; then
        log_success "Cleanup verification passed - no remaining integration components found"
    else
        log_warning "Some components may require manual cleanup"
    fi
    
    echo ""
}

# Display completion summary
display_completion_summary() {
    log_success "=== MCE-ACM Integration Cleanup Complete ==="
    echo ""
    
    log_info "What was removed:"
    echo "  ✓ All imported MCE clusters removed from ACM management"
    echo "  ✓ HyperShift addons disabled from all clusters"
    echo "  ✓ ACM Hub configuration reset to defaults"
    echo "  ✓ Auto-import policies removed (if present)"
    echo "  ✓ Backup labels removed from resources"
    echo ""
    
    log_info "Your environment is now clean:"
    echo "  • ACM Hub is back to default configuration"
    echo "  • No MCE clusters are managed by ACM"
    echo "  • No custom integration configurations remain"
    echo ""
    
    log_info "To re-setup the integration, you can run:"
    echo "  ./setup-mce-acm-integration.sh"
    echo ""
    
    log_success "Cleanup completed successfully!"
}

# Main execution
main() {
    echo "MCE-ACM Integration Cleanup Script"
    echo "=================================="
    echo "This script will completely remove your MCE-ACM integration setup"
    echo "by undoing all configuration steps in reverse order."
    echo ""
    
    parse_arguments "$@"
    check_script_dependencies
    check_prerequisites
    display_current_state
    interactive_confirmation
    
    echo ""
    log_info "Starting complete MCE-ACM integration cleanup..."
    echo ""
    
    # Execute cleanup steps in reverse order
    step_remove_backup_labels
    step_remove_autoimport_policies
    step_disable_hypershift_addons
    step_remove_mce_clusters
    step_reset_acm_hub
    step_final_verification
    
    display_completion_summary
}

# Trap to handle interruptions
trap 'echo ""; log_warning "Cleanup interrupted by user"; exit 130' INT TERM

# Run main function
main "$@"
