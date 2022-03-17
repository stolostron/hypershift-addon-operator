package util

const (
	DefaultHypershiftAddonImage = "quay.io/stolostron/hypershift-addon-operator:latest"

	DefaultHypershiftOperatorImage = "quay.io/hypershift/hypershift-operator:latest"

	// AgentInstallationNamespace is the namespace on the managed cluster to install the addon agent.
	AgentInstallationNamespace = "open-cluster-management-agent-addon"

	MulticlusterHubPullSecret = "open-cluster-management-image-pull-credentials"

	HypershiftDownstreamOverride = "hypershift-operator-imagestream"
	HypershiftOverrideKey        = "imagestream"
	AddonControllerName          = "hypershift-addon"
)
