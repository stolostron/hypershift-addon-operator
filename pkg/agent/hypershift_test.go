package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func initClient() client.Client {
	scheme := runtime.NewScheme()
	//corev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	metav1.AddMetaToScheme(scheme)
	hyperv1alpha1.AddToScheme(scheme)

	ncb := fake.NewClientBuilder()
	ncb.WithScheme(scheme)
	return ncb.Build()

}

func initDeployObj() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftOperatorName,
			Namespace: util.HypershiftOperatorNamespace,
		},
	}
}

func initDeployAddonObj() *appsv1.Deployment {
	deploy := initDeployObj()
	deploy.Annotations = map[string]string{
		hypershiftAddonAnnotationKey: util.AddonControllerName,
	}
	return deploy
}

func initDeployAddonImageDiffObj() *appsv1.Deployment {
	deploy := initDeployObj()
	deploy.Annotations = map[string]string{
		hypershiftAddonAnnotationKey: util.AddonControllerName,
	}
	deploy.Spec.Template.Spec.Containers = []corev1.Container{
		corev1.Container{Image: "testimage"},
	}
	return deploy
}

type HypershiftTestCliExecutor struct {
}

func (c *HypershiftTestCliExecutor) Execute(ctx context.Context, args []string) ([]byte, error) {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm",
			Namespace: "default",
		},
		Data: map[string]string{"test": "test"},
	}

	var items []interface{}
	items = append(items, cm)

	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sa",
			Namespace: "default",
		},
	}
	items = append(items, sa)

	dp := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "operator",
			Namespace: "hypershift",
		},
	}
	items = append(items, dp)

	out := make(map[string]interface{})
	out["items"] = items
	return json.Marshal(out)
}

func TestIsDeploymentMarked(t *testing.T) {

	cases := []struct {
		name        string
		deploy      *appsv1.Deployment
		expectedErr string
		expectedOk  bool
	}{
		{
			name:       "no deployment",
			deploy:     nil,
			expectedOk: false,
		},
		{
			name:       "unmarked deployment",
			deploy:     initDeployObj(),
			expectedOk: false,
		},
		{
			name:       "marked deployment",
			deploy:     initDeployAddonObj(),
			expectedOk: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			zapLog, _ := zap.NewDevelopment()
			aCtrl := &agentController{
				spokeUncachedClient: initClient(),
				log:                 zapr.NewLogger(zapLog),
			}
			if c.deploy != nil {
				assert.Nil(t, aCtrl.spokeUncachedClient.Create(ctx, c.deploy), "")
			}

			ok := aCtrl.isDeploymentMarked(ctx)
			assert.Equal(t, c.expectedOk, ok, "ok as expected")
		})
	}
}

func TestDeploymentExistsWithNoImage(t *testing.T) {

	cases := []struct {
		name          string
		deploy        *appsv1.Deployment
		operatorImage string
		expectedErr   string
		expectedOk    bool
	}{
		{
			name:       "no deployment, function returns false",
			deploy:     nil,
			expectedOk: false,
		},
		{
			name:       "hypershift-operator Deployment, not owned by acm addon",
			deploy:     initDeployObj(),
			expectedOk: true,
		},
		{
			name:       "hypershift-operator Deployment, owned by acm addon with identical images",
			deploy:     initDeployAddonObj(),
			expectedOk: true,
		},
		{
			name:          "hypershift-operator Deployment, owned by acm addon with identical images",
			deploy:        initDeployAddonImageDiffObj(),
			operatorImage: "my-new-image02",
			expectedOk:    false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			aCtrl := &agentController{
				spokeUncachedClient: initClient(),
				operatorImage:       c.operatorImage,
			}
			if c.deploy != nil {
				assert.Nil(t, aCtrl.spokeUncachedClient.Create(ctx, c.deploy), "")
			}

			err, ok := aCtrl.deploymentExistWithNoImageChange(ctx)
			if len(c.expectedErr) == 0 {
				assert.Nil(t, err, "nil when function is successful")
				assert.Equal(t, c.expectedOk, ok, "ok as expected")
			} else {
				assert.Contains(t, err.Error(), c.expectedErr)
			}
		})
	}
}

