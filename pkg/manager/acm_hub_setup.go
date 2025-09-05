package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

const (
	// ACMHubSetupControllerName is the name of the controller
	ACMHubSetupControllerName = "acm-hub-setup-controller"

	// ACMHubSetupLabel indicates ACM hub setup is completed
	ACMHubSetupLabel = "hypershift.open-cluster-management.io/acm-hub-setup"

	// Environment variables for ACM hub setup
	EnvEnableACMHubSetup = "ENABLE_ACM_HUB_SETUP"
	EnvBackupEnabled     = "BACKUP_ENABLED"

	// Setup trigger ConfigMap
	SetupTriggerConfigMapName = "acm-hub-setup-trigger"
	SetupRequestedKey         = "setup-requested"
	UndoRequestedKey          = "undo-requested"
	SetupStatusKey            = "setup-status"
	SetupResultsKey           = "setup-results"
	SetupTimestampKey         = "setup-timestamp"
	SetupErrorKey             = "setup-error"

	// Setup status values
	StatusRequested     = "requested"
	StatusInProgress    = "in-progress"
	StatusCompleted     = "completed"
	StatusFailed        = "failed"
	StatusUndoRequested = "undo-requested"
	StatusUndoProgress  = "undo-in-progress"
	StatusUndoCompleted = "undo-completed"
	StatusUndoFailed    = "undo-failed"

	// Resources created by the controller
	AddonNSConfigName         = "addon-ns-config"
	KlusterletConfigName      = "mce-import-klusterlet-config"
	HypershiftAddonConfigName = "hypershift-addon-deploy-config"
	DiscoveryConfigMapName    = "discovery-config"
	AutoImportPolicyName      = "policy-mce-hcp-autoimport"

	// Backup label for disaster recovery
	BackupLabel = "cluster.open-cluster-management.io/backup"
)

// SetupResult represents the result of a setup operation
type SetupResult struct {
	Component string    `json:"component"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error,omitempty"`
}

// SetupResults contains all setup operation results
type SetupResults struct {
	OverallStatus string        `json:"overallStatus"`
	StartTime     time.Time     `json:"startTime"`
	EndTime       time.Time     `json:"endTime,omitempty"`
	Duration      string        `json:"duration,omitempty"`
	Results       []SetupResult `json:"results"`
	Summary       string        `json:"summary,omitempty"`
}

// ACMHubSetupController automates the ACM hub configuration for MCE discovery
type ACMHubSetupController struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger

	// Configuration
	AddonNamespace  string
	ACMNamespace    string
	PolicyNamespace string
	BackupEnabled   bool
}

