package install

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	imageapi "github.com/openshift/api/image/v1"
	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"

	"github.com/stolostron/hypershift-addon-operator/pkg/metrics"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
)

var (
	hypershiftOperatorKey = types.NamespacedName{
		Name:      util.HypershiftOperatorName,
		Namespace: util.HypershiftOperatorNamespace,
	}
)

func (c *UpgradeController) RunHypershiftCmdWithRetires(
	ctx context.Context, attempts int, sleep time.Duration, f func(context.Context) error) error {
	var err error
	for i := attempts; i > 0; i-- {
		err = f(ctx)

		if err == nil {
			return nil
		}

		c.log.Error(err, "failed to run hypershift cmd")

		// Add some randomness to prevent creating a Thundering Herd
		jitter := time.Duration(getRandInt(int64(sleep)))
		sleep = sleep + jitter/2
		time.Sleep(sleep)

		sleep = 2 * sleep

	}

	return fmt.Errorf("failed to install the hypershift operator and apply its crd after %v retires, err: %w", attempts, err)
}

func getRandInt(m int64) int64 {
	n, err := rand.Int(rand.Reader, big.NewInt(m))
	if err != nil {
		return m
	}

	return n.Int64()
}

func (c *UpgradeController) runHypershiftRender(ctx context.Context, args []string) ([]unstructured.Unstructured, error) {
	out := []unstructured.Unstructured{}
	if c.hypershiftInstallExecutor == nil {
		return out, fmt.Errorf("failed to run hypershift cmd, no install executor specified")
	}

	renderTemplate, err := c.hypershiftInstallExecutor.Execute(ctx, args)
	if err != nil {
		return out, err
	}

	d := map[string]interface{}{}
	if err := json.Unmarshal(renderTemplate, &d); err != nil {
		return out, fmt.Errorf("failed to Unmarshal, err: %w", err) // this is likely an unrecoverable
	}

	items, ok := d["items"].([]interface{})
	if !ok {
		return out, fmt.Errorf("failed to Unmarshal template items")
	}

	for _, item := range items {
		u := unstructured.Unstructured{}

		v, ok := item.(map[string]interface{})
		if !ok {
			return out, fmt.Errorf("failed to type assert an item: %v", item)
		}

		u.SetUnstructuredContent(v)

		out = append(out, u)
	}

	return out, nil
}

func (c *UpgradeController) RunHypershiftCleanup(ctx context.Context) error {
	c.log.Info("enter RunHypershiftCleanup")
	defer c.log.Info("exit RunHypershiftCleanup")

	if !c.isHypershiftOperatorByMCE(ctx) {
		c.log.Info("skip deletion of the hypershift operator, not managed by mce")
		return nil
	}

	hasHCs, err := c.hasHostedClusters(ctx)
	if err != nil {
		c.log.Error(err, "failed to list the hostedclusters")
		return err
	}
	if hasHCs {
		c.log.Info("skip deletion of the hypershift operator, there are existing HostedClusters")
		return nil
	}

	args := []string{
		"render",
		"--hypershift-image", c.operatorImage,
		"--namespace", hypershiftOperatorKey.Namespace,
		"--format", "json",
	}

	items, err := c.runHypershiftRender(ctx, args)
	if err != nil {
		return err
	}

	for _, item := range items {
		item := item
		if err := c.spokeUncachedClient.Delete(ctx, &item); err != nil && !apierrors.IsNotFound(err) {
			c.log.Error(err, fmt.Sprintf("failed to delete %s, %s", item.GetKind(), client.ObjectKeyFromObject(&item)))
		}
	}

	return nil
}

func (c *UpgradeController) RunHypershiftOperatorInstallOnAgentStartup(ctx context.Context) error {
	c.log.Info("enter RunHypershiftOperatorInstallOnAgentStartup")
	defer c.log.Info("exit RunHypershiftOperatorInstallOnAgentStartup")

	return c.runHypershiftInstall(ctx, true)
}

func (c *UpgradeController) RunHypershiftOperatorUpdate(ctx context.Context) error {
	c.log.Info("enter RunHypershiftOperatorUpdate")
	defer c.log.Info("exit RunHypershiftOperatorUpdate")

	return c.runHypershiftInstall(ctx, false)
}

