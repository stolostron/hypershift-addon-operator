package agent

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"

	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"

	"open-cluster-management.io/addon-framework/pkg/lease"

	workv1 "open-cluster-management.io/api/work/v1"
)

const (
	hypershiftAddonAnnotationKey = "addon.hypershift.open-cluster-management.io"
	hypershiftBucketSecretName   = "hypershift-operator-oidc-provider-s3-credentials"
	kindAppliedManifestWork      = "AppliedManifestWork"
)

var (
	scheme           = runtime.NewScheme()
	hyperOperatorKey = types.NamespacedName{Name: "operator", Namespace: "hypershift"}
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hyperv1alpha1.AddToScheme(scheme))

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
	Log                     logr.Logger
	HubKubeconfigFile       string
	SpokeClusterName        string
	AddonName               string
	AddonNamespace          string
	HypershiftOperatorImage string
	MetricAddr              string
	ProbeAddr               string
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
	flags.StringVar(&o.HypershiftOperatorImage, "hypershfit-operator-image", util.DefaultHypershiftOperatorImage, "The HyperShift operator image to deploy")

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

	hubClient, err := client.New(hubConfig, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create hubClient, err: %w", err)
	}

	spokeKubeClient, err := client.New(spokeConfig, ctrlClient.Options{})
	if err != nil {
		return fmt.Errorf("failed to create spoke client, err: %w", err)
	}

	aCtrl := &agentController{
		hubClient:           hubClient,
		spokeUncachedClient: spokeKubeClient,
		spokeClient:         mgr.GetClient(),
		log:                 o.Log.WithName("agent-reconciler"),
		clusterName:         o.SpokeClusterName,
		addonName:           o.AddonName,
		addonNamespace:      o.AddonNamespace,
		operatorImage:       o.HypershiftOperatorImage,
	}

	// retry 3 times, in case something wrong with creating the hypershift install job
	if err := aCtrl.runHypershiftCmdWithRetires(3, time.Second*10, aCtrl.runHypershiftInstall); err != nil {
		log.Error(err, "failed to install hypershift Operator")
		return err
	}

	leaseClient, err := kubernetes.NewForConfig(spokeConfig)
	if err != nil {
		return fmt.Errorf("failed to create lease client, err: %w", err)
	}

	// create a lease updater
	leaseUpdater := lease.NewLeaseUpdater(
		leaseClient,
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
	hubClient           client.Client
	spokeUncachedClient client.Client
	spokeClient         client.Client //local for agent
	log                 logr.Logger
	recorder            events.Recorder
	clusterName         string
	addonName           string
	addonNamespace      string
	operatorImage       string
}

func (c *agentController) scaffoldHostedclusterSecrets(hcKey types.NamespacedName) []*corev1.Secret {
	return []*corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-admin-kubeconfig", hcKey.Name),
				Namespace: hcKey.Namespace,
				Labels:    map[string]string{"synced-from-spoke": "true"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-kubeadmin-password", hcKey.Name),
				Namespace: hcKey.Namespace,
				Labels:    map[string]string{"synced-from-spoke": "true"},
			},
		},
	}
}

func getKey(obj metav1.Object) types.NamespacedName {
	return types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
}

func (c *agentController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("Reconciling hostedcluster secrect %s", req))
	defer c.log.Info(fmt.Sprintf("Done reconcile hostedcluster secrect %s", req))

	hubMirrorSecretName := func(name string) string {
		// The secret stored on hub, and we should reflect the namespace on the name field, otherwise the name
		// may conflict on hub.
		// Note: the name generation rules need to be reproducible.
		// TODO(zhujian7): consider the case len(namespace)+len(name)>253, then create the secret will fail,
		// we may need to hash the name?
		return fmt.Sprintf("%s-%s", req.NamespacedName.Namespace, name)
	}
	hcSecrets := c.scaffoldHostedclusterSecrets(req.NamespacedName)
	deleteMirrorSecrets := func() error {
		var lastErr error

		for _, se := range hcSecrets {
			se.SetNamespace(c.clusterName)
			se.SetName(hubMirrorSecretName(se.Name))
			if err := c.hubClient.Delete(ctx, se); err != nil {
				lastErr = err
				c.log.Error(err, fmt.Sprintf("failed to delete secret(%s) on hub", getKey(se)))
			}
		}

		return lastErr
	}

	hc := &hyperv1alpha1.HostedCluster{}
	if err := c.spokeClient.Get(ctx, req.NamespacedName, hc); err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info(fmt.Sprintf("remove hostedcluster(%s) secrets on hub, since hostedcluster is gone", req.NamespacedName))
			return ctrl.Result{}, deleteMirrorSecrets()
		}

		c.log.Error(err, "failed to get the hostedcluster")
		return ctrl.Result{}, nil
	}

	if !hc.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	createOrUpdateMirrorSecrets := func() error {
		var lastErr error
		hypershiftDeploymentAnnoKey := "cluster.open-cluster-management.io/hypershiftdeployment"
		hypershiftDeploymentAnnoValue, ok := hc.GetAnnotations()[hypershiftDeploymentAnnoKey]
		if !ok || len(hypershiftDeploymentAnnoValue) == 0 {
			lastErr = fmt.Errorf("failed to get hypershift deployment annotation from hosted cluster")
		}
		for _, se := range hcSecrets {
			hubMirrorSecret := se.DeepCopy()
			if err := c.spokeClient.Get(ctx, getKey(se), se); err != nil {
				lastErr = err
				c.log.Error(err, fmt.Sprintf("failed to get hostedcluster secret %s on local cluster, skip this one", getKey(se)))
				continue
			}

			hubMirrorSecret.SetNamespace(c.clusterName)
			hubMirrorSecret.SetName(hubMirrorSecretName(se.Name))
			if len(hypershiftDeploymentAnnoValue) != 0 {
				hubMirrorSecret.SetAnnotations(map[string]string{hypershiftDeploymentAnnoKey: hypershiftDeploymentAnnoValue})
			}
			hubMirrorSecret.Data = se.Data

			nilFunc := func() error { return nil }

			_, err := controllerutil.CreateOrUpdate(ctx, c.hubClient, hubMirrorSecret, nilFunc)
			if err != nil {
				lastErr = err
				c.log.Error(err, fmt.Sprintf("failed to createOrUpdate hostedcluster secret %s to hub", getKey(hubMirrorSecret)))
			} else {
				c.log.Info(fmt.Sprintf("createOrUpdate hostedcluster secret %s to hub", getKey(hubMirrorSecret)))
			}
		}

		return lastErr
	}

	return ctrl.Result{}, createOrUpdateMirrorSecrets()
}

func isOwnerByAppliedManifestWork(ref []metav1.OwnerReference) bool {
	n := len(ref)

	if n == 0 {
		return false
	}

	for _, r := range ref {
		if r.APIVersion == workv1.GroupVersion.String() && r.Kind == kindAppliedManifestWork {
			return true
		}
	}

	return false
}

func (c *agentController) SetupWithManager(mgr ctrl.Manager) error {
	filterByOwner := func(obj client.Object) bool {
		owners := obj.GetOwnerReferences()

		return isOwnerByAppliedManifestWork(owners)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1alpha1.HostedCluster{}).
		WithEventFilter(predicate.NewPredicateFuncs(filterByOwner)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(c)
}
