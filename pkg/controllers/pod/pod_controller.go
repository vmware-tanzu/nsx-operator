/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package pod

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"
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
}

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Info("reconciling pod", "pod", req.NamespacedName)
	startTime := time.Now()
	defer func() {
		log.Info("finished reconciling Pod", "Pod", req.NamespacedName, "duration", time.Since(startTime))
	}()

	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerSyncTotal, MetricResTypePod)

	pod := &v1.Pod{}
	if err := r.Client.Get(ctx, req.NamespacedName, pod); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteSubnetPortByPodName(ctx, req.Namespace, req.Name); err != nil {
				log.Error(err, "failed to delete NSX SubnetPort", "SubnetPort", req.NamespacedName)
				return common.ResultRequeue, err
			}
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
		metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypePod)
		nsxSubnetPath, err := r.GetSubnetPathForPod(ctx, pod)
		if err != nil {
			log.Error(err, "failed to get NSX resource path from subnet", "pod.Name", pod.Name, "pod.UID", pod.UID)
			return common.ResultRequeue, err
		}
		log.Info("got NSX subnet for pod", "NSX subnet path", nsxSubnetPath, "pod.Name", pod.Name, "pod.UID", pod.UID)
		node, err := r.GetNodeByName(pod.Spec.NodeName)
		if err != nil {
			// The error at the very beginning of the operator startup is expected because at that time the node may be not cached yet. We can expect the retry to become normal.
			log.Error(err, "failed to get node ID for pod", "pod.Name", req.NamespacedName, "pod.UID", pod.UID, "node", pod.Spec.NodeName)
			return common.ResultRequeue, err
		}
		contextID := *node.UniqueId
		// There is a race condition that the subnetset controller may delete the
		// subnet during CollectGarbage. So check the subnet under lock.
		r.SubnetService.LockSubnet(&nsxSubnetPath)
		defer r.SubnetService.UnlockSubnet(&nsxSubnetPath)

		nsxSubnet, err := r.SubnetService.GetSubnetByPath(nsxSubnetPath)
		if err != nil {
			return common.ResultRequeue, err
		}
		_, err = r.SubnetPortService.CreateOrUpdateSubnetPort(pod, nsxSubnet, contextID, &pod.ObjectMeta.Labels)
		if err != nil {
			log.Error(err, "failed to create or update NSX subnet port, would retry exponentially", "pod.Name", req.NamespacedName, "pod.UID", pod.UID)
			updateFail(r, ctx, pod, &err)
			return common.ResultRequeue, err
		}
		updateSuccess(r, ctx, pod)
	} else {
		subnetPortID := r.SubnetPortService.BuildSubnetPortId(&pod.ObjectMeta)
		if err := r.SubnetPortService.DeleteSubnetPortById(subnetPortID); err != nil {
			log.Error(err, "deletion failed, would retry exponentially", "pod", req.NamespacedName)
			deleteFail(r, ctx, pod, &err)
			return common.ResultRequeue, err
		}
		deleteSuccess(r, ctx, pod)
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

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		WithEventFilter(PredicateFuncsPod).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: common.NumReconcile(),
			}).
		Complete(r)
}

func StartPodController(mgr ctrl.Manager, subnetPortService *subnetport.SubnetPortService, subnetService servicecommon.SubnetServiceProvider, vpcService servicecommon.VPCServiceProvider, nodeService servicecommon.NodeServiceReader) {
	podPortReconciler := PodReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		SubnetService:     subnetService,
		SubnetPortService: subnetPortService,
		VPCService:        vpcService,
		NodeServiceReader: nodeService,
		Recorder:          mgr.GetEventRecorderFor("pod-controller"),
	}
	if err := podPortReconciler.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "Pod")
		os.Exit(1)
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, podPortReconciler.CollectGarbage)
}

// Start setup manager and launch GC
func (r *PodReconciler) Start(mgr ctrl.Manager) error {
	err := r.SetupWithManager(mgr)
	if err != nil {
		return err
	}
	return nil
}

