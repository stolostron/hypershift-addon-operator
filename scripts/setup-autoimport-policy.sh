#!/bin/bash

# Script: setup-autoimport-policy.sh
# Description: Set up auto-import policy for discovered hosted clusters (Step 5 from discovering_hostedclusters.md)
# Author: Generated automation script
# Usage: ./setup-autoimport-policy.sh [--filter <filter_pattern>] [--dry-run]

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
FILTER_PATTERN=""
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
    echo "Usage: $0 [--filter <filter_pattern>] [--dry-run]"
    echo ""
    echo "Options:"
    echo "  --filter <pattern>   Filter pattern for cluster names to auto-import"
    echo "                       Leave empty to import all discovered clusters"
    echo "  --dry-run           Show what would be created without applying"
    echo ""
    echo "Examples:"
    echo "  $0                           # Import all discovered clusters"
    echo "  $0 --filter \"prod-\"          # Import only clusters with 'prod-' prefix"
    echo "  $0 --filter \"\" --dry-run     # Preview policy without applying"
    echo ""
    echo "Filter Pattern Examples:"
    echo "  \"\"                          # Match all clusters (import everything)"
    echo "  \"prod-\"                     # Match clusters starting with 'prod-'"
    echo "  \"staging\"                   # Match clusters containing 'staging'"
    echo "  \"test-cluster-1\"            # Match specific cluster name"
}

# Parse command line arguments
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --filter)
                FILTER_PATTERN="$2"
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
    
    # Check if the global set namespace exists
    if ! oc get namespace open-cluster-management-global-set &> /dev/null; then
        log_error "Namespace 'open-cluster-management-global-set' not found"
        log_error "This namespace is required for ACM policies"
        exit 1
    fi
    
    # Check if ACM governance policies are available
    if ! oc get crd policies.policy.open-cluster-management.io &> /dev/null; then
        log_error "ACM Policy CRD not found"
        log_error "This script requires ACM governance policies to be installed"
        log_error ""
        log_error "Currently installed components:"
        oc get csv -A | grep -E "(advanced-cluster-management|multicluster-engine)" || log_error "  No ACM/MCE components found"
        log_error ""
        log_error "This appears to be an MCE-only installation. ACM governance policies are required"
        log_error "for automated cluster import. You can manually import discovered clusters using:"
        log_error "  oc patch discoveredcluster <cluster-name> -n <namespace> \\"
        log_error "    --type=merge -p '{\"spec\":{\"importAsManagedCluster\":true}}'"
        exit 1
    fi
    
    log_success "Prerequisites check passed"
}

# Create or update discovery config
create_discovery_config() {
    log_info "Creating discovery configuration..."
    
    local config_yaml
    config_yaml=$(cat <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: discovery-config
  namespace: open-cluster-management-global-set
data:
  mce-hcp-filter: "$FILTER_PATTERN"
EOF
)
    
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: Would create/update ConfigMap:"
        echo "$config_yaml"
        echo ""
        return
    fi
    
    echo "$config_yaml" | oc apply -f -
    
    if [ $? -eq 0 ]; then
        log_success "Discovery configuration created/updated successfully"
        log_info "Filter pattern: '$FILTER_PATTERN'"
    else
        log_error "Failed to create discovery configuration"
        exit 1
    fi
}

