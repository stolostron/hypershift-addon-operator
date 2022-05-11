package agent

import (
	"context"
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
