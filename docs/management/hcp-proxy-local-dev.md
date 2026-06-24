# HCP Proxy — local development & testing

The HCP proxy exposes a Kubernetes extension API (`hcp.ocm.io/v1alpha1`) that lets hub-side tooling
manage `HostedCluster` and `NodePool` resources on spoke clusters without direct spoke access.

For the end-user CLI workflow, see [Managing HostedClusters from the Hub](./from-hub-cli.md).

## Prerequisites

| Tool | Purpose |
|------|---------|
| `oc` / `kubectl` logged into the hub | Kubeconfig at `~/.kube/config` |
| VS Code with the Go extension | Debugger uses `launch.json` |

## 1. Start the hub manager locally

Use the **Hub Manager** launch configuration in `.vscode/launch.json`.
The manager starts its own secure-serving endpoint on `:9444` and the HCP proxy on `:9443`.

```text
# In VS Code: Run → Start Debugging → "Hub Manager"
# You should see this line in the Debug Console:
# starting HCP proxy server {"addr": ":9443"}
```

## 2. Port-forward the cluster-proxy service

The proxy routes spoke traffic through cluster-proxy. In a dedicated terminal (keep it running):

```bash
kubectl port-forward -n multicluster-engine svc/cluster-proxy-addon-user 9092:9092
```

The `launch.json` already sets `CLUSTER_PROXY_URL=https://localhost:9092` and
`CLUSTER_PROXY_INSECURE=true` so the proxy reaches the forwarded service automatically.

## 3. Test the API

All requests require the identity headers that the kube-apiserver normally injects on an
aggregated API call. Set them manually in Postman or curl.

**GET — list all HostedClusters in a namespace**

```bash
curl -sk \
  -H "X-Remote-User: kube:admin" \
  -H "X-Remote-Group: system:cluster-admins" \
  "https://localhost:9443/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters?hostingCluster=local-cluster"
```

**GET — single HostedCluster**

```bash
curl -sk \
  -H "X-Remote-User: kube:admin" \
  -H "X-Remote-Group: system:cluster-admins" \
  "https://localhost:9443/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters/my-cluster?hostingCluster=local-cluster"
```

**GET — full resource bundle (Namespace + HostedCluster + NodePools)**

```bash
curl -sk \
  -H "X-Remote-User: kube:admin" \
  -H "X-Remote-Group: system:cluster-admins" \
  "https://localhost:9443/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters/my-cluster/resources?hostingCluster=local-cluster"
```

**POST — create (mirrors `hcp create cluster --render` output)**

```bash
curl -sk -X POST \
  -H "X-Remote-User: kube:admin" \
  -H "X-Remote-Group: system:cluster-admins" \
  -H "Content-Type: application/json" \
  "https://localhost:9443/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters?hostingCluster=local-cluster" \
  -d '{
    "hostedCluster": {
      "apiVersion": "hypershift.openshift.io/v1beta1",
      "kind": "HostedCluster",
      "metadata": { "name": "my-cluster", "namespace": "clusters" },
      "spec": {
        "release": { "image": "quay.io/openshift-release-dev/ocp-release:4.16.0-x86_64" },
        "pullSecret": { "name": "my-cluster-pull-secret" },
        "sshKey":     { "name": "my-cluster-ssh-key" },
        "platform": { "type": "None" },
        "infraID": "my-cluster"
      }
    },
    "nodePools": [{
      "apiVersion": "hypershift.openshift.io/v1beta1",
      "kind": "NodePool",
      "metadata": { "name": "my-cluster-workers", "namespace": "clusters" },
      "spec": { "clusterName": "my-cluster", "replicas": 2, "platform": { "type": "None" } }
    }],
    "secrets": [
      { "apiVersion": "v1", "kind": "Secret",
        "metadata": { "name": "my-cluster-pull-secret" },
        "data": { ".dockerconfigjson": "<base64-encoded-pull-secret>" } },
      { "apiVersion": "v1", "kind": "Secret",
        "metadata": { "name": "my-cluster-ssh-key" },
        "data": { "id_rsa.pub": "<base64-encoded-public-key>" } }
    ]
  }'
```

**PUT — full resource replace (kubectl-edit semantics)**

```bash
# 1. Fetch the current bundle
curl -sk \
  -H "X-Remote-User: kube:admin" \
  -H "X-Remote-Group: system:cluster-admins" \
  "https://localhost:9443/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters/my-cluster/resources?hostingCluster=local-cluster" \
  > bundle.json

# 2. Edit bundle.json, then apply it
curl -sk -X PUT \
  -H "X-Remote-User: kube:admin" \
  -H "X-Remote-Group: system:cluster-admins" \
  -H "Content-Type: application/json" \
  "https://localhost:9443/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters/my-cluster/resources?hostingCluster=local-cluster" \
  -d @bundle.json
```

## 4. Identity header reference

| Header | Example value | Notes |
|--------|--------------|-------|
| `X-Remote-User` | `kube:admin` | Must match a user with `managedcluster:admin` binding for the `hostingCluster` |
| `X-Remote-Group` | `system:cluster-admins` | One or more groups; used for spoke impersonation |

The proxy enforces two permission gates:

1. **Hub gate** — `GET clusterview.open-cluster-management.io/v1alpha1/userpermissions/managedcluster:admin`
   under the caller's identity; request is denied if the target spoke is not in the bindings.
2. **Spoke gate** — all requests are forwarded with `Impersonate-User`/`Impersonate-Group` headers
   so the spoke cluster's own RBAC also applies.
