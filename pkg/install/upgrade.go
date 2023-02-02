package install

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stolostron/hypershift-addon-operator/pkg/metrics"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
)

type UpgradeController struct {
	hubClient                 client.Client
	spokeUncachedClient       client.Client
	log                       logr.Logger
	addonName                 string
	addonNamespace            string
	clusterName               string
	operatorImage             string
	pullSecret                string
	withOverride              bool
	hypershiftInstallExecutor HypershiftInstallExecutorInterface
	stopch                    chan struct{}
	ctx                       context.Context
	bucketSecret              corev1.Secret
	extDnsSecret              corev1.Secret
	privateLinkSecret         corev1.Secret
	imageOverrideConfigmap    corev1.ConfigMap
	reinstallNeeded           bool // this is used only for code test
	startup                   bool //
	installfailed             bool // previous installation failed - retry needed on next attempt
}

func NewUpgradeController(hubClient, spokeClient client.Client, logger logr.Logger, addonName, addonNamespace, clusterName, operatorImage,
	pullSecretName string, withOverride bool, context context.Context) *UpgradeController {
	return &UpgradeController{
		hubClient:                 hubClient,
		spokeUncachedClient:       spokeClient,
		log:                       logger,
		addonName:                 addonName,
		addonNamespace:            addonNamespace,
		clusterName:               clusterName,
		operatorImage:             operatorImage,
		pullSecret:                pullSecretName,
		withOverride:              withOverride,
		hypershiftInstallExecutor: &HypershiftLibExecutor{},
		ctx:                       context,
		startup:                   true,
	}
}

func (c *UpgradeController) Start() {
	// do nothing if already started
	if c.stopch != nil {
		return
	}

	c.stopch = make(chan struct{})

	go wait.Until(func() {
		c.log.Info(fmt.Sprintf("check if HyperShift operator re-installation is required (startup=%v, installfailed=%v)", c.startup, c.installfailed))

		c.reinstallNeeded = false
		if c.startup || c.installfailed || c.installOptionsChanged() || c.upgradeImageCheck() {
			c.reinstallNeeded = true
			c.log.Info("hypershift operator re-installation is required")
			if err := c.runHypershiftInstall(c.ctx, c.startup); err != nil {
				c.log.Error(err, "failed to install hypershift operator")

				c.installfailed = true
			} else {
				c.installfailed = false
				c.startup = false
			}
		}
	}, 2*time.Minute, c.stopch) // Connect to the hub every 2 minutes to check for any changes
}

func (c *UpgradeController) Stop() {
	close(c.stopch)

	c.stopch = nil
}

func (c *UpgradeController) installOptionsChanged() bool {
	// check for changes in AWS S3 bucket secret
	newBucketSecret := c.getSecretFromHub(util.HypershiftBucketSecretName)
	if c.secretDataChanged(newBucketSecret, c.bucketSecret, util.HypershiftBucketSecretName) {
		c.bucketSecret = newBucketSecret // save the new secret for the next cycle of comparison
		return true
	}

	// check for changes in external DNS secret
	newExtDnsSecret := c.getSecretFromHub(util.HypershiftExternalDNSSecretName)
	if c.secretDataChanged(newExtDnsSecret, c.extDnsSecret, util.HypershiftExternalDNSSecretName) {
		c.extDnsSecret = newExtDnsSecret // save the new secret for the next cycle of comparison
		return true
	}

	// check for changes in AWS private link secret
	newPrivateLinkSecret := c.getSecretFromHub(util.HypershiftPrivateLinkSecretName)
	if c.secretDataChanged(newPrivateLinkSecret, c.privateLinkSecret, util.HypershiftPrivateLinkSecretName) {
		c.privateLinkSecret = newPrivateLinkSecret // save the new secret for the next cycle of comparison
		return true
	}

	return false
}

func (c *UpgradeController) getSecretFromHub(secretName string) corev1.Secret {
	secretKey := types.NamespacedName{Name: secretName, Namespace: c.clusterName}
	newSecret := &corev1.Secret{}
	if err := c.hubClient.Get(context.TODO(), secretKey, newSecret); err != nil && !errors.IsNotFound(err) {
		c.log.Error(err, "failed to get secret from the hub: ")
		// Update hub secret sync metrics count
		metrics.HubResourceSyncFailureCount.WithLabelValues("secret").Inc()
	}
	return *newSecret
}

func (c *UpgradeController) secretDataChanged(oldSecret, newSecret corev1.Secret, secretName string) bool {
	if !reflect.DeepEqual(oldSecret.Data, newSecret.Data) { // compare only the secret data
		c.log.Info(fmt.Sprintf("secret(%s) has changed", secretName))
		return true
	}
	return false
}

func (c *UpgradeController) upgradeImageCheck() bool {
	// Get the image override configmap from the hub and compare it to the controller's cached image override configmap
	newImageOverrideConfigmap := c.getImageOverrideMapFromHub()

	// If changed, we want to re-install the operator
	if !reflect.DeepEqual(c.imageOverrideConfigmap.Data, newImageOverrideConfigmap.Data) {
		c.log.Info(fmt.Sprintf("the image override configmap(%s) has changed", util.HypershiftOverrideImagesCM))
		c.imageOverrideConfigmap = newImageOverrideConfigmap // save the new configmap for the next cycle of comparison
		return true
	}

	return false
}

func (c *UpgradeController) getImageOverrideMapFromHub() corev1.ConfigMap {
	overrideImagesCm := &corev1.ConfigMap{}
	overrideImagesCmKey := types.NamespacedName{Name: util.HypershiftOverrideImagesCM, Namespace: c.clusterName}
	if err := c.hubClient.Get(context.TODO(), overrideImagesCmKey, overrideImagesCm); err != nil && !errors.IsNotFound(err) {
		c.log.Error(err, "failed to get configmap from the hub: ")
		// Update hub image override configmap sync metrics count
		metrics.HubResourceSyncFailureCount.WithLabelValues("configmap").Inc()
	}
	return *overrideImagesCm
}
