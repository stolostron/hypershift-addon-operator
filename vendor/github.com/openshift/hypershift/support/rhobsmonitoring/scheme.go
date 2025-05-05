package rhobsmonitoring

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// SchemeGroupVersion is the group version used to register these objects
var SchemeGroupVersion = schema.GroupVersion{Group: "monitoring.rhobs", Version: "v1"}

// EnvironmentVariable indicates whether RHOBS monitoring resources should be used
var EnvironmentVariable = "RHOBS_MONITORING"

var (
	// localSchemeBuilder and AddToScheme will stay in k8s.io/kubernetes.
	SchemeBuilder      runtime.SchemeBuilder
	localSchemeBuilder = &SchemeBuilder
	AddToScheme        = localSchemeBuilder.AddToScheme
)

func init() {
	// We only register manually written functions here. The registration of the
	// generated functions takes place in the generated files. The separation
	// makes the code compile even when the generated files are missing.
	localSchemeBuilder.Register(addKnownTypes)
}

// Adds the list of known types to api.Scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&prometheusoperatorv1.Prometheus{},
		&prometheusoperatorv1.PrometheusList{},
		&prometheusoperatorv1.ServiceMonitor{},
		&prometheusoperatorv1.ServiceMonitorList{},
		&prometheusoperatorv1.PodMonitor{},
		&prometheusoperatorv1.PodMonitorList{},
		&prometheusoperatorv1.Probe{},
		&prometheusoperatorv1.ProbeList{},
		&prometheusoperatorv1.Alertmanager{},
		&prometheusoperatorv1.AlertmanagerList{},
		&prometheusoperatorv1.PrometheusRule{},
		&prometheusoperatorv1.PrometheusRuleList{},
		&prometheusoperatorv1.ThanosRuler{},
		&prometheusoperatorv1.ThanosRulerList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
