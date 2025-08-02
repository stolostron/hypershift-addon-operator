# Converting ACM Hosting Clusters to MCE

This guide explains how to convert existing ACM (Advanced Cluster Management) hub clusters that are hosting control planes to MCE (Multi-Cluster Engine) clusters while preserving your existing hosted clusters.

## Why Convert ACM to MCE?

This conversion is necessary to implement [the recommended fleet management pattern](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/recommended_pattern.md), where:
- A central ACM hub provides unified management and visibility
- MCE clusters serve as optimized hosting infrastructure for control planes
- ACM cannot manage other ACM clusters, requiring hosting clusters to use MCE

## Prerequisites

Before starting this conversion process:

- ✅ Ensure you have cluster-admin access to the ACM cluster
- ✅ Take a backup of critical configurations and hosted cluster data
- ✅ Schedule maintenance window as this process involves cluster reconfiguration
- ✅ Verify all hosted clusters are in a healthy state

## Important Limitations and Warnings

⚠️ **Critical Limitation**: Hosted clusters in same-named namespaces cannot be preserved during this conversion.

**Example**: If a hosted cluster named `hc-1` exists in namespace `hc-1`, the detachment process will destroy the namespace and the hosted cluster. This is because the namespace removal triggers hosted cluster deletion.

**Recommendation**: Before proceeding, check your hosted cluster deployments:
```bash
oc get hostedcluster -A
```
If any hosted clusters are deployed in namespaces matching their names, consider migrating them to different namespaces first.

## Conversion Process

### Step 1: Disable MCE Management of HyperShift Operator

Prevent MCE from managing the HyperShift operator during the conversion:

```bash
# Add annotation to the operator deployment
oc annotate deployment operator -n hypershift hypershift.open-cluster-management.io/not-by-mce="true"
```

### Step 2: Disable HyperShift Local Hosting Component

Edit the MCE configuration to disable local hosting:

```bash
oc edit mce
```
Disable the following component by setting `enabled` to `false`:
```yaml
spec:
    - configOverrides: {}
      enabled: false
      name: hypershift-local-hosting
```

### Step 3: Verify HyperShift Operator Status

Confirm that the HyperShift operator continues running after disabling MCE management:

```bash
oc get deployment -n hypershift
```

Expected output:
```
NAME       READY   UP-TO-DATE   AVAILABLE   AGE
operator   2/2     2            2           3d3h
```

### Step 4: Verify Hosted Clusters Before Detachment

Document the current state of your hosted clusters:

```bash
oc get hostedcluster -A
```

Example output:
```
NAMESPACE   NAME   VERSION   KUBECONFIG              PROGRESS   AVAILABLE   MESSAGE
clusters    hc-1   4.18.0    hc-1-admin-kubeconfig   Completed  True        The hosted control plane is available
```

### Step 5: Detach Hosted Clusters from ACM

**Critical Step**: Detach all hosted clusters from ACM management before uninstalling ACM.

> **Note**: Detailed instructions for detaching hosted clusters depend on your specific ACM configuration. Ensure this step is completed successfully before proceeding. Otherwise you cannot uninstall ACM.

### Step 6: Verify Hosted Clusters After Detachment

Confirm hosted clusters remain operational after detachment:

```bash
oc get hostedcluster -A
```

The hosted clusters should still be present and available, but no longer managed by ACM.

### Step 7: Remove MCE Management Annotation

Remove the annotation added in Step 1 to allow the new MCE installation to manage HyperShift:

```bash
oc annotate deployment operator -n hypershift hypershift.open-cluster-management.io/not-by-mce-
```

### Step 8: Uninstall ACM and MCE

Remove the existing ACM and MCE installations:

> **Warning**: Ensure all hosted clusters are properly detached before proceeding with uninstallation.


### Step 9: Install MCE

Install MCE using your preferred method (Operator Hub, CLI, etc.):


### Step 10: Verify Automatic Import

After MCE installation completes, verify that existing hosted clusters are automatically imported:

```bash
oc get hostedcluster -A
```

The hosted clusters should now be managed by MCE instead of ACM.

## Post-Conversion Verification

After completing the conversion:

1. **Verify hosted cluster functionality**: Test that all hosted clusters are accessible and operational
2. **Check MCE management**: Confirm MCE is properly managing the HyperShift operator
3. **Hosted cluster import**: Confirm the existing hosted clusters are imported into MCE
4. **Test control plane operations**: Verify you can still create, scale, and manage hosted control planes


## Next Steps

After successful conversion:
1. Configure your central ACM hub to discover this MCE cluster
2. Refer to [Discovering Hosted Clusters from MCE Clusters](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/discovering_hostedclusters.md)
3. Validate the complete fleet management pattern is working as expected