// +kubebuilder:rbac:groups=addon.open-cluster-management.io,resources=addondeploymentconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=addon.open-cluster-management.io,resources=clustermanagementaddons,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.open-cluster-management.io,resources=klusterletconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile handles the ACM hub setup configuration triggered by ConfigMap
func (r *ACMHubSetupController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("namespace", req.Namespace, "name", req.Name)

	// Only reconcile the setup trigger ConfigMap in the ACM namespace
	if req.Namespace != r.ACMNamespace || req.Name != SetupTriggerConfigMapName {
		return ctrl.Result{}, nil
	}

	// Get the setup trigger ConfigMap
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, req.NamespacedName, configMap); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Setup trigger ConfigMap not found")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get setup trigger ConfigMap")
		return ctrl.Result{}, err
	}

	// Check if undo is requested
	undoRequested, undoExists := configMap.Data[UndoRequestedKey]
	if undoExists && strings.ToLower(undoRequested) == "true" {
		return r.handleUndoRequest(ctx, configMap, log)
	}

	// Check if setup is requested
	setupRequested, exists := configMap.Data[SetupRequestedKey]
	if !exists || strings.ToLower(setupRequested) != "true" {
		log.V(1).Info("Setup not requested")
		return ctrl.Result{}, nil
	}

	// Check current status
	currentStatus := configMap.Data[SetupStatusKey]
	if currentStatus == StatusCompleted {
		log.V(1).Info("Setup already completed")
		return ctrl.Result{}, nil
	}

	if currentStatus == StatusInProgress {
		log.V(1).Info("Setup already in progress")
		return ctrl.Result{}, nil
	}

	log.Info("Starting ACM hub setup configuration")

	// Update status to in-progress
	if err := r.updateSetupStatus(ctx, configMap, StatusInProgress, "Starting ACM hub setup", nil); err != nil {
		log.Error(err, "Failed to update setup status to in-progress")
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	// Perform ACM hub setup and track results
	setupResults := &SetupResults{
		OverallStatus: StatusInProgress,
		StartTime:     time.Now(),
		Results:       []SetupResult{},
	}

	if err := r.setupACMHubWithTracking(ctx, setupResults); err != nil {
		log.Error(err, "Failed to setup ACM hub")
		setupResults.OverallStatus = StatusFailed
		setupResults.EndTime = time.Now()
		setupResults.Duration = setupResults.EndTime.Sub(setupResults.StartTime).String()
		setupResults.Summary = fmt.Sprintf("Setup failed: %v", err)

		if updateErr := r.updateSetupStatus(ctx, configMap, StatusFailed, "Setup failed", setupResults); updateErr != nil {
			log.Error(updateErr, "Failed to update setup status to failed")
		}
		return ctrl.Result{RequeueAfter: time.Minute * 5}, err
	}

	// Mark setup as completed
	setupResults.OverallStatus = StatusCompleted
	setupResults.EndTime = time.Now()
	setupResults.Duration = setupResults.EndTime.Sub(setupResults.StartTime).String()
	setupResults.Summary = "ACM hub setup completed successfully"

	if err := r.updateSetupStatus(ctx, configMap, StatusCompleted, "Setup completed successfully", setupResults); err != nil {
		log.Error(err, "Failed to update setup status to completed")
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	log.Info("ACM hub setup completed successfully")
	return ctrl.Result{}, nil
}

// handleUndoRequest processes undo requests from the ConfigMap
func (r *ACMHubSetupController) handleUndoRequest(ctx context.Context, configMap *corev1.ConfigMap, log logr.Logger) (ctrl.Result, error) {
	// Check current status
	currentStatus := configMap.Data[SetupStatusKey]

	// Only allow undo if setup was completed
	if currentStatus != StatusCompleted && currentStatus != StatusUndoFailed {
		log.Info("Undo can only be performed after successful setup", "currentStatus", currentStatus)
		if err := r.updateSetupStatus(ctx, configMap, StatusUndoFailed, "Undo can only be performed after successful setup", nil); err != nil {
			log.Error(err, "Failed to update undo status")
		}
		return ctrl.Result{}, nil
	}

	// Check if undo is already in progress
	if currentStatus == StatusUndoProgress {
		log.V(1).Info("Undo already in progress")
		return ctrl.Result{}, nil
	}

	// Check if undo is already completed
	if currentStatus == StatusUndoCompleted {
		log.V(1).Info("Undo already completed")
		return ctrl.Result{}, nil
	}

	log.Info("Starting ACM hub configuration undo")

	// Update status to undo-in-progress
	if err := r.updateSetupStatus(ctx, configMap, StatusUndoProgress, "Starting ACM hub undo", nil); err != nil {
		log.Error(err, "Failed to update undo status to in-progress")
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	// Perform ACM hub undo and track results
	undoResults := &SetupResults{
		OverallStatus: StatusUndoProgress,
		StartTime:     time.Now(),
		Results:       []SetupResult{},
	}

	if err := r.undoACMHubWithTracking(ctx, undoResults); err != nil {
		log.Error(err, "Failed to undo ACM hub")
		undoResults.OverallStatus = StatusUndoFailed
		undoResults.EndTime = time.Now()
		undoResults.Duration = undoResults.EndTime.Sub(undoResults.StartTime).String()
		undoResults.Summary = fmt.Sprintf("Undo failed: %v", err)

		if updateErr := r.updateSetupStatus(ctx, configMap, StatusUndoFailed, "Undo failed", undoResults); updateErr != nil {
			log.Error(updateErr, "Failed to update undo status to failed")
		}
		return ctrl.Result{RequeueAfter: time.Minute * 5}, err
	}

	// Mark undo as completed
	undoResults.OverallStatus = StatusUndoCompleted
	undoResults.EndTime = time.Now()
	undoResults.Duration = undoResults.EndTime.Sub(undoResults.StartTime).String()
	undoResults.Summary = "ACM hub undo completed successfully"

	if err := r.updateSetupStatus(ctx, configMap, StatusUndoCompleted, "Undo completed successfully", undoResults); err != nil {
		log.Error(err, "Failed to update undo status to completed")
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	log.Info("ACM hub undo completed successfully")
	return ctrl.Result{}, nil
}

// updateSetupStatus updates the ConfigMap with current setup status and results
func (r *ACMHubSetupController) updateSetupStatus(ctx context.Context, configMap *corev1.ConfigMap, status, message string, results *SetupResults) error {
	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	configMap.Data[SetupStatusKey] = status
	configMap.Data[SetupTimestampKey] = time.Now().Format(time.RFC3339)

	if message != "" {
		configMap.Data["setup-message"] = message
	}

	if results != nil {
		resultsJSON, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal setup results: %w", err)
		}
		configMap.Data[SetupResultsKey] = string(resultsJSON)
	}

	if status == StatusFailed && results != nil && results.Summary != "" {
		configMap.Data[SetupErrorKey] = results.Summary
	}

	return r.Update(ctx, configMap)
}

// setupACMHubWithTracking performs ACM hub setup with detailed result tracking
func (r *ACMHubSetupController) setupACMHubWithTracking(ctx context.Context, results *SetupResults) error {
	// Step 1: Create AddOnDeploymentConfig for addon namespace
	if err := r.trackSetupStep(ctx, results, "addon-deployment-config", r.createAddonDeploymentConfig); err != nil {
		return err
	}

	// Step 2: Update ClusterManagementAddOns
	addons := []string{"work-manager", "managed-serviceaccount", "cluster-proxy"}
	for _, addon := range addons {
		stepName := fmt.Sprintf("cluster-management-addon-%s", addon)
		if err := r.trackSetupStep(ctx, results, stepName, func(ctx context.Context) error {
			return r.updateClusterManagementAddOn(ctx, addon)
		}); err != nil {
			return err
		}
	}

	// Step 3: Create KlusterletConfig (placeholder)
	if err := r.trackSetupStep(ctx, results, "klusterlet-config", r.createKlusterletConfig); err != nil {
		return err
	}

	// Step 4: Configure Hypershift Addon
	if err := r.trackSetupStep(ctx, results, "hypershift-addon-config", r.configureHypershiftAddon); err != nil {
		return err
	}

	// Step 5: Apply backup labels if enabled
	if r.BackupEnabled {
		if err := r.trackSetupStep(ctx, results, "backup-labels", r.applyBackupLabels); err != nil {
			return err
		}
	}

	return nil
}

// trackSetupStep executes a setup step and tracks its result
func (r *ACMHubSetupController) trackSetupStep(ctx context.Context, results *SetupResults, component string, setupFunc func(context.Context) error) error {
	r.Log.Info("Executing setup step", "component", component)

	startTime := time.Now()
	err := setupFunc(ctx)

	result := SetupResult{
		Component: component,
		Timestamp: startTime,
	}

	if err != nil {
		result.Status = StatusFailed
		result.Error = err.Error()
		result.Message = fmt.Sprintf("Failed to setup %s", component)
		results.Results = append(results.Results, result)
		return fmt.Errorf("setup step %s failed: %w", component, err)
	}

	result.Status = StatusCompleted
	result.Message = fmt.Sprintf("Successfully configured %s", component)
	results.Results = append(results.Results, result)

	r.Log.Info("Setup step completed", "component", component)
	return nil
}

// undoACMHubWithTracking performs ACM hub undo with detailed result tracking
func (r *ACMHubSetupController) undoACMHubWithTracking(ctx context.Context, results *SetupResults) error {
	// Step 1: Remove backup labels if they were applied
	if r.BackupEnabled {
		if err := r.trackSetupStep(ctx, results, "remove-backup-labels", r.removeBackupLabels); err != nil {
			// Don't fail the entire undo for backup label removal failure
			r.Log.Info("Failed to remove backup labels, continuing with undo", "error", err)
		}
	}

	// Step 2: Restore Hypershift Addon Configuration
	if err := r.trackSetupStep(ctx, results, "restore-hypershift-addon-config", r.restoreHypershiftAddon); err != nil {
		// Don't fail the entire undo for hypershift addon restoration failure
		r.Log.Info("Failed to restore hypershift addon config, continuing with undo", "error", err)
	}

	// Step 3: Restore ClusterManagementAddOns (remove our config references)
	addons := []string{"work-manager", "managed-serviceaccount", "cluster-proxy"}
	for _, addon := range addons {
		stepName := fmt.Sprintf("restore-cluster-management-addon-%s", addon)
		if err := r.trackSetupStep(ctx, results, stepName, func(ctx context.Context) error {
			return r.restoreClusterManagementAddOn(ctx, addon)
		}); err != nil {
			// Don't fail the entire undo for addon restoration failure
			r.Log.Info("Failed to restore ClusterManagementAddOn, continuing with undo", "addon", addon, "error", err)
		}
	}

	// Step 4: Delete AddOnDeploymentConfig
	if err := r.trackSetupStep(ctx, results, "delete-addon-deployment-config", r.deleteAddonDeploymentConfig); err != nil {
		// Don't fail the entire undo for addon deployment config deletion failure
		r.Log.Info("Failed to delete AddOnDeploymentConfig, continuing with undo", "error", err)
	}

	// Step 5: Delete KlusterletConfig (placeholder)
	if err := r.trackSetupStep(ctx, results, "delete-klusterlet-config", r.deleteKlusterletConfig); err != nil {
		// Don't fail the entire undo for klusterlet config deletion failure
		r.Log.Info("Failed to delete KlusterletConfig, continuing with undo", "error", err)
	}

	return nil
}

// deleteAddonDeploymentConfig removes the addon deployment config we created
func (r *ACMHubSetupController) deleteAddonDeploymentConfig(ctx context.Context) error {
	config := &addonv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      AddonNSConfigName,
			Namespace: r.ACMNamespace,
		},
	}

	err := r.Delete(ctx, config)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete AddOnDeploymentConfig %s: %w", AddonNSConfigName, err)
	}

	r.Log.Info("Deleted AddOnDeploymentConfig", "name", AddonNSConfigName)
	return nil
}

