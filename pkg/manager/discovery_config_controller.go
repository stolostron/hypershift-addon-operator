package manager

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

// DiscoveryConfigController manages discovery configuration for HyperShift addon
// This controller watches AddonDeploymentConfig resources for hypershift-addon-deploy-config
type DiscoveryConfigController struct {
	client.Client
	Log               logr.Logger
	Scheme            *runtime.Scheme
	OperatorNamespace string
}

// Reconcile implements the reconcile.Reconciler interface
func (r *DiscoveryConfigController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("addondeploymentconfig", req.NamespacedName)
	log.Info("Reconciling AddonDeploymentConfig for discovery config controller")

	// Get the AddonDeploymentConfig
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err := r.Get(ctx, req.NamespacedName, addonConfig)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("AddonDeploymentConfig not found, may have been deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get AddonDeploymentConfig")
		return ctrl.Result{}, err
	}

	// Check if ACM is installed first - skip reconciliation if not
	acmInstalled, err := r.isACMInstalled(ctx)
	if err != nil {
		log.Error(err, "Failed to check ACM installation status")
		return ctrl.Result{}, err
	}

	if !acmInstalled {
		log.Info("Skipping reconciliation - ACM is not installed in the cluster",
			"name", addonConfig.Name)
		return ctrl.Result{}, nil
	}

	// Debug: Log key fields to understand what's changing
	log.Info("AddonDeploymentConfig details",
		"name", addonConfig.Name,
		"namespace", addonConfig.Namespace,
		"generation", addonConfig.Generation,
		"resourceVersion", addonConfig.ResourceVersion,
		"customizedVariables", len(addonConfig.Spec.CustomizedVariables),
	)

	// Only process hypershift-addon-deploy-config
	if addonConfig.Name != "hypershift-addon-deploy-config" {
		log.V(1).Info("Skipping non-hypershift addon deployment config", "name", addonConfig.Name)
		return ctrl.Result{}, nil
	}

	// Only process if in multicluster-engine namespace
	if addonConfig.Namespace != "multicluster-engine" {
		log.V(1).Info("Skipping addon deployment config not in multicluster-engine namespace", "namespace", addonConfig.Namespace)
		return ctrl.Result{}, nil
	}

	// Check configureMceImport custom variable state
	configureMceImportValue := getConfigureMceImportValue(addonConfig)

	// Only process if configureMceImport is explicitly set to "true" or "false"
	if configureMceImportValue != "true" && configureMceImportValue != "false" {
		log.Info("Skipping addon deployment config processing - configureMceImport is not set to 'true' or 'false'",
			"name", addonConfig.Name, "configureMceImport", configureMceImportValue)

		// Still create/update the ConfigMap for monitoring purposes
		err = r.createOrUpdateAddonConfigMap(ctx, addonConfig)
		if err != nil {
			log.Error(err, "Failed to create/update addon config ConfigMap")
			return ctrl.Result{}, err
		}

		log.Info("Successfully processed addon deployment config (no action taken)", "name", addonConfig.Name)
		return ctrl.Result{}, nil
	}

	configureMceImportEnabled := configureMceImportValue == "true"

	if configureMceImportEnabled {
		log.Info("Processing addon deployment config - configureMceImport is enabled",
			"name", addonConfig.Name, "configureMceImport", configureMceImportValue)

		// Ensure required customized variables are present in hypershift-addon-deploy-config
		err = r.ensureHypershiftAddonCustomizedVariables(ctx, log, addonConfig)
		if err != nil {
			log.Error(err, "Failed to ensure hypershift addon customized variables")
			return ctrl.Result{}, err
		}

		// Create the addon deployment configuration for discovery
		err = r.createOrUpdateDiscoveryAddonConfig(ctx, log)
		if err != nil {
			log.Error(err, "Failed to create/update discovery addon deployment config")
			return ctrl.Result{}, err
		}

		// Update ClusterManagementAddOns to use the discovery config
		err = r.updateClusterManagementAddOns(ctx, log)
		if err != nil {
			log.Error(err, "Failed to update ClusterManagementAddOn configurations")
			return ctrl.Result{}, err
		}

		// Create the KlusterletConfig for MCE import
		err = r.createOrUpdateKlusterletConfig(ctx, log)
		if err != nil {
			log.Error(err, "Failed to create/update KlusterletConfig")
			return ctrl.Result{}, err
		}
	} else {
		log.Info("Processing addon deployment config - configureMceImport is disabled",
			"name", addonConfig.Name, "configureMceImport", configureMceImportValue)

		// Check if there are ManagedClusters still using the mce-import-klusterlet-config
		hasClustersUsingConfig, clusterNames, err := r.checkManagedClustersUsingKlusterletConfig(ctx, log)
		if err != nil {
			log.Error(err, "Failed to check ManagedClusters using klusterlet config")
			return ctrl.Result{}, err
		}

		if hasClustersUsingConfig {
			log.Info("Cannot disable MCE import - there are ManagedClusters still using mce-import-klusterlet-config",
				"clusters", clusterNames)
			// Create/update the ConfigMap but don't perform removal operations
			err = r.createOrUpdateAddonConfigMap(ctx, addonConfig)
			if err != nil {
				log.Error(err, "Failed to create/update addon config ConfigMap")
				return ctrl.Result{}, err
			}
			log.Info("Successfully processed addon deployment config (no action taken due to active clusters)", "name", addonConfig.Name)
			return ctrl.Result{}, nil
		}

		// Remove required customized variables from hypershift-addon-deploy-config
		err = r.removeHypershiftAddonCustomizedVariables(ctx, log, addonConfig)
		if err != nil {
			log.Error(err, "Failed to remove hypershift addon customized variables")
			return ctrl.Result{}, err
		}

		// Remove the addon deployment configuration for discovery if it exists
		err = r.removeDiscoveryAddonConfig(ctx, log)
		if err != nil {
			log.Error(err, "Failed to remove discovery addon deployment config")
			return ctrl.Result{}, err
		}

		// Remove ClusterManagementAddOn configuration references
		err = r.removeClusterManagementAddOnConfigs(ctx, log)
		if err != nil {
			log.Error(err, "Failed to remove ClusterManagementAddOn configurations")
			return ctrl.Result{}, err
		}

		// Remove the KlusterletConfig for MCE import
		err = r.removeKlusterletConfig(ctx, log)
		if err != nil {
			log.Error(err, "Failed to remove KlusterletConfig")
			return ctrl.Result{}, err
		}
	}

	// Create a ConfigMap with addon deployment configuration information
	err = r.createOrUpdateAddonConfigMap(ctx, addonConfig)
	if err != nil {
		log.Error(err, "Failed to create/update addon config ConfigMap")
		return ctrl.Result{}, err
	}

	log.Info("Successfully processed addon deployment config", "name", addonConfig.Name)

	// Only requeue if there was an actual change or error
	return ctrl.Result{}, nil
}

