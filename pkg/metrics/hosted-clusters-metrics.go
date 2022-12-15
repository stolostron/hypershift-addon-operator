package metrics

import "github.com/prometheus/client_golang/prometheus"

var TotalHostedClusterGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_total_hcp_gauge",
	Help: "Total number of hosted contol planes",
})

var HostedClusterAvailableGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_available_hcp_gauge",
	Help: "Number of available hosted contol planes",
})

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration, TotalHostedClusterGauge, HostedClusterAvailableGauge)
}
