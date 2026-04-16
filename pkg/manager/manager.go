package manager

import (
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
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
	frameworkagent "open-cluster-management.io/addon-framework/pkg/agent"
	"open-cluster-management.io/addon-framework/pkg/utils"
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
	runController := func(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
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
			if err := mgr.Start(ctx); err != nil {
				log.Error(err, "failed to start addon framework manager")
				os.Exit(1)
			}
		}()

		// Create a separate controller-runtime manager for custom controllers
		// Use the same scheme that has all the types registered
		customMgr, err := ctrl.NewManager(controllerContext.KubeConfig, ctrl.Options{
			Scheme:           genericScheme,
			LeaderElection:   false, // Disable leader election for custom manager
			LeaderElectionID: "custom-controller-leader-election",
		})
		if err != nil {
			log.Error(err, "failed to create custom controller manager")
			return err
		}

		// Verify the scheme has the required types
		gvk := addonapiv1alpha1.SchemeGroupVersion.WithKind("ManagedClusterAddOn")
		if !genericScheme.Recognizes(gvk) {
			log.Error(fmt.Errorf("scheme does not recognize ManagedClusterAddOn"), "scheme verification failed")
			return fmt.Errorf("scheme does not recognize ManagedClusterAddOn")
		}
		log.Info("Scheme verification successful", "gvk", gvk)

		// Add the discovery config controller to the custom manager
		discoveryConfigController := &DiscoveryConfigController{
			Client:            hubClient,
			Log:               log.WithName("discovery-config-controller"),
			Scheme:            genericScheme,
			OperatorNamespace: controllerContext.OperatorNamespace,
		}

		if err = discoveryConfigController.SetupWithManager(customMgr); err != nil {
			log.Error(err, "failed to setup discovery config controller")
			return err
		}

		// Start the custom controller manager in a goroutine
		go func() {
			log.Info("starting custom controller manager")
			if err := customMgr.Start(ctx); err != nil {
				log.Error(err, "failed to start custom controller manager")
			}
		}()

		err = EnableHypershiftCLIDownload(hubClient, log)
		if err != nil {
			// unable to install HypershiftCLIDownload is not critical.
			// log and continue
			log.Error(err, "failed to enable hypershift CLI download")
		}

		<-ctx.Done()

		return nil
	}

	cmdConfig := controllercmd.
		NewControllerCommandConfig(componentName, version.Get(), runController, clock.RealClock{})

	cmd := cmdConfig.NewCommand()
	cmd.Use = "manager"
	cmd.Short = fmt.Sprintf("Start the %s's manager", componentName)

	// add disable leader election flag
	flags := cmd.Flags()
	flags.BoolVar(&cmdConfig.DisableLeaderElection, "disable-leader-election", true, "Disable leader election for the agent.")
	flags.BoolVar(&withOverride, "with-image-override", false, "Use image from override configmap")

	return cmd
}

func getAgentAddon(componentName string, o *override, controllerContext *controllercmd.ControllerContext, addonClient addonv1alpha1client.Interface) (frameworkagent.AgentAddon, error) {
	registrationOption, err := newRegistrationOption(
		controllerContext.KubeConfig,
		componentName,
		utilrand.String(5),
	)

	if err != nil {
		return nil, err
	}

	return addonfactory.NewAgentAddonFactory(componentName, fs, templatePath).
		WithConfigGVRs(
			schema.GroupVersionResource{Group: "addon.open-cluster-management.io", Version: "v1alpha1", Resource: "addondeploymentconfigs"},
		).
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

    return &agent.RegistrationOption{
        CSRConfigurations: agent.KubeClientSignerConfigurations(addonName, agentName),
        // CSRApproveCheck is only needed for CSR authentication
        CSRApproveCheck:   utils.DefaultCSRApprover(agentName),
        // Use BindKubeClientRole - works with both CSR and token authentication
        PermissionConfig: utils.NewRBACPermissionConfigBuilder(kubeclient).
            BindKubeClientRole(&rbacv1.Role{
                ObjectMeta: metav1.ObjectMeta{
                    Name: fmt.Sprintf("open-cluster-management:%s:agent", addonName),
                },
                Rules: []rbacv1.PolicyRule{
                    {APIGroups: []string{""}, Resources: []string{"secrets", "configmaps"},
                        Verbs: []string{"get", "list", "watch", "create", "delete", "update"}},
                    {APIGroups: []string{"addon.open-cluster-management.io"}, Resources: []string{"managedclusteraddons"},
                        Verbs: []string{"get", "list", "watch", "update", "patch"}},
                    {APIGroups: []string{"addon.open-cluster-management.io"}, Resources: []string{"managedclusteraddons/status"},
                        Verbs: []string{"patch", "update"}},
                    {APIGroups: []string{"cluster.open-cluster-management.io"}, Resources: []string{"addonplacementscores", "addonplacementscores/status"},
                        Verbs: []string{"get", "list", "watch", "create", "delete", "update", "patch"}},
                    {APIGroups: []string{"discovery.open-cluster-management.io"}, Resources: []string{"discoveredclusters"},
                        Verbs: []string{"get", "list", "watch", "create", "update", "delete", "deletecollection", "patch"}},
                },
            }).
			BindKubeClientClusterRole(&rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("open-cluster-management:%s:agent", addonName),
				},
				Rules: []rbacv1.PolicyRule{
					{Verbs: []string{"get", "list", "watch", "patch", "delete"}, Resources: []string{"managedclusters"}, APIGroups: []string{"cluster.open-cluster-management.io"}},
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
	}

	return addonfactory.StructToValues(manifestConfig), nil
}
