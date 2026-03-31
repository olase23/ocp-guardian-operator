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
	"fmt"
	"reflect"
	"strings"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
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
	conditionTypeDriftDetected = "DriftDetected"
	conditionTypeReady         = "Ready"

	driftTypeModified   = "Modified"
	driftTypeDeleted    = "Deleted"
	driftTypeUnexpected = "Unexpected"
)

// RBACDriftMonitorReconciler reconciles a RBACDriftMonitor object.
type RBACDriftMonitorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=monitoring.ocp-guardian.io,resources=rbacdriftmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.ocp-guardian.io,resources=rbacdriftmonitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=monitoring.ocp-guardian.io,resources=rbacdriftmonitors/finalizers,verbs=update
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings;roles;rolebindings,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *RBACDriftMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	monitor := &monitoringv1alpha1.RBACDriftMonitor{}
	if err := r.Get(ctx, req.NamespacedName, monitor); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if monitor.Spec.Suspend {
		logger.Info("RBACDriftMonitor is suspended, skipping reconciliation")
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "Suspended",
			Message:            "Monitoring is suspended",
			ObservedGeneration: monitor.Generation,
		})
		if err := r.Status().Update(ctx, monitor); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// For SnapshotBased mode, ensure we have a baseline snapshot to compare against
	if monitor.Spec.Mode == monitoringv1alpha1.SnapshotBased {
		if monitor.Status.SnapShotBaseline == nil {
			if err := r.MakeBaselineSnapshot(ctx, monitor); err != nil {
				logger.Error(err, "Failed to create baseline snapshot for SnapshotBased mode")
				meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
					Type:               conditionTypeReady,
					Status:             metav1.ConditionFalse,
					Reason:             "BaselineSnapshotFailed",
					Message:            "Failed to create baseline snapshot for SnapshotBased mode",
					ObservedGeneration: monitor.Generation,
				})
				if err := r.Status().Update(ctx, monitor); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
			logger.Info("Baseline snapshot created for SnapshotBased mode")
		}
	}

	if monitor.Spec.DetectUnexpected {
		logger.Info("Start searching for unexpected RBAC drifts")
		r.DetectUnexpectedDrifts(ctx, monitor)
	}

	if monitor.Spec.Mode == monitoringv1alpha1.SnapshotBased {
		logger.Info("Performing SnapshotBased drift check")
		return r.checkDriftSnapshotBased(ctx, monitor)
	}

	logger.Info("Performing RulesBased drift check")
	return r.checkDriftRulesBased(ctx, monitor)
}

// Compare the current RBAC state against the stored snapshot baseline.
func (r *RBACDriftMonitorReconciler) checkDriftSnapshotBased(ctx context.Context, monitor *monitoringv1alpha1.RBACDriftMonitor) (ctrl.Result, error) {
	var driftItems []monitoringv1alpha1.RBACDrift
	now := metav1.Now()

	logger := log.FromContext(ctx)

	// Check ClusterRoles Snapshots
	for _, snapshotBaseline := range monitor.Status.SnapShotBaseline.ClusterRoles {
		actual := &rbacv1.ClusterRole{}
		err := r.Get(ctx, types.NamespacedName{Name: snapshotBaseline.Name}, actual)
		if errors.IsNotFound(err) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "ClusterRole",
				ResourceName: snapshotBaseline.Name,
				DriftType:    driftTypeDeleted,
				Message:      fmt.Sprintf("ClusterRole %q not found but expected in snapshot baseline", snapshotBaseline.Name),
				DetectedAt:   now,
			})
			continue
		}
		if err != nil {
			logger.Error(err, "Failed to get ClusterRole", "name", snapshotBaseline.Name)
			continue
		}
		if !reflect.DeepEqual(sortPolicyRules(snapshotBaseline.Rules), sortPolicyRules(actual.Rules)) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "ClusterRole",
				ResourceName: snapshotBaseline.Name,
				DriftType:    driftTypeModified,
				Message:      fmt.Sprintf("ClusterRole %q rules differ from snapshot baseline", snapshotBaseline.Name),
				DetectedAt:   now,
			})
		}
	}

	// Emit events for detected drifts
	for _, drift := range driftItems {
		eventType := "Warning"
		if monitor.Spec.Severity == "Critical" {
			eventType = "Warning"
		}
		r.Recorder.Event(monitor, eventType, "DriftDetected",
			fmt.Sprintf("[%s] %s %s: %s", drift.DriftType, drift.ResourceKind, drift.ResourceName, drift.Message))
	}

	// Update status with detected drifts and conditions
	monitor.Status.DriftItems = driftItems
	monitor.Status.DriftCount = int32(len(driftItems))
	monitor.Status.LastCheckTime = &now
	monitor.Status.ObservedGeneration = monitor.Generation

	if len(driftItems) > 0 {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeDriftDetected,
			Status:             metav1.ConditionTrue,
			Reason:             "DriftFound",
			Message:            fmt.Sprintf("%d RBAC drift(s) detected", len(driftItems)),
			ObservedGeneration: monitor.Generation,
		})
	}

	return ctrl.Result{}, nil
}

