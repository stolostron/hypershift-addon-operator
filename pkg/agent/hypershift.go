package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	hyperinstall "github.com/openshift/hypershift/cmd/install"
	"github.com/spf13/cobra"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"

	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	deploymentRes = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
)

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

	spokeConfig := ctrl.GetConfigOrDie()

	c, _ := ctrlClient.New(spokeConfig, ctrlClient.Options{})

	aCtrl := &agentController{
		spokeUncachedClient: c,
		log:                 o.Log.WithName("agent-reconciler"),
	}

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

	deploy := &appsv1.Deployment{}

	if err := c.spokeUncachedClient.Get(ctx, hyperOperatorKey, deploy); err != nil {
		if !apierrors.IsNotFound(err) {
			c.log.Info(fmt.Sprintf("can't get hypershift operator %s deployment exists, with err: %v", hyperOperatorKey, err))
			return err
		}

		return nil
	}

	c.log.Info(deploy.GetName())

	a := deploy.GetAnnotations()
	if len(a) == 0 || len(a[hypershiftAddonAnnotationKey]) == 0 {
		c.log.Info("skip, hypershift operator is not deployed by addon agent")
		return nil
	}

	//hypershiftInstall will get the inClusterConfig and use it to apply resources
	opts := &hyperinstall.Options{
		Namespace:       hyperOperatorKey.Namespace,
		HyperShiftImage: c.operatorImage,
		PrivatePlatform: string(hyperv1.NonePlatform),
	}

	cmd := hyperinstall.NewRenderCommand(opts)

	cmd.FParseErrWhitelist.UnknownFlags = true
	cmd.SetArgs([]string{
		"--format", hyperinstall.RenderFormatJson,
	})

	renderTemplate := new(bytes.Buffer)
	cmd.SetOut(renderTemplate)

	if err := cmd.Execute(); err != nil {
		c.log.Error(err, "failed to render the hypershift manifests")
		return err
	}

	d := map[string]interface{}{}

	if err := json.Unmarshal(renderTemplate.Bytes(), &d); err != nil {
		c.log.Error(err, "failed to Unmarshal") // this is likely an unrecoverable
		return nil
	}

	items, ok := d["items"].([]interface{})
	if !ok {
		c.log.Error(fmt.Errorf("failed to Unmarshal template items"), "")
		return nil
	}

	for _, item := range items {
		u := unstructured.Unstructured{}

		v, ok := item.(map[string]interface{})
		if !ok {
			c.log.Error(fmt.Errorf("failed to type assert an item"), "")
			continue
		}

		u.SetUnstructuredContent(v)

		if err := c.spokeUncachedClient.Delete(ctx, &u); !apierrors.IsNotFound(err) {
			c.log.Error(err, fmt.Sprintf("failed to delete %s, %s", u.GetKind(), getKey(&u)))
		}

	}

	return nil
}

func (c *agentController) runHypershiftInstall() error {
	c.log.Info("enter runHypershiftInstall")
	defer c.log.Info("exit runHypershiftInstall")
	ctx := context.TODO()

	deploy := &appsv1.Deployment{}
	if err := c.spokeUncachedClient.Get(ctx, hyperOperatorKey, deploy); err == nil || !apierrors.IsNotFound(err) {
		c.log.Info(fmt.Sprintf("hypershift operator %s deployment exists, wont reinstall it. with err: %v", hyperOperatorKey, err))
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
		c.log.Error(err, "failed to create temp file for hoding aws credentials")
		return nil
	}

	credsFile := file.Name()
	defer os.Remove(credsFile)

	c.log.Info(credsFile)
	if err := ioutil.WriteFile(credsFile, bucketCreds, 0600); err != nil { // likely a unrecoverable error, don't retry
		c.log.Error(err, "failed to write to temp file for aws credentials")
		return nil
	}

	//hypershiftInstall will get the inClusterConfig and use it to apply resources
	cmd := hyperinstall.NewCommand()

	cmd.FParseErrWhitelist.UnknownFlags = true

	cmd.SetArgs([]string{
		"--hypershift-image", c.operatorImage,
		"--namespace", hyperOperatorKey.Namespace,
		"--oidc-storage-provider-s3-bucket-name", bucketName,
		"--oidc-storage-provider-s3-region", bucketRegion,
		"--oidc-storage-provider-s3-credentials", credsFile,
	})

	if err := cmd.Execute(); err != nil {
		c.log.Error(err, "failed to run the hypershift install command")
		return err
	}

	deploy = &appsv1.Deployment{}
	if err := c.spokeUncachedClient.Get(ctx, hyperOperatorKey, deploy); err == nil || !apierrors.IsNotFound(err) {
		c.log.Info(fmt.Sprintf("hypershift operator %s deployment not found after install, err: %v", hyperOperatorKey, err))
		return nil
	}

	a := deploy.GetAnnotations()
	if len(a) == 0 {
		a = map[string]string{}
	}

	a[hypershiftAddonAnnotationKey] = "installer"

	deploy.SetAnnotations(a)

	// a placeholder for later use
	noOp := func(in *appsv1.Deployment) controllerutil.MutateFn {
		return func() error {
			return nil
		}
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, c.spokeUncachedClient, deploy, noOp(deploy)); err != nil {
		c.log.Error(err, fmt.Sprintf("failed to CreateOrUpdate the existing hypershift operator %s", getKey(deploy)))
		return err

	}

	return nil
}