func (c *UpgradeController) runHypershiftInstall(ctx context.Context, controllerStartup bool) error {
	err, ok, operatorDeployment := c.operatorUpgradable(ctx)

	if err != nil {
		return err
	} else if !ok {
		c.log.Info("hypershift operator exists but not deployed by addon, skip update")
		return nil
	}

	// Initially set this to zero to indicate that the AWS S3 bucket secret is not used for the operator installation
	metrics.IsAWSS3BucketSecretConfigured.Set(0)

	// If the hypershift operator installation already exists and it is a controller initial start up,
	// we need to check if the operator re-installation is necessary by comparing the operator images.
	// If the hypershift operator installation failed in the previous attempt, no checking. We need to
	// install again.
	// For now, assume that secrets did not change (MCE upgrade or pod re-cycle scenarios)
	// If controllerStartup = false, we are here because the image override configmap or some operator secrets have changed. No check required.
	reinstallCheckRequired := (operatorDeployment != nil) && controllerStartup && !c.installfailed

	c.log.Info("reinstallCheckRequired = " + strconv.FormatBool(reinstallCheckRequired))

	// Seed the hypershift namespace, the uninstall will remove this namespace.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: hypershiftOperatorKey.Namespace}}
	if err := c.spokeUncachedClient.Get(ctx, client.ObjectKeyFromObject(ns), ns); err != nil {
		if apierrors.IsNotFound(err) {
			if err := c.spokeUncachedClient.Create(ctx, ns); err != nil {
				return err
			}
			// If the hypershift operator namespace does not exist, this is the initial installation.
			reinstallCheckRequired = false
		} else {
			return err
		}
	}

	// Copy image pull secret to the HyperShift namespace
	if c.pullSecret != "" {
		if err := c.ensurePullSecret(ctx); err != nil {
			return fmt.Errorf("failed to deploy pull secret to hypershift namespace, err: %w", err)
		}
	}

	awsPlatform := false
	oidcBucket := true
	privateLinkCreds := true

	//Attempt to retrieve oidc bucket secret
	bucketSecretKey := types.NamespacedName{Name: util.HypershiftBucketSecretName, Namespace: c.clusterName}
	se := &corev1.Secret{}
	if err := c.hubClient.Get(ctx, bucketSecretKey, se); err != nil {
		c.log.Info(fmt.Sprintf("bucket secret(%s) not found on the hub.", bucketSecretKey))

		oidcBucket = false
	}

	//Attempt to retrieve private link creds
	privateSecretKey := types.NamespacedName{Name: util.HypershiftPrivateLinkSecretName, Namespace: c.clusterName}
	spl := &corev1.Secret{}
	if err := c.hubClient.Get(ctx, privateSecretKey, spl); err != nil {
		c.log.Info(fmt.Sprintf("private link secret(%s) not found on the hub.", privateSecretKey))

		privateLinkCreds = false
	}

	//Platform is aws if either secret exists
	awsPlatform = oidcBucket || privateLinkCreds
	c.awsPlatform = awsPlatform
	if !awsPlatform {
		c.log.Info(fmt.Sprintf("bucket secret(%s) and private link secret(%s) not found on the hub, installing hypershift operator for non-AWS platform.", bucketSecretKey, privateSecretKey))
	}

	args := []string{
		"--namespace", hypershiftOperatorKey.Namespace,
	}

	if oidcBucket { // if the S3 secret is found, install hypershift with s3 options
		bucketName := string(se.Data["bucket"])
		bucketRegion := string(se.Data["region"])

		if bucketName == "" {
			return fmt.Errorf("hypershift-operator-oidc-provider-s3-credentials does not contain a bucket key")
		}

		c.log.Info("oidc s3 bucket, region & credential arguments included")
		awsArgs := []string{
			"--oidc-storage-provider-s3-bucket-name", bucketName,
			"--oidc-storage-provider-s3-region", bucketRegion,
			"--oidc-storage-provider-s3-secret", util.HypershiftBucketSecretName,
		}
		args = append(args, awsArgs...)

		// Set this to one to indicate that the AWS S3 bucket secret is used for the operator installation
		metrics.IsAWSS3BucketSecretConfigured.Set(1)

	}

	if privateLinkCreds { // if private link credentials is found, install hypershift with private secret options
		c.log.Info("private link region & credential arguments included")
		awsArgs := []string{
			"--aws-private-secret", util.HypershiftPrivateLinkSecretName,
			"--aws-private-region", string(spl.Data["region"]),
			"--private-platform", "AWS",
		}
		args = append(args, awsArgs...)

	}

	//External DNS
	extDNSSecretKey := types.NamespacedName{Name: util.HypershiftExternalDNSSecretName, Namespace: c.clusterName}
	sExtDNS := &corev1.Secret{}
	if err := c.hubClient.Get(ctx, extDNSSecretKey, sExtDNS); err == nil {

		c.log.Info("external dns provider & domain-filter arguments included")
		awsArgs := []string{
			"--external-dns-secret", util.HypershiftExternalDNSSecretName,
			"--external-dns-domain-filter", string(sExtDNS.Data["domain-filter"]),
			"--external-dns-provider", string(sExtDNS.Data["provider"]),
		}
		args = append(args, awsArgs...)
		if txtOwnerId, exists := sExtDNS.Data["txt-owner-id"]; exists {
			args = append(args, "--external-dns-txt-owner-id", string(txtOwnerId))
		}
	} else {
		c.log.Info(fmt.Sprintf("external dns secret(%s) was not found", extDNSSecretKey))
	}

	// Get the hypershift operator installation flags configmap from the hub
	installFlagsCM, _ := c.getConfigMapFromHub(util.HypershiftInstallFlagsCM)
	c.installFlagsConfigmap = installFlagsCM

	hypershiftImage := c.operatorImage
	imageStreamCMData := make(map[string]string, 0)
	if c.withOverride {
		im, err := c.readInDownstreamOverride()
		if err != nil {
			return fmt.Errorf("failed to read the downstream image override configmap, err: %w", err)
		}

		imageStreamCMData[util.HypershiftDownstreamOverride] = string(im)

		hypershiftImage = getHyperShiftOperatorImage(im)
		args = append(args, "--image-refs", filepath.Join(os.TempDir(), util.HypershiftDownstreamOverride))

		// compare installed operator images to the new image stream
		// If they are the same, skip re-install.
		if reinstallCheckRequired &&
			!(c.operatorImagesUpdated(im, *operatorDeployment) || c.configmapDataUpdated(util.HypershiftInstallFlagsCM, installFlagsCM)) {
			c.log.Info("no change in hypershift operator images and install flags, skipping hypershift operator installation")
			return nil
		}
	} else {
		args = append(args, "--hypershift-image", hypershiftImage)
	}

	// migrate the install flags that we used to add by default
	// and add the rest of operator installation flags user specified
	args = append(args, c.buildOtherInstallFlags(installFlagsCM)...)

	// Emit metrics to indicate that hypershift operator installation is in progress
	metrics.InInstallationOrUpgradeBool.Set(1)

	job, err := c.runHyperShiftInstallJob(ctx, hypershiftImage, os.TempDir(), imageStreamCMData, args)
	if err != nil {
		// Emit metrics to indicate that hypershift operator installation is over
		metrics.InInstallationOrUpgradeBool.Set(0)
		// Emit metrics to return the number of hypershift operator installation
		// failures since the last successful installation
		metrics.InstallationOrUpgradeFailedCount.Inc()
		return err
	}

	if jobSucceeded, err := c.isInstallJobSuccessful(ctx, job.Name); !jobSucceeded || err != nil {
		if err != nil {
			// Emit metrics to indicate that hypershift operator installation is over
			metrics.InInstallationOrUpgradeBool.Set(0)
			// Emit metrics to return the number of hypershift operator installation failures
			// since the last successful installation
			metrics.InstallationOrUpgradeFailedCount.Inc()
			return err
		}

		// Emit metrics to indicate that hypershift operator installation is over
		metrics.InInstallationOrUpgradeBool.Set(0)
		// Emit metrics to return the number of hypershift operator installation
		// failures since the last successful installation
		metrics.InstallationOrUpgradeFailedCount.Inc()
		return fmt.Errorf("install HyperShift job failed")
	}
	c.log.Info(fmt.Sprintf("HyperShift install job: %s completed successfully", job.Name))

	// Emit metrics to indicate that hypershift operator installation is over
	metrics.InInstallationOrUpgradeBool.Set(0)
	// Reset the number of hypershift operator installation failures
	// since the last successful installation
	metrics.InstallationOrUpgradeFailedCount.Set(0)

	if err := c.saveConfigmapLocally(ctx, &installFlagsCM); err != nil { // hypershift operator installation flags
		return err
	}

	// Add label and update image pull secret in Hypershift deployment
	err = c.updateHyperShiftDeployment(ctx)
	if err != nil {
		c.log.Error(err, "failed to update the hypershift operator deployment")
	}

	return nil
}

