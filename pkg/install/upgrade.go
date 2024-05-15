package install

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
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
	installFlagsConfigmap     corev1.ConfigMap
	reinstallNeeded           bool // this is used only for code test
	awsPlatform               bool // this is used only for code test
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
		if strings.EqualFold(os.Getenv("DISABLE_HO_MANAGEMENT"), "true") {
			c.log.Info("hypershift operator management is disabled. Skip installing or upgrading the hypershift operator")
		} else {
			c.log.Info(fmt.Sprintf("check if HyperShift operator re-installation is required (startup=%v, installfailed=%v)", c.startup, c.installfailed))

			c.reinstallNeeded = false
			if c.startup || c.installfailed || c.installOptionsChanged() || c.upgradeImageCheck() {
				c.reinstallNeeded = true
				c.log.Info("hypershift operator re-installation is required")
				if err := c.runHypershiftInstall(c.ctx, c.startup); err != nil {
					c.log.Error(err, "failed to install hypershift operator")

					c.installfailed = true
					metrics.InstallationFailningGaugeBool.Set(float64(1))
				} else {
					c.installfailed = false
					c.startup = false
					metrics.InstallationFailningGaugeBool.Set(0)
				}
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
	newBucketSecret, err := c.getSecretFromHub(util.HypershiftBucketSecretName)
	if err == nil && c.secretDataChanged(newBucketSecret, c.bucketSecret, util.HypershiftBucketSecretName) {
		c.bucketSecret = newBucketSecret // save the new secret for the next cycle of comparison
		return true
	}

	// check for changes in external DNS secret
	newExtDnsSecret, err := c.getSecretFromHub(util.HypershiftExternalDNSSecretName)
	if err == nil && c.secretDataChanged(newExtDnsSecret, c.extDnsSecret, util.HypershiftExternalDNSSecretName) {
		c.extDnsSecret = newExtDnsSecret // save the new secret for the next cycle of comparison
		return true
	}

	// check for changes in AWS private link secret
	newPrivateLinkSecret, err := c.getSecretFromHub(util.HypershiftPrivateLinkSecretName)
	if err == nil && c.secretDataChanged(newPrivateLinkSecret, c.privateLinkSecret, util.HypershiftPrivateLinkSecretName) {
		c.privateLinkSecret = newPrivateLinkSecret // save the new secret for the next cycle of comparison
		return true
	}

	// check for changes in hypershift operator installation flags configmap
	newInstallFlagsCM, err := c.getConfigMapFromHub(util.HypershiftInstallFlagsCM)
	if err == nil && c.configmapDataChanged(newInstallFlagsCM, c.installFlagsConfigmap, util.HypershiftInstallFlagsCM) {
		c.installFlagsConfigmap = newInstallFlagsCM // save the new configmap for the next cycle of comparison
		return true
	}

	return false
}

func (c *UpgradeController) getSecretFromHub(secretName string) (corev1.Secret, error) {
	secretKey := types.NamespacedName{Name: secretName, Namespace: c.clusterName}
	newSecret := &corev1.Secret{}
	if err := c.hubClient.Get(context.TODO(), secretKey, newSecret); err != nil && !errors.IsNotFound(err) {
		c.log.Error(err, "failed to get secret from the hub: ")
		// Update hub secret sync metrics count
		metrics.HubResourceSyncFailureCount.WithLabelValues("secret").Inc()
		return *newSecret, err
	}
	return *newSecret, nil
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
	newImageOverrideConfigmap, err := c.getConfigMapFromHub(util.HypershiftOverrideImagesCM)
	if err == nil && c.configmapDataChanged(newImageOverrideConfigmap, c.imageOverrideConfigmap, util.HypershiftOverrideImagesCM) {
		c.imageOverrideConfigmap = newImageOverrideConfigmap // save the new configmap for the next cycle of comparison
		return true
	}
	return false
}

func (c *UpgradeController) getConfigMapFromHub(cmName string) (corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	cmKey := types.NamespacedName{Name: cmName, Namespace: c.clusterName}
	if err := c.hubClient.Get(context.TODO(), cmKey, cm); err != nil && !errors.IsNotFound(err) {
		c.log.Error(err, "failed to get configmap from the hub: ")
		return *cm, err
	}
	return *cm, nil
}

func (c *UpgradeController) configmapDataChanged(oldCM, newCM corev1.ConfigMap, cmName string) bool {
	// If changed, we want to re-install the operator
	if !reflect.DeepEqual(oldCM.Data, newCM.Data) {
		c.log.Info(fmt.Sprintf("the configmap(%s) has changed", cmName))
		return true
	}
	return false
}
