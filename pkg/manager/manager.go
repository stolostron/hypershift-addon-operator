package manager

import (
	"context"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientfeatures "k8s.io/client-go/features"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/component-base/version"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	consolev1 "github.com/openshift/api/console/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/hypershift/support/rhobsmonitoring"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	mcev1 "github.com/stolostron/backplane-operator/api/v1"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	"open-cluster-management.io/addon-framework/pkg/addonmanager"
	"open-cluster-management.io/addon-framework/pkg/agent"
	addonutil "open-cluster-management.io/addon-framework/pkg/utils"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonv1alpha1client "open-cluster-management.io/api/client/addon/clientset/versioned"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

var (
	genericScheme = runtime.NewScheme()
	genericCodecs = serializer.NewCodecFactory(genericScheme)
	genericCodec  = genericCodecs.UniversalDeserializer()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(genericScheme))
	utilruntime.Must(configv1.AddToScheme(genericScheme))
	utilruntime.Must(operatorsv1alpha1.AddToScheme(genericScheme))
	utilruntime.Must(routev1.AddToScheme(genericScheme))
	utilruntime.Must(consolev1.AddToScheme(genericScheme))
	utilruntime.Must(appsv1.AddToScheme(genericScheme))
	utilruntime.Must(rbacv1.AddToScheme(genericScheme))
	utilruntime.Must(monitoringv1.AddToScheme(genericScheme))
	utilruntime.Must(rhobsmonitoring.AddToScheme(genericScheme))
	utilruntime.Must(mcev1.AddToScheme(genericScheme))
	utilruntime.Must(addonapiv1alpha1.AddToScheme(genericScheme))
	utilruntime.Must(clusterv1.AddToScheme(genericScheme))
}

const (
	hypershiftAddonImageName    = "HYPERSHIFT_ADDON_IMAGE_NAME"
	hypershiftOperatorImageName = "HYPERSHIFT_OPERATOR_IMAGE_NAME"
	kubeRbacProxyImageName      = "KUBE_RBAC_PROXY_IMAGE_NAME"
	templatePath                = "manifests/templates"

	addonAPIGroup     = "addon.open-cluster-management.io"
	clusterAPIGroup   = "cluster.open-cluster-management.io"
	discoveryAPIGroup = "discovery.open-cluster-management.io"
)

//go:embed manifests
//go:embed manifests/templates
var fs embed.FS

type override struct {
	client.Client
	log               logr.Logger
	operatorNamespace string
	withOverride      bool
}

func NewManagerCommand(componentName string, log logr.Logger) *cobra.Command {
	var withOverride bool
	var disableTLSWatcher bool
	runController := func(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
		if err := disableWatchListClient(); err != nil {
			return err
		}

		// Child context: SecurityProfileWatcher cancels this to restart on TLS profile change.
		managerCtx, cancelManager := context.WithCancel(ctx)
		defer cancelManager()

		mgr, err := addonmanager.New(controllerContext.KubeConfig)
		if err != nil {
			return err
		}

		hubClient, err := client.New(controllerContext.KubeConfig, client.Options{Scheme: genericScheme})
		if err != nil {
			log.Error(err, "failed to create hub client to fetch downstream image override configmap")
			return err
		}

		o := &override{
			Client:            hubClient,
			log:               log.WithName("override-values"),
			operatorNamespace: controllerContext.OperatorNamespace,
			withOverride:      withOverride,
		}

		addonClient, err := addonv1alpha1client.NewForConfig(controllerContext.KubeConfig)
		if err != nil {
			return err
		}

		agentAddon, err := getAgentAddon(componentName, o, controllerContext, addonClient)
		if err != nil {
			log.Error(err, "failed to build agent")
			return err
		}

		err = mgr.AddAgent(agentAddon)
		if err != nil {
			log.Error(err, "failed to add agent")
			os.Exit(1)
		}

		// Start the addon framework manager in a goroutine
		go func() {
			if err := mgr.Start(managerCtx); err != nil {
				log.Error(err, "failed to start addon framework manager")
				os.Exit(1)
			}
		}()

		customMgr, err := newCustomControllerManager(controllerContext, hubClient, log)
		if err != nil {
			return err
		}

		profileSpec, err := fetchTLSProfileOrDefault(managerCtx, hubClient, log)
		if err != nil {
			return err
		}

		err = setupTLSProfileWatcher(
			customMgr, hubClient, profileSpec, disableTLSWatcher, cancelManager, log)
		if err != nil {
			return err
		}

		// Start the custom controller manager in a goroutine.
		// If it fails to start (e.g. informer cache sync timeout), cancel the
		// manager context so the pod restarts and gets a clean retry.
		go func() {
			log.Info("starting custom controller manager")
			if err := customMgr.Start(managerCtx); err != nil {
				log.Error(err, "failed to start custom controller manager")
				cancelManager()
			}
		}()

		go startHCPProxy(managerCtx, profileSpec, controllerContext.KubeConfig, hubClient, log)

		err = EnableHypershiftCLIDownload(ctx, hubClient, log)
		if err != nil {
			// unable to install HypershiftCLIDownload is not critical.
			// log and continue
			log.Error(err, "failed to enable hypershift CLI download")
		}

		<-managerCtx.Done()

		return nil
	}

	cmdConfig := controllercmd.
		NewControllerCommandConfig(componentName, version.Get(), runController, clock.RealClock{})

	cmd := cmdConfig.NewCommand()
	cmd.Use = "manager"
	cmd.Short = fmt.Sprintf("Start the %s's manager", componentName)

	// add disable leader election flag
	flags := cmd.Flags()
	flags.BoolVar(&cmdConfig.DisableLeaderElection,
		"disable-leader-election", true,
		"Disable leader election for the agent.")
	flags.BoolVar(&withOverride, "with-image-override", false, "Use image from override configmap")
	flags.BoolVar(&disableTLSWatcher, "disable-tls-watcher", false,
		"Disable the TLS security profile watcher (local development only).")

	return cmd
}