func (c *UpgradeController) buildOtherInstallFlags(installFlagsCM corev1.ConfigMap) []string {
	flagsToAdd := []string{}
	flagsToRemove := []string{}

	if installFlagsCM.Data != nil {
		c.log.Info(fmt.Sprintf("found %s configmap for the hypershift operator installation", util.HypershiftInstallFlagsCM))
		flagsToAdd = strings.Fields(installFlagsCM.Data["installFlagsToAdd"])
		flagsToRemove = strings.Fields(installFlagsCM.Data["installFlagsToRemove"])
	}

	args := []string{}

	//Enable control plane telemetry forwarding
	telemetryArgs := []string{
		"--platform-monitoring", "OperatorOnly",
	}
	if !contains(flagsToRemove, "--platform-monitoring") { // if the flagsToRemove contains --platform-monitoring, do not add it
		if contains(flagsToAdd, "--platform-monitoring") { // if the flagsToAdd contains --platform-monitoring, get the value from the list
			val := getParamValue(flagsToAdd, "--platform-monitoring")
			if val == "" {
				c.log.Info("--platform-monitoring does not have any value in installParamsToAdd, setting it to default [ --platform-monitoringOperatorOnly ]")
				args = append(args, telemetryArgs...)
			} else {
				c.log.Info(fmt.Sprintf("updating the install flag [ --platform-monitoring %s ]", val))
				args = append(args, []string{
					"--platform-monitoring", val,
				}...)
			}
		} else {
			args = append(args, telemetryArgs...)
		}
	}

	// Enable RHOBS
	if strings.EqualFold(os.Getenv("RHOBS_MONITORING"), "true") {
		c.log.Info("RHOBS_MONITORING=true, adding --rhobs-monitoring true --metrics-set SRE")
		rhobsArgs := []string{
			"--rhobs-monitoring",
			"true",
			"--metrics-set",
			"SRE",
		}
		args = append(args, rhobsArgs...)
	} else {
		// add --enable-uwm-telemetry-remote-write only if RHOBS monitoring is not enabled
		uwmArgs := []string{
			"--enable-uwm-telemetry-remote-write",
		}
		if contains(flagsToRemove, "--enable-uwm-telemetry-remote-write") {
			c.log.Info("installFlagsToRemove contains --enable-uwm-telemetry-remote-write, removing it from the install flag list")
		} else {
			args = append(args, uwmArgs...)
		}
	}

	// now add the rest of installParamsToAdd
	for _, flag := range flagsToAdd {
		// if the string is a flag key having -- prefix and not already added to the args
		if strings.HasPrefix(flag, "--") && !contains(args, flag) {
			flagVal := getParamValue(flagsToAdd, flag)
			flagArgs := []string{flag}
			if flagVal != "" {
				flagArgs = append(flagArgs, flagVal)
			}
			args = append(args, flagArgs...)
			c.log.Info(fmt.Sprintf("install flag [ %v ] added", flagArgs))
		}
	}

	return args
}