// Compare the current RBAC state directly against the expected baseline defined in the spec.
func (r *RBACDriftMonitorReconciler) checkDriftRulesBased(ctx context.Context, monitor *monitoringv1alpha1.RBACDriftMonitor) (ctrl.Result, error) {
	var driftItems []monitoringv1alpha1.RBACDrift
	now := metav1.Now()

	logger := log.FromContext(ctx)

	// Check ClusterRoles
	for _, baseline := range monitor.Spec.Baseline.ClusterRoles {
		actual := &rbacv1.ClusterRole{}
		err := r.Get(ctx, types.NamespacedName{Name: baseline.Name}, actual)
		if errors.IsNotFound(err) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "ClusterRole",
				ResourceName: baseline.Name,
				DriftType:    driftTypeDeleted,
				Message:      fmt.Sprintf("ClusterRole %q not found but expected in baseline", baseline.Name),
				DetectedAt:   now,
			})
			continue
		}
		if err != nil {
			logger.Error(err, "Failed to get ClusterRole", "name", baseline.Name)
			continue
		}
		if !reflect.DeepEqual(sortPolicyRules(baseline.Rules), sortPolicyRules(actual.Rules)) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "ClusterRole",
				ResourceName: baseline.Name,
				DriftType:    driftTypeModified,
				Message:      fmt.Sprintf("ClusterRole %q rules differ from baseline", baseline.Name),
				DetectedAt:   now,
			})
		}
	}

	// Check ClusterRoleBindings
	for _, baseline := range monitor.Spec.Baseline.ClusterRoleBindings {
		actual := &rbacv1.ClusterRoleBinding{}
		err := r.Get(ctx, types.NamespacedName{Name: baseline.Name}, actual)
		if errors.IsNotFound(err) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "ClusterRoleBinding",
				ResourceName: baseline.Name,
				DriftType:    driftTypeDeleted,
				Message:      fmt.Sprintf("ClusterRoleBinding %q not found but expected in baseline", baseline.Name),
				DetectedAt:   now,
			})
			continue
		}
		if err != nil {
			logger.Error(err, "Failed to get ClusterRoleBinding", "name", baseline.Name)
			continue
		}
		if !reflect.DeepEqual(baseline.RoleRef, actual.RoleRef) || !reflect.DeepEqual(baseline.Subjects, actual.Subjects) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "ClusterRoleBinding",
				ResourceName: baseline.Name,
				DriftType:    driftTypeModified,
				Message:      fmt.Sprintf("ClusterRoleBinding %q differs from baseline (roleRef or subjects changed)", baseline.Name),
				DetectedAt:   now,
			})
		}
	}

	// Check Roles
	for _, baseline := range monitor.Spec.Baseline.Roles {
		actual := &rbacv1.Role{}
		err := r.Get(ctx, types.NamespacedName{Name: baseline.Name, Namespace: baseline.Namespace}, actual)
		if errors.IsNotFound(err) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "Role",
				ResourceName: baseline.Name,
				Namespace:    baseline.Namespace,
				DriftType:    driftTypeDeleted,
				Message:      fmt.Sprintf("Role %q in namespace %q not found but expected in baseline", baseline.Name, baseline.Namespace),
				DetectedAt:   now,
			})
			continue
		}
		if err != nil {
			logger.Error(err, "Failed to get Role", "name", baseline.Name, "namespace", baseline.Namespace)
			continue
		}
		if !reflect.DeepEqual(sortPolicyRules(baseline.Rules), sortPolicyRules(actual.Rules)) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "Role",
				ResourceName: baseline.Name,
				Namespace:    baseline.Namespace,
				DriftType:    driftTypeModified,
				Message:      fmt.Sprintf("Role %q in namespace %q rules differ from baseline", baseline.Name, baseline.Namespace),
				DetectedAt:   now,
			})
		}
	}

	// Check RoleBindings
	for _, baseline := range monitor.Spec.Baseline.RoleBindings {
		actual := &rbacv1.RoleBinding{}
		err := r.Get(ctx, types.NamespacedName{Name: baseline.Name, Namespace: baseline.Namespace}, actual)
		if errors.IsNotFound(err) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "RoleBinding",
				ResourceName: baseline.Name,
				Namespace:    baseline.Namespace,
				DriftType:    driftTypeDeleted,
				Message:      fmt.Sprintf("RoleBinding %q in namespace %q not found but expected in baseline", baseline.Name, baseline.Namespace),
				DetectedAt:   now,
			})
			continue
		}
		if err != nil {
			logger.Error(err, "Failed to get RoleBinding", "name", baseline.Name, "namespace", baseline.Namespace)
			continue
		}
		if !reflect.DeepEqual(baseline.RoleRef, actual.RoleRef) || !reflect.DeepEqual(baseline.Subjects, actual.Subjects) {
			driftItems = append(driftItems, monitoringv1alpha1.RBACDrift{
				ResourceKind: "RoleBinding",
				ResourceName: baseline.Name,
				Namespace:    baseline.Namespace,
				DriftType:    driftTypeModified,
				Message:      fmt.Sprintf("RoleBinding %q in namespace %q differs from baseline", baseline.Name, baseline.Namespace),
				DetectedAt:   now,
			})
		}
	}

	// Emit events for detected drifts
	for _, drift := range driftItems {
		eventType := "Warning"
		if monitor.Spec.Severity == "Critical" {
			eventType = "Warning"
		}
		r.Recorder.Event(monitor, eventType, "DriftDetected",
			fmt.Sprintf("[%s] %s %s: %s", drift.DriftType, drift.ResourceKind, drift.ResourceName, drift.Message))
	}

	// Update status
	monitor.Status.DriftItems = driftItems
	monitor.Status.DriftCount = int32(len(driftItems))
	monitor.Status.LastCheckTime = &now
	monitor.Status.ObservedGeneration = monitor.Generation

	if len(driftItems) > 0 {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeDriftDetected,
			Status:             metav1.ConditionTrue,
			Reason:             "DriftFound",
			Message:            fmt.Sprintf("%d RBAC drift(s) detected", len(driftItems)),
			ObservedGeneration: monitor.Generation,
		})
	} else {
		meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
			Type:               conditionTypeDriftDetected,
			Status:             metav1.ConditionFalse,
			Reason:             "NoDrift",
			Message:            "All RBAC resources match baseline",
			ObservedGeneration: monitor.Generation,
		})
	}

	meta.SetStatusCondition(&monitor.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             "ReconcileComplete",
		Message:            "RBAC drift check completed successfully",
		ObservedGeneration: monitor.Generation,
	})

	if err := r.Status().Update(ctx, monitor); err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter := time.Duration(monitor.Spec.CheckIntervalMinutes) * time.Minute
	logger.Info("RBAC drift check complete", "driftCount", len(driftItems), "requeueAfter", requeueAfter)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *RBACDriftMonitorReconciler) DetectUnexpectedDrifts(ctx context.Context, monitor *monitoringv1alpha1.RBACDriftMonitor) {
	now := metav1.Now()
	var driftItems []monitoringv1alpha1.RBACDrift
	logger := log.FromContext(ctx)

	unexpectedCRBs, err := r.detectUnexpectedCRBs(ctx, monitor, now)
	if err != nil {
		logger.Error(err, "Failed to detect unexpected ClusterRoleBindings")
	} else {
		driftItems = append(driftItems, unexpectedCRBs...)
	}

	unexpectedCRs, err := r.detectUnexpectedClusterRoles(ctx, monitor, now)
	if err != nil {
		logger.Error(err, "Failed to detect unexpected ClusterRoles")
	} else {
		driftItems = append(driftItems, unexpectedCRs...)
	}

	unexpectedRBs, err := r.detectUnexpectedRBs(ctx, monitor, now)
	if err != nil {
		logger.Error(err, "Failed to detect unexpected RoleBindings")
	} else {
		driftItems = append(driftItems, unexpectedRBs...)
	}

	unexpectedRoles, err := r.detectUnexpectedRoles(ctx, monitor, now)
	if err != nil {
		logger.Error(err, "Failed to detect unexpected Roles")
	} else {
		driftItems = append(driftItems, unexpectedRoles...)
	}

	for _, driftItem := range driftItems {
		eventType := "Warning"
		if monitor.Spec.Severity == "Critical" {
			eventType = "Critical"
		}
		r.Recorder.Event(monitor, eventType, "UnexpectedDriftDetected",
			fmt.Sprintf("[%s] %s %s: %s", driftItem.DriftType, driftItem.ResourceKind, driftItem.ResourceName, driftItem.Message))
	}

	logger.Info("Unexpected drift detection complete", "unexpectedDriftCount", len(driftItems))
}