// disableWatchListClient turns off the WatchListClient feature gate (ACM-36014).
// The default Beta gate breaks cache sync on CRDs.
func disableWatchListClient() error {
	if fg, ok := clientfeatures.FeatureGates().(interface {
		Set(clientfeatures.Feature, bool) error
	}); ok {
		if err := fg.Set(clientfeatures.WatchListClient, false); err != nil {
			return fmt.Errorf("disable WatchListClient feature gate: %w", err)
		}
		return nil
	}
	if err := os.Setenv("KUBE_FEATURE_WatchListClient", "false"); err != nil {
		return fmt.Errorf("set KUBE_FEATURE_WatchListClient env var: %w", err)
	}
	return nil
}

func newCustomControllerManager(
	controllerContext *controllercmd.ControllerContext,
	hubClient client.Client,
	log logr.Logger,
) (ctrl.Manager, error) {
	customMgr, err := ctrl.NewManager(controllerContext.KubeConfig, ctrl.Options{
		Scheme:           genericScheme,
		LeaderElection:   false,
		LeaderElectionID: "custom-controller-leader-election",
	})
	if err != nil {
		log.Error(err, "failed to create custom controller manager")
		return nil, err
	}

	gvk := addonapiv1alpha1.SchemeGroupVersion.WithKind("ManagedClusterAddOn")
	if !genericScheme.Recognizes(gvk) {
		log.Error(fmt.Errorf("scheme does not recognize ManagedClusterAddOn"), "scheme verification failed")
		return nil, fmt.Errorf("scheme does not recognize ManagedClusterAddOn")
	}
	log.Info("Scheme verification successful", "gvk", gvk)

	discoveryConfigController := &DiscoveryConfigController{
		Client:            hubClient,
		Log:               log.WithName("discovery-config-controller"),
		Scheme:            genericScheme,
		OperatorNamespace: controllerContext.OperatorNamespace,
	}
	if err = discoveryConfigController.SetupWithManager(customMgr); err != nil {
		log.Error(err, "failed to setup discovery config controller")
		return nil, err
	}
	return customMgr, nil
}

func fetchTLSProfileOrDefault(
	ctx context.Context,
	hubClient client.Client,
	log logr.Logger,
) (configv1.TLSProfileSpec, error) {
	profileSpec, err := tlspkg.FetchAPIServerTLSProfile(ctx, hubClient)
	if err != nil {
		log.Error(err, "failed to fetch APIServer TLS profile, using Intermediate defaults")
		profileSpec, _ = tlspkg.GetTLSProfileSpec(nil)
	}
	return profileSpec, nil
}