func contains(theList []string, flagToFind string) bool {
	for _, flag := range theList {
		if flag == flagToFind {
			return true
		}
	}
	return false
}

func getParamValue(s []string, e string) string {
	for i, a := range s {
		if a == e {
			if i == (len(s) - 1) {
				return ""
			}
			if !strings.HasPrefix(s[i+1], "--") {
				return s[i+1]
			} else {
				return ""
			}
		}
	}
	return ""
}

func (c *UpgradeController) createOrUpdateAwsSpokeSecret(ctx context.Context, hubSecret *corev1.Secret, regionRequired bool) error {
	spokeSecret := hubSecret.DeepCopy()

	region := hubSecret.Data["region"]
	awsSecretKey := hubSecret.Data["aws-secret-access-key"]
	awsKeyId := hubSecret.Data["aws-access-key-id"]
	if (hubSecret.Data["credentials"] == nil && (awsKeyId == nil || awsSecretKey == nil)) || (region == nil && regionRequired) {
		return fmt.Errorf("secret(%s/%s) does not contain a valid credential or region", hubSecret.Namespace, hubSecret.Name)
	} else {
		if awsSecretKey != nil {
			spokeSecret.Data["credentials"] = []byte(fmt.Sprintf("[default]\naws_access_key_id = %s\naws_secret_access_key = %s", awsKeyId, awsSecretKey))
		}
	}

	return c.createOrUpdateSpokeSecret(ctx, spokeSecret)
}

