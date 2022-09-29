package install

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"testing"
	"time"

	"github.com/ghodss/yaml"
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
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	imageapi "github.com/openshift/api/image/v1"
	kbatch "k8s.io/api/batch/v1"
)

const (
	hsOperatorImage = "hypershift-operator"
)

func initClient() ctrlClient.Client {
	scheme := runtime.NewScheme()
	//corev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	metav1.AddMetaToScheme(scheme)
	hyperv1alpha1.AddToScheme(scheme)
	kbatch.AddToScheme(scheme)

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
		util.HypershiftAddonAnnotationKey: util.AddonControllerName,
	}
	return deploy
}

func initDeployAddonImageDiffObj() *appsv1.Deployment {
	deploy := initDeployObj()
	deploy.Annotations = map[string]string{
		util.HypershiftAddonAnnotationKey: util.AddonControllerName,
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

	container := corev1.Container{
		Name:  "operator",
		Image: "",
	}

	dp := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "operator",
			Namespace: "hypershift",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
			},
		},
	}

	// Override HS operator image
	for i, arg := range args {
		if arg == "--image-refs" {
			im, err := ioutil.ReadFile(args[i+1])
			if err != nil {
				return nil, err
			}

			ims := &imageapi.ImageStream{}
			if err = yaml.Unmarshal(im, ims); err != nil {
				return nil, err
			}

			for _, tag := range ims.Spec.Tags {
				if tag.Name == hsOperatorImage {
					dp.Spec.Template.Spec.Containers[0].Image = tag.From.Name
				}
				break
			}

			break
		}
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
			aCtrl := &UpgradeController{
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
			name:       "no deployment, function returns true",
			deploy:     nil,
			expectedOk: true,
		},
		{
			name:       "hypershift-operator Deployment, not owned by acm addon",
			deploy:     initDeployObj(),
			expectedOk: false,
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
			expectedOk:    true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			aCtrl := &UpgradeController{
				spokeUncachedClient: initClient(),
				operatorImage:       c.operatorImage,
			}
			if c.deploy != nil {
				assert.Nil(t, aCtrl.spokeUncachedClient.Create(ctx, c.deploy), "")
			}

			err, ok := aCtrl.deploymentUpgradable(ctx)
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

	ctl := UpgradeController{
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
	aCtrl := &UpgradeController{
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
			Name:      util.HypershiftBucketSecretName,
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
			Annotations: map[string]string{util.HypershiftAddonAnnotationKey: util.AddonControllerName},
		},
	}
	aCtrl.hubClient.Create(ctx, incompleteDp)

	// No Spec in hypershift deployment operator - skip all operations
	err := installHyperShiftOperator(t, ctx, aCtrl)
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
			Annotations: map[string]string{util.HypershiftAddonAnnotationKey: util.AddonControllerName},
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

	err = installHyperShiftOperator(t, ctx, aCtrl)
	assert.Nil(t, err, "is nil if install HyperShift is successful")

	// Check hypershift-operator-oidc-provider-s3-credentials secret exists
	oidcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftBucketSecretName,
			Namespace: "hypershift",
		},
	}
	err = aCtrl.spokeUncachedClient.Get(ctx, ctrlClient.ObjectKeyFromObject(oidcSecret), oidcSecret)
	assert.Nil(t, err, "is nil when oidc secret is found")
	assert.Equal(t, []byte(`myCredential`), oidcSecret.Data["credentials"], "the credentials should be equal if the copy was a success")

	// Check hypershift-operator-private-link-credentials secret does NOT exist
	plSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftPrivateLinkSecretName,
			Namespace: "hypershift",
		},
	}
	err = aCtrl.spokeUncachedClient.Get(ctx, ctrlClient.ObjectKeyFromObject(plSecret), plSecret)
	assert.NotNil(t, err, "is not nil when private link secret is not provided")
	assert.True(t, errors.IsNotFound(err), "private link secret should not be found")

	// Check hypershift deployment still exists
	err = aCtrl.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, dp)
	assert.Nil(t, err, "is nil if the hypershift deployment is found")

	// Check pull secret is created in HyperShift Namespace
	hsPullSecret := &corev1.Secret{}
	err = aCtrl.spokeUncachedClient.Get(ctx, types.NamespacedName{Name: pullSecret.Name, Namespace: hypershiftOperatorKey.Namespace}, hsPullSecret)
	assert.Nil(t, err, "is nil if the pull secret in the HyperShift namespace is found")

	tr := []imageapi.TagReference{}
	tr = append(tr, imageapi.TagReference{Name: hsOperatorImage, From: &corev1.ObjectReference{Name: "quay.io/stolostron/hypershift-operator@sha256:122a59aaf2fa72d1e3c0befb0de61df2aeea848676b0f41055b07ca0e6291391"}})
	ims := &imageapi.ImageStream{}
	ims.Spec.Tags = tr
	imb, err := yaml.Marshal(ims)

	// Run hypershift install again with image override
	overrideCM := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftDownstreamOverride,
			Namespace: aCtrl.addonNamespace,
		},
		Data: map[string]string{util.HypershiftOverrideKey: base64.StdEncoding.EncodeToString(imb)},
	}
	aCtrl.withOverride = true
	aCtrl.hubClient.Create(ctx, overrideCM)
	defer aCtrl.hubClient.Delete(ctx, overrideCM)

	err = installHyperShiftOperator(t, ctx, aCtrl)
	assert.Nil(t, err, "is nil if install HyperShift is sucessful")

	// Run hypershift install again with image override using image upgrade configmap
	imageUpgradeCM := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftOverrideImagesCM,
			Namespace: aCtrl.addonNamespace,
		},
		Data: map[string]string{
			"hypershift-operator": "quay.io/stolostron/hypershift-operator@sha256:eedb58e7b9c4d9e49c6c53d1b5b97dfddcdffe839bbffd4fb950760715d24244",
		},
	}
	err = aCtrl.hubClient.Create(ctx, imageUpgradeCM)
	assert.Nil(t, err, "err nil when config map is created successfull")

	err = installHyperShiftOperator(t, ctx, aCtrl)
	assert.Nil(t, err, "is nil if install HyperShift is sucessful")

	// Install hypershift job failed
	go updateHsInstallJobToFailed(ctx, aCtrl.spokeUncachedClient, aCtrl.addonNamespace)
	err = aCtrl.RunHypershiftInstall(ctx)
	assert.NotNil(t, err, "is nil if install HyperShift is sucessful")
	assert.Equal(t, "install HyperShift job failed", err.Error())
	if err := deleteAllInstallJobs(ctx, aCtrl.spokeUncachedClient, aCtrl.addonNamespace); err != nil {
		t.Errorf("error cleaning up HyperShift install jobs: %s", err.Error())
	}

	// Run hypershift install again with pull secret deleted
	aCtrl.hubClient.Delete(ctx, pullSecret)
	aCtrl.hubClient.Delete(ctx, hsPullSecret)

	err = installHyperShiftOperator(t, ctx, aCtrl)
	assert.Nil(t, err, "is nil if install HyperShift is sucessful")

	err = aCtrl.spokeUncachedClient.Get(ctx, types.NamespacedName{Name: pullSecret.Name, Namespace: hypershiftOperatorKey.Namespace}, hsPullSecret)
	assert.True(t, err != nil && errors.IsNotFound(err), "is true if the pull secret is not copied to the HyperShift namespace")

	// Run hypershift install again with s3 bucket secret deleted
	aCtrl.hubClient.Delete(ctx, bucketSecret)
	err = installHyperShiftOperator(t, ctx, aCtrl)
	assert.Nil(t, err, "is nil if install HyperShift is sucessful")

	// Create hosted cluster
	hcNN := types.NamespacedName{Name: "hd-1", Namespace: "clusters"}
	hc := getHostedCluster(hcNN)
	err = aCtrl.hubClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfull")

	// Cleanup
	// Hypershift deployment is not deleted because there is an existing hostedcluster
	err = aCtrl.RunHypershiftCmdWithRetries(ctx, 3, time.Second*10, aCtrl.RunHypershiftCleanup)
	assert.Nil(t, err, "is nil if cleanup is succcessful")

	// Check hypershift deployment is not deleted
	err = aCtrl.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, dp)
	assert.Nil(t, err, "is nil if the hypershift deployment exists")

	// Delete HC
	err = aCtrl.spokeUncachedClient.Delete(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is deleted successfull")

	// Cleanup after HC is deleted
	err = aCtrl.RunHypershiftCmdWithRetries(ctx, 3, time.Second*10, aCtrl.RunHypershiftCleanup)
	assert.Nil(t, err, "is nil if cleanup is succcessful")

	// Check hypershift deployment is deleted
	err = aCtrl.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, dp)
	assert.NotNil(t, err, "is not nil if the hypershift deployment is deleted")
	assert.True(t, errors.IsNotFound(err))

	// Cleanup again with nil aCtrl is successful
	err = aCtrl.RunHypershiftCmdWithRetries(ctx, 3, time.Second*10, aCtrl.RunHypershiftCleanup)
	assert.Nil(t, err, "is nil if cleanup is successful")
}