// ensureHypershiftAddonCustomizedVariables ensures the hypershift-addon-deploy-config has required customized variables and agentInstallNamespace
func (r *DiscoveryConfigController) ensureHypershiftAddonCustomizedVariables(ctx context.Context, log logr.Logger, addonConfig *addonapiv1alpha1.AddOnDeploymentConfig) error {
	requiredVariables := map[string]string{
		"disableMetrics":      "true",
		"disableHOManagement": "true",
	}

	requiredAgentInstallNamespace := "open-cluster-management-agent-addon-discovery"

	// Check if all required variables are present with correct values
	existingVariables := make(map[string]string)
	for _, variable := range addonConfig.Spec.CustomizedVariables {
		existingVariables[variable.Name] = variable.Value
	}

	needsUpdate := false
	var updatedVariables []addonapiv1alpha1.CustomizedVariable

	// Copy existing variables
	for _, variable := range addonConfig.Spec.CustomizedVariables {
		updatedVariables = append(updatedVariables, variable)
	}

	// Add or update required variables
	for varName, varValue := range requiredVariables {
		if existingValue, exists := existingVariables[varName]; !exists || existingValue != varValue {
			needsUpdate = true
			// Remove existing variable if it exists
			var filteredVariables []addonapiv1alpha1.CustomizedVariable
			for _, variable := range updatedVariables {
				if variable.Name != varName {
					filteredVariables = append(filteredVariables, variable)
				}
			}
			// Add the required variable
			filteredVariables = append(filteredVariables, addonapiv1alpha1.CustomizedVariable{
				Name:  varName,
				Value: varValue,
			})
			updatedVariables = filteredVariables
		}
	}

	// Check if agentInstallNamespace needs to be updated
	if addonConfig.Spec.AgentInstallNamespace != requiredAgentInstallNamespace {
		needsUpdate = true
		addonConfig.Spec.AgentInstallNamespace = requiredAgentInstallNamespace
	}

	if !needsUpdate {
		log.V(1).Info("Hypershift addon customized variables and agentInstallNamespace are already correct")
		return nil
	}

	// Update the addon config with required variables and agentInstallNamespace
	addonConfig.Spec.CustomizedVariables = updatedVariables
	log.Info("Updating hypershift addon deployment config with required customized variables and agentInstallNamespace",
		"disableMetrics", "true", "disableHOManagement", "true", "agentInstallNamespace", requiredAgentInstallNamespace)
	return r.Update(ctx, addonConfig)
}