func (c *UpgradeController) createOrUpdateSpokeSecret(ctx context.Context, hubSecret *corev1.Secret) error {

	spokeSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hubSecret.Name,
			Namespace: hypershiftOperatorKey.Namespace,
		},
	}
	c.log.Info(fmt.Sprintf("createorupdate the the secret (%s/%s) on cluster %s", hypershiftOperatorKey.Namespace, hubSecret.Name, hubSecret.Namespace))
	_, err := controllerutil.CreateOrUpdate(ctx, c.spokeUncachedClient, spokeSecret, func() error {
		spokeSecret.Data = map[string][]byte{
			"credentials": hubSecret.Data["credentials"],
		}

		return nil
	})

	return err
}

func (c *UpgradeController) saveConfigmapLocally(ctx context.Context, hubConfigmap *corev1.ConfigMap) error {
	if hubConfigmap.Name != "" {
		spokeConfigmap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hubConfigmap.Name,
				Namespace: c.addonNamespace,
			},
		}
		c.log.Info(fmt.Sprintf("save the configmap (%s/%s) locally on cluster %s", c.addonNamespace, hubConfigmap.Name, c.clusterName))
		_, err := controllerutil.CreateOrUpdate(ctx, c.spokeUncachedClient, spokeConfigmap, func() error {
			spokeConfigmap.Data = hubConfigmap.Data
			return nil
		})
		return err
	}
	return nil
}

func (c *UpgradeController) ensurePullSecret(ctx context.Context) error {
	mcePullSecret := &corev1.Secret{}
	mcePullSecretKey := types.NamespacedName{Name: c.pullSecret, Namespace: c.addonNamespace}

	if err := c.spokeUncachedClient.Get(ctx, mcePullSecretKey, mcePullSecret); err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info("mce pull secret not found, skip copy it to the hypershift namespace",
				"namespace", c.addonNamespace, "name", c.pullSecret)
			return nil
		}

		return fmt.Errorf("failed to get mce pull secret, err: %w", err)
	}

	hsPullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.pullSecret,
			Namespace: hypershiftOperatorKey.Namespace,
		},
	}

	c.log.Info(fmt.Sprintf("createorupdate the pull secret (%s/%s)", hsPullSecret.Namespace, hsPullSecret.Name))
	_, err := controllerutil.CreateOrUpdate(ctx, c.spokeUncachedClient, hsPullSecret, func() error {
		hsPullSecret.Data = mcePullSecret.Data
		hsPullSecret.Type = mcePullSecret.Type

		return nil
	})

	return err
}

func (c *UpgradeController) isHypershiftOperatorByMCE(ctx context.Context) bool {
	obj := &appsv1.Deployment{}

	if err := c.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, obj); err != nil {
		c.log.Info(fmt.Sprintf("hypershift operator %s deployment not found after install, err: %v", hypershiftOperatorKey, err))
		return false
	}

	a := obj.GetAnnotations()

	return a[util.HypershiftOperatorNoMCEAnnotationKey] != "true"
}

func (c *UpgradeController) hasHostedClusters(ctx context.Context) (bool, error) {
	listopts := &client.ListOptions{}
	hcList := &hyperv1beta1.HostedClusterList{}
	if err := c.spokeUncachedClient.List(ctx, hcList, listopts); err != nil {
		return false, err
	}

	return len(hcList.Items) != 0, nil
}