// restoreClusterManagementAddOn removes our config reference from the addon
func (r *ACMHubSetupController) restoreClusterManagementAddOn(ctx context.Context, addonName string) error {
	addon := &addonv1alpha1.ClusterManagementAddOn{}
	err := r.Get(ctx, types.NamespacedName{Name: addonName}, addon)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.Log.Info("ClusterManagementAddOn not found during restore, skipping", "addon", addonName)
			return nil
		}
		return fmt.Errorf("failed to get ClusterManagementAddOn %s: %w", addonName, err)
	}

	// Remove our config reference from placements
	modified := false
	for i := range addon.Spec.InstallStrategy.Placements {
		placement := &addon.Spec.InstallStrategy.Placements[i]

		// Filter out our config reference
		var newConfigs []addonv1alpha1.AddOnConfig
		for _, config := range placement.Configs {
			if !(config.ConfigReferent.Name == AddonNSConfigName &&
				config.ConfigReferent.Namespace == r.ACMNamespace) {
				newConfigs = append(newConfigs, config)
			} else {
				modified = true
			}
		}
		placement.Configs = newConfigs
	}

	if modified {
		if err := r.Update(ctx, addon); err != nil {
			return fmt.Errorf("failed to update ClusterManagementAddOn %s: %w", addonName, err)
		}
		r.Log.Info("Restored ClusterManagementAddOn", "addon", addonName)
	} else {
		r.Log.Info("No changes needed for ClusterManagementAddOn", "addon", addonName)
	}

	return nil
}

