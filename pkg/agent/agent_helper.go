package agent

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	localClusterLabelName  = "local-cluster"
	localClusterLabelValue = "true"
)

// spokeHasManagedClusterAPI reports whether the spoke apiserver exposes OCM ManagedCluster
// (e.g. MCE hosting). ROSA HCP management clusters register fleet inventory on the service
// cluster only and omit this API on the MC.
//
// Returns (true, nil) when the API is registered. Returns (false, nil) on NoMatch.
// Other RESTMapping errors are returned so transient discovery failures fail agent startup
// instead of skipping label sync.
func spokeHasManagedClusterAPI(mapper meta.RESTMapper) (bool, error) {
	gvk := clusterv1.SchemeGroupVersion.WithKind("ManagedCluster")
	_, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err == nil {
		return true, nil
	}
	if meta.IsNoMatchError(err) {
		return false, nil
	}
	return false, fmt.Errorf("discover ManagedCluster API on spoke: %w", err)
}

// labelAgentSetup registers the label-agent controller on the spoke manager.
type labelAgentSetup interface {
	SetupWithManager(mgr ctrl.Manager) error
}

// setupSpokeLabelAgent registers label sync when the spoke exposes ManagedCluster.
// Returns labelAgentSkipped=true when the API is absent (e.g. ROSA HCP MC).
func setupSpokeLabelAgent(
	mapper meta.RESTMapper, setup labelAgentSetup, mgr ctrl.Manager,
) (labelAgentSkipped bool, err error) {
	hasAPI, err := spokeHasManagedClusterAPI(mapper)
	if err != nil {
		return false, fmt.Errorf("unable to detect spoke ManagedCluster API: %w", err)
	}
	if !hasAPI {
		return true, nil
	}
	if err := setup.SetupWithManager(mgr); err != nil {
		return false, fmt.Errorf("unable to create label agent controller: %w", err)
	}
	return false, nil
}

// gets the self-managed cluster with label local-cluster=true
func getSelfManagedClusterName(ctx context.Context, spokeClient client.Client, log logr.Logger) string {
	localClusterSelector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{
			localClusterLabelName: localClusterLabelValue,
		},
	})
	if err != nil {
		log.Error(err, err.Error())
		return ""
	}

	listopts := &client.ListOptions{}
	listopts.LabelSelector = localClusterSelector
	localClusterList := &clusterv1.ManagedClusterList{}
	err = spokeClient.List(ctx, localClusterList, listopts)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to list managed clusters with label: %s=%s", localClusterLabelName, localClusterLabelValue))
		return ""
	}

	if len(localClusterList.Items) == 0 {
		log.Error(err, "no local cluster found")
		return ""
	}

	if len(localClusterList.Items) > 1 {
		log.Info("There are more than one local clusters. Using the first one in the list.")
	}

	log.Info(fmt.Sprintf("local cluster name is %s", localClusterList.Items[0].Name))

	return localClusterList.Items[0].Name
}
