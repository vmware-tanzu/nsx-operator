/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

// Package controllers registers watches for Supervisor network transitions.
package controllers

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Reconciler is the controller registration scaffold for a transition kind.
// Execution and controller-owned status updates belong to UBMWM-12405.
type Reconciler struct {
	client.Client
	Enabled bool
	Object  client.Object
}

// +kubebuilder:rbac:groups=iaas.vmware.com,resources=controlplanenetworktransitions;namespacenetworktransitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;create;update,namespace=vmware-system-nsx
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile observes a transition without accepting or executing an action.
// Neither flag setting changes status or the external network in this scaffold.
func (r *Reconciler) Reconcile(ctx context.Context, key ctrl.Request) (ctrl.Result, error) {
	if !r.Enabled {
		return ctrl.Result{}, nil
	}
	object := r.Object.DeepCopyObject().(client.Object)
	if err := r.Get(ctx, key.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if object.GetDeletionTimestamp().IsZero() {
		ctrl.LoggerFrom(ctx).V(1).Info("Migration action execution is pending UBMWM-12405", "transition", key.NamespacedName)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager registers this kind's independent work queue.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(r.Object).Complete(r)
}
