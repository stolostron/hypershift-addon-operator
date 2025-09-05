#!/bin/bash

# setup-auto-import-policy.sh - Deploy auto-import policy for discovered hosted clusters
# This script automates the auto-import policy deployment from docs/discovering_hostedclusters.md

set -euo pipefail

# Configuration
POLICY_NAMESPACE="${POLICY_NAMESPACE:-open-cluster-management-global-set}"
DRY_RUN="${DRY_RUN:-false}"
FILTER_CONFIG="${FILTER_CONFIG:-}"
POLICY_NAME="${POLICY_NAME:-policy-mce-hcp-autoimport}"

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
Usage: $0 [OPTIONS]

Deploy auto-import policy for discovered hosted clusters.

Optional Arguments:
    --filter-config CONFIG     Filter configuration for cluster names (default: empty - import all)
    --policy-name NAME         Name for the policy (default: policy-mce-hcp-autoimport)
    --policy-namespace NS      Namespace for the policy (default: open-cluster-management-global-set)
    --dry-run                  Show what would be done without making changes
    --help, -h                 Show this help message

Environment Variables:
    POLICY_NAMESPACE           Policy namespace (default: open-cluster-management-global-set)
    DRY_RUN                   Enable dry run mode (default: false)
    FILTER_CONFIG             Filter configuration (default: empty)
    POLICY_NAME               Policy name (default: policy-mce-hcp-autoimport)

Examples:
    # Deploy policy to import all discovered clusters
    $0
    
    # Deploy with cluster name filter
    $0 --filter-config "prod-"
    
    # Custom policy name and namespace
    $0 --policy-name custom-autoimport --policy-namespace custom-namespace
EOF
}

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    if ! command -v oc &> /dev/null; then
        log_error "oc CLI is required but not installed"
        exit 1
    fi
    
    if ! oc auth can-i create policy -n "$POLICY_NAMESPACE" &> /dev/null; then
        log_error "Insufficient permissions to create Policy resources in namespace $POLICY_NAMESPACE"
        exit 1
    fi
    
    # Check if namespace exists
    if ! oc get namespace "$POLICY_NAMESPACE" &> /dev/null; then
        log_error "Policy namespace does not exist: $POLICY_NAMESPACE"
        exit 1
    fi
    
    log_info "Prerequisites check passed"
}

create_discovery_config() {
    log_info "Creating discovery configuration..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create discovery-config ConfigMap"
        return
    fi
    
    cat <<EOF | oc apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: discovery-config
  namespace: $POLICY_NAMESPACE
data:
  mce-hcp-filter: "$FILTER_CONFIG"
EOF
    
    log_info "Created discovery-config ConfigMap with filter: '$FILTER_CONFIG'"
}

create_auto_import_policy() {
    log_info "Creating auto-import policy..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create auto-import policy $POLICY_NAME"
        return
    fi
    
    cat <<EOF | oc apply -f -
apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata:
  name: $POLICY_NAME
  namespace: $POLICY_NAMESPACE
  annotations:
    policy.open-cluster-management.io/standards: NIST SP 800-53
    policy.open-cluster-management.io/categories: CM Configuration Management
    policy.open-cluster-management.io/controls: CM-2 Baseline Configuration
    policy.open-cluster-management.io/description: Discovered clusters that are of
      type MultiClusterEngineHCP can be automatically imported into ACM as managed clusters.
      This policy configure those discovered clusters so they are automatically imported. 
      Fine tuning MultiClusterEngineHCP clusters to be automatically imported
      can be done by configure filters at the configMap or add annotation to the discoverd cluster.
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
                  namespace: $POLICY_NAMESPACE
                data:
                  mce-hcp-filter: "$FILTER_CONFIG"
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
            {{- /* find the MultiClusterEngineHCP DiscoveredClusters */ -}}
            {{- range \$dc := (lookup "discovery.open-cluster-management.io/v1" "DiscoveredCluster" "" "").items }}
              {{- /* Check for the flag that indicates the import should be skipped */ -}}
              {{- \$skip := "false" -}}
              {{- range \$key, \$value := \$dc.metadata.annotations }}
                {{- if and (eq \$key "discovery.open-cluster-management.io/previously-auto-imported")
                           (eq \$value "true") }}
                  {{- \$skip = "true" }}
                {{- end }}
              {{- end }}
              {{- /* if the type is MultiClusterEngineHCP and the status is Active */ -}}
              {{- if and (eq \$dc.spec.status "Active") 
                         (contains (fromConfigMap "$POLICY_NAMESPACE" "discovery-config" "mce-hcp-filter") \$dc.spec.displayName)
                         (eq \$dc.spec.type "MultiClusterEngineHCP")
                         (eq \$skip "false") }}
            - complianceType: musthave
              objectDefinition:
                apiVersion: discovery.open-cluster-management.io/v1
                kind: DiscoveredCluster
                metadata:
                  name: {{ \$dc.metadata.name }}
                  namespace: {{ \$dc.metadata.namespace }}
                spec:
                  importAsManagedCluster: true
              {{- end }}
            {{- end }}
EOF
    
    log_info "Created auto-import policy: $POLICY_NAME"
}

