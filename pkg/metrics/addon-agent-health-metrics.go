package metrics

import "github.com/prometheus/client_golang/prometheus"

var InInstallationOrUpgradeBool = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_install_in_progress_bool",
	Help: "Hypershift operator installation in progress true (1) or false (0)",
})

var InstallationOrUpgradeFailedCount = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_install_failure_gauge",
	Help: "Hypershift operator installation failure gauge",
})

var PlacementScoreFailureCount = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_placement_score_failure_gauge",
	Help: "Hypershift addon agent placement score sync failure gauge",
})

var PlacementFullClusterClaimsFailureCount = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "mce_hs_addon_placement_full_cluster_claims_failure_count",
	Help: "Hypershift addon agent placement cluster claims update failure gauge",
})

var PlacementThresholdClusterClaimsFailureCount = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "mce_hs_addon_placement_threshold_cluster_claims_failure_count",
	Help: "Hypershift addon agent placement cluster claims update failure gauge",
})

var PlacementZeroClusterClaimsFailureCount = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "mce_hs_addon_placement_zero_cluster_claims_failure_count",
	Help: "Hypershift addon agent placement cluster claims update failure gauge",
})

var KubeconfigSecretCopyFailureCount = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "mce_hs_addon_kubeconfig_secret_copy_failure_count",
	Help: "Hypershift addon agent placement cluster claims update failure gauge",
})

var HubSecretSyncFailureCount = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "mce_hs_addon_hub_secret_sync_failure_count",
	Help: "Hypershift addon agent hub secret sync failure gauge",
})

var HubImageConfigMapSyncFailureCount = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "mce_hs_addon_hub_image_configmap_sync_failure_count",
	Help: "Hypershift addon agent hub image configmap failure gauge",
})

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration, InInstallationOrUpgradeBool, InstallationOrUpgradeFailedCount)
}
