/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package networkpolicy

import (
	"context"
	"reflect"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/securitypolicy"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// EnqueueRequestForPod handles Pod events and triggers NetworkPolicy reconciliation
// when Pods with named ports are created, updated, or deleted.
type EnqueueRequestForPod struct {
	Client                  client.Client
	NetworkPolicyReconciler *NetworkPolicyReconciler
}

func (e *EnqueueRequestForPod) Create(_ context.Context, createEvent event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	e.Raw(createEvent, q)
}

func (e *EnqueueRequestForPod) Update(_ context.Context, updateEvent event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	e.Raw(updateEvent, q)
}

func (e *EnqueueRequestForPod) Delete(_ context.Context, deleteEvent event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	e.Raw(deleteEvent, q)
}

func (e *EnqueueRequestForPod) Generic(_ context.Context, genericEvent event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	e.Raw(genericEvent, q)
}

func (e *EnqueueRequestForPod) Raw(evt interface{}, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	var obj client.Object

	switch et := evt.(type) {
	case event.CreateEvent:
		obj = et.Object.(*v1.Pod)
	case event.UpdateEvent:
		obj = et.ObjectNew.(*v1.Pod)
	case event.DeleteEvent:
		obj = et.Object.(*v1.Pod)
	case event.GenericEvent:
		obj = et.Object.(*v1.Pod)
	default:
		log.Error(nil, "Unknown event type", "event", evt)
		return
	}

	pod := obj.(*v1.Pod)
	vpcMode := securitypolicy.IsVPCEnabled(e.NetworkPolicyReconciler.Service)
	if isInSysNs, err := util.IsSystemNamespace(e.Client, pod.Namespace, nil, vpcMode); err != nil {
		log.Error(err, "Failed to fetch namespace", "namespace", pod.Namespace)
		return
	} else if isInSysNs {
		log.Trace("POD is in system namespace, do nothing")
		return
	}
	err := reconcileNetworkPolicy(e.Client, q)
	if err != nil {
		log.Error(err, "Failed to reconcile network policy")
	}
}

// PredicateFuncsPod filters Pod events for NetworkPolicy controller
var PredicateFuncsPod = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		if p, ok := e.Object.(*v1.Pod); ok {
			log.Debug("Receive pod create event", "namespace", p.Namespace, "name", p.Name)
			return util.CheckPodHasNamedPort(*p, "create")
		}
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*v1.Pod)
		newObj := e.ObjectNew.(*v1.Pod)
		log.Debug("Receive pod update event", "namespace", oldObj.Namespace, "name", oldObj.Name)
		// The NSX operator should handle the case when the pod phase is changed from Pending to Running.
		if reflect.DeepEqual(oldObj.ObjectMeta.Labels, newObj.ObjectMeta.Labels) && oldObj.Status.Phase == newObj.Status.Phase {
			log.Debug("POD label and phase are not changed, ignore it", "name", oldObj.Name)
			return false
		}
		if util.CheckPodHasNamedPort(*oldObj, "update") {
			return true
		}
		if util.CheckPodHasNamedPort(*newObj, "update") {
			return true
		}
		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		if p, ok := e.Object.(*v1.Pod); ok {
			log.Debug("Receive pod delete event", "namespace", p.Namespace, "name", p.Name)
			return util.CheckPodHasNamedPort(*p, "delete")
		}
		return false
	},
}
