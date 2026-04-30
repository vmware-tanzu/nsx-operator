/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package statefulset

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	subnetportservice "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/subnetport"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

var (
	log                      = logger.Log
	MetricResTypeStatefulSet = common.MetricResTypeStatefulSet
)

// stsSubnetPortPendingRequeueAfter is used when StatefulSet-related subnet ports cannot be released yet
// because the backing Pod is still running (scale-down/delete path). Requeue instead of relying only on GC.
const stsSubnetPortPendingRequeueAfter = 10 * time.Second

type releaseSubnetPortOutcome int

const (
	releaseSubnetPortNoop releaseSubnetPortOutcome = iota
	releaseSubnetPortReleased
	releaseSubnetPortSkippedRunningPod
)

// StatefulSetReconciler reconciles a StatefulSet object
type StatefulSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	SubnetPortService *subnetportservice.SubnetPortService
	Recorder          record.EventRecorder
	StatusUpdater     common.StatusUpdater
}

// StatefulSetPodFeatureEnabled reports whether the StatefulSet pod SubnetPort feature is active (NSX 9.2.0+ and vpc_wcp_enhance=true in config).
func (r *StatefulSetReconciler) StatefulSetPodFeatureEnabled() bool {
	return nsx.StatefulSetPodSubnetPortFeatureEnabled(r.SubnetPortService.NSXClient, r.SubnetPortService.NSXConfig)
}

func (r *StatefulSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Info("Reconciling StatefulSet", "StatefulSet", req.NamespacedName)
	startTime := time.Now()
	defer func() {
		log.Info("Finished reconciling StatefulSet", "StatefulSet", req.NamespacedName, "duration", time.Since(startTime))
	}()

	r.StatusUpdater.IncreaseSyncTotal()
	if !r.StatefulSetPodFeatureEnabled() {
		log.Debug("StatefulSet pod NSX feature disabled; skipping reconcile (pod controller owns SubnetPort lifecycle)",
			"StatefulSet", req.NamespacedName)
		return common.ResultNormal, nil
	}

	sts := &appsv1.StatefulSet{}
	if err := r.Client.Get(ctx, req.NamespacedName, sts); err != nil {
		if apierrors.IsNotFound(err) {
			return r.processDelete(ctx, req.NamespacedName, nil)
		}
		log.Error(err, "Unable to fetch StatefulSet", "StatefulSet", req.NamespacedName)
		return common.ResultRequeue, err
	}
	if sts.DeletionTimestamp != nil {
		return r.processDelete(ctx, req.NamespacedName, sts)
	}
	// Handle replica changes
	replicaResult, err := r.handleReplicaChange(ctx, sts)
	if err != nil {
		log.Error(err, "Failed to handle replica change", "StatefulSet", req.NamespacedName)
		r.StatusUpdater.UpdateFail(ctx, sts, err, "", nil)
		return common.ResultRequeue, err
	}

	r.StatusUpdater.UpdateSuccess(ctx, sts, nil)
	if replicaResult.RequeueAfter > 0 {
		return ctrl.Result{RequeueAfter: replicaResult.RequeueAfter}, nil
	}
	return common.ResultNormal, nil
}

func (r *StatefulSetReconciler) processDelete(ctx context.Context, namespacedName types.NamespacedName, sts *appsv1.StatefulSet) (ctrl.Result, error) {
	pendingRunningPod, err := r.releaseSubnetPortsForStatefulSet(ctx, namespacedName.Namespace, namespacedName.Name)
	if err != nil {
		// StatusUpdater is a struct, not a pointer/interface. So we check if its Client field is initialized.
		if r.StatusUpdater.Client != nil {
			r.StatusUpdater.DeleteFail(namespacedName, sts, err)
		}
		return common.ResultRequeue, err
	}
	if r.StatusUpdater.Client != nil {
		r.StatusUpdater.DeleteSuccess(namespacedName, sts)
	}
	if pendingRunningPod {
		return ctrl.Result{RequeueAfter: stsSubnetPortPendingRequeueAfter}, nil
	}
	return common.ResultNormal, nil
}

