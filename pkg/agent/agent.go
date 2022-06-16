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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"
	"open-cluster-management.io/addon-framework/pkg/lease"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
)

const (
	hypershiftAddonAnnotationKey    = "hypershift.open-cluster-management.io/createBy"
	hypershiftBucketSecretName      = "hypershift-operator-oidc-provider-s3-credentials"
	hypershiftPrivateLinkSecretName = "hypershift-operator-private-link-credentials"
	kindAppliedManifestWork         = "AppliedManifestWork"
	hypershiftDeploymentAnnoKey     = "cluster.open-cluster-management.io/hypershiftdeployment"
)

var (
	scheme = runtime.NewScheme()
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
	PullSecretName          string
	WithOverride            bool
}

// NewWorkloadAgentOptions returns the flags with default value set
func NewAgentOptions(addonName string, logger logr.Logger) *AgentOptions {
	return &AgentOptions{AddonName: addonName, Log: logger}
}

func (o *AgentOptions) AddFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	// This command only supports reading from config
	flags.StringVar(&o.HubKubeconfigFile, "hub-kubeconfig", o.HubKubeconfigFile,
		"Location of kubeconfig file to connect to hub cluster.")
	flags.StringVar(&o.SpokeClusterName, "cluster-name", o.SpokeClusterName, "Name of spoke cluster.")
	flags.StringVar(&o.AddonNamespace, "addon-namespace", util.AgentInstallationNamespace,
		"Installation namespace of addon.")
	flags.StringVar(&o.PullSecretName, "multicluster-pull-secret", util.MulticlusterEnginePullSecret,
		"Pull secret that will be injected to hypershift serviceaccount")
	flags.StringVar(&o.HypershiftOperatorImage, "hypershfit-operator-image", util.DefaultHypershiftOperatorImage,
		"The HyperShift operator image to deploy")
	flags.BoolVar(&o.WithOverride, "with-image-override", false, "Use image from override configmap")

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

	spokeKubeClient, err := client.New(spokeConfig, client.Options{})
	if err != nil {
		return fmt.Errorf("failed to create spoke client, err: %w", err)
	}

	spokeClusterClient, err := clusterclientset.NewForConfig(spokeConfig)
	if err != nil {
		return fmt.Errorf("failed to create spoke clusters client, err: %w", err)
	}

	aCtrl := &agentController{
		hubClient:                 hubClient,
		spokeUncachedClient:       spokeKubeClient,
		spokeClient:               mgr.GetClient(),
		spokeClustersClient:       spokeClusterClient,
		hypershiftInstallExecutor: &HypershiftLibExecutor{},
	}

	o.Log = o.Log.WithName("agent-reconciler")
	aCtrl.plugInOption(o)

	// retry 3 times, in case something wrong with creating the hypershift install job
	if err := aCtrl.runHypershiftCmdWithRetires(ctx, 3, time.Second*10, aCtrl.runHypershiftInstall); err != nil {
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

	if err := aCtrl.createManagementClusterClaim(ctx); err != nil {
		return fmt.Errorf("unable to create management cluster claim, err: %w", err)
	}

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
	hubClient                 client.Client
	spokeUncachedClient       client.Client
	spokeClient               client.Client              //local for agent
	spokeClustersClient       clusterclientset.Interface // client used to create cluster claim for the hypershift management cluster
	log                       logr.Logger
	recorder                  events.Recorder
	clusterName               string
	addonName                 string
	addonNamespace            string
	operatorImage             string
	pullSecret                string
	withOverride              bool
	hypershiftInstallExecutor HypershiftInstallExecutorInterface
}

func (c *agentController) plugInOption(o *AgentOptions) {
	c.log = o.Log
	c.clusterName = o.SpokeClusterName
	c.addonName = o.AddonName
	c.addonNamespace = o.AddonNamespace
	c.operatorImage = o.HypershiftOperatorImage
	c.pullSecret = o.PullSecretName
	c.withOverride = o.WithOverride
}

func (c *agentController) scaffoldHostedclusterSecrets(hcKey types.NamespacedName) []*corev1.Secret {
	return []*corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "admin-kubeconfig",
				Namespace: hcKey.Namespace,
				Labels: map[string]string{
					"synced-from-spoke":                  "true",
					util.HypershiftClusterNameLabel:      hcKey.Name,
					util.HypershiftHostingNamespaceLabel: hcKey.Namespace,
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kubeadmin-password",
				Namespace: hcKey.Namespace,
				Labels: map[string]string{
					"synced-from-spoke":                  "true",
					util.HypershiftClusterNameLabel:      hcKey.Name,
					util.HypershiftHostingNamespaceLabel: hcKey.Namespace,
				},
			},
		},
	}
}