func (r *RBACDriftMonitorReconciler) detectUnexpectedCRBs(
	ctx context.Context,
	monitor *monitoringv1alpha1.RBACDriftMonitor,
	now metav1.Time,
) ([]monitoringv1alpha1.RBACDrift, error) {
	logger := log.FromContext(ctx)

	var allCRBs rbacv1.ClusterRoleBindingList
	if err := r.List(ctx, &allCRBs); err != nil {
		return nil, fmt.Errorf("failed to list ClusterRoleBindings: %w", err)
	}

	// Baseline-Namen als Set für O(1) Lookup
	baselineNames := make(map[string]struct{})
	for _, b := range monitor.Spec.Baseline.ClusterRoleBindings {
		baselineNames[b.Name] = struct{}{}
	}

	var drifts []monitoringv1alpha1.RBACDrift
	for _, crb := range allCRBs.Items {
		// Bereits in Baseline → kein Unexpected
		if _, ok := baselineNames[crb.Name]; ok {
			continue
		}

		// Exclude-Filter anwenden
		if r.shouldExclude(crb.Name, crb.Annotations, &monitor.Spec) {
			continue
		}

		drifts = append(drifts, monitoringv1alpha1.RBACDrift{
			ResourceKind: "ClusterRoleBinding",
			ResourceName: crb.Name,
			DriftType:    driftTypeUnexpected,
			Message: fmt.Sprintf(
				"ClusterRoleBinding %q not in baseline (role: %s, subjects: %s)",
				crb.Name,
				crb.RoleRef.Name,
				formatSubjects(crb.Subjects),
			),
			DetectedAt: now,
		})
	}

	logger.Info("Unexpected CRB check complete",
		"total", len(allCRBs.Items),
		"unexpected", len(drifts),
	)
	return drifts, nil
}

