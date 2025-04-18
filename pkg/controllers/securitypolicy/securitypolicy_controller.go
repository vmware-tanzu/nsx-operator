/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package securitypolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	v1 "k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	crdv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	_ "github.com/vmware-tanzu/nsx-operator/pkg/nsx/ratelimiter"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log                         = &logger.Log
	ResultNormal                = common.ResultNormal
	ResultRequeue               = common.ResultRequeue
	ResultRequeueAfter5mins     = common.ResultRequeueAfter5mins
	MetricResTypeSecurityPolicy = common.MetricResTypeSecurityPolicy
)

// SecurityPolicyReconciler SecurityPolicyReconcile reconciles a SecurityPolicy object
type SecurityPolicyReconciler struct {
	Client        client.Client
	Scheme        *apimachineryruntime.Scheme
	Service       *securitypolicy.SecurityPolicyService
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater
}

func k8sClient(mgr ctrl.Manager) client.Client {
	var c client.Client
	if mgr != nil {
		c = mgr.GetClient()
	}
	return c
}

func setSecurityPolicyErrorAnnotation(ctx context.Context, securityPolicy *v1alpha1.SecurityPolicy, isVPCEnabled bool, client client.Client, info string) {
	if securityPolicy.Annotations == nil {
		securityPolicy.Annotations = make(map[string]string)
	}
	if securityPolicy.Annotations[common.NSXOperatorError] == info {
		return
	}
	securityPolicy.Annotations[common.NSXOperatorError] = info

	var updateErr error
	if isVPCEnabled {
		finalObj := securitypolicy.T1ToVPC(securityPolicy)
		updateErr = client.Update(ctx, finalObj)
	} else {
		updateErr = client.Update(ctx, securityPolicy)
	}
	if updateErr != nil {
		log.Error(updateErr, "Failed to update SecurityPolicy with error annotation")
	}
	log.Info("Updated SecurityPolicy with error annotation", "error", info)
}

func cleanSecurityPolicyErrorAnnotation(ctx context.Context, securityPolicy *v1alpha1.SecurityPolicy, isVPCEnabled bool, client client.Client) {
	if securityPolicy.Annotations == nil {
		return
	}
	if _, exists := securityPolicy.Annotations[common.NSXOperatorError]; exists {
		delete(securityPolicy.Annotations, common.NSXOperatorError)
	}

	var updateErr error
	if isVPCEnabled {
		finalObj := securitypolicy.T1ToVPC(securityPolicy)
		updateErr = client.Update(ctx, finalObj)
	} else {
		updateErr = client.Update(ctx, securityPolicy)
	}
	if updateErr != nil {
		log.Error(updateErr, "Failed to clean SecurityPolicy annotation")
	}
	log.Info("Clean SecurityPolicy annotation")
}

