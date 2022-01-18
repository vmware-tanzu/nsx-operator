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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log = logf.Log.WithName("controller").WithName("securitypolicy")
)

// SecurityPolicyReconciler reconciles a SecurityPolicy object
type SecurityPolicyReconciler struct {
	Client  client.Client
	Scheme  *runtime.Scheme
	Service *services.SecurityPolicyService
}

func (r *SecurityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.SecurityPolicy{}
	log.Info("reconciling securitypolicy CR", "securitypolicy", req.NamespacedName)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch security policy CR")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(obj, util.FinalizerName) {
			controllerutil.AddFinalizer(obj, util.FinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				return ctrl.Result{}, err
			}
		}
		if err := r.Service.CreateOrUpdateSecurityPolicy(obj); err != nil {
			log.Error(err, "failed to create or update security policy CR", "securitypolicy", req.NamespacedName)
			return ctrl.Result{}, err
		}
	} else {
		if containsString(obj.GetFinalizers(), util.FinalizerName) {
			if err := r.Service.DeleteSecurityPolicy(obj.UID); err != nil {
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(obj, util.FinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func containsString(source []string, target string) bool {
	for _, item := range source {
		if item == target {
			return true
		}
	}
	return false
}

func (r *SecurityPolicyReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityPolicy{}).
		Complete(r)
}

// Start setup manager and launch GC
func (r *SecurityPolicyReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}

	go r.GarbageCollector()
	return nil
}

func (r *SecurityPolicyReconciler) GarbageCollector() {
	ctx := context.Background()
	log.V(1).Info("garbage collector started")
	for {
		time.Sleep(util.GCInterval)
		nsxPolicySet := r.Service.ListSecurityPolicy()
		if len(nsxPolicySet) == 0 {
			continue
		}
		policyList := &v1alpha1.SecurityPolicyList{}
		err := r.Client.List(ctx, policyList)
		if err != nil {
			log.Error(err, "failed to list security policy CR")
			continue
		}

		CRPolicySet := sets.NewString()
		for _, policy := range policyList.Items {
			CRPolicySet.Insert(string(policy.UID))
		}

		for elem := range nsxPolicySet {
			if CRPolicySet.Has(elem) {
				continue
			}
			log.V(1).Info("GC collected SecurityPolicy CR", "UID", elem)
			r.Service.DeleteSecurityPolicy(types.UID(elem))
		}
	}
}
