package manager

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

const (
	// MCEDiscoveryControllerName is the name of the controller
	MCEDiscoveryControllerName = "mce-discovery-controller"

	// MCEClusterLabel identifies MCE clusters
	MCEClusterLabel = "cluster.open-cluster-management.io/mce-cluster"

	// DiscoverySetupLabel indicates discovery setup is completed
	DiscoverySetupLabel = "hypershift.open-cluster-management.io/discovery-setup"

	// Environment variables for configuration
	EnvAddonNamespace     = "ADDON_NAMESPACE"
	EnvACMNamespace       = "ACM_NAMESPACE"
	EnvPolicyNamespace    = "POLICY_NAMESPACE"
	EnvEnableMCEDiscovery = "ENABLE_MCE_DISCOVERY"

	// Default configuration
	DefaultAddonNamespace  = "open-cluster-management-agent-addon-discovery"
	DefaultACMNamespace    = "multicluster-engine"
	DefaultPolicyNamespace = "open-cluster-management-global-set"
)

// MCEDiscoveryController automates the setup of MCE hosted cluster discovery
type MCEDiscoveryController struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger

	// Configuration
	AddonNamespace  string
	ACMNamespace    string
	PolicyNamespace string
}

// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=addon.open-cluster-management.io,resources=managedclusteraddons,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=addon.open-cluster-management.io,resources=addondeploymentconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=addon.open-cluster-management.io,resources=clustermanagementaddons,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles the reconciliation of MCE clusters for discovery setup
func (r *MCEDiscoveryController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("managedcluster", req.NamespacedName)

	// Get the ManagedCluster
	managedCluster := &clusterv1.ManagedCluster{}
	if err := r.Get(ctx, req.NamespacedName, managedCluster); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("ManagedCluster not found, may have been deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get ManagedCluster")
		return ctrl.Result{}, err
	}

	// Check if this is an MCE cluster
	if !r.isMCECluster(managedCluster) {
		log.V(2).Info("Not an MCE cluster, skipping")
		return ctrl.Result{}, nil
	}

	// Check if discovery setup is already completed
	if r.isDiscoverySetupCompleted(managedCluster) {
		log.V(1).Info("Discovery setup already completed for this cluster")
		return ctrl.Result{}, nil
	}

	log.Info("Setting up discovery for MCE cluster")

	// Ensure global configuration is set up
	if err := r.ensureGlobalConfiguration(ctx); err != nil {
		log.Error(err, "Failed to ensure global configuration")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, err
	}

	// Setup hypershift addon for the MCE cluster
	if err := r.setupHypershiftAddon(ctx, managedCluster); err != nil {
		log.Error(err, "Failed to setup hypershift addon")
		return ctrl.Result{RequeueAfter: time.Minute * 2}, err
	}

	// Mark discovery setup as completed
	if err := r.markDiscoverySetupCompleted(ctx, managedCluster); err != nil {
		log.Error(err, "Failed to mark discovery setup as completed")
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	log.Info("Discovery setup completed for MCE cluster")
	return ctrl.Result{}, nil
}

// isMCECluster determines if a managed cluster is an MCE cluster
func (r *MCEDiscoveryController) isMCECluster(mc *clusterv1.ManagedCluster) bool {
	// Check for explicit MCE cluster label
	if value, exists := mc.Labels[MCEClusterLabel]; exists && value == "true" {
		return true
	}

	// Check for vendor label indicating MCE/OpenShift
	if vendor, exists := mc.Labels["vendor"]; exists &&
		(strings.Contains(strings.ToLower(vendor), "mce") ||
			strings.Contains(strings.ToLower(vendor), "multicluster")) {
		return true
	}

	// Check for cluster name patterns that might indicate MCE
	if strings.Contains(strings.ToLower(mc.Name), "mce") {
		return true
	}

	return false
}

// isDiscoverySetupCompleted checks if discovery setup is already completed
func (r *MCEDiscoveryController) isDiscoverySetupCompleted(mc *clusterv1.ManagedCluster) bool {
	value, exists := mc.Labels[DiscoverySetupLabel]
	return exists && value == "true"
}

