/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/errors"
)

const (
	AgentReservedCondition        clusterv1.ConditionType = "AgentReserved"
	AgentSpecSyncedCondition      clusterv1.ConditionType = "AgentSpecSynced"
	AgentValidatedCondition       clusterv1.ConditionType = "AgentValidated"
	AgentRequirementsMetCondition clusterv1.ConditionType = "AgentRequirementsMet"
	InstalledCondition            clusterv1.ConditionType = "Installed"

	AgentNotYetFoundReason = "AgentNotYetFound"
	NoSuitableAgentsReason = "NoSuitableAgents"
)

// AgentMachineSpec defines the desired state of AgentMachine
type AgentMachineSpec struct {
	// AgentLabelSelector contains the labels that must be set on an Agent in order to be selected for this Machine.
	// +optional
	AgentLabelSelector *metav1.LabelSelector `json:"agentLabelSelector,omitempty"`

	// ProviderID is the host's motherboard serial formatted as
	// agent://12345678-1234-1234-1234-123456789abc
	ProviderID *string `json:"providerID,omitempty"`
}

// AgentMachineStatus defines the observed state of AgentMachine
type AgentMachineStatus struct {
	// Ready is true when the provider resource is ready.
	// +optional
	Ready bool `json:"ready"`

	// AgentRef is a reference to the Agent matched to the Machine.
	AgentRef *AgentReference `json:"agentRef,omitempty"`

	// Addresses contains the Agent's associated addresses.
	Addresses []clusterv1.MachineAddress `json:"addresses,omitempty"`

	// FailureReason will be set in the event that there is a terminal problem
	// reconciling the Machine and will contain a succinct value suitable
	// for machine interpretation.
	//
	// This field should not be set for transitive errors that a controller
	// faces that are expected to be fixed automatically over
	// time (like service outages), but instead indicate that something is
	// fundamentally wrong with the Machine's spec or the configuration of
	// the controller, and that manual intervention is required. Examples
	// of terminal errors would be invalid combinations of settings in the
	// spec, values that are unsupported by the controller, or the
	// responsible controller itself being critically misconfigured.
	//
	// Any transient errors that occur during the reconciliation of Machines
	// can be added as events to the Machine object and/or logged in the
	// controller's output.
	// +optional
	FailureReason *errors.MachineStatusError `json:"failureReason,omitempty"`

	// FailureMessage will be set in the event that there is a terminal problem
	// reconciling the Machine and will contain a more verbose string suitable
	// for logging and human consumption.
	//
	// This field should not be set for transitive errors that a controller
	// faces that are expected to be fixed automatically over
	// time (like service outages), but instead indicate that something is
	// fundamentally wrong with the Machine's spec or the configuration of
	// the controller, and that manual intervention is required. Examples
	// of terminal errors would be invalid combinations of settings in the
	// spec, values that are unsupported by the controller, or the
	// responsible controller itself being critically misconfigured.
	//
	// Any transient errors that occur during the reconciliation of Machines
	// can be added as events to the Machine object and/or logged in the
	// controller's output.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// Conditions defines current service state of the AgentMachine.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:deprecatedversion:warning="v1alpha1 is a deprecated version for AgentMachine"

// AgentMachine is the Schema for the agentmachines API
type AgentMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentMachineSpec   `json:"spec,omitempty"`
	Status AgentMachineStatus `json:"status,omitempty"`
}

// GetConditions returns the observations of the operational state of the AWSMachine resource.
func (r *AgentMachine) GetConditions() clusterv1.Conditions {
	return r.Status.Conditions
}

// SetConditions sets the underlying service state of the AWSMachine to the predescribed clusterv1.Conditions.
func (r *AgentMachine) SetConditions(conditions clusterv1.Conditions) {
	r.Status.Conditions = conditions
}

//+kubebuilder:object:root=true

// AgentMachineList contains a list of AgentMachine
type AgentMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentMachine{}, &AgentMachineList{})
}
