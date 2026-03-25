---
name: add-spoke-controller
description: >-
  Guides creation of a new controller in pkg/agent/ for the spoke-side
  hypershift-addon agent. Use when adding a new reconciler, watcher, or
  controller to the spoke agent, or when asking how to add agent-side
  functionality.
---

# Add Spoke Controller

## Overview

Spoke controllers run inside the `hypershift-addon-agent` binary on managed clusters. They are registered in `pkg/agent/agent.go` inside `runControllerManager()` and reconcile resources on the spoke cluster, optionally interacting with the hub via `hubClient`.

## Checklist

```
- [ ] Step 1: Define the controller struct
- [ ] Step 2: Implement Reconcile and SetupWithManager
- [ ] Step 3: Add predicates for event filtering
- [ ] Step 4: Register in runControllerManager
- [ ] Step 5: Register new types in the scheme (if needed)
- [ ] Step 6: Add CRDs for envtest (if needed)
- [ ] Step 7: Add a controller name constant
- [ ] Step 8: Write tests
- [ ] Step 9: Run make test
```

### Step 1: Define the controller struct

Create a new file `pkg/agent/<controller_name>_controller.go`. Follow the existing pattern:

```go
package agent

import (
    "context"
    "fmt"

    "github.com/go-logr/logr"
    "github.com/stolostron/hypershift-addon-operator/pkg/util"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/controller"
)

type MyNewController struct {
    hubClient   client.Client
    spokeClient client.Client
    clusterName string
    log         logr.Logger
}
```

Field conventions:
- `hubClient` — uncached client to the hub cluster (reads secrets, creates resources on hub)
- `spokeClient` — uncached client to the spoke/managed cluster
- `clusterName` — the managed cluster name (passed from `AgentOptions.SpokeClusterName`)
- `localClusterName` — name of the self-managed local-cluster (if needed for local-cluster skip logic)
- `log` — logr.Logger instance

### Step 2: Implement Reconcile and SetupWithManager

```go
func (c *MyNewController) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        Named(util.MyNewControllerName).           // constant from pkg/util/constant.go
        For(&someapi.SomeResource{}).               // primary watched resource
        WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
        WithEventFilter(myNewPredicateFunctions).
        Complete(c)
}

func (c *MyNewController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    c.log.Info(fmt.Sprintf("reconciling %s", req.NamespacedName))
    defer c.log.Info(fmt.Sprintf("done reconciling %s", req.NamespacedName))

    // Reconciliation logic here

    return ctrl.Result{}, nil
}
```

Conventions observed across existing controllers:
- `MaxConcurrentReconciles: 1` for most controllers (except `agentController` which uses 10)
- Always use `Named()` with a constant from `pkg/util/constant.go`
- Log entry/exit of reconcile
- Return `ctrl.Result{Requeue: true, RequeueAfter: 1*time.Minute}` for transient errors

### Step 3: Add predicates for event filtering

Define predicates to reduce unnecessary reconciliations. Existing patterns:

```go
var myNewPredicateFunctions = predicate.Funcs{
    CreateFunc: func(e event.CreateEvent) bool {
        return true // or filter by type assertion / labels
    },
    UpdateFunc: func(e event.UpdateEvent) bool {
        // Compare old vs new to detect meaningful changes
        return false
    },
    DeleteFunc: func(e event.DeleteEvent) bool {
        return true
    },
    GenericFunc: func(e event.GenericEvent) bool {
        return false
    },
}
```

Existing examples:
- `hostedClusterEventFilters()` — filters HC updates by condition changes, annotation changes, kubeconfig changes
- `AddonStatusPredicateFunctions` — filters to only `hypershift-addon-agent` and HyperShift operator deployments
- `ExternalSecretPredicateFunctions` — filters to only hosted-mode Klusterlets
- `AutoImportPredicateFunctions` — only reacts to Create events

### Step 4: Register in runControllerManager

In `pkg/agent/agent.go`, inside `runControllerManager()`, add your controller after the existing controllers (around line 318-338):