func (r *StatefulSetReconciler) handleReplicaChange(ctx context.Context, sts *appsv1.StatefulSet) (ctrl.Result, error) {
	start, end := r.GetOrdinalRange(sts)
	log.Info("Checking replicas range", "start", start, "end", end, "stsUID", sts.UID, "namespace", sts.Namespace)

	existingPorts := r.SubnetPortService.ListSubnetPortByStsUid(sts.Namespace, string(sts.UID))

	pendingRunningPod := false
	for _, port := range existingPorts {
		podName := util.FindTag(port.Tags, servicecommon.TagScopePodName)
		if podName == "" {
			continue
		}
		idx := stsPodOrdinalFromPort(port)
		if idx == -1 {
			continue
		}
		if idx < start || idx > end {
			outcome, err := r.releaseSubnetPortForPod(ctx, sts.Namespace, podName)
			if err != nil {
				return ctrl.Result{}, err
			}
			if outcome == releaseSubnetPortSkippedRunningPod {
				pendingRunningPod = true
			}
			if outcome == releaseSubnetPortReleased {
				log.Info("Released out-of-range statefulset subnet port", "index", idx, "port", *port.Id)
			}
		}
	}
	if pendingRunningPod {
		return ctrl.Result{RequeueAfter: stsSubnetPortPendingRequeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

func (r *StatefulSetReconciler) releaseSubnetPortsForStatefulSet(ctx context.Context, namespace, name string) (pendingRunningPod bool, err error) {
	log.Info("Releasing all subnet ports for StatefulSet", "StatefulSet", namespace+"/"+name)

	subnetPorts := r.SubnetPortService.ListSubnetPortByStsName(namespace, name)

	var errList []error
	for _, subnetPort := range subnetPorts {
		podName := util.FindTag(subnetPort.Tags, servicecommon.TagScopePodName)
		if podName != "" {
			pod := &corev1.Pod{}
			if err := r.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: podName}, pod); err == nil {
				podUid := util.FindTag(subnetPort.Tags, servicecommon.TagScopePodUID)
				if pod.UID != types.UID(podUid) {
					log.Info("Pod UID mismatch, deleting subnet port ", "pod", podName, "podUID", pod.UID, "subnetPortUID", podUid)
				} else if !common.PodIsDeleted(pod) {
					log.Info("Pod still exists, skipping subnet port deletion", "pod", podName)
					pendingRunningPod = true
					continue
				}
			}
		}

		if err := r.SubnetPortService.DeleteSubnetPort(subnetPort); err != nil {
			log.Error(err, "Failed to delete subnet port",
				"subnetPort", *subnetPort.Id,
				"StatefulSet", namespace+"/"+name)
			errList = append(errList, err)
		} else {
			log.Info("Successfully deleted subnet port for StatefulSet",
				"subnetPort", *subnetPort.Id,
				"StatefulSet", namespace+"/"+name)
		}
	}

	if len(errList) > 0 {
		return pendingRunningPod, fmt.Errorf("errors found in releasing subnet ports: %v", errList)
	}

	return pendingRunningPod, nil
}

func (r *StatefulSetReconciler) releaseSubnetPortForPod(ctx context.Context, namespace, podName string) (releaseSubnetPortOutcome, error) {
	log.Info("Releasing subnet port for pod", "pod", podName, "namespace", namespace)

	pod := &corev1.Pod{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: podName}, pod); err != nil {
		if apierrors.IsNotFound(err) {
			log.Debug("Pod does not exist, releasing subnet port", "pod", podName, "namespace", namespace)
		} else {
			log.Error(err, "Failed to get pod", "pod", podName, "namespace", namespace)
			return releaseSubnetPortNoop, err
		}
	} else if !common.PodIsDeleted(pod) {
		log.Debug("Pod still exists, skipping subnet port release", "pod", podName, "namespace", namespace, "podPhase", pod.Status.Phase)
		return releaseSubnetPortSkippedRunningPod, nil
	}

	targetPorts := r.SubnetPortService.ListSubnetPortByPodName(namespace, podName)
	if len(targetPorts) == 0 {
		log.Debug("No subnet port found for pod", "pod", podName)
		return releaseSubnetPortNoop, nil
	}

	for _, targetPort := range targetPorts {
		if err := r.SubnetPortService.DeleteSubnetPort(targetPort); err != nil {
			log.Error(err, "Failed to delete subnet port for pod", "pod", podName)
			return releaseSubnetPortNoop, err
		}
	}

	log.Info("Successfully released subnet port for pod", "pod", podName)
	return releaseSubnetPortReleased, nil
}