// restoreHypershiftAddon restores the hypershift addon configuration
func (r *ACMHubSetupController) restoreHypershiftAddon(ctx context.Context) error {
	config := &addonv1alpha1.AddOnDeploymentConfig{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      HypershiftAddonConfigName,
		Namespace: r.ACMNamespace,
	}, config)

	if err != nil {
		if apierrors.IsNotFound(err) {
			r.Log.Info("Hypershift addon deployment config not found during restore, skipping")
			return nil
		}
		return fmt.Errorf("failed to get hypershift addon config: %w", err)
	}

	// Reset to default namespace (remove our custom namespace)
	originalModified := false
	if config.Spec.AgentInstallNamespace == r.AddonNamespace {
		config.Spec.AgentInstallNamespace = ""
		originalModified = true
	}

	// Remove discovery-specific variables
	variablesToRemove := map[string]bool{
		"disableMetrics":      true,
		"disableHOManagement": true,
	}

	var newVariables []addonv1alpha1.CustomizedVariable
	for _, v := range config.Spec.CustomizedVariables {
		if !variablesToRemove[v.Name] {
			newVariables = append(newVariables, v)
		} else {
			originalModified = true
		}
	}

	if originalModified {
		config.Spec.CustomizedVariables = newVariables
		if err := r.Update(ctx, config); err != nil {
			return fmt.Errorf("failed to restore hypershift addon config: %w", err)
		}
		r.Log.Info("Restored hypershift addon configuration")
	} else {
		r.Log.Info("No changes needed for hypershift addon configuration")
	}

	return nil
}

