package install

import (
	"context"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpgradeImageCheck(t *testing.T) {
	ctx := context.Background()
	zapLog, _ := zap.NewDevelopment()
	uCtrl := NewUpgradeController(nil, initClient(), zapr.NewLogger(zapLog), "hypershift-addon",
		"open-cluster-management-agent-addon", "local-cluster", "hs-op-image", "pull-secret", true)

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
						Env:   []corev1.EnvVar{{Name: util.HypershiftEnvVarImageAgentCapiProvider, Value: "123"}},
					}},
				},
			},
		},
	}
	uCtrl.spokeUncachedClient.Create(ctx, dp)

	overrideCM := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftOverrideImagesCM,
			Namespace: uCtrl.addonNamespace,
		},
	}
	uCtrl.spokeUncachedClient.Create(ctx, overrideCM)

	cases := []struct {
		name       string
		imageName  string
		imageHash  string
		expectedOk bool
	}{
		{
			name:       "check image update: " + util.ImageStreamAwsCapiProvider,
			imageName:  util.ImageStreamAwsCapiProvider,
			imageHash:  "abc",
			expectedOk: true,
		},
		{
			name:       "check image update: " + util.ImageStreamAgentCapiProvider,
			imageName:  util.ImageStreamAgentCapiProvider,
			imageHash:  "abc",
			expectedOk: true,
		},
		{
			name:       "check image update: " + util.ImageStreamAwsEncyptionProvider,
			imageName:  util.ImageStreamAwsEncyptionProvider,
			imageHash:  "abc",
			expectedOk: true,
		},
		{
			name:       "check image update: " + util.ImageStreamAzureCapiProvider,
			imageName:  util.ImageStreamAzureCapiProvider,
			imageHash:  "abc",
			expectedOk: true,
		},
		{
			name:       "check image update: " + util.ImageStreamClusterApi,
			imageName:  util.ImageStreamClusterApi,
			imageHash:  "abc",
			expectedOk: true,
		},
		{
			name:       "check image update: " + util.ImageStreamKonnectivity,
			imageName:  util.ImageStreamKonnectivity,
			imageHash:  "abc",
			expectedOk: true,
		},
		{
			name:       "check image update: " + util.ImageStreamKubevertCapiProvider,
			imageName:  util.ImageStreamKubevertCapiProvider,
			imageHash:  "abc",
			expectedOk: true,
		},
		{
			name:       "check image update: " + util.ImageStreamHypershiftOperator,
			imageName:  util.ImageStreamHypershiftOperator,
			imageHash:  "abc",
			expectedOk: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			overrideCM.Data = map[string]string{c.imageName: c.imageHash}
			uCtrl.spokeUncachedClient.Update(ctx, overrideCM)
			upgradeRequired, _ := uCtrl.upgradeImageCheck()
			assert.Equal(t, c.expectedOk, upgradeRequired, "ok as expected")
		})
	}

	// Image override CM does not exist
	uCtrl.spokeUncachedClient.Delete(ctx, overrideCM)
	upgradeRequired, err := uCtrl.upgradeImageCheck()
	assert.Nil(t, err, "error is nil if image override CM does not exist")
	assert.True(t, upgradeRequired, "image upgrade is required if image override CM does not exist")

	// No deployment
	uCtrl.spokeUncachedClient.Delete(ctx, dp)
	_, err = uCtrl.upgradeImageCheck()
	assert.NotNil(t, err, "error is not nil if deployment does not exist")
	assert.Contains(t, err.Error(), "failed to get the hypershift operator deployment")

	// Deployment has no containers
	dp = &appsv1.Deployment{
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
	uCtrl.spokeUncachedClient.Create(ctx, dp)
	upgradeRequired, err = uCtrl.upgradeImageCheck()
	assert.Nil(t, err, "error is nil if deployment does not have a container")
	assert.False(t, upgradeRequired, "no containers found for HyperShift operator deployment - upgrade not required")
	uCtrl.spokeUncachedClient.Delete(ctx, dp)

	// Deployment does not have addon annotation
	dp = &appsv1.Deployment{
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
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx:1.14.2",
						Ports: []corev1.ContainerPort{{ContainerPort: 80}},
						Env:   []corev1.EnvVar{{Name: util.HypershiftEnvVarImageAgentCapiProvider, Value: "123"}},
					}},
				},
			},
		},
	}
	uCtrl.spokeUncachedClient.Create(ctx, dp)
	upgradeRequired, err = uCtrl.upgradeImageCheck()
	assert.Nil(t, err, "error is nil if deployment does not have addon annotation")
	assert.False(t, upgradeRequired, "HyperShift operator deployment not deployed by the HyperShift addon - upgrade not required")
}
