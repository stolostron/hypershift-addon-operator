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
	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"

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

	if !c.isDeploymentMarked(ctx) {
		c.log.Info(fmt.Sprintf("skip deletion of the hypershift operator, not created by %s", util.AddonControllerName))
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

	// If the hypershift operator installation already exists and it is a controller initial start up,
	// we need to check if the operator re-installation is necessary by comparing the operator images.
	// For now, assume that secrets did not change (MCE upgrade or pod re-cycle scenarios)
	// If controllerStartup = false, we are here because the image override configmap or some operator secrets have changed. No check required.
	reinstallCheckRequired := (operatorDeployment != nil) && controllerStartup

	c.log.Info("reinstallCheckRequired = " + strconv.FormatBool(reinstallCheckRequired))

	awsPlatform := true

	bucketSecretKey := types.NamespacedName{Name: util.HypershiftBucketSecretName, Namespace: c.clusterName}
	se := &corev1.Secret{}
	if err := c.hubClient.Get(ctx, bucketSecretKey, se); err != nil {
		c.log.Info(fmt.Sprintf("bucket secret(%s) not found on the hub, installing hypershift operator for non-AWS platform.", bucketSecretKey))

		awsPlatform = false
	}

	// cache the bucket secret for comparison againt the hub's to detect any change
	c.bucketSecret = *se

	args := []string{
		"--namespace", hypershiftOperatorKey.Namespace,
	}

	spl := &corev1.Secret{}

	if awsPlatform { // if the S3 secret is found, install hypershift with s3 options
		bucketName := string(se.Data["bucket"])
		bucketRegion := string(se.Data["region"])

		if bucketName == "" {
			return fmt.Errorf("hypershift-operator-oidc-provider-s3-credentials does not contain a bucket key")
		}

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

		if err := c.createAwsSpokeSecret(ctx, se, true); err != nil {
			return err
		}
		c.log.Info("oidc s3 bucket, region & credential arguments included")
		awsArgs := []string{
			"--oidc-storage-provider-s3-bucket-name", bucketName,
			"--oidc-storage-provider-s3-region", bucketRegion,
			"--oidc-storage-provider-s3-secret", util.HypershiftBucketSecretName,
		}
		args = append(args, awsArgs...)

		if err := c.ensurePullSecret(ctx); err != nil {
			return fmt.Errorf("failed to deploy pull secret to hypershift namespace, err: %w", err)
		}

		//Private link creds
		privateSecretKey := types.NamespacedName{Name: util.HypershiftPrivateLinkSecretName, Namespace: c.clusterName}

		if err := c.hubClient.Get(ctx, privateSecretKey, spl); err == nil {
			if err := c.createAwsSpokeSecret(ctx, spl, true); err != nil {
				return err
			}
			c.log.Info("private link region & credential arguments included")
			awsArgs := []string{
				"--aws-private-secret", util.HypershiftPrivateLinkSecretName,
				"--aws-private-region", string(spl.Data["region"]),
				"--private-platform", "AWS",
			}
			args = append(args, awsArgs...)
		} else {
			c.log.Info(fmt.Sprintf("private-link secret(%s) was not found", privateSecretKey))
		}

		// cache the private link secret for comparison againt the hub's to detect any change
		c.privateLinkSecret = *spl
	}
	//External DNS
	extDNSSecretKey := types.NamespacedName{Name: util.HypershiftExternalDNSSecretName, Namespace: c.clusterName}
	sExtDNS := &corev1.Secret{}
	if err := c.hubClient.Get(ctx, extDNSSecretKey, sExtDNS); err == nil {
		if awsPlatform {
			// For AWS DNS provider, users can specify either credentials or
			// aws-access-key-id and aws-secret-access-key
			if err := c.createAwsSpokeSecret(ctx, sExtDNS, false); err != nil {
				return err
			}
		} else {
			if err := c.createSpokeSecret(ctx, sExtDNS); err != nil {
				return err
			}
		}

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

	// cache the external DNS secret for comparison againt the hub's to detect any change
	c.extDnsSecret = *sExtDNS

	//Enable control plane telemetry forwarding
	telemetryArgs := []string{
		"--enable-uwm-telemetry-remote-write",
		"--platform-monitoring", "OperatorOnly",
	}
	args = append(args, telemetryArgs...)

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
		// compare locally saved secrets to the hub secrets as well
		// If they are the same, skip re-install.
		if reinstallCheckRequired &&
			!(c.operatorImagesUpdated(im, *operatorDeployment) ||
				c.secretDataUpdated(util.HypershiftBucketSecretName, *se) ||
				c.secretDataUpdated(util.HypershiftPrivateLinkSecretName, *spl) ||
				c.secretDataUpdated(util.HypershiftExternalDNSSecretName, *sExtDNS)) {
			c.log.Info("no change in hypershift operator images and secrets, skipping hypershift operator installation")
			return nil
		}
	} else {
		args = append(args, "--hypershift-image", hypershiftImage)
	}

	job, err := c.runHyperShiftInstallJob(ctx, hypershiftImage, os.TempDir(), imageStreamCMData, args)
	if err != nil {
		return err
	}

	if jobSucceeded, err := c.isInstallJobSuccessful(ctx, job.Name); !jobSucceeded || err != nil {
		if err != nil {
			return err
		}

		return fmt.Errorf("install HyperShift job failed")
	}
	c.log.Info(fmt.Sprintf("HyperShift install job: %s completed successfully", job.Name))

	// Upon successful installation, save the secrets locally to check any changes on addon restart
	if err := c.saveSecretLocally(ctx, se); err != nil { // S3 bucket secret
		return err
	}
	if err := c.saveSecretLocally(ctx, spl); err != nil { // private link secret
		return err
	}
	if err := c.saveSecretLocally(ctx, sExtDNS); err != nil { // external DNS secret
		return err
	}

	// Add label to Hypershift deployment
	err = c.addAddonLabelToDeployment(ctx)

	if err != nil {
		c.log.Error(err, "failed to add addon label to the hypershift operator deployment")
	}

	return nil
}