func (c *UpgradeController) operatorUpgradable(ctx context.Context) (error, bool, *appsv1.Deployment) {
	obj := &appsv1.Deployment{}

	if err := c.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, nil
		}

		return err, false, nil
	}

	// Check if hypershift operator deployment has "hypershift.open-cluster-management.io/not-by-mce: true" annotation
	// With this annotation, the addon agent should not install or upgrade the hypershift operator
	if obj.Annotations[util.HypershiftOperatorNoMCEAnnotationKey] == "true" {
		return nil, false, obj
	}

	return nil, true, obj
}

func (c *UpgradeController) updateHyperShiftDeployment(ctx context.Context) error {
	obj := &appsv1.Deployment{}

	if err := c.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, obj); err != nil {
		return err
	}

	// Update pull image
	if c.pullSecret != "" {
		addonPullSecretKey := types.NamespacedName{Namespace: c.addonNamespace, Name: c.pullSecret}
		addonPullSecret := &corev1.Secret{}
		if err := c.spokeUncachedClient.Get(ctx, addonPullSecretKey, addonPullSecret); err == nil {
			if obj.Spec.Template.Spec.ImagePullSecrets == nil {
				obj.Spec.Template.Spec.ImagePullSecrets = make([]corev1.LocalObjectReference, 0)
			}
			//Make sure we do not duplicate the ImagePullSecret name
			newImagePullSecrets := append(obj.Spec.Template.Spec.ImagePullSecrets,
				corev1.LocalObjectReference{Name: c.pullSecret})
			for _, secret := range obj.Spec.Template.Spec.ImagePullSecrets {
				if secret.Name == addonPullSecret.Name {
					// Secret name reference already found, revert the ImagePullSecrets
					newImagePullSecrets = obj.Spec.Template.Spec.ImagePullSecrets
					break
				}
			}
			obj.Spec.Template.Spec.ImagePullSecrets = newImagePullSecrets
		} else {
			c.log.Info("mce pull secret not found, skip update HyperShift operator to use the pull secret")
		}
	}

	if err := c.spokeUncachedClient.Update(ctx, obj); err != nil {
		return err
	}

	return nil
}

func (c *UpgradeController) readInDownstreamOverride() ([]byte, error) {
	// This is the original image stream configmap from the MCE installer
	cm := &corev1.ConfigMap{}
	cmKey := types.NamespacedName{Name: util.HypershiftDownstreamOverride, Namespace: c.addonNamespace}

	if err := c.spokeUncachedClient.Get(context.TODO(), cmKey, cm); err != nil {
		return nil, fmt.Errorf("failed to get the downstream image override configmap, err: %w", err)
	}

	d := cm.Data[util.HypershiftOverrideKey]

	im, err := base64.StdEncoding.DecodeString(d)
	if err != nil {
		return nil, err
	}

	// This is the user provided upgrade images configmap
	// Override the image values in the installer provided imagestream with this
	imUpgradeConfigMap, _ := c.getConfigMapFromHub(util.HypershiftOverrideImagesCM)
	if imUpgradeConfigMap.Data != nil {
		c.log.Info(fmt.Sprintf("found %s configmap, overriding hypershift images in the imagestream", util.HypershiftOverrideImagesCM))

		im, err = c.getUpdatedImageStream(im, imUpgradeConfigMap.Data)
		if err != nil {
			return nil, err
		}
	}

	// cache the configmap for comparison againt the hub's to detect any change
	c.imageOverrideConfigmap = imUpgradeConfigMap

	return im, nil
}

func (c *UpgradeController) getUpdatedImageStream(im []byte, upgradeImagesMap map[string]string) ([]byte, error) {
	imObj := &imageapi.ImageStream{}
	if err := yaml.Unmarshal(im, imObj); err != nil {
		return nil, err
	}

	for k, v := range upgradeImagesMap {
		c.log.Info(fmt.Sprintf("upgrade image %s:%s", k, v))
		overrideImageInImageStream(imObj, k, v)
	}

	im, err := yaml.Marshal(imObj)
	if err != nil {
		return nil, err
	}

	return im, nil
}