// removeHypershiftAddonCustomizedVariables removes the required customized variables and resets agentInstallNamespace when MCE import is disabled
func (r *DiscoveryConfigController) removeHypershiftAddonCustomizedVariables(ctx context.Context, log logr.Logger, addonConfig *addonapiv1alpha1.AddOnDeploymentConfig) error {
	variablesToRemove := map[string]bool{
		"disableMetrics":      true,
		"disableHOManagement": true,
	}

	defaultAgentInstallNamespace := "open-cluster-management-agent-addon"

	// Check if any of the variables to remove are present
	hasVariablesToRemove := false
	var updatedVariables []addonapiv1alpha1.CustomizedVariable

	for _, variable := range addonConfig.Spec.CustomizedVariables {
		if !variablesToRemove[variable.Name] {
			// Keep variables that are not in the removal list
			updatedVariables = append(updatedVariables, variable)
		} else {
			// Mark that we found a variable to remove
			hasVariablesToRemove = true
		}
	}

	// Check if agentInstallNamespace needs to be reset to default
	needsAgentInstallNamespaceReset := addonConfig.Spec.AgentInstallNamespace != defaultAgentInstallNamespace

	if !hasVariablesToRemove && !needsAgentInstallNamespaceReset {
		log.V(1).Info("No hypershift addon customized variables to remove")
		return nil
	}

	// Update the addon config by removing the specified variables and resetting agentInstallNamespace
	addonConfig.Spec.CustomizedVariables = updatedVariables
	if needsAgentInstallNamespaceReset {
		addonConfig.Spec.AgentInstallNamespace = defaultAgentInstallNamespace
	}

	logMessage := "Removing hypershift addon customized variables"
	logFields := []interface{}{"disableMetrics", "removed", "disableHOManagement", "removed"}
	if needsAgentInstallNamespaceReset {
		logMessage += " and resetting agentInstallNamespace"
		logFields = append(logFields, "agentInstallNamespace", defaultAgentInstallNamespace)
	}
	log.Info(logMessage, logFields...)
	return r.Update(ctx, addonConfig)
}

// createOrUpdateAddonConfigMap creates or updates a ConfigMap with addon deployment configuration information
func (r *DiscoveryConfigController) createOrUpdateAddonConfigMap(ctx context.Context, addonConfig *addonapiv1alpha1.AddOnDeploymentConfig) error {
	configMapName := "hypershift-addon-deploy-config-info"

	// Prepare the desired data
	configureMceImportValue := getConfigureMceImportValue(addonConfig)
	desiredData := map[string]string{
		"config-name":        addonConfig.Name,
		"config-namespace":   addonConfig.Namespace,
		"install-namespace":  addonConfig.Spec.AgentInstallNamespace,
		"configureMceImport": configureMceImportValue,
		"import-enabled":     fmt.Sprintf("%t", configureMceImportValue == "true"),
	}

	// Add customized variables as individual keys
	for _, variable := range addonConfig.Spec.CustomizedVariables {
		key := fmt.Sprintf("var-%s", variable.Name)
		desiredData[key] = variable.Value
	}

	// Try to get existing ConfigMap
	existingConfigMap := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{Name: configMapName, Namespace: r.OperatorNamespace}, existingConfigMap)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create new ConfigMap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: r.OperatorNamespace,
				},
				Data: desiredData,
			}
			// Add creation timestamp
			configMap.Data["created-at"] = time.Now().Format(time.RFC3339)
			r.Log.Info("Creating addon config ConfigMap", "configmap", configMapName)
			return r.Create(ctx, configMap)
		}
		return err
	}

	// Check if update is needed by comparing relevant fields
	needsUpdate := false
	for key, desiredValue := range desiredData {
		if existingValue, exists := existingConfigMap.Data[key]; !exists || existingValue != desiredValue {
			needsUpdate = true
			break
		}
	}

	// Also check if there are extra keys that should be removed (old variables)
	for key := range existingConfigMap.Data {
		if key != "created-at" && key != "last-updated" {
			if _, exists := desiredData[key]; !exists {
				needsUpdate = true
				break
			}
		}
	}

	if !needsUpdate {
		r.Log.V(1).Info("ConfigMap is up to date, skipping update", "configmap", configMapName)
		return nil
	}

	// Update existing ConfigMap with new data
	if existingConfigMap.Data == nil {
		existingConfigMap.Data = make(map[string]string)
	}

	// Clear old data (except timestamps) and set new data
	for key := range existingConfigMap.Data {
		if key != "created-at" && key != "last-updated" {
			delete(existingConfigMap.Data, key)
		}
	}
	for key, value := range desiredData {
		existingConfigMap.Data[key] = value
	}

	// Update the last-updated timestamp only when there's an actual change
	existingConfigMap.Data["last-updated"] = time.Now().Format(time.RFC3339)

	r.Log.Info("Updating addon config ConfigMap", "configmap", configMapName)
	return r.Update(ctx, existingConfigMap)
}