func setupTLSProfileWatcher(
	customMgr ctrl.Manager,
	hubClient client.Client,
	profileSpec configv1.TLSProfileSpec,
	disableTLSWatcher bool,
	cancelManager context.CancelFunc,
	log logr.Logger,
) error {
	if disableTLSWatcher {
		log.Info("TLS security profile watcher disabled by flag")
		return nil
	}
	// kind / vanilla k8s lack config.openshift.io APIServer. Registering the
	// watcher there spams "no matches for kind APIServer" every poll interval.
	apiServerGVK := configv1.GroupVersion.WithKind("APIServer")
	if _, err := customMgr.GetRESTMapper().RESTMapping(
		apiServerGVK.GroupKind(), apiServerGVK.Version,
	); err != nil {
		log.Info("APIServer CRD not available, skipping TLS profile watcher",
			"error", err)
		return nil
	}
	// When the cluster TLS profile changes, cancelManager() triggers a graceful
	// shutdown — the pod restarts and picks up the new profile.
	watcher := &tlspkg.SecurityProfileWatcher{
		Client:                hubClient,
		InitialTLSProfileSpec: profileSpec,
		OnProfileChange: func(_ context.Context, old, new configv1.TLSProfileSpec) {
			log.Info("cluster TLS profile changed, initiating graceful restart",
				"old", old.MinTLSVersion, "new", new.MinTLSVersion)
			cancelManager()
		},
	}
	if err := watcher.SetupWithManager(customMgr); err != nil {
		log.Error(err, "failed to setup TLS security profile watcher")
		return err
	}
	return nil
}

func startHCPProxy(
	ctx context.Context,
	profileSpec configv1.TLSProfileSpec,
	kubeConfig *rest.Config,
	hubClient client.Client,
	log logr.Logger,
) {
	err := StartHCPProxy(ctx, profileSpec, kubeConfig, hubClient, log.WithName("hcp-proxy"))
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error(err, "HCP proxy stopped unexpectedly")
	}
}

func getAgentAddon(
	componentName string, o *override,
	controllerContext *controllercmd.ControllerContext,
	addonClient addonv1alpha1client.Interface,
) (agent.AgentAddon, error) {
	registrationOption, err := newRegistrationOption(
		controllerContext.KubeConfig,
		componentName,
		utilrand.String(5),
	)

	if err != nil {
		return nil, err
	}

	return addonfactory.NewAgentAddonFactory(componentName, fs, templatePath).
		WithConfigGVRs(schema.GroupVersionResource{
			Group: addonAPIGroup, Version: "v1alpha1",
			Resource: "addondeploymentconfigs",
		}).
		WithGetValuesFuncs(
			o.getValueForAgentTemplate,
			addonfactory.GetValuesFromAddonAnnotation,
			addonfactory.GetAddOnDeploymentConfigValues(
				addonutil.NewAddOnDeploymentConfigGetter(addonClient),
				addonfactory.ToAddOnDeploymentConfigValues,
				addonfactory.ToAddOnResourceRequirementsValues,
			)).
		WithAgentRegistrationOption(registrationOption).
		WithAgentInstallNamespace(addonutil.AgentInstallNamespaceFromDeploymentConfigFunc(
			addonutil.NewAddOnDeploymentConfigGetter(addonClient))).
		WithScheme(genericScheme).
		BuildTemplateAgentAddon()
}

func newRegistrationOption(kubeConfig *rest.Config, addonName, agentName string) (*agent.RegistrationOption, error) {
	if kubeConfig == nil {
		return nil, fmt.Errorf("kubeConfig must not be nil")
	}
	kubeclient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	roleName := fmt.Sprintf(
		"open-cluster-management:%s:agent", addonName)

	allVerbs := []string{
		"get", "list", "watch", "create",
		"update", "delete", "deletecollection", "patch",
	}

	return &agent.RegistrationOption{
		CSRConfigurations: agent.KubeClientSignerConfigurations(
			addonName, agentName),
		CSRApproveCheck: addonutil.DefaultCSRApprover(agentName),
		PermissionConfig: addonutil.NewRBACPermissionConfigBuilder(
			kubeclient).
			BindKubeClientRole(&rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{Name: roleName},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{""},
						Resources: []string{"secrets", "configmaps"},
						Verbs: []string{
							"get", "list", "watch",
							"create", "delete", "update",
						},
					},
					{
						APIGroups: []string{addonAPIGroup},
						Resources: []string{"managedclusteraddons"},
						Verbs: []string{
							"get", "list", "watch",
							"update", "patch",
						},
					},
					{
						APIGroups: []string{addonAPIGroup},
						Resources: []string{
							"managedclusteraddons/status",
						},
						Verbs: []string{"patch", "update"},
					},
					{
						APIGroups: []string{clusterAPIGroup},
						Resources: []string{
							"addonplacementscores",
							"addonplacementscores/status",
						},
						Verbs: allVerbs,
					},
					{
						APIGroups: []string{discoveryAPIGroup},
						Resources: []string{
							"discoveredclusters",
						},
						Verbs: allVerbs,
					},
				},
			}).
			BindKubeClientClusterRole(&rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: roleName},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{clusterAPIGroup},
						Resources: []string{"managedclusters"},
						Verbs: []string{
							"get", "list", "watch",
							"patch", "delete",
						},
					},
				},
			}).
			Build(),
	}, nil
}

