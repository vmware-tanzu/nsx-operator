/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package pod

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
)

var (
	log              = &logger.Log
	MetricResTypePod = common.MetricResTypePod
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *apimachineryruntime.Scheme

	SubnetPortService *subnetport.SubnetPortService
	SubnetService     servicecommon.SubnetServiceProvider
	VPCService        servicecommon.VPCServiceProvider
	NodeServiceReader servicecommon.NodeServiceReader
	Recorder          record.EventRecorder
	StatusUpdater     common.StatusUpdater
}

func setPodReadyStatusFalse(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, err error, args ...interface{}) {
	pod := obj.(*v1.Pod)
	newConditions := []v1.PodCondition{
		{
			Type:   v1.PodReady,
			Status: v1.ConditionFalse,
			Message: fmt.Sprintf(
				"error occurred while processing the Pod. Error: %v",
				err,
			),
			Reason:             "PodNotReady",
			LastTransitionTime: transitionTime,
		},
	}
	updatePodStatusConditions(client, ctx, pod, newConditions)
}

func setPodReadyStatusTrue(client client.Client, ctx context.Context, obj client.Object, transitionTime metav1.Time, args ...interface{}) {
	pod := obj.(*v1.Pod)
	newConditions := []v1.PodCondition{
		{
			Type:               v1.PodReady,
			Status:             v1.ConditionTrue,
			Message:            "Pod has been successfully created/updated",
			Reason:             "PodReady",
			LastTransitionTime: transitionTime,
		},
	}
	updatePodStatusConditions(client, ctx, pod, newConditions)
}

func updatePodStatusConditions(client client.Client, ctx context.Context, pod *v1.Pod, newConditions []v1.PodCondition) {
	conditionsUpdated := false
	for i := range newConditions {
		if mergePodStatusCondition(pod, &newConditions[i]) {
			conditionsUpdated = true
		}
	}

	if conditionsUpdated {
		err := client.Status().Update(ctx, pod)
		if err != nil {
			log.Error(err, "Failed to update Pod status", "Name", pod.Name, "Namespace", pod.Namespace)
		}
		log.V(2).Info("Updated pod", "Name", pod.Name, "Namespace", pod.Namespace,
			"New Conditions", newConditions)
	}
}

func mergePodStatusCondition(pod *v1.Pod, newCondition *v1.PodCondition) bool {
	matchedCondition := getExistingConditionOfType(newCondition.Type, pod.Status.Conditions)

	if reflect.DeepEqual(matchedCondition, newCondition) {
		log.V(2).Info("Conditions already match", "New Condition", newCondition, "Existing Condition", matchedCondition)
		return false
	}

	if matchedCondition != nil {
		matchedCondition.Reason = newCondition.Reason
		matchedCondition.Message = newCondition.Message
		matchedCondition.Status = newCondition.Status
	} else {
		pod.Status.Conditions = append(pod.Status.Conditions, *newCondition)
	}
	return true
}