// createOrUpdateDiscoveryAddonConfig creates or updates the addon-ns-config AddOnDeploymentConfig
// as described in discovering_hostedclusters.md
func (r *DiscoveryConfigController) createOrUpdateDiscoveryAddonConfig(ctx context.Context, log logr.Logger) error {
	discoveryAddonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "addon-ns-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			AgentInstallNamespace: "open-cluster-management-agent-addon-discovery",
		},
	}

	// Try to get existing AddOnDeploymentConfig
	existingConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err := r.Get(ctx, client.ObjectKey{Name: "addon-ns-config", Namespace: "multicluster-engine"}, existingConfig)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create new AddOnDeploymentConfig
			log.Info("Creating discovery addon deployment config", "name", "addon-ns-config")
			return r.Create(ctx, discoveryAddonConfig)
		}
		return err
	}

	// Update existing AddOnDeploymentConfig if needed
	if existingConfig.Spec.AgentInstallNamespace != "open-cluster-management-agent-addon-discovery" {
		existingConfig.Spec.AgentInstallNamespace = "open-cluster-management-agent-addon-discovery"
		log.Info("Updating discovery addon deployment config", "name", "addon-ns-config")
		return r.Update(ctx, existingConfig)
	}

	log.V(1).Info("Discovery addon deployment config is up to date", "name", "addon-ns-config")
	return nil
}

// removeDiscoveryAddonConfig removes the addon-ns-config AddOnDeploymentConfig when configureMceImport is disabled
func (r *DiscoveryConfigController) removeDiscoveryAddonConfig(ctx context.Context, log logr.Logger) error {
	discoveryAddonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "addon-ns-config",
			Namespace: "multicluster-engine",
		},
	}

	err := r.Get(ctx, client.ObjectKey{Name: "addon-ns-config", Namespace: "multicluster-engine"}, discoveryAddonConfig)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Already doesn't exist, nothing to do
			log.V(1).Info("Discovery addon deployment config already doesn't exist", "name", "addon-ns-config")
			return nil
		}
		return err
	}

	// Delete the AddOnDeploymentConfig
	log.Info("Removing discovery addon deployment config", "name", "addon-ns-config")
	return r.Delete(ctx, discoveryAddonConfig)
}

// updateClusterManagementAddOns updates multiple ClusterManagementAddOns to reference addon-ns-config
func (r *DiscoveryConfigController) updateClusterManagementAddOns(ctx context.Context, log logr.Logger) error {
	addonsToUpdate := []string{"work-manager", "managed-serviceaccount", "cluster-proxy"}

	for _, addonName := range addonsToUpdate {
		err := r.updateSingleClusterManagementAddOn(ctx, log, addonName)
		if err != nil {
			return err
		}
	}

	// Handle special addons (application-manager) separately - they use Manual strategy with supportedConfigs
	// Note: hypershift-addon is excluded from updates to avoid conflicts
	specialAddons := []string{"application-manager"}
	for _, addonName := range specialAddons {
		err := r.updateSpecialAddOn(ctx, log, addonName)
		if err != nil {
			return err
		}
	}

	return nil
}

