# Managing HostedClusters from the Hub with `hcp from-hub`

`hcp from-hub` lets you create, edit, get, and delete HostedClusters on a hosting
ManagedCluster **from the hub**, without needing direct access to the hosting
cluster's kubeconfig.

All requests flow through the `hypershift-addon-operator` HCP proxy
(`hcp.ocm.io/v1alpha1` extension API), which forwards them to the hosting
cluster via cluster-proxy.

```text
hcp CLI → hub kube-apiserver → HCP proxy → cluster-proxy → hosting cluster
```

---

## Prerequisites


| Requirement           | Notes                                                    |
| --------------------- | -------------------------------------------------------- |
| ACM / MCE hub cluster | The `hypershift-addon-operator` must be running          |
| `cluster-proxy` addon | Enabled on the hosting ManagedCluster                    |
| Hub kubeconfig        | `$KUBECONFIG`, `~/.kube/config`, or `--hub-kubeconfig`   |
| RBAC                  | `managedcluster:admin` permission on the hosting cluster |


---

## HCP proxy API (`hcp.ocm.io/v1alpha1`)

The hub manager serves this extension API on port `9443` (Service port `443`,
APIService `v1alpha1.hcp.ocm.io`, provisioned by backplane-operator). Every
resource request requires the query parameter:

| Query parameter   | Required | Description                                      |
| ----------------- | -------- | ------------------------------------------------ |
| `hostingCluster`  | yes      | Name of the target hosting `ManagedCluster`      |

Base path:

```text
/apis/hcp.ocm.io/v1alpha1
```

### Endpoints

| Method | Path | Handler | Description |
| ------ | ---- | ------- | ----------- |
| `GET` | `/healthz`, `/readyz` | health | Liveness / readiness probes |
| `GET` | `/apis/hcp.ocm.io` | discovery | APIGroup document |
| `GET` | `/apis/hcp.ocm.io/v1alpha1` | discovery | APIResourceList (`hostedclusters`, `hostedclusters/resources`, `hostedclusters/finalizers`, `hostedclusters/validate`) |
| `POST` | `/namespaces/{ns}/hostedclusters?hostingCluster={cluster}` | create | Validate → Create Namespace → Secrets → ExtraObjects → HostedCluster → NodePool(s) — GET list is not supported |
| `GET` | `/namespaces/{ns}/hostedclusters/{name}?hostingCluster={cluster}` | get | Return full `ResourceBundle` |
| `GET` | `/namespaces/{ns}/hostedclusters/{name}/resources?hostingCluster={cluster}` | get | Same as GET above (explicit `/resources` alias) |
| `PUT` | `/namespaces/{ns}/hostedclusters/{name}?hostingCluster={cluster}` | put | Full-replace HostedCluster + NodePools from a `ResourceBundle` |
| `PUT` | `/namespaces/{ns}/hostedclusters/{name}/resources?hostingCluster={cluster}` | put | Same as PUT above |
| `DELETE` | `/namespaces/{ns}/hostedclusters/{name}?hostingCluster={cluster}` | delete | Delete matching NodePools, then the HostedCluster |
| `PATCH` | `/namespaces/{ns}/hostedclusters/{name}/finalizers?hostingCluster={cluster}` | patch | Add or remove the CLI destroy finalizer (`openshift.io/destroy-cluster`) on the hosting `HostedCluster` |
| `GET` | `/namespaces/{ns}/hostedclusters/{name}/validate?hostingCluster={cluster}&arch=...&releaseImage=...` | validate | Dedicated pre-create validation (duplicate name + NodePool arch) — read-only, no body, no resources are applied |

`Content-Type` for create/put bodies: `application/json`. Finalizers PATCH uses `application/json` with a `FinalizersRequest` body.

### Request / response types

#### `CreateRequest` (POST body)

Mirrors `hcp create cluster --render` output:

```json
{
  "hostedCluster": { "...": "HostedCluster object" },
  "nodePools": [ { "...": "NodePool object" } ],
  "secrets": [ { "...": "Secret object" } ],
  "extraObjects": [ { "...": "Role, ConfigMap, or other non-secret object" } ]
}
```

| Field | Required | Notes |
| ----- | -------- | ----- |
| `hostedCluster` | yes | Full HostedCluster; `spec.pullSecret.name` / `spec.sshKey.name` must match Secrets in the request |
| `nodePools` | no | One or more NodePools (`--render` may emit several) |
| `secrets` | no | Pull secret, SSH key, cloud credential / STS secrets |
| `extraObjects` | no | Non-secret objects from `--render` (Agent `capi-provider-role` Role, `--additional-trust-bundle` ConfigMap, …). Applied in the HostedCluster namespace after Secrets and before the HostedCluster. Create failures abort the request, roll back any extra objects created in the same request, and treat 409 AlreadyExists as already present. |

