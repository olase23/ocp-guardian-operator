/*
Copyright 2026.

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
)

// ClusterDegradationMonitorSpec defines the desired state of ClusterDegradationMonitor.
type ClusterDegradationMonitorSpec struct {
	// CheckIntervalMinutes controls how often the controller checks ClusterOperator status.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=2
	// +optional
	CheckIntervalMinutes int32 `json:"checkIntervalMinutes,omitempty"`

	// DegradedThreshold is the number of degraded ClusterOperators that triggers a Critical overall health.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	// +optional
	DegradedThreshold int32 `json:"degradedThreshold,omitempty"`

	// OperatorFilter limits which ClusterOperators to monitor. Empty means all.
	// +optional
	OperatorFilter []string `json:"operatorFilter,omitempty"`

	// Suspend stops cluster degradation monitoring when true.
	// +kubebuilder:default=false
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// ClusterOperatorHealth describes the health status of a single ClusterOperator.
type ClusterOperatorHealth struct {
	// Name of the ClusterOperator.
	Name string `json:"name"`
	// Available indicates whether the operator is available.
	Available bool `json:"available"`
	// Degraded indicates whether the operator is degraded.
	Degraded bool `json:"degraded"`
	// Progressing indicates whether the operator is progressing toward a new state.
	Progressing bool `json:"progressing"`
	// Message provides a human-readable status message.
	// +optional
	Message string `json:"message,omitempty"`
	// Version is the currently deployed version of this operator.
	// +optional
	Version string `json:"version,omitempty"`
	// LastTransitionTime is the last time any condition on this operator transitioned.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
}

// ClusterDegradationMonitorStatus defines the observed state of ClusterDegradationMonitor.
type ClusterDegradationMonitorStatus struct {
	// Conditions represent the latest available observations of the monitor's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ClusterOperators contains the health status of all monitored ClusterOperators.
	// +optional
	ClusterOperators []ClusterOperatorHealth `json:"clusterOperators,omitempty"`
	// DegradedOperatorCount is the number of degraded ClusterOperators.
	// +optional
	DegradedOperatorCount int32 `json:"degradedOperatorCount,omitempty"`
	// UnavailableOperatorCount is the number of unavailable ClusterOperators.
	// +optional
	UnavailableOperatorCount int32 `json:"unavailableOperatorCount,omitempty"`
	// ProgressingOperatorCount is the number of progressing ClusterOperators.
	// +optional
	ProgressingOperatorCount int32 `json:"progressingOperatorCount,omitempty"`
	// OverallHealth summarizes cluster health: Healthy, Warning, or Critical.
	// +optional
	OverallHealth string `json:"overallHealth,omitempty"`
	// LastCorrelationTime is the timestamp of the last health correlation.
	// +optional
	LastCorrelationTime *metav1.Time `json:"lastCorrelationTime,omitempty"`
	// ObservedGeneration reflects the generation of the spec last processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Health",type=string,JSONPath=`.status.overallHealth`
// +kubebuilder:printcolumn:name="Degraded",type=integer,JSONPath=`.status.degradedOperatorCount`
// +kubebuilder:printcolumn:name="Unavailable",type=integer,JSONPath=`.status.unavailableOperatorCount`
// +kubebuilder:printcolumn:name="Last Check",type=date,JSONPath=`.status.lastCorrelationTime`

// ClusterDegradationMonitor is the Schema for the clusterdegradationmonitors API.
type ClusterDegradationMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterDegradationMonitorSpec   `json:"spec,omitempty"`
	Status ClusterDegradationMonitorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterDegradationMonitorList contains a list of ClusterDegradationMonitor.
type ClusterDegradationMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterDegradationMonitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterDegradationMonitor{}, &ClusterDegradationMonitorList{})
}
