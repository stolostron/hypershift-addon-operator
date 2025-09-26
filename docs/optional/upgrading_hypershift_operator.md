# Upgrading the hypershift operator in one remote hosting cluster

## Overriding the hypershift operator image references

Without upgrading MCE or ACM, you can create configmap called `hypershift-override-images` in the managed (hosting) cluster namespace on the MCE or ACM hub cluster to upgrade the hypershift operator that was installed by the hypershift `ManagedClusterAddon` on that specific hosting cluster. The hypershift addon agent watches for changes in `hypershift-override-images` configmap and determines whether to upgrade the hypershift operator. This action should not have impact on other running hosted contol planes or the hypershift operator installed on other hosting clusters.

This is a sample configmap YAML. In `data`, you do not have to specify all three images. Specify images you want to override.

```YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-override-images
  namespace: local-cluster
data:
  cluster-api-provider-agent: quay.io:443/acm-d/cluster-api-provider-agent-rhel8@sha256:b02c207a1fc77da4d5e33b5cadf5f79da445a6656f26004b186a7cadbf19a74d
  cluster-api-provider-kubevirt: quay.io:443/acm-d/cluster-api-provider-kubevirt-rhel8@sha256:b76fc28b739b24a3b367000c47b973220252f5e8cd01a0243e54ba9aab79d298
  hypershift-operator: quay.io:443/acm-d/hypershift-rhel8-operator@sha256:eedb58e7b9c4d9e49c6c53d1b5b97dfddcdffe839bbffd4fb950760715d24244
```

# Upgrading the hypershift operator in all remote hosting clusters

## Overriding the hypershift operator image references

Without upgrading ACM, you can create a config map based on an image override JSON file on the ACM hub cluster to upgrade the hypershift operator that was installed by the hypershift `ManagedClusterAddon` on all hosting clusters.

1. Create an image override JSON file in the following format. The JSON file can contain a subset of these or all depending on which image you want to override. `image-remote` is the image repository, `image-name` is the image name in the image repository and `image-tag` is the image tag to be used. **NOTE:** Do not update the `image-key` values. Only these image keys are known by the ACM installer.

```
[
  {
    "image-name": "cluster-api-provider-agent",
    "image-tag": "sha256:c0725b90e19b151340a9abe821b9431d8ab903ad8f3ae93edb0550b0f2486756",
    "image-remote": "quay.io/stolostron",
    "image-key": "cluster_api_provider_agent"
  },
  {
    "image-name": "cluster-api-provider-kubevirt",
    "image-tag": "sha256:c0725b90e19b151340a9abe821b9431d8ab903ad8f3ae93edb0550b0f2486756",
    "image-remote": "quay.io/stolostron",
    "image-key": "cluster_api_provider_kubevirt"
  },
  {
    "image-name": "hypershift-operator",
    "image-tag": "sha256:c0725b90e19b151340a9abe821b9431d8ab903ad8f3ae93edb0550b0f2486756",
    "image-remote": "quay.io/stolostron",
    "image-key": "hypershift_operator"
  }
]
```

2. Create a configmap based on the JSON file. The configmap must be created in `multicluster-engine` namespace.

```
  kubectl create configmap <my-config> --from-file=docs/examples/image-override.json -n multicluster-engine
```

3. For this image override configmap to be effective, annotation MCE with this configmap.

```
  kubectl annotate mce $(kubectl get mce) --overwrite imageOverridesCM=<my-config>
```