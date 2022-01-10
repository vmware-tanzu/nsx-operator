/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services"
)

var (
	log        = logf.Log.WithName("controller").WithName("securitypolicy")
	GCInterval = 60 * time.Second
)

// SecurityPolicyReconciler reconciles a SecurityPolicy object
type SecurityPolicyReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Service *services.SecurityPolicyService
}

func (r *SecurityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.SecurityPolicy{}
	log.Info("reconciling securitypolicy CR", "securitypolicy", req.NamespacedName)

	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "failed to get security policy", "securitypolicy", req.NamespacedName)
			return ctrl.Result{}, err
		}
		if err := r.Service.DeleteSecurityPolicy(obj.UID); err != nil {
			log.Error(err, "failed to delete security policy", "securitypolicy", req.NamespacedName)
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	if err := r.Service.CreateOrUpdateSecurityPolicy(obj); err != nil {
		log.Error(err, "failed to create or update security policy", "securitypolicy", req.NamespacedName)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	go r.GarbageCollector()
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityPolicy{}).
		Complete(r)
}

func (r *SecurityPolicyReconciler) GarbageCollector() {
	ctx := context.Background()
	log.V(1).Info("begin to GC")
	for {
		policyIDset, err := r.Service.ListSecurityPolicy()
		if err != nil {
			log.Error(err, "failed to get security policy from local store")
		}
		var policyList *v1alpha1.SecurityPolicyList
		err = r.List(ctx, policyList)
		if err != nil {
			log.Error(err, "failed to list security policy")
		}
		remotePolicySet := sets.NewString()
		for _, policy := range policyList.Items {
			remotePolicySet.Insert(string(policy.UID))
		}

		for elem := range remotePolicySet {
			if policyIDset.Has(elem) {
				continue
			}
			log.V(1).Info("gc collected", "key", elem)
			r.Service.DeleteSecurityPolicy(types.UID(elem))
		}
		time.Sleep(GCInterval)
	}
}
