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
		c.log.Info(fmt.Sprintf("check if HyperShift operator re-installation is required (startup=%v, installfailed=%v)", c.startup, c.installfailed))

		c.reinstallNeeded = false
		if err := c.syncHypershiftNS(); err != nil {
			c.log.Error(err, "failed to sync secrets in hypershift namespace with secrets in local-cluster namespace")
		}

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
	}, 2*time.Minute, c.stopch) // Connect to the hub every 2 minutes to check for any changes
}

func (c *UpgradeController) Stop() {
	close(c.stopch)

	c.stopch = nil
}

func (c *UpgradeController) installOptionsChanged() bool {

	// check for changes in hypershift operator installation flags configmap
	newInstallFlagsCM, err := c.getConfigMapFromHub(util.HypershiftInstallFlagsCM)
	if err == nil && c.configmapDataChanged(newInstallFlagsCM, c.installFlagsConfigmap, util.HypershiftInstallFlagsCM) {
		c.installFlagsConfigmap = newInstallFlagsCM // save the new configmap for the next cycle of comparison
		return true
	}
	// Create expected args based on secrets' existence and their values
	// Compare the expected args to the actual args
	// If they differ, reinstall
	expectedInstance := []expectedConfig{
		{
			objectName: util.HypershiftBucketSecretName,
			objectType: corev1.Secret{},
			objectArgs: []expectedArg{
				{argument: "--oidc-storage-provider-s3-bucket-name={bucket}", shouldExist: true},
				{argument: "--oidc-storage-provider-s3-region={region}", shouldExist: true},
				{argument: "--oidc-storage-provider-s3-credentials=/etc/oidc-storage-provider-s3-creds/credentials",
					shouldExist: true},
			},
			NoObjectArgs: []expectedArg{
				{argument: "--oidc-storage-provider-s3-bucket-name=", shouldExist: false},
				{argument: "--oidc-storage-provider-s3-region=", shouldExist: false},
				{argument: "--oidc-storage-provider-s3-credentials=/etc/oidc-storage-provider-s3-creds/credentials",
					shouldExist: false},
			},
			deploymentName: util.HypershiftOperatorName,
		},
		{
			objectName: util.HypershiftPrivateLinkSecretName,
			objectType: corev1.Secret{},
			objectArgs: []expectedArg{
				{argument: "--private-platform=AWS", shouldExist: true},
				{argument: "--private-platform=None", shouldExist: false},
			},
			NoObjectArgs: []expectedArg{
				{argument: "--private-platform=None", shouldExist: true},
				{argument: "--private-platform=AWS", shouldExist: false},
			},
			deploymentName: util.HypershiftOperatorName,
		},
		{
			objectName: util.HypershiftExternalDNSSecretName,
			objectType: corev1.Secret{},
			objectArgs: []expectedArg{
				{argument: "--domain-filter={domain-filter}", shouldExist: true},
				{argument: "--provider={provider}", shouldExist: true},
			},
			NoObjectArgs: []expectedArg{
				{argument: "--domain-filter={domain-filter}", shouldExist: false},
				{argument: "--provider={provider}", shouldExist: false},
			},
			deploymentName: util.HypershiftOperatorExternalDNSName,
		},
	}
	c.populateExpectedArgs(&expectedInstance)

	for _, o := range expectedInstance {
		dep, err := c.getDeployment(o.deploymentName)
		if err != nil {
			continue
		}

		deploymentArgs := dep.Spec.Template.Spec.Containers[0].Args

		if err := c.hubClient.Get(
			context.TODO(), types.NamespacedName{Name: o.objectName, Namespace: c.clusterName},
			&corev1.Secret{}); err == nil {

			if argMismatch(o.objectArgs, deploymentArgs) {
				c.log.Info(fmt.Sprintf("Mismatch between %s args and install options", o.objectName))
				return true
			}
		} else {
			if argMismatch(o.NoObjectArgs, deploymentArgs) {
				c.log.Info(fmt.Sprintf("Mismatch between %s args and install options", o.objectName))
				return true
			}
		}

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

func (c *UpgradeController) syncHypershiftNS() error {
	//Sync secrets in local-cluster namespace with secrets in hypershift namespace
	secrets := []string{util.HypershiftBucketSecretName,
		util.HypershiftPrivateLinkSecretName,
		util.HypershiftExternalDNSSecretName}
	ctx := context.TODO()

	for s := range secrets {
		secretKey := types.NamespacedName{Name: secrets[s], Namespace: c.clusterName}
		se := &corev1.Secret{}
		if err := c.hubClient.Get(ctx, secretKey, se); err != nil {
			c.log.Info(fmt.Sprintf("secret(%s) not found on the hub.", secretKey))
		} else if err := c.createOrUpdateSecret(ctx, se); err != nil {
			return err
		}

	}

	return nil
}

func (c *UpgradeController) populateExpectedArgs(toPopulate *[]expectedConfig) {
	//anything with {key} gets replaced with the value of 'key' in the secret
	tp := *toPopulate
	for e := range tp {
		secret, err := c.getSecretFromHub(tp[e].objectName)
		if err != nil {
			c.log.Info(fmt.Sprintf("secret %s is not present on the hub", tp[e].objectName))
			continue
		}
		for i, a := range tp[e].objectArgs {
			key := matchAndTrim(&a.argument)
			if key != "" {
				value := getValueFromKey(secret, key)
				tp[e].objectArgs[i].argument = a.argument + value
			}

		}
		for i, a := range tp[e].NoObjectArgs {
			key := matchAndTrim(&a.argument)
			if key != "" {
				value := getValueFromKey(secret, key)
				tp[e].NoObjectArgs[i].argument = a.argument + value
			}
		}

	}
}

func (c *UpgradeController) createOrUpdateSecret(ctx context.Context, secret *corev1.Secret) error {
	if secret.Name == util.HypershiftExternalDNSSecretName && !c.awsPlatform {
		if err := c.createOrUpdateSpokeSecret(ctx, secret); err != nil {
			return err
		}
	} else {
		c.awsPlatform = true
		if err := c.createOrUpdateAwsSpokeSecret(ctx, secret,
			secret.Name != util.HypershiftExternalDNSSecretName); err != nil {
			return err
		}

	}
	return nil
}
