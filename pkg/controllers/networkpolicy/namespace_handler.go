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

	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// EnqueueRequestForNamespace handles Namespace events and triggers NetworkPolicy reconciliation
// when Namespace labels change and there are pods in the namespace.
type EnqueueRequestForNamespace struct {
	Client                  client.Client
	NetworkPolicyReconciler *NetworkPolicyReconciler
}

func (e *EnqueueRequestForNamespace) Create(_ context.Context, _ event.CreateEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.Debug("Namespace create event, do nothing")
}

func (e *EnqueueRequestForNamespace) Delete(_ context.Context, _ event.DeleteEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.Debug("Namespace delete event, do nothing")
}

func (e *EnqueueRequestForNamespace) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	log.Debug("Namespace generic event, do nothing")
}

func (e *EnqueueRequestForNamespace) Update(_ context.Context, updateEvent event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	obj := updateEvent.ObjectNew.(*v1.Namespace)
	// NetworkPolicy is only supported in VPC mode, so vpcMode is always true
	if isInSysNs, err := util.IsSystemNamespace(nil, "", obj, true); err != nil {
		log.Error(err, "Failed to fetch namespace", "namespace", obj.Name)
		return
	} else if isInSysNs {
		log.Trace("Namespace is in system namespace, ignore it", "namespace", obj.Name)
		return
	}

	podList := &v1.PodList{}
	err := e.Client.List(context.Background(), podList, client.InNamespace(obj.Name))
	if err != nil {
		log.Error(err, "Failed to list pod in namespace", "namespace", obj.Name)
		return
	}

	if len(podList.Items) == 0 {
		log.Info("No pods in namespace", "namespace", obj.Name)
		return
	}

	shouldReconcile := false
	for _, pod := range podList.Items {
		if util.CheckPodHasNamedPort(pod, "update") {
			shouldReconcile = true
			break
		}
	}
	if !shouldReconcile {
		log.Info("No pod in namespace has named ports", "namespace", obj.Name)
		return
	}

	err = reconcileNetworkPolicy(e.Client, q)
	if err != nil {
		log.Error(err, "Failed to reconcile network policy for namespace label change")
	}
}

// PredicateFuncsNs filters Namespace events for NetworkPolicy controller
var PredicateFuncsNs = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldObj := e.ObjectOld.(*v1.Namespace)
		newObj := e.ObjectNew.(*v1.Namespace)
		log.Debug("Receive namespace update event", "name", oldObj.Name)
		if reflect.DeepEqual(oldObj.ObjectMeta.Labels, newObj.ObjectMeta.Labels) {
			log.Info("Label of namespace is not changed, ignore it", "name", oldObj.Name)
			return false
		}
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
}
