package manager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"
)

func TestDiscoveryConfigController_Reconcile(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Setup test logger
	testLogger := zap.New(zap.UseDevMode(true))

	tests := []struct {
		name                   string
		addonConfig            *addonapiv1alpha1.AddOnDeploymentConfig
		existingConfigMap      *corev1.ConfigMap
		expectedConfigMapData  map[string]string
		expectedReconcileError bool
		expectedLogMessage     string
	}{
		{
			name: "configureMceImport enabled - creates new ConfigMap",
			addonConfig: &addonapiv1alpha1.AddOnDeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config",
					Namespace: "multicluster-engine",
				},
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					AgentInstallNamespace: "test-namespace",
					CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
						{Name: "configureMceImport", Value: "true"},
						{Name: "testVar", Value: "testValue"},
					},
				},
			},
			expectedConfigMapData: map[string]string{
				"config-name":             "hypershift-addon-deploy-config",
				"config-namespace":        "multicluster-engine",
				"install-namespace":       "open-cluster-management-agent-addon-discovery",
				"configureMceImport":      "true",
				"import-enabled":          "true",
				"var-configureMceImport":  "true",
				"var-disableMetrics":      "true",
				"var-disableHOManagement": "true",
				"var-testVar":             "testValue",
			},
		},
		{
			name: "configureMceImport disabled - creates new ConfigMap",
			addonConfig: &addonapiv1alpha1.AddOnDeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config",
					Namespace: "multicluster-engine",
				},
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					AgentInstallNamespace: "test-namespace",
					CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
						{Name: "configureMceImport", Value: "false"},
					},
				},
			},
			expectedConfigMapData: map[string]string{
				"config-name":            "hypershift-addon-deploy-config",
				"config-namespace":       "multicluster-engine",
				"install-namespace":      "open-cluster-management-agent-addon",
				"configureMceImport":     "false",
				"import-enabled":         "false",
				"var-configureMceImport": "false",
			},
		},
		{
			name: "configureMceImport not set - creates ConfigMap but takes no action",
			addonConfig: &addonapiv1alpha1.AddOnDeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config",
					Namespace: "multicluster-engine",
				},
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					AgentInstallNamespace: "test-namespace",
					CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
						{Name: "otherVar", Value: "otherValue"},
					},
				},
			},
			expectedConfigMapData: map[string]string{
				"config-name":        "hypershift-addon-deploy-config",
				"config-namespace":   "multicluster-engine",
				"install-namespace":  "test-namespace",
				"configureMceImport": "not-set",
				"import-enabled":     "false",
				"var-otherVar":       "otherValue",
			},
		},
		{
			name: "configureMceImport invalid value - creates ConfigMap but takes no action",
			addonConfig: &addonapiv1alpha1.AddOnDeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config",
					Namespace: "multicluster-engine",
				},
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					AgentInstallNamespace: "test-namespace",
					CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
						{Name: "configureMceImport", Value: "invalid"},
						{Name: "otherVar", Value: "otherValue"},
					},
				},
			},
			expectedConfigMapData: map[string]string{
				"config-name":            "hypershift-addon-deploy-config",
				"config-namespace":       "multicluster-engine",
				"install-namespace":      "test-namespace",
				"configureMceImport":     "invalid",
				"import-enabled":         "false",
				"var-configureMceImport": "invalid",
				"var-otherVar":           "otherValue",
			},
		},
		{
			name: "updates existing ConfigMap when data changes",
			addonConfig: &addonapiv1alpha1.AddOnDeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config",
					Namespace: "multicluster-engine",
				},
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					AgentInstallNamespace: "updated-namespace",
					CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
						{Name: "configureMceImport", Value: "true"},
					},
				},
			},
			existingConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config-info",
					Namespace: "test-operator-namespace",
				},
				Data: map[string]string{
					"config-name":        "hypershift-addon-deploy-config",
					"config-namespace":   "multicluster-engine",
					"install-namespace":  "old-namespace",
					"configureMceImport": "false",
					"import-enabled":     "false",
					"created-at":         "2023-01-01T00:00:00Z",
				},
			},
			expectedConfigMapData: map[string]string{
				"config-name":             "hypershift-addon-deploy-config",
				"config-namespace":        "multicluster-engine",
				"install-namespace":       "open-cluster-management-agent-addon-discovery",
				"configureMceImport":      "true",
				"import-enabled":          "true",
				"var-configureMceImport":  "true",
				"var-disableMetrics":      "true",
				"var-disableHOManagement": "true",
				"created-at":              "2023-01-01T00:00:00Z", // Should preserve created-at
			},
		},
		{
			name: "skips update when ConfigMap is up to date",
			addonConfig: &addonapiv1alpha1.AddOnDeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config",
					Namespace: "multicluster-engine",
				},
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					AgentInstallNamespace: "open-cluster-management-agent-addon-discovery",
					CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
						{Name: "configureMceImport", Value: "true"},
						{Name: "disableMetrics", Value: "true"},
						{Name: "disableHOManagement", Value: "true"},
					},
				},
			},
			existingConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config-info",
					Namespace: "test-operator-namespace",
				},
				Data: map[string]string{
					"config-name":             "hypershift-addon-deploy-config",
					"config-namespace":        "multicluster-engine",
					"install-namespace":       "open-cluster-management-agent-addon-discovery",
					"configureMceImport":      "true",
					"import-enabled":          "true",
					"var-configureMceImport":  "true",
					"var-disableMetrics":      "true",
					"var-disableHOManagement": "true",
					"created-at":              "2023-01-01T00:00:00Z",
					"last-updated":            "2023-01-01T12:00:00Z",
				},
			},
			expectedConfigMapData: map[string]string{
				"config-name":             "hypershift-addon-deploy-config",
				"config-namespace":        "multicluster-engine",
				"install-namespace":       "open-cluster-management-agent-addon-discovery",
				"configureMceImport":      "true",
				"import-enabled":          "true",
				"var-configureMceImport":  "true",
				"var-disableMetrics":      "true",
				"var-disableHOManagement": "true",
				"created-at":              "2023-01-01T00:00:00Z",
				"last-updated":            "2023-01-01T12:00:00Z", // Should not update last-updated
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup fake client with initial objects
			var initialObjects []client.Object
			initialObjects = append(initialObjects, tt.addonConfig)
			if tt.existingConfigMap != nil {
				initialObjects = append(initialObjects, tt.existingConfigMap)
			}

			// Add ACM namespace and CSV to simulate ACM installation
			acmNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "open-cluster-management",
				},
			}
			acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "advanced-cluster-management.v2.9.0",
					Namespace: "open-cluster-management",
				},
			}
			initialObjects = append(initialObjects, acmNamespace, acmCSV)

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(initialObjects...).
				Build()

			// Create controller
			controller := &DiscoveryConfigController{
				Client:            fakeClient,
				Log:               testLogger,
				Scheme:            testScheme,
				OperatorNamespace: "test-operator-namespace",
			}

			// Execute reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.addonConfig.Name,
					Namespace: tt.addonConfig.Namespace,
				},
			}

			result, err := controller.Reconcile(context.TODO(), req)

			// Verify reconcile result
			if tt.expectedReconcileError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, ctrl.Result{}, result)

			// Verify ConfigMap was created/updated correctly
			configMap := &corev1.ConfigMap{}
			err = fakeClient.Get(context.TODO(), types.NamespacedName{
				Name:      "hypershift-addon-deploy-config-info",
				Namespace: "test-operator-namespace",
			}, configMap)
			assert.NoError(t, err)

			// Verify expected data (excluding timestamps for new ConfigMaps)
			for key, expectedValue := range tt.expectedConfigMapData {
				actualValue, exists := configMap.Data[key]
				assert.True(t, exists, "Expected key %s to exist in ConfigMap", key)
				assert.Equal(t, expectedValue, actualValue, "Expected value for key %s", key)
			}

			// Verify timestamps are present for new ConfigMaps
			if tt.existingConfigMap == nil {
				assert.Contains(t, configMap.Data, "created-at")
				assert.NotEmpty(t, configMap.Data["created-at"])
			}
		})
	}
}

