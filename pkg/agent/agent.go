package agent

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
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

	hyperv1beta1 "github.com/openshift/hypershift/api/v1beta1"
	"open-cluster-management.io/addon-framework/pkg/lease"
	addonutils "open-cluster-management.io/addon-framework/pkg/utils"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"
	operatorapiv1 "open-cluster-management.io/api/operator/v1"

	"github.com/stolostron/hypershift-addon-operator/pkg/install"
	"github.com/stolostron/hypershift-addon-operator/pkg/metrics"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(hyperv1beta1.AddToScheme(scheme))
	utilruntime.Must(addonv1alpha1.AddToScheme(scheme))
	utilruntime.Must(clusterv1alpha1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(operatorapiv1.AddToScheme(scheme))

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

	metrics.AddonAgentFailedToStartBool.Set(0)

	if err != nil {
		log.Error(err, "unable to start manager")
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("unable to create manager, err: %w", err)
	}

	// build kubeinformerfactory of hub cluster
	hubConfig, err := clientcmd.BuildConfigFromFlags("" /* leave masterurl as empty */, o.HubKubeconfigFile)
	if err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("failed to create hubConfig from flag, err: %w", err)
	}

	hubClient, err := client.New(hubConfig, client.Options{Scheme: scheme})
	if err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("failed to create hubClient, err: %w", err)
	}

	spokeKubeClient, err := client.New(spokeConfig, client.Options{Scheme: scheme})
	if err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("failed to create spoke client, err: %w", err)
	}

	spokeClusterClient, err := clusterclientset.NewForConfig(spokeConfig)
	if err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
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

	metrics.InstallationFailningGaugeBool.Set(0)

	// Image upgrade controller
	uCtrl := install.NewUpgradeController(hubClient, spokeKubeClient, o.Log, o.AddonName, o.AddonNamespace, o.SpokeClusterName,
		o.HypershiftOperatorImage, o.PullSecretName, o.WithOverride, ctx)

	// Perform initial hypershift operator installation on start-up, then start the process to continuously check
	// if the hypershift operator re-installation is needed
	uCtrl.Start()

	leaseClient, err := kubernetes.NewForConfig(spokeConfig)
	if err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("failed to create lease client, err: %w", err)
	}

	// create a lease updater
	leaseUpdater := lease.NewLeaseUpdater(
		leaseClient,
		o.AddonName,
		o.AddonNamespace,
	)

	go leaseUpdater.Start(ctx)

	cc, err := addonutils.NewConfigChecker("hypershift-addon-agent", "/var/run/hub/kubeconfig")
	if err != nil {
		return fmt.Errorf("unable to create config checker for controller err: %v", err)
	}
	go func() {
		if err = aCtrl.serveHealthProbes(":8000", cc.Check); err != nil {
			log.Error(err, "unable to serve health probes")
		}
	}()

	if err := aCtrl.createManagementClusterClaim(ctx); err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("unable to create management cluster claim, err: %w", err)
	}

	maxHCNum, thresholdHCNum := aCtrl.getMaxAndThresholdHCCount()
	aCtrl.maxHostedClusterCount = maxHCNum
	aCtrl.thresholdHostedClusterCount = thresholdHCNum
	log.Info("the maximum hosted cluster count set to " + strconv.Itoa(aCtrl.maxHostedClusterCount))
	log.Info("the threshold hosted cluster count set to " + strconv.Itoa(aCtrl.thresholdHostedClusterCount))
	metrics.MaxNumHostedClustersGauge.Set(float64(maxHCNum))
	metrics.ThresholdNumHostedClustersGauge.Set(float64(thresholdHCNum))

	err = aCtrl.SyncAddOnPlacementScore(ctx, true)
	if err != nil {
		// AddOnPlacementScore must be created initially
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("failed to create AddOnPlacementScore, err: %w", err)
	}

	log.Info("starting manager")

	//+kubebuilder:scaffold:builder
	if err = aCtrl.SetupWithManager(mgr); err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
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
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("unable to create agent status controller: %s, err: %w", util.AddonStatusControllerName, err)
	}

	externalSecretController := &ExternalSecretController{
		hubClient:   hubClient,
		spokeClient: spokeKubeClient,
		log:         o.Log.WithName("external-secret-controller"),
	}

	if err = externalSecretController.SetupWithManager(mgr); err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("unable to create external secret controller: %s, err: %w", util.ExternalSecretControllerName, err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("unable to set up health check, err: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		metrics.AddonAgentFailedToStartBool.Set(1)
		return fmt.Errorf("unable to set up ready check, err: %w", err)
	}

	return mgr.Start(ctrl.SetupSignalHandler())
}

