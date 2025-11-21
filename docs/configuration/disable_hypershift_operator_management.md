# Disabling HyperShift Operator Lifecycle Management

This guide explains how to prevent the HyperShift addon agent from managing the lifecycle of the HyperShift operator on a hosting cluster.

## Overview

By default, when the HyperShift addon (`ManagedClusterAddon`) is installed on a hosting cluster, the addon agent automatically:
- Installs the HyperShift operator
- Upgrades the HyperShift operator when MCE/ACM is upgraded
- Reinstalls the HyperShift operator if configuration changes are detected

In some scenarios, you may want to manage the HyperShift operator lifecycle manually or through a different mechanism. The `hypershift.open-cluster-management.io/not-by-mce` annotation allows you to disable this automatic management.

> **Note**: This annotation provides **per-cluster** control. If you want to disable HyperShift operator lifecycle management **globally** across all managed clusters, see the [Global Configuration](#global-configuration-for-all-managed-clusters) section below.

## Use Cases

You might want to disable automatic management in the following scenarios:

1. **Custom HyperShift Installation**: You have a customized HyperShift operator installation with specific configurations that should not be modified by the addon agent
2. **Manual Upgrade Control**: You want to control when and how the HyperShift operator is upgraded independently of MCE/ACM upgrades
3. **Testing and Development**: You're testing custom HyperShift operator builds and don't want them overwritten
4. **Migration Scenarios**: During cluster migrations or conversions (e.g., ACM to MCE), you want to preserve the existing operator installation

## How It Works

When the `hypershift.open-cluster-management.io/not-by-mce` annotation is set to `"true"` on the HyperShift operator deployment:

1. The addon agent detects the annotation during its reconciliation loop
2. The agent skips all HyperShift operator installation and upgrade operations
3. The agent continues to monitor but does not modify the operator deployment
4. Hosted clusters continue to function normally
5. Other addon agent functions (auto-import, discovery, metrics) continue to work

The addon agent checks this annotation:
- During controller startup
- When image override configmaps change
- When operator secrets are updated
- During periodic reconciliation

## Adding the Annotation

To disable HyperShift operator lifecycle management, add the annotation to the operator deployment in the `hypershift` namespace:

```bash
oc annotate deployment operator -n hypershift \
  hypershift.open-cluster-management.io/not-by-mce="true"
```

### Verification

Verify the annotation has been applied:

```bash
oc get deployment operator -n hypershift -o jsonpath='{.metadata.annotations}' | grep not-by-mce
```

Expected output:
```
"hypershift.open-cluster-management.io/not-by-mce":"true"
```

### Check Addon Agent Logs

After adding the annotation, you can verify the addon agent recognizes it by checking the logs:

```bash
oc logs -n open-cluster-management-agent-addon \
  deployment/hypershift-addon-agent | grep "not deployed by addon"
```

Expected log message:
```
"hypershift operator exists but not deployed by addon, skip update"
```

## Removing the Annotation

To re-enable automatic HyperShift operator lifecycle management, remove the annotation:

```bash
oc annotate deployment operator -n hypershift \
  hypershift.open-cluster-management.io/not-by-mce-
```

**Note**: The trailing hyphen (`-`) is important as it removes the annotation.

After removing the annotation:
1. The addon agent will resume managing the operator
2. On the next reconciliation, it may upgrade or reinstall the operator to match the expected state
3. Any manual changes to the operator deployment may be overwritten

## Global Configuration for All Managed Clusters

The annotation-based approach described above allows you to disable HyperShift operator lifecycle management on a **per-cluster basis**. However, if you want to disable this management **globally** for all managed clusters where the HyperShift addon agent is deployed, you can use the `AddonDeploymentConfig` resource.

### Setting Global Configuration

To disable HyperShift operator management globally across all managed clusters, set the `disableHOManagement` variable to `"true"` in the `hypershift-addon-deploy-config` AddonDeploymentConfig on the hub cluster:

```bash
oc patch addondeploymentconfig hypershift-addon-deploy-config \
  -n multicluster-engine --type=merge \
  -p '{"spec":{"customizedVariables":[{"name":"disableHOManagement","value":"true"}]}}'
```

### Verification

Verify the configuration has been applied:

```bash
oc get addondeploymentconfig hypershift-addon-deploy-config \
  -n multicluster-engine -o jsonpath='{.spec.customizedVariables}' | jq
```

Expected output should include:
```json
[
  {
    "name": "disableHOManagement",
    "value": "true"
  }
]
```

### Re-enabling Global Management

To re-enable automatic HyperShift operator lifecycle management globally, set the value to `"false"`:

```bash
oc patch addondeploymentconfig hypershift-addon-deploy-config \
  -n multicluster-engine --type=merge \
  -p '{"spec":{"customizedVariables":[{"name":"disableHOManagement","value":"false"}]}}'
```

Or remove the variable entirely by editing the AddonDeploymentConfig:

```bash
oc edit addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine
```

Then remove the `disableHOManagement` entry from `spec.customizedVariables`.

### How Global Configuration Works

When `disableHOManagement` is set to `"true"` in the AddonDeploymentConfig:

1. The configuration is propagated to all managed clusters via the addon agent deployment
2. The `DISABLE_HO_MANAGEMENT` environment variable is set to `"true"` in the addon agent pods
3. All addon agents across all managed clusters will skip HyperShift operator installation and upgrade operations
4. This applies to all current and future managed clusters where the addon is deployed

### Per-Cluster vs Global Configuration

| Configuration Method | Scope | Use Case | Configuration Location |
|---------------------|-------|----------|----------------------|
| **Annotation** | Per-cluster | Disable management on specific clusters | Individual managed cluster (on operator deployment) |
| **AddonDeploymentConfig** | Global | Disable management on all clusters | Hub cluster (in multicluster-engine namespace) |

**Key Differences:**

- **Annotation**: Use when you need fine-grained control over individual clusters (e.g., one cluster needs manual management while others remain automatic)
- **AddonDeploymentConfig**: Use when you want consistent behavior across all clusters (e.g., all HyperShift operators are managed via GitOps)

### Precedence and Interaction

If **both** the global `disableHOManagement` setting and the per-cluster annotation are used:
- The global setting affects the addon agent's default behavior
- The per-cluster annotation provides additional control at the deployment level
- Both mechanisms will result in the operator lifecycle management being disabled

**Recommendation**: Choose one approach based on your requirements:
- Use the global setting for uniform policies across all clusters
- Use the per-cluster annotation for exceptions or specific cluster requirements
- Avoid mixing both unless you have a specific architectural reason

## Important Considerations

### 🚨 Warnings

1. **Manual Management Required**: When this annotation is set, you are responsible for:
   - Installing and upgrading the HyperShift operator
   - Maintaining compatibility with MCE/ACM versions
   - Managing operator configuration and secrets
   - Troubleshooting operator issues

2. **Version Compatibility**: Ensure your manually managed HyperShift operator version is compatible with:
   - The MCE/ACM version on the hub cluster
   - The OpenShift version of hosted clusters
   - Other HyperShift components

3. **Image References**: The operator may reference different images than those provided by MCE/ACM, which could lead to:
   - Incompatibility issues
   - Unsupported configurations
   - Upgrade problems

4. **Support Impact**: Using a manually managed HyperShift operator may:
   - Affect supportability of your configuration
   - Require additional troubleshooting steps
   - Be outside standard support boundaries

### ✅ Best Practices

1. **Document Your Changes**: Keep detailed records of:
   - Why the annotation was added
   - What custom configurations are in place
   - The operator version being used

2. **Monitor Compatibility**: Regularly check that your operator version is compatible with:
   - MCE/ACM releases
   - Hosted cluster versions
   - Security patches and updates

3. **Test Before Production**: Test the annotation and manual management in a non-production environment first

4. **Plan for Re-enablement**: Have a plan to remove the annotation and return to automatic management if needed

5. **Backup Critical Resources**: Before making changes, backup:
   - The HyperShift operator deployment
   - Related secrets and configmaps
   - Hosted cluster configurations

## Example Workflows

### Scenario 1: Testing a Custom HyperShift Operator Build on a Single Cluster

```bash
# 1. Add the annotation to prevent automatic management on this specific cluster
oc annotate deployment operator -n hypershift \
  hypershift.open-cluster-management.io/not-by-mce="true"

# 2. Verify the annotation
oc get deployment operator -n hypershift -o yaml | grep not-by-mce

# 3. Update the operator with your custom image
oc set image deployment/operator -n hypershift \
  operator=quay.io/myorg/hypershift-operator:custom-build

# 4. Verify the operator is running with your custom image
oc get deployment operator -n hypershift -o jsonpath='{.spec.template.spec.containers[0].image}'

# 5. Now the hypershift operator will remain at the current version until the annotation is removed.

# 6. When done, remove the annotation to restore automatic management
oc annotate deployment operator -n hypershift \
  hypershift.open-cluster-management.io/not-by-mce-

# 7. The addon agent will reconcile back to the standard configuration
```

### Scenario 2: Disabling HyperShift Operator Management Globally for GitOps

```bash
# On the hub cluster, disable operator management globally
oc patch addondeploymentconfig hypershift-addon-deploy-config \
  -n multicluster-engine --type=merge \
  -p '{"spec":{"customizedVariables":[{"name":"disableHOManagement","value":"true"}]}}'

# Verify the configuration
oc get addondeploymentconfig hypershift-addon-deploy-config \
  -n multicluster-engine -o jsonpath='{.spec.customizedVariables}' | jq

# The configuration will be propagated to all managed clusters
# Verify on a managed cluster that the environment variable is set
oc get deployment hypershift-addon-agent \
  -n open-cluster-management-agent-addon \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DISABLE_HO_MANAGEMENT")].value}'

# Expected output: true

# Now the hypershift operator will remain at the current version until the configuration is removed.

# To re-enable automatic management later (e.g., transitioning back from GitOps):
oc patch addondeploymentconfig hypershift-addon-deploy-config \
  -n multicluster-engine --type=merge \
  -p '{"spec":{"customizedVariables":[{"name":"disableHOManagement","value":"false"}]}}'
```

## Related Documentation

- [HyperShift Operator Configuration](./hypershift_operator_configuration.md) - Standard operator configuration options
- [Upgrading HyperShift Operator](../optional/upgrading_hypershift_operator.md) - Automatic upgrade mechanisms
- [ACM to MCE Migration](./acm_to_mce.md) - Using the annotation during migrations
- [AKS Configuration](../optional/aks_configuration.md) - Example of using global `disableHOManagement` setting
- [Supported Versions](../reference/supported_versions.md) - Version compatibility information

## Summary

There are two mechanisms to disable automatic HyperShift operator lifecycle management:

1. **Per-Cluster Control**: The `hypershift.open-cluster-management.io/not-by-mce="true"` annotation on the operator deployment in individual managed clusters
2. **Global Control**: The `disableHOManagement="true"` variable in the `hypershift-addon-deploy-config` AddonDeploymentConfig on the hub cluster

Both mechanisms are useful for specific scenarios requiring manual control, but should be used carefully with full understanding of the responsibilities and risks involved. Choose the appropriate mechanism based on whether you need per-cluster flexibility or consistent global behavior. Always plan for re-enabling automatic management and maintain good documentation of your custom configuration.