create_placement() {
    local placement_name="${POLICY_NAME}-placement"
    
    log_info "Creating placement for policy..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create placement $placement_name"
        return
    fi
    
    cat <<EOF | oc apply -f -
apiVersion: cluster.open-cluster-management.io/v1beta1
kind: Placement
metadata:
  name: $placement_name
  namespace: $POLICY_NAMESPACE
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
EOF
    
    log_info "Created placement: $placement_name"
}

create_placement_binding() {
    local placement_name="${POLICY_NAME}-placement"
    local binding_name="${POLICY_NAME}-placement-binding"
    
    log_info "Creating placement binding..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would create placement binding $binding_name"
        return
    fi
    
    cat <<EOF | oc apply -f -
apiVersion: policy.open-cluster-management.io/v1
kind: PlacementBinding
metadata:
  name: $binding_name
  namespace: $POLICY_NAMESPACE
placementRef:
  name: $placement_name
  apiGroup: cluster.open-cluster-management.io
  kind: Placement
subjects:
  - name: $POLICY_NAME
    apiGroup: policy.open-cluster-management.io
    kind: Policy
EOF
    
    log_info "Created placement binding: $binding_name"
}

verify_policy_deployment() {
    log_info "Verifying policy deployment..."
    
    if $DRY_RUN; then
        log_info "[DRY RUN] Would verify policy deployment"
        return
    fi
    
    # Check if policy exists and is compliant
    local policy_status
    policy_status=$(oc get policy "$POLICY_NAME" -n "$POLICY_NAMESPACE" -o jsonpath='{.status.compliant}' 2>/dev/null || echo "")
    
    if [[ -n "$policy_status" ]]; then
        log_info "Policy status: $policy_status"
        
        # Show policy details
        echo "Policy details:"
        oc get policy "$POLICY_NAME" -n "$POLICY_NAMESPACE" -o wide
    else
        log_warn "Could not get policy status"
    fi
    
    # Check placement
    if oc get placement "${POLICY_NAME}-placement" -n "$POLICY_NAMESPACE" &> /dev/null; then
        log_info "✓ Placement created successfully"
    else
        log_error "✗ Placement not found"
    fi
    
    # Check placement binding
    if oc get placementbinding "${POLICY_NAME}-placement-binding" -n "$POLICY_NAMESPACE" &> /dev/null; then
        log_info "✓ Placement binding created successfully"
    else
        log_error "✗ Placement binding not found"
    fi
}

show_monitoring_instructions() {
    log_info "Auto-import policy has been deployed successfully!"
    log_info ""
    log_info "How it works:"
    log_info "1. The hypershift addon discovers hosted clusters and creates DiscoveredCluster resources"
    log_info "2. This policy watches for DiscoveredCluster resources of type 'MultiClusterEngineHCP'"
    log_info "3. When found, it sets 'importAsManagedCluster: true' to trigger auto-import"
    log_info "4. ACM's discovery operator imports the cluster as a ManagedCluster"
    log_info ""
    log_info "Monitoring commands:"
    log_info "  # View discovered clusters"
    log_info "  oc get discoveredcluster --all-namespaces"
    log_info ""
    log_info "  # View policy status"
    log_info "  oc get policy $POLICY_NAME -n $POLICY_NAMESPACE"
    log_info ""
    log_info "  # View managed clusters"
    log_info "  oc get managedcluster"
    log_info ""
    if [[ -n "$FILTER_CONFIG" ]]; then
        log_info "Filter configuration: Only clusters with names containing '$FILTER_CONFIG' will be auto-imported"
    else
        log_info "Filter configuration: All discovered MultiClusterEngineHCP clusters will be auto-imported"
    fi
}

main() {
    echo "=========================================="
    echo "Auto-Import Policy Setup"
    echo "=========================================="
    echo "Policy Name: $POLICY_NAME"
    echo "Policy Namespace: $POLICY_NAMESPACE"
    echo "Filter Config: '$FILTER_CONFIG'"
    echo "Dry Run: $DRY_RUN"
    echo "=========================================="
    
    check_prerequisites
    
    log_info "Step 1: Creating discovery configuration..."
    create_discovery_config
    
    log_info "Step 2: Creating auto-import policy..."
    create_auto_import_policy
    
    log_info "Step 3: Creating placement..."
    create_placement
    
    log_info "Step 4: Creating placement binding..."
    create_placement_binding
    
    log_info "Step 5: Verifying deployment..."
    verify_policy_deployment
    
    echo "=========================================="
    log_info "Auto-import policy setup completed!"
    echo "=========================================="
    
    show_monitoring_instructions
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --filter-config)
            FILTER_CONFIG="$2"
            shift 2
            ;;
        --policy-name)
            POLICY_NAME="$2"
            shift 2
            ;;
        --policy-namespace)
            POLICY_NAMESPACE="$2"
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
