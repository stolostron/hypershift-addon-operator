package util

const (
	DefaultHypershiftImage = "quay.io/ianzhang366/hypershift-addon-operator:latest"
	// AgentInstallationNamespace is the namespace on the managed cluster to install the addon agent.
	AgentInstallationNamespace = "default"

	//HypershiftBucketNamespaceOnHub by default it some be the managedcluster's namespace.
	//If other namespace specified, then you need to give the addon secret RBAC for the namespace
	HypershiftBucketNamespaceOnHub = "local-cluster"

	AddonControllerName = "hypershift-addon"
)
