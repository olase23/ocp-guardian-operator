/*
Copyright 2026..

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
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	RulesBased    = "rules"
	SnapshotBased = "snapshot"
)

// RBACDriftMonitorSpec defines the desired state of RBACDriftMonitor.
type RBACDriftMonitorSpec struct {
	// Baseline defines the expected RBAC state to compare against.
	// +kubebuilder:validation:Required
	Baseline RBACBaseline `json:"baseline"`

	// CheckIntervalMinutes controls how often the controller rechecks for drift.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=5
	// +optional
	CheckIntervalMinutes int32 `json:"checkIntervalMinutes,omitempty"`

	// Severity for drift events: Warning or Critical.
	// +kubebuilder:validation:Enum=Warning;Critical
	// +kubebuilder:default=Warning
	// +optional
	Severity string `json:"severity,omitempty"`

	// Suspend stops drift monitoring when true.
	// +kubebuilder:default=false
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// Mode selector for rules based or snapshot based drift detection.
	// +kubebuilder:validation:Enum=RulesBased;SnapshotBased
	// +kubebuilder:default=RulesBased
	// +optional
	Mode string `json:"mode,omitempty"`

	// DetectUnexpected enables detection of RBAC resources that exist
	// in the cluster but are NOT listed in the baseline.
	// When false (default), only Modified and Deleted drifts are reported.
	// +kubebuilder:default=false
	// +optional
	DetectUnexpected bool `json:"detectUnexpected,omitempty"`

	// ExcludePatterns defines name prefixes to ignore when detecting
	// unexpected resources. Typical values: "system:", "openshift-",
	// "kube-". Only used when detectUnexpected is true.
	// +optional
	ExcludePatterns []string `json:"excludePatterns,omitempty"`

	// ExcludeAnnotations defines annotation keys that, when present on
	// a resource, cause it to be excluded from unexpected detection.
	// Example: "guardian.monitoring/ignore"
	// +optional
	ExcludeAnnotations []string `json:"excludeAnnotations,omitempty"`
}

// RBACBaseline defines the expected RBAC resources.
type RBACBaseline struct {
	// +optional
	ClusterRoles []ClusterRoleBaseline `json:"clusterRoles,omitempty"`
	// +optional
	ClusterRoleBindings []ClusterRoleBindingBaseline `json:"clusterRoleBindings,omitempty"`
	// +optional
	Roles []RoleBaseline `json:"roles,omitempty"`
	// +optional
	RoleBindings []RoleBindingBaseline `json:"roleBindings,omitempty"`
}

// ClusterRoleBaseline defines the expected state of a ClusterRole.
type ClusterRoleBaseline struct {
	// Name of the ClusterRole to monitor.
	Name string `json:"name"`
	// Rules expected in this ClusterRole.
	Rules []rbacv1.PolicyRule `json:"rules"`
}

// ClusterRoleBindingBaseline defines the expected state of a ClusterRoleBinding.
type ClusterRoleBindingBaseline struct {
	// Name of the ClusterRoleBinding to monitor.
	Name string `json:"name"`
	// RoleRef expected for this binding.
	RoleRef rbacv1.RoleRef `json:"roleRef"`
	// Subjects expected for this binding.
	Subjects []rbacv1.Subject `json:"subjects"`
}

// RoleBaseline defines the expected state of a namespaced Role.
type RoleBaseline struct {
	// Name of the Role to monitor.
	Name string `json:"name"`
	// Namespace of the Role.
	Namespace string `json:"namespace"`
	// Rules expected in this Role.
	Rules []rbacv1.PolicyRule `json:"rules"`
}

// RoleBindingBaseline defines the expected state of a namespaced RoleBinding.
type RoleBindingBaseline struct {
	// Name of the RoleBinding to monitor.
	Name string `json:"name"`
	// Namespace of the RoleBinding.
	Namespace string `json:"namespace"`
	// RoleRef expected for this binding.
	RoleRef rbacv1.RoleRef `json:"roleRef"`
	// Subjects expected for this binding.
	Subjects []rbacv1.Subject `json:"subjects"`
}

// RBACDrift describes a single detected drift item.
type RBACDrift struct {
	// ResourceKind is the type of RBAC resource (ClusterRole, ClusterRoleBinding, Role, RoleBinding).
	ResourceKind string `json:"resourceKind"`
	// ResourceName is the name of the drifted resource.
	ResourceName string `json:"resourceName"`
	// Namespace of the resource (empty for cluster-scoped resources).
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// DriftType describes the kind of drift: Modified, Deleted, or Unexpected.
	DriftType string `json:"driftType"`
	// Message provides a human-readable description of the drift.
	Message string `json:"message"`
	// DetectedAt is the time when this drift was first detected.
	DetectedAt metav1.Time `json:"detectedAt"`
}

// RBACDriftMonitorStatus defines the observed state of RBACDriftMonitor.
type RBACDriftMonitorStatus struct {
	// Conditions represent the latest available observations of the monitor's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// DriftItems contains all detected RBAC drifts.
	// +optional
	DriftItems []RBACDrift `json:"driftItems,omitempty"`
	// LastCheckTime is the timestamp of the last drift check.
	// +optional
	LastCheckTime *metav1.Time `json:"lastCheckTime,omitempty"`
	// DriftCount is the number of currently detected drifts.
	// +optional
	DriftCount int32 `json:"driftCount,omitempty"`
	// ObservedGeneration reflects the generation of the spec last processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// SnapshotBaseline captures the RBAC state at the time of the last check for snapshot-based drift detection.
	// +optional
	SnapShotBaseline *RBACBaseline `json:"snapshotBaseline,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rdm
// +kubebuilder:printcolumn:name="Drift Count",type=integer,JSONPath=`.status.driftCount`
// +kubebuilder:printcolumn:name="Last Check",type=date,JSONPath=`.status.lastCheckTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RBACDriftMonitor is the Schema for the rbacdriftmonitors API.
type RBACDriftMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RBACDriftMonitorSpec   `json:"spec,omitempty"`
	Status RBACDriftMonitorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RBACDriftMonitorList contains a list of RBACDriftMonitor.
type RBACDriftMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RBACDriftMonitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RBACDriftMonitor{}, &RBACDriftMonitorList{})
}