func (c *UpgradeController) createAwsSpokeSecret(ctx context.Context, hubSecret *corev1.Secret, regionRequired bool) error {
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

	return c.createSpokeSecret(ctx, spokeSecret)
}

func (c *UpgradeController) createSpokeSecret(ctx context.Context, hubSecret *corev1.Secret) error {

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

func (c *UpgradeController) saveSecretLocally(ctx context.Context, hubSecret *corev1.Secret) error {
	if hubSecret.Name != "" {
		spokeSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hubSecret.Name,
				Namespace: c.addonNamespace,
			},
		}
		c.log.Info(fmt.Sprintf("save the secret (%s/%s) locally on cluster %s", c.addonNamespace, hubSecret.Name, c.clusterName))
		_, err := controllerutil.CreateOrUpdate(ctx, c.spokeUncachedClient, spokeSecret, func() error {
			spokeSecret.Data = hubSecret.Data
			return nil
		})
		return err
	}
	return nil
}

func (c *UpgradeController) ensurePullSecret(ctx context.Context) error {
	obj := &corev1.Secret{}
	mcePullSecretKey := types.NamespacedName{Name: c.pullSecret, Namespace: c.addonNamespace}

	if err := c.spokeUncachedClient.Get(ctx, mcePullSecretKey, obj); err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info("mce pull secret not found, skip copy it to the hypershift namespace",
				"namespace", c.addonNamespace, "name", c.pullSecret)
			return nil
		}

		return fmt.Errorf("failed to get mce pull secret, err: %w", err)
	}

	hypershiftNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: hypershiftOperatorKey.Namespace,
		},
	}

	if err := c.spokeUncachedClient.Create(ctx, hypershiftNs); err != nil && !apierrors.IsAlreadyExists(err) {
		c.log.Error(err, "failed to create hypershift operator's namespace")
		return nil
	}

	overrideFunc := func(in *corev1.Secret, ns string) *corev1.Secret {
		out := &corev1.Secret{}
		out.SetName(in.GetName())
		out.SetNamespace(ns)

		out.Immutable = in.Immutable
		out.Data = in.Data
		out.StringData = in.StringData
		out.Type = in.Type

		return out
	}

	if err := c.spokeUncachedClient.Create(ctx, overrideFunc(obj, hypershiftOperatorKey.Namespace)); err != nil &&
		!apierrors.IsAlreadyExists(err) {

		return fmt.Errorf("failed to create hypershift operator's namespace, err: %w", err)
	}

	return nil
}

func (c *UpgradeController) isDeploymentMarked(ctx context.Context) bool {
	obj := &appsv1.Deployment{}

	if err := c.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, obj); err != nil {
		c.log.Info(fmt.Sprintf("hypershift operator %s deployment not found after install, err: %v", hypershiftOperatorKey, err))
		return false
	}

	a := obj.GetAnnotations()
	if len(a) == 0 || len(a[util.HypershiftAddonAnnotationKey]) == 0 {
		return false
	}

	return true
}

func (c *UpgradeController) hasHostedClusters(ctx context.Context) (bool, error) {
	listopts := &client.ListOptions{}
	hcList := &hyperv1alpha1.HostedClusterList{}
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

	// Check if deployment is created by the addon
	if obj.Annotations[util.HypershiftAddonAnnotationKey] == util.AddonControllerName {
		return nil, true, obj
	}

	return nil, false, nil
}

func (c *UpgradeController) addAddonLabelToDeployment(ctx context.Context) error {
	obj := &appsv1.Deployment{}

	if err := c.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, obj); err != nil {
		return err
	}

	// Check if deployment is created by the addon
	obj.Annotations[util.HypershiftAddonAnnotationKey] = util.AddonControllerName
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
	imUpgradeConfigMap := c.getImageOverrideMapFromHub()
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
		c.log.Error(err, "failed to get image stream content: ", err.Error())
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

func (c *UpgradeController) secretDataUpdated(secretName string, secret corev1.Secret) bool {
	c.log.Info(fmt.Sprintf("comparing hypershift operator installation secret(%s) to the locally saved secret", secretName))
	secretKey := types.NamespacedName{Name: secretName, Namespace: c.addonNamespace}
	localSecret := &corev1.Secret{}
	if err := c.spokeUncachedClient.Get(context.TODO(), secretKey, localSecret); err != nil && !apierrors.IsNotFound(err) {
		c.log.Error(err, "failed to find secret: ", err.Error()) // just log and continue
	}

	if !reflect.DeepEqual(localSecret.Data, secret.Data) { // compare only the secret data
		c.log.Info("the secret has changed")
		return true
	}

	return false
}
