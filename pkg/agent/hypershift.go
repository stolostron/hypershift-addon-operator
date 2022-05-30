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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"

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

		c, err := ctrlClient.New(spokeConfig, ctrlClient.Options{})
		if err != nil {
			return fmt.Errorf("failed to create spokeUncacheClient, err: %w", err)
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
		c.log.Info(fmt.Sprintf("skip the hypershift operator deleting, not created by %s", util.AddonControllerName))
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

	bucketSecretKey := types.NamespacedName{Name: hypershiftBucketSecretName, Namespace: c.clusterName}
	se := &corev1.Secret{}
	if err := c.hubClient.Get(ctx, bucketSecretKey, se); err != nil {
		c.log.Error(err, fmt.Sprintf("failed to get bucket secret(%s) from hub, will retry.", bucketSecretKey))
		return err
	}

	bucketName := string(se.Data["bucket"])
	bucketRegion := string(se.Data["region"])
	bucketCreds := se.Data["credentials"]

	file, err := ioutil.TempFile("", ".aws-creds")
	if err != nil { // likely a unrecoverable error, don't retry
		return fmt.Errorf("failed to create temp file for hoding aws credentials, err: %w", err)
	}

	credsFile := file.Name()
	defer os.Remove(credsFile)

	c.log.Info(fmt.Sprintf("aws config at: %s", credsFile))
	if err := ioutil.WriteFile(credsFile, bucketCreds, 0600); err != nil { // likely a unrecoverable error, don't retry
		return fmt.Errorf("failed to write to temp file for aws credentials, err: %w", err)
	}

	if err := c.ensurePullSecret(ctx); err != nil {
		return fmt.Errorf("failed to deploy pull secret to hypershift namespace, err: %w", err)
	}

	args := []string{
		"render",
		"--format", "json",
		"--namespace", hypershiftOperatorKey.Namespace,
		"--oidc-storage-provider-s3-bucket-name", bucketName,
		"--oidc-storage-provider-s3-region", bucketRegion,
		"--oidc-storage-provider-s3-credentials", credsFile,
	}

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
