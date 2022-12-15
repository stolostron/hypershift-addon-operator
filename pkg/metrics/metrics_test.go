package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	"github.com/stretchr/testify/assert"
)

func TestMetrics(t *testing.T) {
	assert := assert.New(t)

	// Check all metrics are registered
	AddonAgentFailedToStartBool.Set(0)
	assert.Equal(float64(0), testutil.ToFloat64(AddonAgentFailedToStartBool))

	InInstallationOrUpgradeBool.Set(0)
	assert.Equal(float64(0), testutil.ToFloat64(InInstallationOrUpgradeBool))

	InstallationOrUpgradeFailedCount.Set(1)
	assert.Equal(float64(1), testutil.ToFloat64(InstallationOrUpgradeFailedCount))

	PlacementScoreFailureCount.Inc()
	assert.Equal(float64(1), testutil.ToFloat64(PlacementScoreFailureCount))

	PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelFullClusterClaim).Inc()
	assert.Equal(float64(1), testutil.ToFloat64(PlacementClusterClaimsFailureCount.WithLabelValues(util.MetricsLabelFullClusterClaim)))

	KubeconfigSecretCopyFailureCount.Inc()
	assert.Equal(float64(1), testutil.ToFloat64(KubeconfigSecretCopyFailureCount))

	HubResourceSyncFailureCount.WithLabelValues("secret").Inc()
	assert.Equal(float64(1), testutil.ToFloat64(HubResourceSyncFailureCount.WithLabelValues("secret")))

	TotalHostedClusterGauge.Set(0)
	assert.Equal(float64(0), testutil.ToFloat64(TotalHostedClusterGauge))

	HostedControlPlaneAvailableGauge.Set(5)
	assert.Equal(float64(5), testutil.ToFloat64(HostedControlPlaneAvailableGauge))

	HostedClusterAvailableGauge.Set(3)
	assert.Equal(float64(3), testutil.ToFloat64(HostedClusterAvailableGauge))

	IsHypershiftOperatorDegraded.Set(1)
	assert.Equal(float64(1), testutil.ToFloat64(IsHypershiftOperatorDegraded))

	IsExtDNSOperatorDegraded.Set(0)
	assert.Equal(float64(0), testutil.ToFloat64(IsExtDNSOperatorDegraded))

	IsAWSS3BucketSecretConfigured.Set(1)
	assert.Equal(float64(1), testutil.ToFloat64(IsAWSS3BucketSecretConfigured))

}
