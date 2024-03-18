/* Copyright © 2021 VMware, Inc. All Rights Reserved.
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

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1"

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
	log                     = logger.Log
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

func updateFail(r *SecurityPolicyReconciler, c *context.Context, o *v1alpha1.SecurityPolicy, e *error) {
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

func deleteFail(r *SecurityPolicyReconciler, c *context.Context, o *v1alpha1.SecurityPolicy, e *error) {
	r.setSecurityPolicyReadyStatusFalse(c, o, metav1.Now(), e)
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
}

func updateSuccess(r *SecurityPolicyReconciler, c *context.Context, o *v1alpha1.SecurityPolicy) {
	r.setSecurityPolicyReadyStatusTrue(c, o, metav1.Now())
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "SecurityPolicy CR has been successfully updated")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResType)
}

func deleteSuccess(r *SecurityPolicyReconciler, _ *context.Context, o *v1alpha1.SecurityPolicy) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "SecurityPolicy CR has been successfully deleted")
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
}

func (r *SecurityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.SecurityPolicy{}
	log.Info("reconciling securitypolicy CR", "securitypolicy", req.NamespacedName)
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResType)

	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "unable to fetch security policy CR", "req", req.NamespacedName)
		return ResultNormal, client.IgnoreNotFound(err)
	}

	// Since SecurityPolicy service can only be activated from NSX 3.2.0 onwards,
	// So need to check NSX version before starting SecurityPolicy reconcile
	if !r.Service.NSXClient.NSXCheckVersion(nsx.SecurityPolicy) {
		err := errors.New("NSX version check failed, SecurityPolicy feature is not supported")
		updateFail(r, &ctx, obj, &err)
		// if NSX version check fails, it will be put back to reconcile queue and be reconciled after 5 minutes
		return ResultRequeueAfter5mins, nil
	}

	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResType)
		if !controllerutil.ContainsFinalizer(obj, servicecommon.SecurityPolicyFinalizerName) {
			controllerutil.AddFinalizer(obj, servicecommon.SecurityPolicyFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "add finalizer", "securitypolicy", req.NamespacedName)
				updateFail(r, &ctx, obj, &err)
				return ResultRequeue, err
			}
			log.V(1).Info("added finalizer on securitypolicy CR", "securitypolicy", req.NamespacedName)
		}

		if isCRInSysNs, err := util.IsSystemNamespace(r.Client, req.Namespace, nil); err != nil {
			err = errors.New("fetch namespace associated with security policy CR failed")
			log.Error(err, "would retry exponentially", "securitypolicy", req.NamespacedName)
			updateFail(r, &ctx, obj, &err)
			return ResultRequeue, err
		} else if isCRInSysNs {
			err = errors.New("security Policy CR cannot be created in System Namespace")
			log.Error(err, "", "securitypolicy", req.NamespacedName)
			updateFail(r, &ctx, obj, &err)
			return ResultNormal, nil
		}

		if err := r.Service.CreateOrUpdateSecurityPolicy(obj); err != nil {
			if errors.As(err, &nsxutil.RestrictionError{}) {
				log.Error(err, err.Error(), "securitypolicy", req.NamespacedName)
				updateFail(r, &ctx, obj, &err)
				return ResultNormal, nil
			}
			// check if invalid license
			apiErr, _ := nsxutil.DumpAPIError(err)
			if apiErr != nil {
				invalidLicense := false
				errorMessage := ""
				for _, apiErrItem := range apiErr.RelatedErrors {
					if *apiErrItem.ErrorCode == nsxutil.InvalidLicenseErrorCode {
						invalidLicense = true
						errorMessage = *apiErrItem.ErrorMessage
					}
				}
				if *apiErr.ErrorCode == nsxutil.InvalidLicenseErrorCode {
					invalidLicense = true
					errorMessage = *apiErr.ErrorMessage
				}
				if invalidLicense {
					log.Error(err, "Invalid license, nsx-operator will restart", "error message", errorMessage)
					os.Exit(1)
				}
			}
			log.Error(err, "create or update failed, would retry exponentially", "securitypolicy", req.NamespacedName)
			updateFail(r, &ctx, obj, &err)
			return ResultRequeue, err
		}
		updateSuccess(r, &ctx, obj)
	} else {
		if controllerutil.ContainsFinalizer(obj, servicecommon.SecurityPolicyFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
			if err := r.Service.DeleteSecurityPolicy(obj, false, servicecommon.ResourceTypeSecurityPolicy); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "securitypolicy", req.NamespacedName)
				deleteFail(r, &ctx, obj, &err)
				return ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(obj, servicecommon.SecurityPolicyFinalizerName)
			if err := r.Client.Update(ctx, obj); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "securitypolicy", req.NamespacedName)
				deleteFail(r, &ctx, obj, &err)
				return ResultRequeue, err
			}
			log.V(1).Info("removed finalizer", "securitypolicy", req.NamespacedName)
			deleteSuccess(r, &ctx, obj)
		} else {
			// only print a message because it's not a normal case
			log.Info("finalizers cannot be recognized", "securitypolicy", req.NamespacedName)
		}
	}

	return ResultNormal, nil
}

func (r *SecurityPolicyReconciler) setSecurityPolicyReadyStatusTrue(ctx *context.Context, secPolicy *v1alpha1.SecurityPolicy, transitionTime metav1.Time) {
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX Security Policy has been successfully created/updated",
			Reason:             "NSX API returned 200 response code for PATCH",
			LastTransitionTime: transitionTime,
		},
	}
	r.updateSecurityPolicyStatusConditions(ctx, secPolicy, newConditions)
}

func (r *SecurityPolicyReconciler) setSecurityPolicyReadyStatusFalse(ctx *context.Context, secPolicy *v1alpha1.SecurityPolicy, transitionTime metav1.Time, err *error) {
	newConditions := []v1alpha1.Condition{
		{
			Type:    v1alpha1.Ready,
			Status:  v1.ConditionFalse,
			Message: "NSX Security Policy could not be created/updated",
			Reason: fmt.Sprintf(
				"error occurred while processing the SecurityPolicy CR. Error: %v",
				*err,
			),
			LastTransitionTime: transitionTime,
		},
	}
	r.updateSecurityPolicyStatusConditions(ctx, secPolicy, newConditions)
}

func (r *SecurityPolicyReconciler) updateSecurityPolicyStatusConditions(ctx *context.Context, secPolicy *v1alpha1.SecurityPolicy, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	for i := range newConditions {
		if r.mergeSecurityPolicyStatusCondition(ctx, secPolicy, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		r.Client.Status().Update(*ctx, secPolicy)
		log.V(1).Info("updated SecurityPolicy", "Name", secPolicy.Name, "Namespace", secPolicy.Namespace,
			"New Conditions", newConditions)
	}
}

func (r *SecurityPolicyReconciler) mergeSecurityPolicyStatusCondition(ctx *context.Context, secPolicy *v1alpha1.SecurityPolicy, newCondition *v1alpha1.Condition) bool {
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityPolicy{}).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Watches(
			&v1.Namespace{},
			&EnqueueRequestForNamespace{Client: k8sClient(mgr)},
			builder.WithPredicates(PredicateFuncsNs),
		).
		Watches(
			&v1.Pod{},
			&EnqueueRequestForPod{Client: k8sClient(mgr)},
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

	go r.GarbageCollector(make(chan bool), servicecommon.GCInterval)
	return nil
}

// GarbageCollector collect securitypolicy which has been removed from k8s.
// cancel is used to break the loop during UT
func (r *SecurityPolicyReconciler) GarbageCollector(cancel chan bool, timeout time.Duration) {
	ctx := context.Background()
	log.Info("garbage collector started")
	for {
		select {
		case <-cancel:
			return
		case <-time.After(timeout):
		}
		nsxPolicySet := r.Service.ListSecurityPolicyID()
		if len(nsxPolicySet) == 0 {
			continue
		}
		policyList := &v1alpha1.SecurityPolicyList{}
		err := r.Client.List(ctx, policyList)
		if err != nil {
			log.Error(err, "failed to list SecurityPolicy CR")
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
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResType)
			err = r.Service.DeleteSecurityPolicy(types.UID(elem), false, servicecommon.ResourceTypeSecurityPolicy)
			if err != nil {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResType)
			} else {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResType)
			}
		}
	}
}

// It is triggered by associated controller like pod, namespace, etc.
func reconcileSecurityPolicy(client client.Client, pods []v1.Pod, q workqueue.RateLimitingInterface) error {
	podPortNames := getAllPodPortNames(pods)
	log.V(1).Info("pod named port", "podPortNames", podPortNames)
	spList := &v1alpha1.SecurityPolicyList{}
	err := client.List(context.Background(), spList)
	if err != nil {
		log.Error(err, "failed to list all the security policy")
		return err
	}

	for _, securityPolicy := range spList.Items {
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
	return nil
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
}