// stsPodOrdinalFromPort returns the StatefulSet pod ordinal from NSX port tags.
// It prefers tag apps.kubernetes.io/pod-index when present (e.g. synced from the Pod label).
// If that tag is missing or invalid, it falls back to parsing the pod name (legacy ports).
func stsPodOrdinalFromPort(port *model.VpcSubnetPort) int {
	if port == nil {
		return -1
	}
	if s := util.FindTag(port.Tags, servicecommon.TagScopePodIndex); s != "" {
		if i, err := strconv.Atoi(s); err == nil {
			return i
		}
	}
	podName := util.FindTag(port.Tags, servicecommon.TagScopePodName)
	if podName == "" {
		return -1
	}
	return parseIndexFromPodName(podName)
}

// parseIndexFromPodName returns the ordinal from default StatefulSet pod naming
// (<name>-<ordinal>, ordinal in the last '-' segment). Returns -1 if not parseable.
func parseIndexFromPodName(podName string) int {
	lastDashIndex := strings.LastIndex(podName, "-")
	if lastDashIndex == -1 {
		return -1
	}
	indexStr := podName[lastDashIndex+1:]
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		return -1
	}
	return index
}

// CollectGarbage collects StatefulSet subnet ports which have been removed
func (r *StatefulSetReconciler) CollectGarbage(ctx context.Context) error {
	if !r.StatefulSetPodFeatureEnabled() {
		return nil
	}
	log.Info("StatefulSet garbage collector started")
	statefulSetList := &appsv1.StatefulSetList{}
	err := r.Client.List(ctx, statefulSetList)
	if err != nil {
		log.Error(err, "Failed to list StatefulSet")
		return err
	}

	var errList []error
	statefulSetUIDs := sets.New[string]()
	for _, sts := range statefulSetList.Items {
		existingPorts := r.SubnetPortService.SubnetPortStore.GetByIndex(servicecommon.TagScopeStatefulSetUID, string(sts.UID))
		start, end := r.GetOrdinalRange(&sts)
		log.Debug("StatefulSet garbage collector", "stsUID", sts.UID, "namespace", sts.Namespace, "start", start, "end", end)
		for _, port := range existingPorts {
			podName := util.FindTag(port.Tags, servicecommon.TagScopePodName)
			if podName == "" {
				continue
			}
			idx := stsPodOrdinalFromPort(port)
			// Match handleReplicaChange: do not treat unparseable ordinals as out-of-range.
			if idx == -1 {
				continue
			}
			if idx < start || idx > end {
				if podName != "" {
					pod := &corev1.Pod{}
					if err := r.Client.Get(ctx, types.NamespacedName{Namespace: sts.Namespace, Name: podName}, pod); err == nil && !common.PodIsDeleted(pod) {
						log.Debug("GC: pod still exists, skipping port deletion", "pod", podName)
						continue
					}
				}
				log.Info("StatefulSet garbage collector: found out-of-range port", "index", idx, "stsUID", sts.UID, "start", start, "end", end, "namespace", sts.Namespace)
				if err := r.SubnetPortService.DeleteSubnetPort(port); err != nil {
					log.Error(err, "GC: failed to delete out-of-range subnet port", "port", *port.Id, "stsUID", sts.UID, "namespace", sts.Namespace)
					errList = append(errList, err)
				}
			}
		}
		statefulSetUIDs.Insert(string(sts.UID))
	}

	existingPorts := r.SubnetPortService.SubnetPortStore.GetByIndex(servicecommon.IndexKeyAllStsPorts, servicecommon.StsPortBucket)
	for _, port := range existingPorts {
		stsID := util.FindTag(port.Tags, servicecommon.TagScopeStatefulSetUID)
		if !statefulSetUIDs.Has(stsID) {
			podName := util.FindTag(port.Tags, servicecommon.TagScopePodName)
			namespace := util.FindTag(port.Tags, servicecommon.TagScopeNamespace)
			if podName != "" && namespace != "" {
				pod := &corev1.Pod{}
				if err := r.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: podName}, pod); err == nil && !common.PodIsDeleted(pod) {
					log.Debug("GC: pod still exists, skipping orphaned port deletion", "pod", podName)
					continue
				}
			}
			log.Debug("Found orphaned subnet port for deleted StatefulSet", "port", *port.Id, "stsUID", stsID)
			if err := r.SubnetPortService.DeleteSubnetPort(port); err != nil {
				log.Error(err, "GC: failed to delete orphaned subnet port", "port", *port.Id, "stsUID", stsID)
				errList = append(errList, err)
			}
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("StatefulSet GC: %d delete error(s): %v", len(errList), errList)
	}
	return nil
}

