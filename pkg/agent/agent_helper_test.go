package agent

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/go-logr/zapr"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_isACMInstalled_NonOpenShiftCluster(t *testing.T) {
	// Setup test environment
	testScheme := runtime.NewScheme()
	require.NoError(t, k8sscheme.AddToScheme(testScheme))
	require.NoError(t, addonv1alpha1.AddToScheme(testScheme))
	// Note: Intentionally NOT adding operatorsv1alpha1 to simulate non-OpenShift cluster

	// Create fake client without CSV scheme
	client := fake.NewClientBuilder().
		WithScheme(testScheme).
		Build()

	zapLog, _ := zap.NewDevelopment()
	controller := &agentController{
		hubClient:           client,
		spokeClient:         client,
		spokeUncachedClient: client,
		log:                 zapr.NewLogger(zapLog),
	}

	// Test ACM detection on non-OpenShift cluster
	acmInstalled, err := controller.isACMInstalled(context.TODO())
	assert.NoError(t, err, "should not error on non-OpenShift cluster")
	assert.True(t, acmInstalled, "should assume ACM is installed on non-OpenShift cluster")
}

// brokenRESTMapper simulates a non-NoMatch discovery failure from RESTMapping.
type brokenRESTMapper struct {
	err error
}

func (b brokenRESTMapper) KindFor(_ schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, b.err
}

func (b brokenRESTMapper) KindsFor(_ schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	return nil, b.err
}

func (b brokenRESTMapper) ResourceFor(_ schema.GroupVersionResource) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{}, b.err
}

func (b brokenRESTMapper) ResourcesFor(_ schema.GroupVersionResource) ([]schema.GroupVersionResource, error) {
	return nil, b.err
}

func (b brokenRESTMapper) RESTMapping(_ schema.GroupKind, _ ...string) (*meta.RESTMapping, error) {
	return nil, b.err
}

func (b brokenRESTMapper) RESTMappings(_ schema.GroupKind, _ ...string) ([]*meta.RESTMapping, error) {
	return nil, b.err
}

func (b brokenRESTMapper) ResourceSingularizer(_ string) (string, error) {
	return "", b.err
}

func Test_spokeHasManagedClusterAPI(t *testing.T) {
	gvk := clusterv1.SchemeGroupVersion.WithKind("ManagedCluster")

	withMC := meta.NewDefaultRESTMapper([]schema.GroupVersion{clusterv1.SchemeGroupVersion})
	withMC.Add(gvk, meta.RESTScopeRoot)
	hasAPI, err := spokeHasManagedClusterAPI(withMC)
	assert.NoError(t, err)
	assert.True(t, hasAPI, "mapper with ManagedCluster GVK should enable label-agent registration")

	withoutMC := meta.NewDefaultRESTMapper(nil)
	hasAPI, err = spokeHasManagedClusterAPI(withoutMC)
	assert.NoError(t, err)
	assert.False(t, hasAPI, "NoMatch means API absent; label agent should be skipped on ROSA-like spoke")

	hasAPI, err = spokeHasManagedClusterAPI(brokenRESTMapper{err: errors.New("discovery unavailable")})
	assert.Error(t, err, "transient discovery errors must fail startup instead of skipping label agent")
	assert.False(t, hasAPI)
}

type stubLabelAgentSetup struct {
	setupErr error
	called   bool
}

func (s *stubLabelAgentSetup) SetupWithManager(_ ctrl.Manager) error {
	s.called = true
	return s.setupErr
}

