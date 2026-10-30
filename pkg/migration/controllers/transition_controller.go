/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package controllers registers watches for Supervisor network transitions.
package controllers

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TransitionReconciler is the controller registration scaffold for a transition kind.
// Action handlers and controller-owned status updates are registered separately.
type TransitionReconciler struct {
	client.Client
	Object client.Object
}

// +kubebuilder:rbac:groups=migration.network.vmware.com,resources=controlplanenetworktransitions;namespacenetworktransitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;create;update,namespace=vmware-system-nsx
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile observes a transition without accepting or executing an action.
// This scaffold does not change status or the external network.
func (r *TransitionReconciler) Reconcile(ctx context.Context, key ctrl.Request) (ctrl.Result, error) {
	object := r.Object.DeepCopyObject().(client.Object)
	if err := r.Get(ctx, key.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if object.GetDeletionTimestamp().IsZero() {
		ctrl.LoggerFrom(ctx).WithName("migration").V(1).Info("Observed migration transition; action execution is not configured", "transition", key.NamespacedName)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers this kind's independent work queue.
func (r *TransitionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(r.Object).Complete(r)
}