func overrideImageInImageStream(imObj *imageapi.ImageStream, overrideImageName, overrideImageValue string) {
	for _, tag := range imObj.Spec.Tags {
		if tag.Name == overrideImageName {
			tag.From.Name = overrideImageValue
			break
		}
	}
}

func getHyperShiftOperatorImage(im []byte) string {
	imObj := &imageapi.ImageStream{}
	if err := yaml.Unmarshal(im, imObj); err != nil {
		return ""
	}

	for _, tag := range imObj.Spec.Tags {
		if tag.Name == util.ImageStreamHypershiftOperator {
			return tag.From.Name
		}
	}
	return ""
}

func (c *UpgradeController) operatorImagesUpdated(im []byte, operatorDeployment appsv1.Deployment) bool {
	different := false

	hsOperatorContainer := operatorDeployment.Spec.Template.Spec.Containers[0]

	imObj := &imageapi.ImageStream{}
	if err := yaml.Unmarshal(im, imObj); err != nil {
		c.log.Error(err, "failed to get image stream content")
		return false
	}

	c.log.Info("comparing hypershift operator images to the existing hypershift operator")
	for _, tag := range imObj.Spec.Tags {
		switch tag.Name {
		case util.ImageStreamAgentCapiProvider:
			if tag.From.Name != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageAgentCapiProvider) {
				c.log.Info(fmt.Sprintf("image(%s) has changed", util.ImageStreamAgentCapiProvider))
				different = true
			}
		case util.ImageStreamAwsCapiProvider:
			if tag.From.Name != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageAwsCapiProvider) {
				c.log.Info(fmt.Sprintf("image(%s) has changed", util.ImageStreamAwsCapiProvider))
				different = true
			}
		case util.ImageStreamAwsEncyptionProvider:
			if tag.From.Name != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageAwsEncyptionProvider) {
				c.log.Info(fmt.Sprintf("image(%s) has changed", util.ImageStreamAwsEncyptionProvider))
				different = true
			}
		case util.ImageStreamAzureCapiProvider:
			if tag.From.Name != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageAzureCapiProvider) {
				c.log.Info(fmt.Sprintf("image(%s) has changed", util.ImageStreamAzureCapiProvider))
				different = true
			}
		case util.ImageStreamClusterApi:
			if tag.From.Name != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageClusterApi) {
				c.log.Info(fmt.Sprintf("image(%s) has changed", util.ImageStreamClusterApi))
				different = true
			}
		case util.ImageStreamKonnectivity:
			if tag.From.Name != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageKonnectivity) {
				c.log.Info(fmt.Sprintf("image(%s) has changed", util.ImageStreamKonnectivity))
				different = true
			}
		case util.ImageStreamKubevertCapiProvider:
			if tag.From.Name != getContainerEnvVar(hsOperatorContainer.Env, util.HypershiftEnvVarImageKubevertCapiProvider) {
				c.log.Info(fmt.Sprintf("image(%s) has changed", util.ImageStreamKubevertCapiProvider))
				different = true
			}
		case util.ImageStreamHypershiftOperator:
			if tag.From.Name != hsOperatorContainer.Image {
				c.log.Info(fmt.Sprintf("image(%s) has changed", util.ImageStreamHypershiftOperator))
				different = true
			}
		}
	}

	return different
}

func getContainerEnvVar(envVars []corev1.EnvVar, imageName string) string {
	for _, ev := range envVars {
		if ev.Name == imageName {
			return ev.Value
		}
	}
	return ""
}

func (c *UpgradeController) configmapDataUpdated(cmName string, cm corev1.ConfigMap) bool {
	c.log.Info(fmt.Sprintf("comparing hypershift operator installation flags configmap(%s) to the locally saved configmap", cmName))
	cmKey := types.NamespacedName{Name: cmName, Namespace: c.addonNamespace}
	localCM := &corev1.ConfigMap{}
	if err := c.spokeUncachedClient.Get(context.TODO(), cmKey, localCM); err != nil && !apierrors.IsNotFound(err) {
		c.log.Error(err, "failed to find configmap:") // just log and continue
	}

	if !reflect.DeepEqual(localCM.Data, cm.Data) { // compare only the configmap data
		c.log.Info("the configmap has changed")
		return true
	}

	return false
}
