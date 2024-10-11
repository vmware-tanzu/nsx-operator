/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"

	v1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	crdv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                     = &logger.Log
	ResultNormal            = common.ResultNormal
	ResultRequeue           = common.ResultRequeue
	ResultRequeueAfter5mins = common.ResultRequeueAfter5mins
	MetricResType           = common.MetricResTypeSecurityPolicy
)

// SecurityPolicyReconciler SecurityPolicyReconcile reconciles a SecurityPolicy object
type SecurityPolicyReconciler struct {
	Client   client.Client
	Scheme   *apimachineryruntime.Scheme
	Service  *securitypolicy.SecurityPolicyService
	Recorder record.EventRecorder
}

func updateFail(r *SecurityPolicyReconciler, c context.Context, o *v1alpha1.SecurityPolicy, e *error) {
	r.setSecurityPolicyReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResType)
}

func k8sClient(mgr ctrl.Manager) client.Client {
	var c client.Client
	if mgr != nil {
		c = mgr.GetClient()
	}
	return c
}

func deleteFail(r *SecurityPolicyReconciler, c context.Context, o *v1alpha1.SecurityPolicy, e *error) {
	r.setSecurityPolicyReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
}

func updateSuccess(r *SecurityPolicyReconciler, c context.Context, o *v1alpha1.SecurityPolicy) {
	r.setSecurityPolicyReadyStatusTrue(c, o, metav1.Now())
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "SecurityPolicy CR has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func deleteSuccess(r *SecurityPolicyReconciler, _ context.Context, o *v1alpha1.SecurityPolicy) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "SecurityPolicy CR has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
}

func setSecurityPolicyErrorAnnotation(ctx context.Context, securityPolicy *v1alpha1.SecurityPolicy, isVPCEanbled bool, client client.Client, info string) {
	if securityPolicy.Annotations == nil {
		securityPolicy.Annotations = make(map[string]string)
	}
	if securityPolicy.Annotations[common.NSXOperatorError] == info {
		return
	}
	securityPolicy.Annotations[common.NSXOperatorError] = info

	var updateErr error
	if isVPCEanbled {
		finalObj := securitypolicy.T1ToVPC(securityPolicy)
		updateErr = client.Update(ctx, finalObj)
	} else {
		updateErr = client.Update(ctx, securityPolicy)
	}
	if updateErr != nil {
		log.Error(updateErr, "Failed to update SecurityPolicy with error annotation")
	}
	log.Info("update SecurityPolicy with error annotation", "error", info)
}

func cleanSecurityPolicyErrorAnnotation(ctx context.Context, securityPolicy *v1alpha1.SecurityPolicy, isVPCEanbled bool, client client.Client) {
	if securityPolicy.Annotations == nil {
		return
	}
	if _, exists := securityPolicy.Annotations[common.NSXOperatorError]; exists {
		delete(securityPolicy.Annotations, common.NSXOperatorError)
	}

	var updateErr error
	if isVPCEanbled {
		finalObj := securitypolicy.T1ToVPC(securityPolicy)
		updateErr = client.Update(ctx, finalObj)
	} else {
		updateErr = client.Update(ctx, securityPolicy)
	}
	if updateErr != nil {
		log.Error(updateErr, "Failed to clean SecurityPolicy annotation")
	}
	log.Info("clean SecurityPolicy annotation")
}