func (c *agentController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("Reconciling hostedcluster secrect %s", req))
	defer c.log.Info(fmt.Sprintf("Done reconcile hostedcluster secrect %s", req))

	// Delete HC secrets on the hub using labels for HC and the hosting NS
	deleteMirrorSecrets := func() error {
		secretSelector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
			MatchLabels: map[string]string{
				util.HypershiftClusterNameLabel:      req.Name,
				util.HypershiftHostingNamespaceLabel: req.Namespace,
			},
		})
		if err != nil {
			c.log.Error(err, "failed to convert label to get secrets on hub for hostedCluster: %s", req)
			return err
		}

		listopts := &client.ListOptions{}
		listopts.LabelSelector = secretSelector
		listopts.Namespace = c.clusterName
		hcHubSecretList := &corev1.SecretList{}
		err = c.hubClient.List(ctx, hcHubSecretList, listopts)
		if err != nil {
			c.log.Error(err, "failed to get secrets on hub for hostedCluster: %s", req)
			return err
		}

		var lastErr error
		for i := range hcHubSecretList.Items {
			se := hcHubSecretList.Items[i]
			c.log.V(4).Info(fmt.Sprintf("deleting secret(%s) on hub", client.ObjectKeyFromObject(&se)))
			if err := c.hubClient.Delete(ctx, &se); err != nil && !apierrors.IsNotFound(err) {
				lastErr = err
				c.log.Error(err, fmt.Sprintf("failed to delete secret(%s) on hub", client.ObjectKeyFromObject(&se)))
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

	if hc.Status.KubeConfig == nil ||
		!meta.IsStatusConditionTrue(hc.Status.Conditions, string(hyperv1alpha1.HostedClusterAvailable)) {
		// Wait for secrets to exist
		return ctrl.Result{}, nil
	}

	createOrUpdateMirrorSecrets := func() error {
		var lastErr error
		hypershiftDeploymentAnnoValue, ok := hc.GetAnnotations()[hypershiftDeploymentAnnoKey]
		if !ok || len(hypershiftDeploymentAnnoValue) == 0 {
			lastErr = fmt.Errorf("failed to get hypershift deployment annotation from hosted cluster")
		}

		hcSecrets := c.scaffoldHostedclusterSecrets(req.NamespacedName)
		for _, se := range hcSecrets {
			hubMirrorSecret := se.DeepCopy()
			hubMirrorSecret.SetNamespace(c.clusterName)
			hubMirrorSecret.SetName(fmt.Sprintf("%s-%s", hc.Spec.InfraID, se.Name))

			se.SetName(fmt.Sprintf("%s-%s", hc.Name, se.Name))
			if err := c.spokeClient.Get(ctx, client.ObjectKeyFromObject(se), se); err != nil {
				lastErr = err
				c.log.Error(err, fmt.Sprintf("failed to get hostedcluster secret %s on local cluster, skip this one", client.ObjectKeyFromObject(se)))
				continue
			}

			if len(hypershiftDeploymentAnnoValue) != 0 {
				hubMirrorSecret.SetAnnotations(map[string]string{hypershiftDeploymentAnnoKey: hypershiftDeploymentAnnoValue})
			}
			hubMirrorSecret.Data = se.Data

			nilFunc := func() error { return nil }

			_, err := controllerutil.CreateOrUpdate(ctx, c.hubClient, hubMirrorSecret, nilFunc)
			if err != nil {
				lastErr = err
				c.log.Error(err, fmt.Sprintf("failed to createOrUpdate hostedcluster secret %s to hub", client.ObjectKeyFromObject(hubMirrorSecret)))
			} else {
				c.log.Info(fmt.Sprintf("createOrUpdate hostedcluster secret %s to hub", client.ObjectKeyFromObject(hubMirrorSecret)))
			}
		}

		return lastErr
	}

	if err := createOrUpdateMirrorSecrets(); err != nil {
		return ctrl.Result{}, err
	}

	if err := c.createHostedClusterClaim(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hc.Status.KubeConfig.Name},
		generateClusterClientFromSecret); err != nil {
		// just log the infomation and wait for the next reconcile to retry.
		// since the hosted cluster may:
		//   - not available now
		//   - have not been imported to the hub, and there is no clusterclaim CRD.
		c.log.V(4).Info("unable to create hosted cluster claim, wait for the next retry", "error", err.Error())
		return ctrl.Result{Requeue: true, RequeueAfter: 1 * time.Minute}, nil
	}

	return ctrl.Result{}, nil
}

func (c *agentController) SetupWithManager(mgr ctrl.Manager) error {
	filterByOwner := func(obj client.Object) bool {
		hypershiftDeploymentAnnoValue, ok := obj.GetAnnotations()[hypershiftDeploymentAnnoKey]
		if !ok || len(hypershiftDeploymentAnnoValue) == 0 {
			return false
		}
		return true
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1alpha1.HostedCluster{}).
		WithEventFilter(predicate.NewPredicateFuncs(filterByOwner)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(c)
}
