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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	deploymentRes = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
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
		log:                 o.Log.WithName("agent-reconciler"),
		clusterName:         o.SpokeClusterName,
		addonName:           o.AddonName,
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

	if !c.isManagedclusterAddonMarked(ctx) {
		c.log.Info(fmt.Sprintf("skip the hypershift operator deleting, not created by %s", util.AddonControllerName))
		return nil
	}

	args := []string{
		"install",
		"render",
		"--hypershift-image", c.operatorImage,
		"--namespace", hyperOperatorKey.Namespace,
		"--format", "json",
	}

	//hypershiftInstall will get the inClusterConfig and use it to apply resources
	//
	//skip the GoSec since we intent to run the hypershift binary
	cmd := exec.Command("hypershift", args...) //#nosec G204

	c.log.Info(cmd.String())

	renderTemplate, err := cmd.Output()
	if err != nil {
		c.log.Error(err, fmt.Sprintf("failed to run the hypershift install render command"))
		return err
	}

	d := map[string]interface{}{}

	if err := json.Unmarshal(renderTemplate, &d); err != nil {
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
			c.log.Error(err, fmt.Sprintf("failed to delete %s, %s", u.GetKind(), client.ObjectKeyFromObject(&u)))
		}

	}

	return nil
}

func (c *agentController) runHypershiftInstall() error {
	c.log.Info("enter runHypershiftInstall")
	defer c.log.Info("exit runHypershiftInstall")
	ctx := context.TODO()

	if err := c.markManagedclusterAddon(ctx); err != nil {
		return fmt.Errorf("faield to mark the managedclusteraddon, err: %v", err)
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

	args := []string{
		"install",
		"--hypershift-image", c.operatorImage,
		"--namespace", hyperOperatorKey.Namespace,
		"--oidc-storage-provider-s3-bucket-name", bucketName,
		"--oidc-storage-provider-s3-region", bucketRegion,
		"--oidc-storage-provider-s3-credentials", credsFile,
	}

	//hypershiftInstall will get the inClusterConfig and use it to apply resources
	//
	//skip the GoSec since we intent to run the hypershift binary
	cmd := exec.Command("hypershift", args...) //#nosec G204

	if err := cmd.Run(); err != nil {
		c.log.Error(err, "failed to run the hypershift install command")
		return err
	}

	return nil
}

func (c *agentController) markManagedclusterAddon(ctx context.Context) error {
	addonCfg := &addonv1alpha1.ManagedClusterAddOn{}
	addonCfgKey := types.NamespacedName{Name: c.addonName, Namespace: c.clusterName}

	if err := c.hubClient.Get(ctx, addonCfgKey, addonCfg); err != nil {
		c.log.Info(fmt.Sprintf("managedclsuteraddon %s not found, err: %v", addonCfgKey, err))
		return nil
	}

	if !addonCfg.GetDeletionTimestamp().IsZero() {
		c.log.Info("deleting the hypershift addon")
		return nil
	}

	a := addonCfg.GetAnnotations()
	if len(a) == 0 {
		a = map[string]string{}
	}

	a[hypershiftAddonAnnotationKey] = util.AddonControllerName

	addonCfg.SetAnnotations(a)

	if err := c.hubClient.Update(ctx, addonCfg); err != nil {
		c.log.Error(err, fmt.Sprintf("failed to CreateOrPatch the existing  managedclusteraddon %s ", client.ObjectKeyFromObject(addonCfg)))
		return err
	}

	return nil
}

func (c *agentController) isManagedclusterAddonMarked(ctx context.Context) bool {
	addonCfg := &addonv1alpha1.ManagedClusterAddOn{}
	addonCfgKey := types.NamespacedName{Name: c.addonName, Namespace: c.clusterName}

	if err := c.hubClient.Get(ctx, addonCfgKey, addonCfg); err != nil {
		c.log.Info(fmt.Sprintf("hypershift operator %s deployment not found after install, err: %v", hyperOperatorKey, err))
		return false
	}

	a := addonCfg.GetAnnotations()
	if len(a) == 0 || len(a[hypershiftAddonAnnotationKey]) == 0 {
		return false
	}

	return true
}
