# Hypershift add-on Configuration for AKS

This document describes how to configure MCE or ACM in an AKS cluster for the hypershift addon. Before following this document, install MCE 2.7 or ACM 2.12 in an AKS cluster. Also refer to [this](https://github.com/openshift/hypershift/blob/main/docs/content/how-to/azure/create-azure-cluster_on_aks.md#prerequisites) for other pre-requisites.

## Disabling Hypershift Operator installation (optional)

When you install MCE or ACM, it enables the hypershift addon for `local-cluster` by default. When the hypershift addon agent starts up, it installs the hypershift operator automatically unless the hypershift operator management is disabled. If you do not want the hypershift addon agent to install and manage the hypershift operator, run the following command to disable it. This command also disables prometheus metrics which currently relies on OpenShift.

```
oc patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=merge -p '{"spec":{"customizedVariables":[{"name":"disableMetrics","value": "true"},{"name":"disableHOManagement","value": "true"},{"name":"aroHcp","value":"true"}]}}'
```

- `disableMetrics=true` disables the prometheus metrics service which depnds on OpenShift service monitor.
- `disableHOManagement=true` disables installing and managing the hypershift operator. With this setting, the hypershift operator needs to be installed manually. The hypershift addon agent will contantly fail until the hypershift operator and its CRDs are installed in the cluster.
- `aroHcp=true` is the indicator for the addon agent that the agent is run in non-OCP cluster.

## Configuring Hypershift Operator installation

If you want the hypershift addon to install and manage the hypershift operator for AKS with the external DNS, follow these steps.

Before performing these steps, you might notice that `hypershift-install-job` jobs in `open-cluster-management-agent-addon` namespace are failing or the `operator` and `external-dns` deployments are failing to start in `hypershift` namespace.

Apply some CRDs that are missing. This is temporary. These CRDs will be installed as part of the hypershift operator installation eventually.

```
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
oc apply -f https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml
```

Disable the prometheus metrics service and indicate that the addon is for an AKS cluster.

```
oc patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=merge -p '{"spec":{"customizedVariables":[{"name":"disableMetrics","value": "true"},{"name":"disableHOManagement","value": "false"},{"name":"aroHcp","value":"true"}]}}'
```

- `disableMetrics=true` disables the prometheus metrics service which depnds on OpenShift service monitor.
- `disableHOManagement=false` enables installing and managing the hypershift operator. With this setting, the hypershift operator is installed by the hypershift addon agent.
- `aroHcp=true` is the indicator for the addon agent that the agent is run in non-OCP cluster.

Create `ho-pull-secret` secret in `local-cluster` namespace. Replace `/Users/user/pull-secret.txt` with your own pull-secret which has access to `registry.redhat.io` registry. This pull secret is used by the hypershift operator installation to pull the external DNS image.

```
oc create secret generic ho-pull-secret -n local-cluster --from-file=.dockerconfigjson=/Users/user/pull-secret.txt --type=kubernetes.io/dockerconfigjson
```

The hypershift operator addon agent will copy this `ho-pull-secret` secret into `open-cluster-management-image-pull-credentials` secret in `hypershift` namespace and configure the `operator` and `external-dns` deployment to reference this image pull secret.


Follow [this](https://github.com/openshift/hypershift/blob/main/docs/content/how-to/azure/create-azure-cluster_on_aks.md#setup-externaldns) to configure external DNS.

Create `hypershift-operator-external-dns-credentials` secret in `local-cluster` namespace. `/Users/user/azure.json` and `${DNS_ZONE_NAME}` are from the previous step of configuring external DNS.

```
kubectl create secret generic hypershift-operator-external-dns-credentials -n local-cluster --from-file=credentials=/Users/user/azure.json --from-literal=provider=azure --from-literal=domain-filter=${DNS_ZONE_NAME}
```

This `hypershift-operator-external-dns-credentials` secret is used by the hypershift addon to install the hypershift operator. This secret gets copied to `hypershift-operator-external-dns-credentials` in `hypershift` namespace.

There are other AKS specific hypershift operator installation flags that need to be added and some default installation flags need to be removed. These are configured in `hypershift-operator-install-flags` configmap in `local-cluster` namespace. Refer to [this](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/hypershift_operator_configuration.md#customizing-the-hypershift-operator-installation-flags).

```
kind: ConfigMap
apiVersion: v1
metadata:
  name: hypershift-operator-install-flags
  namespace: local-cluster
data:
  installFlagsToAdd: "--enable-conversion-webhook=false --managed-service ARO-HCP"
  installFlagsToRemove: "--enable-uwm-telemetry-remote-write --platform-monitoring --enable-defaulting-webhook --enable-validating-webhook"
```

After making these changes, the `hypershift-install-job` jobs in `open-cluster-management-agent-addon` namespace should be completed successfully and the `operator` and `external-dns` are deployed successfully in `hypershift` namespace.

## Troubleshooting

If the hypershift operator installation continues to fail, disable the hypershift operator installation in the hypershift addon by running the following command.

```
oc patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=merge -p '{"spec":{"customizedVariables":[{"name":"disableMetrics","value": "true"},{"name":"disableHOManagement","value": "true"}]}}'
```

Uninstall the hypershift operator.

Review the hypershift operator installation configuration steps above.

Enable the hypershift operator installation in the addon to let the hypershift addon agent try to install the hypershift operator again.

```
oc patch addondeploymentconfig hypershift-addon-deploy-config -n multicluster-engine --type=merge -p '{"spec":{"customizedVariables":[{"name":"disableMetrics","value": "true"},{"name":"disableHOManagement","value": "false"}]}}'
```