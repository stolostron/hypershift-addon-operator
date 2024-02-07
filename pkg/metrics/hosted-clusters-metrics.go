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

var HostedClusterBeingDeletedGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_deleted_hosted_clusters_gauge",
	Help: "Number of hosted clusters being deleted",
})

var MaxNumHostedClustersGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_max_hosted_clusters_gauge",
	Help: "Maximum number of hosted clusters",
})

var ThresholdNumHostedClustersGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_threshold_hosted_clusters_gauge",
	Help: "Threshold number of hosted clusters",
})

var HostedControlPlaneStatusGaugeVec = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "mce_hs_addon_hosted_control_planes_status_gauge",
		Help: "Number of hosted contol planes with status",
	},
	[]string{"hcp_namespace", "hcp_name", "ready", "version"},
)

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration,
		TotalHostedClusterGauge,
		HostedControlPlaneAvailableGauge,
		HostedClusterAvailableGauge,
		HostedClusterBeingDeletedGauge,
		MaxNumHostedClustersGauge,
		ThresholdNumHostedClustersGauge,
		HostedControlPlaneStatusGaugeVec)
}
