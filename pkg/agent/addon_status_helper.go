package agent

import (
	"fmt"

	"github.com/stolostron/hypershift-addon-operator/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
)

const (
	degradedReasonHypershiftDeployed                 = "HypershiftDeployed"
	degradedReasonHypershiftDeployedMessage          = "Hypershift is deployed on managed cluster."
	degradedReasonOperatorNotFound                   = "OperatorNotFound"
	degradedReasonOperatorDeleted                    = "OperatorDeleted"
	degradedReasonOperatorNotAllAvailableReplicas    = "OperatorNotAllAvailableReplicas"
	degradedReasonExternalDNSNotFound                = "ExternalDNSNotFound"
	degradedReasonExternalDNSDeleted                 = "ExternalDNSDeleted"
	degradedReasonExternalDNSNotAllAvailableReplicas = "ExternalDNSNotAllAvailableReplicas"
)

var (
	degradedReasonOperatorNotFoundMessage                   = fmt.Sprintf("The %s deployment does not exist", util.HypershiftOperatorName)
	degradedReasonOperatorDeletedMessage                    = fmt.Sprintf("The %s deployment is being deleted", util.HypershiftOperatorName)
	degradedReasonOperatorNotAllAvailableReplicasMessage    = fmt.Sprintf("There are no %s replica available", util.HypershiftOperatorName)
	degradedReasonExternalDNSNotFoundMessage                = fmt.Sprintf("The %s deployment does not exist", util.HypershiftOperatorExternalDNSName)
	degradedReasonExternalDNSDeletedMessage                 = fmt.Sprintf("The %s deployment is being deleted", util.HypershiftOperatorExternalDNSName)
	degradedReasonExternalDNSNotAllAvailableReplicasMessage = fmt.Sprintf("There are no %s replica available", util.HypershiftOperatorExternalDNSName)
)

func containsHypershiftAddonDeployment(deployment appsv1.Deployment) bool {
	if len(deployment.Name) == 0 || len(deployment.Namespace) == 0 {
		return false
	}

	if deployment.Namespace != util.HypershiftOperatorNamespace {
		return false
	}

	return deployment.Name == util.HypershiftOperatorName ||
		deployment.Name == util.HypershiftOperatorExternalDNSName
}

func checkDeployments(checkExtDNSDeploy bool,
	operatorDeployment, externalDNSDeployment *appsv1.Deployment) metav1.Condition {
	reason := ""
	message := ""

	if operatorDeployment == nil {
		reason = degradedReasonOperatorNotFound
		message = degradedReasonOperatorNotFoundMessage
	} else if !operatorDeployment.GetDeletionTimestamp().IsZero() {
		reason = degradedReasonOperatorDeleted
		message = degradedReasonOperatorDeletedMessage
	} else if operatorDeployment.Status.AvailableReplicas == 0 ||
		(operatorDeployment.Spec.Replicas != nil && *operatorDeployment.Spec.Replicas != operatorDeployment.Status.AvailableReplicas) {
		reason = degradedReasonOperatorNotAllAvailableReplicas
		message = degradedReasonOperatorNotAllAvailableReplicasMessage
	}

	if checkExtDNSDeploy {
		isReasonPopulated := len(reason) > 0
		if externalDNSDeployment == nil {
			if isReasonPopulated {
				reason += ","
				message += "\n"
			}
			reason += degradedReasonExternalDNSNotFound
			message += degradedReasonExternalDNSNotFoundMessage
		} else if !externalDNSDeployment.GetDeletionTimestamp().IsZero() {
			if isReasonPopulated {
				reason += ","
				message += "\n"
			}
			reason += degradedReasonExternalDNSDeleted
			message += degradedReasonExternalDNSDeletedMessage
		} else if externalDNSDeployment.Status.AvailableReplicas == 0 ||
			(externalDNSDeployment.Spec.Replicas != nil && *externalDNSDeployment.Spec.Replicas != externalDNSDeployment.Status.AvailableReplicas) {
			if isReasonPopulated {
				reason += ","
				message += "\n"
			}
			reason += degradedReasonExternalDNSNotAllAvailableReplicas
			message += degradedReasonExternalDNSNotAllAvailableReplicasMessage
		}
	}

	if len(reason) != 0 {
		return metav1.Condition{
			Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: message,
		}
	}

	return metav1.Condition{
		Type:    addonv1alpha1.ManagedClusterAddOnConditionDegraded,
		Status:  metav1.ConditionFalse,
		Reason:  degradedReasonHypershiftDeployed,
		Message: degradedReasonHypershiftDeployedMessage,
	}
}
