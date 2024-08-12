package agent

import (
	"context"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

func Test_getSelfManagedClusterName(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)

	localClusterName := getSelfManagedClusterName(ctx, client, logger)
	assert.Equal(t, "", localClusterName)

	managedCluster := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: "mc1",
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err := client.Create(ctx, managedCluster)
	assert.Nil(t, err, "err nil when managedcluster is created successfully")

	localClusterName = getSelfManagedClusterName(ctx, client, logger)
	assert.Equal(t, "", localClusterName)

	localCluster := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:   "mc2",
			Labels: map[string]string{"local-cluster": "true"},
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err = client.Create(ctx, localCluster)
	assert.Nil(t, err, "err nil when local cluster managedcluster is created successfully")

	localClusterName = getSelfManagedClusterName(ctx, client, logger)
	assert.Equal(t, "mc2", localClusterName)

	// If there are more than one local clusters??
	// Return the first local cluster
	localCluster2 := &clusterv1.ManagedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:   "mc3",
			Labels: map[string]string{"local-cluster": "true"},
		},
		Spec: clusterv1.ManagedClusterSpec{
			HubAcceptsClient:     false,
			LeaseDurationSeconds: 0,
		},
	}
	err = client.Create(ctx, localCluster2)
	assert.Nil(t, err, "err nil when local cluster managedcluster is created successfully")

	localClusterName = getSelfManagedClusterName(ctx, client, logger)
	assert.Equal(t, "mc2", localClusterName)

}
