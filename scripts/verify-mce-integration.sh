#!/bin/bash

# Script: verify-mce-integration.sh
# Description: Verification and troubleshooting script for MCE-ACM integration
# Author: Generated automation script
# Usage: ./verify-mce-integration.sh [--cluster <cluster_name>] [--verbose]

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Default values
CLUSTER_NAME=""
VERBOSE=false

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

log_debug() {
    if [ "$VERBOSE" = true ]; then
        echo -e "${CYAN}[DEBUG]${NC} $1"
    fi
}

# Usage information
usage() {
    echo "Usage: $0 [--cluster <cluster_name>] [--verbose]"
    echo ""
    echo "Options:"
    echo "  --cluster <name>    Focus verification on specific cluster"
    echo "  --verbose          Enable detailed debug output"
    echo ""
    echo "Examples:"
    echo "  $0                           # Verify entire integration"
    echo "  $0 --cluster mce-cluster-1   # Verify specific cluster"
    echo "  $0 --verbose                 # Detailed verification output"
}

# Parse command line arguments
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --cluster)
                CLUSTER_NAME="$2"
                shift 2
                ;;
            --verbose)
                VERBOSE=true
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
        return 1
    fi
    
    # Check if logged into OpenShift
    if ! oc whoami &> /dev/null; then
        log_error "Not logged into OpenShift. Please run 'oc login' first"
        return 1
    fi
    
    # Check if ACM is installed
    log_info "Checking ACM installation..."
    
    # Check for ACM namespace
    if ! oc get namespace open-cluster-management &> /dev/null; then
        log_error "ACM namespace 'open-cluster-management' not found"
        log_error "This script requires Red Hat Advanced Cluster Management (ACM) to be installed"
        log_error "Found components:"
        oc get csv -A | grep -E "(advanced-cluster-management|multicluster-engine)" || log_error "  No ACM/MCE components found"
        return 1
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
        return 1
    fi
    
    log_success "Found ACM installation: $acm_csv"
    log_success "Prerequisites check passed"
    return 0
}

# Verify ACM hub configuration
verify_acm_hub_config() {
    log_info "=== Verifying ACM Hub Configuration ==="
    local issues=0
    
    # Check AddOnDeploymentConfig
    log_info "Checking AddOnDeploymentConfig..."
    if oc get addondeploymentconfig addon-ns-config -n multicluster-engine &> /dev/null; then
        log_success "AddOnDeploymentConfig 'addon-ns-config' exists"
        
        local install_ns
        install_ns=$(oc get addondeploymentconfig addon-ns-config -n multicluster-engine -o jsonpath='{.spec.agentInstallNamespace}' 2>/dev/null || echo "")
        if [ "$install_ns" = "open-cluster-management-agent-addon-discovery" ]; then
            log_success "Addon installation namespace correctly configured"
        else
            log_error "Addon installation namespace misconfigured: '$install_ns'"
            ((issues++))
        fi
    else
        log_error "AddOnDeploymentConfig 'addon-ns-config' not found"
        ((issues++))
    fi
    
    # Check KlusterletConfig
    log_info "Checking KlusterletConfig..."
    if oc get klusterletconfig mce-import-klusterlet-config &> /dev/null; then
        log_success "KlusterletConfig 'mce-import-klusterlet-config' exists"
        
        local postfix
        postfix=$(oc get klusterletconfig mce-import-klusterlet-config -o jsonpath='{.spec.installMode.noOperator.postfix}' 2>/dev/null || echo "")
        if [ "$postfix" = "mce-import" ]; then
            log_success "Klusterlet postfix correctly configured"
        else
            log_error "Klusterlet postfix misconfigured: '$postfix'"
            ((issues++))
        fi
    else
        log_error "KlusterletConfig 'mce-import-klusterlet-config' not found"
        ((issues++))
    fi
    
    # Check ClusterManagementAddOns
    log_info "Checking ClusterManagementAddOns configuration..."
    local addons=("work-manager" "managed-serviceaccount" "cluster-proxy")
    
    for addon in "${addons[@]}"; do
        if oc get clustermanagementaddon "$addon" &> /dev/null; then
            log_success "ClusterManagementAddOn '$addon' exists"
            
            # Check if it references the correct config
            local config_ref
            config_ref=$(oc get clustermanagementaddon "$addon" -o jsonpath='{.spec.installStrategy.placements[0].configs[0].name}' 2>/dev/null || echo "")
            if [ "$config_ref" = "addon-ns-config" ]; then
                log_success "ClusterManagementAddOn '$addon' correctly references addon-ns-config"
            else
                log_warning "ClusterManagementAddOn '$addon' config reference: '$config_ref'"
            fi
        else
            log_error "ClusterManagementAddOn '$addon' not found"
            ((issues++))
        fi
    done
    
    # Check HyperShift addon configuration
    log_info "Checking HyperShift addon configuration..."
    if oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine &> /dev/null; then
        log_success "HyperShift addon deployment config exists"
        
        local hs_install_ns
        hs_install_ns=$(oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o jsonpath='{.spec.agentInstallNamespace}' 2>/dev/null || echo "")
        if [ "$hs_install_ns" = "open-cluster-management-agent-addon-discovery" ]; then
            log_success "HyperShift addon installation namespace correctly configured"
        else
            log_warning "HyperShift addon installation namespace: '$hs_install_ns'"
        fi
        
        # Check customized variables
        local custom_vars
        custom_vars=$(oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o jsonpath='{.spec.customizedVariables}' 2>/dev/null || echo "[]")
        log_debug "HyperShift addon custom variables: $custom_vars"
    else
        log_warning "HyperShift addon deployment config not found"
    fi
    
    return $issues
}

