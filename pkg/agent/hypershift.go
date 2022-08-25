package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
)

var (
	hypershiftOperatorKey = types.NamespacedName{
		Name:      util.HypershiftOperatorName,
		Namespace: util.HypershiftOperatorNamespace,
	}
)

func init() {
	utilruntime.Must(addonv1alpha1.AddToScheme(scheme))
}

func NewCleanupCommand(addonName string, logger logr.Logger) *cobra.Command {
	o := NewAgentOptions(addonName, logger)

	ctx := context.TODO()

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: fmt.Sprintf("clean up the hypershift operator if it's deployed by %s", addonName),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.runCleanup(ctx, nil)
		},
	}

	o.AddFlags(cmd)

	cmd.FParseErrWhitelist.UnknownFlags = true

	return cmd
}

func (o *AgentOptions) runCleanup(ctx context.Context, aCtrl *agentController) error {
	log := o.Log.WithName("controller-manager-setup")

	flag.Parse()

	if aCtrl == nil {
		spokeConfig := ctrl.GetConfigOrDie()

		c, err := ctrlClient.New(spokeConfig, ctrlClient.Options{Scheme: scheme})
		if err != nil {
			return fmt.Errorf("failed to create spokeUncacheClient, err: %w", err)
		}

		if err := hyperv1alpha1.AddToScheme(scheme); err != nil {
			log.Error(err, "unable add HyperShift APIs to scheme")
			return fmt.Errorf("unable add HyperShift APIs to scheme, err: %w", err)
		}

		aCtrl = &agentController{
			spokeUncachedClient:       c,
			hypershiftInstallExecutor: &HypershiftLibExecutor{},
		}
	}

	o.Log = o.Log.WithName("hypersfhit-operation")
	aCtrl.plugInOption(o)

	// retry 3 times, in case something wrong with deleting the hypershift install job
	if err := aCtrl.runHypershiftCmdWithRetires(ctx, 3, time.Second*10, aCtrl.runHypershiftCleanup); err != nil {
		log.Error(err, "failed to clean up hypershift Operator")
		return err
	}

	return nil
}

