# MCE-ACM Integration Automation Scripts

This directory contains automation scripts for setting up and managing the integration between MultiCluster Engine (MCE) hosting clusters and Red Hat Advanced Cluster Management (ACM), as described in the [`discovering_hostedclusters.md`](../docs/discovering_hostedclusters.md) documentation.

## Overview

These scripts automate the complete process of:
1. Preparing ACM Hub for MCE integration
2. Importing MCE clusters into ACM
3. Enabling HyperShift addon for cluster discovery
4. Setting up auto-import policies for discovered hosted clusters
5. Verifying the integration
6. Backing up critical resources for disaster recovery

## Prerequisites

Before using these scripts, ensure you have:

- **OpenShift CLI (`oc`)**: Installed and configured
- **Cluster Admin Access**: To both ACM hub and MCE clusters
- **clusteradm CLI**: For addon management ([Installation](https://github.com/open-cluster-management-io/clusteradm/releases))
- **Network Connectivity**: Between ACM hub and MCE clusters
- **ACM Hub Cluster**: With **full Red Hat Advanced Cluster Management (ACM)** installed
  - ⚠️ **Important**: MCE-only installations are not sufficient
  - ACM governance policies are required for auto-import functionality
- **MCE Clusters**: With MCE operator installed and hosted clusters capability

## Scripts Overview

### 🚀 Main Orchestration Script

#### `setup-mce-acm-integration.sh`
**Complete end-to-end setup automation**

```bash
# Interactive mode (recommended for first-time setup)
./setup-mce-acm-integration.sh

# Non-interactive mode
./setup-mce-acm-integration.sh \
  --non-interactive \
  --mce-clusters "mce-cluster-1,mce-cluster-2" \
  --discovery-prefix "prod-" \
  --autoimport-filter "prod-"
```

**Options:**
- `--non-interactive`: Run without user prompts
- `--mce-clusters <names>`: Comma-separated MCE cluster names
- `--discovery-prefix <prefix>`: Custom prefix for discovered clusters
- `--autoimport-filter <filter>`: Pattern for auto-importing clusters
- `--skip-verification`: Skip final verification step
- `--skip-backup`: Skip backup step

### 📋 Individual Step Scripts

#### 1. `setup-acm-hub.sh`
**Prepare ACM Hub for MCE Integration**

Configures ACM hub with necessary addon deployment configurations, klusterlet settings, and cluster management addons.

```bash
./setup-acm-hub.sh
```

**What it does:**
- Creates `AddOnDeploymentConfig` for addon namespace isolation
- Updates `ClusterManagementAddOn` resources
- Creates `KlusterletConfig` for MCE cluster imports
- Verifies configuration deployment

#### 2. `import-mce-cluster.sh`
**Import MCE Clusters into ACM**

Imports individual MCE clusters as managed clusters in ACM using kubeconfig files.

```bash
# Import MCE cluster with kubeconfig
./import-mce-cluster.sh mce-cluster-1 /path/to/mce-cluster-1-kubeconfig

# Import with different kubeconfig location
./import-mce-cluster.sh prod-mce ~/.kube/prod-mce-config
```

**Features:**
- Creates `ManagedCluster` resources with proper annotations
- Automatically creates auto-import secret using provided kubeconfig
- Extracts server URL from kubeconfig automatically
- Monitors import progress until completion

#### 3. `enable-hypershift-addon.sh`
**Enable HyperShift Addon for Discovery**

Enables and configures the HyperShift addon on MCE clusters for hosted cluster discovery.

```bash
# Interactive cluster selection
./enable-hypershift-addon.sh

# Specify clusters directly
./enable-hypershift-addon.sh mce-cluster-1,mce-cluster-2

# Custom discovery prefix
./enable-hypershift-addon.sh --discovery-prefix "custom-"

# Remove prefix (use hosted cluster names only)
./enable-hypershift-addon.sh --discovery-prefix ""
```

**Configuration:**
- Sets addon installation namespace
- Configures discovery prefix for cluster naming
- Disables metrics and HO management for performance
- Verifies addon deployment

#### 4. `setup-autoimport-policy.sh`
**Setup Auto-Import Policy**

Creates ACM policies to automatically import discovered hosted clusters.

```bash
# Import all discovered clusters
./setup-autoimport-policy.sh

# Import only clusters matching pattern
./setup-autoimport-policy.sh --filter "prod-"

# Preview without applying
./setup-autoimport-policy.sh --dry-run
```

**Features:**
- Creates policy with configurable filters
- Generates placement and placement binding resources
- Creates helper scripts for filter management
- Supports dry-run mode

### 🔍 Verification and Management Scripts

#### `verify-mce-integration.sh`
**Comprehensive Integration Verification**

Verifies all aspects of the MCE-ACM integration setup.

```bash
# Verify entire integration
./verify-mce-integration.sh

# Focus on specific cluster
./verify-mce-integration.sh --cluster mce-cluster-1

# Detailed output
./verify-mce-integration.sh --verbose
```

**Verification Areas:**
- ACM hub configuration
- Managed cluster status
- Addon deployments
- Discovered clusters
- Auto-import policies
- Network connectivity

#### `backup-mce-resources.sh`
**Backup Critical Resources**

Labels and backs up critical resources for disaster recovery.

```bash
# Create backup with default location
./backup-mce-resources.sh

# Custom backup directory
./backup-mce-resources.sh --backup-dir /path/to/backup

# Preview without creating backup
./backup-mce-resources.sh --dry-run
```

**Backup Contents:**
- Configuration resources (AddOnDeploymentConfig, KlusterletConfig)
- Addon resources (ClusterManagementAddOn)
- Policy resources (Policy, Placement, PlacementBinding)
- Managed cluster configurations
- Restore script and manifest

### 🗑️ Cleanup and Undo Scripts

#### `cleanup-mce-acm-integration.sh`
**Complete Integration Cleanup**

Removes the entire MCE-ACM integration by undoing all setup steps in reverse order.

```bash
# Interactive cleanup (recommended)
./cleanup-mce-acm-integration.sh --mce-clusters mce-a,mce-b

# Non-interactive cleanup
./cleanup-mce-acm-integration.sh --mce-clusters mce-a,mce-b --non-interactive --force

# Skip confirmation prompt
./cleanup-mce-acm-integration.sh --mce-clusters mce-a --skip-confirmation
```

**Features:**
- Removes all imported MCE clusters from ACM
- Disables HyperShift addons from all clusters
- Resets ACM Hub configuration to defaults
- Removes auto-import policies (if present)
- Comprehensive verification of cleanup

#### Individual Undo Scripts

**`undo-hypershift-addon.sh`** - Disable HyperShift addons
```bash
./undo-hypershift-addon.sh mce-cluster-1,mce-cluster-2
```

**`undo-import-mce-cluster.sh`** - Remove MCE cluster from ACM
```bash
./undo-import-mce-cluster.sh mce-cluster-1
```

**`undo-acm-hub.sh`** - Reset ACM Hub configuration
```bash
./undo-acm-hub.sh --force
```

## Usage Patterns

### 🎯 Quick Start (Recommended)

For most users, the main orchestration script provides the easiest setup:

```bash
# Make scripts executable
chmod +x scripts/*.sh

# Run interactive setup
./scripts/setup-mce-acm-integration.sh
```

This will guide you through the entire process with prompts for configuration.

### 🔧 Step-by-Step Setup

For more control or troubleshooting, run individual scripts:

```bash
# Step 1: Prepare ACM Hub
./scripts/setup-acm-hub.sh

# Step 2: Import MCE clusters
./scripts/import-mce-cluster.sh mce-cluster-1 /path/to/mce-cluster-1-kubeconfig
./scripts/import-mce-cluster.sh mce-cluster-2 /path/to/mce-cluster-2-kubeconfig

# Step 3: Enable HyperShift addon
./scripts/enable-hypershift-addon.sh mce-cluster-1,mce-cluster-2

# Step 4: Setup auto-import policy
./scripts/setup-autoimport-policy.sh --filter "prod-"

# Step 5: Verify setup
./scripts/verify-mce-integration.sh

# Step 6: Backup resources
./scripts/backup-mce-resources.sh
```

### 🤖 Automated CI/CD Pipeline

For automated deployments:

```bash
./scripts/setup-mce-acm-integration.sh \
  --non-interactive \
  --mce-clusters "mce-prod-1,mce-prod-2" \
  --discovery-prefix "prod-" \
  --autoimport-filter "prod-" \
  --skip-verification
```

### 🗑️ Complete Cleanup

To completely remove the integration:

```bash
# Interactive cleanup (safest)
./scripts/cleanup-mce-acm-integration.sh --mce-clusters mce-a,mce-b

# Automated cleanup
./scripts/cleanup-mce-acm-integration.sh \
  --mce-clusters mce-a,mce-b \
  --non-interactive \
  --force \
  --skip-confirmation
```

### 🔧 Partial Cleanup

For selective cleanup:

```bash
# Remove specific cluster
./scripts/undo-import-mce-cluster.sh mce-cluster-1

# Disable HyperShift addon only
./scripts/undo-hypershift-addon.sh mce-cluster-1

# Reset ACM Hub only
./scripts/undo-acm-hub.sh
```

## Configuration Examples

### Understanding Discovery Prefix vs Auto-Import Filter

These two configuration options serve different purposes and work at different stages of the MCE-ACM integration:

#### 🔍 **Discovery Prefix** - Controls HOW clusters are NAMED

- **Purpose**: Determines the naming convention for discovered hosted clusters
- **When applied**: During discovery when HyperShift addon finds hosted clusters
- **Scope**: Affects ALL discovered clusters from a specific MCE cluster
- **Configured in**: HyperShift addon deployment config
- **Default behavior**: Uses `<mce-cluster-name>-` as prefix if not specified

#### 🎯 **Auto-Import Filter** - Controls WHICH clusters are IMPORTED

- **Purpose**: Selectively chooses which discovered clusters should be automatically imported
- **When applied**: After discovery, when auto-import policy evaluates clusters
- **Scope**: Can filter any discovered cluster based on its display name
- **Configured in**: Auto-import policy ConfigMap

#### 📊 **Comparison Table**

| Aspect | Discovery Prefix | Auto-Import Filter |
|--------|------------------|-------------------|
| **Purpose** | Naming convention | Import selection |
| **Affects** | All discovered clusters | Only matching clusters |
| **When applied** | During discovery | During import evaluation |
| **Can be empty** | No (uses default) | Yes (import all) |
| **Examples** | `prod-`, `staging-`, `<default>` | `prod-`, `web`, `critical-app` |

#### 🔄 **How They Work Together**

```bash
# Example: Production environment setup
./enable-hypershift-addon.sh --discovery-prefix "prod-"
./setup-autoimport-policy.sh --filter "prod-"

# Result flow:
# 1. MCE hosted cluster "web-app" → discovered as "prod-web-app"
# 2. Auto-import policy sees "prod-web-app" → matches "prod-" filter
# 3. "prod-web-app" gets imported as managed cluster in ACM
```

#### 🎯 **Common Use Cases**

**Environment Separation:**
```bash
# Production MCE cluster
./enable-hypershift-addon.sh --discovery-prefix "prod-"
./setup-autoimport-policy.sh --filter "prod-"

# Staging MCE cluster  
./enable-hypershift-addon.sh --discovery-prefix "staging-"
./setup-autoimport-policy.sh --filter "staging-"
```

**Selective Import with Standard Naming:**
```bash
# Discover all with default naming
./enable-hypershift-addon.sh

# Only auto-import critical clusters
./setup-autoimport-policy.sh --filter "critical"
```

**Custom Prefix with Full Import:**
```bash
# Use custom prefix for all clusters
./enable-hypershift-addon.sh --discovery-prefix "myorg-"

# Import everything discovered
./setup-autoimport-policy.sh --filter ""
```

### Discovery Prefix Configuration

The discovery prefix determines how discovered hosted clusters are named:

```bash
# Default: mce-cluster-1-hosted-cluster-name
./enable-hypershift-addon.sh

# Custom prefix: prod-hosted-cluster-name
./enable-hypershift-addon.sh --discovery-prefix "prod-"

# Note: Empty string is not supported for discovery prefix
# The system will always use either your custom prefix or the default MCE cluster name
```

### Auto-Import Filter Examples

Control which discovered clusters are automatically imported:

```bash
# Import all discovered clusters
./setup-autoimport-policy.sh --filter ""

# Import only production clusters
./setup-autoimport-policy.sh --filter "prod-"

# Import clusters containing "staging"
./setup-autoimport-policy.sh --filter "staging"

# Import specific cluster
./setup-autoimport-policy.sh --filter "test-cluster-1"
```

## Troubleshooting

### Common Issues and Solutions

1. **ACM vs MCE Installation Issues**
   ```bash
   # Check what's installed
   oc get csv -A | grep -E "(advanced-cluster-management|multicluster-engine)"
   
   # For MCE-only installations:
   # - Use individual scripts manually (setup-acm-hub.sh will fail)
   # - Skip auto-import policy setup
   # - Manually import discovered clusters when they appear
   ```

2. **Script Not Found Errors**
   ```bash
   # Make scripts executable
   chmod +x scripts/*.sh
   
   # Verify script location
   ls -la scripts/
   ```

3. **Permission Denied**
   ```bash
   # Verify cluster-admin access
   oc auth can-i '*' '*' --all-namespaces
   
   # Check current user
   oc whoami
   ```

3. **Import Failures**
   ```bash
   # Check cluster connectivity
   ./scripts/verify-mce-integration.sh --cluster <cluster-name>
   
   # Verify import status
   oc get managedcluster
   ```

4. **Addon Issues**
   ```bash
   # Check addon status
   oc get managedclusteraddon --all-namespaces
   
   # Check addon logs
   oc logs -n open-cluster-management-agent-addon-discovery deploy/hypershift-addon-agent
   ```

5. **Discovery Not Working**
   ```bash
   # Verify HyperShift addon configuration
   oc get addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine -o yaml
   
   # Check for hosted clusters in MCE
   # (Run on MCE cluster)
   oc get hostedcluster --all-namespaces
   ```

### Debug Commands

```bash
# Check overall system status
./scripts/verify-mce-integration.sh --verbose

# Monitor resources
watch 'oc get managedcluster; echo ""; oc get discoveredcluster --all-namespaces'

# Check policy compliance
oc get policy -n open-cluster-management-global-set

# View addon deployments
oc get deployment -n open-cluster-management-agent-addon-discovery
```

## Recovery and Maintenance

### Disaster Recovery

If you need to restore your MCE-ACM integration:

```bash
# Create backup (should be done regularly)
./scripts/backup-mce-resources.sh

# To restore (after ACM hub recovery)
cd <backup-directory>
./restore.sh
```

### Update Configuration

To modify existing configuration:

```bash
# Update discovery prefix
oc patch addondeploymentconfig hypershift-addon-deploy-config \
  -n multicluster-engine --type=merge \
  -p '{"spec":{"customizedVariables":[{"name":"discoveryPrefix","value":"new-prefix-"}]}}'

# Update auto-import filter
oc patch configmap discovery-config \
  -n open-cluster-management-global-set --type=merge \
  -p '{"data":{"mce-hcp-filter":"new-pattern"}}'

# Disable auto-import temporarily
oc patch policy policy-mce-hcp-autoimport \
  -n open-cluster-management-global-set --type=merge \
  -p '{"spec":{"disabled":true}}'
```

### Cleanup

To remove the integration:

```bash
# Remove auto-import policy
oc delete policy policy-mce-hcp-autoimport -n open-cluster-management-global-set
oc delete placement policy-mce-hcp-autoimport-placement -n open-cluster-management-global-set
oc delete placementbinding policy-mce-hcp-autoimport-placement-binding -n open-cluster-management-global-set

# Disable HyperShift addon
clusteradm addon disable --names hypershift-addon --clusters <cluster-names>

# Detach MCE clusters
oc delete managedcluster <mce-cluster-names>
```

## Best Practices

1. **Test in Development**: Always test scripts in a development environment first
2. **Regular Backups**: Run backup script regularly or integrate into CI/CD
3. **Monitor Resources**: Use verification script to monitor system health
4. **Gradual Rollout**: Import and configure one MCE cluster before scaling
5. **Documentation**: Keep track of your specific configuration choices
6. **Version Control**: Store your configuration parameters in version control

## Contributing

When modifying these scripts:

1. Test thoroughly in development environments
2. Update documentation for any new options or behaviors
3. Follow the existing error handling and logging patterns
4. Ensure scripts remain idempotent where possible
5. Add appropriate validation for new parameters

## Support

For issues with these automation scripts:

1. Check the troubleshooting section above
2. Run the verification script with `--verbose` flag
3. Review the original documentation: [`discovering_hostedclusters.md`](../docs/discovering_hostedclusters.md)
4. Check OpenShift and ACM logs for detailed error messages

## Script Dependencies

```
setup-mce-acm-integration.sh (main orchestration)
├── setup-acm-hub.sh
├── import-mce-cluster.sh
├── enable-hypershift-addon.sh
├── setup-autoimport-policy.sh
├── verify-mce-integration.sh
└── backup-mce-resources.sh
```

All scripts are designed to be run independently or as part of the main orchestration script.