func TestDiscoveryConfigController_SkipsNonHypershiftConfig(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create non-hypershift addon config
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)

	// Should succeed but not create ConfigMap
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify no ConfigMap was created
	configMap := &corev1.ConfigMap{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config-info",
		Namespace: "test-operator-namespace",
	}, configMap)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDiscoveryConfigController_SkipsWrongNamespace(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config in wrong namespace
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "wrong-namespace",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)

	// Should succeed but not create ConfigMap
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify no ConfigMap was created
	configMap := &corev1.ConfigMap{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config-info",
		Namespace: "test-operator-namespace",
	}, configMap)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDiscoveryConfigController_SkipsInvalidConfigureMceImportValues(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	testCases := []struct {
		name                    string
		configureMceImportValue string
	}{
		{
			name:                    "configureMceImport not set",
			configureMceImportValue: "", // This will result in "not-set" from getConfigureMceImportValue
		},
		{
			name:                    "configureMceImport invalid value",
			configureMceImportValue: "invalid",
		},
		{
			name:                    "configureMceImport empty string",
			configureMceImportValue: "",
		},
		{
			name:                    "configureMceImport custom value",
			configureMceImportValue: "custom",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var customizedVariables []addonapiv1alpha1.CustomizedVariable
			if tc.configureMceImportValue != "" {
				customizedVariables = []addonapiv1alpha1.CustomizedVariable{
					{Name: "configureMceImport", Value: tc.configureMceImportValue},
				}
			}

			// Create addon config with invalid/missing configureMceImport
			addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config",
					Namespace: "multicluster-engine",
				},
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					CustomizedVariables: customizedVariables,
				},
			}

			// Add ACM installation simulation
			acmNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "open-cluster-management",
				},
			}
			acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "advanced-cluster-management.v2.9.0",
					Namespace: "open-cluster-management",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(addonConfig, acmNamespace, acmCSV).
				Build()

			controller := &DiscoveryConfigController{
				Client:            fakeClient,
				Log:               zap.New(zap.UseDevMode(true)),
				Scheme:            testScheme,
				OperatorNamespace: "test-operator-namespace",
			}

			// Execute reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      addonConfig.Name,
					Namespace: addonConfig.Namespace,
				},
			}

			result, err := controller.Reconcile(context.TODO(), req)
			assert.NoError(t, err)
			assert.Equal(t, ctrl.Result{}, result)

			// Verify NO discovery addon config was created (because configureMceImport is not "true" or "false")
			discoveryConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
			err = fakeClient.Get(context.TODO(), types.NamespacedName{
				Name:      "addon-ns-config",
				Namespace: "multicluster-engine",
			}, discoveryConfig)
			assert.True(t, apierrors.IsNotFound(err), "Discovery addon config should not be created for invalid configureMceImport value")

			// Verify ConfigMap was still created for monitoring purposes
			configMap := &corev1.ConfigMap{}
			err = fakeClient.Get(context.TODO(), types.NamespacedName{
				Name:      "hypershift-addon-deploy-config-info",
				Namespace: "test-operator-namespace",
			}, configMap)
			assert.NoError(t, err, "ConfigMap should still be created for monitoring")
		})
	}
}

func TestDiscoveryConfigController_CreatesDiscoveryAddonConfig(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create ACM namespace to simulate ACM installation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}

	// Create ACM CSV to simulate ACM installation
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify discovery addon config was created
	discoveryConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "addon-ns-config",
		Namespace: "multicluster-engine",
	}, discoveryConfig)
	assert.NoError(t, err)
	assert.Equal(t, "open-cluster-management-agent-addon-discovery", discoveryConfig.Spec.AgentInstallNamespace)
}

func TestDiscoveryConfigController_RemovesDiscoveryAddonConfig(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport disabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
		},
	}

	// Create existing discovery config that should be removed
	existingDiscoveryConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "addon-ns-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			AgentInstallNamespace: "open-cluster-management-agent-addon-discovery",
		},
	}

	// Create ACM namespace to simulate ACM installation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}

	// Create ACM CSV to simulate ACM installation
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, existingDiscoveryConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify discovery addon config was removed
	discoveryConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "addon-ns-config",
		Namespace: "multicluster-engine",
	}, discoveryConfig)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDiscoveryConfigController_SkipsWhenACMNotInstalled(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create fake client WITHOUT ACM namespace or CSV (simulating no ACM installation)
	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify no discovery addon config was created (because ACM is not installed)
	discoveryConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "addon-ns-config",
		Namespace: "multicluster-engine",
	}, discoveryConfig)
	assert.True(t, apierrors.IsNotFound(err))

	// Verify no ConfigMap was created
	configMap := &corev1.ConfigMap{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config-info",
		Namespace: "test-operator-namespace",
	}, configMap)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDiscoveryConfigController_ProcessesWhenACMInstalled(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create ACM namespace to simulate ACM installation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}

	// Create ACM CSV to simulate ACM installation
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify discovery addon config was created (because ACM is installed)
	discoveryConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "addon-ns-config",
		Namespace: "multicluster-engine",
	}, discoveryConfig)
	assert.NoError(t, err)
	assert.Equal(t, "open-cluster-management-agent-addon-discovery", discoveryConfig.Spec.AgentInstallNamespace)

	// Verify ConfigMap was created
	configMap := &corev1.ConfigMap{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config-info",
		Namespace: "test-operator-namespace",
	}, configMap)
	assert.NoError(t, err)
	assert.Equal(t, "true", configMap.Data["configureMceImport"])
}

func TestDiscoveryConfigController_UpdatesWorkManagerAddon(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create existing Work Manager ClusterManagementAddOn without discovery config
	workManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "work-manager",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyPlacements,
				Placements: []addonapiv1alpha1.PlacementStrategy{
					{
						PlacementRef: addonapiv1alpha1.PlacementRef{
							Name:      "global",
							Namespace: "open-cluster-management-global-set",
						},
						RolloutStrategy: clusterv1alpha1.RolloutStrategy{
							Type: clusterv1alpha1.All,
						},
						Configs: []addonapiv1alpha1.AddOnConfig{}, // Initially empty
					},
				},
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, workManagerAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify Work Manager addon was updated with discovery config reference
	updatedWorkManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "work-manager"}, updatedWorkManagerAddon)
	assert.NoError(t, err)

	// Check that the config was added to the global placement
	found := false
	for _, placement := range updatedWorkManagerAddon.Spec.InstallStrategy.Placements {
		if placement.PlacementRef.Name == "global" && placement.PlacementRef.Namespace == "open-cluster-management-global-set" {
			for _, config := range placement.Configs {
				if config.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
					config.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
					config.ConfigReferent.Name == "addon-ns-config" &&
					config.ConfigReferent.Namespace == "multicluster-engine" {
					found = true
					break
				}
			}
			break
		}
	}
	assert.True(t, found, "Expected addon-ns-config reference to be added to Work Manager addon")
}