func Test_setupSpokeLabelAgent(t *testing.T) {
	gvk := clusterv1.SchemeGroupVersion.WithKind("ManagedCluster")
	withMC := meta.NewDefaultRESTMapper([]schema.GroupVersion{clusterv1.SchemeGroupVersion})
	withMC.Add(gvk, meta.RESTScopeRoot)
	withoutMC := meta.NewDefaultRESTMapper(nil)

	t.Run("skip when spoke lacks ManagedCluster API", func(t *testing.T) {
		setup := &stubLabelAgentSetup{}
		labelAgentSkipped, err := setupSpokeLabelAgent(withoutMC, setup, nil)
		assert.NoError(t, err)
		assert.True(t, labelAgentSkipped, "ROSA-like spoke should skip label agent without calling setup")
		assert.False(t, setup.called)
	})

	t.Run("fail when discovery errors", func(t *testing.T) {
		setup := &stubLabelAgentSetup{}
		labelAgentSkipped, err := setupSpokeLabelAgent(
			brokenRESTMapper{err: errors.New("discovery unavailable")}, setup, nil)
		assert.Error(t, err)
		assert.False(t, labelAgentSkipped)
		assert.False(t, setup.called)
	})

	t.Run("register when API is present", func(t *testing.T) {
		setup := &stubLabelAgentSetup{}
		labelAgentSkipped, err := setupSpokeLabelAgent(withMC, setup, nil)
		assert.NoError(t, err)
		assert.False(t, labelAgentSkipped)
		assert.True(t, setup.called, "label agent setup must run when ManagedCluster API exists")
	})

	t.Run("propagate label agent setup failure", func(t *testing.T) {
		setup := &stubLabelAgentSetup{setupErr: errors.New("controller build failed")}
		labelAgentSkipped, err := setupSpokeLabelAgent(withMC, setup, nil)
		assert.Error(t, err)
		assert.False(t, labelAgentSkipped)
		assert.True(t, setup.called)
	})
}

func Test_getSelfManagedClusterName(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)

	localClusterName := getSelfManagedClusterName(ctx, client, logger)
	assert.Equal(t, "", localClusterName)

	managedCluster := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: "mc1",
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err := client.Create(ctx, managedCluster)
	assert.Nil(t, err, "err nil when managedcluster is created successfully")

	localClusterName = getSelfManagedClusterName(ctx, client, logger)
	assert.Equal(t, "", localClusterName)

	localCluster := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:   "mc2",
			Labels: map[string]string{"local-cluster": "true"},
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err = client.Create(ctx, localCluster)
	assert.Nil(t, err, "err nil when local cluster managedcluster is created successfully")

	localClusterName = getSelfManagedClusterName(ctx, client, logger)
	assert.Equal(t, "mc2", localClusterName)

	// If there are more than one local clusters??
	// Return the first local cluster
	localCluster2 := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:   "mc3",
			Labels: map[string]string{"local-cluster": "true"},
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err = client.Create(ctx, localCluster2)
	assert.Nil(t, err, "err nil when local cluster managedcluster is created successfully")

	localClusterName = getSelfManagedClusterName(ctx, client, logger)
	assert.Equal(t, "mc2", localClusterName)

}