func (c *agentController) runHypershiftCmdWithRetires(
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

func (c *agentController) runHypershiftRender(ctx context.Context, args []string) ([]unstructured.Unstructured, error) {
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

func (c *agentController) runHypershiftCleanup(ctx context.Context) error {
	c.log.Info("enter runHypershiftCleanup")
	defer c.log.Info("exit runHypershiftCleanup")

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
		c.log.Info(fmt.Sprintf("skip deletion of the hypershift operator, there are existing HostedClusters"))
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

func (c *agentController) runHypershiftInstall(ctx context.Context) error {
	c.log.Info("enter runHypershiftInstall")
	defer c.log.Info("exit runHypershiftInstall")

	if err, ok := c.deploymentExistWithNoImageChange(ctx); ok || err != nil {
		if err != nil {
			return err
		}
		c.log.Error(err, "hypershift operator already exists at the required image level, skip update")
		return nil
	}

	awsPlatform := true

	bucketSecretKey := types.NamespacedName{Name: hypershiftBucketSecretName, Namespace: c.clusterName}
	se := &corev1.Secret{}
	if err := c.hubClient.Get(ctx, bucketSecretKey, se); err != nil {
		c.log.Info(fmt.Sprintf("bucket secret(%s) not found on the hub, installing hypershift operator for non-AWS platform.", bucketSecretKey))

		awsPlatform = false
	}

	args := []string{
		"render",
		"--format", "json",
		"--namespace", hypershiftOperatorKey.Namespace,
	}

	if awsPlatform { // if the S3 secret is found, install hypershift with s3 options
		bucketName := string(se.Data["bucket"])
		bucketRegion := string(se.Data["region"])

		if bucketName == "" {
			return fmt.Errorf("hypershift-operator-oidc-provider-s3-credentials does not contain a bucket key")
		}

		// Seed the hypershift namespace, the uninstall will remove this namespace.
		ns := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{Name: hypershiftOperatorKey.Namespace}}
		if err := c.spokeUncachedClient.Get(ctx, client.ObjectKeyFromObject(ns), ns); err != nil {
			if errors.IsNotFound(err) {
				if err := c.spokeUncachedClient.Create(ctx, ns); err != nil {
					return err
				}
			} else {
				return err
			}
		}

		if err := c.createAwsSpokeSecret(ctx, se); err != nil {
			return err
		}
		c.log.Info(fmt.Sprintf("oidc s3 bucket, region & credential arguments included"))
		awsArgs := []string{
			"--oidc-storage-provider-s3-bucket-name", bucketName,
			"--oidc-storage-provider-s3-region", bucketRegion,
			"--oidc-storage-provider-s3-secret", hypershiftBucketSecretName,
		}
		args = append(args, awsArgs...)

		if err := c.ensurePullSecret(ctx); err != nil {
			return fmt.Errorf("failed to deploy pull secret to hypershift namespace, err: %w", err)
		}

		//Private link creds
		privateSecretKey := types.NamespacedName{Name: hypershiftPrivateLinkSecretName, Namespace: c.clusterName}
		spl := &corev1.Secret{}
		if err := c.hubClient.Get(ctx, privateSecretKey, spl); err == nil {
			if err := c.createAwsSpokeSecret(ctx, spl); err != nil {
				return err
			}
			c.log.Info(fmt.Sprintf("private link region & credential arguments included"))
			awsArgs := []string{
				"--aws-private-secret", hypershiftPrivateLinkSecretName,
				"--aws-private-region", string(spl.Data["region"]),
				"--private-platform", "AWS",
			}
			args = append(args, awsArgs...)
		} else {
			c.log.Info(fmt.Sprintf("private-link secret(%s) was not found", privateSecretKey))
		}
	}
	//External DNS
	extDNSSecretKey := types.NamespacedName{Name: hypershiftExternalDNSSecretName, Namespace: c.clusterName}
	sExtDNS := &corev1.Secret{}
	if err := c.hubClient.Get(ctx, extDNSSecretKey, sExtDNS); err == nil {
		if err := c.createSpokeSecret(ctx, sExtDNS); err != nil {
			return err
		}
		c.log.Info(fmt.Sprintf("external dns provider & domain-filter arguments included"))
		awsArgs := []string{
			"--external-dns-secret", hypershiftExternalDNSSecretName,
			"--external-dns-domain-filter", string(sExtDNS.Data["domain-filter"]),
			"--external-dns-provider", string(sExtDNS.Data["provider"]),
		}
		args = append(args, awsArgs...)
	} else {
		c.log.Info(fmt.Sprintf("external dns secret(%s) was not found", extDNSSecretKey))
	}

	//Enable control plane telemetry forwarding
	telemetryArgs := []string{
		"--enable-uwm-telemetry-remote-write",
		"--platform-monitoring", "OperatorOnly",
	}
	args = append(args, telemetryArgs...)

	if c.withOverride {
		imageStreamFile, err := c.readInDownstreamOverride()
		if err != nil {
			return fmt.Errorf("failed to read the downstream image override configmap, err: %w", err)
		}

		defer os.Remove(imageStreamFile.Name())

		args = append(args, "--image-refs", imageStreamFile.Name())
	} else {
		args = append(args, "--hypershift-image", c.operatorImage)
	}

	c.log.Info(fmt.Sprintf("hypershift install args: %v", args))

	items, err := c.runHypershiftRender(ctx, args)
	if err != nil {
		return err
	}

	//TODO: @ianzhang366 fix the dependecy issue and use better way to inject the pull secret
	for _, item := range items {
		item := item
		if item.GetKind() == "ServiceAccount" {
			sa := &corev1.ServiceAccount{
				ImagePullSecrets: []corev1.LocalObjectReference{
					corev1.LocalObjectReference{Name: c.pullSecret},
				},
			}

			sa.SetName(item.GetName())
			sa.SetNamespace(item.GetNamespace())
			sa.SetLabels(item.GetLabels())
			sa.SetAnnotations(item.GetAnnotations())
			sa.SetFinalizers(item.GetFinalizers())

			if err := c.spokeUncachedClient.Create(ctx, sa); err != nil && !apierrors.IsAlreadyExists(err) {
				c.log.Error(err, fmt.Sprintf("failed to create %s, %s", item.GetKind(), client.ObjectKeyFromObject(&item)))
			}

			continue
		}

		if item.GetKind() == "Deployment" {
			a := item.GetAnnotations()
			if len(a) == 0 {
				a = map[string]string{}
			}

			a[hypershiftAddonAnnotationKey] = "hypershift-addon"

			item.SetAnnotations(a)
		}

		itemBytes, err := item.MarshalJSON()
		if err != nil {
			c.log.Error(err, fmt.Sprintf("failed to marshal json %s, %s", item.GetKind(), client.ObjectKeyFromObject(&item)))
			continue
		}

		if err := c.spokeUncachedClient.Patch(ctx,
			&item,
			ctrlClient.RawPatch(types.ApplyPatchType, itemBytes),
			ctrlClient.ForceOwnership,
			ctrlClient.FieldOwner("hypershift")); err != nil {
			c.log.Error(err, fmt.Sprintf("failed to apply %s, %s", item.GetKind(), client.ObjectKeyFromObject(&item)))
			continue
		}
		c.log.Info(fmt.Sprintf("applied: %s at %s", item.GetKind(), client.ObjectKeyFromObject(&item)))
	}

	return nil
}

func (c *agentController) createAwsSpokeSecret(ctx context.Context, hubSecret *corev1.Secret) error {

	region := hubSecret.Data["region"]
	awsSecretKey := hubSecret.Data["aws-secret-access-key"]
	awsKeyId := hubSecret.Data["aws-access-key-id"]
	if (hubSecret.Data["credentials"] == nil && (awsKeyId == nil || awsSecretKey == nil)) || region == nil {
		return fmt.Errorf("secret(%s/%s) does not contain a valid credential or region", hubSecret.Namespace, hubSecret.Name)
	} else {
		if awsSecretKey != nil {
			hubSecret.Data["credentials"] = []byte(fmt.Sprintf("[default]\naws_access_key_id = %s\naws_secret_access_key = %s", awsKeyId, awsSecretKey))
		}
	}

	return c.createSpokeSecret(ctx, hubSecret)
}

func (c *agentController) createSpokeSecret(ctx context.Context, hubSecret *corev1.Secret) error {

	spokeSecret := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      hubSecret.Name,
			Namespace: hypershiftOperatorKey.Namespace,
		},
		Data: map[string][]byte{
			"credentials": hubSecret.Data["credentials"],
		},
	}
	c.log.Info(fmt.Sprintf("createorupdate the the secret (%s/%s) on cluster %s", hypershiftOperatorKey.Namespace, hubSecret.Name, hubSecret.Namespace))
	_, err := controllerutil.CreateOrUpdate(ctx, c.spokeUncachedClient, spokeSecret, func() error { return nil })

	return err
}