// updateSingleClusterManagementAddOn updates a single ClusterManagementAddOn to reference addon-ns-config
func (r *DiscoveryConfigController) updateSingleClusterManagementAddOn(ctx context.Context, log logr.Logger, addonName string) error {
	// Get the ClusterManagementAddOn
	addon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err := r.Get(ctx, client.ObjectKey{Name: addonName}, addon)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ClusterManagementAddOn not found, skipping update", "addon", addonName)
			return nil
		}
		return err
	}

	// Check if the config is already present
	configExists := false
	for _, placement := range addon.Spec.InstallStrategy.Placements {
		if placement.PlacementRef.Name == "global" && placement.PlacementRef.Namespace == "open-cluster-management-global-set" {
			for _, config := range placement.Configs {
				if config.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
					config.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
					config.ConfigReferent.Name == "addon-ns-config" &&
					config.ConfigReferent.Namespace == "multicluster-engine" {
					configExists = true
					break
				}
			}
			break
		}
	}

	if configExists {
		log.V(1).Info("ClusterManagementAddOn already has discovery config reference", "addon", addonName)
		return nil
	}

	// Add the config reference to the global placement
	for i, placement := range addon.Spec.InstallStrategy.Placements {
		if placement.PlacementRef.Name == "global" && placement.PlacementRef.Namespace == "open-cluster-management-global-set" {
			newConfig := addonapiv1alpha1.AddOnConfig{
				ConfigGroupResource: addonapiv1alpha1.ConfigGroupResource{
					Group:    "addon.open-cluster-management.io",
					Resource: "addondeploymentconfigs",
				},
				ConfigReferent: addonapiv1alpha1.ConfigReferent{
					Name:      "addon-ns-config",
					Namespace: "multicluster-engine",
				},
			}
			addon.Spec.InstallStrategy.Placements[i].Configs = append(
				addon.Spec.InstallStrategy.Placements[i].Configs,
				newConfig,
			)
			break
		}
	}

	log.Info("Updating ClusterManagementAddOn to reference discovery config", "addon", addonName)
	return r.Update(ctx, addon)
}

// removeClusterManagementAddOnConfigs removes the addon-ns-config reference from multiple ClusterManagementAddOns
func (r *DiscoveryConfigController) removeClusterManagementAddOnConfigs(ctx context.Context, log logr.Logger) error {
	addonsToUpdate := []string{"work-manager", "managed-serviceaccount", "cluster-proxy"}

	for _, addonName := range addonsToUpdate {
		err := r.removeSingleClusterManagementAddOnConfig(ctx, log, addonName)
		if err != nil {
			return err
		}
	}

	// Handle special addons (application-manager) separately - remove supportedConfigs
	// Note: hypershift-addon is excluded from removal to avoid conflicts
	specialAddons := []string{"application-manager"}
	for _, addonName := range specialAddons {
		err := r.removeSpecialAddOnConfig(ctx, log, addonName)
		if err != nil {
			return err
		}
	}

	return nil
}

// removeSingleClusterManagementAddOnConfig removes the addon-ns-config reference from a single ClusterManagementAddOn
func (r *DiscoveryConfigController) removeSingleClusterManagementAddOnConfig(ctx context.Context, log logr.Logger, addonName string) error {
	// Get the ClusterManagementAddOn
	addon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err := r.Get(ctx, client.ObjectKey{Name: addonName}, addon)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("ClusterManagementAddOn not found, nothing to remove", "addon", addonName)
			return nil
		}
		return err
	}

	// Remove the config reference from the global placement
	configRemoved := false
	for i, placement := range addon.Spec.InstallStrategy.Placements {
		if placement.PlacementRef.Name == "global" && placement.PlacementRef.Namespace == "open-cluster-management-global-set" {
			var updatedConfigs []addonapiv1alpha1.AddOnConfig
			for _, config := range placement.Configs {
				if !(config.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
					config.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
					config.ConfigReferent.Name == "addon-ns-config" &&
					config.ConfigReferent.Namespace == "multicluster-engine") {
					updatedConfigs = append(updatedConfigs, config)
				} else {
					configRemoved = true
				}
			}
			addon.Spec.InstallStrategy.Placements[i].Configs = updatedConfigs
			break
		}
	}

	if !configRemoved {
		log.V(1).Info("ClusterManagementAddOn does not have discovery config reference to remove", "addon", addonName)
		return nil
	}

	log.Info("Removing discovery config reference from ClusterManagementAddOn", "addon", addonName)
	return r.Update(ctx, addon)
}

