package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func initClient() client.Client {
	scheme := runtime.NewScheme()
	//corev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)

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
