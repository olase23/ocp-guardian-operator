/*
Copyright 2024 OCP Guardian.

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
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	monitoringv1alpha1 "github.com/ocp-guardian/ocp-guardian-operator/api/v1alpha1"
)

const (
	conditionTypeClusterDegraded    = "ClusterDegraded"
	conditionTypeClusterUnavailable = "ClusterUnavailable"

	healthHealthy  = "Healthy"
	healthWarning  = "Warning"
	healthCritical = "Critical"
)

// ClusterDegradationMonitorReconciler reconciles a ClusterDegradationMonitor object.
type ClusterDegradationMonitorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=monitoring.ocp-guardian.io,resources=clusterdegradationmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.ocp-guardian.io,resources=clusterdegradationmonitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=monitoring.ocp-guardian.io,resources=clusterdegradationmonitors/finalizers,verbs=update
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusteroperators,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ClusterDegradationMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	monitor := &monitoringv1alpha1.ClusterDegradationMonitor{}
	if err := r.Get(ctx, req.NamespacedName, monitor); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if monitor.Spec.Suspend {
		logger.Info("ClusterDegradationMonitor is suspended, skipping reconciliation")
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "Suspended",
			Message:            "Cluster degradation monitoring is suspended",
			ObservedGeneration: monitor.Generation,
		})
		if err := r.Status().Update(ctx, monitor); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// List all ClusterOperators
	clusterOperators := &configv1.ClusterOperatorList{}
	if err := r.List(ctx, clusterOperators); err != nil {
		logger.Error(err, "Failed to list ClusterOperators")
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "ListFailed",
			Message:            fmt.Sprintf("Failed to list ClusterOperators: %v", err),
			ObservedGeneration: monitor.Generation,
		})
		if statusErr := r.Status().Update(ctx, monitor); statusErr != nil {
			logger.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Build operator filter set
	filterSet := make(map[string]bool)
	for _, name := range monitor.Spec.OperatorFilter {
		filterSet[name] = true
	}

	var operatorHealths []monitoringv1alpha1.ClusterOperatorHealth
	var degradedCount, unavailableCount, progressingCount int32

	for i := range clusterOperators.Items {
		co := &clusterOperators.Items[i]

		// Apply filter if specified
		if len(filterSet) > 0 && !filterSet[co.Name] {
			continue
		}

		health := monitoringv1alpha1.ClusterOperatorHealth{
			Name: co.Name,
		}

		// Extract condition states
		for _, condition := range co.Status.Conditions {
			switch condition.Type {
			case configv1.OperatorAvailable:
				health.Available = condition.Status == configv1.ConditionTrue
				if !health.Available {
					health.Message = condition.Message
				}
			case configv1.OperatorDegraded:
				health.Degraded = condition.Status == configv1.ConditionTrue
				if health.Degraded && health.Message == "" {
					health.Message = condition.Message
				}
			case configv1.OperatorProgressing:
				health.Progressing = condition.Status == configv1.ConditionTrue
			}
			if health.LastTransitionTime == nil || condition.LastTransitionTime.After(health.LastTransitionTime.Time) {
				t := metav1.NewTime(condition.LastTransitionTime.Time)
				health.LastTransitionTime = &t
			}
		}

		// Extract version
		for _, v := range co.Status.Versions {
			if v.Name == "operator" {
				health.Version = v.Version
				break
			}
		}

		if health.Degraded {
			degradedCount++
		}
		if !health.Available {
			unavailableCount++
		}
		if health.Progressing {
			progressingCount++
		}

		operatorHealths = append(operatorHealths, health)
	}

	// Determine overall health
	degradedThreshold := monitor.Spec.DegradedThreshold
	if degradedThreshold == 0 {
		degradedThreshold = 3
	}

	var overallHealth string
	switch {
	case unavailableCount > 0 || degradedCount >= degradedThreshold:
		overallHealth = healthCritical
	case degradedCount > 0:
		overallHealth = healthWarning
	default:
		overallHealth = healthHealthy
	}

	// Emit events for state changes
	if degradedCount > 0 {
		r.Recorder.Event(monitor, "Warning", "ClusterDegraded",
			fmt.Sprintf("%d ClusterOperator(s) are degraded", degradedCount))
	}
	if unavailableCount > 0 {
		r.Recorder.Event(monitor, "Warning", "ClusterUnavailable",
			fmt.Sprintf("%d ClusterOperator(s) are unavailable", unavailableCount))
	}

	// Update status
	now := metav1.Now()
	monitor.Status.ClusterOperators = operatorHealths
	monitor.Status.DegradedOperatorCount = degradedCount
	monitor.Status.UnavailableOperatorCount = unavailableCount
	monitor.Status.ProgressingOperatorCount = progressingCount
	monitor.Status.OverallHealth = overallHealth
	monitor.Status.LastCorrelationTime = &now
	monitor.Status.ObservedGeneration = monitor.Generation

	if degradedCount > 0 {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeClusterDegraded,
			Status:             metav1.ConditionTrue,
			Reason:             "OperatorsDegraded",
			Message:            fmt.Sprintf("%d ClusterOperator(s) are in degraded state", degradedCount),
			ObservedGeneration: monitor.Generation,
		})
	} else {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeClusterDegraded,
			Status:             metav1.ConditionFalse,
			Reason:             "NoOperatorsDegraded",
			Message:            "All monitored ClusterOperators are healthy",
			ObservedGeneration: monitor.Generation,
		})
	}

	if unavailableCount > 0 {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeClusterUnavailable,
			Status:             metav1.ConditionTrue,
			Reason:             "OperatorsUnavailable",
			Message:            fmt.Sprintf("%d ClusterOperator(s) are unavailable", unavailableCount),
			ObservedGeneration: monitor.Generation,
		})
	} else {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeClusterUnavailable,
			Status:             metav1.ConditionFalse,
			Reason:             "AllOperatorsAvailable",
			Message:            "All monitored ClusterOperators are available",
			ObservedGeneration: monitor.Generation,
		})
	}

	meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             "CorrelationComplete",
		Message:            fmt.Sprintf("Cluster health: %s (%d operators monitored)", overallHealth, len(operatorHealths)),
		ObservedGeneration: monitor.Generation,
	})

	if err := r.Status().Update(ctx, monitor); err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter := time.Duration(monitor.Spec.CheckIntervalMinutes) * time.Minute
	logger.Info("Cluster degradation check complete",
		"overallHealth", overallHealth,
		"degraded", degradedCount,
		"unavailable", unavailableCount,
		"progressing", progressingCount)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// mapClusterOperatorToMonitor maps ClusterOperator events to ClusterDegradationMonitor reconcile requests.
func (r *ClusterDegradationMonitorReconciler) mapClusterOperatorToMonitor(ctx context.Context, obj client.Object) []reconcile.Request {
	monitors := &monitoringv1alpha1.ClusterDegradationMonitorList{}
	if err := r.List(ctx, monitors); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, monitor := range monitors.Items {
		if monitor.Spec.Suspend {
			continue
		}
		// Check if this operator is in the filter (or no filter means all)
		if len(monitor.Spec.OperatorFilter) > 0 {
			found := false
			for _, name := range monitor.Spec.OperatorFilter {
				if name == obj.GetName() {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: monitor.Name},
		})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterDegradationMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1alpha1.ClusterDegradationMonitor{}).
		Watches(&configv1.ClusterOperator{},
			handler.EnqueueRequestsFromMapFunc(r.mapClusterOperatorToMonitor)).
		Complete(r)
}