func TestRunHypershiftRender(t *testing.T) {
	ctx := context.Background()
	args := []string{
		"render",
		"--hypershift-image", "quay.io/stolostron/hypershift",
		"--namespace", hypershiftOperatorKey.Namespace,
		"--format", "json",
	}

	ctl := agentController{
		hypershiftInstallExecutor: &HypershiftLibExecutor{},
	}
	outputs, err := ctl.runHypershiftRender(ctx, args)
	if err != nil {
		t.Errorf("Execute hypershift command failed: %v", err)
	}

	// mapKey format: resourceKind/namespace/name
	expectResources := map[string]struct{}{
		"PriorityClass//hypershift-control-plane":                                                  {},
		"PriorityClass//hypershift-etcd":                                                           {},
		"PriorityClass//hypershift-api-critical":                                                   {},
		"PriorityClass//hypershift-operator":                                                       {},
		"Namespace//hypershift":                                                                    {},
		"ServiceAccount/hypershift/operator":                                                       {},
		"ClusterRole//hypershift-operator":                                                         {},
		"ClusterRoleBinding//hypershift-operator":                                                  {},
		"Role/hypershift/hypershift-operator":                                                      {},
		"RoleBinding/hypershift/hypershift-operator":                                               {},
		"Deployment/hypershift/operator":                                                           {},
		"Service/hypershift/operator":                                                              {},
		"Role/hypershift/prometheus":                                                               {},
		"RoleBinding/hypershift/prometheus":                                                        {},
		"ServiceMonitor/hypershift/operator":                                                       {},
		"PrometheusRule/hypershift/metrics":                                                        {},
		"CustomResourceDefinition//clusterresourcesetbindings.addons.cluster.x-k8s.io":             {},
		"CustomResourceDefinition//clusterresourcesets.addons.cluster.x-k8s.io":                    {},
		"CustomResourceDefinition//clusterclasses.cluster.x-k8s.io":                                {},
		"CustomResourceDefinition//clusters.cluster.x-k8s.io":                                      {},
		"CustomResourceDefinition//machinedeployments.cluster.x-k8s.io":                            {},
		"CustomResourceDefinition//machinehealthchecks.cluster.x-k8s.io":                           {},
		"CustomResourceDefinition//machinepools.cluster.x-k8s.io":                                  {},
		"CustomResourceDefinition//machines.cluster.x-k8s.io":                                      {},
		"CustomResourceDefinition//machinesets.cluster.x-k8s.io":                                   {},
		"CustomResourceDefinition//agentclusters.capi-provider.agent-install.openshift.io":         {},
		"CustomResourceDefinition//agentmachines.capi-provider.agent-install.openshift.io":         {},
		"CustomResourceDefinition//agentmachinetemplates.capi-provider.agent-install.openshift.io": {},
		"CustomResourceDefinition//awsclustercontrolleridentities.infrastructure.cluster.x-k8s.io": {},
		"CustomResourceDefinition//awsclusterroleidentities.infrastructure.cluster.x-k8s.io":       {},
		"CustomResourceDefinition//awsclusters.infrastructure.cluster.x-k8s.io":                    {},
		"CustomResourceDefinition//awsclusterstaticidentities.infrastructure.cluster.x-k8s.io":     {},
		"CustomResourceDefinition//awsclustertemplates.infrastructure.cluster.x-k8s.io":            {},
		"CustomResourceDefinition//awsfargateprofiles.infrastructure.cluster.x-k8s.io":             {},
		"CustomResourceDefinition//awsmachinepools.infrastructure.cluster.x-k8s.io":                {},
		"CustomResourceDefinition//awsmachines.infrastructure.cluster.x-k8s.io":                    {},
		"CustomResourceDefinition//awsmachinetemplates.infrastructure.cluster.x-k8s.io":            {},
		"CustomResourceDefinition//awsmanagedmachinepools.infrastructure.cluster.x-k8s.io":         {},
		"CustomResourceDefinition//azureclusteridentities.infrastructure.cluster.x-k8s.io":         {},
		"CustomResourceDefinition//azureclusters.infrastructure.cluster.x-k8s.io":                  {},
		"CustomResourceDefinition//azuremachines.infrastructure.cluster.x-k8s.io":                  {},
		"CustomResourceDefinition//azuremachinetemplates.infrastructure.cluster.x-k8s.io":          {},
		"CustomResourceDefinition//ibmpowervsclusters.infrastructure.cluster.x-k8s.io":             {},
		"CustomResourceDefinition//ibmpowervsimages.infrastructure.cluster.x-k8s.io":               {},
		"CustomResourceDefinition//ibmpowervsmachines.infrastructure.cluster.x-k8s.io":             {},
		"CustomResourceDefinition//ibmpowervsmachinetemplates.infrastructure.cluster.x-k8s.io":     {},
		"CustomResourceDefinition//ibmvpcclusters.infrastructure.cluster.x-k8s.io":                 {},
		"CustomResourceDefinition//ibmvpcmachines.infrastructure.cluster.x-k8s.io":                 {},
		"CustomResourceDefinition//ibmvpcmachinetemplates.infrastructure.cluster.x-k8s.io":         {},
		"CustomResourceDefinition//kubevirtclusters.infrastructure.cluster.x-k8s.io":               {},
		"CustomResourceDefinition//kubevirtmachines.infrastructure.cluster.x-k8s.io":               {},
		"CustomResourceDefinition//kubevirtmachinetemplates.infrastructure.cluster.x-k8s.io":       {},
		"CustomResourceDefinition//awsendpointservices.hypershift.openshift.io":                    {},
		"CustomResourceDefinition//hostedclusters.hypershift.openshift.io":                         {},
		"CustomResourceDefinition//hostedcontrolplanes.hypershift.openshift.io":                    {},
		"CustomResourceDefinition//nodepools.hypershift.openshift.io":                              {},
	}

	if len(expectResources) != len(outputs) {
		t.Errorf("Expect resource number %d, but got %d", len(expectResources), len(outputs))
	}

	for _, v := range outputs {
		key := fmt.Sprintf("%s/%s/%s", v.GetKind(), v.GetNamespace(), v.GetName())
		if _, ok := expectResources[key]; !ok {
			t.Errorf("Resource %s is not what we expect", key)
		}

		// print the expect resource map keys
		// t.Errorf("\"%s/%s/%s\":{},", v.GetKind(), v.GetNamespace(), v.GetName())
	}
}