func Test_createMCEImportConfig(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)

	controller := &agentController{
		hubClient:           client,
		spokeClient:         client,
		spokeUncachedClient: client,
		log:                 logger,
	}

	// Test case 1: CONFIGURE_MCE_IMPORT not set (should skip)
	os.Unsetenv("CONFIGURE_MCE_IMPORT")
	err := controller.createMCEImportConfig(ctx)
	assert.Nil(t, err, "should not error when env var is not set")

	// Verify no AddOnDeploymentConfig was created
	addonConfig := &addonv1alpha1.AddOnDeploymentConfig{}
	err = client.Get(ctx, types.NamespacedName{
		Name:      "disable-sync-labels-to-clusterclaims",
		Namespace: "multicluster-engine",
	}, addonConfig)
	assert.NotNil(t, err, "AddOnDeploymentConfig should not exist when env var is not set")

	// Test case 2: CONFIGURE_MCE_IMPORT set to false (should skip)
	os.Setenv("CONFIGURE_MCE_IMPORT", "false")
	err = controller.createMCEImportConfig(ctx)
	assert.Nil(t, err, "should not error when env var is false")

	// Test case 3: CONFIGURE_MCE_IMPORT set to true (should create config)
	// First create a work-manager ClusterManagementAddOn for testing
	workManagerAddon := &addonv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "work-manager",
		},
		Spec: addonv1alpha1.ClusterManagementAddOnSpec{
			InstallStrategy: addonv1alpha1.InstallStrategy{
				Type: addonv1alpha1.AddonInstallStrategyPlacements,
				Placements: []addonv1alpha1.PlacementStrategy{
					{
						PlacementRef: addonv1alpha1.PlacementRef{
							Name:      "global",
							Namespace: "open-cluster-management-global-set",
						},
						Configs: []addonv1alpha1.AddOnConfig{},
					},
				},
			},
		},
	}
	err = client.Create(ctx, workManagerAddon)
	assert.Nil(t, err, "should create work-manager ClusterManagementAddOn for testing")

	os.Setenv("CONFIGURE_MCE_IMPORT", "true")
	err = controller.createMCEImportConfig(ctx)
	assert.Nil(t, err, "should not error when creating AddOnDeploymentConfig")

	// Verify AddOnDeploymentConfig was created with correct values
	err = client.Get(ctx, types.NamespacedName{
		Name:      "disable-sync-labels-to-clusterclaims",
		Namespace: "multicluster-engine",
	}, addonConfig)
	assert.Nil(t, err, "AddOnDeploymentConfig should exist")
	assert.Equal(t, "disable-sync-labels-to-clusterclaims", addonConfig.Name)
	assert.Equal(t, "multicluster-engine", addonConfig.Namespace)
	assert.Len(t, addonConfig.Spec.CustomizedVariables, 1)
	assert.Equal(t, "enableSyncLabelsToClusterClaims", addonConfig.Spec.CustomizedVariables[0].Name)
	assert.Equal(t, "false", addonConfig.Spec.CustomizedVariables[0].Value)

	// Verify work-manager ClusterManagementAddOn was updated with config reference
	updatedWorkManagerAddon := &addonv1alpha1.ClusterManagementAddOn{}
	err = client.Get(ctx, types.NamespacedName{Name: "work-manager"}, updatedWorkManagerAddon)
	assert.Nil(t, err, "should get updated work-manager ClusterManagementAddOn")

	// Check that the config reference was added
	found := false
	for _, placement := range updatedWorkManagerAddon.Spec.InstallStrategy.Placements {
		if placement.PlacementRef.Name == "global" && placement.PlacementRef.Namespace == "open-cluster-management-global-set" {
			for _, config := range placement.Configs {
				if config.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
					config.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
					config.ConfigReferent.Name == "disable-sync-labels-to-clusterclaims" &&
					config.ConfigReferent.Namespace == "multicluster-engine" {
					found = true
					break
				}
			}
			break
		}
	}
	assert.True(t, found, "work-manager ClusterManagementAddOn should have disable-sync-labels-to-clusterclaims config reference")

	// Test case 4: Run again to test update functionality
	err = controller.createMCEImportConfig(ctx)
	assert.Nil(t, err, "should not error when updating existing AddOnDeploymentConfig")

	// Test case 5: CONFIGURE_MCE_IMPORT set to true but ACM is installed (should skip)
	// Create ACM namespace to simulate ACM installation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	err = client.Create(ctx, acmNamespace)
	assert.Nil(t, err, "should create ACM namespace for testing")

	// Create ACM CSV to simulate ACM installation
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}
	err = client.Create(ctx, acmCSV)
	assert.Nil(t, err, "should create ACM CSV for testing")

	// Clear any existing config to test fresh creation
	err = client.Delete(ctx, addonConfig)
	if err != nil && !apierrors.IsNotFound(err) {
		assert.Nil(t, err, "should delete existing config for ACM test")
	}

	// Clear work-manager addon to test fresh creation
	err = client.Delete(ctx, workManagerAddon)
	if err != nil && !apierrors.IsNotFound(err) {
		assert.Nil(t, err, "should delete existing work-manager addon for ACM test")
	}

	// Now run createMCEImportConfig - should skip because ACM is installed
	err = controller.createMCEImportConfig(ctx)
	assert.Nil(t, err, "should not error when ACM is installed")

	// Verify no AddOnDeploymentConfig was created (because ACM is installed)
	freshAddonConfig := &addonv1alpha1.AddOnDeploymentConfig{}
	err = client.Get(ctx, types.NamespacedName{
		Name:      "disable-sync-labels-to-clusterclaims",
		Namespace: "multicluster-engine",
	}, freshAddonConfig)
	assert.NotNil(t, err, "AddOnDeploymentConfig should not be created when ACM is installed")
	assert.True(t, apierrors.IsNotFound(err), "should get NotFound error when ACM is installed")

	// Clean up
	os.Unsetenv("CONFIGURE_MCE_IMPORT")
}