func (c *agentController) ensurePullSecret(ctx context.Context) error {
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

func (c *agentController) isDeploymentMarked(ctx context.Context) bool {
	obj := &appsv1.Deployment{}

	if err := c.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, obj); err != nil {
		c.log.Info(fmt.Sprintf("hypershift operator %s deployment not found after install, err: %v", hypershiftOperatorKey, err))
		return false
	}

	a := obj.GetAnnotations()
	if len(a) == 0 || len(a[hypershiftAddonAnnotationKey]) == 0 {
		return false
	}

	return true
}

func (c *agentController) hasHostedClusters(ctx context.Context) (bool, error) {
	listopts := &client.ListOptions{}
	hcList := &hyperv1alpha1.HostedClusterList{}
	if err := c.spokeUncachedClient.List(ctx, hcList, listopts); err != nil {
		return false, err
	}

	return len(hcList.Items) != 0, nil
}

func (c *agentController) deploymentExistWithNoImageChange(ctx context.Context) (error, bool) {
	obj := &appsv1.Deployment{}

	if err := c.spokeUncachedClient.Get(ctx, hypershiftOperatorKey, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false
		}

		return err, false
	}

	// Check if image has changed
	if len(obj.Spec.Template.Spec.Containers) == 1 &&
		len(c.operatorImage) > 0 &&
		obj.Spec.Template.Spec.Containers[0].Image != c.operatorImage &&
		len(obj.Annotations) > 0 &&
		obj.Annotations[hypershiftAddonAnnotationKey] == util.AddonControllerName {
		return nil, false
	}
	return nil, true
}

func (c *agentController) readInDownstreamOverride() (*os.File, error) {
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

	file, err := ioutil.TempFile("", "hypershift-imagestream")
	if err != nil { // likely a unrecoverable error, don't retry
		return nil, fmt.Errorf("failed to create temp file for hoding aws credentials, err: %w", err)
	}

	f := file.Name()

	c.log.Info(fmt.Sprintf("imagestream at: %s", f))
	if err := ioutil.WriteFile(f, im, 0600); err != nil {
		return nil, fmt.Errorf("failed to write to temp file for imagestream, err: %w", err)
	}

	return file, nil
}
