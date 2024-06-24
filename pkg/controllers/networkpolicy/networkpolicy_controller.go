/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkpolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/vmware-tanzu/net-operator-api/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/vpcnetwork"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log                     = &logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResType           = common.MetricResTypeNetworkPolicy
	once                    sync.Once
)

// NetworkPolicyReconciler reconciles a NetworkPolicy object
type NetworkPolicyReconciler struct {
	Client          client.Client
	Scheme          *apimachineryruntime.Scheme
	Service         *securitypolicy.SecurityPolicyService
	Recorder        record.EventRecorder
	NetworkProvider vpcnetwork.VPCNetworkProvider
}

func updateFail(r *NetworkPolicyReconciler, c *context.Context, o *networkingv1.NetworkPolicy, e *error) {
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func deleteFail(r *NetworkPolicyReconciler, c *context.Context, o *networkingv1.NetworkPolicy, e *error) {
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
}

func updateSuccess(r *NetworkPolicyReconciler, c *context.Context, o *networkingv1.NetworkPolicy) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "NetworkPolicy has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func deleteSuccess(r *NetworkPolicyReconciler, _ *context.Context, o *networkingv1.NetworkPolicy) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "NetworkPolicy has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
}

func (r *NetworkPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Use once.Do to ensure gc is called only once
	common.GcOnce(r, &once)
	if r.NetworkProvider != nil {
		return r.NetworkProvider.ReconcileWithVPCFilters("networkpolicy", ctx, req, r.reconcile)
	}
	return r.reconcile(ctx, req)
}

func (r *NetworkPolicyReconciler) reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	networkPolicy := &networkingv1.NetworkPolicy{}
	log.Info("reconciling networkpolicy", "networkpolicy", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)

	if err := r.Client.Get(ctx, req.NamespacedName, networkPolicy); err != nil {
		log.Error(err, "unable to fetch network policy", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	if networkPolicy.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)
		if !controllerutil.ContainsFinalizer(networkPolicy, servicecommon.NetworkPolicyFinalizerName) {
			controllerutil.AddFinalizer(networkPolicy, servicecommon.NetworkPolicyFinalizerName)
			if err := r.Client.Update(ctx, networkPolicy); err != nil {
				log.Error(err, "add finalizer", "networkpolicy", req.NamespacedName)
				updateFail(r, &ctx, networkPolicy, &err)
				return ResultRequeue, err
			}
			log.V(1).Info("added finalizer on networkpolicy", "networkpolicy", req.NamespacedName)
		}

		if err := r.Service.CreateOrUpdateSecurityPolicy(networkPolicy); err != nil {
			if errors.As(err, &nsxutil.RestrictionError{}) {
				log.Error(err, err.Error(), "networkpolicy", req.NamespacedName)
				updateFail(r, &ctx, networkPolicy, &err)
				return ResultNormal, nil
			}
			log.Error(err, "create or update failed, would retry exponentially", "networkpolicy", req.NamespacedName)
			updateFail(r, &ctx, networkPolicy, &err)
			return ResultRequeue, err
		}
		updateSuccess(r, &ctx, networkPolicy)
	} else {
		if controllerutil.ContainsFinalizer(networkPolicy, servicecommon.NetworkPolicyFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
			if err := r.Service.DeleteSecurityPolicy(networkPolicy, false, servicecommon.ResourceTypeNetworkPolicy); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "networkpolicy", req.NamespacedName)
				deleteFail(r, &ctx, networkPolicy, &err)
				return ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(networkPolicy, servicecommon.NetworkPolicyFinalizerName)
			if err := r.Client.Update(ctx, networkPolicy); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "networkpolicy", req.NamespacedName)
				deleteFail(r, &ctx, networkPolicy, &err)
				return ResultRequeue, err
			}
			log.V(1).Info("removed finalizer", "networkpolicy", req.NamespacedName)
			deleteSuccess(r, &ctx, networkPolicy)
		} else {
			// only print a message because it's not a normal case
			log.Info("finalizers cannot be recognized", "networkpolicy", req.NamespacedName)
		}
	}

	return ResultNormal, nil
}

func (r *NetworkPolicyReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.NetworkPolicy{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Watches(
			&v1alpha1.Network{},
			&vpcnetwork.EnqueueRequestForNetwork{Client: r.Client, Lister: r.listNetworkPolicies},
			builder.WithPredicates(vpcnetwork.PredicateFuncsByNetwork),
		).
		Complete(r)
}

// Start setup manager and launch GC
func (r *NetworkPolicyReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

// CollectGarbage  collect networkpolicy which has been removed from K8s.
// it implements the interface GarbageCollector method.
func (r *NetworkPolicyReconciler) CollectGarbage(ctx context.Context) {
	log.Info("networkpolicy garbage collector started")
	nsxPolicySet := r.Service.ListNetworkPolicyID()
	if len(nsxPolicySet) == 0 {
		return
	}
	policyList := &networkingv1.NetworkPolicyList{}
	err := r.Client.List(ctx, policyList)
	if err != nil {
		log.Error(err, "failed to list NetworkPolicy")
		return
	}

	CRPolicySet := sets.New[string]()
	for _, policy := range policyList.Items {
		CRPolicySet.Insert(r.Service.BuildNetworkPolicyAllowPolicyID(string(policy.UID)))
		CRPolicySet.Insert(r.Service.BuildNetworkPolicyIsolationPolicyID(string(policy.UID)))
	}

	diffSet := nsxPolicySet.Difference(CRPolicySet)
	for elem := range diffSet {
		log.V(1).Info("GC collected NetworkPolicy", "ID", elem)
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
		err = r.Service.DeleteSecurityPolicy(types.UID(elem), false, servicecommon.ResourceTypeNetworkPolicy)
		if err != nil {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
		} else {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
		}
	}
}

func (r *NetworkPolicyReconciler) listNetworkPolicies(ns string) ([]types.NamespacedName, error) {
	npList := &networkingv1.NetworkPolicyList{}
	err := r.Client.List(context.Background(), npList, client.InNamespace(ns))
	if err != nil {
		return nil, err
	}
	nsNames := make([]types.NamespacedName, 0)
	for _, np := range npList.Items {
		nsNames = append(nsNames, types.NamespacedName{
			Namespace: np.Namespace,
			Name:      np.Name,
		})
	}
	return nsNames, nil
}

func StartNetworkPolicyController(mgr ctrl.Manager, commonService servicecommon.Service, vpcService servicecommon.VPCServiceProvider, networkProvider vpcnetwork.VPCNetworkProvider) {
	networkPolicyReconcile := NetworkPolicyReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Recorder:        mgr.GetEventRecorderFor("networkpolicy-controller"),
		NetworkProvider: networkProvider,
	}
	networkPolicyReconcile.Service = securitypolicy.GetSecurityService(commonService, vpcService)
	if err := networkPolicyReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "NetworkPolicy")
		os.Exit(1)
	}
}
