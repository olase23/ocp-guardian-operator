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

// CertificateExpiryMonitorSpec defines the desired state of CertificateExpiryMonitor.
type CertificateExpiryMonitorSpec struct {
	// WarningThresholdDays is the number of days before expiry to raise a warning.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=30
	// +optional
	WarningThresholdDays int32 `json:"warningThresholdDays,omitempty"`

	// CriticalThresholdDays is the number of days before expiry to raise a critical alert.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=7
	// +optional
	CriticalThresholdDays int32 `json:"criticalThresholdDays,omitempty"`

	// CheckIntervalMinutes controls how often the controller scans for expiring certificates.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=60
	// +optional
	CheckIntervalMinutes int32 `json:"checkIntervalMinutes,omitempty"`

	// NamespaceSelector limits which namespaces to scan. Empty means all namespaces.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// ExcludeNamespaces is a list of namespaces to skip during scanning.
	// +optional
	ExcludeNamespaces []string `json:"excludeNamespaces,omitempty"`

	// Suspend stops certificate scanning when true.
	// +kubebuilder:default=false
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// CertificateInfo describes a single certificate that is expiring or expired.
type CertificateInfo struct {
	// SecretName is the name of the TLS Secret containing this certificate.
	SecretName string `json:"secretName"`
	// Namespace where the Secret resides.
	Namespace string `json:"namespace"`
	// Subject is the certificate's subject common name.
	// +optional
	Subject string `json:"subject,omitempty"`
	// Issuer is the certificate's issuer common name.
	// +optional
	Issuer string `json:"issuer,omitempty"`
	// ExpiryDate is the certificate's NotAfter timestamp.
	ExpiryDate metav1.Time `json:"expiryDate"`
	// DaysUntilExpiry is the number of days until the certificate expires (negative if already expired).
	DaysUntilExpiry int32 `json:"daysUntilExpiry"`
	// Severity is the alert level: Warning, Critical, or Expired.
	Severity string `json:"severity"`
}

// CertificateExpiryMonitorStatus defines the observed state of CertificateExpiryMonitor.
type CertificateExpiryMonitorStatus struct {
	// Conditions represent the latest available observations of the monitor's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ExpiringCertificates contains all certificates that are expiring or expired.
	// +optional
	ExpiringCertificates []CertificateInfo `json:"expiringCertificates,omitempty"`
	// TotalCertificatesScanned is the number of TLS secrets examined in the last scan.
	// +optional
	TotalCertificatesScanned int32 `json:"totalCertificatesScanned,omitempty"`
	// ExpiringCount is the number of certificates within the warning or critical threshold.
	// +optional
	ExpiringCount int32 `json:"expiringCount,omitempty"`
	// ExpiredCount is the number of certificates that have already expired.
	// +optional
	ExpiredCount int32 `json:"expiredCount,omitempty"`
	// LastScanTime is the timestamp of the last certificate scan.
	// +optional
	LastScanTime *metav1.Time `json:"lastScanTime,omitempty"`
	// ObservedGeneration reflects the generation of the spec last processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Expiring",type=integer,JSONPath=`.status.expiringCount`
// +kubebuilder:printcolumn:name="Expired",type=integer,JSONPath=`.status.expiredCount`
// +kubebuilder:printcolumn:name="Total Scanned",type=integer,JSONPath=`.status.totalCertificatesScanned`
// +kubebuilder:printcolumn:name="Last Scan",type=date,JSONPath=`.status.lastScanTime`

// CertificateExpiryMonitor is the Schema for the certificateexpirymonitors API.
type CertificateExpiryMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CertificateExpiryMonitorSpec   `json:"spec,omitempty"`
	Status CertificateExpiryMonitorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CertificateExpiryMonitorList contains a list of CertificateExpiryMonitor.
type CertificateExpiryMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CertificateExpiryMonitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CertificateExpiryMonitor{}, &CertificateExpiryMonitorList{})
}