// updateSpecialAddOn updates a special ClusterManagementAddOn (application-manager or hypershift-addon) with Manual strategy and supportedConfigs
// These addons require special handling: they use Manual strategy with supportedConfigs when MCE import is enabled
func (r *DiscoveryConfigController) updateSpecialAddOn(ctx context.Context, log logr.Logger, addonName string) error {
	// Get the ClusterManagementAddOn
	addon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err := r.Get(ctx, client.ObjectKey{Name: addonName}, addon)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ClusterManagementAddOn not found, skipping update", "addon", addonName)
			return nil
		}
		return err
	}

	// Check if it already has the correct Manual strategy with supportedConfigs
	hasCorrectConfig := false
	if addon.Spec.InstallStrategy.Type == addonapiv1alpha1.AddonInstallStrategyManual {
		for _, supportedConfig := range addon.Spec.SupportedConfigs {
			if supportedConfig.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
				supportedConfig.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
				supportedConfig.DefaultConfig != nil &&
				supportedConfig.DefaultConfig.Name == "addon-ns-config" &&
				supportedConfig.DefaultConfig.Namespace == "multicluster-engine" {
				hasCorrectConfig = true
				break
			}
		}
	}

	if hasCorrectConfig {
		log.V(1).Info("ClusterManagementAddOn already has correct Manual configuration with supportedConfigs", "addon", addonName)
		return nil
	}

	// Configure addon with Manual strategy and supportedConfigs with defaultConfig reference
	addon.Spec.InstallStrategy.Type = addonapiv1alpha1.AddonInstallStrategyManual
	addon.Spec.InstallStrategy.Placements = nil // Clear placements when using Manual strategy

	// Set supportedConfigs with defaultConfig
	addon.Spec.SupportedConfigs = []addonapiv1alpha1.ConfigMeta{
		{
			ConfigGroupResource: addonapiv1alpha1.ConfigGroupResource{
				Group:    "addon.open-cluster-management.io",
				Resource: "addondeploymentconfigs",
			},
			DefaultConfig: &addonapiv1alpha1.ConfigReferent{
				Name:      "addon-ns-config",
				Namespace: "multicluster-engine",
			},
		},
	}

	log.Info("Updating ClusterManagementAddOn to use Manual strategy with supportedConfigs", "addon", addonName)
	return r.Update(ctx, addon)
}

// removeSpecialAddOnConfig reverts a special ClusterManagementAddOn (application-manager or hypershift-addon) to Manual strategy without supportedConfigs
func (r *DiscoveryConfigController) removeSpecialAddOnConfig(ctx context.Context, log logr.Logger, addonName string) error {
	// Get the ClusterManagementAddOn
	addon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err := r.Get(ctx, client.ObjectKey{Name: addonName}, addon)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("ClusterManagementAddOn not found, nothing to remove", "addon", addonName)
			return nil
		}
		return err
	}

	// Check if it already has Manual strategy without discovery supportedConfigs
	hasDiscoveryConfig := false
	if addon.Spec.InstallStrategy.Type == addonapiv1alpha1.AddonInstallStrategyManual {
		for _, supportedConfig := range addon.Spec.SupportedConfigs {
			if supportedConfig.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
				supportedConfig.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
				supportedConfig.DefaultConfig != nil &&
				supportedConfig.DefaultConfig.Name == "addon-ns-config" &&
				supportedConfig.DefaultConfig.Namespace == "multicluster-engine" {
				hasDiscoveryConfig = true
				break
			}
		}
	}

	if !hasDiscoveryConfig {
		log.V(1).Info("ClusterManagementAddOn already has Manual strategy without discovery config", "addon", addonName)
		return nil
	}

	// Remove discovery supportedConfigs while keeping Manual strategy
	addon.Spec.InstallStrategy.Type = addonapiv1alpha1.AddonInstallStrategyManual
	addon.Spec.InstallStrategy.Placements = nil // Clear placements when using Manual strategy

	// Remove discovery-related supportedConfigs
	var updatedSupportedConfigs []addonapiv1alpha1.ConfigMeta
	for _, supportedConfig := range addon.Spec.SupportedConfigs {
		if !(supportedConfig.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
			supportedConfig.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
			supportedConfig.DefaultConfig != nil &&
			supportedConfig.DefaultConfig.Name == "addon-ns-config" &&
			supportedConfig.DefaultConfig.Namespace == "multicluster-engine") {
			updatedSupportedConfigs = append(updatedSupportedConfigs, supportedConfig)
		}
	}
	addon.Spec.SupportedConfigs = updatedSupportedConfigs

	log.Info("Removing discovery supportedConfigs from ClusterManagementAddOn", "addon", addonName)
	return r.Update(ctx, addon)
}