func TestDiscoveryConfigController_RemovesWorkManagerAddonConfig(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport disabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
		},
	}

	// Create Work Manager ClusterManagementAddOn WITH discovery config (to be removed)
	workManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "work-manager",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyPlacements,
				Placements: []addonapiv1alpha1.PlacementStrategy{
					{
						PlacementRef: addonapiv1alpha1.PlacementRef{
							Name:      "global",
							Namespace: "open-cluster-management-global-set",
						},
						RolloutStrategy: clusterv1alpha1.RolloutStrategy{
							Type: clusterv1alpha1.All,
						},
						Configs: []addonapiv1alpha1.AddOnConfig{
							{
								ConfigGroupResource: addonapiv1alpha1.ConfigGroupResource{
									Group:    "addon.open-cluster-management.io",
									Resource: "addondeploymentconfigs",
								},
								ConfigReferent: addonapiv1alpha1.ConfigReferent{
									Name:      "addon-ns-config",
									Namespace: "multicluster-engine",
								},
							},
						},
					},
				},
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, workManagerAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify Work Manager addon config reference was removed
	updatedWorkManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "work-manager"}, updatedWorkManagerAddon)
	assert.NoError(t, err)

	// Check that the config was removed from the global placement
	found := false
	for _, placement := range updatedWorkManagerAddon.Spec.InstallStrategy.Placements {
		if placement.PlacementRef.Name == "global" && placement.PlacementRef.Namespace == "open-cluster-management-global-set" {
			for _, config := range placement.Configs {
				if config.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
					config.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
					config.ConfigReferent.Name == "addon-ns-config" &&
					config.ConfigReferent.Namespace == "multicluster-engine" {
					found = true
					break
				}
			}
			break
		}
	}
	assert.False(t, found, "Expected addon-ns-config reference to be removed from Work Manager addon")
}

func TestDiscoveryConfigController_UpdatesMultipleAddons(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create all three ClusterManagementAddOns without discovery config
	addonsToCreate := []string{"work-manager", "managed-serviceaccount", "cluster-proxy"}
	var initialObjects []client.Object
	initialObjects = append(initialObjects, addonConfig)

	for _, addonName := range addonsToCreate {
		addon := &addonapiv1alpha1.ClusterManagementAddOn{
			ObjectMeta: metav1.ObjectMeta{
				Name: addonName,
			},
			Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
				InstallStrategy: addonapiv1alpha1.InstallStrategy{
					Type: addonapiv1alpha1.AddonInstallStrategyPlacements,
					Placements: []addonapiv1alpha1.PlacementStrategy{
						{
							PlacementRef: addonapiv1alpha1.PlacementRef{
								Name:      "global",
								Namespace: "open-cluster-management-global-set",
							},
							RolloutStrategy: clusterv1alpha1.RolloutStrategy{
								Type: clusterv1alpha1.All,
							},
							Configs: []addonapiv1alpha1.AddOnConfig{}, // Initially empty
						},
					},
				},
			},
		}
		initialObjects = append(initialObjects, addon)
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}
	initialObjects = append(initialObjects, acmNamespace, acmCSV)

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(initialObjects...).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify all three addons were updated with discovery config reference
	for _, addonName := range addonsToCreate {
		updatedAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
		err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: addonName}, updatedAddon)
		assert.NoError(t, err)

		// Check that the config was added to the global placement
		found := false
		for _, placement := range updatedAddon.Spec.InstallStrategy.Placements {
			if placement.PlacementRef.Name == "global" && placement.PlacementRef.Namespace == "open-cluster-management-global-set" {
				for _, config := range placement.Configs {
					if config.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
						config.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
						config.ConfigReferent.Name == "addon-ns-config" &&
						config.ConfigReferent.Namespace == "multicluster-engine" {
						found = true
						break
					}
				}
				break
			}
		}
		assert.True(t, found, "Expected addon-ns-config reference to be added to %s addon", addonName)
	}
}

func TestDiscoveryConfigController_RemovesMultipleAddonConfigs(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport disabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
		},
	}

	// Create all three ClusterManagementAddOns WITH discovery config (to be removed)
	addonsToCreate := []string{"work-manager", "managed-serviceaccount", "cluster-proxy"}
	var initialObjects []client.Object
	initialObjects = append(initialObjects, addonConfig)

	for _, addonName := range addonsToCreate {
		addon := &addonapiv1alpha1.ClusterManagementAddOn{
			ObjectMeta: metav1.ObjectMeta{
				Name: addonName,
			},
			Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
				InstallStrategy: addonapiv1alpha1.InstallStrategy{
					Type: addonapiv1alpha1.AddonInstallStrategyPlacements,
					Placements: []addonapiv1alpha1.PlacementStrategy{
						{
							PlacementRef: addonapiv1alpha1.PlacementRef{
								Name:      "global",
								Namespace: "open-cluster-management-global-set",
							},
							RolloutStrategy: clusterv1alpha1.RolloutStrategy{
								Type: clusterv1alpha1.All,
							},
							Configs: []addonapiv1alpha1.AddOnConfig{
								{
									ConfigGroupResource: addonapiv1alpha1.ConfigGroupResource{
										Group:    "addon.open-cluster-management.io",
										Resource: "addondeploymentconfigs",
									},
									ConfigReferent: addonapiv1alpha1.ConfigReferent{
										Name:      "addon-ns-config",
										Namespace: "multicluster-engine",
									},
								},
							},
						},
					},
				},
			},
		}
		initialObjects = append(initialObjects, addon)
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}
	initialObjects = append(initialObjects, acmNamespace, acmCSV)

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(initialObjects...).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify all three addons had their config references removed
	for _, addonName := range addonsToCreate {
		updatedAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
		err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: addonName}, updatedAddon)
		assert.NoError(t, err)

		// Check that the config was removed from the global placement
		found := false
		for _, placement := range updatedAddon.Spec.InstallStrategy.Placements {
			if placement.PlacementRef.Name == "global" && placement.PlacementRef.Namespace == "open-cluster-management-global-set" {
				for _, config := range placement.Configs {
					if config.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
						config.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
						config.ConfigReferent.Name == "addon-ns-config" &&
						config.ConfigReferent.Namespace == "multicluster-engine" {
						found = true
						break
					}
				}
				break
			}
		}
		assert.False(t, found, "Expected addon-ns-config reference to be removed from %s addon", addonName)
	}
}

func TestGetConfigureMceImportValue(t *testing.T) {
	tests := []struct {
		name      string
		variables []addonapiv1alpha1.CustomizedVariable
		expected  string
	}{
		{
			name: "configureMceImport set to true",
			variables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
			expected: "true",
		},
		{
			name: "configureMceImport set to false",
			variables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
			expected: "false",
		},
		{
			name: "configureMceImport not present",
			variables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "otherVar", Value: "otherValue"},
			},
			expected: "not-set",
		},
		{
			name:      "no variables",
			variables: []addonapiv1alpha1.CustomizedVariable{},
			expected:  "not-set",
		},
		{
			name: "configureMceImport with custom value",
			variables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "custom"},
			},
			expected: "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					CustomizedVariables: tt.variables,
				},
			}

			result := getConfigureMceImportValue(addonConfig)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasConfigureMceImportEnabled(t *testing.T) {
	tests := []struct {
		name      string
		variables []addonapiv1alpha1.CustomizedVariable
		expected  bool
	}{
		{
			name: "configureMceImport set to true",
			variables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
			expected: true,
		},
		{
			name: "configureMceImport set to false",
			variables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
			expected: false,
		},
		{
			name: "configureMceImport not present",
			variables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "otherVar", Value: "otherValue"},
			},
			expected: false,
		},
		{
			name: "configureMceImport with custom value",
			variables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "custom"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					CustomizedVariables: tt.variables,
				},
			}

			result := hasConfigureMceImportEnabled(addonConfig)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDiscoveryConfigController_PredicateFunctions(t *testing.T) {
	// Test CreateFunc
	t.Run("CreateFunc", func(t *testing.T) {
		controller := &DiscoveryConfigController{}
		predicates := controller.getPredicateFuncs()

		tests := []struct {
			name     string
			object   client.Object
			expected bool
		}{
			{
				name: "correct name and namespace",
				object: &addonapiv1alpha1.AddOnDeploymentConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hypershift-addon-deploy-config",
						Namespace: "multicluster-engine",
					},
				},
				expected: true,
			},
			{
				name: "wrong name",
				object: &addonapiv1alpha1.AddOnDeploymentConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-config",
						Namespace: "multicluster-engine",
					},
				},
				expected: false,
			},
			{
				name: "wrong namespace",
				object: &addonapiv1alpha1.AddOnDeploymentConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hypershift-addon-deploy-config",
						Namespace: "other-namespace",
					},
				},
				expected: false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := predicates.CreateFunc(event.CreateEvent{Object: tt.object})
				assert.Equal(t, tt.expected, result)
			})
		}
	})

	// Test UpdateFunc
	t.Run("UpdateFunc", func(t *testing.T) {
		controller := &DiscoveryConfigController{}
		predicates := controller.getPredicateFuncs()

		oldConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "hypershift-addon-deploy-config",
				Namespace:  "multicluster-engine",
				Generation: 1,
			},
			Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
				CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
					{Name: "configureMceImport", Value: "false"},
				},
			},
		}

		tests := []struct {
			name      string
			newConfig *addonapiv1alpha1.AddOnDeploymentConfig
			expected  bool
		}{
			{
				name: "configureMceImport value changed",
				newConfig: &addonapiv1alpha1.AddOnDeploymentConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "hypershift-addon-deploy-config",
						Namespace:  "multicluster-engine",
						Generation: 1,
					},
					Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
						CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
							{Name: "configureMceImport", Value: "true"},
						},
					},
				},
				expected: true,
			},
			{
				name: "generation changed",
				newConfig: &addonapiv1alpha1.AddOnDeploymentConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "hypershift-addon-deploy-config",
						Namespace:  "multicluster-engine",
						Generation: 2,
					},
					Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
						CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
							{Name: "configureMceImport", Value: "false"},
						},
					},
				},
				expected: true,
			},
			{
				name: "no changes",
				newConfig: &addonapiv1alpha1.AddOnDeploymentConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "hypershift-addon-deploy-config",
						Namespace:  "multicluster-engine",
						Generation: 1,
					},
					Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
						CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
							{Name: "configureMceImport", Value: "false"},
						},
					},
				},
				expected: false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := predicates.UpdateFunc(event.UpdateEvent{
					ObjectOld: oldConfig,
					ObjectNew: tt.newConfig,
				})
				assert.Equal(t, tt.expected, result)
			})
		}
	})
}