func (r *StatefulSetReconciler) GetOrdinalRange(sts *appsv1.StatefulSet) (int, int) {
	start := 0
	if sts.Spec.Ordinals != nil {
		// K8s 1.31+ feature, start index can be specified by users, default is 0
		start = int(sts.Spec.Ordinals.Start)
	}

	replicas := 0
	if sts.Spec.Replicas != nil {
		replicas = int(*sts.Spec.Replicas)
	}

	if replicas == 0 {
		// No desired pods: empty ordinal range. Callers treat any idx>=0 as out-of-range
		// so handleReplicaChange/GC release all STS-tagged ports for this StatefulSet.
		return -1, -1
	}

	return start, start + replicas - 1
}

// PredicateFuncsForStatefulSet limits which StatefulSet watch events reach the reconciler.
//
// The reconciler mainly needs to release NSX resources when desired pod ordinals drop out
// of the active range (scale down or ordinals start moved up). Updates are therefore admitted
// only when the inclusive ordinal window [start, start+replicas-1] shrinks: either the start
// moves right (newStart > oldStart) or the end moves left (newEnd < oldEnd). Other updates,
// including scale-up and unchanged replica/ordinal fields, are ignored.
//
// Create and generic events are ignored; subnet wiring for new pods is handled via Pod
// reconciliation. Delete events are always admitted so the controller can tear down
// remaining resources for the StatefulSet.
var PredicateFuncsForStatefulSet = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldSts, ok1 := e.ObjectOld.(*appsv1.StatefulSet)
		newSts, ok2 := e.ObjectNew.(*appsv1.StatefulSet)
		if !ok1 || !ok2 {
			log.Error(fmt.Errorf("type assertion failed"), "Failed to cast to StatefulSet in update event")
			return false
		}

		oldStart := int32(0)
		if oldSts.Spec.Ordinals != nil {
			oldStart = oldSts.Spec.Ordinals.Start
		}
		newStart := int32(0)
		if newSts.Spec.Ordinals != nil {
			newStart = newSts.Spec.Ordinals.Start
		}

		// If either has nil replicas, don't trigger.
		if oldSts.Spec.Replicas == nil || newSts.Spec.Replicas == nil {
			return false
		}

		oldRepl := *oldSts.Spec.Replicas
		oldEnd := oldStart + oldRepl - 1

		newRepl := *newSts.Spec.Replicas
		newEnd := newStart + newRepl - 1

		// Shrink of desired ordinal range: dropped lower indices and/or dropped upper indices.
		if newStart > oldStart || newEnd < oldEnd {
			log.Debug("StatefulSet update event received",
				"name", oldSts.Name,
				"namespace", oldSts.Namespace,
				"oldReplicas", oldSts.Spec.Replicas,
				"newReplicas", newSts.Spec.Replicas,
				"oldOrdinalsStart", oldStart,
				"newOrdinalsStart", newStart)
			return true
		}
		return false
	},
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return true
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return false
	},
}

// setupWithManager sets up the controller with the Manager.
func (r *StatefulSetReconciler) setupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.StatefulSet{}).
		WithEventFilter(PredicateFuncsForStatefulSet).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.NumReconcile(),
		}).
		Complete(r)
}

func (r *StatefulSetReconciler) StartController(mgr ctrl.Manager, _ webhook.Server) error {
	if err := r.setupWithManager(mgr); err != nil {
		log.Error(err, "failed to create controller", "controller", "StatefulSet")
		return err
	}
	go common.GenericGarbageCollector(make(chan bool), servicecommon.GCInterval, r.CollectGarbage)
	return nil
}

func NewStatefulSetReconciler(mgr ctrl.Manager, subnetPortService *subnetportservice.SubnetPortService) *StatefulSetReconciler {
	log.Debug("New StatefulSet Reconciler")
	reconciler := &StatefulSetReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		SubnetPortService: subnetPortService,
		Recorder:          mgr.GetEventRecorderFor("statefulset-controller"), //nolint:staticcheck // record.EventRecorder; StatusUpdater not on events.EventRecorder yet
	}
	reconciler.StatusUpdater = common.NewStatusUpdater(reconciler.Client, reconciler.SubnetPortService.NSXConfig, reconciler.Recorder, MetricResTypeStatefulSet, "SubnetPort", "StatefulSet")
	return reconciler
}

// Start starts the controller
func (r *StatefulSetReconciler) Start(mgr ctrl.Manager) error {
	return r.setupWithManager(mgr)
}

// RestoreReconcile implements ReconcilerProvider interface
func (r *StatefulSetReconciler) RestoreReconcile() error {
	// StatefulSet controller doesn't need restore reconcile for now
	return nil
}