func getExistingConditionOfType(conditionType v1.PodConditionType, existingConditions []v1.PodCondition) *v1.PodCondition {
	for i := range existingConditions {
		if existingConditions[i].Type == conditionType {
			return &existingConditions[i]
		}
	}
	return nil
}

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Info("reconciling pod", "pod", req.NamespacedName)
	startTime := time.Now()
	defer func() {
		log.Info("finished reconciling Pod", "Pod", req.NamespacedName, "duration", time.Since(startTime))
	}()

	r.StatusUpdater.IncreaseSyncTotal()

	pod := &v1.Pod{}
	if err := r.Client.Get(ctx, req.NamespacedName, pod); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteSubnetPortByPodName(ctx, req.Namespace, req.Name); err != nil {
				r.StatusUpdater.DeleteFail(req.NamespacedName, nil, err)
				return common.ResultRequeue, err
			}
			r.StatusUpdater.DeleteSuccess(req.NamespacedName, nil)
			return common.ResultNormal, nil
		}
		log.Error(err, "unable to fetch Pod", "Pod", req.NamespacedName)
		return common.ResultRequeue, err
	}
	if len(pod.Spec.NodeName) == 0 {
		log.Info("pod is not scheduled on node yet, skipping", "pod", req.NamespacedName)
		return common.ResultNormal, nil
	}

	if !podIsDeleted(pod) {
		r.StatusUpdater.IncreaseUpdateTotal()
		isExisting, nsxSubnetPath, err := r.GetSubnetPathForPod(ctx, pod)
		if err != nil {
			log.Error(err, "failed to get NSX resource path from subnet", "pod.Name", pod.Name, "pod.UID", pod.UID)
			r.StatusUpdater.UpdateFail(ctx, pod, err, "", setPodReadyStatusFalse)
			return common.ResultRequeue, err
		}
		if !isExisting {
			defer r.SubnetPortService.ReleasePortInSubnet(nsxSubnetPath)
		}
		log.Info("got NSX subnet for pod", "NSX subnet path", nsxSubnetPath, "pod.Name", pod.Name, "pod.UID", pod.UID)
		node, err := r.GetNodeByName(pod.Spec.NodeName)
		if err != nil {
			// The error at the very beginning of the operator startup is expected because at that time the node may be not cached yet. We can expect the retry to become normal.
			log.Error(err, "failed to get node ID for pod", "pod.Name", req.NamespacedName, "pod.UID", pod.UID, "node", pod.Spec.NodeName)
			return common.ResultRequeue, err
		}
		contextID := *node.UniqueId
		nsxSubnet, err := r.SubnetService.GetSubnetByPath(nsxSubnetPath)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, pod, err, "", setPodReadyStatusFalse)
			return common.ResultRequeue, err
		}
		_, err = r.SubnetPortService.CreateOrUpdateSubnetPort(pod, nsxSubnet, contextID, &pod.ObjectMeta.Labels, false, false)
		if err != nil {
			r.StatusUpdater.UpdateFail(ctx, pod, err, "", setPodReadyStatusFalse)
			return common.ResultRequeue, err
		}
		r.StatusUpdater.UpdateSuccess(ctx, pod, setPodReadyStatusTrue)
	} else {
		subnetPortID := r.SubnetPortService.BuildSubnetPortId(&pod.ObjectMeta)
		if err := r.SubnetPortService.DeleteSubnetPortById(subnetPortID); err != nil {
			r.StatusUpdater.DeleteFail(req.NamespacedName, pod, err)
			return common.ResultRequeue, err
		}
		r.StatusUpdater.DeleteSuccess(req.NamespacedName, pod)
	}
	return common.ResultNormal, nil
}

func (r *PodReconciler) GetNodeByName(nodeName string) (*model.HostTransportNode, error) {
	nodes := r.NodeServiceReader.GetNodeByName(nodeName)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("node %s not found", nodeName)
	}
	if len(nodes) > 1 {
		var nodeIDs []string
		for _, node := range nodes {
			nodeIDs = append(nodeIDs, *node.UniqueId)
		}
		return nil, fmt.Errorf("multiple node IDs found for node %s: %v", nodeName, nodeIDs)
	}
	return nodes[0], nil
}

// setupWithManager sets up the controller with the Manager.
func (r *PodReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		WithEventFilter(PredicateFuncsPod).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

func (r *PodReconciler) RestoreReconcile() error {
	return nil
}

func (r *PodReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "Pod")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewPodReconciler(mgr ctrl.Manager, subnetPortService *subnetport.SubnetPortService, subnetService servicecommon.SubnetServiceProvider, vpcService servicecommon.VPCServiceProvider, nodeService servicecommon.NodeServiceReader) *PodReconciler {
	podPortReconciler := &PodReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		VPCService:        vpcService,
		NodeServiceReader: nodeService,
		Recorder:          mgr.GetEventRecorderFor("pod-controller"),
	}
	podPortReconciler.StatusUpdater = common.NewStatusUpdater(podPortReconciler.Client, podPortReconciler.SubnetPortService.NSXConfig, podPortReconciler.Recorder, MetricResTypePod, "SubnetPort", "Pod")
	return podPortReconciler
}