// Helper method to get predicate functions for testing
func (r *DiscoveryConfigController) getPredicateFuncs() *predicateFuncs {
	return &predicateFuncs{
		CreateFunc: func(e event.CreateEvent) bool {
			if addonConfig, ok := e.Object.(*addonapiv1alpha1.AddOnDeploymentConfig); ok {
				return addonConfig.Name == "hypershift-addon-deploy-config" &&
					addonConfig.Namespace == "multicluster-engine"
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldConfig, oldOk := e.ObjectOld.(*addonapiv1alpha1.AddOnDeploymentConfig)
			newConfig, newOk := e.ObjectNew.(*addonapiv1alpha1.AddOnDeploymentConfig)

			if !oldOk || !newOk ||
				newConfig.Name != "hypershift-addon-deploy-config" ||
				newConfig.Namespace != "multicluster-engine" {
				return false
			}

			oldValue := getConfigureMceImportValue(oldConfig)
			newValue := getConfigureMceImportValue(newConfig)

			valueChanged := oldValue != newValue
			specChanged := oldConfig.Generation != newConfig.Generation
			metadataChanged := !equalStringMaps(oldConfig.Labels, newConfig.Labels) ||
				!equalStringMaps(oldConfig.Annotations, newConfig.Annotations)

			return valueChanged || specChanged || metadataChanged
		},
	}
}

// Helper struct for testing predicates
type predicateFuncs struct {
	CreateFunc func(event.CreateEvent) bool
	UpdateFunc func(event.UpdateEvent) bool
}

func TestDiscoveryConfigController_UpdatesApplicationManagerAddon(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create existing application-manager ClusterManagementAddOn with Manual strategy
	applicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "application-manager",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			AddOnMeta: addonapiv1alpha1.AddOnMeta{
				DisplayName: "application-manager",
			},
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyManual,
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, applicationManagerAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify application-manager addon was updated to Manual strategy with supportedConfigs
	updatedApplicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "application-manager"}, updatedApplicationManagerAddon)
	assert.NoError(t, err)

	// Check that the strategy remains Manual
	assert.Equal(t, addonapiv1alpha1.AddonInstallStrategyManual, updatedApplicationManagerAddon.Spec.InstallStrategy.Type)

	// Check that placements are cleared
	assert.Nil(t, updatedApplicationManagerAddon.Spec.InstallStrategy.Placements)

	// Check that the supportedConfigs was added with defaultConfig reference
	require.Len(t, updatedApplicationManagerAddon.Spec.SupportedConfigs, 1)
	supportedConfig := updatedApplicationManagerAddon.Spec.SupportedConfigs[0]

	assert.Equal(t, "addon.open-cluster-management.io", supportedConfig.ConfigGroupResource.Group)
	assert.Equal(t, "addondeploymentconfigs", supportedConfig.ConfigGroupResource.Resource)
	require.NotNil(t, supportedConfig.DefaultConfig)
	assert.Equal(t, "addon-ns-config", supportedConfig.DefaultConfig.Name)
	assert.Equal(t, "multicluster-engine", supportedConfig.DefaultConfig.Namespace)
}

func TestDiscoveryConfigController_ApplicationManagerAddonIdempotent(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create application-manager ClusterManagementAddOn already configured with Manual strategy and supportedConfigs
	applicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "application-manager",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			AddOnMeta: addonapiv1alpha1.AddOnMeta{
				DisplayName: "application-manager",
			},
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyManual,
			},
			SupportedConfigs: []addonapiv1alpha1.ConfigMeta{
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
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, applicationManagerAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify application-manager addon configuration remains unchanged (idempotent)
	updatedApplicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "application-manager"}, updatedApplicationManagerAddon)
	assert.NoError(t, err)

	// Verify the configuration is still correct and unchanged
	assert.Equal(t, addonapiv1alpha1.AddonInstallStrategyManual, updatedApplicationManagerAddon.Spec.InstallStrategy.Type)
	assert.Nil(t, updatedApplicationManagerAddon.Spec.InstallStrategy.Placements)

	require.Len(t, updatedApplicationManagerAddon.Spec.SupportedConfigs, 1)
	supportedConfig := updatedApplicationManagerAddon.Spec.SupportedConfigs[0]
	require.NotNil(t, supportedConfig.DefaultConfig)
	assert.Equal(t, "addon-ns-config", supportedConfig.DefaultConfig.Name)
	assert.Equal(t, "multicluster-engine", supportedConfig.DefaultConfig.Namespace)
}

func TestDiscoveryConfigController_RemovesApplicationManagerAddonConfig(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport disabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
		},
	}

	// Create application-manager ClusterManagementAddOn WITH Manual strategy and supportedConfigs (to be removed)
	applicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "application-manager",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			AddOnMeta: addonapiv1alpha1.AddOnMeta{
				DisplayName: "application-manager",
			},
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyManual,
			},
			SupportedConfigs: []addonapiv1alpha1.ConfigMeta{
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
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, applicationManagerAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify application-manager addon supportedConfigs were removed
	updatedApplicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "application-manager"}, updatedApplicationManagerAddon)
	assert.NoError(t, err)

	// Check that the strategy remains Manual
	assert.Equal(t, addonapiv1alpha1.AddonInstallStrategyManual, updatedApplicationManagerAddon.Spec.InstallStrategy.Type)

	// Check that placements are cleared
	assert.Nil(t, updatedApplicationManagerAddon.Spec.InstallStrategy.Placements)

	// Check that supportedConfigs were cleared
	assert.Empty(t, updatedApplicationManagerAddon.Spec.SupportedConfigs)
}

func TestDiscoveryConfigController_ApplicationManagerRemovalIdempotent(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport disabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
		},
	}

	// Create application-manager ClusterManagementAddOn already in Manual strategy
	applicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "application-manager",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			AddOnMeta: addonapiv1alpha1.AddOnMeta{
				DisplayName: "application-manager",
			},
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyManual,
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, applicationManagerAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify application-manager addon configuration remains unchanged (idempotent)
	updatedApplicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "application-manager"}, updatedApplicationManagerAddon)
	assert.NoError(t, err)

	// Verify the configuration is still Manual and unchanged
	assert.Equal(t, addonapiv1alpha1.AddonInstallStrategyManual, updatedApplicationManagerAddon.Spec.InstallStrategy.Type)
	assert.Nil(t, updatedApplicationManagerAddon.Spec.InstallStrategy.Placements)
	assert.Empty(t, updatedApplicationManagerAddon.Spec.SupportedConfigs)
}

