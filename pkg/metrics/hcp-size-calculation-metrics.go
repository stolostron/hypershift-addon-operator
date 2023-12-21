package metrics

import "github.com/prometheus/client_golang/prometheus"

var CapacityOfRequestBasedHCPs = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_request_based_hcp_capacity_gauge",
	Help: "Cluster's capacity to host hosted control planes based on HCP resource request",
})

var CapacityOfLowQPSHCPs = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_low_qps_based_hcp_capacity_gauge",
	Help: "Cluster's capacity to host hosted control planes based on low Kube API QPS",
})

var CapacityOfMediumQPSHCPs = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_medium_qps_based_hcp_capacity_gauge",
	Help: "Cluster's capacity to host hosted control planes based on medium Kube API QPS",
})

var CapacityOfHighQPSHCPs = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_high_qps_based_hcp_capacity_gauge",
	Help: "Cluster's capacity to host hosted control planes based on high Kube API QPS",
})

var CapacityOfAverageQPSHCPs = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_average_qps_based_hcp_capacity_gauge",
	Help: "Cluster's capacity to host hosted control planes based on the current average Kube API QPS by running HCPs",
})

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration,
		CapacityOfRequestBasedHCPs,
		CapacityOfLowQPSHCPs,
		CapacityOfMediumQPSHCPs,
		CapacityOfHighQPSHCPs,
		CapacityOfAverageQPSHCPs)
}
