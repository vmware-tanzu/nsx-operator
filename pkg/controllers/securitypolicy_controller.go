/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services"
)

var (
	log = logf.Log.WithName("controller").WithName("securitypolicy")
)

// SecurityPolicyReconciler reconciles a SecurityPolicy object
type SecurityPolicyReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Service *services.SecurityPolicyService
}

func (r *SecurityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	obj := &v1alpha1.SecurityPolicy{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch object")
		return ctrl.Result{}, err
	}

	log.Info("reconciling securitypolicy CR", "securitypolicy", req.NamespacedName)

	if err := r.Service.CreateOrUpdateSecurityPolicy(obj); err != nil {
		log.Error(err, "failed to create or update security policy", "securitypolicy", req.NamespacedName)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityPolicy{}).
		Complete(r)
}
