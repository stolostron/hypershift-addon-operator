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

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration, TotalHostedClusterGauge, HostedClusterAvailableGauge)
}
