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

	// AgentInstallationNamespace is the namespace on the managed cluster to install the addon agent.
	AgentInstallationNamespace = "open-cluster-management-agent-addon"

	MulticlusterHubPullSecret = "open-cluster-management-image-pull-credentials"

	HypershiftDownstreamOverride = "hypershift-operator-imagestream"
	HypershiftOverrideKey        = "imagestream"
	AddonControllerName          = "hypershift-addon"

	HypershiftOperatorNamespace = "hypershift"
	HypershiftOperatorName      = "operator"
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