`handleCreate` always runs the same pre-create checks as the dedicated `/validate`
endpoint below before applying any resource — this is a safety net so a create
request can never bypass validation, even if the caller skipped calling
`/validate` first. `handleCreate`'s internal check does have the full body
(`hostedCluster`/`nodePools`/`secrets`) available, so unlike the standalone
`/validate` endpoint it *can* fall back to a registry manifest lookup when a
release image's multi-arch status isn't obvious from its name.

Create order on the spoke: `Namespace` (idempotent) → `Secrets` (create-or-update) → `ExtraObjects` → `HostedCluster` → `NodePool(s)`.

**Response:** `201 Created` with a `ResourceBundle` (Namespace + HostedCluster + NodePools + ExtraObjects). Secrets are never returned.

#### `GET .../hostedclusters/{name}/validate` (dedicated pre-create validation)

A standalone, read-only endpoint that runs the hosting-cluster pre-create
checks **without applying any resources and without a body**, so
`hcp from-hub create` can validate before it even starts rendering/POSTing
infrastructure (or a caller can dry-run a request on its own). Everything the
checks need is already in the path/query string:

```text
GET .../hostedclusters/{name}/validate?hostingCluster={cluster}&arch=amd64&arch=arm64&releaseImage={image}
```

| Param | Required | Notes |
| ----- | -------- | ----- |
| `hostingCluster` | yes | Same as every other endpoint |
| `arch` | no, repeatable | One NodePool CPU arch per value, e.g. `?arch=amd64&arch=arm64`. Omit to skip the architecture check (e.g. when only checking for a name collision). |
| `releaseImage` | no | Used only to recognize a multi-arch release by naming convention (contains `multi`) and skip the arch check. Unlike `handleCreate`'s internal check, there's no pull secret here, so a registry manifest lookup isn't attempted — an inconclusive image is *not* treated as multi-arch. |

Checks, in order:

1. **Duplicate name** — GETs the HostedCluster by namespace/name (from the
   path) on the hosting cluster; if it already exists, fails with
   `409 Conflict`.
2. **Node architecture** — if any `arch` values were given, reads the hosting
   cluster's CPU architecture (via `/version`) and compares it against each
   one. Skipped entirely when `releaseImage` names a multi-arch release. On
   mismatch, fails with `400 Bad Request` naming both architectures.

**Response:** `200 OK` with a success `Status` body on pass; a Kubernetes
`Status` error body (`409`/`400`/`502`/…) on failure — same error shape as
every other HCP proxy endpoint.

#### `ResourceBundle` (GET / PUT body and response)

```json
{
  "namespace": { "...": "Namespace object" },
  "hostedCluster": { "...": "HostedCluster object" },
  "nodePools": [ { "...": "NodePool object" } ],
  "extraObjects": [ { "...": "Role, ConfigMap, or other non-secret object" } ]
}
```

Secrets are never included — the HostedCluster only carries LocalObjectReferences (names). Extra objects are included on successful create responses.

PUT workflow (same idea as `kubectl edit`):

1. `GET .../hostedclusters/{name}/resources` → receive `ResourceBundle`
2. Edit fields
3. `PUT .../hostedclusters/{name}/resources` with the modified bundle

The proxy PUTs the HostedCluster and each NodePool present in the bundle (by `metadata.name`). Objects omitted from the bundle are left untouched. The response is a fresh GET of the live bundle.

#### `FinalizersRequest` (PATCH body)

Used by `hcp from-hub delete` to add or remove the CLI destroy finalizer on the hosting cluster (same as `hcp delete cluster`):

```json
{ "operation": "add" }
```

```json
{ "operation": "remove" }
```

| Field | Required | Notes |
| ----- | -------- | ----- |
| `operation` | yes | `add` appends `openshift.io/destroy-cluster` before deletion starts; `remove` drops that finalizer after AWS infra cleanup |

**Response:** `200 OK` with `{ "hostedCluster": { ... } }` reflecting the live object on the hosting cluster. Patch failures return a clear error (not a silent success).


### Common HTTP status codes

