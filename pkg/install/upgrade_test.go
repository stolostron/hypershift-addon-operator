package install

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestUpgradeImageCheck(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	controller := &UpgradeController{
		spokeUncachedClient:       client,
		hubClient:                 client,
		log:                       zapr.NewLogger(zapLog),
		addonNamespace:            "addon",
		operatorImage:             "my-test-image",
		clusterName:               "cluster1",
		pullSecret:                "pull-secret",
		hypershiftInstallExecutor: &HypershiftTestCliExecutor{},
		imageOverrideConfigmap:    corev1.ConfigMap{},
	}

	overrideCM := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftOverrideImagesCM,
			Namespace: controller.clusterName,
		},
		Data: map[string]string{
			util.ImageStreamAwsCapiProvider:      `ImageStreamAwsCapiProvider-1`,
			util.ImageStreamAgentCapiProvider:    `ImageStreamAgentCapiProvider-1`,
			util.ImageStreamAwsEncyptionProvider: `ImageStreamAwsEncyptionProvider-1`,
			util.ImageStreamAzureCapiProvider:    `ImageStreamAzureCapiProvider-1`,
			util.ImageStreamClusterApi:           `ImageStreamClusterApi-1`,
			util.ImageStreamKonnectivity:         `ImageStreamKonnectivity-1`,
			util.ImageStreamKubevertCapiProvider: `ImageStreamKubevertCapiProvider-1`,
			util.ImageStreamHypershiftOperator:   `ImageStreamHypershiftOperator-1`,
		},
	}
	controller.hubClient.Create(ctx, overrideCM)

	assert.Eventually(t, func() bool {
		theConfigMap := &corev1.ConfigMap{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftOverrideImagesCM}, theConfigMap)
		return err == nil
	}, 10*time.Second, 1*time.Second, "The test image override configmap was created successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The image override configmap has changed. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")
	controller.Stop()

	changedOverrideCM := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftOverrideImagesCM,
			Namespace: controller.clusterName,
		},
		Data: map[string]string{
			util.ImageStreamAwsCapiProvider:      `ImageStreamAwsCapiProvider-2`,
			util.ImageStreamAgentCapiProvider:    `ImageStreamAgentCapiProvider-2`,
			util.ImageStreamAwsEncyptionProvider: `ImageStreamAwsEncyptionProvider-2`,
			util.ImageStreamAzureCapiProvider:    `ImageStreamAzureCapiProvider-2`,
			util.ImageStreamClusterApi:           `ImageStreamClusterApi-2`,
			util.ImageStreamKonnectivity:         `ImageStreamKonnectivity-1`,
			util.ImageStreamKubevertCapiProvider: `ImageStreamKubevertCapiProvider-1`,
			util.ImageStreamHypershiftOperator:   `ImageStreamHypershiftOperator-1`,
		},
	}

	controller.hubClient.Update(ctx, changedOverrideCM)

	assert.Eventually(t, func() bool {
		theConfigMap := &corev1.ConfigMap{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftOverrideImagesCM}, theConfigMap)
		if err == nil {
			return theConfigMap.Data[util.ImageStreamAwsCapiProvider] == "ImageStreamAwsCapiProvider-2"
		}
		return false
	}, 10*time.Second, 1*time.Second, "The image override configmap was updated successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The image override configmap was updated. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.hubClient.Delete(ctx, overrideCM)

	assert.Eventually(t, func() bool {
		theConfigMap := &corev1.ConfigMap{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftOverrideImagesCM}, theConfigMap)
		return errors.IsNotFound(err)
	}, 10*time.Second, 1*time.Second, "The image override configmap was deleted successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The image override configmap was removed. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")
	controller.Stop()

	controller.hubClient = initErrorClient()
	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The agent fails to get the image override configmap from the hub. The hypershift operator should not be re-installed")
	controller.Stop()
}

func TestBucketSecretChanges(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	controller := &UpgradeController{
		spokeUncachedClient:       client,
		hubClient:                 client,
		log:                       zapr.NewLogger(zapLog),
		addonNamespace:            "addon",
		operatorImage:             "my-test-image",
		clusterName:               "cluster1",
		pullSecret:                "pull-secret",
		hypershiftInstallExecutor: &HypershiftTestCliExecutor{},
		bucketSecret:              corev1.Secret{},
	}

	newBucketSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftBucketSecretName,
			Namespace: controller.clusterName,
		},
		Data: map[string][]byte{
			"bucket":      []byte(`my-bucket`),
			"region":      []byte(`us-east-1`),
			"credentials": []byte(`myCredential`),
		},
	}
	controller.hubClient.Create(ctx, newBucketSecret)

	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftBucketSecretName}, theSecret)
		return err == nil
	}, 10*time.Second, 1*time.Second, "The test bucket secret was created successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The bucket secret has changed. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")
	controller.Stop()

	changedBucketSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftBucketSecretName,
			Namespace: controller.clusterName,
		},
		Data: map[string][]byte{
			"bucket":      []byte(`my-bucket`),
			"region":      []byte(`us-east-1`),
			"credentials": []byte(`myNewCredential`),
		},
	}

	controller.hubClient.Update(ctx, changedBucketSecret)

	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftBucketSecretName}, theSecret)
		if err == nil {
			return string(theSecret.Data["credentials"]) == "myNewCredential"
		}
		return false
	}, 10*time.Second, 1*time.Second, "The bucket secret was updated successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The bucket secret was updated. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.hubClient.Delete(ctx, newBucketSecret)

	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftBucketSecretName}, theSecret)
		return errors.IsNotFound(err)
	}, 10*time.Second, 1*time.Second, "The test bucket secret was deleted successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The bucket secret was removed. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")
	controller.Stop()

	controller.hubClient = initErrorClient()
	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The agent fails to get the bucket secret from the hub. The hypershift operator should not be re-installed")
	controller.Stop()
}