# Verify managed clusters
verify_managed_clusters() {
    log_info "=== Verifying Managed Clusters ==="
    local issues=0
    
    # Get all managed clusters (excluding local-cluster)
    local clusters
    if [ -n "$CLUSTER_NAME" ]; then
        clusters="$CLUSTER_NAME"
    else
        clusters=$(oc get managedcluster --no-headers | grep -v local-cluster | awk '{print $1}' | tr '\n' ' ')
    fi
    
    if [ -z "$clusters" ]; then
        log_warning "No MCE clusters found (excluding local-cluster)"
        return 0
    fi
    
    for cluster in $clusters; do
        log_info "Checking cluster '$cluster'..."
        
        # Check cluster status
        if oc get managedcluster "$cluster" &> /dev/null; then
            local hub_accepted
            hub_accepted=$(oc get managedcluster "$cluster" -o jsonpath='{.spec.hubAcceptsClient}' 2>/dev/null || echo "false")
            local joined
            joined=$(oc get managedcluster "$cluster" -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterJoined")].status}' 2>/dev/null || echo "Unknown")
            local available
            available=$(oc get managedcluster "$cluster" -o jsonpath='{.status.conditions[?(@.type=="ManagedClusterConditionAvailable")].status}' 2>/dev/null || echo "Unknown")
            
            log_info "  Hub Accepts Client: $hub_accepted"
            log_info "  Joined: $joined"
            log_info "  Available: $available"
            
            if [ "$joined" = "True" ] && [ "$available" = "True" ]; then
                log_success "Cluster '$cluster' is healthy"
            else
                log_warning "Cluster '$cluster' may have issues"
                ((issues++))
            fi
            
            # Check klusterlet config annotation
            local klusterlet_config
            klusterlet_config=$(oc get managedcluster "$cluster" -o jsonpath='{.metadata.annotations.agent\.open-cluster-management\.io/klusterlet-config}' 2>/dev/null || echo "")
            if [ "$klusterlet_config" = "mce-import-klusterlet-config" ]; then
                log_success "  Klusterlet config annotation correctly set"
            else
                log_warning "  Klusterlet config annotation: '$klusterlet_config'"
            fi
        else
            log_error "Managed cluster '$cluster' not found"
            ((issues++))
            continue
        fi
        
        # Check addons for this cluster
        log_info "  Checking addons for cluster '$cluster'..."
        local cluster_addons
        cluster_addons=$(oc get managedclusteraddon -n "$cluster" --no-headers 2>/dev/null | awk '{print $1}' | tr '\n' ' ' || echo "")
        
        if [ -n "$cluster_addons" ]; then
            log_info "  Found addons: $cluster_addons"
            
            for addon in $cluster_addons; do
                local addon_available
                addon_available=$(oc get managedclusteraddon "$addon" -n "$cluster" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || echo "Unknown")
                
                if [ "$addon_available" = "True" ]; then
                    log_success "    Addon '$addon' is available"
                else
                    log_warning "    Addon '$addon' status: $addon_available"
                    ((issues++))
                fi
            done
        else
            log_warning "  No addons found for cluster '$cluster'"
        fi
    done
    
    return $issues
}

# Verify addon deployments
verify_addon_deployments() {
    log_info "=== Verifying Addon Deployments ==="
    local issues=0
    
    # Check if the addon namespace exists
    if ! oc get namespace open-cluster-management-agent-addon-discovery &> /dev/null; then
        log_warning "Addon namespace 'open-cluster-management-agent-addon-discovery' not found"
        log_info "This is normal if no MCE clusters have been imported yet"
        return 0
    fi
    
    log_info "Checking deployments in addon namespace..."
    local deployments
    deployments=$(oc get deployment -n open-cluster-management-agent-addon-discovery --no-headers 2>/dev/null | awk '{print $1}' | tr '\n' ' ' || echo "")
    
    if [ -n "$deployments" ]; then
        for deployment in $deployments; do
            local ready
            ready=$(oc get deployment "$deployment" -n open-cluster-management-agent-addon-discovery -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
            local desired
            desired=$(oc get deployment "$deployment" -n open-cluster-management-agent-addon-discovery -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "1")
            
            if [ "$ready" = "$desired" ]; then
                log_success "Deployment '$deployment' is ready ($ready/$desired)"
            else
                log_warning "Deployment '$deployment' is not fully ready ($ready/$desired)"
                ((issues++))
            fi
        done
    else
        log_warning "No deployments found in addon namespace"
    fi
    
    return $issues
}

# Verify discovered clusters
verify_discovered_clusters() {
    log_info "=== Verifying Discovered Clusters ==="
    local issues=0
    
    # Check for discovered clusters
    local discovered_clusters
    discovered_clusters=$(oc get discoveredcluster --all-namespaces --no-headers 2>/dev/null | wc -l || echo "0")
    
    log_info "Found $discovered_clusters discovered cluster(s)"
    
    if [ "$discovered_clusters" -gt 0 ]; then
        log_info "Discovered clusters details:"
        oc get discoveredcluster --all-namespaces -o custom-columns=\
"NAMESPACE:.metadata.namespace,NAME:.metadata.name,DISPLAY_NAME:.spec.displayName,TYPE:.spec.type,STATUS:.spec.status,IMPORT:.spec.importAsManagedCluster" 2>/dev/null || true
        
        # Check for MultiClusterEngineHCP type clusters
        local hcp_clusters
        hcp_clusters=$(oc get discoveredcluster --all-namespaces -o jsonpath='{.items[?(@.spec.type=="MultiClusterEngineHCP")].metadata.name}' 2>/dev/null | wc -w || echo "0")
        
        if [ "$hcp_clusters" -gt 0 ]; then
            log_success "Found $hcp_clusters MultiClusterEngineHCP discovered cluster(s)"
        else
            log_info "No MultiClusterEngineHCP discovered clusters found"
            log_info "This is normal if no hosted clusters have been created in MCE yet"
        fi
    else
        log_info "No discovered clusters found"
        log_info "This is normal if:"
        log_info "  - No hosted clusters have been created in MCE clusters"
        log_info "  - HyperShift addon is not enabled on MCE clusters"
    fi
    
    return $issues
}

# Verify auto-import policy
verify_autoimport_policy() {
    log_info "=== Verifying Auto-Import Policy ==="
    local issues=0
    
    # Check if policy exists
    if oc get policy policy-mce-hcp-autoimport -n open-cluster-management-global-set &> /dev/null; then
        log_success "Auto-import policy exists"
        
        # Check policy status
        local compliant
        compliant=$(oc get policy policy-mce-hcp-autoimport -n open-cluster-management-global-set -o jsonpath='{.status.compliant}' 2>/dev/null || echo "Unknown")
        local disabled
        disabled=$(oc get policy policy-mce-hcp-autoimport -n open-cluster-management-global-set -o jsonpath='{.spec.disabled}' 2>/dev/null || echo "false")
        
        log_info "Policy compliance: $compliant"
        log_info "Policy disabled: $disabled"
        
        if [ "$disabled" = "true" ]; then
            log_warning "Auto-import policy is disabled"
        fi
        
        # Check discovery config
        if oc get configmap discovery-config -n open-cluster-management-global-set &> /dev/null; then
            local filter
            filter=$(oc get configmap discovery-config -n open-cluster-management-global-set -o jsonpath='{.data.mce-hcp-filter}' 2>/dev/null || echo "")
            log_info "Discovery filter: '$filter'"
            
            if [ -z "$filter" ]; then
                log_info "Filter is empty - all discovered clusters will be imported"
            else
                log_info "Filter is set - only clusters matching '$filter' will be imported"
            fi
        else
            log_error "Discovery config ConfigMap not found"
            ((issues++))
        fi
        
        # Check placement
        if oc get placement policy-mce-hcp-autoimport-placement -n open-cluster-management-global-set &> /dev/null; then
            log_success "Policy placement exists"
        else
            log_error "Policy placement not found"
            ((issues++))
        fi
        
        # Check placement binding
        if oc get placementbinding policy-mce-hcp-autoimport-placement-binding -n open-cluster-management-global-set &> /dev/null; then
            log_success "Policy placement binding exists"
        else
            log_error "Policy placement binding not found"
            ((issues++))
        fi
        
    else
        log_warning "Auto-import policy not found"
        log_info "Run setup-autoimport-policy.sh to create it"
    fi
    
    return $issues
}

# Check network connectivity
verify_network_connectivity() {
    if [ -z "$CLUSTER_NAME" ]; then
        log_info "=== Skipping Network Connectivity Check ==="
        log_info "Use --cluster <name> to check connectivity to a specific cluster"
        return 0
    fi
    
    log_info "=== Verifying Network Connectivity ==="
    local issues=0
    
    # Get cluster API URL
    local api_url
    api_url=$(oc get managedcluster "$CLUSTER_NAME" -o jsonpath='{.spec.managedClusterClientConfigs[0].url}' 2>/dev/null || echo "")
    
    if [ -n "$api_url" ]; then
        log_info "Testing connectivity to cluster '$CLUSTER_NAME' at $api_url"
        
        # Extract hostname and port from URL
        local hostname
        hostname=$(echo "$api_url" | sed -E 's|https?://([^:/]+).*|\1|')
        local port
        port=$(echo "$api_url" | sed -E 's|https?://[^:/]+:?([0-9]+)?.*|\1|')
        port=${port:-443}
        
        if command -v nc &> /dev/null; then
            if nc -z -w5 "$hostname" "$port" 2>/dev/null; then
                log_success "Network connectivity to $hostname:$port is working"
            else
                log_error "Cannot connect to $hostname:$port"
                ((issues++))
            fi
        else
            log_warning "netcat (nc) not available - cannot test connectivity"
        fi
    else
        log_warning "Could not determine API URL for cluster '$CLUSTER_NAME'"
    fi
    
    return $issues
}

# Provide troubleshooting recommendations
provide_troubleshooting_recommendations() {
    local total_issues="$1"
    
    log_info "=== Troubleshooting Recommendations ==="
    
    if [ "$total_issues" -eq 0 ]; then
        log_success "No issues detected! Your MCE-ACM integration appears to be working correctly."
        return
    fi
    
    log_warning "Found $total_issues issue(s). Here are some troubleshooting steps:"
    
    echo ""
    echo "Common Solutions:"
    echo "1. Configuration Issues:"
    echo "   - Re-run setup-acm-hub.sh to fix ACM hub configuration"
    echo "   - Check if all required CRDs are installed"
    echo ""
    echo "2. Cluster Import Issues:"
    echo "   - Verify network connectivity between ACM hub and MCE clusters"
    echo "   - Check klusterlet logs: oc logs -n open-cluster-management-agent deploy/klusterlet"
    echo "   - Verify import credentials are correct"
    echo ""
    echo "3. Addon Issues:"
    echo "   - Check addon agent logs in open-cluster-management-agent-addon-discovery namespace"
    echo "   - Verify addon deployment configurations"
    echo "   - Re-enable addons: clusteradm addon enable --names <addon-name> --clusters <cluster-name>"
    echo ""
    echo "4. Discovery Issues:"
    echo "   - Ensure hosted clusters are created and running in MCE"
    echo "   - Check HyperShift addon logs"
    echo "   - Verify HyperShift addon configuration"
    echo ""
    echo "5. Auto-Import Issues:"
    echo "   - Check policy compliance: oc get policy -n open-cluster-management-global-set"
    echo "   - Verify discovery filter configuration"
    echo "   - Check for 'previously-auto-imported' annotations on DiscoveredCluster resources"
    echo ""
    echo "Useful Commands for Debugging:"
    echo "- oc get managedcluster"
    echo "- oc get managedclusteraddon --all-namespaces"
    echo "- oc get discoveredcluster --all-namespaces"
    echo "- oc get policy -n open-cluster-management-global-set"
    echo "- oc get deployment -n open-cluster-management-agent-addon-discovery"
    echo "- oc logs -n open-cluster-management-agent-addon-discovery deploy/hypershift-addon-agent"
}

# Generate detailed report
generate_detailed_report() {
    if [ "$VERBOSE" = false ]; then
        return
    fi
    
    log_info "=== Detailed System Report ==="
    
    echo ""
    echo "ACM/MCE Versions:"
    oc get csv -A | grep -E "(advanced-cluster-management|multicluster-engine)" 2>/dev/null || echo "  Version information not available"
    
    echo ""
    echo "All ManagedClusters:"
    oc get managedcluster -o wide 2>/dev/null || echo "  No managed clusters found"
    
    echo ""
    echo "All ManagedClusterAddOns:"
    oc get managedclusteraddon --all-namespaces 2>/dev/null || echo "  No addons found"
    
    echo ""
    echo "All DiscoveredClusters:"
    oc get discoveredcluster --all-namespaces -o wide 2>/dev/null || echo "  No discovered clusters found"
    
    echo ""
    echo "Policies:"
    oc get policy -n open-cluster-management-global-set 2>/dev/null || echo "  No policies found"
    
    echo ""
    echo "Addon Namespace Resources:"
    if oc get namespace open-cluster-management-agent-addon-discovery &> /dev/null; then
        oc get all -n open-cluster-management-agent-addon-discovery 2>/dev/null || echo "  No resources found"
    else
        echo "  Addon namespace does not exist"
    fi
}

# Main execution
main() {
    parse_arguments "$@"
    
    log_info "Starting MCE-ACM integration verification..."
    if [ -n "$CLUSTER_NAME" ]; then
        log_info "Focusing on cluster: $CLUSTER_NAME"
    fi
    
    local total_issues=0
    
    # Run all verification steps
    if ! check_prerequisites; then
        log_error "Prerequisites check failed. Cannot continue."
        exit 1
    fi
    
    verify_acm_hub_config
    total_issues=$((total_issues + $?))
    
    verify_managed_clusters
    total_issues=$((total_issues + $?))
    
    verify_addon_deployments
    total_issues=$((total_issues + $?))
    
    verify_discovered_clusters
    total_issues=$((total_issues + $?))
    
    verify_autoimport_policy
    total_issues=$((total_issues + $?))
    
    verify_network_connectivity
    total_issues=$((total_issues + $?))
    
    # Generate reports
    generate_detailed_report
    provide_troubleshooting_recommendations "$total_issues"
    
    echo ""
    if [ "$total_issues" -eq 0 ]; then
        log_success "Verification completed successfully with no issues found!"
        exit 0
    else
        log_warning "Verification completed with $total_issues issue(s) found."
        exit 1
    fi
}

# Run main function
main "$@"