func TestRunHypershiftInstall(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	aCtrl := &agentController{
		spokeUncachedClient:       client,
		hubClient:                 client,
		log:                       zapr.NewLogger(zapLog),
		addonNamespace:            "addon",
		operatorImage:             "my-test-image",
		clusterName:               "cluster1",
		pullSecret:                "pull-secret",
		hypershiftInstallExecutor: &HypershiftTestCliExecutor{},
	}

	addonNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: aCtrl.addonNamespace,
		},
	}
	aCtrl.hubClient.Create(ctx, addonNs)
	defer aCtrl.hubClient.Delete(ctx, addonNs)

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      aCtrl.pullSecret,
			Namespace: aCtrl.addonNamespace,
		},
		Data: map[string][]byte{
			".dockerconfigjson": []byte(`docker-pull-secret`),
		},
	}
	aCtrl.hubClient.Create(ctx, pullSecret)

	bucketSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hypershiftBucketSecretName,
			Namespace: aCtrl.clusterName,
		},
		Data: map[string][]byte{
			"bucket":      []byte(`my-bucket`),
			"region":      []byte(`us-east-1`),
			"credentials": []byte(`myCredential`),
		},
	}
	aCtrl.hubClient.Create(ctx, bucketSecret)
	defer aCtrl.hubClient.Delete(ctx, bucketSecret)

	incompleteDp := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "operator",
			Namespace:   "hypershift",
			Annotations: map[string]string{hypershiftAddonAnnotationKey: util.AddonControllerName},
		},
	}
	aCtrl.hubClient.Create(ctx, incompleteDp)

	// No Spec in hypershift deployment operator - skip all operations
	err := aCtrl.runHypershiftInstall(ctx)
	assert.Nil(t, err, "is nil if install HyperShift is successful")
	aCtrl.hubClient.Delete(ctx, incompleteDp)

	dp := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "operator",
			Namespace:   "hypershift",
			Annotations: map[string]string{hypershiftAddonAnnotationKey: util.AddonControllerName},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx:1.14.2",
						Ports: []corev1.ContainerPort{{ContainerPort: 80}},
					}},
				},
			},
		},
	}
	aCtrl.hubClient.Create(ctx, dp)
	defer aCtrl.hubClient.Delete(ctx, dp)

	err = aCtrl.runHypershiftInstall(ctx)
	assert.Nil(t, err, "is nil if install HyperShift is successful")

	// Check service account is created
	testSa := &corev1.ServiceAccount{}
	err = aCtrl.spokeUncachedClient.Get(ctx, types.NamespacedName{Name: "test-sa", Namespace: "default"}, testSa)
	assert.Nil(t, err, "is nil if the service account is found")
	assert.Equal(t, aCtrl.pullSecret, testSa.ImagePullSecrets[0].Name, "is equal if the image pull secret in the service account matches the provided pull secret")

	// Check hypershift deployment still exists
	err = aCtrl.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, dp)
	assert.Nil(t, err, "is nil if the hypershift deployment is found")

	// Check pull secret is created in HyperShift Namespace
	hsPullSecret := &corev1.Secret{}
	err = aCtrl.spokeUncachedClient.Get(ctx, types.NamespacedName{Name: pullSecret.Name, Namespace: hypershiftOperatorKey.Namespace}, hsPullSecret)
	assert.Nil(t, err, "is nil if the pull secret in the HyperShift namespace is found")

	// Run hypershift install again with image override
	overrideCM := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftDownstreamOverride,
			Namespace: aCtrl.addonNamespace,
		},
		Data: map[string]string{util.HypershiftOverrideKey: base64.StdEncoding.EncodeToString([]byte("test"))},
	}
	aCtrl.withOverride = true
	aCtrl.hubClient.Create(ctx, overrideCM)
	defer aCtrl.hubClient.Delete(ctx, overrideCM)
	assert.Nil(t, err, "is nil if install HyperShift is sucessful")

	// Run hypershift install again with pull secret deleted
	aCtrl.hubClient.Delete(ctx, pullSecret)
	aCtrl.hubClient.Delete(ctx, hsPullSecret)
	err = aCtrl.runHypershiftInstall(ctx)
	assert.Nil(t, err, "is nil if install HyperShift is sucessful")
	err = aCtrl.spokeUncachedClient.Get(ctx, types.NamespacedName{Name: pullSecret.Name, Namespace: hypershiftOperatorKey.Namespace}, hsPullSecret)
	assert.True(t, err != nil && errors.IsNotFound(err), "is true if the pull secret is not copied to the HyperShift namespace")

	// Cleanup
	o := &AgentOptions{
		Log:            zapr.NewLogger(zapLog),
		AddonName:      "hypershift-addon",
		AddonNamespace: "hypershift",
	}
	err = o.runCleanup(ctx, aCtrl)
	assert.Nil(t, err, "is nil if cleanup is succcessful")

	// Check service account is deleted
	err = aCtrl.spokeUncachedClient.Get(ctx, types.NamespacedName{Name: "test-sa", Namespace: "default"}, testSa)
	assert.NotNil(t, err, "is not nil if the service account is deleted")
	assert.True(t, errors.IsNotFound(err))

	// Check hypershift deployment is deleted
	err = aCtrl.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, dp)
	assert.NotNil(t, err, "is not nil if the hypershift deployment is deleted")
	assert.True(t, errors.IsNotFound(err))

	// Cleanup with nil aCtrl results in error
	o.runCleanup(ctx, nil)
	assert.NotNil(t, err, "is not nil if cleanup failed")
}

