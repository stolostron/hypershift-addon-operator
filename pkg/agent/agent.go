package agent

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hyperinstall "github.com/openshift/hypershift/cmd/install"

	"open-cluster-management.io/addon-framework/pkg/lease"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

const (
	hypershiftSecretAnnotationKey = "hypershift.openshift.io/cluster"
	hypershiftBucketSecretName    = "hypershift-operator-oidc-provider-s3-credentials"
	hypershiftOperatorImage       = "quay.io/hypershift/hypershift-operator:latest"
)

var (
	scheme           = runtime.NewScheme()
	hyperOperatorKey = types.NamespacedName{Name: "operator", Namespace: "hypershift"}
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(addonv1alpha1.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

func NewAgentCommand(addonName string, logger logr.Logger) *cobra.Command {
	o := NewAgentOptions(addonName, logger)

	ctx := context.TODO()

	cmd := &cobra.Command{
		Use:   "agent",
		Short: fmt.Sprintf("Start the %s's agent", addonName),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.runControllerManager(ctx)
		},
	}

	o.AddFlags(cmd)

	cmd.FParseErrWhitelist.UnknownFlags = true

	return cmd
}

// AgentOptions defines the flags for workload agent
type AgentOptions struct {
	Log                   logr.Logger
	HubKubeconfigFile     string
	SpokeClusterName      string
	AddonName             string
	AddonNamespace        string
	BucketSecretNamespace string
	MetricAddr            string
	ProbeAddr             string
}

// NewWorkloadAgentOptions returns the flags with default value set
func NewAgentOptions(addonName string, logger logr.Logger) *AgentOptions {
	return &AgentOptions{AddonName: addonName, Log: logger}
}

func (o *AgentOptions) AddFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	// This command only supports reading from config
	flags.StringVar(&o.HubKubeconfigFile, "hub-kubeconfig", o.HubKubeconfigFile, "Location of kubeconfig file to connect to hub cluster.")
	flags.StringVar(&o.SpokeClusterName, "cluster-name", o.SpokeClusterName, "Name of spoke cluster.")
	flags.StringVar(&o.AddonNamespace, "addon-namespace", util.AgentInstallationNamespace, "Installation namespace of addon.")
	flags.StringVar(&o.BucketSecretNamespace, "hypershfit-bucket-namespace", util.HypershiftBucketNamespaceOnHub, "Namespace that holds the hypershift bucket on hub")

	flags.StringVar(&o.MetricAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flags.StringVar(&o.ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
}

func (o *AgentOptions) runControllerManager(ctx context.Context) error {
	log := o.Log.WithName("controller-manager-setup")

	flag.Parse()

	spokeConfig := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(spokeConfig, ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     o.MetricAddr,
		Port:                   9443,
		HealthProbeBindAddress: o.ProbeAddr,
		LeaderElection:         false,
	})

	if err != nil {
		log.Error(err, "unable to start manager")
		return fmt.Errorf("unable to create manager, err: %w", err)
	}

	// build kubeinformerfactory of hub cluster
	hubConfig, err := clientcmd.BuildConfigFromFlags("" /* leave masterurl as empty */, o.HubKubeconfigFile)
	if err != nil {
		return fmt.Errorf("failed to create hubConfig from flag, err: %w", err)
	}

	hubClient, err := client.New(hubConfig, client.Options{})
	if err != nil {
		return fmt.Errorf("failed to create hubClient, err: %w", err)
	}

	spokeKubeClient, err := kubernetes.NewForConfig(spokeConfig)
	if err != nil {
		return fmt.Errorf("failed to create spoke client, err: %w", err)
	}

	aCtrl := &agentController{
		hubClient:             hubClient,
		spokeUncachedClient:   spokeKubeClient,
		spokeClient:           mgr.GetClient(),
		log:                   o.Log.WithName("agent-reconciler"),
		clusterName:           o.SpokeClusterName,
		addonName:             o.AddonName,
		addonNamespace:        o.AddonNamespace,
		bucketSecretNamespace: o.BucketSecretNamespace,
	}

	// retry 3 times, in case something wrong with creating the hypershift install job
	if err := aCtrl.installHypershiftOperatorWithRetires(3, time.Second*10, aCtrl.runHypershiftInstall); err != nil {
		log.Error(err, "failed to install hypershift Operator")
		return err
	}

	// create a lease updater
	leaseUpdater := lease.NewLeaseUpdater(
		spokeKubeClient,
		o.AddonName,
		o.AddonNamespace,
	)

	go leaseUpdater.Start(ctx)

	log.Info("starting manager")

	//+kubebuilder:scaffold:builder
	if err = aCtrl.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create agent controller: %s, err: %w", util.AddonControllerName, err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up health check, err: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up ready check, err: %w", err)
	}

	return mgr.Start(ctrl.SetupSignalHandler())
}