// getValues prepare values for templates at manifests/templates
func (o *override) getValueForAgentTemplate(cluster *clusterv1.ManagedCluster,
	addon *addonapiv1alpha1.ManagedClusterAddOn) (addonfactory.Values, error) {
	installNamespace := addon.Spec.InstallNamespace
	if len(installNamespace) == 0 {
		installNamespace = util.AgentInstallationNamespace
	}

	addonImage := os.Getenv(hypershiftAddonImageName)
	if len(addonImage) == 0 {
		addonImage = util.DefaultHypershiftAddonImage
	}

	operatorImage := os.Getenv(hypershiftOperatorImageName)
	if len(operatorImage) == 0 {
		operatorImage = util.DefaultHypershiftOperatorImage
	}

	kubeRbacProxyImage := os.Getenv(kubeRbacProxyImageName)
	if len(kubeRbacProxyImage) == 0 {
		kubeRbacProxyImage = util.DefaultKubeRbacProxyImage
	}

	content := ""

	if o.withOverride {
		cm := &corev1.ConfigMap{}
		cmKey := types.NamespacedName{Name: util.HypershiftDownstreamOverride, Namespace: o.operatorNamespace}
		if err := o.Client.Get(context.TODO(), cmKey, cm); err != nil {
			return nil, fmt.Errorf("failed to get override configmap, err: %w", err)
		}

		c := cm.Data[util.HypershiftOverrideKey]
		content = base64.StdEncoding.EncodeToString([]byte(c))
	}

	tlsMinVersion, tlsCipherSuites := o.getTLSProfileValues()

	manifestConfig := struct {
		KubeConfigSecret                    string
		ClusterName                         string
		AddonName                           string
		AddonInstallNamespace               string
		HypershiftOperatorImage             string
		KubeRbacProxyImage                  string
		Image                               string
		SpokeRolebindingName                string
		AgentServiceAccountName             string
		HypershiftDownstreamOverride        string
		HypershiftOverrideKey               string
		HypershiftDownstreamOverrideContent string
		HyeprshiftImageOverride             bool
		MulticlusterEnginePullSecret        string
		TLSMinVersion                       string
		TLSCipherSuites                     string
	}{
		KubeConfigSecret:                    fmt.Sprintf("%s-hub-kubeconfig", addon.Name),
		AddonInstallNamespace:               installNamespace,
		ClusterName:                         cluster.Name,
		AddonName:                           fmt.Sprintf("%s-agent", addon.Name),
		Image:                               addonImage,
		HypershiftOperatorImage:             operatorImage,
		KubeRbacProxyImage:                  kubeRbacProxyImage,
		SpokeRolebindingName:                fmt.Sprintf("%s-%s", cluster.Name, addon.Name),
		AgentServiceAccountName:             fmt.Sprintf("%s-agent-sa", addon.Name),
		HyeprshiftImageOverride:             o.withOverride,
		HypershiftOverrideKey:               util.HypershiftOverrideKey,
		HypershiftDownstreamOverride:        util.HypershiftDownstreamOverride,
		HypershiftDownstreamOverrideContent: content,
		MulticlusterEnginePullSecret:        util.MulticlusterEnginePullSecret,
		TLSMinVersion:                       tlsMinVersion,
		TLSCipherSuites:                     tlsCipherSuites,
	}

	return addonfactory.StructToValues(manifestConfig), nil
}

// getTLSProfileValues reads the cluster APIServer's TLS security profile and
// returns the min TLS version and comma-separated IANA cipher suite names suitable
// for kube-rbac-proxy flags. Falls back to Intermediate profile on error.
func (o *override) getTLSProfileValues() (string, string) {
	ctx := context.Background()
	profileSpec, err := tlspkg.FetchAPIServerTLSProfile(ctx, o.Client)
	if err != nil {
		o.log.Info("unable to read APIServer TLS profile, using Intermediate defaults", "error", err)
		profileSpec, _ = tlspkg.GetTLSProfileSpec(nil)
	}

	minVersion := string(profileSpec.MinTLSVersion)
	cipherSuites := strings.Join(crypto.OpenSSLToIANACipherSuites(profileSpec.Ciphers), ",")

	return minVersion, cipherSuites
}