func (r *SecurityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var obj client.Object
	if securitypolicy.IsVPCEnabled(r.Service) {
		obj = &crdv1alpha1.SecurityPolicy{}
	} else {
		obj = &v1alpha1.SecurityPolicy{}
	}

	log.Info("reconciling SecurityPolicy CR", "securitypolicy", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		// IgnoreNotFound returns nil on NotFound errors.
		if client.IgnoreNotFound(err) == nil {
			if err := r.deleteSecurityPolicyByName(req.Namespace, req.Name); err != nil {
				log.Error(err, "failed to delete SecurityPolicy", "securitypolicy", req.NamespacedName)
				return ResultRequeue, err
			}
			return ResultNormal, nil
		}
		// In case that client is unable to check CR
		log.Error(err, "client is unable to fetch SecurityPolicy CR", "req", req.NamespacedName)
		return ResultRequeue, err
	}

	isZero := false
	finalizerName := servicecommon.T1SecurityPolicyFinalizerName
	realObj := &v1alpha1.SecurityPolicy{}
	switch obj.(type) {
	case *crdv1alpha1.SecurityPolicy:
		o := obj.(*crdv1alpha1.SecurityPolicy)
		isZero = o.ObjectMeta.DeletionTimestamp.IsZero()
		realObj = securitypolicy.VPCToT1(o)
	case *v1alpha1.SecurityPolicy:
		realObj = obj.(*v1alpha1.SecurityPolicy)
		isZero = realObj.ObjectMeta.DeletionTimestamp.IsZero()
	}

	// Since SecurityPolicy service can only be activated from NSX 3.2.0 onwards,
	// So need to check NSX version before starting SecurityPolicy reconcile
	if !r.Service.NSXClient.NSXCheckVersion(nsx.SecurityPolicy) {
		err := errors.New("NSX version check failed, SecurityPolicy feature is not supported")
		updateFail(r, ctx, realObj, &err)
		// if NSX version check fails, it will be put back to reconcile queue and be reconciled after 5 minutes
		return ResultRequeueAfter5mins, nil
	}

	if isZero {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)

		if isCRInSysNs, err := util.IsSystemNamespace(r.Client, req.Namespace, nil); err != nil {
			err = errors.New("fetch namespace associated with security policy CR failed")
			log.Error(err, "would retry exponentially", "securitypolicy", req.NamespacedName)
			updateFail(r, ctx, realObj, &err)
			return ResultRequeue, err
		} else if isCRInSysNs {
			err = errors.New("security Policy CR cannot be created in System Namespace")
			log.Error(err, "", "securitypolicy", req.NamespacedName)
			updateFail(r, ctx, realObj, &err)
			return ResultNormal, nil
		}

		log.Info("reconciling CR to create or update securitypolicy", "securitypolicy", req.NamespacedName)
		if err := r.Service.CreateOrUpdateSecurityPolicy(realObj); err != nil {
			if errors.As(err, &nsxutil.RestrictionError{}) {
				log.Error(err, err.Error(), "securitypolicy", req.NamespacedName)
				setSecurityPolicyErrorAnnotation(ctx, realObj, securitypolicy.IsVPCEnabled(r.Service), r.Client, common.ErrorNoDFWLicense)
				updateFail(r, ctx, realObj, &err)
				return ResultNormal, nil
			}
			if nsxutil.IsInvalidLicense(err) {
				log.Error(err, err.Error(), "securitypolicy", req.NamespacedName)
				setSecurityPolicyErrorAnnotation(ctx, realObj, securitypolicy.IsVPCEnabled(r.Service), r.Client, common.ErrorNoDFWLicense)
				os.Exit(1)
			}
			log.Error(err, "create or update failed, would retry exponentially", "securitypolicy", req.NamespacedName)
			updateFail(r, ctx, realObj, &err)
			return ResultRequeue, err
		}
		updateSuccess(r, ctx, realObj)
		cleanSecurityPolicyErrorAnnotation(ctx, realObj, securitypolicy.IsVPCEnabled(r.Service), r.Client)
	} else {
		log.Info("reconciling CR to delete securitypolicy", "securitypolicy", req.NamespacedName)
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)

		// For T1 upgrade, the upgraded CRs still has finalizer
		if controllerutil.ContainsFinalizer(obj, finalizerName) {
			controllerutil.RemoveFinalizer(obj, finalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "finalizer remove failed, would retry exponentially", "securitypolicy", req.NamespacedName)
				deleteFail(r, ctx, realObj, &err)
				return ResultRequeue, err
			}
			log.V(1).Info("removed finalizer", "securitypolicy", req.NamespacedName)
		}
		if err := r.Service.DeleteSecurityPolicy(realObj.UID, false, false, servicecommon.ResourceTypeSecurityPolicy); err != nil {
			log.Error(err, "deletion failed, would retry exponentially", "securitypolicy", req.NamespacedName)
			deleteFail(r, ctx, realObj, &err)
			return ResultRequeue, err
		}
		deleteSuccess(r, ctx, realObj)
	}

	return ResultNormal, nil
}

func (r *SecurityPolicyReconciler) setSecurityPolicyReadyStatusTrue(ctx context.Context, secPolicy *v1alpha1.SecurityPolicy, transitionTime metav1.Time) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX Security Policy has been successfully created/updated",
			Reason:             "SecurityPolicyReady",
			LastTransitionTime: transitionTime,
		},
	}
	r.updateSecurityPolicyStatusConditions(ctx, secPolicy, newConditions)
}

