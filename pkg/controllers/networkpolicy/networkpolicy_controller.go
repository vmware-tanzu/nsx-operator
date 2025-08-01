/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkpolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	stderrors "github.com/vmware/vsphere-automation-sdk-go/lib/vapi/std/errors"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
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
	Client        client.Client
	Scheme        *apimachineryruntime.Scheme
	Service       *securitypolicy.SecurityPolicyService
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater
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

func cleanNetworkPolicyErrorAnnotation(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, args ...interface{}) {
	networkPolicy := obj.(*networkingv1.NetworkPolicy)
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

	r.StatusUpdater.IncreaseSyncTotal()

	if err := r.Client.Get(ctx, req.NamespacedName, networkPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteNetworkPolicyByName(req.Namespace, req.Name); err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return ResultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return ResultNormal, nil
		}
		// In case that client is unable to check CR
		log.Error(err, "Failed to fetch NetworkPolicy CR", "req", req.NamespacedName)
		return ResultRequeue, err
	}

	if networkPolicy.ObjectMeta.DeletionTimestamp.IsZero() {
		r.StatusUpdater.IncreaseUpdateTotal()
		log.Info("Reconciling CR to create or update networkPolicy", "networkPolicy", req.NamespacedName)

		if err := r.Service.CreateOrUpdateSecurityPolicy(networkPolicy); err != nil {
			if errors.As(err, &nsxutil.RestrictionError{}) {
				setNetworkPolicyErrorAnnotation(ctx, networkPolicy, r.Client, common.ErrorNoDFWLicense)
				r.StatusUpdater.UpdateFail(ctx, networkPolicy, err, "", nil)
				return ResultNormal, nil
			}
			if nsxutil.IsInvalidLicense(err) {
				log.Error(err, err.Error(), "networkpolicy", req.NamespacedName)
				setNetworkPolicyErrorAnnotation(ctx, networkPolicy, r.Client, common.ErrorNoDFWLicense)
				os.Exit(1)
			}
			r.StatusUpdater.UpdateFail(ctx, networkPolicy, err, "", clarifyAndSetNetworkPolicyErrorAnnotation)
			return ResultRequeue, err
		}
		r.StatusUpdater.UpdateSuccess(ctx, networkPolicy, cleanNetworkPolicyErrorAnnotation)
	} else {
		log.Info("Reconciling CR to delete networkPolicy", "networkPolicy", req.NamespacedName)
		r.StatusUpdater.IncreaseDeleteTotal()
		if err := r.deleteNetworkPolicyByName(req.Namespace, req.Name); err != nil {
			r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
			return ResultRequeue, err
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
	}

	return ResultNormal, nil
}