// serveHealthProbes serves health probes and configchecker.
func (c *agentController) serveHealthProbes(healthProbeBindAddress string, configCheck healthz.Checker) error {
	mux := http.NewServeMux()
	mux.Handle("/healthz", http.StripPrefix("/healthz", &healthz.Handler{Checks: map[string]healthz.Checker{
		"healthz-ping": healthz.Ping,
		"configz-ping": configCheck,
	}}))
	server := http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		Addr:              healthProbeBindAddress,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	c.log.Info("heath probes server is running...")
	return server.ListenAndServe()
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

func (c *agentController) generateExtManagedKubeconfigSecret(ctx context.Context, secretData map[string][]byte, hc hyperv1beta1.HostedCluster) error {
	// 1. Get hosted cluster's admin kubeconfig secret
	secret := &corev1.Secret{}
	secret.SetName("external-managed-kubeconfig")
	managedClusterAnnoValue, ok := hc.GetAnnotations()[util.ManagedClusterAnnoKey]
	if !ok || len(managedClusterAnnoValue) == 0 {
		managedClusterAnnoValue = hc.Name
	}
	secret.SetNamespace("klusterlet-" + managedClusterAnnoValue)
	kubeconfigData := secretData["kubeconfig"]

	klusterletNamespace := &corev1.Namespace{}
	klusterletNamespaceNsn := types.NamespacedName{Name: "klusterlet-" + managedClusterAnnoValue}

	if err := c.spokeClient.Get(ctx, klusterletNamespaceNsn, klusterletNamespace); err != nil {
		c.log.Error(err, fmt.Sprintf("failed to find the klusterlet namespace: %s ", klusterletNamespaceNsn.Name))
		return fmt.Errorf("failed to find the klusterlet namespace: %s", klusterletNamespaceNsn.Name)
	}

	if kubeconfigData == nil {
		return fmt.Errorf("failed to get kubeconfig from secret: %s", secret.GetName())
	}

	kubeconfig, err := clientcmd.Load(kubeconfigData)

	if err != nil {
		c.log.Error(err, fmt.Sprintf("failed to load kubeconfig from secret: %s", secret.GetName()))
		return fmt.Errorf("failed to load kubeconfig from secret: %s", secret.GetName())
	}

	if len(kubeconfig.Clusters) == 0 {
		c.log.Error(err, fmt.Sprintf("there is no cluster in kubeconfig from secret: %s", secret.GetName()))
		return fmt.Errorf("there is no cluster in kubeconfig from secret: %s", secret.GetName())
	}

	if kubeconfig.Clusters["cluster"] == nil {
		c.log.Error(err, fmt.Sprintf("failed to get a cluster from kubeconfig in secret: %s", secret.GetName()))
		return fmt.Errorf("failed to get a cluster from kubeconfig in secret: %s", secret.GetName())
	}

	// 2. Get the kube-apiserver service port
	apiServicePort, err := c.getAPIServicePort(hc)
	if err != nil {
		c.log.Error(err, "failed to get the kube api service port")
		return err
	}

	// 3. Replace the config.Clusters["cluster"].Server URL with internal kubeadpi service URL kube-apiserver.<Namespace>.svc.cluster.local
	apiServerURL := "https://kube-apiserver." + hc.Namespace + "-" + hc.Name + ".svc.cluster.local:" + apiServicePort
	kubeconfig.Clusters["cluster"].Server = apiServerURL

	newKubeconfig, err := clientcmd.Write(*kubeconfig)

	if err != nil {
		c.log.Error(err, fmt.Sprintf("failed to write new kubeconfig to secret: %s", secret.GetName()))
		return fmt.Errorf("failed to write new kubeconfig to secret: %s", secret.GetName())
	}

	secretData["kubeconfig"] = newKubeconfig

	secret.Data = secretData

	c.log.Info("Set the cluster server URL in external-managed-kubeconfig secret", "apiServerURL", apiServerURL)

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

func (c *agentController) getAPIServicePort(hc hyperv1beta1.HostedCluster) (string, error) {
	apiService := &corev1.Service{}
	apiServiceNsn := types.NamespacedName{Namespace: hc.Namespace + "-" + hc.Name, Name: "kube-apiserver"}
	err := c.spokeClient.Get(context.TODO(), apiServiceNsn, apiService)
	if err != nil {
		c.log.Error(err, "failed to find kube-apiserver service for the hosted cluster")
		return "", err
	}

	apiServicePort := apiService.Spec.Ports[0].Port

	return strconv.FormatInt(int64(apiServicePort), 10), nil
}

func (c *agentController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.log.Info(fmt.Sprintf("Reconciling triggered by %s in namespace %s", req.Name, req.Namespace))
	c.log.Info(fmt.Sprintf("Reconciling hostedcluster secrect %s", req))
	defer c.log.Info(fmt.Sprintf("Done reconcile hostedcluster secrect %s", req))

	// Update the AddOnPlacementScore resource, requeue reconcile if error occurred
	metrics.TotalReconcileCount.Inc() // increase reconcile action count
	if err := c.SyncAddOnPlacementScore(ctx, false); err != nil {
		c.log.Info(fmt.Sprintf("failed to create or update ethe AddOnPlacementScore %s, error: %s. Will try again in 30 seconds", util.HostedClusterScoresResourceName, err.Error()))
		metrics.ReconcileRequeueCount.Inc()
		metrics.FailedReconcileCount.Inc()
		return ctrl.Result{Requeue: true, RequeueAfter: time.Duration(1) * time.Minute}, nil
	}

	// Delete HC secrets on the hub using labels for HC and the hosting NS
	deleteMirrorSecrets := func() error {
		secretSelector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
			MatchLabels: map[string]string{
				util.HypershiftClusterNameLabel:      req.Name,
				util.HypershiftHostingNamespaceLabel: req.Namespace,
			},
		})
		if err != nil {
			c.log.Error(err, fmt.Sprintf("failed to convert label to get secrets on hub for hostedCluster: %s", req))
			return err
		}

		listopts := &client.ListOptions{}
		listopts.LabelSelector = secretSelector
		listopts.Namespace = c.clusterName
		hcHubSecretList := &corev1.SecretList{}
		err = c.hubClient.List(ctx, hcHubSecretList, listopts)
		if err != nil {
			c.log.Error(err, fmt.Sprintf("failed to get secrets on hub for hostedCluster: %s", req))
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

	hc := &hyperv1beta1.HostedCluster{}
	if err := c.spokeClient.Get(ctx, req.NamespacedName, hc); err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info(fmt.Sprintf("remove hostedcluster(%s) secrets on hub, since hostedcluster is gone", req.NamespacedName))

			return ctrl.Result{}, deleteMirrorSecrets()
		}

		c.log.Error(err, "failed to get the hostedcluster")
		return ctrl.Result{}, nil
	}

	if !hc.GetDeletionTimestamp().IsZero() {
		c.log.Info(fmt.Sprintf("hostedcluster %s has deletionTimestamp %s. Skip reconciling klusterlet secrets", hc.Name, hc.GetDeletionTimestamp().String()))

		if err := c.deleteManagedCluster(ctx, hc); err != nil {
			c.log.Error(err, "failed to delete the managed cluster")
		}

		return ctrl.Result{}, nil
	}

	if hc.Status.Conditions == nil || len(hc.Status.Conditions) == 0 ||
		!c.isHostedControlPlaneAvailable(hc.Status) {
		// Wait for secrets to exist
		c.log.Info(fmt.Sprintf("hostedcluster %s's control plane is not ready yet.", hc.Name))
		return ctrl.Result{}, nil
	}

	adminKubeConfigSecretWithCert := &corev1.Secret{}

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

			if strings.HasSuffix(hubMirrorSecret.Name, "kubeadmin-password") {
				if hc.Status.KubeadminPassword == nil {
					// the kubeadmin password secret is not ready yet
					// this secret will not be created if a custom identity provider
					// is configured in configuration.oauth.identityProviders
					c.log.Info("cannot find the kubeadmin password secret yet.")
					continue
				}
			}

			se.SetName(fmt.Sprintf("%s-%s", hc.Name, se.Name))
			if err := c.spokeClient.Get(ctx, client.ObjectKeyFromObject(se), se); err != nil {
				lastErr = err
				c.log.Error(err, fmt.Sprintf("failed to get hosted cluster secret %s on local cluster, skip this one", client.ObjectKeyFromObject(se)))
				continue
			}

			hubMirrorSecret.SetAnnotations(map[string]string{util.ManagedClusterAnnoKey: managedClusterAnnoValue})
			hubMirrorSecret.Data = se.Data

			if strings.HasSuffix(hubMirrorSecret.Name, "admin-kubeconfig") {
				// Create or update external-managed-kubeconfig secret for managed cluster registration agent
				c.log.Info("Generating external-managed-kubeconfig secret")

				extSecret := se.DeepCopy()

				errExt := c.generateExtManagedKubeconfigSecret(ctx, extSecret.Data, *hc)

				if errExt != nil {
					// This is where we avoid counting metrics for certain error conditions
					// Klusterlet namespace will not exist until import is done
					if !strings.Contains(errExt.Error(), "failed to find the klusterlet namespace") {
						metrics.KubeconfigSecretCopyFailureCount.Inc()
						lastErr = errExt // it is an error condition only if the klueterlet namespace exists
					}

				} else {
					c.log.Info("Successfully generated external-managed-kubeconfig secret")
				}

				// Replace certificate-authority-data from admin-kubeconfig
				servingCert := getServingCert(hc)
				if servingCert != "" {
					kubeconfig := hubMirrorSecret.Data["kubeconfig"]

					updatedKubeconfig, err := c.replaceCertAuthDataInKubeConfig(ctx, kubeconfig, hc.Namespace, servingCert)
					if err != nil {
						lastErr = err
						c.log.Info("failed to replace certificate-authority-data from kubeconfig")
						continue
					}

					c.log.Info(fmt.Sprintf("Replaced certificate-authority-data from secret: %v", hubMirrorSecret.Name))

					hubMirrorSecret.Data["kubeconfig"] = updatedKubeconfig
				}

				// Save this admin kubeconfig secret to use later to create the cluster claim
				// which requires connection to the hosted cluster's API server
				adminKubeConfigSecretWithCert = hubMirrorSecret
			}

			mutateFunc := func(secret *corev1.Secret, data map[string][]byte) controllerutil.MutateFn {
				return func() error {
					secret.Data = data
					return nil
				}
			}

			_, err := controllerutil.CreateOrUpdate(ctx, c.hubClient, hubMirrorSecret, mutateFunc(hubMirrorSecret, hubMirrorSecret.Data))
			if err != nil {
				lastErr = err
				c.log.Error(err, fmt.Sprintf("failed to createOrUpdate hostedcluster secret %s to hub", client.ObjectKeyFromObject(hubMirrorSecret)))
			} else {
				c.log.Info(fmt.Sprintf("createOrUpdate hostedcluster secret %s to hub", client.ObjectKeyFromObject(hubMirrorSecret)))
			}

		}

		return lastErr
	}

	metrics.TotalReconcileCount.Inc() // increase reconcile action count
	if err := createOrUpdateMirrorSecrets(); err != nil {
		c.log.Info(fmt.Sprintf("failed to create external-managed-kubeconfig and mirror secrets for hostedcluster %s, error: %s. Will try again in 30 seconds", hc.Name, err.Error()))

		//Not failure, namespace is still creating
		if !strings.Contains(err.Error(), "failed to find the klusterlet namespace") {
			metrics.FailedReconcileCount.Inc()
		}
		return ctrl.Result{Requeue: true, RequeueAfter: time.Duration(1) * time.Minute}, nil
	}

	if isVersionHistoryStateFound(hc.Status.Version.History, configv1.CompletedUpdate) {
		if err := c.createHostedClusterClaim(ctx, adminKubeConfigSecretWithCert,
			generateClusterClientFromSecret); err != nil {
			// just log the infomation and wait for the next reconcile to retry.
			// since the hosted cluster may:
			//   - not available now
			//   - have not been imported to the hub, and there is no clusterclaim CRD.
			c.log.Info("unable to create hosted cluster claim, wait for the next retry", "error", err.Error())
			// this is not critical for managing hosted clusters. don't count as a failed reconcile
			metrics.ReconcileRequeueCount.Inc()
			return ctrl.Result{Requeue: true, RequeueAfter: time.Duration(1) * time.Minute}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (c *agentController) isHostedControlPlaneAvailable(status hyperv1beta1.HostedClusterStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Reason == hyperv1beta1.AsExpectedReason && condition.Status == metav1.ConditionTrue && condition.Type == string(hyperv1beta1.HostedClusterAvailable) {
			return true
		}
	}
	return false
}

func isVersionHistoryStateFound(history []configv1.UpdateHistory, state configv1.UpdateState) bool {
	for _, h := range history {
		if h.State == state {
			return true
		}
	}
	return false
}

func (c *agentController) SyncAddOnPlacementScore(ctx context.Context, startup bool) error {
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
		// Emit metrics to return the number of placement score update failures
		metrics.PlacementScoreFailureCount.Inc()
		return err
	}

	listopts := &client.ListOptions{}
	hcList := &hyperv1beta1.HostedClusterList{}
	err = c.spokeUncachedClient.List(context.TODO(), hcList, listopts)
	hcCRDNotInstalledYet := err != nil &&
		(strings.HasPrefix(err.Error(), "no matches for kind ") || strings.HasPrefix(err.Error(), "no kind is registered ")) &&
		startup
	if hcCRDNotInstalledYet {
		c.log.Info("this is the initial agent startup and the hypershift CRDs are not installed yet, " + err.Error())
		c.log.Info("going to continue updating AddOnPlacementScore and cluster claims with zero HC count")
	}
	// During the first agent startup on a brand new cluster, the hypershift operator and its CRDs will not be installed yet.
	// So listing the HCs will fail. In this case, just set the count to len(hcList.Items) which is zero.
	if err != nil && !hcCRDNotInstalledYet {
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
			// Emit metrics to return the number of placement score update failures
			metrics.PlacementScoreFailureCount.Inc()
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

		// Total number of hosted clusters metric
		metrics.TotalHostedClusterGauge.Set(float64(hcCount))

		availableHcpNum := 0
		completedHcNum := 0
		deletingHcNum := 0

		for _, hc := range hcList.Items {
			if hc.Status.Conditions == nil || len(hc.Status.Conditions) == 0 || c.isHostedControlPlaneAvailable(hc.Status) {
				availableHcpNum++
			}

			if hc.Status.Version == nil || len(hc.Status.Version.History) == 0 ||
				isVersionHistoryStateFound(hc.Status.Version.History, configv1.CompletedUpdate) {
				completedHcNum++
			}

			if !hc.GetDeletionTimestamp().IsZero() {
				deletingHcNum++
			}
		}

		// Total number of available hosted control plains metric
		metrics.HostedControlPlaneAvailableGauge.Set(float64(availableHcpNum))
		// Total number of completed hosted clusters metric
		metrics.HostedClusterAvailableGauge.Set(float64(completedHcNum))
		// Total number of hosted clusters being deleted
		metrics.HostedClusterBeingDeletedGauge.Set(float64(deletingHcNum))

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
			// Emit metrics to return the number of placement score update failures
			metrics.PlacementScoreFailureCount.Inc()
			return err
		}

		c.log.Info(fmt.Sprintf("updated the addOnPlacementScore for %s: %v", c.clusterName, hcCount))

		// Based on the new HC count, update the zero, threshold, full cluster claim values.
		if err := c.createHostedClusterFullClusterClaim(ctx, hcCount); err != nil {
			c.log.Error(err, "failed to create or update hosted cluster full cluster claim")
			metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelFullClusterClaim).Inc()
			return err
		}

		if err = c.createHostedClusterThresholdClusterClaim(ctx, hcCount); err != nil {
			c.log.Error(err, "failed to create or update hosted cluster threshold cluster claim")
			metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelThresholdClusterClaim).Inc()
			return err
		}

		if err = c.createHostedClusterZeroClusterClaim(ctx, hcCount); err != nil {
			c.log.Error(err, "failed to create hosted cluster zero cluster claim")
			metrics.PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelZeroClusterClaim).Inc()
			return err
		}

		c.log.Info("updated the hosted cluster cound cluster claims successfully")
	}

	return nil
}