type agentController struct {
	hubClient             client.Client
	spokeUncachedClient   *kubernetes.Clientset
	spokeClient           client.Client //local for agent
	log                   logr.Logger
	recorder              events.Recorder
	clusterName           string
	addonName             string
	addonNamespace        string
	bucketSecretNamespace string
}

func (c *agentController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("Reconciling hostedcluster secrect %s", req))
	defer c.log.Info(fmt.Sprintf("Done reconcile hostedcluster secrect %s", req))

	se := &corev1.Secret{}
	if err := c.spokeClient.Get(ctx, req.NamespacedName, se); err != nil {
		c.log.Error(err, "failed to get the hostedcluster secret")
		return ctrl.Result{}, nil
	}

	mcAddOnKey := types.NamespacedName{Name: c.addonName, Namespace: c.clusterName}
	mcAddOn := &addonv1alpha1.ManagedClusterAddOn{}

	if err := c.hubClient.Get(ctx, mcAddOnKey, mcAddOn); err != nil {
		return ctrl.Result{}, err
	}

	if !mcAddOn.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	seTmp := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      se.Name,
			Namespace: c.clusterName,
			Labels:    map[string]string{"synced-from-spoke": "true"},
		},
		Data: se.Data,
	}

	res, err := controllerutil.CreateOrUpdate(ctx, c.hubClient, seTmp, func() error { return nil })
	c.log.Info(fmt.Sprintf("CreateOrUpdate result: %s", res))

	return ctrl.Result{}, err

}

func (c *agentController) SetupWithManager(mgr ctrl.Manager) error {
	filterByAnnotation := func(obj client.Object) bool {
		an := obj.GetAnnotations()

		if len(an) == 0 || len(an[hypershiftSecretAnnotationKey]) == 0 {
			return false
		}

		return true
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		WithEventFilter(predicate.NewPredicateFuncs(filterByAnnotation)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(c)
}

func (c *agentController) installHypershiftOperatorWithRetires(attempts int, sleep time.Duration, f func() error) error {
	var err error
	for i := attempts; i > 0; i-- {
		err = f()

		if err == nil {
			return nil
		}

		// Add some randomness to prevent creating a Thundering Herd
		jitter := time.Duration(rand.Int63n(int64(sleep)))
		sleep = sleep + jitter/2
		time.Sleep(sleep)

		sleep = 2 * sleep

	}

	return fmt.Errorf("failed to install the hypershift operator and apply its crd after %v retires, err: %w", attempts, err)
}

func (c *agentController) runHypershiftInstall() error {
	c.log.Info("enter runHypershiftInstall")
	defer c.log.Info("exit runHypershiftInstall")
	ctx := context.TODO()

	if _, err := c.spokeUncachedClient.AppsV1().Deployments(hyperOperatorKey.Namespace).Get(ctx, hyperOperatorKey.Name, metav1.GetOptions{}); err == nil || !apierrors.IsNotFound(err) {
		c.log.Info(fmt.Sprintf("hypershift operator %s deployment exists, wont reinstall it. with err: %v", hyperOperatorKey, err))
		return nil
	}

	bucketSecretKey := types.NamespacedName{Name: hypershiftBucketSecretName, Namespace: c.bucketSecretNamespace}
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
	if err := ioutil.WriteFile(credsFile, bucketCreds, 0644); err != nil { // likely a unrecoverable error, don't retry
		c.log.Error(err, "failed to write to temp file for aws credentials")
		return nil
	}

	//hypershiftInstall will get the inClusterConfig and use it to apply resources
	cmd := hyperinstall.NewCommand()

	cmd.FParseErrWhitelist.UnknownFlags = true

	cmd.SetArgs([]string{
		"--hypershift-image", hypershiftOperatorImage,
		"--namespace", hyperOperatorKey.Namespace,
		"--oidc-storage-provider-s3-bucket-name", bucketName,
		"--oidc-storage-provider-s3-region", bucketRegion,
		"--oidc-storage-provider-s3-credentials", credsFile,
	})

	if err := cmd.Execute(); err != nil {
		c.log.Error(err, "failed to run the hypershift install command")
		return err
	}

	return nil
}
