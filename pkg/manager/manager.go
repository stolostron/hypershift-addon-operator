package manager

import (
	"context"
	"embed"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/openshift/library-go/pkg/assets"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/component-base/version"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"open-cluster-management.io/addon-framework/pkg/addonfactory"
	"open-cluster-management.io/addon-framework/pkg/addonmanager"
	frameworkagent "open-cluster-management.io/addon-framework/pkg/agent"
	"open-cluster-management.io/addon-framework/pkg/utils"
	addonapiv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

var (
	genericScheme = runtime.NewScheme()
	genericCodecs = serializer.NewCodecFactory(genericScheme)
	genericCodec  = genericCodecs.UniversalDeserializer()
)

const (
	hypershiftAddonImageName    = "HYPERSHIFT_ADDON_IMAGE_NAME"
	hypershiftOperatorImageName = "HYPERSHIFT_OPERATOR_IMAGE_NAME"
	templatePath                = "manifests/templates"
)

//go:embed manifests
//go:embed manifests/templates
var fs embed.FS

var agentPermissionFiles = []string{
	// role with RBAC rules to access resources on hub
	"manifests/permission/role.yaml",
	// rolebinding to bind the above role to a certain user group
	"manifests/permission/rolebinding.yaml",
}

func NewManagerCommand(componentName string, log logr.Logger) *cobra.Command {
	runController := func(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
		mgr, err := addonmanager.New(controllerContext.KubeConfig)
		if err != nil {
			return err
		}
		registrationOption := newRegistrationOption(
			controllerContext.KubeConfig,
			controllerContext.EventRecorder,
			componentName,
			utilrand.String(5),
		)

		agentAddon, err := addonfactory.NewAgentAddonFactory(componentName, fs, templatePath).
			WithGetValuesFuncs(getValueForAgentTemplate, addonfactory.GetValuesFromAddonAnnotation).
			WithAgentRegistrationOption(registrationOption).
			WithInstallStrategy(frameworkagent.InstallAllStrategy(util.AgentInstallationNamespace)).
			BuildTemplateAgentAddon()
		if err != nil {
			log.Error(err, "failed to build agent")
			return err
		}

		err = mgr.AddAgent(agentAddon)
		if err != nil {
			log.Error(err, "failed to add agent")
			os.Exit(1)
		}

		err = mgr.Start(ctx)
		if err != nil {
			log.Error(err, "failed to start addon framework manager")
			os.Exit(1)
		}
		<-ctx.Done()

		return nil
	}

	cmd := controllercmd.
		NewControllerCommandConfig(componentName, version.Get(), runController).
		NewCommand()
	cmd.Use = "manager"
	cmd.Short = fmt.Sprintf("Start the %s's manager", componentName)

	return cmd
}

func newRegistrationOption(kubeConfig *rest.Config, recorder events.Recorder, componentName, agentName string) *frameworkagent.RegistrationOption {
	return &frameworkagent.RegistrationOption{
		CSRConfigurations: frameworkagent.KubeClientSignerConfigurations(componentName, agentName),
		CSRApproveCheck:   utils.DefaultCSRApprover(agentName),
		PermissionConfig: func(cluster *clusterv1.ManagedCluster, addon *addonapiv1alpha1.ManagedClusterAddOn) error {
			kubeclient, err := kubernetes.NewForConfig(kubeConfig)
			if err != nil {
				return err
			}

			for _, file := range agentPermissionFiles {
				if err := applyAgentPermissionManifestFromFile(file, cluster.Name, addon.Name, kubeclient, recorder); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

func applyAgentPermissionManifestFromFile(file, clusterName, componentName string, kubeclient *kubernetes.Clientset, recorder events.Recorder) error {
	groups := frameworkagent.DefaultGroups(clusterName, componentName)
	config := struct {
		ClusterName            string
		Group                  string
		RoleAndRolebindingName string
	}{
		ClusterName: clusterName,

		Group:                  groups[0],
		RoleAndRolebindingName: fmt.Sprintf("open-cluster-management:%s:agent", componentName),
	}

	results := resourceapply.ApplyDirectly(
		context.Background(),
		resourceapply.NewKubeClientHolder(kubeclient),
		recorder,
		resourceapply.NewResourceCache(),
		func(name string) ([]byte, error) {
			template, err := fs.ReadFile(file)
			if err != nil {
				return nil, err
			}

			data := assets.MustCreateAssetFromTemplate(name, template, config).Data

			return data, nil
		},
		file,
	)

	for _, result := range results {
		if result.Error != nil {
			return result.Error
		}
	}

	return nil
}

// getValues prepare values for templates at manifests/templates
func getValueForAgentTemplate(cluster *clusterv1.ManagedCluster,
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

	manifestConfig := struct {
		KubeConfigSecret               string
		ClusterName                    string
		AddonName                      string
		AddonInstallNamespace          string
		HypershiftBucketNamespaceOnHub string
		HypershiftOperatorImage        string
		Image                          string
		SpokeRolebindingName           string
		AgentServiceAccountName        string
	}{
		KubeConfigSecret:               fmt.Sprintf("%s-hub-kubeconfig", addon.Name),
		AddonInstallNamespace:          installNamespace,
		HypershiftBucketNamespaceOnHub: util.HypershiftBucketNamespaceOnHub,
		ClusterName:                    cluster.Name,
		AddonName:                      fmt.Sprintf("%s-agent", addon.Name),
		Image:                          addonImage,
		HypershiftOperatorImage:        operatorImage,
		SpokeRolebindingName:           addon.Name,
		AgentServiceAccountName:        fmt.Sprintf("%s-agent-sa", addon.Name),
	}

	return addonfactory.StructToValues(manifestConfig), nil
}
