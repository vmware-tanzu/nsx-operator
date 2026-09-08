/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package ipaddressallocation

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/ipaddressallocation"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log           = logger.Log
	resultNormal  = common.ResultNormal
	resultRequeue = common.ResultRequeue
	MetricResType = common.MetricResTypeIPAddressAllocation
)

// IPAddressAllocationReconciler reconciles a IPAddressAllocation object
type IPAddressAllocationReconciler struct {
	client.Client
	Scheme        *apimachineryruntime.Scheme
	Service       *ipaddressallocation.IPAddressAllocationService
	VPCService    servicecommon.VPCServiceProvider
	Recorder      record.EventRecorder
	StatusUpdater common.StatusUpdater
	restoreMode   bool
}

func setReadyStatusFalse(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, _ ...interface{}) {
	ipaddressallocation := obj.(*v1alpha1.IPAddressAllocation)
	newConditions := []v1alpha1.Condition{
		{
			Type:   v1alpha1.Ready,
			Status: v1.ConditionFalse,
			Message: fmt.Sprintf(
				"error occurred while processing the IPAddressAllocation CR. Error: %v",
				err,
			),
			Reason:             "IPAddressAllocationNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	updateIPAddressAllocationStatusConditions(client, ctx, ipaddressallocation, newConditions)
}

func setReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, args ...interface{}) {
	ipaddressallocation := obj.(*v1alpha1.IPAddressAllocation)
	dnsActive := true
	if len(args) > 0 {
		if active, ok := args[0].(bool); ok {
			dnsActive = active
		}
	}
	newConditions := []v1alpha1.Condition{
		{
			Type:               v1alpha1.Ready,
			Status:             v1.ConditionTrue,
			Message:            "NSX IPAddressAllocation has been successfully created/updated",
			Reason:             "IPAddressAllocationReady",
			LastTransitionTime: transitionTime,
		},
	}
	if ipaddressallocation.Spec.DNSHostName != "" {
		if dnsActive {
			newConditions = append(newConditions, v1alpha1.Condition{
				Type:               v1alpha1.ConditionType(nsxutil.ConditionTypeDNSRecordReady),
				Status:             v1.ConditionTrue,
				Message:            "DNS auto-record successfully created",
				Reason:             nsxutil.ConditionReasonDNSSuccess,
				LastTransitionTime: transitionTime,
			})
		} else {
			newConditions = append(newConditions, v1alpha1.Condition{
				Type:               v1alpha1.ConditionType(nsxutil.ConditionTypeDNSRecordReady),
				Status:             v1.ConditionFalse,
				Message:            "VPC DNS service is not configured on VPC service profile; DNS auto-record is not active in NSX",
				Reason:             nsxutil.ConditionReasonVPCDNSNotConfigured,
				LastTransitionTime: transitionTime,
			})
		}
	}
	updateIPAddressAllocationStatusConditions(client, ctx, ipaddressallocation, newConditions)
}

func updateIPAddressAllocationStatusConditions(client client.Client, ctx context.Context, ipaddressallocation *v1alpha1.IPAddressAllocation, newConditions []v1alpha1.Condition) {
	conditionsUpdated := false
	if ipaddressallocation.Spec.DNSHostName == "" {
		if common.RemoveIPAddressAllocationDNSRecordCondition(ipaddressallocation) {
			conditionsUpdated = true
		}
	}
	for i := range newConditions {
		if mergeIPAddressAllocationStatusCondition(ipaddressallocation, &newConditions[i]) {
			conditionsUpdated = true
		}
	}
	if conditionsUpdated {
		if err := client.Status().Update(ctx, ipaddressallocation); err != nil {
			log.Error(err, "Failed to update status", "Name", ipaddressallocation.Name, "Namespace", ipaddressallocation.Namespace)
		} else {
			log.Info("Updated IPAddressAllocation", "Name", ipaddressallocation.Name, "Namespace", ipaddressallocation.Namespace, "Status", ipaddressallocation.Status)
		}
	}
}

func mergeIPAddressAllocationStatusCondition(ipaddressallocation *v1alpha1.IPAddressAllocation, newCondition *v1alpha1.Condition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, ipaddressallocation.Status.Conditions)

	if common.IsConditionSemanticEqual(matchedCondition, newCondition) {
		log.Trace("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
		matchedCondition.LastTransitionTime = newCondition.LastTransitionTime
	} else {
		ipaddressallocation.Status.Conditions = append(ipaddressallocation.Status.Conditions, *newCondition)
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

func (r *IPAddressAllocationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &v1alpha1.IPAddressAllocation{}
	log.Info("Reconciling IPAddressAllocation CR", "IPAddressAllocation", req.NamespacedName)
	r.StatusUpdater.IncreaseSyncTotal()
	if err := r.Client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			err = r.Service.DeleteIPAddressAllocationByNamespacedName(req.Namespace, req.Name)
			if err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return resultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return common.ResultNormal, nil
		}
		return resultRequeue, err
	}
	if obj.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleUpdate(ctx, obj)
	}
	return r.handleDeletion(req, obj)
}

