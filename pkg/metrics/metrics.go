package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var CollectorsForRegistration []prometheus.Collector

func init() {
	metrics.Registry.MustRegister(CollectorsForRegistration...)
}