func (r *NetworkPolicyReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.NetworkPolicy{}).
		Watches(
			&v1.Pod{},
			&EnqueueRequestForPod{
				Client:                  mgr.GetClient(),
				NetworkPolicyReconciler: r,
			},
			builder.WithPredicates(PredicateFuncsPod),
		).
		Watches(
			&v1.Namespace{},
			&EnqueueRequestForNamespace{
				Client:                  mgr.GetClient(),
				NetworkPolicyReconciler: r,
			},
			builder.WithPredicates(PredicateFuncsNs),
		).
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
func (r *NetworkPolicyReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("NetworkPolicy garbage collector started")
	nsxPolicySet := r.Service.ListNetworkPolicyID()
	if len(nsxPolicySet) == 0 {
		return nil
	}

	CRPolicySet, err := r.listNetworkPolicyCRIDs()
	if err != nil {
		return err
	}

	var errList []error
	diffSet := nsxPolicySet.Difference(CRPolicySet)
	for elem := range diffSet {
		log.V(1).Info("GC collected NetworkPolicy", "ID", elem)
		r.StatusUpdater.IncreaseDeleteTotal()
		err = r.Service.DeleteSecurityPolicy(types.UID(elem), true, servicecommon.ResourceTypeNetworkPolicy)
		if err != nil {
			errList = append(errList, err)
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("errors found in NetworkPolicy garbage collection: %s", errList)
	}
	return nil
}

func (r *NetworkPolicyReconciler) deleteNetworkPolicyByName(ns, name string) error {
	nsxSecurityPolicies := r.Service.ListNetworkPolicyByName(ns, name)
	for _, item := range nsxSecurityPolicies {
		uid := nsxutil.FindTag(item.Tags, servicecommon.TagScopeNetworkPolicyUID)
		log.Info("Deleting NetworkPolicy", "networkPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
		if err := r.Service.DeleteSecurityPolicy(types.UID(uid), false, servicecommon.ResourceTypeNetworkPolicy); err != nil {
			log.Error(err, "Failed to delete NetworkPolicy", "networkPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
			return err
		}
		log.Info("Successfully deleted NetworkPolicy", "networkPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
	}
	return nil
}

func (r *NetworkPolicyReconciler) listNetworkPolicyCRIDs() (sets.Set[string], error) {
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

func (r *NetworkPolicyReconciler) RestoreReconcile() error {
	return nil
}

func (r *NetworkPolicyReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.Start(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "NetworkPolicy")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

// reconcileNetworkPolicy is triggered by Pod events or Namespace events to reconcile NetworkPolicies with named ports
func reconcileNetworkPolicy(pkgClient client.Client, q workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
	npList := &networkingv1.NetworkPolicyList{}
	err := pkgClient.List(context.Background(), npList)
	if err != nil {
		log.Error(err, "Failed to list all the network policies")
		return err
	}

	for i := range npList.Items {
		np := &npList.Items[i]
		shouldReconcile := false

		// Check ingress rules for named ports
		for _, ingress := range np.Spec.Ingress {
			for _, port := range ingress.Ports {
				if port.Port != nil && port.Port.Type == intstr.String {
					shouldReconcile = true
					break
				}
			}
			if shouldReconcile {
				break
			}
		}

		// Check egress rules for named ports
		if !shouldReconcile {
			for _, egress := range np.Spec.Egress {
				for _, port := range egress.Ports {
					if port.Port != nil && port.Port.Type == intstr.String {
						shouldReconcile = true
					}
				}
				if shouldReconcile {
					break
				}
			}
		}

		if shouldReconcile {
			log.Info("Reconcile network policy because of associated pod change",
				"namespace", np.Namespace, "name", np.Name)
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      np.Name,
					Namespace: np.Namespace,
				},
			})
		}
	}
	return nil
}

func NewNetworkPolicyReconciler(mgr ctrl.Manager, commonService servicecommon.Service, vpcService servicecommon.VPCServiceProvider) *NetworkPolicyReconciler {
	networkPolicyReconcile := &NetworkPolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("networkpolicy-controller"),
	}
	networkPolicyReconcile.Service = securitypolicy.GetSecurityService(commonService, vpcService)
	networkPolicyReconcile.StatusUpdater = common.NewStatusUpdater(networkPolicyReconcile.Client, networkPolicyReconcile.Service.NSXConfig, networkPolicyReconcile.Recorder, MetricResType, "NetworkPolicy", "NetworkPolicy")
	return networkPolicyReconcile
}

func clarifyAndSetNetworkPolicyErrorAnnotation(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, args ...interface{}) {
	var nsxErr *nsxutil.NSXApiError
	var validationErr *nsxutil.ValidationError
	annotationError := common.ErrorNetworkPolicyUpdateFailed
	if errors.As(err, &validationErr) {
		annotationError = common.ErrorNetworkPolicyValidationFailed
	} else if errors.As(err, &nsxErr) && nsxErr != nil && (nsxErr.ErrorTypeEnum == stderrors.ErrorType_SERVICE_UNAVAILABLE || nsxErr.ErrorTypeEnum == stderrors.ErrorType_TIMED_OUT || nsxErr.ErrorTypeEnum == stderrors.ErrorType_INTERNAL_SERVER_ERROR) {
		annotationError = common.ErrorNetworkPolicyUpdatePending
	}
	log.Info("Setting NetworkPolicy annotation for error", "error", annotationError, "networkPolicy", obj.GetName(), "namespace", obj.GetNamespace())
	setNetworkPolicyErrorAnnotation(ctx, obj.(*networkingv1.NetworkPolicy), client, annotationError)
}
