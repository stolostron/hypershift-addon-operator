package install

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	kbatch "k8s.io/api/batch/v1"
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
			"region":      []byte(`us-east-1`),
			"credentials": []byte(`my-credential-file`),
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

	//Add test to check successful installation

	controller.Start()
	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")
	controller.Stop()

	controller.startup = true
	controller.installfailed = false
	controller.Start()

	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed, but Startup=true. The hypershift operator needs to be re-installed")

	controller.Stop()

	changedPrivateLinkSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftPrivateLinkSecretName,
			Namespace: controller.clusterName,
		},
		Data: map[string][]byte{
			"region":      []byte(`us-west-1`),
			"credentials": []byte(`my-credential-file`),
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

func TestInstallFlagChanges(t *testing.T) {
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
		installFlagsConfigmap:     corev1.ConfigMap{},
	}

	addonNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: controller.addonNamespace,
		},
	}
	controller.hubClient.Create(ctx, addonNs)
	defer controller.hubClient.Delete(ctx, addonNs)

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controller.pullSecret,
			Namespace: controller.addonNamespace,
		},
		Data: map[string][]byte{
			".dockerconfigjson": []byte(`docker-pull-secret`),
		},
	}
	controller.hubClient.Create(ctx, pullSecret)

	newInstallFlagsConfigmap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HypershiftInstallFlagsCM,
			Namespace: controller.clusterName,
		},
		Data: map[string]string{
			"installFlagsToAdd":    "--platform-monitoring AWS --exclude-etcd --metrics-set SRE",
			"installFlagsToRemove": "--enable-uwm-telemetry-remote-write",
		},
	}

	controller.hubClient.Create(ctx, newInstallFlagsConfigmap)

	assert.Eventually(t, func() bool {
		theCM := &corev1.ConfigMap{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftInstallFlagsCM}, theCM)
		if err == nil {
			return string(theCM.Data["installFlagsToAdd"]) == "--platform-monitoring AWS --exclude-etcd --metrics-set SRE"
		}
		return false
	}, 10*time.Second, 1*time.Second, "The install flag configmap was created successfully")

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
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx:1.14.2",
						Ports: []corev1.ContainerPort{{ContainerPort: 80}},
					}},
				},
			},
		},
	}
	controller.hubClient.Create(ctx, dp)
	defer controller.hubClient.Delete(ctx, dp)

	err := installHyperShiftOperator(t, ctx, controller, false)
	defer deleteAllInstallJobs(ctx, controller.spokeUncachedClient, controller.addonNamespace)
	assert.Nil(t, err, "is nil if install HyperShift is successful")

	// expect the default --enable-uwm-telemetry-remote-write flag to be removed and additional flags to be created or updated
	installJobList := &kbatch.JobList{}
	err = controller.spokeUncachedClient.List(ctx, installJobList)
	if assert.Nil(t, err, "listing jobs should succeed: %s", err) {
		if assert.Equal(t, 1, len(installJobList.Items), "there should be exactly one install job") {
			installJob := installJobList.Items[0]
			expectArgs := []string{
				"--namespace", "hypershift",
				"--hypershift-image", "my-test-image",
				"--platform-monitoring", "AWS",
				"--exclude-etcd",
				"--metrics-set", "SRE",
			}
			assert.Equal(t, expectArgs, installJob.Spec.Template.Spec.Containers[0].Args, "mismatched container arguments")
		}
	}

	changedInstallFlagsConfigmap := &corev1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      util.HypershiftInstallFlagsCM,
			Namespace: controller.clusterName,
		},
		Data: map[string]string{
			"installFlagsToAdd":    "--platform-monitoring None",
			"installFlagsToRemove": "",
		},
	}

	controller.hubClient.Update(ctx, changedInstallFlagsConfigmap)

	assert.Eventually(t, func() bool {
		theCM := &corev1.ConfigMap{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftInstallFlagsCM}, theCM)
		if err == nil {
			return string(theCM.Data["installFlagsToAdd"]) == "--platform-monitoring None"
		}
		return false
	}, 10*time.Second, 1*time.Second, "The install flag configmap was updated successfully")

	controller.Start()

	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The install flags configmap has changed. The hypershift operator needs to be re-installed")

	controller.Stop()

	controller.Start()

	assert.Eventually(t, func() bool {
		return !controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "Nothing has changed. The hypershift operator does not need to be re-installed")

	controller.Stop()
}

func TestDeploymentArgMismatch(t *testing.T) {
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

	localOidcSecret := &corev1.Secret{
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
	operatorDeployment := &appsv1.Deployment{
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
					}},
				},
			},
		},
	}

	//Create deployment
	controller.hubClient.Create(ctx, operatorDeployment)
	defer controller.hubClient.Delete(ctx, operatorDeployment)

	defer deleteAllInstallJobs(ctx, controller.spokeUncachedClient, controller.addonNamespace)

	// create oidc secret
	controller.hubClient.Create(ctx, localOidcSecret)
	assert.Eventually(t, func() bool {
		theSecret := &corev1.Secret{}
		err := controller.hubClient.Get(ctx, types.NamespacedName{Namespace: controller.clusterName, Name: util.HypershiftBucketSecretName}, theSecret)
		return err == nil
	}, 10*time.Second, 1*time.Second, "The test oidc secret was created successfully")

	// Installing first time, reinstall needed
	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "First time install, \"reinstall\" needed")
	controller.Stop()

	//Remove args from deployment
	operatorDeployment.Spec.Template.Spec.Containers[0].Args = []string{"sample-arg-no-oidc"}
	if err := controller.hubClient.Update(ctx, operatorDeployment); err != nil {
		fmt.Println("could not update deployment")
	}
	// oidc secret exists but not present in deployment args, should reinstall
	controller.Start()
	assert.Eventually(t, func() bool {
		return controller.reinstallNeeded
	}, 10*time.Second, 1*time.Second, "The oidc secret exists but not among operator args. The hypershift operator needs to be re-installed")
	controller.Stop()

}