func TestDiscoveryConfigController_ApplicationManagerNotFound(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Add ACM installation simulation (but no application-manager addon)
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	// Should succeed even when application-manager addon doesn't exist
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify no application-manager addon was created (controller should skip gracefully)
	applicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "application-manager"}, applicationManagerAddon)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDiscoveryConfigController_ApplicationManagerWithOtherAddons(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create all addons including application-manager
	addonsToCreate := []string{"work-manager", "managed-serviceaccount", "cluster-proxy", "application-manager"}
	var initialObjects []client.Object
	initialObjects = append(initialObjects, addonConfig)

	for _, addonName := range addonsToCreate {
		var addon *addonapiv1alpha1.ClusterManagementAddOn

		if addonName == "application-manager" {
			// Create application-manager with Manual strategy initially
			addon = &addonapiv1alpha1.ClusterManagementAddOn{
				ObjectMeta: metav1.ObjectMeta{
					Name: addonName,
				},
				Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
					AddOnMeta: addonapiv1alpha1.AddOnMeta{
						DisplayName: addonName,
					},
					InstallStrategy: addonapiv1alpha1.InstallStrategy{
						Type: addonapiv1alpha1.AddonInstallStrategyManual,
					},
				},
			}
		} else {
			// Create other addons with Placements strategy but no discovery config
			addon = &addonapiv1alpha1.ClusterManagementAddOn{
				ObjectMeta: metav1.ObjectMeta{
					Name: addonName,
				},
				Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
					InstallStrategy: addonapiv1alpha1.InstallStrategy{
						Type: addonapiv1alpha1.AddonInstallStrategyPlacements,
						Placements: []addonapiv1alpha1.PlacementStrategy{
							{
								PlacementRef: addonapiv1alpha1.PlacementRef{
									Name:      "global",
									Namespace: "open-cluster-management-global-set",
								},
								RolloutStrategy: clusterv1alpha1.RolloutStrategy{
									Type: clusterv1alpha1.All,
								},
								Configs: []addonapiv1alpha1.AddOnConfig{}, // Initially empty
							},
						},
					},
				},
			}
		}
		initialObjects = append(initialObjects, addon)
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}
	initialObjects = append(initialObjects, acmNamespace, acmCSV)

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(initialObjects...).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify all standard addons were updated with discovery config reference
	standardAddons := []string{"work-manager", "managed-serviceaccount", "cluster-proxy"}
	for _, addonName := range standardAddons {
		updatedAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
		err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: addonName}, updatedAddon)
		assert.NoError(t, err)

		// Check that the config was added to the global placement
		found := false
		for _, placement := range updatedAddon.Spec.InstallStrategy.Placements {
			if placement.PlacementRef.Name == "global" && placement.PlacementRef.Namespace == "open-cluster-management-global-set" {
				for _, config := range placement.Configs {
					if config.ConfigGroupResource.Group == "addon.open-cluster-management.io" &&
						config.ConfigGroupResource.Resource == "addondeploymentconfigs" &&
						config.ConfigReferent.Name == "addon-ns-config" &&
						config.ConfigReferent.Namespace == "multicluster-engine" {
						found = true
						break
					}
				}
				break
			}
		}
		assert.True(t, found, "Expected addon-ns-config reference to be added to %s addon", addonName)
	}

	// Verify application-manager was updated to Manual strategy with supportedConfigs
	updatedApplicationManagerAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "application-manager"}, updatedApplicationManagerAddon)
	assert.NoError(t, err)

	// Check that application-manager was configured with Manual strategy and supportedConfigs
	assert.Equal(t, addonapiv1alpha1.AddonInstallStrategyManual, updatedApplicationManagerAddon.Spec.InstallStrategy.Type)
	assert.Nil(t, updatedApplicationManagerAddon.Spec.InstallStrategy.Placements)

	require.Len(t, updatedApplicationManagerAddon.Spec.SupportedConfigs, 1)
	supportedConfig := updatedApplicationManagerAddon.Spec.SupportedConfigs[0]
	require.NotNil(t, supportedConfig.DefaultConfig)
	assert.Equal(t, "addon-ns-config", supportedConfig.DefaultConfig.Name)
	assert.Equal(t, "multicluster-engine", supportedConfig.DefaultConfig.Namespace)
}

func TestDiscoveryConfigController_UpdatesHypershiftAddon(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create existing hypershift-addon ClusterManagementAddOn with Manual strategy
	hypershiftAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-addon",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			AddOnMeta: addonapiv1alpha1.AddOnMeta{
				DisplayName: "hypershift-addon",
			},
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyManual,
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, hypershiftAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify hypershift-addon was updated to Manual strategy with supportedConfigs
	updatedHypershiftAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "hypershift-addon"}, updatedHypershiftAddon)
	assert.NoError(t, err)

	// Check that the strategy remains Manual
	assert.Equal(t, addonapiv1alpha1.AddonInstallStrategyManual, updatedHypershiftAddon.Spec.InstallStrategy.Type)

	// Check that placements are cleared
	assert.Nil(t, updatedHypershiftAddon.Spec.InstallStrategy.Placements)

	// Check that the supportedConfigs was NOT added (hypershift-addon is excluded from updates)
	assert.Len(t, updatedHypershiftAddon.Spec.SupportedConfigs, 0, "hypershift-addon should not be updated with discovery config")
}

func TestDiscoveryConfigController_HypershiftAddonIdempotent(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Create hypershift-addon ClusterManagementAddOn already configured with Manual strategy and supportedConfigs
	hypershiftAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-addon",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			AddOnMeta: addonapiv1alpha1.AddOnMeta{
				DisplayName: "hypershift-addon",
			},
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyManual,
			},
			SupportedConfigs: []addonapiv1alpha1.ConfigMeta{
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
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, hypershiftAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify hypershift-addon configuration remains unchanged (idempotent)
	updatedHypershiftAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "hypershift-addon"}, updatedHypershiftAddon)
	assert.NoError(t, err)

	// Verify the configuration is still correct and unchanged
	assert.Equal(t, addonapiv1alpha1.AddonInstallStrategyManual, updatedHypershiftAddon.Spec.InstallStrategy.Type)
	assert.Nil(t, updatedHypershiftAddon.Spec.InstallStrategy.Placements)

	require.Len(t, updatedHypershiftAddon.Spec.SupportedConfigs, 1)
	supportedConfig := updatedHypershiftAddon.Spec.SupportedConfigs[0]
	require.NotNil(t, supportedConfig.DefaultConfig)
	assert.Equal(t, "addon-ns-config", supportedConfig.DefaultConfig.Name)
	assert.Equal(t, "multicluster-engine", supportedConfig.DefaultConfig.Namespace)
}

func TestDiscoveryConfigController_RemovesHypershiftAddonConfig(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport disabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
		},
	}

	// Create hypershift-addon ClusterManagementAddOn WITH Manual strategy and supportedConfigs (to be removed)
	hypershiftAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-addon",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			AddOnMeta: addonapiv1alpha1.AddOnMeta{
				DisplayName: "hypershift-addon",
			},
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyManual,
			},
			SupportedConfigs: []addonapiv1alpha1.ConfigMeta{
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
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, hypershiftAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify hypershift-addon supportedConfigs were removed
	updatedHypershiftAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "hypershift-addon"}, updatedHypershiftAddon)
	assert.NoError(t, err)

	// Check that the strategy remains Manual
	assert.Equal(t, addonapiv1alpha1.AddonInstallStrategyManual, updatedHypershiftAddon.Spec.InstallStrategy.Type)

	// Check that placements are cleared
	assert.Nil(t, updatedHypershiftAddon.Spec.InstallStrategy.Placements)

	// Check that supportedConfigs were NOT cleared (hypershift-addon is excluded from removal)
	assert.Len(t, updatedHypershiftAddon.Spec.SupportedConfigs, 1, "hypershift-addon should not be modified during removal")
}

