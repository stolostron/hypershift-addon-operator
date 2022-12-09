package metrics

import "github.com/prometheus/client_golang/prometheus"

var InInstallationOrUpgradeBool = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "hypershift_install_in_progress_bool",
	Help: "Hypershift operator installation in progress true (1) or false (0)",
})

var InstallationOrUpgradeFailedCount = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "hypershift_install_failure_gauge",
	Help: "Hypershift operator installation failure gauge",
})

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration, InInstallationOrUpgradeBool, InstallationOrUpgradeFailedCount)
}
