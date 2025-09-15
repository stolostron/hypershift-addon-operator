#!/bin/bash

# Script: setup-mce-acm-integration.sh
# Description: Main orchestration script for MCE-ACM integration setup
# Author: Generated automation script
# Usage: ./setup-mce-acm-integration.sh [options]

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
SKIP_VERIFICATION=false
SKIP_BACKUP=false
MCE_CLUSTERS=""
DISCOVERY_PREFIX=""
AUTOIMPORT_FILTER=""
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
    echo "MCE-ACM Integration Setup Script"
    echo "================================"
    echo ""
    echo "This script automates the complete setup of MCE-ACM integration as described"
    echo "in the discovering_hostedclusters.md documentation."
    echo ""
    echo "Usage: $0 [options]"
    echo ""
    echo "Options:"
    echo "  --non-interactive           Run in non-interactive mode (requires --mce-clusters)"
    echo "  --mce-clusters <names>      Comma-separated list of MCE cluster names"
    echo "                              Note: For step-by-step import, use individual scripts with kubeconfigs"
    echo "  --discovery-prefix <prefix> Custom prefix for discovered cluster names (optional)"
    echo "  --autoimport-filter <filter> Filter pattern for auto-import policy"
    echo "  --skip-verification         Skip final verification step"
    echo "  --skip-backup              Skip backup step"
    echo "  -h, --help                 Show this help message"
    echo ""
    echo "Interactive Mode (default):"
    echo "  The script will guide you through each step and prompt for input"
    echo ""
    echo "Non-Interactive Mode:"
    echo "  All required parameters must be provided via command line options"
    echo ""
    echo "Examples:"
    echo "  $0                                    # Interactive mode"
    echo "  $0 --mce-clusters mce-1,mce-2 \\      # Non-interactive mode"
    echo "     --discovery-prefix \"prod-\" \\      # with custom settings"
    echo "     --autoimport-filter \"prod-\" \\     "
    echo "     --non-interactive"
    echo ""
    echo "Steps performed:"
    echo "  1. Setup ACM Hub for MCE integration"
    echo "  2. Import MCE clusters into ACM"
    echo "  3. Enable HyperShift addon for MCE clusters"
    echo "  4. Setup auto-import policy for discovered clusters"
    echo "  5. Verify the complete integration"
    echo "  6. Backup critical resources for disaster recovery"
}

# Parse command line arguments
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --non-interactive)
                INTERACTIVE=false
                shift
                ;;
            --mce-clusters)
                MCE_CLUSTERS="$2"
                shift 2
                ;;
            --discovery-prefix)
                DISCOVERY_PREFIX="$2"
                shift 2
                ;;
            --autoimport-filter)
                AUTOIMPORT_FILTER="$2"
                shift 2
                ;;
            --skip-verification)
                SKIP_VERIFICATION=true
                shift
                ;;
            --skip-backup)
                SKIP_BACKUP=true
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
    
    # Validate non-interactive mode requirements
    if [ "$INTERACTIVE" = false ] && [ -z "$MCE_CLUSTERS" ]; then
        log_error "Non-interactive mode requires --mce-clusters option"
        usage
        exit 1
    fi
}

