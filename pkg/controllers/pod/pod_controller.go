/* Copyright © 2023 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package pod

import (
	"context"
	"os"
	"runtime"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/metrics"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

var (
	log              = logger.Log
	MetricResTypePod = common.MetricResTypePod
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme  *apimachineryruntime.Scheme
	Service *subnetport.SubnetPortService
}

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	pod := &v1.Pod{}
	log.Info("reconciling pod", "pod", req.NamespacedName)

	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerSyncTotal, MetricResTypePod)

	if err := r.Client.Get(ctx, req.NamespacedName, pod); err != nil {
		log.Error(err, "unable to fetch pod", "req", req.NamespacedName)
		return common.ResultNormal, client.IgnoreNotFound(err)
	}
	if pod.Spec.HostNetwork {
		log.Info("skipping handling hostnetwork pod", "pod", req.NamespacedName)
		return common.ResultNormal, nil
	}
	if len(pod.Spec.NodeName) == 0 {
		log.Info("pod is not scheduled on node yet, skipping", "pod", req.NamespacedName)
		return common.ResultNormal, nil
	}

	if pod.ObjectMeta.DeletionTimestamp.IsZero() {
		metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateTotal, MetricResTypePod)
		if !controllerutil.ContainsFinalizer(pod, servicecommon.PodFinalizerName) {
			controllerutil.AddFinalizer(pod, servicecommon.PodFinalizerName)
			if err := r.Client.Update(ctx, pod); err != nil {
				log.Error(err, "add finalizer", "pod", req.NamespacedName)
				updateFail(r, &ctx, pod, &err)
				return common.ResultRequeue, err
			}
			log.Info("added finalizer on pod", "pod", req.NamespacedName)
		}

		nsxSubnetPath, err := r.GetSubnetPathForPod(ctx, pod)
		if err != nil {
			log.Error(err, "failed to get NSX resource path from subnet", "pod.Name", pod.Name, "pod.UID", pod.UID)
			return common.ResultRequeue, err
		}
		log.Info("got NSX subnet for pod", "NSX subnet path", nsxSubnetPath, "pod.Name", pod.Name, "pod.UID", pod.UID)
		node, err := common.ServiceMediator.GetNodeByName(pod.Spec.NodeName)
		if err != nil {
			// The error at the very beginning of the operator startup is expected because at that time the node may be not cached yet. We can expect the retry to become normal.
			log.Error(err, "failed to get node ID for pod", "pod.Name", req.NamespacedName, "pod.UID", pod.UID, "node", pod.Spec.NodeName)
			return common.ResultRequeue, err
		}
		contextID := *node.Id
		nsxSubnetPortState, err := r.Service.CreateOrUpdateSubnetPort(pod, nsxSubnetPath, contextID, nil)
		if err != nil {
			log.Error(err, "failed to create or update NSX subnet port, would retry exponentially", "pod.Name", req.NamespacedName, "pod.UID", pod.UID)
			updateFail(r, &ctx, pod, &err)
			return common.ResultRequeue, err
		}
		podAnnotationChanges := map[string]string{
			servicecommon.AnnotationPodMAC:        strings.Trim(*nsxSubnetPortState.RealizedBindings[0].Binding.MacAddress, "\""),
			servicecommon.AnnotationPodAttachment: *nsxSubnetPortState.Attachment.Id,
		}
		err = util.UpdateK8sResourceAnnotation(r.Client, &ctx, pod, podAnnotationChanges)
		if err != nil {
			log.Error(err, "failed to update pod annotation", "pod.Name", req.NamespacedName, "pod.UID", pod.UID, "podAnnotationChanges", podAnnotationChanges)
			return common.ResultNormal, err
		}
		updateSuccess(r, &ctx, pod)
	} else {
		if controllerutil.ContainsFinalizer(pod, servicecommon.PodFinalizerName) {
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypePod)
			if err := r.Service.DeleteSubnetPort(pod.UID); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "pod", req.NamespacedName)
				deleteFail(r, &ctx, pod, &err)
				return common.ResultRequeue, err
			}
			controllerutil.RemoveFinalizer(pod, servicecommon.PodFinalizerName)
			if err := r.Client.Update(ctx, pod); err != nil {
				log.Error(err, "deletion failed, would retry exponentially", "pod", req.NamespacedName)
				deleteFail(r, &ctx, pod, &err)
				return common.ResultRequeue, err
			}
			log.Info("removed finalizer", "pod", req.NamespacedName)
			deleteSuccess(r, &ctx, pod)
		} else {
			log.Info("finalizers cannot be recognized", "pod", req.NamespacedName)
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		WithEventFilter(
			predicate.Funcs{
				DeleteFunc: func(e event.DeleteEvent) bool {
					// Suppress Delete events to avoid filtering them out in the Reconcile function
					return false
				},
			},
		).
		WithOptions(
			controller.Options{
				MaxConcurrentReconciles: runtime.NumCPU(),
			}).
		Complete(r)
}

func (r *PodReconciler) StartController(mgr ctrl.Manager, commonService servicecommon.Service) {
	if subnetPortService, err := subnetport.InitializeSubnetPort(commonService); err != nil {
		log.Error(err, "failed to initialize subnetport commonService", "controller", "Pod")
		os.Exit(1)
	} else {
		r.Service = subnetPortService
		common.ServiceMediator.SubnetPortService = r.Service
	}
	if err := r.Start(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "Pod")
		os.Exit(1)
	}
}

// Start setup manager and launch GC
func (r *PodReconciler) Start(mgr ctrl.Manager) error {
	err := r.SetupWithManager(mgr)
	if err != nil {
		return err
	}
	go r.GarbageCollector(make(chan bool), servicecommon.GCInterval)
	return nil
}

// GarbageCollector collect Pod which has been removed from crd.
// cancel is used to break the loop during UT
func (r *PodReconciler) GarbageCollector(cancel chan bool, timeout time.Duration) {
	ctx := context.Background()
	log.Info("pod garbage collector started")
	for {
		select {
		case <-cancel:
			return
		case <-time.After(timeout):
		}
		nsxSubnetPortSet := r.Service.ListNSXSubnetPortIDForPod()
		if len(nsxSubnetPortSet) == 0 {
			continue
		}
		podList := &v1.PodList{}
		err := r.Client.List(ctx, podList)
		if err != nil {
			log.Error(err, "failed to list Pod")
			continue
		}

		PodSet := sets.NewString()
		for _, pod := range podList.Items {
			PodSet.Insert(string(pod.UID))
		}

		for elem := range nsxSubnetPortSet {
			if PodSet.Has(elem) {
				continue
			}
			log.V(1).Info("GC collected Pod", "UID", elem)
			metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteTotal, MetricResTypePod)
			err = r.Service.DeleteSubnetPort(types.UID(elem))
			if err != nil {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypePod)
			} else {
				metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypePod)
			}
		}
	}
}

func updateFail(r *PodReconciler, c *context.Context, o *v1.Pod, e *error) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateFailTotal, MetricResTypePod)
}

func deleteFail(r *PodReconciler, c *context.Context, o *v1.Pod, e *error) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteFailTotal, MetricResTypePod)
}

func updateSuccess(r *PodReconciler, c *context.Context, o *v1.Pod) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerUpdateSuccessTotal, MetricResTypePod)
}

func deleteSuccess(r *PodReconciler, _ *context.Context, _ *v1.Pod) {
	metrics.CounterInc(r.Service.NSXConfig, metrics.ControllerDeleteSuccessTotal, MetricResTypePod)
}

func (r *PodReconciler) GetSubnetPathForPod(ctx context.Context, pod *v1.Pod) (string, error) {
	subnetPath := r.Service.GetSubnetPathForSubnetPortFromStore(string(pod.UID))
	if len(subnetPath) > 0 {
		log.V(1).Info("NSX subnet port had been created, returning the existing NSX subnet path", "pod.UID", pod.UID, "subnetPath", subnetPath)
		return subnetPath, nil
	}
	subnetSet, err := common.GetDefaultSubnetSet(r.Service.Client, ctx, pod.Namespace)
	if err != nil {
		return "", err
	}
	log.Info("got default subnetset for pod, allocating the NSX subnet", "subnetSet.Name", subnetSet.Name, "subnetSet.UID", subnetSet.UID, "pod.Name", pod.Name, "pod.UID", pod.UID)
	subnetPath, err = common.AllocateSubnetFromSubnetSet(subnetSet)
	if err != nil {
		return subnetPath, err
	}
	log.Info("allocated NSX subnet for pod", "nsxSubnetPath", subnetPath, "pod.Name", pod.Name, "pod.UID", pod.UID)
	return subnetPath, nil
}