func TestDiscoveryConfigController_HypershiftRemovalIdempotent(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport disabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
		},
	}

	// Create hypershift-addon ClusterManagementAddOn already in Manual strategy
	hypershiftAddon := &addonapiv1alpha1.ClusterManagementAddOn{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-addon",
		},
		Spec: addonapiv1alpha1.ClusterManagementAddOnSpec{
			AddOnMeta: addonapiv1alpha1.AddOnMeta{
				DisplayName: "hypershift-addon",
			},
			InstallStrategy: addonapiv1alpha1.InstallStrategy{
				Type: addonapiv1alpha1.AddonInstallStrategyManual,
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, hypershiftAddon, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify hypershift-addon configuration remains unchanged (idempotent)
	updatedHypershiftAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "hypershift-addon"}, updatedHypershiftAddon)
	assert.NoError(t, err)

	// Verify the configuration is still Manual and unchanged
	assert.Equal(t, addonapiv1alpha1.AddonInstallStrategyManual, updatedHypershiftAddon.Spec.InstallStrategy.Type)
	assert.Nil(t, updatedHypershiftAddon.Spec.InstallStrategy.Placements)
	assert.Empty(t, updatedHypershiftAddon.Spec.SupportedConfigs)
}

func TestDiscoveryConfigController_EnsuresHypershiftAddonCustomizedVariables(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled but missing required variables
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
				{Name: "existingVar", Value: "existingValue"},
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify hypershift addon config was updated with required variables
	updatedAddonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config",
		Namespace: "multicluster-engine",
	}, updatedAddonConfig)
	assert.NoError(t, err)

	// Check that required variables were added
	variableMap := make(map[string]string)
	for _, variable := range updatedAddonConfig.Spec.CustomizedVariables {
		variableMap[variable.Name] = variable.Value
	}

	// Verify required variables are present
	assert.Equal(t, "true", variableMap["disableMetrics"])
	assert.Equal(t, "true", variableMap["disableHOManagement"])
	assert.Equal(t, "true", variableMap["configureMceImport"])
	assert.Equal(t, "existingValue", variableMap["existingVar"])
}

func TestDiscoveryConfigController_HypershiftAddonCustomizedVariablesIdempotent(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled and all required variables already present
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
				{Name: "disableMetrics", Value: "true"},
				{Name: "disableHOManagement", Value: "true"},
				{Name: "existingVar", Value: "existingValue"},
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify hypershift addon config remains unchanged (idempotent)
	updatedAddonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config",
		Namespace: "multicluster-engine",
	}, updatedAddonConfig)
	assert.NoError(t, err)

	// Check that variables are still correct and count hasn't changed
	assert.Len(t, updatedAddonConfig.Spec.CustomizedVariables, 4)

	variableMap := make(map[string]string)
	for _, variable := range updatedAddonConfig.Spec.CustomizedVariables {
		variableMap[variable.Name] = variable.Value
	}

	// Verify all variables are still present and correct
	assert.Equal(t, "true", variableMap["disableMetrics"])
	assert.Equal(t, "true", variableMap["disableHOManagement"])
	assert.Equal(t, "true", variableMap["configureMceImport"])
	assert.Equal(t, "existingValue", variableMap["existingVar"])
}

func TestDiscoveryConfigController_CorrectsMissingHypershiftAddonVariables(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled and incorrect/missing required variables
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
				{Name: "disableMetrics", Value: "false"}, // Wrong value - should be corrected
				// disableHOManagement is missing - should be added
				{Name: "existingVar", Value: "existingValue"},
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify hypershift addon config was corrected
	updatedAddonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config",
		Namespace: "multicluster-engine",
	}, updatedAddonConfig)
	assert.NoError(t, err)

	// Check that variables were corrected/added
	variableMap := make(map[string]string)
	for _, variable := range updatedAddonConfig.Spec.CustomizedVariables {
		variableMap[variable.Name] = variable.Value
	}

	// Verify required variables are present with correct values
	assert.Equal(t, "true", variableMap["disableMetrics"])      // Should be corrected from "false" to "true"
	assert.Equal(t, "true", variableMap["disableHOManagement"]) // Should be added
	assert.Equal(t, "true", variableMap["configureMceImport"])
	assert.Equal(t, "existingValue", variableMap["existingVar"]) // Should remain unchanged
}

func TestDiscoveryConfigController_RemovesHypershiftAddonCustomizedVariables(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport disabled and variables that should be removed
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
				{Name: "disableMetrics", Value: "true"},       // Should be removed
				{Name: "disableHOManagement", Value: "true"},  // Should be removed
				{Name: "keepThisVar", Value: "keepThisValue"}, // Should be kept
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify hypershift addon config had variables removed
	updatedAddonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config",
		Namespace: "multicluster-engine",
	}, updatedAddonConfig)
	assert.NoError(t, err)

	// Check that required variables were removed but others were kept
	variableMap := make(map[string]string)
	for _, variable := range updatedAddonConfig.Spec.CustomizedVariables {
		variableMap[variable.Name] = variable.Value
	}

	// Verify required variables are removed
	_, hasDisableMetrics := variableMap["disableMetrics"]
	_, hasDisableHOManagement := variableMap["disableHOManagement"]
	assert.False(t, hasDisableMetrics, "disableMetrics should be removed")
	assert.False(t, hasDisableHOManagement, "disableHOManagement should be removed")

	// Verify other variables are kept
	assert.Equal(t, "false", variableMap["configureMceImport"])
	assert.Equal(t, "keepThisValue", variableMap["keepThisVar"])
	assert.Len(t, updatedAddonConfig.Spec.CustomizedVariables, 2) // Only configureMceImport and keepThisVar should remain
}

func TestDiscoveryConfigController_RemovalHypershiftAddonCustomizedVariablesIdempotent(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport disabled and no variables to remove
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
				{Name: "keepThisVar", Value: "keepThisValue"},
			},
		},
	}

	// Add ACM installation simulation
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify hypershift addon config remains unchanged (idempotent)
	updatedAddonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config",
		Namespace: "multicluster-engine",
	}, updatedAddonConfig)
	assert.NoError(t, err)

	// Check that variables are still correct and count hasn't changed
	assert.Len(t, updatedAddonConfig.Spec.CustomizedVariables, 2)

	variableMap := make(map[string]string)
	for _, variable := range updatedAddonConfig.Spec.CustomizedVariables {
		variableMap[variable.Name] = variable.Value
	}

	// Verify variables are still present and correct
	assert.Equal(t, "false", variableMap["configureMceImport"])
	assert.Equal(t, "keepThisValue", variableMap["keepThisVar"])

	// Verify the variables we would remove are not present
	_, hasDisableMetrics := variableMap["disableMetrics"]
	_, hasDisableHOManagement := variableMap["disableHOManagement"]
	assert.False(t, hasDisableMetrics)
	assert.False(t, hasDisableHOManagement)
}

