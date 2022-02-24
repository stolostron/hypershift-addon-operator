package util

const (
	DefaultHypershiftImage = "quay.io/ianzhang366/hypershift-addon-operator:latest"
	// AgentInstallationNamespace is the namespace on the managed cluster to install the addon agent.
	AgentInstallationNamespace = "open-cluster-management-agent-addon"

	//HypershiftBucketNamespaceOnHub which should be the same as the addon manager's namespace
	HypershiftBucketNamespaceOnHub = "open-cluster-management"

	AddonControllerName = "hypershift-addon"
)