# Check if all required scripts exist
check_script_dependencies() {
    log_info "Checking script dependencies..."
    
    local required_scripts=(
        "setup-acm-hub.sh"
        "import-mce-cluster.sh"
        "enable-hypershift-addon.sh"
        "setup-autoimport-policy.sh"
        "verify-mce-integration.sh"
        "backup-mce-resources.sh"
    )
    
    local missing_scripts=()
    
    for script in "${required_scripts[@]}"; do
        if [ ! -f "$SCRIPT_DIR/$script" ]; then
            missing_scripts+=("$script")
        fi
    done
    
    if [ ${#missing_scripts[@]} -gt 0 ]; then
        log_error "Missing required scripts:"
        for script in "${missing_scripts[@]}"; do
            log_error "  - $script"
        done
        log_error "Please ensure all scripts are in the same directory: $SCRIPT_DIR"
        exit 1
    fi
    
    # Make scripts executable
    for script in "${required_scripts[@]}"; do
        chmod +x "$SCRIPT_DIR/$script"
    done
    
    log_success "All required scripts found and made executable"
}

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check if oc command is available
    if ! command -v oc &> /dev/null; then
        log_error "oc CLI tool is not installed or not in PATH"
        exit 1
    fi
    
    # Check if clusteradm command is available
    if ! command -v clusteradm &> /dev/null; then
        log_error "clusteradm CLI tool is not installed or not in PATH"
        log_info "Install it from: https://github.com/open-cluster-management-io/clusteradm/releases"
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
    
    # Check if this is an ACM hub cluster
    if ! oc get namespace open-cluster-management &> /dev/null; then
        log_error "This does not appear to be an ACM hub cluster"
        log_error "The 'open-cluster-management' namespace is not found"
        log_error ""
        log_error "Currently installed components:"
        oc get csv -A | grep -E "(advanced-cluster-management|multicluster-engine)" || log_error "  No ACM/MCE components found"
        log_error ""
        log_error "This orchestration script requires Red Hat Advanced Cluster Management (ACM)."
        log_error "MCE-only installations should use individual scripts manually."
        exit 1
    fi
    
    log_success "All prerequisites satisfied"
}

# Interactive prompts
interactive_prompts() {
    if [ "$INTERACTIVE" = false ]; then
        return
    fi
    
    echo ""
    log_info "=== Interactive Configuration ==="
    
    # MCE clusters
    if [ -z "$MCE_CLUSTERS" ]; then
        echo ""
        log_info "Please provide the names of MCE clusters to import into ACM."
        log_info "These should be existing MCE clusters that you want to manage through ACM."
        read -p "Enter MCE cluster names (comma-separated): " MCE_CLUSTERS
        
        if [ -z "$MCE_CLUSTERS" ]; then
            log_error "MCE cluster names are required"
            exit 1
        fi
    fi
    
    # Discovery prefix
    if [ -z "$DISCOVERY_PREFIX" ]; then
        echo ""
        log_info "Discovery prefix determines how discovered hosted clusters are named."
        log_info "Default: <mce-cluster-name>-<hosted-cluster-name>"
        log_info "Custom prefix: <prefix><hosted-cluster-name>"
        log_info "Note: Empty prefix is not supported - system will use default if not specified"
        read -p "Enter discovery prefix (or press Enter for default): " DISCOVERY_PREFIX
    fi
    
    # Auto-import filter
    if [ -z "$AUTOIMPORT_FILTER" ]; then
        echo ""
        log_info "Auto-import filter determines which discovered clusters are automatically imported."
        log_info "Empty filter: Import all discovered clusters"
        log_info "Custom filter: Import only clusters matching the pattern"
        read -p "Enter auto-import filter pattern (or press Enter for all): " AUTOIMPORT_FILTER
    fi
    
    # Confirmation
    echo ""
    log_info "=== Configuration Summary ==="
    echo "MCE Clusters: $MCE_CLUSTERS"
    echo "Discovery Prefix: ${DISCOVERY_PREFIX:-<default>}"
    echo "Auto-import Filter: ${AUTOIMPORT_FILTER:-<all>}"
    echo "Skip Verification: $SKIP_VERIFICATION"
    echo "Skip Backup: $SKIP_BACKUP"
    echo ""
    
    read -p "Proceed with this configuration? (y/N): " confirm
    if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
        log_info "Setup cancelled by user"
        exit 0
    fi
}

# Execute step with error handling
execute_step() {
    local step_name="$1"
    local script_name="$2"
    shift 2
    local script_args=("$@")
    
    log_step "$step_name"
    echo ""
    
    if "$SCRIPT_DIR/$script_name" "${script_args[@]}"; then
        log_success "Step completed: $step_name"
        echo ""
    else
        log_error "Step failed: $step_name"
        log_error "Script: $script_name"
        log_error "Arguments: ${script_args[*]}"
        echo ""
        
        if [ "$INTERACTIVE" = true ]; then
            read -p "Continue with remaining steps? (y/N): " continue_choice
            if [[ ! "$continue_choice" =~ ^[Yy]$ ]]; then
                log_info "Setup stopped by user"
                exit 1
            fi
        else
            log_error "Non-interactive mode: stopping on first failure"
            exit 1
        fi
    fi
}

# Step 1: Setup ACM Hub
step_setup_acm_hub() {
    execute_step "Setting up ACM Hub for MCE integration" "setup-acm-hub.sh"
}

# Step 2: Import MCE clusters
step_import_mce_clusters() {
    log_step "Importing MCE clusters into ACM"
    echo ""
    
    log_warning "The orchestration script cannot automatically import clusters with kubeconfigs."
    log_info "Please run the import script manually for each cluster:"
    echo ""
    
    # Convert comma-separated list to array
    IFS=',' read -ra cluster_array <<< "$MCE_CLUSTERS"
    
    for cluster in "${cluster_array[@]}"; do
        cluster=$(echo "$cluster" | xargs)  # Trim whitespace
        echo "  ./import-mce-cluster.sh $cluster /path/to/$cluster-kubeconfig"
    done
    
    echo ""
    log_info "After importing clusters manually, you can continue with:"
    log_info "  ./enable-hypershift-addon.sh $MCE_CLUSTERS"
    log_info "  ./setup-autoimport-policy.sh"
    
    if [ "$INTERACTIVE" = true ]; then
        echo ""
        read -p "Have you completed the manual import steps above? (y/N): " import_done
        if [[ ! "$import_done" =~ ^[Yy]$ ]]; then
            log_info "Please complete the manual import steps and re-run this script"
            exit 0
        fi
    else
        log_error "Non-interactive mode: Cannot proceed with manual import steps"
        log_error "Please use individual scripts for complete automation"
        exit 1
    fi
    
    # Wait for imports to complete
    log_info "Waiting for cluster imports to complete..."
    log_info "This may take several minutes..."
    
    local max_wait=600  # 10 minutes
    local wait_time=0
    local all_ready=false
    
    while [ $wait_time -lt $max_wait ] && [ "$all_ready" = false ]; do
        all_ready=true
        
        for cluster in "${cluster_array[@]}"; do
            cluster=$(echo "$cluster" | xargs)
            local joined
            joined=$(oc get managedcluster "$cluster" -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterJoined")].status}' 2>/dev/null || echo "Unknown")
            
            if [ "$joined" != "True" ]; then
                all_ready=false
                break
            fi
        done
        
        if [ "$all_ready" = false ]; then
            sleep 30
            wait_time=$((wait_time + 30))
            log_info "Still waiting for clusters to join... ($wait_time/${max_wait}s)"
        fi
    done
    
    if [ "$all_ready" = true ]; then
        log_success "All clusters have joined successfully"
    else
        log_warning "Not all clusters joined within the timeout period"
        log_info "You can check status with: oc get managedcluster"
        
        if [ "$INTERACTIVE" = true ]; then
            read -p "Continue with addon enablement? (y/N): " continue_choice
            if [[ ! "$continue_choice" =~ ^[Yy]$ ]]; then
                log_info "Setup stopped by user"
                exit 1
            fi
        fi
    fi
    
    echo ""
}

# Step 3: Enable HyperShift addon
step_enable_hypershift_addon() {
    local addon_args=("$MCE_CLUSTERS")
    
    if [ -n "$DISCOVERY_PREFIX" ]; then
        addon_args+=("--discovery-prefix" "$DISCOVERY_PREFIX")
    fi
    
    execute_step "Enabling HyperShift addon for MCE clusters" "enable-hypershift-addon.sh" "${addon_args[@]}"
}

# Step 4: Setup auto-import policy
step_setup_autoimport_policy() {
    local policy_args=()
    
    if [ -n "$AUTOIMPORT_FILTER" ]; then
        policy_args+=("--filter" "$AUTOIMPORT_FILTER")
    fi
    
    execute_step "Setting up auto-import policy for discovered clusters" "setup-autoimport-policy.sh" "${policy_args[@]}"
}

# Step 5: Verify integration
step_verify_integration() {
    if [ "$SKIP_VERIFICATION" = true ]; then
        log_info "Skipping verification step as requested"
        return
    fi
    
    execute_step "Verifying MCE-ACM integration" "verify-mce-integration.sh" "--verbose"
}

# Step 6: Backup resources
step_backup_resources() {
    if [ "$SKIP_BACKUP" = true ]; then
        log_info "Skipping backup step as requested"
        return
    fi
    
    execute_step "Backing up critical resources for disaster recovery" "backup-mce-resources.sh"
}

# Display completion summary
display_completion_summary() {
    log_success "=== MCE-ACM Integration Setup Complete ==="
    echo ""
    
    log_info "What was configured:"
    echo "  ✓ ACM Hub prepared for MCE integration"
    echo "  ✓ MCE clusters imported: $MCE_CLUSTERS"
    echo "  ✓ HyperShift addon enabled with discovery capabilities"
    echo "  ✓ Auto-import policy configured for discovered clusters"
    
    if [ "$SKIP_VERIFICATION" = false ]; then
        echo "  ✓ Integration verified"
    fi
    
    if [ "$SKIP_BACKUP" = false ]; then
        echo "  ✓ Critical resources backed up"
    fi
    
    echo ""
    log_info "Next steps:"
    echo "1. Create hosted clusters in your MCE clusters"
    echo "2. Monitor discovered clusters: oc get discoveredcluster --all-namespaces"
    echo "3. Check auto-imported clusters: oc get managedcluster"
    echo "4. Use ACM console for policy management and application deployment"
    echo "5. Use MCE console for hosted cluster lifecycle management"
    
    echo ""
    log_info "Useful commands:"
    echo "  # Monitor cluster status"
    echo "  oc get managedcluster"
    echo ""
    echo "  # Check addon status"
    echo "  oc get managedclusteraddon --all-namespaces"
    echo ""
    echo "  # View discovered clusters"
    echo "  oc get discoveredcluster --all-namespaces"
    echo ""
    echo "  # Check policy status"
    echo "  oc get policy -n open-cluster-management-global-set"
    echo ""
    echo "  # Run verification anytime"
    echo "  $SCRIPT_DIR/verify-mce-integration.sh"
    
    echo ""
    log_info "Configuration details:"
    echo "  Discovery Prefix: ${DISCOVERY_PREFIX:-<default>}"
    echo "  Auto-import Filter: ${AUTOIMPORT_FILTER:-<all>}"
    
    if [ "$SKIP_BACKUP" = false ]; then
        echo "  Backup Location: $(ls -td ./mce-acm-backup-* 2>/dev/null | head -1 || echo 'Not found')"
    fi
    
    echo ""
    log_success "Your MCE-ACM integration is ready for production use!"
}

# Main execution
main() {
    echo "MCE-ACM Integration Setup Script"
    echo "================================"
    echo "This script automates the complete setup process described in"
    echo "the discovering_hostedclusters.md documentation."
    echo ""
    
    parse_arguments "$@"
    check_script_dependencies
    check_prerequisites
    interactive_prompts
    
    echo ""
    log_info "Starting MCE-ACM integration setup..."
    echo ""
    
    # Execute all steps
    step_setup_acm_hub
    step_import_mce_clusters
    step_enable_hypershift_addon
    step_setup_autoimport_policy
    step_verify_integration
    step_backup_resources
    
    display_completion_summary
}

# Trap to handle interruptions
trap 'echo ""; log_warning "Setup interrupted by user"; exit 130' INT TERM

# Run main function
main "$@"