func (r *SecurityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var obj client.Object
	if securitypolicy.IsVPCEnabled(r.Service) {
		obj = &crdv1alpha1.SecurityPolicy{}
	} else {
		obj = &v1alpha1.SecurityPolicy{}
	}

	log.Info("Reconciling SecurityPolicy CR", "securitypolicy", req.NamespacedName)
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling SecurityPolicy CR", "securitypolicy", req.NamespacedName, "duration(ms)", time.Since(startTime).Milliseconds())
	}()

	r.StatusUpdater.IncreaseSyncTotal()

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteSecurityPolicyByName(req.Namespace, req.Name); err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return ResultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return ResultNormal, nil
		}
		// In case that client is unable to check CR
		log.Error(err, "Failed to fetch SecurityPolicy CR", "req", req.NamespacedName)
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
		r.StatusUpdater.UpdateFail(ctx, realObj, err, "", setSecurityPolicyReadyStatusFalse, r.Service)
		// if NSX version check fails, it will be put back to reconcile queue and be reconciled after 5 minutes
		return ResultRequeueAfter5mins, nil
	}

	if isZero {
		r.StatusUpdater.IncreaseUpdateTotal()

		if isCRInSysNs, err := util.IsSystemNamespace(r.Client, req.Namespace, nil); err != nil {
			err = errors.New("fetch namespace associated with security policy CR failed")
			r.StatusUpdater.UpdateFail(ctx, realObj, err, "", setSecurityPolicyReadyStatusFalse, r.Service)
			return ResultRequeue, err
		} else if isCRInSysNs {
			err = errors.New("security Policy CR cannot be created in System Namespace")
			r.StatusUpdater.UpdateFail(ctx, realObj, err, "", setSecurityPolicyReadyStatusFalse, r.Service)
			return ResultNormal, nil
		}

		log.Info("Reconciling CR to create or update securitypolicy", "securitypolicy", req.NamespacedName)
		if err := r.Service.CreateOrUpdateSecurityPolicy(realObj); err != nil {
			if errors.As(err, &nsxutil.RestrictionError{}) {
				setSecurityPolicyErrorAnnotation(ctx, realObj, securitypolicy.IsVPCEnabled(r.Service), r.Client, common.ErrorNoDFWLicense)
				r.StatusUpdater.UpdateFail(ctx, realObj, err, "", setSecurityPolicyReadyStatusFalse, r.Service)
				return ResultNormal, nil
			}
			if nsxutil.IsInvalidLicense(err) {
				log.Error(err, err.Error(), "securitypolicy", req.NamespacedName)
				setSecurityPolicyErrorAnnotation(ctx, realObj, securitypolicy.IsVPCEnabled(r.Service), r.Client, common.ErrorNoDFWLicense)
				os.Exit(1)
			}
			r.StatusUpdater.UpdateFail(ctx, realObj, err, "", setSecurityPolicyReadyStatusFalse, r.Service)
			return ResultRequeue, err
		}
		r.StatusUpdater.UpdateSuccess(ctx, realObj, setSecurityPolicyReadyStatusTrue, r.Service)
		cleanSecurityPolicyErrorAnnotation(ctx, realObj, securitypolicy.IsVPCEnabled(r.Service), r.Client)
	} else {
		log.Info("Reconciling CR to delete securitypolicy", "securitypolicy", req.NamespacedName)
		r.StatusUpdater.IncreaseDeleteTotal()

		// For T1 upgrade, the upgraded CRs still has finalizer
		if controllerutil.ContainsFinalizer(obj, finalizerName) {
			controllerutil.RemoveFinalizer(obj, finalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "Failed to remove finalizer, would retry exponentially", "securitypolicy", req.NamespacedName)
				r.StatusUpdater.DeleteFail(req.NamespacedName, realObj, err)
				return ResultRequeue, err
			}
			log.V(1).Info("Removed finalizer", "securitypolicy", req.NamespacedName)
		}
		if err := r.Service.DeleteSecurityPolicy(realObj.UID, false, servicecommon.ResourceTypeSecurityPolicy); err != nil {
			r.StatusUpdater.DeleteFail(req.NamespacedName, realObj, err)
			return ResultRequeue, err
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, realObj)
	}

	return ResultNormal, nil
}

func setSecurityPolicyReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, args ...interface{}) {
	if len(args) != 1 {
		log.Error(nil, "Service is needed when setting SecurityPolicy status")
		return
	}
	service := args[0].(*securitypolicy.SecurityPolicyService)
	secPolicy := obj.(*v1alpha1.SecurityPolicy)
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX Security Policy has been successfully created/updated",
			Reason:             "SecurityPolicyReady",
			LastTransitionTime: transitionTime,
		},
	}
	updateSecurityPolicyStatusConditions(client, ctx, secPolicy, newConditions, service)
}

