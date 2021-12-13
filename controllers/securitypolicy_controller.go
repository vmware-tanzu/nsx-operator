/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	securitypolicyv1 "github.com/vmware-tanzu/nsx-operator/api/v1alpha1"
)

// SecurityPolicyReconciler reconciles a SecurityPolicy object
type SecurityPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	*nsx.NSXClient
}

func (r *SecurityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	obj := &securitypolicyv1.SecurityPolicy{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		print(err, "Unable to fetch object")
	} else {
		print("Geeting from Kubebuilder to", obj.Spec.Priority)
	}

	if err := r.Status().Update(ctx, obj); err != nil {
		print(err, "unable to update status")
	}

	queryParam := "Segment"
	r.QueryClient.List(queryParam, nil, nil, nil, nil, nil)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securitypolicyv1.SecurityPolicy{}).
		Complete(r)
}
