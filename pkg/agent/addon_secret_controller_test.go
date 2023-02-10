package agent

import (
	"context"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	ctrl "sigs.k8s.io/controller-runtime"

	"k8s.io/apimachinery/pkg/types"
)

func TestSecretReconcile(t *testing.T) {
	ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	aCtrl := &AddonSecretController{
		spokeClient: client,
		log:         zapr.NewLogger(zapLog),
	}

	// Create hosted cluster
	hcNN := types.NamespacedName{Name: "hc1", Namespace: "clusters"}
	hc := getHostedCluster(hcNN)
	err := aCtrl.spokeClient.Create(ctx, hc)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	secretNN := types.NamespacedName{Name: util.ExternalManagedKubeconfigSecretName, Namespace: util.ExternalManagedKubeconfigSecretNsPrefix + "hc1"}

	// Reconcile with annotation
	_, err = aCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: secretNN})
	assert.Nil(t, err, "err nil when reconcile was successfully")

	err = aCtrl.spokeClient.Get(ctx, hcNN, hc)
	assert.Nil(t, err, "is nil when the hosted cluster is found")
	assert.NotEmpty(t, hc.Annotations[util.HostedClusterRefreshAnnoKey])

	// Create 2nd hosted cluster with managedcluster-name annotation
	hc2NN := types.NamespacedName{Name: "hc2", Namespace: "clusters"}
	hc2 := getHostedCluster(hc2NN)
	annotations := make(map[string]string)
	annotations[util.ManagedClusterAnnoKey] = "hc1"
	hc2.Annotations = annotations
	err = aCtrl.spokeClient.Create(ctx, hc2)
	assert.Nil(t, err, "err nil when hosted cluster is created successfully")

	// Reconcile with annotation
	_, err = aCtrl.Reconcile(ctx, ctrl.Request{NamespacedName: secretNN})
	assert.Nil(t, err, "err nil when reconcile was successfully")

	// managedcluster-name annotation takes precedence, hc2 is updated by the controller this time
	err = aCtrl.spokeClient.Get(ctx, hc2NN, hc2)
	assert.Nil(t, err, "is nil when the hosted cluster is found")
	assert.NotEmpty(t, hc2.Annotations[util.HostedClusterRefreshAnnoKey])
}