```go
myNewController := &MyNewController{
    hubClient:   hubClient,
    spokeClient: spokeKubeClient,
    clusterName: aCtrl.clusterName,
    log:         o.Log.WithName("my-new-controller"),
}

if err = myNewController.SetupWithManager(mgr); err != nil {
    metrics.AddonAgentFailedToStartBool.Set(1)
    return fmt.Errorf("unable to create my new controller: %s, err: %w",
        util.MyNewControllerName, err)
}
```

Place it **after** the existing controller registrations and **before** the healthz/readyz checks.

### Step 5: Register new types in the scheme (if needed)

If the controller watches a type not already in the scheme, add it to the `init()` block at the top of `pkg/agent/agent.go`:

```go
func init() {
    // ... existing registrations ...
    utilruntime.Must(mynewapi.AddToScheme(scheme))
}
```

Currently registered types: clientgo, hyperv1beta1, addonv1alpha1, clusterv1alpha1, clusterv1, operatorapiv1, operatorv1, operatorsv1alpha1, klusterletAddonController, routev1, corev1, discoveryv1.

### Step 6: Add CRDs for envtest (if needed)

If the watched type requires a new CRD for envtest, add the CRD YAML to `hack/crds/`. Name it descriptively (e.g., `myresource.crd.yaml`).

Envtest suites in `pkg/agent/` load CRDs from `../../hack/crds` relative to the test file.

### Step 7: Add a controller name constant

In `pkg/util/constant.go`, add:

```go
MyNewControllerName = "my-new-controller"
```

Existing controller name constants: `AddonControllerName`, `AddonStatusControllerName`, `AutoImportControllerName`, `DiscoveryAgentName`, `ExternalSecretControllerName`.

### Step 8: Write tests

Create `pkg/agent/<controller_name>_controller_test.go`. Two test patterns are used:

**Pattern A — standard `testing` with fake client:**

```go
func TestMyNewController_Reconcile(t *testing.T) {
    scheme := runtime.NewScheme()
    // register types...

    fakeClient := fake.NewClientBuilder().
        WithScheme(scheme).
        WithObjects(/* seed objects */).
        Build()

    ctrl := &MyNewController{
        spokeClient: fakeClient,
        hubClient:   fakeHubClient,
        clusterName: "test-cluster",
        log:         zapr.NewLogger(zap.NewNop()),
    }

    result, err := ctrl.Reconcile(context.TODO(), ctrl.Request{
        NamespacedName: types.NamespacedName{Name: "test", Namespace: "test-ns"},
    })
    assert.NoError(t, err)
    // assertions...
}
```

**Pattern B — Ginkgo/Gomega with envtest (used by AddonStatus, Discovery, HcpKubeconfig controllers):**

These are registered in `pkg/agent/suite_test.go` which boots envtest with CRDs from `hack/crds/`.

### Step 9: Run make test

```bash
make test
```

This runs `go fmt`, `go vet`, sets up envtest, and runs all tests except e2e. Fix any failures before committing.

## Existing Controllers Reference

| Controller | File | Watches | Purpose |
|---|---|---|---|
| `agentController` | `agent.go` | `HostedCluster` | Secret mirroring, ext-managed-kubeconfig, placement scores, cluster claims, cleanup |
| `AddonStatusController` | `addon_status_controller.go` | `Deployment` | Reports addon health from operator/ext-DNS deployment status |
| `ExternalSecretController` | `external_secret_controller.go` | `Klusterlet` | Annotates HostedClusters when hosted-mode Klusterlets are created |
| `AutoImportController` | `auto_import_controller.go` | `HostedCluster` | Auto-creates ManagedCluster + KlusterletAddonConfig for new HCs |
| `DiscoveryAgent` | `discovery_agent.go` | `HostedCluster` | Creates/updates/deletes DiscoveredCluster on hub |
| `HcpKubeconfigChangeWatcher` | `hcp_kubeconfig_watcher.go` | Secrets | Reacts to HCP kubeconfig changes |