func (c *agentController) deleteManagedCluster(ctx context.Context, hc *hyperv1beta1.HostedCluster) error {
	if hc == nil {
		return fmt.Errorf("failed to delete nil hostedCluster")
	}

	managedClusterName, ok := hc.GetAnnotations()[util.ManagedClusterAnnoKey]
	if !ok || len(managedClusterName) == 0 {
		managedClusterName = hc.Name
	}

	// Delete the managed cluster
	mc := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: managedClusterName,
		},
	}

	if err := c.hubClient.Get(ctx, client.ObjectKeyFromObject(mc), mc); err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info(fmt.Sprintf("managedCluster %v is already deleted", managedClusterName))
			mc = nil
		} else {
			c.log.Info(fmt.Sprintf("failed to get the managedCluster %v", managedClusterName))
			return err
		}
	}

	if mc != nil {
		if err := c.hubClient.Delete(ctx, mc); err != nil {
			c.log.Info(fmt.Sprintf("failed to delete the managedCluster %v", managedClusterName))
			return err
		}

		c.log.Info(fmt.Sprintf("deleted managedCluster %v", managedClusterName))
	}

	klusterletName := "klusterlet-" + managedClusterName

	// Remove the operator.open-cluster-management.io/klusterlet-hosted-cleanup finalizer in klusterlet
	klusterlet := &operatorapiv1.Klusterlet{
		ObjectMeta: metav1.ObjectMeta{
			Name: klusterletName,
		},
	}

	if err := c.spokeUncachedClient.Get(ctx, client.ObjectKeyFromObject(klusterlet), klusterlet); err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Info(fmt.Sprintf("klusterlet %v is already deleted", klusterletName))
			return nil
		} else {
			c.log.Info(fmt.Sprintf("failed to get the klusterlet %v", klusterletName))
			return err
		}
	}

	updated := controllerutil.RemoveFinalizer(klusterlet, "operator.open-cluster-management.io/klusterlet-hosted-cleanup")
	c.log.Info(fmt.Sprintf("klusterlet %v finalizer removed:%v", klusterletName, updated))

	if updated {
		if err := c.spokeUncachedClient.Update(ctx, klusterlet); err != nil {
			c.log.Info("failed to update klusterlet to remove the finalizer")
			return err
		}
	}

	return nil
}