func TestDiscoveryConfigController_HypershiftAddonNotFound(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create addon config with configureMceImport enabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "true"},
			},
		},
	}

	// Add ACM installation simulation (but no hypershift-addon)
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, acmNamespace, acmCSV).
		Build()

	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		Scheme:            testScheme,
		OperatorNamespace: "test-operator-namespace",
	}

	// Execute reconcile
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      addonConfig.Name,
			Namespace: addonConfig.Namespace,
		},
	}

	result, err := controller.Reconcile(context.TODO(), req)
	// Should succeed even when hypershift-addon doesn't exist
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify no hypershift-addon was created (controller should skip gracefully)
	hypershiftAddon := &addonapiv1alpha1.ClusterManagementAddOn{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "hypershift-addon"}, hypershiftAddon)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDiscoveryConfigController_KlusterletConfig(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Setup test logger
	testLogger := zap.New(zap.UseDevMode(true))

	tests := []struct {
		name                           string
		configureMceImportValue        string
		existingKlusterletConfig       *unstructured.Unstructured
		expectedKlusterletConfigExists bool
		expectedKlusterletConfigSpec   map[string]interface{}
	}{
		{
			name:                           "configureMceImport enabled - creates KlusterletConfig",
			configureMceImportValue:        "true",
			expectedKlusterletConfigExists: true,
			expectedKlusterletConfigSpec: map[string]interface{}{
				"installMode": map[string]interface{}{
					"type": "noOperator",
					"noOperator": map[string]interface{}{
						"postfix": "mce-import",
					},
				},
			},
		},
		{
			name:                    "configureMceImport disabled - removes KlusterletConfig",
			configureMceImportValue: "false",
			existingKlusterletConfig: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "config.open-cluster-management.io/v1alpha1",
					"kind":       "KlusterletConfig",
					"metadata": map[string]interface{}{
						"name": "mce-import-klusterlet-config",
					},
					"spec": map[string]interface{}{
						"installMode": map[string]interface{}{
							"type": "noOperator",
							"noOperator": map[string]interface{}{
								"postfix": "mce-import",
							},
						},
					},
				},
			},
			expectedKlusterletConfigExists: false,
		},
		{
			name:                    "configureMceImport enabled - updates existing KlusterletConfig",
			configureMceImportValue: "true",
			existingKlusterletConfig: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "config.open-cluster-management.io/v1alpha1",
					"kind":       "KlusterletConfig",
					"metadata": map[string]interface{}{
						"name": "mce-import-klusterlet-config",
					},
					"spec": map[string]interface{}{
						"installMode": map[string]interface{}{
							"type": "different",
						},
					},
				},
			},
			expectedKlusterletConfigExists: true,
			expectedKlusterletConfigSpec: map[string]interface{}{
				"installMode": map[string]interface{}{
					"type": "noOperator",
					"noOperator": map[string]interface{}{
						"postfix": "mce-import",
					},
				},
			},
		},
		{
			name:                    "configureMceImport enabled - no update needed when spec is correct",
			configureMceImportValue: "true",
			existingKlusterletConfig: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "config.open-cluster-management.io/v1alpha1",
					"kind":       "KlusterletConfig",
					"metadata": map[string]interface{}{
						"name": "mce-import-klusterlet-config",
					},
					"spec": map[string]interface{}{
						"installMode": map[string]interface{}{
							"type": "noOperator",
							"noOperator": map[string]interface{}{
								"postfix": "mce-import",
							},
						},
					},
				},
			},
			expectedKlusterletConfigExists: true,
			expectedKlusterletConfigSpec: map[string]interface{}{
				"installMode": map[string]interface{}{
					"type": "noOperator",
					"noOperator": map[string]interface{}{
						"postfix": "mce-import",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create addon config with configureMceImport value
			addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hypershift-addon-deploy-config",
					Namespace: "multicluster-engine",
				},
				Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
					AgentInstallNamespace: "test-namespace",
					CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
						{Name: "configureMceImport", Value: tt.configureMceImportValue},
					},
				},
			}

			// Setup initial objects
			initialObjects := []client.Object{addonConfig}

			// Add existing KlusterletConfig if provided
			if tt.existingKlusterletConfig != nil {
				initialObjects = append(initialObjects, tt.existingKlusterletConfig)
			}

			// Add ACM installation objects to pass ACM check
			acmNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "open-cluster-management",
				},
			}
			acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "advanced-cluster-management.v2.10.0",
					Namespace: "open-cluster-management",
				},
			}
			initialObjects = append(initialObjects, acmNamespace, acmCSV)

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(initialObjects...).
				Build()

			// Create controller
			controller := &DiscoveryConfigController{
				Client:            fakeClient,
				Log:               testLogger,
				Scheme:            testScheme,
				OperatorNamespace: "test-operator-namespace",
			}

			// Execute reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      addonConfig.Name,
					Namespace: addonConfig.Namespace,
				},
			}

			result, err := controller.Reconcile(context.TODO(), req)
			assert.NoError(t, err)
			assert.Equal(t, ctrl.Result{}, result)

			// Verify KlusterletConfig state
			klusterletConfig := &unstructured.Unstructured{}
			klusterletConfig.SetAPIVersion("config.open-cluster-management.io/v1alpha1")
			klusterletConfig.SetKind("KlusterletConfig")
			err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "mce-import-klusterlet-config"}, klusterletConfig)

			if tt.expectedKlusterletConfigExists {
				assert.NoError(t, err, "Expected KlusterletConfig to exist")

				// Verify the spec matches expected
				actualSpec, found, err := unstructured.NestedMap(klusterletConfig.Object, "spec")
				assert.NoError(t, err)
				assert.True(t, found, "Expected spec to be present")
				assert.Equal(t, tt.expectedKlusterletConfigSpec, actualSpec, "KlusterletConfig spec should match expected")
			} else {
				assert.True(t, apierrors.IsNotFound(err), "Expected KlusterletConfig to not exist")
			}
		})
	}
}

