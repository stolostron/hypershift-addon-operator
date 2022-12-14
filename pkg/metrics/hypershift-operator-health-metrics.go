package metrics

import "github.com/prometheus/client_golang/prometheus"

var IsHypershiftOperatorDegraded = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_hypershift_operator_degraded_bool",
	Help: "Hypershift operator degraded true (1) or false (0)",
})

var IsExtDNSOperatorDegraded = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_ext_dns_operator_degraded_bool",
	Help: "External DNS operator degraded true (1) or false (0)",
})

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration, IsHypershiftOperatorDegraded, IsExtDNSOperatorDegraded)
}