func (r *SecurityPolicyReconciler) setSecurityPolicyReadyStatusFalse(ctx context.Context, secPolicy *v1alpha1.SecurityPolicy, transitionTime metav1.Time, err *error) {
	newConditions := []v1alpha1.Condition{
		{
			Type:   v1alpha1.Ready,
			Status: v1.ConditionFalse,
			Message: fmt.Sprintf(
				"error occurred while processing the SecurityPolicy CR. Error: %v",
				*err,
			),
			Reason:             "SecurityPolicyNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	r.updateSecurityPolicyStatusConditions(ctx, secPolicy, newConditions)
}

func (r *SecurityPolicyReconciler) updateSecurityPolicyStatusConditions(ctx context.Context, secPolicy *v1alpha1.SecurityPolicy, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeSecurityPolicyStatusCondition(ctx, secPolicy, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		if securitypolicy.IsVPCEnabled(r.Service) {
			finalObj := securitypolicy.T1ToVPC(secPolicy)
			err := r.Client.Status().Update(ctx, finalObj)
			if err != nil {
				log.Error(err, "")
			}
		} else {
			err := r.Client.Status().Update(ctx, secPolicy)
			if err != nil {
				log.Error(err, "")
			}
		}
		log.V(1).Info("updated SecurityPolicy", "Name", secPolicy.Name, "Namespace", secPolicy.Namespace,
			"New Conditions", newConditions)
	}
}

func (r *SecurityPolicyReconciler) mergeSecurityPolicyStatusCondition(ctx context.Context, secPolicy *v1alpha1.SecurityPolicy, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, secPolicy.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		secPolicy.Status.Conditions = append(secPolicy.Status.Conditions, *newCondition)
	}
	return true
}

func getExistingConditionOfType(conditionType v1alpha1.ConditionType, existingConditions []v1alpha1.Condition) *v1alpha1.Condition {
	for i := range existingConditions {
		if existingConditions[i].Type == conditionType {
			return &existingConditions[i]
		}
	}
	return nil
}

func (r *SecurityPolicyReconciler) setupWithManager(mgr ctrl.Manager) error {
	var blr *builder.Builder
	if securitypolicy.IsVPCEnabled(r.Service) {
		blr = ctrl.NewControllerManagedBy(mgr).For(&crdv1alpha1.SecurityPolicy{})
	} else {
		blr = ctrl.NewControllerManagedBy(mgr).For(&v1alpha1.SecurityPolicy{})
	}
	return blr.
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Watches(
			&v1.Namespace{},
			&EnqueueRequestForNamespace{Client: k8sClient(mgr), SecurityPolicyReconciler: r},
			builder.WithPredicates(PredicateFuncsNs),
		).
		Watches(
			&v1.Pod{},
			&EnqueueRequestForPod{Client: k8sClient(mgr), SecurityPolicyReconciler: r},
			builder.WithPredicates(PredicateFuncsPod),
		).
		Complete(r)
}

// Start setup manager and launch GC
func (r *SecurityPolicyReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

// CollectGarbage collect securitypolicy which has been removed from k8s,
// it implements the interface GarbageCollector method.
func (r *SecurityPolicyReconciler) CollectGarbage(ctx context.Context) {
	log.Info("security policy garbage collector started")
	nsxPolicySet := r.Service.ListSecurityPolicyID()
	if len(nsxPolicySet) == 0 {
		return
	}

	CRPolicySet, err := r.listSecurityPolciyCRIDs()
	if err != nil {
		return
	}

	diffSet := nsxPolicySet.Difference(CRPolicySet)
	for elem := range diffSet {
		log.V(1).Info("GC collected SecurityPolicy CR", "securityPolicyUID", elem)
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
		err = r.Service.DeleteSecurityPolicy(types.UID(elem), true, false, servicecommon.ResourceTypeSecurityPolicy)
		if err != nil {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
		} else {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
		}
	}
}

func (r *SecurityPolicyReconciler) deleteSecurityPolicyByName(ns, name string) error {
	nsxSecurityPolicies := r.Service.ListSecurityPolicyByName(ns, name)

	CRPolicySet, err := r.listSecurityPolciyCRIDs()
	if err != nil {
		return err
	}
	for _, item := range nsxSecurityPolicies {
		uid := nsxutil.FindTag(item.Tags, servicecommon.TagValueScopeSecurityPolicyUID)
		if CRPolicySet.Has(uid) {
			log.Info("skipping deletion, SecurityPolicy CR still exists in K8s", "securityPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
			continue
		}

		log.Info("deleting SecurityPolicy", "securityPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
		if err := r.Service.DeleteSecurityPolicy(types.UID(uid), false, false, servicecommon.ResourceTypeSecurityPolicy); err != nil {
			log.Error(err, "failed to delete SecurityPolicy", "securityPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
			return err
		}
		log.Info("successfully deleted SecurityPolicy", "securityPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
	}
	return nil
}

func (r *SecurityPolicyReconciler) listSecurityPolciyCRIDs() (sets.Set[string], error) {
	var objectList client.ObjectList
	if securitypolicy.IsVPCEnabled(r.Service) {
		objectList = &crdv1alpha1.SecurityPolicyList{}
	} else {
		objectList = &v1alpha1.SecurityPolicyList{}
	}
	err := r.Client.List(context.Background(), objectList)
	if err != nil {
		log.Error(err, "failed to list SecurityPolicy CR")
		return nil, err
	}

	CRPolicySet := sets.New[string]()
	switch objectList.(type) {
	case *crdv1alpha1.SecurityPolicyList:
		o := objectList.(*crdv1alpha1.SecurityPolicyList)
		for _, policy := range o.Items {
			CRPolicySet.Insert(string(policy.UID))
		}
	case *v1alpha1.SecurityPolicyList:
		o := objectList.(*v1alpha1.SecurityPolicyList)
		for _, policy := range o.Items {
			CRPolicySet.Insert(string(policy.UID))
		}
	}

	return CRPolicySet, nil
}

// It is triggered by associated controller like pod, namespace, etc.
func reconcileSecurityPolicy(r *SecurityPolicyReconciler, pkgclient client.Client, pods []v1.Pod, q workqueue.RateLimitingInterface) error {
	podPortNames := getAllPodPortNames(pods)
	log.V(1).Info("pod named port", "podPortNames", podPortNames)
	var spList client.ObjectList
	if securitypolicy.IsVPCEnabled(r.Service) {
		spList = &crdv1alpha1.SecurityPolicyList{}
	} else {
		spList = &v1alpha1.SecurityPolicyList{}
	}
	err := pkgclient.List(context.Background(), spList)
	if err != nil {
		log.Error(err, "failed to list all the security policy")
		return err
	}

	// find the security policy that needs
	switch spList.(type) {
	case *crdv1alpha1.SecurityPolicyList:
		o := spList.(*crdv1alpha1.SecurityPolicyList)
		for i := 0; i < len(o.Items); i++ {
			realObj := securitypolicy.VPCToT1(&o.Items[i])
			shouldReconcile(realObj, q, podPortNames)
		}
	case *v1alpha1.SecurityPolicyList:
		o := spList.(*v1alpha1.SecurityPolicyList)
		for i := 0; i < len(o.Items); i++ {
			shouldReconcile(&o.Items[i], q, podPortNames)
		}
	}
	return nil
}

func shouldReconcile(securityPolicy *v1alpha1.SecurityPolicy, q workqueue.RateLimitingInterface, podPortNames sets.Set[string]) {
	shouldReconcile := false
	for _, rule := range securityPolicy.Spec.Rules {
		for _, port := range rule.Ports {
			if port.Port.Type == intstr.String {
				if podPortNames.Has(port.Port.StrVal) {
					shouldReconcile = true
					break
				}
			}
		}
		if shouldReconcile {
			break
		}
	}
	if shouldReconcile {
		log.Info("reconcile security policy because of associated resource change",
			"namespace", securityPolicy.Namespace, "name", securityPolicy.Name)
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      securityPolicy.Name,
				Namespace: securityPolicy.Namespace,
			},
		})
	}
}

func StartSecurityPolicyController(mgr ctrl.Manager, commonService servicecommon.Service, vpcService servicecommon.VPCServiceProvider) {
	securityPolicyReconcile := SecurityPolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("securitypolicy-controller"),
	}
	securityPolicyReconcile.Service = securitypolicy.GetSecurityService(commonService, vpcService)
	if err := securityPolicyReconcile.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "SecurityPolicy")
		os.Exit(1)
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, securityPolicyReconcile.CollectGarbage)
}
