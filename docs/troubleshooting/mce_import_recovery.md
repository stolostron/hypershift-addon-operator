# Troubleshooting MCE Import and Recovery from Misconfiguration

This guide provides detailed instructions on how to troubleshoot issues during the MultiCluster Engine (MCE) import process and how to recover from misconfigurations.

## Troubleshooting MCE Import

### Error: Klusterlet CRD already exists

**Symptom:**

When attempting to import an MCE cluster, the import fails with an error similar to:

```
Error from server (AlreadyExists): error when creating "STDIN": customresourcedefinitions.apiextensions.k8s.io "klusterlets.operator.open-cluster-management.io" already exists
```

In the ACM console, the MCE cluster status remains in "Pending import".

**Causes:**

This error occurs because the MCE cluster already has a self-managed `local-cluster`, and the import process is attempting to install the Klusterlet in a way that conflicts with the existing configuration. This typically happens in two scenarios:

1.  **Missing Hub Configuration:** You attempted to import an MCE cluster without first configuring the ACM hub to install foundation addons in a separate namespace (as documented in [Discovering and Managing Hosted Clusters](../management/discovering_hostedclusters.md)).
2.  **Missing Import Annotation (UI Import):** You successfully configured the foundation addons to be installed in a different namespace on the Hub, but then imported the MCE cluster using the ACM console (UI) without adding the required annotation.

**Resolution:**

In both cases, the resolution is to detach the failed attempt and re-import correctly using the CLI.

1.  **Detach the Cluster:**
    *   Remove the failed import attempt. You can do this from the ACM console by detaching the cluster, or by deleting the `ManagedCluster` resource via CLI.
    *   Ensure the cluster is fully detached before proceeding.

2.  **Re-import with Configuration:**
    *   Ensure your ACM Hub is configured properly (check the `configureMceImport: "true"` setting).
    *   Re-import the cluster using the `ManagedCluster` YAML with the special annotation as documented in [Discovering and Managing Hosted Clusters](../management/discovering_hostedclusters.md):
        ```yaml
        metadata:
          annotations:
            agent.open-cluster-management.io/klusterlet-config: mce-import-klusterlet-config
        ```

## Recovering from Misconfiguration

### Addon Conflict causing Unknown Cluster Status

**Symptom:**

*   The MCE spoke cluster status is reported as **Unknown** in the ACM UI/API.
*   Addon agents lose connection with either ACM or MCE repeatedly.
*   Race conditions, operator restarts, or duplicate resources are observed on the MCE spoke cluster.
*   Both ACM and MCE appear to be attempting to install/manage the same addons/operators in the same namespace.

**Cause:**

This issue occurs when an MCE cluster is imported into an ACM hub without first configuring the ACM foundation addons (such as work-manager) to run in a non-default namespace.

By default, both ACM and the MCE's local control plane attempt to install addon agents in the same namespace (usually `open-cluster-management-agent-addon`). This results in two sets of the same addon agents conflicting with each other, leading to instability and connectivity loss.

**Verification:**

Check the namespace of the addon agents in the ACM hub. If they are not configured to run in the discovery namespace (e.g., `open-cluster-management-agent-addon-discovery`), a conflict is likely occurring.

**Resolution:**

To recover from this state, you must completely clean up the current configuration and re-import the cluster with the correct settings.

1.  **Detach Hosted Clusters:**
    In the ACM hub cluster, detach all discovered hosted clusters that are associated with the affected MCE clusters.

2.  **Disable HyperShift Addon:**
    Disable the HyperShift addon for the imported MCE clusters to stop the discovery process.
    ```bash
    clusteradm addon disable --names hypershift-addon --clusters <MCE_CLUSTER_NAME>
    ```

3.  **Detach MCE Clusters:**
    Detach the MCE clusters from the ACM hub. Wait for the detachment to complete and for the conflicting resources to be removed from the MCE cluster.

4.  **Configure ACM Hub Correctly:**
    Follow the instructions in [Discovering and Managing Hosted Clusters](../management/discovering_hostedclusters.md) to configure the ACM hub. Specifically, ensure the `configureMceImport: "true"` variable is set so addons are installed in the correct non-default namespace.

5.  **Re-import MCE Clusters:**
    Once the Hub is correctly configured, re-import the MCE clusters using the `ManagedCluster` CR with the required annotation to point to the correct klusterlet configuration.
