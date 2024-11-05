/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkpolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
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
)

// NetworkPolicyReconciler reconciles a NetworkPolicy object
type NetworkPolicyReconciler struct {
	Client   client.Client
	Scheme   *apimachineryruntime.Scheme
	Service  *securitypolicy.SecurityPolicyService
	Recorder record.EventRecorder
}

func updateFail(r *NetworkPolicyReconciler, c context.Context, o *networkingv1.NetworkPolicy, e *error) {
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func deleteFail(r *NetworkPolicyReconciler, c context.Context, o *networkingv1.NetworkPolicy, e *error) {
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
}

func updateSuccess(r *NetworkPolicyReconciler, c context.Context, o *networkingv1.NetworkPolicy) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "NetworkPolicy has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func deleteSuccess(r *NetworkPolicyReconciler, _ context.Context, o *networkingv1.NetworkPolicy) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "NetworkPolicy has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
}

func setNetworkPolicyErrorAnnotation(ctx context.Context, networkPolicy *networkingv1.NetworkPolicy, client client.Client, info string) {
	if networkPolicy.Annotations == nil {
		networkPolicy.Annotations = make(map[string]string)
	}
	if networkPolicy.Annotations[common.NSXOperatorError] == info {
		return
	}
	networkPolicy.Annotations[common.NSXOperatorError] = info
	updateErr := client.Update(ctx, networkPolicy)
	if updateErr != nil {
		log.Error(updateErr, "Failed to update NetworkPolicy with error annotation")
	}
	log.Info("Updated NetworkPolicy with error annotation", "error", info)
}

func cleanNetworkPolicyErrorAnnotation(ctx context.Context, networkPolicy *networkingv1.NetworkPolicy, client client.Client) {
	if networkPolicy.Annotations == nil {
		return
	}
	if _, exists := networkPolicy.Annotations[common.NSXOperatorError]; exists {
		delete(networkPolicy.Annotations, common.NSXOperatorError)
	}
	updateErr := client.Update(ctx, networkPolicy)
	if updateErr != nil {
		log.Error(updateErr, "Failed to clean NetworkPolicy annotation")
	}
	log.Info("Clean NetworkPolicy annotation")
}

func (r *NetworkPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	networkPolicy := &networkingv1.NetworkPolicy{}
	log.Info("Reconciling NetworkPolicy", "networkpolicy", req.NamespacedName)
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling NetworkPolicy", "networkpolicy", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)

	if err := r.Client.Get(ctx, req.NamespacedName, networkPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteNetworkPolicyByName(req.Namespace, req.Name); err != nil {
				log.Error(err, "Failed to delete NetworkPolicy", "networkpolicy", req.NamespacedName)
				return ResultRequeue, err
			}
			return ResultNormal, nil
		}
		// In case that client is unable to check CR
		log.Error(err, "Failed to fetch NetworkPolicy CR", "req", req.NamespacedName)
		return ResultRequeue, err
	}

	if networkPolicy.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)
		log.Info("Reconciling CR to create or update networkPolicy", "networkPolicy", req.NamespacedName)

		if err := r.Service.CreateOrUpdateSecurityPolicy(networkPolicy); err != nil {
			if errors.As(err, &nsxutil.RestrictionError{}) {
				log.Error(err, err.Error(), "networkpolicy", req.NamespacedName)
				setNetworkPolicyErrorAnnotation(ctx, networkPolicy, r.Client, common.ErrorNoDFWLicense)
				updateFail(r, ctx, networkPolicy, &err)
				return ResultNormal, nil
			}
			if nsxutil.IsInvalidLicense(err) {
				log.Error(err, err.Error(), "networkpolicy", req.NamespacedName)
				setNetworkPolicyErrorAnnotation(ctx, networkPolicy, r.Client, common.ErrorNoDFWLicense)
				os.Exit(1)
			}
			log.Error(err, "Failed to create or update, would retry exponentially", "networkpolicy", req.NamespacedName)
			updateFail(r, ctx, networkPolicy, &err)
			return ResultRequeue, err
		}
		updateSuccess(r, ctx, networkPolicy)
		cleanNetworkPolicyErrorAnnotation(ctx, networkPolicy, r.Client)
	} else {
		log.Info("Reconciling CR to delete networkPolicy", "networkPolicy", req.NamespacedName)
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)

		if err := r.Service.DeleteSecurityPolicy(networkPolicy, false, false, servicecommon.ResourceTypeNetworkPolicy); err != nil {
			log.Error(err, "Failed to Delete, would retry exponentially", "networkpolicy", req.NamespacedName)
			deleteFail(r, ctx, networkPolicy, &err)
			return ResultRequeue, err
		}
		deleteSuccess(r, ctx, networkPolicy)
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
	log.Info("NetworkPolicy garbage collector started")
	nsxPolicySet := r.Service.ListNetworkPolicyID()
	if len(nsxPolicySet) == 0 {
		return
	}

	CRPolicySet, err := r.listNetworkPolciyCRIDs()
	if err != nil {
		return
	}

	diffSet := nsxPolicySet.Difference(CRPolicySet)
	for elem := range diffSet {
		log.V(1).Info("GC collected NetworkPolicy", "ID", elem)
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
		err = r.Service.DeleteSecurityPolicy(types.UID(elem), true, false, servicecommon.ResourceTypeNetworkPolicy)
		if err != nil {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
		} else {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
		}
	}
}

func (r *NetworkPolicyReconciler) deleteNetworkPolicyByName(ns, name string) error {
	CRPolicySet, err := r.listNetworkPolciyCRIDs()
	if err != nil {
		return err
	}

	nsxSecurityPolicies := r.Service.ListNetworkPolicyByName(ns, name)
	for _, item := range nsxSecurityPolicies {
		uid := nsxutil.FindTag(item.Tags, servicecommon.TagScopeNetworkPolicyUID)
		if CRPolicySet.Has(uid) {
			log.Info("Skipping deletion, NetworkPolicy CR still exists in K8s", "networkPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
			continue
		}

		log.Info("Deleting NetworkPolicy", "networkPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
		if err := r.Service.DeleteSecurityPolicy(types.UID(uid), false, false, servicecommon.ResourceTypeNetworkPolicy); err != nil {
			log.Error(err, "Failed to delete NetworkPolicy", "networkPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
			return err
		}
		log.Info("Successfully deleted NetworkPolicy", "networkPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
	}
	return nil
}

func (r *NetworkPolicyReconciler) listNetworkPolciyCRIDs() (sets.Set[string], error) {
	networkPolicyList := &networkingv1.NetworkPolicyList{}
	err := r.Client.List(context.Background(), networkPolicyList)
	if err != nil {
		log.Error(err, "Failed to list NetworkPolicy CRs")
		return nil, err
	}

	CRPolicySet := sets.New[string]()
	for _, policy := range networkPolicyList.Items {
		CRPolicySet.Insert(r.Service.BuildNetworkPolicyAllowPolicyID(string(policy.UID)))
		CRPolicySet.Insert(r.Service.BuildNetworkPolicyIsolationPolicyID(string(policy.UID)))
	}
	return CRPolicySet, nil
}

func StartNetworkPolicyController(mgr ctrl.Manager, commonService servicecommon.Service, vpcService servicecommon.VPCServiceProvider) {
	networkPolicyReconcile := NetworkPolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("networkpolicy-controller"),
	}
	networkPolicyReconcile.Service = securitypolicy.GetSecurityService(commonService, vpcService)
	if err := networkPolicyReconcile.Start(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "NetworkPolicy")
		os.Exit(1)
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, networkPolicyReconcile.CollectGarbage)
}
