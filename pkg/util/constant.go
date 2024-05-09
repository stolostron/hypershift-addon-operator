package util

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	DefaultHypershiftAddonImage = "quay.io/stolostron/hypershift-addon-operator:latest"

	DefaultHypershiftOperatorImage = "quay.io/hypershift/hypershift-operator:latest"

	DefaultKubeRbacProxyImage = "registry.redhat.io/openshift4/ose-kube-rbac-proxy:v4.10"

	// AgentInstallationNamespace is the namespace on the managed cluster to install the addon agent.
	AgentInstallationNamespace = "open-cluster-management-agent-addon"

	MulticlusterEnginePullSecret = "open-cluster-management-image-pull-credentials"

	HypershiftDownstreamOverride = "hypershift-operator-imagestream"
	HypershiftOverrideKey        = "imagestream"
	AddonControllerName          = "hypershift-addon"
	AddonStatusControllerName    = "hypershift-addon-status"
	AutoImportControllerName     = "auto-import"
	DiscoveryControllerName      = "discovery"
	ExternalSecretControllerName = "external-secret"
	AgentDeploymentName          = "hypershift-addon-agent"

	HypershiftOverrideImagesCM = "hypershift-override-images"
	ImageUpgradeControllerName = "hypershift-image-upgrade"
	HypershiftInstallFlagsCM   = "hypershift-operator-install-flags"

	HCPSizingBaselineCM = "hcp-sizing-baseline"

	HypershiftOperatorNamespace       = "hypershift"
	HypershiftOperatorName            = "operator"
	HypershiftOperatorExternalDNSName = "external-dns"

	// Labels for resources to reference the Hosted Cluster
	HypershiftClusterNameLabel           = "hypershiftdeployments.cluster.open-cluster-management.io/cluster-name"
	HypershiftHostingNamespaceLabel      = "hypershiftdeployments.cluster.open-cluster-management.io/hosting-namespace"
	HypershiftOperatorNoMCEAnnotationKey = "hypershift.open-cluster-management.io/not-by-mce"
	HostedClusterNameLabel               = "hypershift.open-cluster-management.io/hc-name"
	HostedClusterNamespaceLabel          = "hypershift.open-cluster-management.io/hc-namespace"

	// ImageStream image names
	ImageStreamAwsCapiProvider      = "cluster-api-provider-aws"
	ImageStreamAzureCapiProvider    = "cluster-api-provider-azure"
	ImageStreamKubevertCapiProvider = "cluster-api-provider-kubevirt"
	ImageStreamKonnectivity         = "apiserver-network-proxy"
	ImageStreamAwsEncyptionProvider = "aws-encryption-provider"
	ImageStreamClusterApi           = "cluster-api"
	ImageStreamAgentCapiProvider    = "cluster-api-provider-agent"
	ImageStreamHypershiftOperator   = "hypershift-operator"

	HypershiftBucketSecretName      = "hypershift-operator-oidc-provider-s3-credentials"
	HypershiftPrivateLinkSecretName = "hypershift-operator-private-link-credentials"
	HypershiftExternalDNSSecretName = "hypershift-operator-external-dns-credentials"
	ManagedClusterAnnoKey           = "cluster.open-cluster-management.io/managedcluster-name"

	// HyperShift install job
	HypershiftInstallJobName           = "hypershift-install-job-"
	HypershiftInstallJobServiceAccount = "hypershift-addon-agent-sa"
	HypershiftInstallJobVolume         = "hypershift-imagestream-volume"
	HypershiftInstallJobImageStream    = "hypershift-install-job-imagestream"

	// Hypershift Operator Deployment env vars for images
	HypershiftEnvVarImageAwsCapiProvider      = "IMAGE_AWS_CAPI_PROVIDER"
	HypershiftEnvVarImageAzureCapiProvider    = "IMAGE_AZURE_CAPI_PROVIDER"
	HypershiftEnvVarImageKubevertCapiProvider = "IMAGE_KUBEVIRT_CAPI_PROVIDER"
	HypershiftEnvVarImageKonnectivity         = "IMAGE_KONNECTIVITY"
	HypershiftEnvVarImageAwsEncyptionProvider = "IMAGE_AWS_ENCRYPTION_PROVIDER"
	HypershiftEnvVarImageClusterApi           = "IMAGE_CLUSTER_API"
	HypershiftEnvVarImageAgentCapiProvider    = "IMAGE_AGENT_CAPI_PROVIDER"

	// AddOnPlacementScore resource name
	HostedClusterScoresResourceName = "hosted-clusters-score"
	// AddOnPlacementScore score name
	HostedClusterScoresScoreName = "hostedClustersCount"

	// Default xaximum hosted cluster count on a hosting cluster
	DefaultMaxHostedClusterCount = 80
	// Default threshold hosted cluster count on a hosting cluster
	DefaultThresholdHostedClusterCount = 60

	// Full HC cluster claim metrics label
	MetricsLabelFullClusterClaim = "full-hc"
	// Zero HC cluster claim metrics label
	MetricsLabelZeroClusterClaim = "zero-hc"
	// Threshold HC cluster claim metrics label
	MetricsLabelThresholdClusterClaim = "threshold-hc"
)

// GenerateClientConfigFromSecret generate a client config from a given secret
func GenerateClientConfigFromSecret(secret *corev1.Secret) (*rest.Config, error) {
	var err error
	var config *clientcmdapi.Config

	if kubeconfig, ok := secret.Data["kubeconfig"]; ok {
		config, err = clientcmd.Load(kubeconfig)
		if err != nil {
			return nil, err
		}
	}

	if config == nil {
		return nil, fmt.Errorf("kubeconfig or token and server are missing")
	}

	return clientcmd.NewDefaultClientConfig(*config, &clientcmd.ConfigOverrides{}).ClientConfig()
}
