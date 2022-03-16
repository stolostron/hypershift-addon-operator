package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"os/exec"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

var (
	hypershiftOperatorKey = types.NamespacedName{Name: "operator", Namespace: "hypershift"}
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
			return o.runCleanup(ctx)
		},
	}

	o.AddFlags(cmd)

	cmd.FParseErrWhitelist.UnknownFlags = true

	return cmd
}

func (o *AgentOptions) runCleanup(ctx context.Context) error {
	log := o.Log.WithName("controller-manager-setup")

	flag.Parse()

	// build kubeinformerfactory of hub cluster
	hubConfig, err := clientcmd.BuildConfigFromFlags("" /* leave masterurl as empty */, o.HubKubeconfigFile)
	if err != nil {
		return fmt.Errorf("failed to create hubConfig from flag, err: %w", err)
	}

	hubClient, err := client.New(hubConfig, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create hubClient, err: %w", err)
	}

	spokeConfig := ctrl.GetConfigOrDie()

	c, err := ctrlClient.New(spokeConfig, ctrlClient.Options{})
	if err != nil {
		return fmt.Errorf("failed to create spokeUncacheClient, err: %w", err)
	}

	aCtrl := &agentController{
		hubClient:           hubClient,
		spokeUncachedClient: c,
	}

	o.Log = o.Log.WithName("hypersfhit-operation")
	aCtrl.plugInOption(o)

	// retry 3 times, in case something wrong with deleting the hypershift install job
	if err := aCtrl.runHypershiftCmdWithRetires(3, time.Second*10, aCtrl.runHypershiftCleanup); err != nil {
		log.Error(err, "failed to clean up hypershift Operator")
		return err
	}

	return nil
}

func (c *agentController) runHypershiftCmdWithRetires(attempts int, sleep time.Duration, f func() error) error {
	var err error
	for i := attempts; i > 0; i-- {
		err = f()

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

func (c *agentController) runHypershiftCleanup() error {
	c.log.Info("enter runHypershiftCleanup")
	defer c.log.Info("exit runHypershiftCleanup")
	ctx := context.TODO()

	if !c.isDeploymentMarked(ctx) {
		c.log.Info(fmt.Sprintf("skip the hypershift operator deleting, not created by %s", util.AddonControllerName))
		return nil
	}

	args := []string{
		"install",
		"render",
		"--hypershift-image", c.operatorImage,
		"--namespace", hypershiftOperatorKey.Namespace,
		"--format", "json",
	}

	items, err := c.runHypershiftRender(args)
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

func (c *agentController) runHypershiftRender(args []string) ([]unstructured.Unstructured, error) {
	out := []unstructured.Unstructured{}
	//hypershiftInstall will get the inClusterConfig and use it to apply resources
	//
	//skip the GoSec since we intent to run the hypershift binary
	cmd := exec.Command("hypershift", args...) //#nosec G204

	renderTemplate, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("failed to run the hypershift install render command, err: %w", err)
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

func (c *agentController) runHypershiftInstall() error {
	c.log.Info("enter runHypershiftInstall")
	defer c.log.Info("exit runHypershiftInstall")
	ctx := context.TODO()

	if err, ok := c.deploymentExist(ctx); ok {
		c.log.Error(err, "hypershift operator already exist or failed to get deployment, skip install")
		return err
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

	c.log.Info(credsFile)
	if err := ioutil.WriteFile(credsFile, bucketCreds, 0600); err != nil { // likely a unrecoverable error, don't retry
		return fmt.Errorf("failed to write to temp file for aws credentials, err: %w", err)
	}

	if err := c.ensurePullSecret(ctx); err != nil {
		return fmt.Errorf("failed to deploy pull secret to hypershift namespace, err: %w", err)
	}

	args := []string{
		"install",
		"render",
		"--format", "json",
		"--hypershift-image", c.operatorImage,
		"--namespace", hypershiftOperatorKey.Namespace,
		"--oidc-storage-provider-s3-bucket-name", bucketName,
		"--oidc-storage-provider-s3-region", bucketRegion,
		"--oidc-storage-provider-s3-credentials", credsFile,
	}

	items, err := c.runHypershiftRender(args)
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

		if err := c.spokeUncachedClient.Create(ctx, &item); err != nil && !apierrors.IsAlreadyExists(err) {
			c.log.Error(err, fmt.Sprintf("failed to create %s, %s", item.GetKind(), client.ObjectKeyFromObject(&item)))
		}

	}

	return nil
}

func (c *agentController) ensurePullSecret(ctx context.Context) error {
	obj := &corev1.Secret{}
	mchPullSecretKey := types.NamespacedName{Name: c.pullSecret, Namespace: c.addonNamespace}

	if err := c.spokeUncachedClient.Get(ctx, mchPullSecretKey, obj); err != nil {
		return fmt.Errorf("failed to get mch pull secret, err: %w", err)
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

	if err := c.spokeUncachedClient.Create(ctx, overrideFunc(obj, hypershiftOperatorKey.Namespace)); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create hypershift operator's namespace, err: %w", err)
	}

	return nil
}

func (c *agentController) isDeploymentMarked(ctx context.Context) bool {
	obj := &appsv1.Deployment{}

	if err := c.hubClient.Get(ctx, hypershiftOperatorKey, obj); err != nil {
		c.log.Info(fmt.Sprintf("hypershift operator %s deployment not found after install, err: %v", hypershiftOperatorKey, err))
		return false
	}

	a := obj.GetAnnotations()
	if len(a) == 0 || len(a[hypershiftAddonAnnotationKey]) == 0 {
		return false
	}

	return true
}

func (c *agentController) deploymentExist(ctx context.Context) (error, bool) {
	obj := &appsv1.Deployment{}

	if err := c.hubClient.Get(ctx, hypershiftOperatorKey, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false
		}

		return err, false
	}

	return nil, true
}
