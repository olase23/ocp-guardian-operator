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

package controller

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	monitoringv1alpha1 "github.com/ocp-guardian/ocp-guardian-operator/api/v1alpha1"
)

const (
	conditionTypeCertExpiring = "CertificatesExpiring"
	conditionTypeCertExpired  = "CertificatesExpired"

	severityWarning  = "Warning"
	severityCritical = "Critical"
	severityExpired  = "Expired"
)

// CertificateExpiryMonitorReconciler reconciles a CertificateExpiryMonitor object.
type CertificateExpiryMonitorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=monitoring.ocp-guardian.io,resources=certificateexpirymonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.ocp-guardian.io,resources=certificateexpirymonitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=monitoring.ocp-guardian.io,resources=certificateexpirymonitors/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *CertificateExpiryMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	monitor := &monitoringv1alpha1.CertificateExpiryMonitor{}
	if err := r.Get(ctx, req.NamespacedName, monitor); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if monitor.Spec.Suspend {
		logger.Info("CertificateExpiryMonitor is suspended, skipping reconciliation")
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "Suspended",
			Message:            "Certificate monitoring is suspended",
			ObservedGeneration: monitor.Generation,
		})
		if err := r.Status().Update(ctx, monitor); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Determine namespaces to scan
	namespaces, err := r.getTargetNamespaces(ctx, monitor)
	if err != nil {
		logger.Error(err, "Failed to determine target namespaces")
		return ctrl.Result{}, err
	}

	logger.Info("Target namespaces for certificate scan", "namespaces", namespaces)

	// Build exclude set
	excludeSet := make(map[string]bool)
	for _, ns := range monitor.Spec.ExcludeNamespaces {
		excludeSet[ns] = true
	}

	var expiringCerts []monitoringv1alpha1.CertificateInfo
	var totalScanned int32
	var expiringCount, expiredCount int32
	var currentStatus string

	warningDays := monitor.Spec.WarningThresholdDays
	if warningDays == 0 {
		warningDays = 30
	}
	criticalDays := monitor.Spec.CriticalThresholdDays
	if criticalDays == 0 {
		criticalDays = 7
	}

	for _, ns := range namespaces {
		if excludeSet[ns] {
			continue
		}

		secrets := &corev1.SecretList{}
		if err := r.List(ctx, secrets, client.InNamespace(ns)); err != nil {
			logger.Error(err, "Failed to list secrets", "namespace", ns)
			continue
		}

		for i := range secrets.Items {
			secret := &secrets.Items[i]
			if secret.Type != corev1.SecretTypeTLS {
				continue
			}

			logger.Info("Checking certificate expiration", "namespace", ns)
			totalScanned++

			certData, ok := secret.Data["tls.crt"]
			if !ok {
				continue
			}

			certInfo := r.parseCertificate(certData, secret.Name, secret.Namespace, warningDays, criticalDays)
			if certInfo != nil {
				logger.Info("Found expiring/expired certificate", "namespace", certInfo.Namespace,
					"secret", certInfo.SecretName, "subject", certInfo.Subject, "daysUntilExpiry",
					certInfo.DaysUntilExpiry, "severity", certInfo.Severity)
				expiringCerts = append(expiringCerts, *certInfo)
				currentStatus = certInfo.Severity
				switch certInfo.Severity {
				case severityExpired:
					expiredCount++
					expiringCount++
				case severityCritical, severityWarning:
					expiringCount++
				}
			}
		}
	}

	// Emit events for expiring/expired certificates
	for _, cert := range expiringCerts {
		prevStatus := getPreviousCertStatus(monitor, cert.SecretName)
		if prevStatus != currentStatus {
			r.Recorder.Event(monitor, "Warning", "CertificateExpiry",
				fmt.Sprintf("[%s] Certificate in %s/%s (subject: %s) expires in %d days",
					cert.Severity, cert.Namespace, cert.SecretName, cert.Subject, cert.DaysUntilExpiry))
		}
	}

	// Update status
	now := metav1.Now()
	monitor.Status.ExpiringCertificates = expiringCerts
	monitor.Status.TotalCertificatesScanned = totalScanned
	monitor.Status.ExpiringCount = expiringCount
	monitor.Status.ExpiredCount = expiredCount
	monitor.Status.LastScanTime = &now
	monitor.Status.ObservedGeneration = monitor.Generation

	if expiredCount > 0 {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeCertExpired,
			Status:             metav1.ConditionTrue,
			Reason:             "CertificatesExpired",
			Message:            fmt.Sprintf("%d certificate(s) have expired", expiredCount),
			ObservedGeneration: monitor.Generation,
		})
	} else {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeCertExpired,
			Status:             metav1.ConditionFalse,
			Reason:             "NoCertificatesExpired",
			Message:            "No expired certificates found",
			ObservedGeneration: monitor.Generation,
		})
	}

	if expiringCount > 0 {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeCertExpiring,
			Status:             metav1.ConditionTrue,
			Reason:             "CertificatesExpiring",
			Message:            fmt.Sprintf("%d certificate(s) expiring within threshold", expiringCount),
			ObservedGeneration: monitor.Generation,
		})
	} else {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeCertExpiring,
			Status:             metav1.ConditionFalse,
			Reason:             "NoCertificatesExpiring",
			Message:            "All certificates are within acceptable expiry range",
			ObservedGeneration: monitor.Generation,
		})
	}

	meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             "ScanComplete",
		Message:            fmt.Sprintf("Scanned %d certificates across %d namespaces", totalScanned, len(namespaces)-len(excludeSet)),
		ObservedGeneration: monitor.Generation,
	})

	if err := r.Status().Update(ctx, monitor); err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter := time.Duration(monitor.Spec.CheckIntervalMinutes) * time.Minute
	logger.Info("Certificate scan complete", "totalScanned", totalScanned, "expiring", expiringCount, "expired", expiredCount)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func getPreviousCertStatus(monitor *monitoringv1alpha1.CertificateExpiryMonitor, certName string) string {
	for _, cert := range monitor.Status.ExpiringCertificates {
		if cert.SecretName == certName {
			return cert.Severity
		}
	}
	return ""
}

