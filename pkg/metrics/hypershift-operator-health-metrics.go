package metrics

import "github.com/prometheus/client_golang/prometheus"

var IsHypershiftOperatorDegraded = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_hypershift_operator_degraded_bool",
	Help: "Hypershift operator degraded true (1) or false (0)",
})

var IsExtDNSOperatorDegraded = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_ext_dns_operator_degraded_bool",
	Help: "External DNS operator degraded true (1) or false (0)",
})

var IsAWSS3BucketSecretConfigured = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "mce_hs_addon_aws_s3_bucket_secret_configured_bool",
	Help: "AWS S3 bucket secret configured true (1) or false (0)",
})

func init() {
	CollectorsForRegistration = append(CollectorsForRegistration, IsHypershiftOperatorDegraded, IsExtDNSOperatorDegraded)
}