// CollectGarbage  collect Pod which has been removed from crd.
func (r *PodReconciler) CollectGarbage(ctx context.Context) {
	log.Info("pod garbage collector started")
	nsxSubnetPortSet := r.SubnetPortService.ListNSXSubnetPortIDForPod()
	if len(nsxSubnetPortSet) == 0 {
		return
	}
	podList := &v1.PodList{}
	err := r.Client.List(ctx, podList)
	if err != nil {
		log.Error(err, "failed to list Pod")
		return
	}

	PodSet := sets.New[string]()
	for _, pod := range podList.Items {
		subnetPortID := r.SubnetPortService.BuildSubnetPortId(&pod.ObjectMeta)
		PodSet.Insert(subnetPortID)
	}

	diffSet := nsxSubnetPortSet.Difference(PodSet)
	for elem := range diffSet {
		log.V(1).Info("GC collected Pod", "NSXSubnetPortID", elem)
		metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypePod)
		err = r.SubnetPortService.DeleteSubnetPortById(elem)
		if err != nil {
			metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypePod)
		} else {
			metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypePod)
		}
	}
}

func updateFail(r *PodReconciler, _ context.Context, o *v1.Pod, e *error) {
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailUpdate, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResTypePod)
}

func deleteFail(r *PodReconciler, _ context.Context, o *v1.Pod, e *error) {
	r.Recorder.Event(o, v1.EventTypeWarning, common.ReasonFailDelete, fmt.Sprintf("%v", *e))
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypePod)
}

func updateSuccess(r *PodReconciler, _ context.Context, o *v1.Pod) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulUpdate, "Pod CR has been successfully updated")
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResTypePod)
}

func deleteSuccess(r *PodReconciler, _ context.Context, o *v1.Pod) {
	r.Recorder.Event(o, v1.EventTypeNormal, common.ReasonSuccessfulDelete, "Pod CR has been successfully deleted")
	metrics.CounterInc(r.SubnetPortService.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypePod)
}

func (r *PodReconciler) GetSubnetPathForPod(ctx context.Context, pod *v1.Pod) (string, error) {
	subnetPortIDForPod := r.SubnetPortService.BuildSubnetPortId(&pod.ObjectMeta)
	subnetPath := r.SubnetPortService.GetSubnetPathForSubnetPortFromStore(subnetPortIDForPod)
	if len(subnetPath) > 0 {
		log.V(1).Info("NSX subnet port had been created, returning the existing NSX subnet path", "pod.UID", pod.UID, "subnetPath", subnetPath)
		return subnetPath, nil
	}
	subnetSet, err := common.GetDefaultSubnetSet(r.SubnetPortService.Client, ctx, pod.Namespace, servicecommon.LabelDefaultPodSubnetSet)
	if err != nil {
		return "", err
	}
	log.Info("got default subnetset for pod, allocating the NSX subnet", "subnetSet.Name", subnetSet.Name, "subnetSet.UID", subnetSet.UID, "pod.Name", pod.Name, "pod.UID", pod.UID)
	subnetPath, err = common.AllocateSubnetFromSubnetSet(subnetSet, r.VPCService, r.SubnetService, r.SubnetPortService)
	if err != nil {
		return subnetPath, err
	}
	log.Info("allocated NSX subnet for pod", "nsxSubnetPath", subnetPath, "pod.Name", pod.Name, "pod.UID", pod.UID)
	return subnetPath, nil
}

func podIsDeleted(pod *v1.Pod) bool {
	return !pod.ObjectMeta.DeletionTimestamp.IsZero() || pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed"
}

func (r *PodReconciler) deleteSubnetPortByPodName(ctx context.Context, ns string, name string) error {
	// When deleting SubnetPort by Name and Namespace, skip the SubnetPort belonging to the existed SubnetPort CR
	nsxSubnetPorts := r.SubnetPortService.ListSubnetPortByPodName(ns, name)

	crSubnetPortIDsSet, err := r.SubnetPortService.ListSubnetPortIDsFromCRs(ctx)
	if err != nil {
		log.Error(err, "failed to list SubnetPort CRs")
		return err
	}

	for _, nsxSubnetPort := range nsxSubnetPorts {
		if crSubnetPortIDsSet.Has(*nsxSubnetPort.Id) {
			log.Info("skipping deletion, Pod CR still exists in K8s", "ID", *nsxSubnetPort.Id)
			continue
		}
		if err := r.SubnetPortService.DeleteSubnetPort(nsxSubnetPort); err != nil {
			return err
		}
	}
	log.Info("successfully deleted nsxSubnetPort for Pod", "namespace", ns, "name", name)
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
