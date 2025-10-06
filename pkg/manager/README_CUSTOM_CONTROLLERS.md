# Adding Custom Controllers to the Manager (Hub Side)

This guide explains how to add new controllers to the HyperShift Addon Manager running on the hub cluster.

## Architecture Overview

The manager now supports two types of controllers:

1. **Addon Framework Manager**: Handles OCM addon lifecycle (existing functionality)
2. **Custom Controller Manager**: Handles custom business logic (new functionality)

Both managers run concurrently in separate goroutines.

## Discovery Configuration Controller

See `discovery_config_controller.go` for a complete implementation that:

- Watches `AddonDeploymentConfig` resources for `hypershift-addon-deploy-config`
- Monitors `configureMceImport` custom variable changes
- Automatically creates/removes `addon-ns-config` AddOnDeploymentConfig
- Updates multiple ClusterManagementAddOns (work-manager, managed-serviceaccount, cluster-proxy)
- Skips all processing when ACM is not installed
- Creates/updates ConfigMaps with discovery configuration information
- Handles both enabled and disabled states
- Demonstrates proper error handling and logging

## Adding Your Own Controller

### Step 1: Create Your Controller

Create a new file `pkg/manager/your_controller.go`:

```go
package manager

import (
    "context"
    "github.com/go-logr/logr"
    "k8s.io/apimachinery/pkg/runtime"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

type YourController struct {
    client.Client
    Log               logr.Logger
    Scheme            *runtime.Scheme
    OperatorNamespace string
}

func (r *YourController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Your reconciliation logic here
    return ctrl.Result{}, nil
}

func (r *YourController) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&YourResource{}).
        Complete(r)
}
```

### Step 2: Register Your Controller

In `manager.go`, add your controller after the example controller:

```go
// Add your controller to the custom manager
yourController := &YourController{
    Client:            hubClient,
    Log:               log.WithName("your-controller"),
    Scheme:            genericScheme,
    OperatorNamespace: controllerContext.OperatorNamespace,
}

if err = yourController.SetupWithManager(customMgr); err != nil {
    log.Error(err, "failed to setup your controller")
    return err
}
```

### Step 3: Update RBAC (if needed)

If your controller needs additional permissions, update the RBAC manifests in `manifests/permission/`.

## Best Practices

1. **Resource Filtering**: Use predicates to filter events and reduce unnecessary reconciliations
2. **Error Handling**: Always handle errors gracefully and return appropriate results
3. **Logging**: Use structured logging with meaningful context
4. **Requeue Strategy**: Use appropriate requeue intervals for periodic reconciliation
5. **Resource Ownership**: Set owner references when creating resources
6. **Context Handling**: Always respect the context for cancellation

## Testing Your Controller

Create unit tests following the pattern in `manager_test.go`:

```go
func TestYourController_Reconcile(t *testing.T) {
    // Test implementation
}
```

## Monitoring and Metrics

Controllers automatically get basic metrics from controller-runtime. Add custom metrics as needed:

```go
import "github.com/prometheus/client_golang/prometheus"

var (
    yourMetric = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "your_controller_operations_total",
            Help: "Total number of operations performed by your controller",
        },
        []string{"operation", "result"},
    )
)
```

## Troubleshooting

- Check controller logs: Look for your controller name in the manager logs
- Verify RBAC: Ensure your controller has necessary permissions
- Resource conflicts: Make sure your controller doesn't conflict with existing ones
- Performance: Monitor reconciliation frequency and duration