// detectUnexpectedClusterRoles() finds ClusterRoles that are not in the baseline.
func (r *RBACDriftMonitorReconciler) detectUnexpectedClusterRoles(ctx context.Context,
	monitor *monitoringv1alpha1.RBACDriftMonitor,
	now metav1.Time,
) ([]monitoringv1alpha1.RBACDrift, error) {
	var allCRs rbacv1.ClusterRoleList
	if err := r.List(ctx, &allCRs); err != nil {
		return nil, fmt.Errorf("failed to list ClusterRoles: %w", err)
	}

	baselineNames := make(map[string]struct{})
	for _, b := range monitor.Spec.Baseline.ClusterRoles {
		baselineNames[b.Name] = struct{}{}
	}

	var drifts []monitoringv1alpha1.RBACDrift
	for _, cr := range allCRs.Items {
		if _, ok := baselineNames[cr.Name]; ok {
			continue
		}
		if r.shouldExclude(cr.Name, cr.Annotations, &monitor.Spec) {
			continue
		}

		drifts = append(drifts, monitoringv1alpha1.RBACDrift{
			ResourceKind: "ClusterRole",
			ResourceName: cr.Name,
			DriftType:    driftTypeUnexpected,
			Message: fmt.Sprintf(
				"ClusterRole %q not in baseline (%d rules)",
				cr.Name, len(cr.Rules),
			),
			DetectedAt: now,
		})
	}
	return drifts, nil
}