// getTargetNamespaces returns the list of namespaces to scan based on the selector.
func (r *CertificateExpiryMonitorReconciler) getTargetNamespaces(ctx context.Context, monitor *monitoringv1alpha1.CertificateExpiryMonitor) ([]string, error) {
	nsList := &corev1.NamespaceList{}

	if monitor.Spec.NamespaceSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(monitor.Spec.NamespaceSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid namespace selector: %w", err)
		}
		if err := r.List(ctx, nsList, client.MatchingLabelsSelector{Selector: selector}); err != nil {
			return nil, err
		}
	} else {
		if err := r.List(ctx, nsList); err != nil {
			return nil, err
		}
	}

	var namespaces []string
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, ns.Name)
	}
	return namespaces, nil
}

// parseCertificate parses PEM-encoded certificate data and returns CertificateInfo if it's expiring/expired.
func (r *CertificateExpiryMonitorReconciler) parseCertificate(certData []byte, secretName, namespace string, warningDays, criticalDays int32) *monitoringv1alpha1.CertificateInfo {
	block, _ := pem.Decode(certData)
	if block == nil {
		return nil
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}

	daysUntilExpiry := int32(time.Until(cert.NotAfter).Hours() / 24)

	var severity string
	switch {
	case daysUntilExpiry <= 0:
		severity = severityExpired
	case daysUntilExpiry <= criticalDays:
		severity = severityCritical
	case daysUntilExpiry <= warningDays:
		severity = severityWarning
	default:
		return nil
	}

	return &monitoringv1alpha1.CertificateInfo{
		SecretName:      secretName,
		Namespace:       namespace,
		Subject:         cert.Subject.CommonName,
		Issuer:          cert.Issuer.CommonName,
		ExpiryDate:      metav1.NewTime(cert.NotAfter),
		DaysUntilExpiry: daysUntilExpiry,
		Severity:        severity,
	}
}

// mapTLSSecretToMonitor maps TLS Secret events to CertificateExpiryMonitor reconcile requests.
func (r *CertificateExpiryMonitorReconciler) mapTLSSecretToMonitor(ctx context.Context, obj client.Object) []reconcile.Request {
	monitors := &monitoringv1alpha1.CertificateExpiryMonitorList{}
	if err := r.List(ctx, monitors); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, monitor := range monitors.Items {
		if monitor.Spec.Suspend {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: monitor.Name},
		})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *CertificateExpiryMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1alpha1.CertificateExpiryMonitor{}).
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapTLSSecretToMonitor),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				secret, ok := obj.(*corev1.Secret)
				return ok && secret.Type == corev1.SecretTypeTLS
			}))).
		Complete(r)
}

// Ensure labels import is used
var _ = labels.Everything
