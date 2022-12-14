package metrics

import "github.com/prometheus/client_golang/prometheus"

var TotalHostedClusterCount = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_total_hcp_count",
	Help: "Total number of hosted contol planes count",
})

var HostedClusterReadyCount = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_ready_hcp_count",
	Help: "Number of ready hosted contol planes count",
})

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration, TotalHostedClusterCount, HostedClusterReadyCount)
}