func TestDiscoveryConfigController_KlusterletConfig_Functions(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))

	testLogger := zap.New(zap.UseDevMode(true))

	t.Run("createOrUpdateKlusterletConfig - creates new KlusterletConfig", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			Build()

		controller := &DiscoveryConfigController{
			Client:            fakeClient,
			Log:               testLogger,
			Scheme:            testScheme,
			OperatorNamespace: "test-operator-namespace",
		}

		err := controller.createOrUpdateKlusterletConfig(context.TODO(), testLogger)
		assert.NoError(t, err)

		// Verify KlusterletConfig was created
		klusterletConfig := &unstructured.Unstructured{}
		klusterletConfig.SetAPIVersion("config.open-cluster-management.io/v1alpha1")
		klusterletConfig.SetKind("KlusterletConfig")
		err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "mce-import-klusterlet-config"}, klusterletConfig)
		assert.NoError(t, err)

		// Verify spec
		expectedSpec := map[string]interface{}{
			"installMode": map[string]interface{}{
				"type": "noOperator",
				"noOperator": map[string]interface{}{
					"postfix": "mce-import",
				},
			},
		}
		actualSpec, found, err := unstructured.NestedMap(klusterletConfig.Object, "spec")
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, expectedSpec, actualSpec)
	})

	t.Run("createOrUpdateKlusterletConfig - updates existing KlusterletConfig", func(t *testing.T) {
		existingConfig := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "config.open-cluster-management.io/v1alpha1",
				"kind":       "KlusterletConfig",
				"metadata": map[string]interface{}{
					"name": "mce-import-klusterlet-config",
				},
				"spec": map[string]interface{}{
					"installMode": map[string]interface{}{
						"type": "different",
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(existingConfig).
			Build()

		controller := &DiscoveryConfigController{
			Client:            fakeClient,
			Log:               testLogger,
			Scheme:            testScheme,
			OperatorNamespace: "test-operator-namespace",
		}

		err := controller.createOrUpdateKlusterletConfig(context.TODO(), testLogger)
		assert.NoError(t, err)

		// Verify KlusterletConfig was updated
		klusterletConfig := &unstructured.Unstructured{}
		klusterletConfig.SetAPIVersion("config.open-cluster-management.io/v1alpha1")
		klusterletConfig.SetKind("KlusterletConfig")
		err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "mce-import-klusterlet-config"}, klusterletConfig)
		assert.NoError(t, err)

		// Verify spec was updated
		expectedSpec := map[string]interface{}{
			"installMode": map[string]interface{}{
				"type": "noOperator",
				"noOperator": map[string]interface{}{
					"postfix": "mce-import",
				},
			},
		}
		actualSpec, found, err := unstructured.NestedMap(klusterletConfig.Object, "spec")
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, expectedSpec, actualSpec)
	})

	t.Run("createOrUpdateKlusterletConfig - no update needed when spec is correct", func(t *testing.T) {
		existingConfig := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "config.open-cluster-management.io/v1alpha1",
				"kind":       "KlusterletConfig",
				"metadata": map[string]interface{}{
					"name":            "mce-import-klusterlet-config",
					"resourceVersion": "123",
				},
				"spec": map[string]interface{}{
					"installMode": map[string]interface{}{
						"type": "noOperator",
						"noOperator": map[string]interface{}{
							"postfix": "mce-import",
						},
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(existingConfig).
			Build()

		controller := &DiscoveryConfigController{
			Client:            fakeClient,
			Log:               testLogger,
			Scheme:            testScheme,
			OperatorNamespace: "test-operator-namespace",
		}

		err := controller.createOrUpdateKlusterletConfig(context.TODO(), testLogger)
		assert.NoError(t, err)

		// Verify KlusterletConfig was not updated (resourceVersion should be the same)
		klusterletConfig := &unstructured.Unstructured{}
		klusterletConfig.SetAPIVersion("config.open-cluster-management.io/v1alpha1")
		klusterletConfig.SetKind("KlusterletConfig")
		err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "mce-import-klusterlet-config"}, klusterletConfig)
		assert.NoError(t, err)

		resourceVersion, found, err := unstructured.NestedString(klusterletConfig.Object, "metadata", "resourceVersion")
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "123", resourceVersion, "ResourceVersion should not change when no update is needed")
	})

	t.Run("removeKlusterletConfig - removes existing KlusterletConfig", func(t *testing.T) {
		existingConfig := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "config.open-cluster-management.io/v1alpha1",
				"kind":       "KlusterletConfig",
				"metadata": map[string]interface{}{
					"name": "mce-import-klusterlet-config",
				},
				"spec": map[string]interface{}{
					"installMode": map[string]interface{}{
						"type": "noOperator",
						"noOperator": map[string]interface{}{
							"postfix": "mce-import",
						},
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(existingConfig).
			Build()

		controller := &DiscoveryConfigController{
			Client:            fakeClient,
			Log:               testLogger,
			Scheme:            testScheme,
			OperatorNamespace: "test-operator-namespace",
		}

		err := controller.removeKlusterletConfig(context.TODO(), testLogger)
		assert.NoError(t, err)

		// Verify KlusterletConfig was removed
		klusterletConfig := &unstructured.Unstructured{}
		klusterletConfig.SetAPIVersion("config.open-cluster-management.io/v1alpha1")
		klusterletConfig.SetKind("KlusterletConfig")
		err = fakeClient.Get(context.TODO(), client.ObjectKey{Name: "mce-import-klusterlet-config"}, klusterletConfig)
		assert.True(t, apierrors.IsNotFound(err), "Expected KlusterletConfig to be removed")
	})

	t.Run("removeKlusterletConfig - handles non-existent KlusterletConfig gracefully", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			Build()

		controller := &DiscoveryConfigController{
			Client:            fakeClient,
			Log:               testLogger,
			Scheme:            testScheme,
			OperatorNamespace: "test-operator-namespace",
		}

		err := controller.removeKlusterletConfig(context.TODO(), testLogger)
		assert.NoError(t, err, "Should handle non-existent KlusterletConfig gracefully")
	})
}

func TestDiscoveryConfigController_PreventsMCEImportDisableWithActiveClusters(t *testing.T) {
	// Setup test scheme
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, addonapiv1alpha1.AddToScheme(testScheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(testScheme))
	require.NoError(t, clusterv1.AddToScheme(testScheme))
	require.NoError(t, clusterv1alpha1.AddToScheme(testScheme))

	// Create a ManagedCluster with the mce-import-klusterlet-config annotation
	managedCluster := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-cluster",
			Annotations: map[string]string{
				"agent.open-cluster-management.io/klusterlet-config": "mce-import-klusterlet-config",
			},
		},
	}

	// Create addon config with configureMceImport disabled
	addonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
		Spec: addonapiv1alpha1.AddOnDeploymentConfigSpec{
			CustomizedVariables: []addonapiv1alpha1.CustomizedVariable{
				{Name: "configureMceImport", Value: "false"},
			},
		},
	}

	// Create ACM namespace
	acmNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "open-cluster-management",
		},
	}

	// Create ACM CSV
	acmCSV := &operatorsv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "advanced-cluster-management.v2.9.0",
			Namespace: "open-cluster-management",
		},
	}

	// Create fake client with the objects
	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(addonConfig, managedCluster, acmNamespace, acmCSV).
		Build()

	// Create controller
	controller := &DiscoveryConfigController{
		Client:            fakeClient,
		Log:               zap.New(zap.UseDevMode(true)),
		OperatorNamespace: "multicluster-engine",
	}

	// Reconcile
	result, err := controller.Reconcile(context.TODO(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "hypershift-addon-deploy-config",
			Namespace: "multicluster-engine",
		},
	})

	// Should not return error but should not perform removal operations
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, result)

	// Verify that the discovery addon config was NOT removed (should not exist since it was never created)
	discoveryConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "addon-ns-config",
		Namespace: "multicluster-engine",
	}, discoveryConfig)
	assert.True(t, apierrors.IsNotFound(err), "Discovery addon config should not exist")

	// Verify that the KlusterletConfig was NOT removed (should not exist since it was never created)
	klusterletConfig := &unstructured.Unstructured{}
	klusterletConfig.SetAPIVersion("operator.open-cluster-management.io/v1")
	klusterletConfig.SetKind("KlusterletConfig")
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name: "mce-import-klusterlet-config",
	}, klusterletConfig)
	assert.True(t, apierrors.IsNotFound(err), "KlusterletConfig should not exist")

	// Verify that the hypershift addon config still has the original customized variables
	updatedAddonConfig := &addonapiv1alpha1.AddOnDeploymentConfig{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{
		Name:      "hypershift-addon-deploy-config",
		Namespace: "multicluster-engine",
	}, updatedAddonConfig)
	require.NoError(t, err)

	// The configureMceImport should still be "false" and no removal should have occurred
	found := false
	for _, variable := range updatedAddonConfig.Spec.CustomizedVariables {
		if variable.Name == "configureMceImport" && variable.Value == "false" {
			found = true
			break
		}
	}
	assert.True(t, found, "configureMceImport should still be 'false'")
}

func TestEqualNestedMaps(t *testing.T) {
	tests := []struct {
		name     string
		map1     map[string]interface{}
		map2     map[string]interface{}
		expected bool
	}{
		{
			name: "identical simple maps",
			map1: map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
			map2: map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
			expected: true,
		},
		{
			name: "different simple maps",
			map1: map[string]interface{}{
				"key1": "value1",
			},
			map2: map[string]interface{}{
				"key1": "value2",
			},
			expected: false,
		},
		{
			name: "identical nested maps",
			map1: map[string]interface{}{
				"installMode": map[string]interface{}{
					"type": "noOperator",
					"noOperator": map[string]interface{}{
						"postfix": "mce-import",
					},
				},
			},
			map2: map[string]interface{}{
				"installMode": map[string]interface{}{
					"type": "noOperator",
					"noOperator": map[string]interface{}{
						"postfix": "mce-import",
					},
				},
			},
			expected: true,
		},
		{
			name: "different nested maps",
			map1: map[string]interface{}{
				"installMode": map[string]interface{}{
					"type": "noOperator",
					"noOperator": map[string]interface{}{
						"postfix": "mce-import",
					},
				},
			},
			map2: map[string]interface{}{
				"installMode": map[string]interface{}{
					"type": "different",
				},
			},
			expected: false,
		},
		{
			name: "different map sizes",
			map1: map[string]interface{}{
				"key1": "value1",
			},
			map2: map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
			expected: false,
		},
		{
			name:     "empty maps",
			map1:     map[string]interface{}{},
			map2:     map[string]interface{}{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := equalNestedMaps(tt.map1, tt.map2)
			assert.Equal(t, tt.expected, result)
		})
	}
}