// Start setup manager and launch GC
func (r *PodReconciler) Start(mgr ctrl.Manager) error {
	err := r.setupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

// CollectGarbage  collect Pod which has been removed from crd.
func (r *PodReconciler) CollectGarbage(ctx context.Context) error {
	log.Info("pod garbage collector started")
	nsxSubnetPortSet := r.SubnetPortService.ListNSXSubnetPortIDForPod()
	if len(nsxSubnetPortSet) == 0 {
		return nil
	}
	podList := &v1.PodList{}
	err := r.Client.List(ctx, podList)
	if err != nil {
		log.Error(err, "failed to list Pod")
		return err
	}

	PodSet := sets.New[string]()
	for _, pod := range podList.Items {
		subnetPortID := r.SubnetPortService.BuildSubnetPortId(&pod.ObjectMeta)
		PodSet.Insert(subnetPortID)
	}

	var errList []error
	diffSet := nsxSubnetPortSet.Difference(PodSet)
	for elem := range diffSet {
		log.V(1).Info("GC collected Pod", "NSXSubnetPortID", elem)
		r.StatusUpdater.IncreaseDeleteTotal()
		err = r.SubnetPortService.DeleteSubnetPortById(elem)
		if err != nil {
			errList = append(errList, err)
			r.StatusUpdater.IncreaseDeleteFailTotal()
		} else {
			r.StatusUpdater.IncreaseDeleteSuccessTotal()
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("errors found in Pod garbage collection: %s", errList)
	}
	return nil
}

func (r *PodReconciler) GetSubnetPathForPod(ctx context.Context, pod *v1.Pod) (bool, string, error) {
	subnetPortIDForPod := r.SubnetPortService.BuildSubnetPortId(&pod.ObjectMeta)
	subnetPath := r.SubnetPortService.GetSubnetPathForSubnetPortFromStore(subnetPortIDForPod)
	if len(subnetPath) > 0 {
		log.V(1).Info("NSX subnet port had been created, returning the existing NSX subnet path", "pod.UID", pod.UID, "subnetPath", subnetPath)
		return true, subnetPath, nil
	}
	subnetSet, err := common.GetDefaultSubnetSetByNamespace(r.SubnetPortService.Client, pod.Namespace, servicecommon.LabelDefaultPodSubnetSet)
	if err != nil {
		return false, "", err
	}
	log.Info("got default subnetset for pod, allocating the NSX subnet", "subnetSet.Name", subnetSet.Name, "subnetSet.UID", subnetSet.UID, "pod.Name", pod.Name, "pod.UID", pod.UID)
	subnetPath, err = common.AllocateSubnetFromSubnetSet(subnetSet, r.VPCService, r.SubnetService, r.SubnetPortService)
	if err != nil {
		return false, subnetPath, err
	}
	log.Info("allocated NSX subnet for pod", "nsxSubnetPath", subnetPath, "pod.Name", pod.Name, "pod.UID", pod.UID)
	return false, subnetPath, nil
}

func podIsDeleted(pod *v1.Pod) bool {
	return !pod.ObjectMeta.DeletionTimestamp.IsZero() || pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed"
}

func (r *PodReconciler) deleteSubnetPortByPodName(ctx context.Context, ns string, name string) error {
	// NamespacedName is a unique identity in store as only one worker can deal with the NamespacedName at a time
	nsxSubnetPorts := r.SubnetPortService.ListSubnetPortByPodName(ns, name)

	for _, nsxSubnetPort := range nsxSubnetPorts {
		if err := r.SubnetPortService.DeleteSubnetPort(nsxSubnetPort); err != nil {
			return err
		}
	}
	log.Info("Successfully deleted nsxSubnetPort for Pod", "Namespace", ns, "Name", name)
	return nil
}

// PredicateFuncsPod filters out events where pod.Spec.HostNetwork is true
var PredicateFuncsPod = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldPod, okOld := e.ObjectOld.(*v1.Pod)
		newPod, okNew := e.ObjectNew.(*v1.Pod)
		if !okOld || !okNew {
			return true
		}

		if oldPod.Spec.HostNetwork && newPod.Spec.HostNetwork {
			return false
		}
		return true
	},
	CreateFunc: func(e event.CreateEvent) bool {
		pod, ok := e.Object.(*v1.Pod)
		if !ok {
			return true
		}

		if pod.Spec.HostNetwork {
			return false
		}
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool { return true },
	GenericFunc: func(e event.GenericEvent) bool {
		return true
	},
}