func TestReadDownstreamOverride(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	aCtrl := &agentController{
		spokeUncachedClient: client,
		hubClient:           client,
		log:                 zapr.NewLogger(zapLog),
		addonNamespace:      "addon",
		operatorImage:       "my-test-image",
		clusterName:         "cluster1",
		pullSecret:          "pull-secret",
	}

	_, err := aCtrl.readInDownstreamOverride()
	assert.NotNil(t, err, "is not nil when read downstream image override fails")

	overrideCM := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftDownstreamOverride,
			Namespace: aCtrl.addonNamespace,
		},
		Data: map[string]string{util.HypershiftOverrideKey: base64.StdEncoding.EncodeToString([]byte("test"))},
	}
	aCtrl.hubClient.Create(ctx, overrideCM)
	defer aCtrl.hubClient.Delete(ctx, overrideCM)

	f, err := aCtrl.readInDownstreamOverride()
	assert.Nil(t, err, "is nil when read downstream image override is successful")
	assert.NotNil(t, f, "is not nil when override file is created")
}

func TestRunCommandWithRetries(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	aCtrl := &agentController{
		spokeUncachedClient: client,
		hubClient:           client,
		log:                 zapr.NewLogger(zapLog),
		addonNamespace:      "addon",
		operatorImage:       "my-test-image",
		clusterName:         "cluster1",
		pullSecret:          "pull-secret",
	}

	cmd := func(context.Context) error {
		cm1 := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cm1",
				Namespace: "default",
			},
		}
		cm2 := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cm2",
				Namespace: "default",
			},
		}

		// 1st entry - create cm1 and return error
		// 2nd entry - create cm2 and return nil error
		err := aCtrl.spokeUncachedClient.Get(ctx, types.NamespacedName{Name: cm1.Name, Namespace: cm1.Namespace}, cm1)
		if err == nil {
			aCtrl.hubClient.Create(ctx, cm2)
			return nil
		}

		err = aCtrl.spokeUncachedClient.Get(ctx, types.NamespacedName{Name: cm2.Name, Namespace: cm2.Namespace}, cm1)
		if err == nil {
			// Should not be called a 3rd time, error
			return fmt.Errorf("failed 3rd call")
		}

		aCtrl.hubClient.Create(ctx, cm1)
		return fmt.Errorf("failed 1st call")
	}

	err := aCtrl.runHypershiftCmdWithRetires(ctx, 3, 1*time.Second, cmd)
	assert.Nil(t, err, "is nil if retry is successful")
}

func TestCleanupCommand(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	cleanupCmd := NewCleanupCommand("operator", zapr.NewLogger(zapLog))
	assert.Equal(t, "cleanup", cleanupCmd.Use)
}