# Create auto-import policy
create_autoimport_policy() {
    log_info "Creating auto-import policy..."
    
    local policy_yaml
    policy_yaml=$(cat <<'EOF'
apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  name: policy-mce-hcp-autoimport
  namespace: open-cluster-management-global-set
  annotations:
    policy.open-cluster-management.io/standards: NIST SP 800-53
    policy.open-cluster-management.io/categories: CM Configuration Management
    policy.open-cluster-management.io/controls: CM-2 Baseline Configuration
    policy.open-cluster-management.io/description: |
      Automatically imports discovered MultiClusterEngineHCP clusters into ACM as managed clusters.
      Fine-tune which clusters to import using configMap filters or DiscoveredCluster annotations.
spec:
  disabled: false
  policy-templates:
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata:
          name: mce-hcp-autoimport-config
        spec:
          object-templates:
            - complianceType: musthave
              objectDefinition:
                apiVersion: v1
                kind: ConfigMap
                metadata:
                  name: discovery-config
                  namespace: open-cluster-management-global-set
                data:
                  mce-hcp-filter: ""
          remediationAction: enforce
          severity: low
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata:
          name: policy-mce-hcp-autoimport
        spec:
          remediationAction: enforce
          severity: low
          object-templates-raw: |
            {{- /* Find MultiClusterEngineHCP DiscoveredClusters */ -}}
            {{- range $dc := (lookup "discovery.open-cluster-management.io/v1" "DiscoveredCluster" "" "").items }}
              {{- /* Check if import should be skipped */ -}}
              {{- $skip := "false" -}}
              {{- range $key, $value := $dc.metadata.annotations }}
                {{- if and (eq $key "discovery.open-cluster-management.io/previously-auto-imported")
                           (eq $value "true") }}
                  {{- $skip = "true" }}
                {{- end }}
              {{- end }}
              {{- /* Auto-import eligible clusters */ -}}
              {{- if and (eq $dc.spec.status "Active") 
                         (contains (fromConfigMap "open-cluster-management-global-set" "discovery-config" "mce-hcp-filter") $dc.spec.displayName)
                         (eq $dc.spec.type "MultiClusterEngineHCP")
                         (eq $skip "false") }}
            - complianceType: musthave
              objectDefinition:
                apiVersion: discovery.open-cluster-management.io/v1
                kind: DiscoveredCluster
                metadata:
                  name: {{ $dc.metadata.name }}
                  namespace: {{ $dc.metadata.namespace }}
                spec:
                  importAsManagedCluster: true
              {{- end }}
            {{- end }}
---
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: policy-mce-hcp-autoimport-placement
  namespace: open-cluster-management-global-set
spec:
  tolerations:
    - key: cluster.open-cluster-management.io/unreachable
      operator: Exists
    - key: cluster.open-cluster-management.io/unavailable
      operator: Exists
  clusterSets:
    - global
  predicates:
    - requiredClusterSelector:
        labelSelector:
          matchExpressions:
            - key: local-cluster
              operator: In
              values:
                - "true"
---
apiVersion: policy.open-cluster-management.io/v1
kind: PlacementBinding
metadata:
  name: policy-mce-hcp-autoimport-placement-binding
  namespace: open-cluster-management-global-set
placementRef:
  name: policy-mce-hcp-autoimport-placement
  apiGroup: cluster.open-cluster-management.io
  kind: Placement
subjects:
  - name: policy-mce-hcp-autoimport
    apiGroup: policy.open-cluster-management.io
    kind: Policy
EOF
)
    
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: Would create auto-import policy:"
        echo "$policy_yaml"
        echo ""
        return
    fi
    
    echo "$policy_yaml" | oc apply -f -
    
    if [ $? -eq 0 ]; then
        log_success "Auto-import policy created successfully"
    else
        log_error "Failed to create auto-import policy"
        exit 1
    fi
}

# Verify policy deployment
verify_policy_deployment() {
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: Skipping policy verification"
        return
    fi
    
    log_info "Verifying policy deployment..."
    
    # Check if policy exists
    if oc get policy policy-mce-hcp-autoimport -n open-cluster-management-global-set &> /dev/null; then
        log_success "Policy 'policy-mce-hcp-autoimport' found"
        
        # Check policy status
        local compliant
        compliant=$(oc get policy policy-mce-hcp-autoimport -n open-cluster-management-global-set -o jsonpath='{.status.compliant}' 2>/dev/null || echo "Unknown")
        
        case "$compliant" in
            "Compliant")
                log_success "Policy is compliant"
                ;;
            "NonCompliant")
                log_warning "Policy is non-compliant (this may be normal initially)"
                ;;
            *)
                log_info "Policy compliance status: $compliant"
                ;;
        esac
    else
        log_error "Policy not found after creation"
        exit 1
    fi
    
    # Check placement
    if oc get placement policy-mce-hcp-autoimport-placement -n open-cluster-management-global-set &> /dev/null; then
        log_success "Placement 'policy-mce-hcp-autoimport-placement' found"
    else
        log_error "Placement not found after creation"
        exit 1
    fi
    
    # Check placement binding
    if oc get placementbinding policy-mce-hcp-autoimport-placement-binding -n open-cluster-management-global-set &> /dev/null; then
        log_success "PlacementBinding 'policy-mce-hcp-autoimport-placement-binding' found"
    else
        log_error "PlacementBinding not found after creation"
        exit 1
    fi
}

# Show current discovered clusters
show_discovered_clusters() {
    log_info "Current discovered clusters:"
    
    if oc get discoveredcluster --all-namespaces &> /dev/null; then
        oc get discoveredcluster --all-namespaces -o custom-columns=\
"NAMESPACE:.metadata.namespace,NAME:.metadata.name,DISPLAY_NAME:.spec.displayName,TYPE:.spec.type,STATUS:.spec.status,IMPORT:.spec.importAsManagedCluster"
    else
        log_info "No discovered clusters found yet"
        log_info "Discovered clusters will appear after hosted clusters are created in MCE clusters"
    fi
}