// detectUnexpectedRBs findet namespace-scoped RoleBindings die nicht in der Baseline stehen.
func (r *RBACDriftMonitorReconciler) detectUnexpectedRBs(
	ctx context.Context,
	monitor *monitoringv1alpha1.RBACDriftMonitor,
	now metav1.Time,
) ([]monitoringv1alpha1.RBACDrift, error) {
	var allRBs rbacv1.RoleBindingList
	if err := r.List(ctx, &allRBs); err != nil {
		return nil, fmt.Errorf("failed to list RoleBindings: %w", err)
	}

	type nsName struct{ ns, name string }
	baselineKeys := make(map[nsName]struct{})
	for _, b := range monitor.Spec.Baseline.RoleBindings {
		baselineKeys[nsName{b.Namespace, b.Name}] = struct{}{}
	}

	var drifts []monitoringv1alpha1.RBACDrift
	for _, rb := range allRBs.Items {
		if _, ok := baselineKeys[nsName{rb.Namespace, rb.Name}]; ok {
			continue
		}
		if r.shouldExclude(rb.Name, rb.Annotations, &monitor.Spec) {
			continue
		}

		drifts = append(drifts, monitoringv1alpha1.RBACDrift{
			ResourceKind: "RoleBinding",
			ResourceName: rb.Name,
			Namespace:    rb.Namespace,
			DriftType:    driftTypeUnexpected,
			Message: fmt.Sprintf(
				"RoleBinding %q in namespace %q not in baseline (role: %s)",
				rb.Name, rb.Namespace, rb.RoleRef.Name,
			),
			DetectedAt: now,
		})
	}
	return drifts, nil
}

// detectUnexpectedRoles finds namespace-scoped Roles that are not in the baseline.
func (r *RBACDriftMonitorReconciler) detectUnexpectedRoles(
	ctx context.Context,
	monitor *monitoringv1alpha1.RBACDriftMonitor,
	now metav1.Time,
) ([]monitoringv1alpha1.RBACDrift, error) {
	var allRoles rbacv1.RoleList
	if err := r.List(ctx, &allRoles); err != nil {
		return nil, fmt.Errorf("failed to list Roles: %w", err)
	}

	type nsName struct{ ns, name string }
	baselineKeys := make(map[nsName]struct{})
	for _, b := range monitor.Spec.Baseline.Roles {
		baselineKeys[nsName{b.Namespace, b.Name}] = struct{}{}
	}

	var drifts []monitoringv1alpha1.RBACDrift
	for _, role := range allRoles.Items {
		if _, ok := baselineKeys[nsName{role.Namespace, role.Name}]; ok {
			continue
		}
		if r.shouldExclude(role.Name, role.Annotations, &monitor.Spec) {
			continue
		}

		drifts = append(drifts, monitoringv1alpha1.RBACDrift{
			ResourceKind: "Role",
			ResourceName: role.Name,
			Namespace:    role.Namespace,
			DriftType:    driftTypeUnexpected,
			Message: fmt.Sprintf(
				"Role %q in namespace %q not in baseline (%d rules)",
				role.Name, role.Namespace, len(role.Rules),
			),
			DetectedAt: now,
		})
	}
	return drifts, nil
}

// formatSubjects() creates a human-readable representation of the subjects.
func formatSubjects(subjects []rbacv1.Subject) string {
	if len(subjects) == 0 {
		return "<none>"
	}
	parts := make([]string, 0, len(subjects))
	for _, s := range subjects {
		switch s.Kind {
		case "ServiceAccount":
			parts = append(parts, fmt.Sprintf("SA:%s/%s", s.Namespace, s.Name))
		case "User":
			parts = append(parts, fmt.Sprintf("User:%s", s.Name))
		case "Group":
			parts = append(parts, fmt.Sprintf("Group:%s", s.Name))
		default:
			parts = append(parts, fmt.Sprintf("%s:%s", s.Kind, s.Name))
		}
	}
	return strings.Join(parts, ", ")
}

// sortPolicyRules returns a sorted copy of policy rules for stable comparison.
func sortPolicyRules(rules []rbacv1.PolicyRule) []rbacv1.PolicyRule {
	sorted := make([]rbacv1.PolicyRule, len(rules))
	copy(sorted, rules)
	// Sort each rule's internal slices for stable comparison
	for i := range sorted {
		sortStrings(sorted[i].Verbs)
		sortStrings(sorted[i].APIGroups)
		sortStrings(sorted[i].Resources)
		sortStrings(sorted[i].ResourceNames)
		sortStrings(sorted[i].NonResourceURLs)
	}
	return sorted
}

// sortStrings sorts a string slice in place.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// mapRBACToMonitor maps RBAC resource events to RBACDriftMonitor reconcile requests.
func (r *RBACDriftMonitorReconciler) mapRBACToMonitor(ctx context.Context, obj client.Object) []reconcile.Request {
	monitors := &monitoringv1alpha1.RBACDriftMonitorList{}
	if err := r.List(ctx, monitors); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, monitor := range monitors.Items {
		if monitor.Spec.Suspend {
			continue
		}
		if r.baselineReferencesResource(&monitor, obj) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: monitor.Name},
			})
		}
	}
	return requests
}

