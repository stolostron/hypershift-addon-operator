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

var CapacityOfQPSBasedHCPs = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "mce_hs_addon_qps_based_hcp_capacity_gauge",
		Help: "Cluster's capacity to host hosted control planes based on API server QPS",
	},
	[]string{"qps_rate"},
)

var WorkerNodeResourceCapacities = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "mce_hs_addon_worker_node_resource_capacities_gauge",
		Help: "Worker node resource capacities",
	},
	[]string{"node", "cpu", "memory", "maxPods"},
)

var QPSValues = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "mce_hs_addon_qps_gauge",
		Help: "API server request rates (QPS) used for HCP sizing calculations",
	},
	[]string{"rate"},
)

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration,
		CapacityOfRequestBasedHCPs,
		CapacityOfLowQPSHCPs,
		CapacityOfMediumQPSHCPs,
		CapacityOfHighQPSHCPs,
		CapacityOfAverageQPSHCPs,
		CapacityOfQPSBasedHCPs,
		WorkerNodeResourceCapacities,
		QPSValues)
}