| Status | When |
| ------ | ---- |
| `400 Bad Request` | Missing `hostingCluster`, invalid JSON, missing `hostedCluster` on create, or NodePool architecture doesn't match the hosting cluster |
| `403 Forbidden` | Caller lacks `managedcluster:admin` on the hosting cluster |
| `404 Not Found` | Unknown path, or HostedCluster not found on get |
| `405 Method Not Allowed` | Unsupported verb on a path |
| `409 Conflict` | HostedCluster name already exists on the hosting cluster (checked by `/validate` and before create) |
| `503 Service Unavailable` | Hosting `ManagedCluster` is missing or not Available |
| `502 Bad Gateway` | Spoke / cluster-proxy request failed |
| `201 Created` | Successful create (body is `ResourceBundle`) |
| `200 OK` | Successful `/validate` (body is a success `Status`) |

---

## Shared flags

These flags are available on every `hcp from-hub` subcommand:


| Flag                | Default                          | Description                                                                                        |
| ------------------- | -------------------------------- | -------------------------------------------------------------------------------------------------- |
| `--hub-kubeconfig`  | `$KUBECONFIG` / `~/.kube/config` | Path to the hub cluster kubeconfig                                                                 |
| `--hosting-cluster` | *(required)*                     | Name of the hosting ManagedCluster (`hostingCluster` query param)                                  |
| `--namespace`       | `clusters`                       | Namespace for HostedCluster resources                                                              |
| `--context`         | *(current context)*              | Kubeconfig context to use                                                                          |
| `--proxy-url`       | *(empty)*                        | Connect directly to the HCP proxy for local testing. Skips hub auth and disables TLS verification. |


---

## Create

`hcp from-hub create` renders resources with the standard `hcp create cluster`
logic and applies them to the hosting cluster through the HCP proxy
(`POST .../hostedclusters` with a `CreateRequest`).

### Platform subcommands

```
hcp from-hub create <platform> [flags]
```

Supported platforms: `aws`, `azure`, `agent`, `kubevirt`, `openstack`

Each platform subcommand accepts the **same flags** as the corresponding
`hcp create cluster <platform>` command.

### How it works internally