func (c *agentController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1beta1.HostedCluster{}).
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

		if err := hyperv1beta1.AddToScheme(scheme); err != nil {
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

func (c *agentController) replaceCertAuthDataInKubeConfig(ctx context.Context, kubeconfig []byte, certNs, certName string) ([]byte, error) {
	secret := &corev1.Secret{}
	if err := c.spokeClient.Get(ctx, types.NamespacedName{Namespace: certNs, Name: certName}, secret); err != nil {
		c.log.Info(fmt.Sprintf("failed to get secret for serving certificate %v/%v", certNs, certName))

		return nil, err
	}

	tlsCrt := secret.Data["tls.crt"]
	if tlsCrt == nil {
		err := fmt.Errorf("invalid serving certificate secret")
		c.log.Info(err.Error())

		return nil, err
	}

	config, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, err
	}

	for _, v := range config.Clusters {
		v.CertificateAuthorityData = tlsCrt
	}

	updatedConfig, err := clientcmd.Write(*config)
	if err != nil {
		return nil, err
	}

	return updatedConfig, nil
}

// Retrieves the first serving certificate
func getServingCert(hc *hyperv1beta1.HostedCluster) string {
	if hc.Spec.Configuration != nil &&
		hc.Spec.Configuration.APIServer != nil &&
		&hc.Spec.Configuration.APIServer.ServingCerts != nil &&
		len(hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates) > 0 {
		return hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates[0].ServingCertificate.Name
	}

	return ""
}
