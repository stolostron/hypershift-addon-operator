package agent

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	localClusterLabelName  = "local-cluster"
	localClusterLabelValue = "true"
)

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