1. Runs `hcp create cluster <platform>` in render mode (`--render --render-sensitive`) to produce YAML.
2. Parses the YAML to extract `HostedCluster`, `NodePool(s)`, `Secret`, and remaining documents (`Role`, `ConfigMap`, …).
3. Stamps client-side labels (see [Resource labels](#resource-labels)).
4. POSTs a `CreateRequest` to the HCP proxy's create endpoint, which validates the request against the hosting cluster (duplicate HostedCluster name, NodePool CPU architecture — the same checks exposed standalone at `GET .../hostedclusters/{name}/validate`) and, if validation passes, creates the resources in dependency order:
   `Namespace → Secrets → ExtraObjects → HostedCluster → NodePool(s)`

### Examples

**AWS**

```bash
hcp from-hub create aws \
  --hosting-cluster local-cluster \
  --name my-cluster \
  --release-image quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64 \
  --pull-secret ./pull-secret.json \
  --base-domain example.com \
  --aws-creds ~/.aws/credentials \
  --region us-east-1 \
  --generate-ssh
```

**Azure**

```bash
hcp from-hub create azure \
  --hosting-cluster local-cluster \
  --name my-cluster \
  --release-image quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64 \
  --pull-secret ./pull-secret.json \
  --azure-creds ./azure-creds.json \
  --location eastus \
  --base-domain example.com
```

**Agent**

```bash
hcp from-hub create agent \
  --hosting-cluster local-cluster \
  --name my-cluster \
  --release-image quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64 \
  --pull-secret ./pull-secret.json \
  --agent-namespace hardware-provisioning \
  --base-domain example.com
```

---

## Get

The proxy exposes get even if your `hcp` build does not yet wrap every verb.
You can call it with `kubectl` / `curl` against the hub APIService (or `--proxy-url` in local dev):

```bash
# Get full ResourceBundle (HostedCluster + NodePools + Namespace)
kubectl get --raw \
  '/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters/my-cluster/resources?hostingCluster=local-cluster'
```

---

## Edit

`hcp from-hub edit` works like `kubectl edit`: it fetches the live
`ResourceBundle` (`GET .../resources`), opens it in your editor, and applies a
`PUT .../resources` when you save.

```bash
hcp from-hub edit <name> --hosting-cluster <cluster>
```

### Editor selection

The editor is resolved in this order:

1. `$VISUAL`
2. `$EDITOR`
3. `vi` (Linux / macOS) or `notepad` (Windows)

Editors with arguments are supported (e.g. `VISUAL="code --wait"`).

### Edit loop behaviour


| Situation            | What happens                                                        |
| -------------------- | ------------------------------------------------------------------- |
| File saved unchanged | Exits: `Edit cancelled, no changes made.`                           |
| Invalid YAML saved   | Error is shown; editor re-opens with your edits                     |
| Server rejects PUT   | Error is prepended in a comment; editor re-opens                    |
| Valid change saved   | Bundle applied; prints `hostedcluster/<name> edited`                |


### Example

```bash
# Uses $VISUAL or $EDITOR; falls back to vi
hcp from-hub edit my-cluster --hosting-cluster local-cluster

# Explicit editor
EDITOR=nano hcp from-hub edit my-cluster --hosting-cluster local-cluster
```

---

## Delete

```bash
hcp from-hub delete <name> --hosting-cluster <cluster>
```

Sends a `DELETE` to the HCP proxy, which deletes NodePools whose
`spec.clusterName` matches, then deletes the HostedCluster on the hosting
cluster.

### Example

```bash
hcp from-hub delete my-cluster --hosting-cluster local-cluster
```

---

## Resource labels

Every resource created through `hcp from-hub create` / `POST` carries these labels on
the hosting cluster:


| Label                      | Value          | Set by             |
| -------------------------- | -------------- | ------------------ |
| `hcp.ocm.io/created-via`   | `hcp-from-hub` | HCP proxy (server) |
| `hcp.ocm.io/created-by`    | `from-hub-cli` | `hcp` CLI (client) |
| `hcp.ocm.io/hostedcluster` | `<name>`       | both               |


This lets you find all resources belonging to a cluster:

```bash
kubectl get secrets,hostedclusters,nodepools,roles,configmaps \
  -l hcp.ocm.io/hostedcluster=my-cluster -A
```

---

## Authentication and authorization

### Production (hub cluster with ACM/MCE)

Your hub kubeconfig credentials are used to authenticate against the hub
kube-apiserver. The kube-apiserver injects your identity as
`X-Remote-User` / `X-Remote-Group` headers before forwarding to the HCP proxy.

The proxy:

1. Checks that you hold `managedcluster:admin` on the target hosting cluster
   via the `clusterview.open-cluster-management.io` API.
2. Impersonates your identity (`Impersonate-User`, `Impersonate-Group`) toward
   cluster-proxy so the hosting cluster enforces its own RBAC for your user.

### Local development (kind / non-ACM)

When `clusterview.open-cluster-management.io` is not installed (e.g. kind),
the permission check is skipped non-fatally and any authenticated user can call
the proxy.

---

## Service URL resolution

At startup the HCP proxy resolves in-cluster dependency URLs:

| Dependency | Env overrides | Namespace |
| ---------- | ------------- | --------- |
| cluster-proxy | `CLUSTER_PROXY_URL` | Operator pod NS (`POD_NAMESPACE`) — Route, else Service `cluster-proxy-addon-user` |

---

## Local development

Use `--proxy-url` to bypass the hub kube-apiserver and talk directly to the
HCP proxy. This is useful when the proxy is exposed via `kubectl port-forward`
or run as a local binary.

```bash
# 1. Port-forward the proxy from a kind cluster
kubectl port-forward -n multicluster-engine \
  svc/hypershift-addon-hcp-proxy 8443:443

# 2. Hit the API directly
curl -k "https://localhost:8443/apis/hcp.ocm.io/v1alpha1" | jq .

curl -k \
  "https://localhost:8443/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters?hostingCluster=local-cluster"

curl -k \
  "https://localhost:8443/apis/hcp.ocm.io/v1alpha1/namespaces/clusters/hostedclusters/my-cluster/resources?hostingCluster=local-cluster"

# 3. Or use the CLI against the same proxy
hcp from-hub create agent \
  --proxy-url https://localhost:8443 \
  --hosting-cluster local-cluster \
  --name my-cluster \
  --pull-secret ./pull-secret.json \
  --agent-namespace hardware-provisioning

hcp from-hub edit my-cluster \
  --proxy-url https://localhost:8443 \
  --hosting-cluster local-cluster

hcp from-hub delete my-cluster \
  --proxy-url https://localhost:8443 \
  --hosting-cluster local-cluster
```

!!! note
    `--proxy-url` skips hub kube-apiserver authentication and disables TLS
    verification (the proxy uses a self-signed certificate in local
    environments). Do not use this flag in production.