func TestReadDownstreamOverride(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	aCtrl := &UpgradeController{
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
	aCtrl := &UpgradeController{
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

	err := aCtrl.RunHypershiftCmdWithRetries(ctx, 3, 1*time.Second, cmd)
	assert.Nil(t, err, "is nil if retry is successful")
}

func TestRunHypershiftInstallPrivateLinkExternalDNS(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	aCtrl := &UpgradeController{
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
			Name:      util.HypershiftBucketSecretName,
			Namespace: aCtrl.clusterName,
		},
		Data: map[string][]byte{
			"bucket":                []byte(`my-bucket`),
			"region":                []byte(`us-east-1`),
			"aws-secret-access-key": []byte(`aws_s3_secret`),
			"aws-access-key-id":     []byte(`aws_s3_key_id`),
		},
	}
	privateSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftPrivateLinkSecretName,
			Namespace: aCtrl.clusterName,
		},
		Data: map[string][]byte{
			"region":                []byte(`us-east-1`),
			"aws-secret-access-key": []byte(`private_secret`),
			"aws-access-key-id":     []byte(`private_secret_key_id`),
		},
	}
	externalDnsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftExternalDNSSecretName,
			Namespace: aCtrl.clusterName,
		},
		Data: map[string][]byte{
			"provider":      []byte(`aws`),
			"credentials":   []byte(`private_secret`),
			"domain-filter": []byte(`my.house.com`),
		},
	}

	aCtrl.hubClient.Create(ctx, bucketSecret)
	defer aCtrl.hubClient.Delete(ctx, bucketSecret)
	aCtrl.hubClient.Create(ctx, privateSecret)
	defer aCtrl.hubClient.Delete(ctx, privateSecret)
	aCtrl.hubClient.Create(ctx, externalDnsSecret)
	defer aCtrl.hubClient.Delete(ctx, externalDnsSecret)

	dp := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "operator",
			Namespace:   "hypershift",
			Annotations: map[string]string{util.HypershiftAddonAnnotationKey: util.AddonControllerName},
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

	err := installHyperShiftOperator(t, ctx, aCtrl)
	assert.Nil(t, err, "is nil if install HyperShift is successful")

	// Check hypershift-operator-oidc-provider-s3-credentials secret exists
	oidcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftBucketSecretName,
			Namespace: "hypershift",
		},
	}
	err = aCtrl.spokeUncachedClient.Get(ctx, ctrlClient.ObjectKeyFromObject(oidcSecret), oidcSecret)
	assert.Nil(t, err, "is nil when oidc secret is found")
	assert.Equal(t, []byte("[default]\naws_access_key_id = aws_s3_key_id\naws_secret_access_key = aws_s3_secret"), oidcSecret.Data["credentials"], "the credentials should be equal if the copy was a success")

	// Check hypershift-operator-private-link-credentials secret exists
	plSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftPrivateLinkSecretName,
			Namespace: "hypershift",
		},
	}
	err = aCtrl.spokeUncachedClient.Get(ctx, ctrlClient.ObjectKeyFromObject(plSecret), plSecret)
	assert.Nil(t, err, "is nil when private link secret is found")
	assert.Equal(t, []byte("[default]\naws_access_key_id = private_secret_key_id\naws_secret_access_key = private_secret"), plSecret.Data["credentials"], "the credentials should be equal if the copy was a success")

	// Check hypershift-operator-external-dns-credentials secret exists
	edSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftExternalDNSSecretName,
			Namespace: "hypershift",
		},
	}
	err = aCtrl.spokeUncachedClient.Get(ctx, ctrlClient.ObjectKeyFromObject(edSecret), edSecret)
	assert.Nil(t, err, "is nil when external dns secret is found")
	assert.Equal(t, []byte(`private_secret`), edSecret.Data["credentials"], "the credentials should be equal if the copy was a success")

	// Cleanup
	err = aCtrl.RunHypershiftCmdWithRetries(ctx, 3, time.Second*10, aCtrl.RunHypershiftCleanup)
	assert.Nil(t, err, "is nil if cleanup is succcessful")
}