// baselineReferencesResource checks whether a monitor's baseline references the given resource.
func (r *RBACDriftMonitorReconciler) baselineReferencesResource(monitor *monitoringv1alpha1.RBACDriftMonitor, obj client.Object) bool {
	name := obj.GetName()
	namespace := obj.GetNamespace()

	switch obj.(type) {
	case *rbacv1.ClusterRole:
		for _, cr := range monitor.Spec.Baseline.ClusterRoles {
			if cr.Name == name {
				return true
			}
		}
	case *rbacv1.ClusterRoleBinding:
		for _, crb := range monitor.Spec.Baseline.ClusterRoleBindings {
			if crb.Name == name {
				return true
			}
		}
	case *rbacv1.Role:
		for _, role := range monitor.Spec.Baseline.Roles {
			if role.Name == name && role.Namespace == namespace {
				return true
			}
		}
	case *rbacv1.RoleBinding:
		for _, rb := range monitor.Spec.Baseline.RoleBindings {
			if rb.Name == name && rb.Namespace == namespace {
				return true
			}
		}
	}
	return false
}

func (r *RBACDriftMonitorReconciler) MakeBaselineSnapshot(ctx context.Context, monitor *monitoringv1alpha1.RBACDriftMonitor) error {
	var snapshot monitoringv1alpha1.RBACBaseline

	// Capture ClusterRoles
	var clusterRoles rbacv1.ClusterRoleList
	if err := r.List(ctx, &clusterRoles); err != nil {
		return err
	}
	for _, cr := range clusterRoles.Items {
		snapshot.ClusterRoles = append(snapshot.ClusterRoles, monitoringv1alpha1.ClusterRoleBaseline{
			Name:  cr.Name,
			Rules: cr.Rules,
		})
	}

	// Capture ClusterRoleBindings
	var clusterRoleBindings rbacv1.ClusterRoleBindingList
	if err := r.List(ctx, &clusterRoleBindings); err != nil {
		return err
	}
	for _, crb := range clusterRoleBindings.Items {
		snapshot.ClusterRoleBindings = append(snapshot.ClusterRoleBindings, monitoringv1alpha1.ClusterRoleBindingBaseline{
			Name:     crb.Name,
			RoleRef:  crb.RoleRef,
			Subjects: crb.Subjects,
		})
	}

	// Capture Roles
	var roles rbacv1.RoleList
	if err := r.List(ctx, &roles); err != nil {
		return err
	}
	for _, role := range roles.Items {
		snapshot.Roles = append(snapshot.Roles, monitoringv1alpha1.RoleBaseline{
			Name:      role.Name,
			Namespace: role.Namespace,
			Rules:     role.Rules,
		})
	}

	// Capture RoleBindings
	var roleBindings rbacv1.RoleBindingList
	if err := r.List(ctx, &roleBindings); err != nil {
		return err
	}
	for _, rb := range roleBindings.Items {
		snapshot.RoleBindings = append(snapshot.RoleBindings, monitoringv1alpha1.RoleBindingBaseline{
			Name:      rb.Name,
			Namespace: rb.Namespace,
			RoleRef:   rb.RoleRef,
			Subjects:  rb.Subjects,
		})
	}

	monitor.Status.SnapShotBaseline = &snapshot
	return r.Status().Update(ctx, monitor)
}

func (r *RBACDriftMonitorReconciler) shouldExclude(
	name string,
	annotations map[string]string,
	spec *monitoringv1alpha1.RBACDriftMonitorSpec,
) bool {
	// Prefix-basierte Ausschlüsse
	for _, pattern := range spec.ExcludePatterns {
		if strings.HasPrefix(name, pattern) {
			return true
		}
	}

	// Annotation-basierte Ausschlüsse
	for _, key := range spec.ExcludeAnnotations {
		if _, exists := annotations[key]; exists {
			return true
		}
	}

	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *RBACDriftMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1alpha1.RBACDriftMonitor{}).
		Watches(&rbacv1.ClusterRole{}, handler.EnqueueRequestsFromMapFunc(r.mapRBACToMonitor)).
		Watches(&rbacv1.ClusterRoleBinding{}, handler.EnqueueRequestsFromMapFunc(r.mapRBACToMonitor)).
		Watches(&rbacv1.Role{}, handler.EnqueueRequestsFromMapFunc(r.mapRBACToMonitor)).
		Watches(&rbacv1.RoleBinding{}, handler.EnqueueRequestsFromMapFunc(r.mapRBACToMonitor)).
		Complete(r)
}
