package metrics

import "github.com/prometheus/client_golang/prometheus"

var TotalHostedClusterGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_total_hosted_control_planes_gauge",
	Help: "Total number of hosted contol planes",
})

var HostedControlPlaneAvailableGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_available_hosted_control_planes_gauge",
	Help: "Number of available hosted contol planes",
})

var HostedClusterAvailableGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_available_hosted_clusters_gauge",
	Help: "Number of available hosted clusters",
})

var MaxNumHostedClustersGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_max_hosted_clusters_gauge",
	Help: "Maximum number of hosted clusters",
})

var ThresholdNumHostedClustersGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_threshold_hosted_clusters_gauge",
	Help: "Threshold number of hosted clusters",
})

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration,
		TotalHostedClusterGauge,
		HostedControlPlaneAvailableGauge,
		HostedClusterAvailableGauge,
		MaxNumHostedClustersGauge,
		ThresholdNumHostedClustersGauge)
}