# Display policy summary
display_policy_summary() {
    log_info "Auto-Import Policy Configuration Summary:"
    echo "  - Policy Name: policy-mce-hcp-autoimport"
    echo "  - Namespace: open-cluster-management-global-set"
    echo "  - Filter Pattern: '$FILTER_PATTERN'"
    
    if [ -z "$FILTER_PATTERN" ]; then
        echo "  - Import Behavior: All discovered MultiClusterEngineHCP clusters will be imported"
    else
        echo "  - Import Behavior: Only clusters matching '$FILTER_PATTERN' will be imported"
    fi
    
    echo ""
    log_info "How the policy works:"
    echo "  1. Monitors all DiscoveredCluster resources of type 'MultiClusterEngineHCP'"
    echo "  2. Filters clusters based on the configured filter pattern"
    echo "  3. Automatically sets importAsManagedCluster=true for eligible clusters"
    echo "  4. Skips clusters with 'previously-auto-imported' annotation"
    echo ""
    log_info "To modify the filter pattern:"
    echo "  oc patch configmap discovery-config -n open-cluster-management-global-set --type=merge -p '{\"data\":{\"mce-hcp-filter\":\"<new-pattern>\"}}'"
    echo ""
    log_info "To disable auto-import temporarily:"
    echo "  oc patch policy policy-mce-hcp-autoimport -n open-cluster-management-global-set --type=merge -p '{\"spec\":{\"disabled\":true}}'"
}

# Create management scripts
create_management_scripts() {
    if [ "$DRY_RUN" = true ]; then
        log_info "DRY RUN: Skipping management script creation"
        return
    fi
    
    log_info "Creating management helper scripts..."
    
    # Script to update filter
    cat > /tmp/update-autoimport-filter.sh <<'EOF'
#!/bin/bash
# Helper script to update auto-import filter

if [ $# -ne 1 ]; then
    echo "Usage: $0 <filter_pattern>"
    echo "Example: $0 \"prod-\""
    echo "Example: $0 \"\" # Import all clusters"
    exit 1
fi

FILTER="$1"
echo "Updating auto-import filter to: '$FILTER'"

oc patch configmap discovery-config -n open-cluster-management-global-set \
    --type=merge -p "{\"data\":{\"mce-hcp-filter\":\"$FILTER\"}}"

if [ $? -eq 0 ]; then
    echo "Filter updated successfully"
else
    echo "Failed to update filter"
    exit 1
fi
EOF
    
    # Script to toggle policy
    cat > /tmp/toggle-autoimport-policy.sh <<'EOF'
#!/bin/bash
# Helper script to enable/disable auto-import policy

if [ $# -ne 1 ]; then
    echo "Usage: $0 <enable|disable>"
    exit 1
fi

case "$1" in
    "enable")
        echo "Enabling auto-import policy..."
        oc patch policy policy-mce-hcp-autoimport -n open-cluster-management-global-set \
            --type=merge -p '{"spec":{"disabled":false}}'
        ;;
    "disable")
        echo "Disabling auto-import policy..."
        oc patch policy policy-mce-hcp-autoimport -n open-cluster-management-global-set \
            --type=merge -p '{"spec":{"disabled":true}}'
        ;;
    *)
        echo "Invalid option. Use 'enable' or 'disable'"
        exit 1
        ;;
esac

if [ $? -eq 0 ]; then
    echo "Policy updated successfully"
else
    echo "Failed to update policy"
    exit 1
fi
EOF
    
    chmod +x /tmp/update-autoimport-filter.sh
    chmod +x /tmp/toggle-autoimport-policy.sh
    
    log_success "Management scripts created:"
    log_info "  - /tmp/update-autoimport-filter.sh"
    log_info "  - /tmp/toggle-autoimport-policy.sh"
}

# Main execution
main() {
    parse_arguments "$@"
    
    log_info "Starting auto-import policy setup..."
    
    if [ "$DRY_RUN" = true ]; then
        log_warning "DRY RUN MODE: No changes will be applied"
    fi
    
    check_prerequisites
    create_discovery_config
    create_autoimport_policy
    verify_policy_deployment
    show_discovered_clusters
    display_policy_summary
    create_management_scripts
    
    if [ "$DRY_RUN" = true ]; then
        log_success "DRY RUN completed - no changes were applied"
    else
        log_success "Auto-import policy setup completed!"
        log_info "Next steps:"
        log_info "1. Create hosted clusters in your MCE clusters"
        log_info "2. Monitor discovered clusters: oc get discoveredcluster --all-namespaces"
        log_info "3. Monitor policy compliance: oc get policy -n open-cluster-management-global-set"
        log_info "4. Check imported clusters: oc get managedcluster"
    fi
}

# Run main function
main "$@"