func (r *IPAddressAllocationReconciler) handleUpdate(ctx context.Context, obj *v1alpha1.IPAddressAllocation) (ctrl.Result, error) {
	r.StatusUpdater.IncreaseUpdateTotal()
	if obj.Spec.DNSHostName != "" {
		if err := nsxutil.ValidateDNSHostname(obj.Spec.DNSHostName); err != nil {
			log.Error(err, "DNS hostname validation failed for IPAddressAllocation", "IPAddressAllocation", obj.Name, "Namespace", obj.Namespace, "dnsHostname", obj.Spec.DNSHostName)
			common.SetIPAddressAllocationDNSRecordCondition(obj, v1.ConditionFalse, nsxutil.ConditionReasonDNSInvalidLength, err.Error())
			if r.Recorder != nil {
				r.Recorder.Event(obj, v1.EventTypeWarning, nsxutil.EventReasonDNSConfigFailed, err.Error())
			}
			r.StatusUpdater.UpdateFail(ctx, obj, err, "DNS hostname validation failed", setReadyStatusFalse)
			return resultNormal, nil
		}
	}
	updated, err := r.Service.CreateOrUpdateIPAddressAllocation(obj, r.restoreMode)
	if err != nil {
		if obj.Spec.DNSHostName != "" {
			outcome := nsxutil.ClassifyDNSError(err)
			common.SetIPAddressAllocationDNSRecordCondition(obj, v1.ConditionFalse, outcome.ConditionReason, outcome.Message)
			if r.Recorder != nil {
				r.Recorder.Event(obj, outcome.EventType, outcome.EventReason, outcome.Message)
			}
			if outcome.IsPermanent {
				r.StatusUpdater.UpdateFail(ctx, obj, err, outcome.Message, setReadyStatusFalse)
				return resultNormal, nil
			}
		}
		r.StatusUpdater.UpdateFail(ctx, obj, err, "", setReadyStatusFalse)
		return resultRequeue, err
	}
	if updated {
		// If the IPAddressAllocation is updated, we clear the conditions to ensure the status is updated anyway
		obj.Status.Conditions = nil
	}
	dnsActive := false
	if r.VPCService != nil && obj.Spec.DNSHostName != "" {
		active, vpcErr := r.VPCService.IsVPCDNSEnabledByNamespace(obj.Namespace)
		if vpcErr != nil {
			log.Warn("Failed to check if VPC DNS is enabled for IPAddressAllocation", "IPAddressAllocation", obj.Name, "Namespace", obj.Namespace, "error", vpcErr)
		} else {
			dnsActive = active
		}
	}
	r.StatusUpdater.UpdateSuccess(ctx, obj, setReadyStatusTrue, dnsActive)
	if obj.Spec.DNSHostName != "" && r.Recorder != nil {
		if dnsActive {
			r.Recorder.Event(obj, v1.EventTypeNormal, nsxutil.EventReasonDNSRecordCreated, "DNS auto-record successfully created")
		} else {
			r.Recorder.Event(obj, v1.EventTypeWarning, nsxutil.EventReasonVPCDNSNotConfigured, "VPC DNS service is not configured on VPC service profile; DNS auto-record is not active in NSX")
		}
	}
	return resultNormal, nil
}

func (r *IPAddressAllocationReconciler) handleDeletion(req ctrl.Request, obj *v1alpha1.IPAddressAllocation) (ctrl.Result, error) {
	r.StatusUpdater.IncreaseDeleteTotal()
	if err := r.Service.DeleteIPAddressAllocation(obj); err != nil {
		r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
		return resultRequeue, err
	}
	r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
	return resultNormal, nil
}

func (r *IPAddressAllocationReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.IPAddressAllocation{}).
		WithEventFilter(common.VPCNamespacePredicate(r.Client)).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