func TestCreateSpokeCredential(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	aCtrl := &UpgradeController{
		spokeUncachedClient:       client,
		hubClient:                 client,
		log:                       zapr.NewLogger(zapLog),
		addonNamespace:            "addon",
		operatorImage:             "my-test-image",
		clusterName:               "cluster1",
		pullSecret:                "pull-secret",
		hypershiftInstallExecutor: &HypershiftTestCliExecutor{},
	}

	bucketSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftBucketSecretName,
			Namespace: aCtrl.clusterName,
		},
	}

	err := aCtrl.createAwsSpokeSecret(ctx, bucketSecret)
	assert.NotNil(t, err, "is not nil, when secret is not well formed")

}

func getHostedCluster(hcNN types.NamespacedName) *hyperv1alpha1.HostedCluster {
	hc := &hyperv1alpha1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: "hypershift.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        hcNN.Name,
			Namespace:   hcNN.Namespace,
			Annotations: map[string]string{util.HypershiftDeploymentAnnoKey: "test-hd1"},
		},
		Spec: hyperv1alpha1.HostedClusterSpec{
			Platform: hyperv1alpha1.PlatformSpec{
				Type: hyperv1alpha1.AWSPlatform,
			},
			Networking: hyperv1alpha1.ClusterNetworking{
				NetworkType: hyperv1alpha1.OpenShiftSDN,
			},
			Services: []hyperv1alpha1.ServicePublishingStrategyMapping{},
			Release: hyperv1alpha1.Release{
				Image: "test-image",
			},
			Etcd: hyperv1alpha1.EtcdSpec{
				ManagementType: hyperv1alpha1.Managed,
			},
			InfraID: "infra-abcdef",
		},
		Status: hyperv1alpha1.HostedClusterStatus{
			KubeConfig:        &corev1.LocalObjectReference{Name: "kubeconfig"},
			KubeadminPassword: &corev1.LocalObjectReference{Name: "kubeadmin"},
			Conditions:        []metav1.Condition{{Type: string(hyperv1alpha1.HostedClusterAvailable), Status: metav1.ConditionTrue}},
		},
	}
	return hc
}