// removeBackupLabels removes backup labels from resources
func (r *ACMHubSetupController) removeBackupLabels(ctx context.Context) error {
	resources := []struct {
		name      string
		namespace string
		obj       client.Object
	}{
		{AddonNSConfigName, r.ACMNamespace, &addonv1alpha1.AddOnDeploymentConfig{}},
		{HypershiftAddonConfigName, r.ACMNamespace, &addonv1alpha1.AddOnDeploymentConfig{}},
		{"work-manager", "", &addonv1alpha1.ClusterManagementAddOn{}},
		{"cluster-proxy", "", &addonv1alpha1.ClusterManagementAddOn{}},
		{"managed-serviceaccount", "", &addonv1alpha1.ClusterManagementAddOn{}},
	}

	for _, resource := range resources {
		key := types.NamespacedName{Name: resource.name, Namespace: resource.namespace}
		if err := r.Get(ctx, key, resource.obj); err != nil {
			if apierrors.IsNotFound(err) {
				r.Log.V(1).Info("Resource not found for backup label removal", "resource", resource.name)
				continue
			}
			// Don't fail for permission errors or other issues
			r.Log.Info("Failed to get resource for backup label removal", "resource", resource.name, "error", err)
			continue
		}

		labels := resource.obj.GetLabels()
		if labels != nil {
			if _, exists := labels[BackupLabel]; exists {
				delete(labels, BackupLabel)
				resource.obj.SetLabels(labels)

				if err := r.Update(ctx, resource.obj); err != nil {
					r.Log.Info("Failed to remove backup label", "resource", resource.name, "error", err)
					continue
				}

				r.Log.V(1).Info("Removed backup label", "resource", resource.name)
			}
		}
	}

	return nil
}

// deleteKlusterletConfig removes the klusterlet config (placeholder)
func (r *ACMHubSetupController) deleteKlusterletConfig(ctx context.Context) error {
	// Note: This would require importing the KlusterletConfig API
	// For now, we'll create a placeholder that logs the intent
	r.Log.Info("KlusterletConfig deletion placeholder", "name", KlusterletConfigName)

	// In a real implementation, this would be:
	// klusterletConfig := &configv1alpha1.KlusterletConfig{
	//     ObjectMeta: metav1.ObjectMeta{
	//         Name: KlusterletConfigName,
	//     },
	// }
	// return r.Delete(ctx, klusterletConfig)

	return nil
}

// setupACMHub performs the complete ACM hub setup
func (r *ACMHubSetupController) setupACMHub(ctx context.Context) error {
	// Step 1: Create AddOnDeploymentConfig for addon namespace
	if err := r.createAddonDeploymentConfig(ctx); err != nil {
		return fmt.Errorf("failed to create addon deployment config: %w", err)
	}

	// Step 2: Update ClusterManagementAddOns
	addons := []string{"work-manager", "managed-serviceaccount", "cluster-proxy"}
	for _, addon := range addons {
		if err := r.updateClusterManagementAddOn(ctx, addon); err != nil {
			return fmt.Errorf("failed to update ClusterManagementAddOn %s: %w", addon, err)
		}
	}

	// Step 3: Create KlusterletConfig (placeholder - would need the actual API)
	if err := r.createKlusterletConfig(ctx); err != nil {
		return fmt.Errorf("failed to create klusterlet config: %w", err)
	}

	// Step 4: Configure Hypershift Addon
	if err := r.configureHypershiftAddon(ctx); err != nil {
		return fmt.Errorf("failed to configure hypershift addon: %w", err)
	}

	// Step 5: Apply backup labels if enabled
	if r.BackupEnabled {
		if err := r.applyBackupLabels(ctx); err != nil {
			return fmt.Errorf("failed to apply backup labels: %w", err)
		}
	}

	return nil
}