func (r *IPAddressAllocationReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("IPAddressAllocation garbage collector started")
	ipAddressAllocationSet := r.Service.ListIPAddressAllocationID()
	if len(ipAddressAllocationSet) == 0 {
		return nil
	}

	ipAddressAllocationCRList := &v1alpha1.IPAddressAllocationList{}
	if err := r.Client.List(ctx, ipAddressAllocationCRList); err != nil {
		log.Error(err, "Failed to list IPAddressAllocation CR")
		return err
	}
	CRIPAddressAllocationSet := sets.New[string]()
	for _, ipa := range ipAddressAllocationCRList.Items {
		CRIPAddressAllocationSet.Insert(string(ipa.UID))
	}

	log.Trace("IPAddressAllocation garbage collector", "nsxIPAddressAllocationSet", ipAddressAllocationSet, "CRIPAddressAllocationSet", CRIPAddressAllocationSet)

	diffSet := ipAddressAllocationSet.Difference(CRIPAddressAllocationSet)
	var errList []error
	for elem := range diffSet {
		log.Info("GC collected nsx IPAddressAllocation", "UID", elem)
		if err := r.Service.DeleteIPAddressAllocation(types.UID(elem)); err != nil {
			log.Error(err, "Failed to delete nsx IPAddressAllocation", "UID", elem)
			errList = append(errList, err)
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("errors found in IPAddressAllocation garbage collection: %s", errList)
	}
	return nil
}

func (r *IPAddressAllocationReconciler) RestoreReconcile() error {
	restoreList, err := r.getRestoreList()
	if err != nil {
		err = fmt.Errorf("failed to get IPAddressAllocation restore list: %w", err)
		return err
	}
	var errorList []error
	r.restoreMode = true
	for _, key := range restoreList {
		result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if err != nil || common.IsReconcileResultRequeue(result) {
			errorList = append(errorList, fmt.Errorf("failed to restore IPAddressAllocation %s, error: %w", key, err))
		}
	}
	if len(errorList) > 0 {
		return fmt.Errorf("errors found in IPAddressAllocation restore: %v", errorList)
	}
	return nil
}

func (r *IPAddressAllocationReconciler) getRestoreList() ([]types.NamespacedName, error) {
	ipAddressAllocationCRIDs := r.Service.ListIPAddressAllocationID()

	restoreList := []types.NamespacedName{}
	ipAddressAllocationCRList := &v1alpha1.IPAddressAllocationList{}
	if err := r.Client.List(context.TODO(), ipAddressAllocationCRList); err != nil {
		return restoreList, err
	}
	for _, ipAddressAllocationCR := range ipAddressAllocationCRList.Items {
		// Restore an IPAddressAllocation if IPAddressAllocation CR has status updated but no corresponding NSX IPAddressAllocation in cache
		if len(ipAddressAllocationCR.Status.AllocationIPs) > 0 && !ipAddressAllocationCRIDs.Has(string(ipAddressAllocationCR.GetUID())) {
			restoreList = append(restoreList, types.NamespacedName{Namespace: ipAddressAllocationCR.Namespace, Name: ipAddressAllocationCR.Name})
		}
	}
	return restoreList, nil
}

func (r *IPAddressAllocationReconciler) StartController(mgr ctrl.Manager, hookServer webhook.Server) error {
	if err := r.setupWithManager(mgr); err != nil {
		log.Error(err, "Failed to create ipaddressallocation controller")
		return err
	}
	if hookServer != nil {
		hookServer.Register("/validate-crd-nsx-vmware-com-v1alpha1-ipaddressallocation",
			&webhook.Admission{
				Handler: &IPAddressAllocationValidator{
					Client:  mgr.GetClient(),
					decoder: admission.NewDecoder(mgr.GetScheme()),
				},
			})
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewIPAddressAllocationReconciler(mgr ctrl.Manager, ipAddressAllocationService *ipaddressallocation.IPAddressAllocationService, vpcService servicecommon.VPCServiceProvider) *IPAddressAllocationReconciler {
	ipAddressAllocationReconciler := &IPAddressAllocationReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Service:    ipAddressAllocationService,
		VPCService: vpcService,
		Recorder:   mgr.GetEventRecorderFor("ipaddressallocation-controller"), //nolint:staticcheck // record.EventRecorder; StatusUpdater not on events.EventRecorder yet
	}
	ipAddressAllocationReconciler.StatusUpdater = common.NewStatusUpdater(ipAddressAllocationReconciler.Client, ipAddressAllocationReconciler.Service.NSXConfig, ipAddressAllocationReconciler.Recorder, common.MetricResTypeNetworkInfo, "IPAddressAllocation", "IPAddressAllocation")
	return ipAddressAllocationReconciler
}