// createOrUpdateKlusterletConfig creates or updates the mce-import-klusterlet-config KlusterletConfig
// as described in discovering_hostedclusters.md
func (r *DiscoveryConfigController) createOrUpdateKlusterletConfig(ctx context.Context, log logr.Logger) error {
	klusterletConfig := &unstructured.Unstructured{}
	klusterletConfig.SetAPIVersion("config.open-cluster-management.io/v1alpha1")
	klusterletConfig.SetKind("KlusterletConfig")
	klusterletConfig.SetName("mce-import-klusterlet-config")

	// Set the spec according to the documentation
	spec := map[string]interface{}{
		"installMode": map[string]interface{}{
			"type": "noOperator",
			"noOperator": map[string]interface{}{
				"postfix": "mce-import",
			},
		},
	}
	klusterletConfig.Object["spec"] = spec

	// Try to get existing KlusterletConfig
	existingConfig := &unstructured.Unstructured{}
	existingConfig.SetAPIVersion("config.open-cluster-management.io/v1alpha1")
	existingConfig.SetKind("KlusterletConfig")
	err := r.Get(ctx, client.ObjectKey{Name: "mce-import-klusterlet-config"}, existingConfig)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create new KlusterletConfig
			log.Info("Creating KlusterletConfig", "name", "mce-import-klusterlet-config")
			return r.Create(ctx, klusterletConfig)
		}
		return err
	}

	// Check if update is needed by comparing the spec
	existingSpec, found, err := unstructured.NestedMap(existingConfig.Object, "spec")
	if err != nil {
		return err
	}
	if !found {
		existingSpec = make(map[string]interface{})
	}

	// Compare specs to see if update is needed
	if !equalNestedMaps(existingSpec, spec) {
		existingConfig.Object["spec"] = spec
		log.Info("Updating KlusterletConfig", "name", "mce-import-klusterlet-config")
		return r.Update(ctx, existingConfig)
	}

	log.V(1).Info("KlusterletConfig is up to date", "name", "mce-import-klusterlet-config")
	return nil
}

// removeKlusterletConfig removes the mce-import-klusterlet-config KlusterletConfig when configureMceImport is disabled
func (r *DiscoveryConfigController) removeKlusterletConfig(ctx context.Context, log logr.Logger) error {
	klusterletConfig := &unstructured.Unstructured{}
	klusterletConfig.SetAPIVersion("config.open-cluster-management.io/v1alpha1")
	klusterletConfig.SetKind("KlusterletConfig")
	klusterletConfig.SetName("mce-import-klusterlet-config")

	err := r.Get(ctx, client.ObjectKey{Name: "mce-import-klusterlet-config"}, klusterletConfig)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Already doesn't exist, nothing to do
			log.V(1).Info("KlusterletConfig already doesn't exist", "name", "mce-import-klusterlet-config")
			return nil
		}
		return err
	}

	// Delete the KlusterletConfig
	log.Info("Removing KlusterletConfig", "name", "mce-import-klusterlet-config")
	return r.Delete(ctx, klusterletConfig)
}

// equalNestedMaps compares two nested maps for equality
func equalNestedMaps(map1, map2 map[string]interface{}) bool {
	if len(map1) != len(map2) {
		return false
	}
	for key, value1 := range map1 {
		value2, exists := map2[key]
		if !exists {
			return false
		}

		// Handle nested maps recursively
		if nestedMap1, ok := value1.(map[string]interface{}); ok {
			if nestedMap2, ok := value2.(map[string]interface{}); ok {
				if !equalNestedMaps(nestedMap1, nestedMap2) {
					return false
				}
			} else {
				return false
			}
		} else if fmt.Sprintf("%v", value1) != fmt.Sprintf("%v", value2) {
			return false
		}
	}
	return true
}

// isACMInstalled checks if ACM is installed by looking for advanced-cluster-management CSV
func (r *DiscoveryConfigController) isACMInstalled(ctx context.Context) (bool, error) {
	// Check for ACM ClusterServiceVersion directly (avoid namespace permission issues)
	csvList := &operatorsv1alpha1.ClusterServiceVersionList{}
	err := r.List(ctx, csvList)
	if err != nil {
		// If CSV CRD doesn't exist (non-OpenShift cluster), assume ACM is installed
		// since the hypershift addon is running, which implies cluster management is present
		errMsg := err.Error()
		if apierrors.IsNotFound(err) ||
			strings.Contains(errMsg, "no matches for kind") ||
			strings.Contains(errMsg, "no kind is registered for the type") ||
			strings.Contains(errMsg, "ClusterServiceVersionList") {
			r.Log.V(1).Info("ClusterServiceVersion CRD not found (likely non-OpenShift cluster), assuming ACM is installed")
			return true, nil
		}
		r.Log.Error(err, "Failed to list ClusterServiceVersions")
		return false, err
	}

	// Look for advanced-cluster-management CSV
	for _, csv := range csvList.Items {
		if strings.HasPrefix(csv.Name, "advanced-cluster-management") {
			r.Log.V(1).Info("Found ACM installation", "csv", csv.Name, "namespace", csv.Namespace)
			return true, nil
		}
	}

	r.Log.V(1).Info("ACM ClusterServiceVersion not found")
	return false, nil
}