func setSecurityPolicyReadyStatusFalse(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, args ...interface{}) {
	if len(args) != 1 {
		log.Error(nil, "Service is needed when setting SecurityPolicy status")
		return
	}
	service := args[0].(*securitypolicy.SecurityPolicyService)
	secPolicy := obj.(*v1alpha1.SecurityPolicy)
	newConditions := []v1alpha1.Condition{
		{
			Type:   v1alpha1.Ready,
			Status: v1.ConditionFalse,
			Message: fmt.Sprintf(
				"error occurred while processing the SecurityPolicy CR. Error: %v",
				err,
			),
			Reason:             "SecurityPolicyNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	updateSecurityPolicyStatusConditions(client, ctx, secPolicy, newConditions, service)
}

func updateSecurityPolicyStatusConditions(client client.Client, ctx context.Context, secPolicy *v1alpha1.SecurityPolicy, newConditions []v1alpha1.Condition, service *securitypolicy.SecurityPolicyService) {
	conditionsUpdated := false
	for i := range newConditions {
		if mergeSecurityPolicyStatusCondition(secPolicy, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		if securitypolicy.IsVPCEnabled(service) {
			finalObj := securitypolicy.T1ToVPC(secPolicy)
			err := client.Status().Update(ctx, finalObj)
			if err != nil {
				log.Error(err, "")
			}
		} else {
			err := client.Status().Update(ctx, secPolicy)
			if err != nil {
				log.Error(err, "")
			}
		}
		log.V(1).Info("Updated SecurityPolicy", "Name", secPolicy.Name, "Namespace", secPolicy.Namespace,
			"New Conditions", newConditions)
	}
}

func mergeSecurityPolicyStatusCondition(secPolicy *v1alpha1.SecurityPolicy, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, secPolicy.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
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
func (r *SecurityPolicyReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("SecurityPolicy garbage collector started")
	nsxPolicySet := r.Service.ListSecurityPolicyID()
	if len(nsxPolicySet) == 0 {
		return nil
	}

	CRPolicySet, err := r.listSecurityPolciyCRIDs()
	if err != nil {
		return err
	}

	var errList []error
	diffSet := nsxPolicySet.Difference(CRPolicySet)
	for elem := range diffSet {
		log.V(1).Info("GC collected SecurityPolicy CR", "securityPolicyUID", elem)
		r.StatusUpdater.IncreaseDeleteTotal()
		err = r.Service.DeleteSecurityPolicy(types.UID(elem), true, servicecommon.ResourceTypeSecurityPolicy)
		if err != nil {
			errList = append(errList, err)
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("errors found in SecurityPolicy garbage collection: %s", errList)
	}
	return nil
}

func (r *SecurityPolicyReconciler) deleteSecurityPolicyByName(ns, name string) error {
	nsxSecurityPolicies := r.Service.ListSecurityPolicyByName(ns, name)
	for _, item := range nsxSecurityPolicies {
		uid := nsxutil.FindTag(item.Tags, servicecommon.TagValueScopeSecurityPolicyUID)
		log.Info("Deleting SecurityPolicy", "securityPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
		if err := r.Service.DeleteSecurityPolicy(types.UID(uid), false, servicecommon.ResourceTypeSecurityPolicy); err != nil {
			log.Error(err, "Failed to delete SecurityPolicy", "securityPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
			return err
		}
		log.Info("Successfully deleted SecurityPolicy", "securityPolicyUID", uid, "nsxSecurityPolicyId", *item.Id)
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
		log.Error(err, "Failed to list SecurityPolicy CR")
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
func reconcileSecurityPolicy(r *SecurityPolicyReconciler, pkgclient client.Client, pods []v1.Pod, q workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
	podPortNames := getAllPodPortNames(pods)
	log.V(1).Info("POD named port", "podPortNames", podPortNames)
	var spList client.ObjectList
	if securitypolicy.IsVPCEnabled(r.Service) {
		spList = &crdv1alpha1.SecurityPolicyList{}
	} else {
		spList = &v1alpha1.SecurityPolicyList{}
	}
	err := pkgclient.List(context.Background(), spList)
	if err != nil {
		log.Error(err, "Failed to list all the security policy")
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

func shouldReconcile(securityPolicy *v1alpha1.SecurityPolicy, q workqueue.TypedRateLimitingInterface[reconcile.Request], podPortNames sets.Set[string]) {
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
		log.Info("Reconcile security policy because of associated resource change",
			"namespace", securityPolicy.Namespace, "name", securityPolicy.Name)
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      securityPolicy.Name,
				Namespace: securityPolicy.Namespace,
			},
		})
	}
}

func (r *SecurityPolicyReconciler) RestoreReconcile() error {
	return nil
}

func (r *SecurityPolicyReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.Start(mgr); err != nil {
		log.Error(err, "Failed to create controller", "controller", "SecurityPolicy")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewSecurityPolicyReconciler(mgr ctrl.Manager, commonService servicecommon.Service, vpcService servicecommon.VPCServiceProvider) *SecurityPolicyReconciler {
	securityPolicyReconcile := &SecurityPolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("securitypolicy-controller"),
	}
	securityPolicyReconcile.Service = securitypolicy.GetSecurityService(commonService, vpcService)
	securityPolicyReconcile.StatusUpdater = common.NewStatusUpdater(securityPolicyReconcile.Client, securityPolicyReconcile.Service.NSXConfig, securityPolicyReconcile.Recorder, MetricResTypeSecurityPolicy, "SecurityPolicy", "SecurityPolicy")
	return securityPolicyReconcile
}