func installHyperShiftOperator(t *testing.T, ctx context.Context, aCtrl *UpgradeController) error {
	go updateHsInstallJobToSucceeded(ctx, aCtrl.spokeUncachedClient, aCtrl.addonNamespace)
	err := aCtrl.RunHypershiftInstall(ctx)
	if err := deleteAllInstallJobs(ctx, aCtrl.spokeUncachedClient, aCtrl.addonNamespace); err != nil {
		t.Errorf("error cleaning up HyperShift install jobs: %s", err.Error())
	}

	return err
}

func markInstallJobFinished(ctx context.Context, client ctrlClient.Client, addonNamespace string, f func(*kbatch.Job)) wait.ConditionFunc {
	return func() (bool, error) {
		listopts := &ctrlClient.ListOptions{}
		listopts.Namespace = addonNamespace
		jobList := &kbatch.JobList{}
		if err := client.List(ctx, jobList, listopts); err != nil {
			return false, err
		}

		if len(jobList.Items) > 0 {
			for _, job := range jobList.Items {
				f(&job)
				if err := client.Update(ctx, &job); err != nil {
					return false, err
				}
			}
			return true, nil
		}

		return false, nil
	}
}

func markInstallJobSucceeded(ctx context.Context, client ctrlClient.Client, addonNamespace string) wait.ConditionFunc {
	sFunc := func(job *kbatch.Job) {
		job.Status.Succeeded = 1
	}

	return markInstallJobFinished(ctx, client, addonNamespace, sFunc)
}

func markInstallJobFailed(ctx context.Context, client ctrlClient.Client, addonNamespace string) wait.ConditionFunc {
	sFunc := func(job *kbatch.Job) {
		job.Status.Failed = 1
	}

	return markInstallJobFinished(ctx, client, addonNamespace, sFunc)
}

func updateHsInstallJobToSucceeded(ctx context.Context, client ctrlClient.Client, addonNamespace string) error {
	return wait.PollImmediate(3*time.Second, 15*time.Second, markInstallJobSucceeded(ctx, client, addonNamespace))
}

func updateHsInstallJobToFailed(ctx context.Context, client ctrlClient.Client, addonNamespace string) error {
	return wait.PollImmediate(3*time.Second, 15*time.Second, markInstallJobFailed(ctx, client, addonNamespace))
}

func deleteAllInstallJobs(ctx context.Context, client ctrlClient.Client, addonNamespace string) error {
	listopts := &ctrlClient.ListOptions{}
	listopts.Namespace = addonNamespace
	jobList := &kbatch.JobList{}
	if err := client.List(ctx, jobList, listopts); err != nil {
		return err
	}

	if len(jobList.Items) > 0 {
		for _, job := range jobList.Items {
			if err := client.Delete(ctx, &job); err != nil {
				return err
			}
		}
	}
	return nil
}
