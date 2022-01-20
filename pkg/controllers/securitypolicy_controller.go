/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	v1 "k8s.io/api/core/v1"
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
			r.setSecurityPolicyReadyStatusFalse(&ctx, obj, &err)
			return ctrl.Result{}, err
		}
		r.setSecurityPolicyReadyStatusTrue(&ctx, obj)
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

func (r *SecurityPolicyReconciler) setSecurityPolicyReadyStatusTrue(ctx *context.Context, sec_policy *v1alpha1.SecurityPolicy) {
	newConditions := []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionTrue,
			Message: "NSX Security Policy has been successfully created/updated",
			Reason:  "NSX API returned 200 response code for PATCH",
		},
	}
	r.updateSecurityPolicyStatusConditions(ctx, sec_policy, newConditions)
}

func (r *SecurityPolicyReconciler) setSecurityPolicyReadyStatusFalse(ctx *context.Context, sec_policy *v1alpha1.SecurityPolicy, err *error) {
	newConditions := []v1alpha1.SecurityPolicyCondition{
		{
			Type:    v1alpha1.SecurityPolicyReady,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason:  fmt.Sprintf("Error occurred while processing the Security Policy CR. Please check the config and try again. Error: %v", *err),
		},
	}
	r.updateSecurityPolicyStatusConditions(ctx, sec_policy, newConditions)
}

func (r *SecurityPolicyReconciler) updateSecurityPolicyStatusConditions(ctx *context.Context, sec_policy *v1alpha1.SecurityPolicy, newConditions []v1alpha1.SecurityPolicyCondition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeSecurityPolicyStatusCondition(ctx, sec_policy, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		r.Client.Status().Update(*ctx, sec_policy)
		log.V(1).Info("Updated Security Policy CRD", "Name", sec_policy.Name, "Namespace", sec_policy.Namespace, "New Conditions", newConditions)
	}
}

func (r *SecurityPolicyReconciler) mergeSecurityPolicyStatusCondition(ctx *context.Context, sec_policy *v1alpha1.SecurityPolicy, newCondition *v1alpha1.SecurityPolicyCondition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, sec_policy.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		sec_policy.Status.Conditions = append(sec_policy.Status.Conditions, *newCondition)
	}
	return true
}

func getExistingConditionOfType(conditionType v1alpha1.SecurityPolicyStatusCondition, existingConditions []v1alpha1.SecurityPolicyCondition) *v1alpha1.SecurityPolicyCondition {
	for i := range existingConditions {
		if existingConditions[i].Type == conditionType {
			return &existingConditions[i]
		}
	}
	return nil
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
