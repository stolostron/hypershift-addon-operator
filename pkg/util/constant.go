package util

const (
	DefaultHypershiftAddonImage = "quay.io/stolostron/hypershift-addon-operator:latest"

	DefaultHypershiftOperatorImage = "quay.io/hypershift/hypershift-operator:latest"

	// AgentInstallationNamespace is the namespace on the managed cluster to install the addon agent.
	AgentInstallationNamespace = "open-cluster-management-agent-addon"

	//HypershiftBucketNamespaceOnHub which should be the same as the addon manager's namespace
	HypershiftBucketNamespaceOnHub = "open-cluster-management"

	AddonControllerName = "hypershift-addon"
)
