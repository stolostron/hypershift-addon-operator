package agent

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stolostron/hypershift-addon-operator/pkg/metrics"
	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

const (
	// Success reasons
	degradedReasonHypershiftDeployed        = "HypershiftDeployed"
	degradedReasonHypershiftDeployedMessage = "Hypershift is deployed on managed cluster."

	// Operator failure reasons
	degradedReasonOperatorNotFound                = "OperatorNotFound"
	degradedReasonOperatorDeleted                 = "OperatorDeleted"
	degradedReasonOperatorNotAllAvailableReplicas = "OperatorNotAllAvailableReplicas"

	// External DNS failure reasons
	degradedReasonExternalDNSNotFound                = "ExternalDNSNotFound"
	degradedReasonExternalDNSDeleted                 = "ExternalDNSDeleted"
	degradedReasonExternalDNSNotAllAvailableReplicas = "ExternalDNSNotAllAvailableReplicas"

	// Metrics values
	metricsHealthy    = 0
	metricsDegraded   = 1
	metricsNotPresent = -1
)

var (
	degradedReasonOperatorNotFoundMessage                   = fmt.Sprintf("The %s deployment does not exist", util.HypershiftOperatorName)
	degradedReasonOperatorDeletedMessage                    = fmt.Sprintf("The %s deployment is being deleted", util.HypershiftOperatorName)
	degradedReasonOperatorNotAllAvailableReplicasMessage    = fmt.Sprintf("There are no %s replica available", util.HypershiftOperatorName)
	degradedReasonExternalDNSNotFoundMessage                = fmt.Sprintf("The %s deployment does not exist", util.HypershiftOperatorExternalDNSName)
	degradedReasonExternalDNSDeletedMessage                 = fmt.Sprintf("The %s deployment is being deleted", util.HypershiftOperatorExternalDNSName)
	degradedReasonExternalDNSNotAllAvailableReplicasMessage = fmt.Sprintf("There are no %s replica available", util.HypershiftOperatorExternalDNSName)
)

// containsHypershiftAddonDeployment checks if the given deployment is a hypershift addon deployment.
// It validates that the deployment has the correct namespace and name.
func containsHypershiftAddonDeployment(deployment appsv1.Deployment) bool {
	if deployment.Name == "" || deployment.Namespace == "" {
		return false
	}

	if deployment.Namespace != util.HypershiftOperatorNamespace {
		return false
	}

	return deployment.Name == util.HypershiftOperatorName ||
		deployment.Name == util.HypershiftOperatorExternalDNSName
}

// DeploymentStatus represents the health status of a deployment
type DeploymentStatus struct {
	Reason  string
	Message string
	Healthy bool
}

// checkDeployments evaluates the health of hypershift operator and external DNS deployments
// and returns the appropriate condition for the managed cluster addon.
func checkDeployments(checkExtDNSDeploy bool,
	operatorDeployment, externalDNSDeployment *appsv1.Deployment) metav1.Condition {

	// Check operator deployment status
	operatorStatus := checkSingleDeployment(operatorDeployment, "operator")
	updateMetric(metrics.IsHypershiftOperatorDegraded, operatorStatus.Healthy)

	// Check external DNS deployment status if required
	var extDNSStatus DeploymentStatus
	if checkExtDNSDeploy {
		extDNSStatus = checkSingleDeployment(externalDNSDeployment, "external-dns")
		updateMetric(metrics.IsExtDNSOperatorDegraded, extDNSStatus.Healthy)
	} else {
		metrics.IsExtDNSOperatorDegraded.Set(metricsNotPresent)
		extDNSStatus.Healthy = true // Don't consider it unhealthy if not checking
	}

	// Build the final condition
	return buildCondition(operatorStatus, extDNSStatus)
}

// checkSingleDeployment evaluates the health of a single deployment
func checkSingleDeployment(deployment *appsv1.Deployment, deploymentType string) DeploymentStatus {
	if deployment == nil {
		return getDeploymentStatus(deploymentType, "not-found")
	}

	if !deployment.GetDeletionTimestamp().IsZero() {
		return getDeploymentStatus(deploymentType, "deleted")
	}

	if !isDeploymentReady(deployment) {
		return getDeploymentStatus(deploymentType, "not-ready")
	}

	return DeploymentStatus{Healthy: true}
}

// isDeploymentReady checks if a deployment has all replicas available
func isDeploymentReady(deployment *appsv1.Deployment) bool {
	if deployment.Status.AvailableReplicas == 0 {
		return false
	}

	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas != deployment.Status.AvailableReplicas {
		return false
	}

	return true
}

// getDeploymentStatus returns the appropriate status for a deployment based on type and issue
func getDeploymentStatus(deploymentType, issue string) DeploymentStatus {
	switch deploymentType {
	case "operator":
		switch issue {
		case "not-found":
			return DeploymentStatus{
				Reason:  degradedReasonOperatorNotFound,
				Message: degradedReasonOperatorNotFoundMessage,
				Healthy: false,
			}
		case "deleted":
			return DeploymentStatus{
				Reason:  degradedReasonOperatorDeleted,
				Message: degradedReasonOperatorDeletedMessage,
				Healthy: false,
			}
		case "not-ready":
			return DeploymentStatus{
				Reason:  degradedReasonOperatorNotAllAvailableReplicas,
				Message: degradedReasonOperatorNotAllAvailableReplicasMessage,
				Healthy: false,
			}
		}
	case "external-dns":
		switch issue {
		case "not-found":
			return DeploymentStatus{
				Reason:  degradedReasonExternalDNSNotFound,
				Message: degradedReasonExternalDNSNotFoundMessage,
				Healthy: false,
			}
		case "deleted":
			return DeploymentStatus{
				Reason:  degradedReasonExternalDNSDeleted,
				Message: degradedReasonExternalDNSDeletedMessage,
				Healthy: false,
			}
		case "not-ready":
			return DeploymentStatus{
				Reason:  degradedReasonExternalDNSNotAllAvailableReplicas,
				Message: degradedReasonExternalDNSNotAllAvailableReplicasMessage,
				Healthy: false,
			}
		}
	}

	// Should never reach here, but return a safe default
	return DeploymentStatus{Healthy: true}
}

// updateMetric sets the appropriate metric value based on health status
func updateMetric(metric prometheus.Gauge, healthy bool) {
	if healthy {
		metric.Set(metricsHealthy)
	} else {
		metric.Set(metricsDegraded)
	}
}

// buildCondition creates the final condition based on operator and external DNS status
func buildCondition(operatorStatus, extDNSStatus DeploymentStatus) metav1.Condition {
	// If both are healthy, return success condition
	if operatorStatus.Healthy && extDNSStatus.Healthy {
		return metav1.Condition{
			Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
			Status:  metav1.ConditionFalse,
			Reason:  degradedReasonHypershiftDeployed,
			Message: degradedReasonHypershiftDeployedMessage,
		}
	}

	// Build degraded condition with combined reasons and messages
	reasons := make([]string, 0, 2)
	messages := make([]string, 0, 2)

	if !operatorStatus.Healthy {
		reasons = append(reasons, operatorStatus.Reason)
		messages = append(messages, operatorStatus.Message)
	}

	if !extDNSStatus.Healthy {
		reasons = append(reasons, extDNSStatus.Reason)
		messages = append(messages, extDNSStatus.Message)
	}

	return metav1.Condition{
		Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
		Status:  metav1.ConditionTrue,
		Reason:  strings.Join(reasons, ","),
		Message: strings.Join(messages, "\n"),
	}
}