// getConfigureMceImportValue returns the value of the configureMceImport custom variable
func getConfigureMceImportValue(addonConfig *addonapiv1alpha1.AddOnDeploymentConfig) string {
	for _, variable := range addonConfig.Spec.CustomizedVariables {
		if variable.Name == "configureMceImport" {
			return variable.Value
		}
	}
	return "not-set"
}

// hasConfigureMceImportEnabled checks if the configureMceImport custom variable is set to true
func hasConfigureMceImportEnabled(addonConfig *addonapiv1alpha1.AddOnDeploymentConfig) bool {
	return getConfigureMceImportValue(addonConfig) == "true"
}

// equalStringMaps compares two string maps for equality
func equalStringMaps(map1, map2 map[string]string) bool {
	if len(map1) != len(map2) {
		return false
	}
	for key, value1 := range map1 {
		if value2, exists := map2[key]; !exists || value1 != value2 {
			return false
		}
	}
	return true
}

// SetupWithManager sets up the controller with the Manager
func (r *DiscoveryConfigController) SetupWithManager(mgr ctrl.Manager) error {
	// Create predicate to filter events
	predicateFuncs := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// Only process hypershift-addon-deploy-config in multicluster-engine namespace
			if addonConfig, ok := e.Object.(*addonapiv1alpha1.AddOnDeploymentConfig); ok {
				return addonConfig.Name == "hypershift-addon-deploy-config" &&
					addonConfig.Namespace == "multicluster-engine"
				// Process regardless of mceHcpDiscovery value to handle both enabled/disabled states
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			// Only process hypershift-addon-deploy-config
			oldConfig, oldOk := e.ObjectOld.(*addonapiv1alpha1.AddOnDeploymentConfig)
			newConfig, newOk := e.ObjectNew.(*addonapiv1alpha1.AddOnDeploymentConfig)

			if !oldOk || !newOk ||
				newConfig.Name != "hypershift-addon-deploy-config" ||
				newConfig.Namespace != "multicluster-engine" {
				return false
			}

			// Check configureMceImport value changes
			oldValue := getConfigureMceImportValue(oldConfig)
			newValue := getConfigureMceImportValue(newConfig)

			// Reconcile if:
			// 1. configureMceImport value changed (enabled->disabled, disabled->enabled, etc.)
			// 2. There are spec/metadata changes (regardless of configureMceImport state)
			valueChanged := oldValue != newValue
			specChanged := oldConfig.Generation != newConfig.Generation
			metadataChanged := !equalStringMaps(oldConfig.Labels, newConfig.Labels) ||
				!equalStringMaps(oldConfig.Annotations, newConfig.Annotations)

			return valueChanged || specChanged || metadataChanged
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// Process deletion events for hypershift-addon-deploy-config
			if addonConfig, ok := e.Object.(*addonapiv1alpha1.AddOnDeploymentConfig); ok {
				return addonConfig.Name == "hypershift-addon-deploy-config" &&
					addonConfig.Namespace == "multicluster-engine"
			}
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&addonapiv1alpha1.AddOnDeploymentConfig{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		WithEventFilter(predicateFuncs).
		Complete(r)
}

// checkManagedClustersUsingKlusterletConfig checks if there are any ManagedClusters
// that have the annotation agent.open-cluster-management.io/klusterlet-config: mce-import-klusterlet-config
func (r *DiscoveryConfigController) checkManagedClustersUsingKlusterletConfig(ctx context.Context, log logr.Logger) (bool, []string, error) {
	const klusterletConfigAnnotation = "agent.open-cluster-management.io/klusterlet-config"
	const mceImportConfigName = "mce-import-klusterlet-config"

	// List all ManagedClusters
	managedClusterList := &clusterv1.ManagedClusterList{}
	err := r.List(ctx, managedClusterList)
	if err != nil {
		log.Error(err, "Failed to list ManagedClusters")
		return false, nil, err
	}

	var clustersUsingConfig []string
	for _, cluster := range managedClusterList.Items {
		if cluster.Annotations != nil {
			if configValue, exists := cluster.Annotations[klusterletConfigAnnotation]; exists {
				if configValue == mceImportConfigName {
					clustersUsingConfig = append(clustersUsingConfig, cluster.Name)
				}
			}
		}
	}

	if len(clustersUsingConfig) > 0 {
		log.Info("Found ManagedClusters using mce-import-klusterlet-config",
			"count", len(clustersUsingConfig), "clusters", clustersUsingConfig)
		return true, clustersUsingConfig, nil
	}

	log.V(1).Info("No ManagedClusters found using mce-import-klusterlet-config")
	return false, nil, nil
}
