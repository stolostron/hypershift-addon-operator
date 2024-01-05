package agent

import (
	"context"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stolostron/hypershift-addon-operator/pkg/metrics"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	clustercsfake "open-cluster-management.io/api/client/cluster/clientset/versioned/fake"
)

func TestCalculateCapacitiesToHostHCPs(t *testing.T) {
	//ctx := context.Background()
	client := initClient()
	zapLog, _ := zap.NewDevelopment()

	fakeClusterCS := clustercsfake.NewSimpleClientset()

	aCtrl := &agentController{
		spokeClustersClient: fakeClusterCS,
		spokeUncachedClient: client,
		spokeClient:         client,
		hubClient:           client,
		log:                 zapr.NewLogger(zapLog),
	}

	aCtrl.SetHCPSizingBaseline(context.TODO())

	err := aCtrl.calculateCapacitiesToHostHCPs()
	assert.Nil(t, err, "err nil when calculateCapacitiesToHostHCPs was successful")

	// There is no worker node so all capacity values are expected to be 0.
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfAverageQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfHighQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfHighQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfLowQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfMediumQPSHCPs))
	assert.Equal(t, float64(0), testutil.ToFloat64(metrics.CapacityOfRequestBasedHCPs))
}
