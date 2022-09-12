# Upgrading the hypershift operator on a remote hosting cluster

## Overriding the hypershift operator image references

Without upgrading MCE or ACM, you can create configmap called `hypershift-upgrade-images` in `open-cluster-management-agent-addon` namespace on a hosting (management) cluster to upgrade the hypershift operator that was installed by the hypershift `ManagedClusterAddon`. The hypershift addon agent watches this specific configmap and determines whether to upgrade the hypershift operator. This action should not have impact on other running hosted contol planes.

This is a sample configmap YAML.

```YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-upgrade-images
  namespace: open-cluster-management-agent-addon
spec:
  images:
  - name: apiserver-network-proxy
    ref: quay.io:443/acm-d/apiserver-network-proxy-rhel8@sha256:90af8dd96676f1b07d9420924628ffe91682971d377030fe752d1bae226c8ffe
  - name: aws-encryption-provider
    path: quay.io:443/acm-d/aws-encryption-provider-rhel8@sha256:b3256a9a917f0895bb0973a5ee690dc649b66b9c8e14da789e6fa352e2bece4c
  - name: cluster-api
    path: quay.io:443/acm-d/cluster-api-rhel8@sha256:b3edf4e95efc5dd749b938d85be63fc7b927f7c7b6d088fae3a4700f756f7c6f
  - name: cluster-api-provider-agent
    path: quay.io:443/acm-d/cluster-api-provider-agent-rhel8@sha256:b02c207a1fc77da4d5e33b5cadf5f79da445a6656f26004b186a7cadbf19a74d
  - name: cluster-api-provider-aws
    path: quay.io:443/acm-d/cluster-api-provider-aws-rhel8@sha256:065bf16f8a18a6de58ed522e4bbcdc2b744a9f89d73a39bdd36dcc297c493c39
  - name: cluster-api-provider-azure
    path: quay.io:443/acm-d/cluster-api-provider-azure-rhel8@sha256:9f9061f05c1a794b6ece36b481b107646bafe411457cfdc73bcc64c102c12ae4
  - name: cluster-api-provider-kubevirt
    path: quay.io:443/acm-d/cluster-api-provider-kubevirt-rhel8@sha256:b76fc28b739b24a3b367000c47b973220252f5e8cd01a0243e54ba9aab79d298
  - name: hypershift-operator
    path: quay.io:443/acm-d/hypershift-rhel8-operator@sha256:eedb58e7b9c4d9e49c6c53d1b5b97dfddcdffe839bbffd4fb950760715d24244
```

## Using manifestwork from the service cluster (MCE/ACM hub cluster) to upgrade the hypershift operator

You can put `hypershift-upgrade-images` configmap as a payload of a `manifestwork` in the hosting managed cluster's namespace on the service cluster (MCE hub cluster). This will create create configmap called `hypershift-upgrade-images` in `open-cluster-management-agent-addon` namespace on a hosting (management) cluster to trigger the hypershift operator upgrade on the hosting cluster.

This is a sample manifestwork YAML.

```YAML
apiVersion: work.open-cluster-management.io/v1
kind: ManifestWork
metadata:
  name: hypershift-operator-upgrade
  namespace: my-hosting-cluster
spec:
  workload:
    manifests:
    - apiVersion: v1
      kind: ConfigMap
      metadata:
        name: hypershift-upgrade-images
        namespace: open-cluster-management-agent-addon
      spec:
        images:
        - name: apiserver-network-proxy
          ref: quay.io:443/acm-d/apiserver-network-proxy-rhel8@sha256:90af8dd96676f1b07d9420924628ffe91682971d377030fe752d1bae226c8ffe
        - name: aws-encryption-provider
          path: quay.io:443/acm-d/aws-encryption-provider-rhel8@sha256:b3256a9a917f0895bb0973a5ee690dc649b66b9c8e14da789e6fa352e2bece4c
        - name: cluster-api
          path: quay.io:443/acm-d/cluster-api-rhel8@sha256:b3edf4e95efc5dd749b938d85be63fc7b927f7c7b6d088fae3a4700f756f7c6f
        - name: cluster-api-provider-agent
          path: quay.io:443/acm-d/cluster-api-provider-agent-rhel8@sha256:b02c207a1fc77da4d5e33b5cadf5f79da445a6656f26004b186a7cadbf19a74d
        - name: cluster-api-provider-aws
          path: quay.io:443/acm-d/cluster-api-provider-aws-rhel8@sha256:065bf16f8a18a6de58ed522e4bbcdc2b744a9f89d73a39bdd36dcc297c493c39
        - name: cluster-api-provider-azure
          path: quay.io:443/acm-d/cluster-api-provider-azure-rhel8@sha256:9f9061f05c1a794b6ece36b481b107646bafe411457cfdc73bcc64c102c12ae4
        - name: cluster-api-provider-kubevirt
          path: quay.io:443/acm-d/cluster-api-provider-kubevirt-rhel8@sha256:b76fc28b739b24a3b367000c47b973220252f5e8cd01a0243e54ba9aab79d298
        - name: hypershift-operator
          path: quay.io:443/acm-d/hypershift-rhel8-operator@sha256:eedb58e7b9c4d9e49c6c53d1b5b97dfddcdffe839bbffd4fb950760715d24244
```