func TestExtDnsSecretChanges(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	controller := &UpgradeController{
		spokeUncachedClient:       client,
		hubClient:                 client,
		log:                       zapr.NewLogger(zapLog),
		addonNamespace:            "addon",
		operatorImage:             "my-test-image",
		clusterName:               "cluster1",
		pullSecret:                "pull-secret",
		hypershiftInstallExecutor: &HypershiftTestCliExecutor{},
		extDnsSecret:              corev1.Secret{},
	}

	newExtDnsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftExternalDNSSecretName,
			Namespace: controller.clusterName,
		},
		Data: map[string][]byte{
			"domain-filter": []byte(`my.domain.filter`),
			"provider":      []byte(`aws`),
			"txt-owner-id":  []byte(`my-txt-owner-id`),
			"credentials":   []byte(`myCredential`),
		},
	}
	controller.hubClient.Create(ctx, newExtDnsSecret)

	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftExternalDNSSecretName}, theSecret)
		return err == nil
	}, 10*time.Second, 1*time.Second, "The test external DNS secret was created successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The external DNS secret has changed. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")
	controller.Stop()

	changedExtDnsSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftExternalDNSSecretName,
			Namespace: controller.clusterName,
		},
		Data: map[string][]byte{
			"domain-filter": []byte(`my.domain.filter`),
			"provider":      []byte(`aws`),
			"txt-owner-id":  []byte(`my-txt-owner-id`),
			"credentials":   []byte(`myNewCredential`),
		},
	}

	controller.hubClient.Update(ctx, changedExtDnsSecret)

	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftExternalDNSSecretName}, theSecret)
		if err == nil {
			return string(theSecret.Data["credentials"]) == "myNewCredential"
		}
		return false
	}, 10*time.Second, 1*time.Second, "The external DNS secret was updated successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The external DNS secret was updated. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.hubClient.Delete(ctx, newExtDnsSecret)

	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftExternalDNSSecretName}, theSecret)
		return errors.IsNotFound(err)
	}, 10*time.Second, 1*time.Second, "The test external DNS secret was deleted successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The external DNS secret was removed. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")
	controller.Stop()

	controller.hubClient = initErrorClient()
	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The agent fails to get the external DNS secret from the hub. The hypershift operator should not be re-installed")
	controller.Stop()
}

func TestPrivateLinkSecretChanges(t *testing.T) {
	ctx := context.Background()

	zapLog, _ := zap.NewDevelopment()
	client := initClient()
	controller := &UpgradeController{
		spokeUncachedClient:       client,
		hubClient:                 client,
		log:                       zapr.NewLogger(zapLog),
		addonNamespace:            "addon",
		operatorImage:             "my-test-image",
		clusterName:               "cluster1",
		pullSecret:                "pull-secret",
		hypershiftInstallExecutor: &HypershiftTestCliExecutor{},
		privateLinkSecret:         corev1.Secret{},
	}

	newPrivateLinkSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftPrivateLinkSecretName,
			Namespace: controller.clusterName,
		},
		Data: map[string][]byte{
			"region": []byte(`us-east-1`),
		},
	}
	controller.hubClient.Create(ctx, newPrivateLinkSecret)

	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftPrivateLinkSecretName}, theSecret)
		return err == nil
	}, 10*time.Second, 1*time.Second, "The test private link secret was created successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The private link secret has changed. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")
	controller.Stop()

	controller.startup = true
	controller.installfailed = false
	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.startup && controller.installfailed
	}, 3*time.Minute, 10*time.Second, "Nothing has changed, but Startup=true. The hypershift operator needs to be re-installed")
	controller.Stop()

	changedPrivateLinkSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftPrivateLinkSecretName,
			Namespace: controller.clusterName,
		},
		Data: map[string][]byte{
			"region": []byte(`us-west-1`),
		},
	}

	controller.hubClient.Update(ctx, changedPrivateLinkSecret)

	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftPrivateLinkSecretName}, theSecret)
		if err == nil {
			return string(theSecret.Data["region"]) == "us-west-1"
		}
		return false
	}, 10*time.Second, 1*time.Second, "The private link secret was updated successfully")

	controller.startup = false
	controller.installfailed = false
	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The private link secret was updated. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.hubClient.Delete(ctx, newPrivateLinkSecret)

	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftPrivateLinkSecretName}, theSecret)
		return errors.IsNotFound(err)
	}, 10*time.Second, 1*time.Second, "The test private link secret was deleted successfully")

	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The private link secret was removed. The hypershift operator needs to be re-installed")
	controller.Stop()

	controller.startup = false
	controller.installfailed = false
	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")
	controller.Stop()

	controller.hubClient = initErrorClient()
	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The agent fails to get the private link secret from the hub. The hypershift operator should not be re-installed")
	controller.Stop()
}
