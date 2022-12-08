package agent

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"
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

	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"
	"open-cluster-management.io/addon-framework/pkg/lease"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"

	"github.com/stolostron/hypershift-addon-operator/pkg/install"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hyperv1alpha1.AddToScheme(scheme))
	utilruntime.Must(addonv1alpha1.AddToScheme(scheme))
	utilruntime.Must(clusterv1alpha1.AddToScheme(scheme))
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

	spokeKubeClient, err := client.New(spokeConfig, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("failed to create spoke client, err: %w", err)
	}

	spokeClusterClient, err := clusterclientset.NewForConfig(spokeConfig)
	if err != nil {
		return fmt.Errorf("failed to create spoke clusters client, err: %w", err)
	}

	aCtrl := &agentController{
		hubClient:           hubClient,
		spokeUncachedClient: spokeKubeClient,
		spokeClient:         mgr.GetClient(),
		spokeClustersClient: spokeClusterClient,
	}

	o.Log = o.Log.WithName("agent-reconciler")
	aCtrl.plugInOption(o)

	// Image upgrade controller
	uCtrl := install.NewUpgradeController(hubClient, spokeKubeClient, o.Log, o.AddonName, o.AddonNamespace, o.SpokeClusterName,
		o.HypershiftOperatorImage, o.PullSecretName, o.WithOverride, ctx)

	// retry 3 times, in case something wrong with creating the hypershift install job
	if err := uCtrl.RunHypershiftOperatorInstallOnAgentStartup(ctx); err != nil {
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

	maxHCNum, thresholdHCNum := aCtrl.getMaxAndThresholdHCCount()
	aCtrl.maxHostedClusterCount = maxHCNum
	aCtrl.thresholdHostedClusterCount = thresholdHCNum
	log.Info("the maximum hosted cluster count set to " + strconv.Itoa(aCtrl.maxHostedClusterCount))
	log.Info("the threshold hosted cluster count set to " + strconv.Itoa(aCtrl.thresholdHostedClusterCount))

	err = aCtrl.SyncAddOnPlacementScore(ctx)
	if err != nil {
		// AddOnPlacementScore must be created initially
		return fmt.Errorf("failed to create AddOnPlacementScore, err: %w", err)
	}

	log.Info("starting manager")

	//+kubebuilder:scaffold:builder
	if err = aCtrl.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create agent controller: %s, err: %w", util.AddonControllerName, err)
	}

	addonStatusController := &AddonStatusController{
		spokeClient: spokeKubeClient,
		hubClient:   hubClient,
		log:         o.Log.WithName("addon-status-controller"),
		addonNsn:    types.NamespacedName{Namespace: o.SpokeClusterName, Name: util.AddonControllerName},
		clusterName: o.SpokeClusterName,
	}

	if err = addonStatusController.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create agent status controller: %s, err: %w", util.AddonStatusControllerName, err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up health check, err: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up ready check, err: %w", err)
	}

	// After the initial hypershift operator installation, start the process to continuously check
	// if the hypershift operator re-installation is needed
	uCtrl.Start()

	return mgr.Start(ctrl.SetupSignalHandler())
}

type agentController struct {
	hubClient                   client.Client
	spokeUncachedClient         client.Client
	spokeClient                 client.Client              //local for agent
	spokeClustersClient         clusterclientset.Interface // client used to create cluster claim for the hypershift management cluster
	log                         logr.Logger
	recorder                    events.Recorder
	clusterName                 string
	maxHostedClusterCount       int
	thresholdHostedClusterCount int
}

