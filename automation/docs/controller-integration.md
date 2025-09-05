# MCE Discovery Controller Integration

The MCE Discovery Controller has been integrated into the existing `pkg/manager` structure and runs alongside the hypershift addon framework manager.

## How It Works

### Architecture

The integration uses a **dual-manager approach**:

1. **Addon Framework Manager**: Handles the existing hypershift addon functionality
2. **Controller-Runtime Manager**: Runs the MCE discovery controller

Both managers run concurrently in the same process:

```go
// Start the addon framework manager in a goroutine
go func() {
    err := mgr.Start(ctx)
    // ...
}()

// Start MCE discovery controller if enabled
if IsMCEDiscoveryEnabled() {
    if err := startMCEDiscoveryController(ctx, controllerContext.KubeConfig, log); err != nil {
        // Handle error but don't exit - this is optional functionality
    }
}
```

### Controller Functionality

The MCE Discovery Controller automatically:

1. **Watches ManagedCluster resources** for new MCE clusters
2. **Detects MCE clusters** using labels and naming patterns
3. **Sets up discovery configuration** including:
   - AddOnDeploymentConfig for proper namespace isolation
   - ManagedClusterAddOn for hypershift-addon
   - Marks clusters as discovery-ready
4. **Avoids duplicate work** by checking completion labels

### Configuration

The controller is configured through environment variables:

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `ENABLE_MCE_DISCOVERY` | `true` | Enable/disable the MCE discovery controller |
| `ADDON_NAMESPACE` | `open-cluster-management-agent-addon-discovery` | Namespace for addon installation |
| `ACM_NAMESPACE` | `multicluster-engine` | ACM/MCE namespace |
| `POLICY_NAMESPACE` | `open-cluster-management-global-set` | Policy namespace |

## Usage

### 1. Enable MCE Discovery (Default)

The controller runs by default when the manager starts:

```bash
# Controller will automatically start
./hypershift-addon-operator manager
```

### 2. Disable MCE Discovery

Set environment variable to disable:

```bash
export ENABLE_MCE_DISCOVERY=false
./hypershift-addon-operator manager
```

### 3. Label MCE Clusters

To ensure proper detection, label your MCE clusters:

```bash
oc label managedcluster mce-cluster-name cluster.open-cluster-management.io/mce-cluster=true
```

### 4. Monitor Controller Activity

Check logs for controller activity:

```bash
# Look for MCE discovery controller logs
oc logs -n multicluster-engine deployment/hypershift-addon-operator | grep "mce-discovery-controller"
```

## Integration Benefits

### Automatic Discovery Setup

- **No manual intervention**: MCE clusters are automatically configured for discovery
- **Consistent configuration**: Ensures all MCE clusters have the same discovery setup
- **Error handling**: Retries failed operations automatically

### Operational Advantages

- **Single deployment**: No need for separate controller deployment
- **Shared resources**: Uses existing RBAC, service account, and configuration
- **Optional functionality**: Can be disabled without affecting core functionality
- **Resource efficient**: Minimal additional overhead

### Complementary to Scripts

The controller works alongside the automation scripts:

- **Controller**: Handles ongoing cluster management and discovery setup
- **Scripts**: Handle initial ACM hub configuration and complex policy setup

## Example Workflow

1. **Deploy hypershift-addon-operator** with MCE discovery enabled
2. **Import MCE cluster** using standard ACM import process or automation scripts
3. **Label the cluster** (optional, for explicit identification):
   ```bash
   oc label managedcluster my-mce-cluster cluster.open-cluster-management.io/mce-cluster=true
   ```
4. **Controller automatically**:
   - Detects the MCE cluster
   - Creates AddOnDeploymentConfig if needed
   - Enables hypershift-addon with proper configuration
   - Marks cluster as discovery-ready
5. **Hypershift addon discovers hosted clusters** and creates DiscoveredCluster resources
6. **Auto-import policy** (if deployed via scripts) triggers import of discovered clusters

## Troubleshooting

### Controller Not Starting

Check if MCE discovery is enabled:
```bash
echo $ENABLE_MCE_DISCOVERY
```

Check manager logs:
```bash
oc logs -n multicluster-engine deployment/hypershift-addon-operator
```

### Clusters Not Being Detected

Verify cluster labels:
```bash
oc get managedcluster --show-labels
```

Check controller logs for detection logic:
```bash
oc logs -n multicluster-engine deployment/hypershift-addon-operator | grep "Not an MCE cluster"
```

### Permission Issues

Ensure the service account has proper RBAC permissions for:
- ManagedCluster resources
- ManagedClusterAddOn resources
- AddOnDeploymentConfig resources

## Future Enhancements

The controller can be extended to:

1. **Full policy management**: Create and manage auto-import policies
2. **Health monitoring**: Monitor discovery agent health
3. **Metrics collection**: Export discovery metrics
4. **Advanced filtering**: More sophisticated MCE cluster detection
5. **Configuration validation**: Validate MCE cluster readiness

## Migration from Standalone Controller

If you were using the standalone controller approach, migration is simple:

1. **Remove standalone deployment**: Delete the separate controller deployment
2. **Update hypershift-addon-operator**: Use version with integrated controller
3. **Set environment variables**: Configure as needed
4. **Verify operation**: Check that discovery continues to work

The integration maintains the same functionality while simplifying deployment and management.