// ensureGlobalConfiguration ensures the global ACM configuration is set up
func (r *MCEDiscoveryController) ensureGlobalConfiguration(ctx context.Context) error {
	// Ensure AddOnDeploymentConfig for addon namespace
	if err := r.ensureAddonDeploymentConfig(ctx); err != nil {
		return fmt.Errorf("failed to ensure addon deployment config: %w", err)
	}

	// Ensure auto-import policy (simplified version)
	if err := r.ensureAutoImportPolicy(ctx); err != nil {
		return fmt.Errorf("failed to ensure auto-import policy: %w", err)
	}

	return nil
}

// ensureAddonDeploymentConfig creates the addon deployment config if it doesn't exist
func (r *MCEDiscoveryController) ensureAddonDeploymentConfig(ctx context.Context) error {
	config := &addonv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "addon-ns-config",
			Namespace: r.ACMNamespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, config, func() error {
		config.Spec.AgentInstallNamespace = r.AddonNamespace
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create/update addon deployment config: %w", err)
	}

	return nil
}

// ensureAutoImportPolicy creates a basic auto-import policy if it doesn't exist
func (r *MCEDiscoveryController) ensureAutoImportPolicy(ctx context.Context) error {
	// For now, we'll skip creating the complex policy and let the shell scripts handle it
	// This could be enhanced later with the full policy template when the policy API is available
	r.Log.Info("Auto-import policy creation skipped - use automation scripts for full policy setup")
	return nil
}

// setupHypershiftAddon sets up the hypershift addon for the MCE cluster
func (r *MCEDiscoveryController) setupHypershiftAddon(ctx context.Context, mc *clusterv1.ManagedCluster) error {
	// Create ManagedClusterAddOn for hypershift-addon
	addon := &addonv1alpha1.ManagedClusterAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon",
			Namespace: mc.Name,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, addon, func() error {
		addon.Spec.InstallNamespace = r.AddonNamespace
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create/update hypershift addon: %w", err)
	}

	return nil
}

// markDiscoverySetupCompleted marks the discovery setup as completed
func (r *MCEDiscoveryController) markDiscoverySetupCompleted(ctx context.Context, mc *clusterv1.ManagedCluster) error {
	if mc.Labels == nil {
		mc.Labels = make(map[string]string)
	}
	mc.Labels[DiscoverySetupLabel] = "true"

	return r.Update(ctx, mc)
}

// SetupWithManager sets up the controller with the Manager
func (r *MCEDiscoveryController) SetupWithManager(mgr ctrl.Manager) error {
	// Set default values from environment variables if not configured
	if r.AddonNamespace == "" {
		r.AddonNamespace = getEnvOrDefault(EnvAddonNamespace, DefaultAddonNamespace)
	}
	if r.ACMNamespace == "" {
		r.ACMNamespace = getEnvOrDefault(EnvACMNamespace, DefaultACMNamespace)
	}
	if r.PolicyNamespace == "" {
		r.PolicyNamespace = getEnvOrDefault(EnvPolicyNamespace, DefaultPolicyNamespace)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1.ManagedCluster{}).
		Owns(&addonv1alpha1.ManagedClusterAddOn{}).
		Complete(r)
}

// NewMCEDiscoveryController creates a new MCE Discovery Controller
func NewMCEDiscoveryController(client client.Client, scheme *runtime.Scheme, log logr.Logger) *MCEDiscoveryController {
	return &MCEDiscoveryController{
		Client: client,
		Scheme: scheme,
		Log:    log.WithName(MCEDiscoveryControllerName),

		AddonNamespace:  getEnvOrDefault(EnvAddonNamespace, DefaultAddonNamespace),
		ACMNamespace:    getEnvOrDefault(EnvACMNamespace, DefaultACMNamespace),
		PolicyNamespace: getEnvOrDefault(EnvPolicyNamespace, DefaultPolicyNamespace),
	}
}

// getEnvOrDefault returns environment variable value or default if not set
func getEnvOrDefault(envVar, defaultValue string) string {
	if value := os.Getenv(envVar); value != "" {
		return value
	}
	return defaultValue
}

// IsMCEDiscoveryEnabled checks if MCE discovery should be enabled
func IsMCEDiscoveryEnabled() bool {
	return !strings.EqualFold(os.Getenv(EnvEnableMCEDiscovery), "false")
}