func (c *agentController) plugInOption(o *AgentOptions) {
	c.log = o.Log
	c.clusterName = o.SpokeClusterName
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

func (c *agentController) generateExtManagedKubeconfigSecret(ctx context.Context, secretData map[string][]byte, hc hyperv1alpha1.HostedCluster) error {
	// 1. Get hosted cluster's admin kubeconfig secret
	secret := &corev1.Secret{}
	secret.SetName("external-managed-kubeconfig")
	managedClusterAnnoValue, ok := hc.GetAnnotations()[util.ManagedClusterAnnoKey]
	if !ok || len(managedClusterAnnoValue) == 0 {
		managedClusterAnnoValue = hc.Name
	}
	secret.SetNamespace("klusterlet-" + managedClusterAnnoValue)
	kubeconfigData := secretData["kubeconfig"]

	if kubeconfigData == nil {
		return fmt.Errorf("failed to get kubeconfig from secret: %s", secret.GetName())
	}

	kubeconfig, err := clientcmd.Load(kubeconfigData)

	if err != nil {
		c.log.Error(err, "failed to load kubeconfig from secret: %s", secret.GetName())
		return fmt.Errorf("failed to load kubeconfig from secret: %s", secret.GetName())
	}

	if len(kubeconfig.Clusters) == 0 {
		c.log.Error(err, "there is no cluster in kubeconfig from secret: %s", secret.GetName())
		return fmt.Errorf("there is no cluster in kubeconfig from secret: %s", secret.GetName())
	}

	if kubeconfig.Clusters["cluster"] == nil {
		c.log.Error(err, "failed to get a cluster from kubeconfig in secret: %s", secret.GetName())
		return fmt.Errorf("failed to get a cluster from kubeconfig in secret: %s", secret.GetName())
	}

	// 2. Extract port from kubeconfig
	portSeparatorIndex := strings.LastIndex(kubeconfig.Clusters["cluster"].Server, ":")
	if portSeparatorIndex == -1 {
		c.log.Info(fmt.Sprintf("failed to get the port from the server URL: %s", kubeconfig.Clusters["cluster"].Server))
		return fmt.Errorf("failed to get the port from the server URL: %s", kubeconfig.Clusters["cluster"].Server)
	}
	serverPort := kubeconfig.Clusters["cluster"].Server[portSeparatorIndex:]

	// 3. Replace the config.Clusters["cluster"].Server URL with internal kubeadpi service URL kube-apiserver.<Namespace>.svc.cluster.local
	clusterServerURL := "https://kube-apiserver." + hc.Namespace + "-" + hc.Name + ".svc.cluster.local" + serverPort
	kubeconfig.Clusters["cluster"].Server = clusterServerURL

	newKubeconfig, err := clientcmd.Write(*kubeconfig)

	if err != nil {
		c.log.Error(err, "failed to write new kubeconfig to secret: %s", secret.GetName())
		return fmt.Errorf("failed to write new kubeconfig to secret: %s", secret.GetName())
	}

	secretData["kubeconfig"] = newKubeconfig

	secret.Data = secretData

	c.log.Info("Set the cluster server URL in external-managed-kubeconfig secret", "clusterServerURL", clusterServerURL)

	nilFunc := func() error { return nil }

	// 3. Create the admin kubeconfig secret as external-managed-kubeconfig in klusterlet-<infraID> namespace
	_, err = controllerutil.CreateOrUpdate(ctx, c.spokeClient, secret, nilFunc)
	if err != nil {
		c.log.Error(err, "failed to createOrUpdate external-managed-kubeconfig secret", "secret", client.ObjectKeyFromObject(secret))
		return err
	}

	c.log.Info("createOrUpdate external-managed-kubeconfig secret", "secret", client.ObjectKeyFromObject(secret))

	return nil
}

func (c *agentController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("Reconciling hostedcluster secrect %s", req))
	defer c.log.Info(fmt.Sprintf("Done reconcile hostedcluster secrect %s", req))

	// Update the AddOnPlacementScore resource, continue reconcile even if error occurred
	_ = c.SyncAddOnPlacementScore(ctx)

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

	if hc.Status.Conditions == nil || len(hc.Status.Conditions) == 0 ||
		!c.isHostedControlPlaneAvailable(hc.Status) {
		// Wait for secrets to exist
		return ctrl.Result{}, nil
	}

	createOrUpdateMirrorSecrets := func() error {
		var lastErr error
		managedClusterAnnoValue, ok := hc.GetAnnotations()[util.ManagedClusterAnnoKey]
		if !ok || len(managedClusterAnnoValue) == 0 {
			c.log.Info("did not find managed cluster's name annotation from hosted cluster, using infra-id")
			managedClusterAnnoValue = hc.Name
			ok = true
		}

		hcSecrets := c.scaffoldHostedclusterSecrets(req.NamespacedName)
		for _, se := range hcSecrets {
			secretName := hc.Spec.InfraID
			if ok && len(managedClusterAnnoValue) > 0 {
				secretName = managedClusterAnnoValue
			}
			hubMirrorSecret := se.DeepCopy()
			hubMirrorSecret.SetNamespace(c.clusterName)
			hubMirrorSecret.SetName(fmt.Sprintf("%s-%s", secretName, se.Name))

			se.SetName(fmt.Sprintf("%s-%s", hc.Name, se.Name))
			if err := c.spokeClient.Get(ctx, client.ObjectKeyFromObject(se), se); err != nil {
				lastErr = err
				c.log.Error(err, fmt.Sprintf("failed to get hosted cluster secret %s on local cluster, skip this one", client.ObjectKeyFromObject(se)))
				continue
			}

			hubMirrorSecret.SetAnnotations(map[string]string{util.ManagedClusterAnnoKey: managedClusterAnnoValue})
			hubMirrorSecret.Data = se.Data

			// Create or update external-managed-kubeconfig secret for managed cluster registration agent
			if strings.HasSuffix(hubMirrorSecret.Name, "admin-kubeconfig") {
				c.log.Info("Generating external-managed-kubeconfig secret")

				extSecret := se.DeepCopy()

				errExt := c.generateExtManagedKubeconfigSecret(ctx, extSecret.Data, *hc)

				if errExt != nil {
					lastErr = errExt
				} else {
					c.log.Info("Successfully generated external-managed-kubeconfig secret")
				}
			}

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

func (c *agentController) isHostedControlPlaneAvailable(status hyperv1alpha1.HostedClusterStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Reason == hyperv1alpha1.HostedClusterAsExpectedReason && condition.Status == metav1.ConditionTrue && condition.Type == string(hyperv1alpha1.HostedClusterAvailable) {
			return true
		}
	}
	return false
}

func (c *agentController) SyncAddOnPlacementScore(ctx context.Context) error {
	addOnPlacementScore := &clusterv1alpha1.AddOnPlacementScore{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AddOnPlacementScore",
			APIVersion: "cluster.open-cluster-management.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.HostedClusterScoresResourceName,
			Namespace: c.clusterName,
		},
	}

	_, err := controllerutil.CreateOrUpdate(context.TODO(), c.hubClient, addOnPlacementScore, func() error { return nil })
	if err != nil {
		// just log the error. it should not stop the rest of reconcile
		c.log.Error(err, fmt.Sprintf("failed to create or update the addOnPlacementScore resource in %s", c.clusterName))
		return err
	}

	listopts := &client.ListOptions{}
	hcList := &hyperv1alpha1.HostedClusterList{}
	err = c.spokeUncachedClient.List(context.TODO(), hcList, listopts)
	if err != nil {
		// just log the error. it should not stop the rest of reconcile
		c.log.Error(err, "failed to get HostedCluster list")

		meta.SetStatusCondition(&addOnPlacementScore.Status.Conditions, metav1.Condition{
			Type:    "HostedClusterCountUpdated",
			Status:  metav1.ConditionFalse,
			Reason:  "HostedClusterCountFailed",
			Message: err.Error(),
		})

		err = c.hubClient.Status().Update(context.TODO(), addOnPlacementScore, &client.UpdateOptions{})
		if err != nil {
			// just log the error. it should not stop the rest of reconcile
			c.log.Error(err, fmt.Sprintf("failed to update the addOnPlacementScore status in %s", c.clusterName))
			return err
		}
	} else {
		hcCount := len(hcList.Items)
		scores := []clusterv1alpha1.AddOnPlacementScoreItem{
			{
				Name:  util.HostedClusterScoresScoreName,
				Value: int32(hcCount),
			},
		}

		meta.SetStatusCondition(&addOnPlacementScore.Status.Conditions, metav1.Condition{
			Type:    "HostedClusterCountUpdated",
			Status:  metav1.ConditionTrue,
			Reason:  "HostedClusterCountUpdated",
			Message: "Hosted cluster count was updated successfully",
		})
		addOnPlacementScore.Status.Scores = scores

		err = c.hubClient.Status().Update(context.TODO(), addOnPlacementScore, &client.UpdateOptions{})
		if err != nil {
			// just log the error. it should not stop the rest of reconcile
			c.log.Error(err, fmt.Sprintf("failed to update the addOnPlacementScore status in %s", c.clusterName))
			return err
		}

		c.log.Info(fmt.Sprintf("updated the addOnPlacementScore for %s: %v", c.clusterName, hcCount))

		// Based on the new HC count, update the zero, threshold, full cluster claim values.
		if err := c.createHostedClusterFullClusterClaim(ctx, hcCount); err != nil {
			c.log.Error(err, "failed to create or update hosted cluster full cluster claim")
			return err
		}

		if err = c.createHostedClusterThresholdClusterClaim(ctx, hcCount); err != nil {
			c.log.Error(err, "failed to create or update hosted cluster threshold cluster claim")
			return err
		}

		if err = c.createHostedClusterZeroClusterClaim(ctx, hcCount); err != nil {
			c.log.Error(err, "failed to create hosted cluster zero cluster claim")
			return err
		}

		c.log.Info("updated the hosted cluster cound cluster claims successfully")
	}

	return nil
}

func (c *agentController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1alpha1.HostedCluster{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(c)
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

func (o *AgentOptions) runCleanup(ctx context.Context, uCtrl *install.UpgradeController) error {
	log := o.Log.WithName("controller-manager-setup")

	flag.Parse()

	if uCtrl == nil {
		spokeConfig := ctrl.GetConfigOrDie()

		c, err := client.New(spokeConfig, client.Options{Scheme: scheme})
		if err != nil {
			return fmt.Errorf("failed to create spokeUncacheClient, err: %w", err)
		}

		if err := hyperv1alpha1.AddToScheme(scheme); err != nil {
			log.Error(err, "unable add HyperShift APIs to scheme")
			return fmt.Errorf("unable add HyperShift APIs to scheme, err: %w", err)
		}

		// Image upgrade controller
		o.Log = o.Log.WithName("hypersfhit-operation")
		uCtrl = install.NewUpgradeController(nil, c, o.Log, o.AddonName, o.AddonNamespace, o.SpokeClusterName,
			o.HypershiftOperatorImage, o.PullSecretName, o.WithOverride, ctx)
	}

	// retry 3 times, in case something wrong with deleting the hypershift install job
	if err := uCtrl.RunHypershiftCmdWithRetires(ctx, 3, time.Second*10, uCtrl.RunHypershiftCleanup); err != nil {
		log.Error(err, "failed to clean up hypershift Operator")
		return err
	}

	return nil
}