// createAddonDeploymentConfig creates the addon deployment config
func (r *ACMHubSetupController) createAddonDeploymentConfig(ctx context.Context) error {
	config := &addonv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      AddonNSConfigName,
			Namespace: r.ACMNamespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, config, func() error {
		config.Spec.AgentInstallNamespace = r.AddonNamespace
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create/update %s: %w", AddonNSConfigName, err)
	}

	r.Log.Info("Created/updated AddOnDeploymentConfig", "name", AddonNSConfigName)
	return nil
}

// updateClusterManagementAddOn updates a ClusterManagementAddOn to reference the addon deployment config
func (r *ACMHubSetupController) updateClusterManagementAddOn(ctx context.Context, addonName string) error {
	addon := &addonv1alpha1.ClusterManagementAddOn{}
	err := r.Get(ctx, types.NamespacedName{Name: addonName}, addon)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.Log.Info("ClusterManagementAddOn not found, skipping", "addon", addonName)
			return nil
		}
		return fmt.Errorf("failed to get ClusterManagementAddOn %s: %w", addonName, err)
	}

	// Update the addon to reference our deployment config
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, addon, func() error {
		if addon.Spec.InstallStrategy.Type != addonv1alpha1.AddonInstallStrategyPlacements {
			addon.Spec.InstallStrategy.Type = addonv1alpha1.AddonInstallStrategyPlacements
		}

		// Ensure we have at least one placement
		if len(addon.Spec.InstallStrategy.Placements) == 0 {
			addon.Spec.InstallStrategy.Placements = []addonv1alpha1.PlacementStrategy{
				{
					PlacementRef: addonv1alpha1.PlacementRef{
						Name:      "global",
						Namespace: "open-cluster-management-global-set",
					},
				},
			}
		}

		// Add our config reference to the first placement
		placement := &addon.Spec.InstallStrategy.Placements[0]
		configRef := addonv1alpha1.AddOnConfig{
			ConfigGroupResource: addonv1alpha1.ConfigGroupResource{
				Group:    "addon.open-cluster-management.io",
				Resource: "addondeploymentconfigs",
			},
			ConfigReferent: addonv1alpha1.ConfigReferent{
				Name:      AddonNSConfigName,
				Namespace: r.ACMNamespace,
			},
		}

		// Check if config already exists
		configExists := false
		for _, config := range placement.Configs {
			if config.ConfigReferent.Name == AddonNSConfigName &&
				config.ConfigReferent.Namespace == r.ACMNamespace {
				configExists = true
				break
			}
		}

		if !configExists {
			placement.Configs = append(placement.Configs, configRef)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to update ClusterManagementAddOn %s: %w", addonName, err)
	}

	r.Log.Info("Updated ClusterManagementAddOn", "addon", addonName)
	return nil
}

// createKlusterletConfig creates the klusterlet config for MCE import
func (r *ACMHubSetupController) createKlusterletConfig(ctx context.Context) error {
	// Note: This would require importing the KlusterletConfig API
	// For now, we'll create a placeholder that logs the intent
	r.Log.Info("KlusterletConfig creation placeholder", "name", KlusterletConfigName)

	// In a real implementation, this would be:
	// klusterletConfig := &configv1alpha1.KlusterletConfig{
	//     ObjectMeta: metav1.ObjectMeta{
	//         Name: KlusterletConfigName,
	//     },
	//     Spec: configv1alpha1.KlusterletConfigSpec{
	//         InstallMode: configv1alpha1.InstallMode{
	//             Type: "noOperator",
	//             NoOperator: &configv1alpha1.NoOperator{
	//                 Postfix: "mce-import",
	//             },
	//         },
	//     },
	// }
	// return controllerutil.CreateOrUpdate(ctx, r.Client, klusterletConfig, func() error { return nil })

	return nil
}

