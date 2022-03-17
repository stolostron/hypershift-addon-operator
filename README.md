# hypershift-addon-operator

hypershift-addon-operator can deploy a hypershift-operator to a managed cluster.

## Install the hypershift addon manager

```
oc apply -f example/addon-manager-deployment.yaml
```

You can check the hypershift addon manager status by
```
╰─$ oc get deployment -n multicluster-engine | grep hypershift-addon
hypershift-addon-manager              1/1     1            1           107m
```

## Enable a hypershift addon agent to a managed cluster

1. Check the target managed cluster is imported to the hub, we will use managed cluster <cluster1> as an example.
```
╰─$ oc get managedcluster cluster1
NAME            HUB ACCEPTED   MANAGED CLUSTER URLS          JOINED   AVAILABLE   AGE
cluster1        true                                         True     True        147m
```

2. Install a hypershift addon to the managed cluster <cluster1>
```
╰─$ oc apply -f - <<EOF
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: hypershift-addon
  namespace: cluster1
spec:
  installNamespace: open-cluster-management-agent-addon
EOF
```

3. Create a hypershift-operator-oidc-provider-s3-credentials for the hypershift addon in the <cluster1> namespace
```
oc create secret generic hypershift-operator-oidc-provider-s3-credentials --from-file=credentials=${HOME}/.aws/credentials --from-literal=bucket=<bucket-name> --from-literal=region=us-east-1 -n cluster1
```

4. Check the status of the hypershift-addon
```
╰─$ oc get managedclusteraddons -n cluster1 hypershift-addon
NAME               AVAILABLE   DEGRADED   PROGRESSING
hypershift-addon   True
```