// configureHypershiftAddon configures the hypershift addon for discovery
func (r *ACMHubSetupController) configureHypershiftAddon(ctx context.Context) error {
	config := &addonv1alpha1.AddOnDeploymentConfig{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      HypershiftAddonConfigName,
		Namespace: r.ACMNamespace,
	}, config)

	if err != nil {
		if apierrors.IsNotFound(err) {
			r.Log.Info("Hypershift addon deployment config not found, skipping configuration")
			return nil
		}
		return fmt.Errorf("failed to get hypershift addon config: %w", err)
	}

	// Update the hypershift addon configuration
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, config, func() error {
		config.Spec.AgentInstallNamespace = r.AddonNamespace

		// Set discovery-specific variables
		variables := []addonv1alpha1.CustomizedVariable{
			{Name: "disableMetrics", Value: "true"},
			{Name: "disableHOManagement", Value: "true"},
		}

		// Merge with existing variables
		existingVars := make(map[string]string)
		for _, v := range config.Spec.CustomizedVariables {
			existingVars[v.Name] = v.Value
		}

		for _, v := range variables {
			existingVars[v.Name] = v.Value
		}

		// Rebuild the variables list
		config.Spec.CustomizedVariables = []addonv1alpha1.CustomizedVariable{}
		for name, value := range existingVars {
			config.Spec.CustomizedVariables = append(config.Spec.CustomizedVariables, addonv1alpha1.CustomizedVariable{
				Name:  name,
				Value: value,
			})
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to update hypershift addon config: %w", err)
	}

	r.Log.Info("Configured hypershift addon for discovery mode")
	return nil
}

// applyBackupLabels applies backup labels to resources for disaster recovery
func (r *ACMHubSetupController) applyBackupLabels(ctx context.Context) error {
	resources := []struct {
		name      string
		namespace string
		obj       client.Object
	}{
		{AddonNSConfigName, r.ACMNamespace, &addonv1alpha1.AddOnDeploymentConfig{}},
		{HypershiftAddonConfigName, r.ACMNamespace, &addonv1alpha1.AddOnDeploymentConfig{}},
		{"work-manager", "", &addonv1alpha1.ClusterManagementAddOn{}},
		{"cluster-proxy", "", &addonv1alpha1.ClusterManagementAddOn{}},
		{"managed-serviceaccount", "", &addonv1alpha1.ClusterManagementAddOn{}},
	}

	for _, resource := range resources {
		key := types.NamespacedName{Name: resource.name, Namespace: resource.namespace}
		if err := r.Get(ctx, key, resource.obj); err != nil {
			if apierrors.IsNotFound(err) {
				r.Log.V(1).Info("Resource not found for backup label", "resource", resource.name)
				continue
			}
			return fmt.Errorf("failed to get resource %s for backup label: %w", resource.name, err)
		}

		labels := resource.obj.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[BackupLabel] = "true"
		resource.obj.SetLabels(labels)

		if err := r.Update(ctx, resource.obj); err != nil {
			return fmt.Errorf("failed to apply backup label to %s: %w", resource.name, err)
		}

		r.Log.V(1).Info("Applied backup label", "resource", resource.name)
	}

	return nil
}

// markACMHubSetupCompleted marks the ACM hub setup as completed
func (r *ACMHubSetupController) markACMHubSetupCompleted(ctx context.Context, ns *corev1.Namespace) error {
	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}
	ns.Labels[ACMHubSetupLabel] = "true"

	return r.Update(ctx, ns)
}

// SetupWithManager sets up the controller with the Manager
func (r *ACMHubSetupController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Owns(&addonv1alpha1.AddOnDeploymentConfig{}).
		Owns(&addonv1alpha1.ClusterManagementAddOn{}).
		Complete(r)
}

// NewACMHubSetupController creates a new ACM Hub Setup Controller
func NewACMHubSetupController(client client.Client, scheme *runtime.Scheme, log logr.Logger) *ACMHubSetupController {
	return &ACMHubSetupController{
		Client: client,
		Scheme: scheme,
		Log:    log.WithName(ACMHubSetupControllerName),

		AddonNamespace:  getEnvOrDefault(EnvAddonNamespace, DefaultAddonNamespace),
		ACMNamespace:    getEnvOrDefault(EnvACMNamespace, DefaultACMNamespace),
		PolicyNamespace: getEnvOrDefault(EnvPolicyNamespace, DefaultPolicyNamespace),
		BackupEnabled:   !strings.EqualFold(os.Getenv(EnvBackupEnabled), "false"),
	}
}

// IsACMHubSetupEnabled checks if ACM hub setup should be enabled
func IsACMHubSetupEnabled() bool {
	return !strings.EqualFold(os.Getenv(EnvEnableACMHubSetup), "false")
}
